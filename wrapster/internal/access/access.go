package access

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nbd-wtf/go-nostr"
	adminauth "github.com/trustroots/nostroots/vibe/wrapster/internal/admin"
	"github.com/trustroots/nostroots/vibe/wrapster/internal/nip05"
)

const (
	RuleTrustrootsNIP05 = "trustroots_nip05"
	RuleNostrFollow     = "nostr_follow"

	DefaultRelayURL = "wss://nip42.trustroots.org"

	// perRelayTimeout bounds each individual relay lookup so a single slow or
	// auth-gated relay cannot consume the whole access-check budget.
	perRelayTimeout = 4 * time.Second

	TrustrootsProfileKind            = 10390
	TrustrootsUsernameLabelNamespace = "org.trustroots:username"
	NIP02FollowListKind              = 3
)

var (
	ErrNoRule            = errors.New("access rule is not configured")
	ErrUnsupportedRule   = errors.New("access rule type is unsupported")
	ErrDenied            = errors.New("pubkey is denied by access rule")
	ErrNotAllowed        = errors.New("pubkey is not allowed by access rule")
	ErrNoTrustrootsName  = errors.New("no Trustroots username profile event found")
	ErrNoFollowList      = errors.New("no NIP-02 follow list found")
	ErrInvalidFollowRule = errors.New("nostr follow rule must use owner_follows_user")
)

// PublicProfileRelays are publicly readable relays that carry Trustroots
// ecosystem profile (kind 10390/0) and follow-list (kind 3) events. They are
// always appended as fallbacks so the access check still works when the
// deployment's configured relays require NIP-42 auth and close anonymous
// subscriptions.
var PublicProfileRelays = []string{
	"wss://relay.trustroots.org",
	"wss://relay.nomadwiki.org",
}

type Rule struct {
	Type         string
	RelayURL     string
	RelayURLs    []string
	NIP05BaseURL string
	OwnerPubkey  string
	Relationship string
	DenyPubkeys  map[string]struct{}
}

type RelayConn interface {
	WriteJSON(any) error
	ReadMessage() (int, []byte, error)
	SetReadDeadline(time.Time) error
	Close() error
}

type Authorizer struct {
	Rules              map[string]Rule
	MaxAge             time.Duration
	Now                func() time.Time
	HTTPClient         *http.Client
	DialURL            func(context.Context, string) (RelayConn, error)
	TrustrootsVerifier func(context.Context, Rule, string) error
	FollowVerifier     func(context.Context, Rule, string) error
}

func (a Authorizer) VerifyRequest(r *http.Request, ruleName string) (string, error) {
	return a.verifyAnyRequest(r, []string{ruleName})
}

func (a Authorizer) VerifyAnyRequest(r *http.Request, ruleNames []string) (string, error) {
	return a.verifyAnyRequest(r, ruleNames)
}

func (a Authorizer) VerifyAllRequest(r *http.Request, ruleNames []string) (string, error) {
	return a.verifyAllRequest(r, ruleNames)
}

func (a Authorizer) CheckPubkey(ctx context.Context, ruleName, pubkey string) error {
	return a.checkRule(ctx, ruleName, strings.ToLower(strings.TrimSpace(pubkey)))
}

func (a Authorizer) verifiedPubkey(r *http.Request) (string, error) {
	event, err := adminauth.EventFromAuthorization(r.Header.Get("Authorization"))
	if err != nil {
		return "", err
	}
	return adminauth.VerifyNIP98Event(event, adminauth.AbsoluteRequestURL(r), r.Method, a.MaxAge, a.Now)
}

func (a Authorizer) verifyAnyRequest(r *http.Request, ruleNames []string) (string, error) {
	pubkey, err := a.verifiedPubkey(r)
	if err != nil {
		return "", err
	}
	if len(compactRuleNames(ruleNames)) == 0 {
		return "", ErrNoRule
	}
	var lastErr error
	for _, ruleName := range compactRuleNames(ruleNames) {
		if err := a.checkRule(r.Context(), ruleName, pubkey); err == nil {
			return pubkey, nil
		} else {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = ErrNoRule
	}
	return "", lastErr
}

func (a Authorizer) verifyAllRequest(r *http.Request, ruleNames []string) (string, error) {
	ruleNames = compactRuleNames(ruleNames)
	if len(ruleNames) == 0 {
		return "", ErrNoRule
	}
	pubkey, err := a.verifiedPubkey(r)
	if err != nil {
		return "", err
	}
	for _, ruleName := range ruleNames {
		if err := a.checkRule(r.Context(), ruleName, pubkey); err != nil {
			return "", err
		}
	}
	return pubkey, nil
}

func compactRuleNames(ruleNames []string) []string {
	out := make([]string, 0, len(ruleNames))
	for _, ruleName := range ruleNames {
		if ruleName = strings.TrimSpace(ruleName); ruleName != "" {
			out = append(out, ruleName)
		}
	}
	return out
}

func (a Authorizer) checkRule(ctx context.Context, ruleName, pubkey string) error {
	rule, ok := a.Rules[ruleName]
	if !ok {
		return ErrNoRule
	}
	if _, denied := rule.DenyPubkeys[strings.ToLower(pubkey)]; denied {
		return ErrDenied
	}
	switch rule.Type {
	case RuleTrustrootsNIP05:
		if a.TrustrootsVerifier != nil {
			return a.TrustrootsVerifier(ctx, rule, pubkey)
		}
		return a.checkTrustrootsNIP05(ctx, rule, pubkey)
	case RuleNostrFollow:
		if rule.Relationship != "" && rule.Relationship != "owner_follows_user" {
			return ErrInvalidFollowRule
		}
		if a.FollowVerifier != nil {
			return a.FollowVerifier(ctx, rule, pubkey)
		}
		return a.checkOwnerFollows(ctx, rule, pubkey)
	default:
		return ErrUnsupportedRule
	}
}

func (a Authorizer) checkTrustrootsNIP05(ctx context.Context, rule Rule, pubkey string) error {
	username, err := a.findTrustrootsUsername(ctx, relayURLsForRule(rule), pubkey)
	if err != nil {
		return err
	}
	baseURL := strings.TrimSpace(rule.NIP05BaseURL)
	if baseURL == "" {
		return fmt.Errorf("trustroots NIP-05 base URL is empty")
	}
	return nip05.Client{BaseURL: baseURL, HTTPClient: a.HTTPClient}.Verify(ctx, username, pubkey)
}

func (a Authorizer) checkOwnerFollows(ctx context.Context, rule Rule, pubkey string) error {
	owner := strings.ToLower(strings.TrimSpace(rule.OwnerPubkey))
	if owner == "" {
		return fmt.Errorf("nostr follow owner pubkey is empty")
	}
	event, err := a.findFollowList(ctx, relayURLsForRule(rule), owner)
	if err != nil {
		return err
	}
	for _, tag := range event.Tags {
		if len(tag) >= 2 && tag[0] == "p" && strings.EqualFold(tag[1], pubkey) {
			return nil
		}
	}
	return ErrNotAllowed
}

func (a Authorizer) findTrustrootsUsername(ctx context.Context, relayURLs []string, pubkey string) (string, error) {
	var lastErr error
	notFound := false
	for _, relayURL := range relayURLs {
		username, err := a.findTrustrootsUsernameOnRelay(ctx, relayURL, pubkey)
		if err == nil {
			return username, nil
		}
		if errors.Is(err, ErrNoTrustrootsName) {
			notFound = true
		}
		lastErr = err
	}
	if notFound {
		return "", ErrNoTrustrootsName
	}
	if lastErr == nil {
		lastErr = ErrNoTrustrootsName
	}
	return "", lastErr
}

func (a Authorizer) findTrustrootsUsernameOnRelay(ctx context.Context, relayURL, pubkey string) (string, error) {
	ctx, cancel := relayContext(ctx)
	defer cancel()
	conn, err := a.dial(ctx, relayURL)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	timeout := deadlineDuration(ctx)
	subID := "wrapster-access-profile"
	if err := conn.WriteJSON([]any{
		"REQ",
		subID,
		map[string]any{
			"kinds":   []int{TrustrootsProfileKind, 0},
			"authors": []string{pubkey},
			"limit":   10,
		},
	}); err != nil {
		return "", err
	}
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	for {
		event, done, err := readSubscriptionEvent(conn, subID)
		if err != nil {
			return "", err
		}
		if done {
			return "", ErrNoTrustrootsName
		}
		if validEventAuthor(event, pubkey) {
			if username, ok := trustrootsUsernameFromEvent(event); ok {
				_ = conn.WriteJSON([]any{"CLOSE", subID})
				return username, nil
			}
		}
	}
}

func (a Authorizer) findFollowList(ctx context.Context, relayURLs []string, ownerPubkey string) (nostr.Event, error) {
	var lastErr error
	noList := false
	for _, relayURL := range relayURLs {
		event, err := a.findFollowListOnRelay(ctx, relayURL, ownerPubkey)
		if err == nil {
			return event, nil
		}
		if errors.Is(err, ErrNoFollowList) {
			noList = true
		}
		lastErr = err
	}
	if noList {
		return nostr.Event{}, ErrNoFollowList
	}
	if lastErr == nil {
		lastErr = ErrNoFollowList
	}
	return nostr.Event{}, lastErr
}

func (a Authorizer) findFollowListOnRelay(ctx context.Context, relayURL, ownerPubkey string) (nostr.Event, error) {
	ctx, cancel := relayContext(ctx)
	defer cancel()
	conn, err := a.dial(ctx, relayURL)
	if err != nil {
		return nostr.Event{}, err
	}
	defer conn.Close()
	timeout := deadlineDuration(ctx)
	subID := "wrapster-access-follow"
	if err := conn.WriteJSON([]any{
		"REQ",
		subID,
		map[string]any{
			"kinds":   []int{NIP02FollowListKind},
			"authors": []string{ownerPubkey},
			"limit":   25,
		},
	}); err != nil {
		return nostr.Event{}, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	var latest nostr.Event
	var latestWithContacts nostr.Event
	var hasLatest bool
	var hasLatestWithContacts bool
	for {
		event, done, err := readSubscriptionEvent(conn, subID)
		if err != nil {
			return nostr.Event{}, err
		}
		if done {
			if hasLatestWithContacts {
				return latestWithContacts, nil
			}
			if hasLatest {
				return latest, nil
			}
			return nostr.Event{}, ErrNoFollowList
		}
		if event.Kind == NIP02FollowListKind && validEventAuthor(event, ownerPubkey) {
			if !hasLatest || event.CreatedAt > latest.CreatedAt {
				latest = event
				hasLatest = true
			}
			if hasPubkeyTags(event) && (!hasLatestWithContacts || event.CreatedAt > latestWithContacts.CreatedAt) {
				latestWithContacts = event
				hasLatestWithContacts = true
			}
		}
	}
}

func hasPubkeyTags(event nostr.Event) bool {
	for _, tag := range event.Tags {
		if len(tag) >= 2 && tag[0] == "p" && strings.TrimSpace(tag[1]) != "" {
			return true
		}
	}
	return false
}

func validEventAuthor(event nostr.Event, pubkey string) bool {
	if !strings.EqualFold(event.PubKey, pubkey) {
		return false
	}
	ok, err := event.CheckSignature()
	return err == nil && ok
}

func (a Authorizer) dial(ctx context.Context, relayURL string) (RelayConn, error) {
	if a.DialURL != nil {
		return a.DialURL(ctx, relayURL)
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, relayURL, nil)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func readSubscriptionEvent(conn RelayConn, subID string) (nostr.Event, bool, error) {
	_, message, err := conn.ReadMessage()
	if err != nil {
		return nostr.Event{}, false, err
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(message, &raw); err != nil || len(raw) == 0 {
		return nostr.Event{}, false, nil
	}
	var typ string
	if err := json.Unmarshal(raw[0], &typ); err != nil {
		return nostr.Event{}, false, nil
	}
	switch typ {
	case "EVENT":
		if len(raw) < 3 {
			return nostr.Event{}, false, nil
		}
		var id string
		if err := json.Unmarshal(raw[1], &id); err != nil || id != subID {
			return nostr.Event{}, false, nil
		}
		var event nostr.Event
		if err := json.Unmarshal(raw[2], &event); err != nil {
			return nostr.Event{}, false, nil
		}
		return event, false, nil
	case "EOSE":
		return nostr.Event{}, true, nil
	case "CLOSED":
		return nostr.Event{}, false, fmt.Errorf("relay closed access subscription")
	default:
		return nostr.Event{}, false, nil
	}
}

func trustrootsUsernameFromEvent(event nostr.Event) (string, bool) {
	if event.Kind == TrustrootsProfileKind {
		for _, tag := range event.Tags {
			if len(tag) >= 3 && tag[0] == "l" && tag[2] == TrustrootsUsernameLabelNamespace {
				return normalizeUsername(tag[1])
			}
		}
	}
	if event.Kind == 0 {
		var profile struct {
			TrustrootsUsername string `json:"trustrootsUsername"`
			NIP05              string `json:"nip05"`
		}
		if err := json.Unmarshal([]byte(event.Content), &profile); err != nil {
			return "", false
		}
		if username, ok := normalizeUsername(profile.TrustrootsUsername); ok {
			return username, true
		}
		if strings.HasSuffix(strings.ToLower(profile.NIP05), "@trustroots.org") {
			return normalizeUsername(strings.TrimSuffix(strings.ToLower(profile.NIP05), "@trustroots.org"))
		}
	}
	return "", false
}

func normalizeUsername(username string) (string, bool) {
	username = strings.ToLower(strings.TrimSpace(username))
	if len([]rune(username)) < 3 {
		return "", false
	}
	return username, true
}

func NormalizePubkey(value string) (string, error) {
	normalized := adminauth.NormalizePubkey(value)
	if normalized == "" {
		return "", nil
	}
	if !nostr.IsValidPublicKeyHex(normalized) {
		return "", fmt.Errorf("pubkey %q is not valid hex or npub", value)
	}
	return normalized, nil
}

func HTTPStatus(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, adminauth.ErrMissingAuthorization),
		errors.Is(err, adminauth.ErrWrongScheme),
		errors.Is(err, adminauth.ErrBadEncoding),
		errors.Is(err, adminauth.ErrWrongKind),
		errors.Is(err, adminauth.ErrBadSignature),
		errors.Is(err, adminauth.ErrStaleEvent),
		errors.Is(err, adminauth.ErrWrongURL),
		errors.Is(err, adminauth.ErrWrongMethod):
		return http.StatusUnauthorized
	default:
		return http.StatusForbidden
	}
}

func relayURL(rule Rule) string {
	if strings.TrimSpace(rule.RelayURL) == "" {
		return DefaultRelayURL
	}
	return strings.TrimSpace(rule.RelayURL)
}

// relayURLsForRule returns the ordered, de-duplicated list of relays to try
// for a rule: the rule's configured relays first, then the public profile
// relays as fallbacks. This makes the access check resilient to configured
// relays that require NIP-42 auth and close anonymous subscriptions.
func relayURLsForRule(rule Rule) []string {
	candidates := make([]string, 0, len(rule.RelayURLs)+len(PublicProfileRelays)+1)
	candidates = append(candidates, rule.RelayURLs...)
	if len(rule.RelayURLs) == 0 {
		candidates = append(candidates, relayURL(rule))
	}
	candidates = append(candidates, PublicProfileRelays...)

	seen := make(map[string]struct{}, len(candidates))
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		trimmed := strings.TrimSpace(candidate)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		out = append(out, DefaultRelayURL)
	}
	return out
}

// relayContext bounds a single relay attempt so one slow or auth-gated relay
// cannot consume the entire access-check budget.
func relayContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := perRelayTimeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 && remaining < timeout {
			timeout = remaining
		}
	}
	return context.WithTimeout(ctx, timeout)
}

func deadlineDuration(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		if d := time.Until(deadline); d > 0 {
			return d
		}
	}
	return 5 * time.Second
}
