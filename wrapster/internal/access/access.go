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
	"github.com/nbd-wtf/go-nostr/nip19"
	adminauth "github.com/trustroots/nostroots/vibe/wrapster/internal/admin"
	"github.com/trustroots/nostroots/vibe/wrapster/internal/nip05"
)

const (
	RuleTrustrootsNIP05 = "trustroots_nip05"
	RuleNostrFollow    = "nostr_follow"

	DefaultRelayURL = "wss://nip42.trustroots.org"

	TrustrootsProfileKind            = 10390
	TrustrootsUsernameLabelNamespace = "org.trustroots:username"
	NIP02FollowListKind              = 3
)

var (
	ErrNoRule             = errors.New("access rule is not configured")
	ErrUnsupportedRule    = errors.New("access rule type is unsupported")
	ErrDenied            = errors.New("pubkey is denied by access rule")
	ErrNotAllowed        = errors.New("pubkey is not allowed by access rule")
	ErrNoTrustrootsName  = errors.New("no Trustroots username profile event found")
	ErrNoFollowList      = errors.New("no NIP-02 follow list found")
	ErrInvalidFollowRule = errors.New("nostr follow rule must use owner_follows_user")
)

type Rule struct {
	Type        string
	RelayURL    string
	NIP05BaseURL string
	OwnerPubkey string
	Relationship string
	DenyPubkeys map[string]struct{}
}

type RelayConn interface {
	WriteJSON(any) error
	ReadMessage() (int, []byte, error)
	SetReadDeadline(time.Time) error
	Close() error
}

type Authorizer struct {
	Rules map[string]Rule
	MaxAge time.Duration
	Now func() time.Time
	HTTPClient *http.Client
	DialURL func(context.Context, string) (RelayConn, error)
	TrustrootsVerifier func(context.Context, Rule, string) error
	FollowVerifier func(context.Context, Rule, string) error
}

func (a Authorizer) VerifyRequest(r *http.Request, ruleName string) (string, error) {
	return a.verifyRequest(r, []string{ruleName})
}

func (a Authorizer) VerifyAnyRequest(r *http.Request, ruleNames []string) (string, error) {
	return a.verifyRequest(r, ruleNames)
}

func (a Authorizer) verifyRequest(r *http.Request, ruleNames []string) (string, error) {
	event, err := adminauth.EventFromAuthorization(r.Header.Get("Authorization"))
	if err != nil {
		return "", err
	}
	pubkey, err := a.verifyNIP98Event(event, adminauth.AbsoluteRequestURL(r), r.Method)
	if err != nil {
		return "", err
	}
	if len(ruleNames) == 0 {
		return "", ErrNoRule
	}
	var lastErr error
	for _, ruleName := range ruleNames {
		if strings.TrimSpace(ruleName) == "" {
			continue
		}
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

func (a Authorizer) verifyNIP98Event(event nostr.Event, requestURL, method string) (string, error) {
	if event.Kind != adminauth.NIP98EventKind {
		return "", adminauth.ErrWrongKind
	}
	ok, err := event.CheckSignature()
	if err != nil {
		return "", fmt.Errorf("%w: %v", adminauth.ErrBadSignature, err)
	}
	if !ok {
		return "", adminauth.ErrBadSignature
	}
	maxAge := a.MaxAge
	if maxAge <= 0 {
		maxAge = time.Minute
	}
	now := time.Now()
	if a.Now != nil {
		now = a.Now()
	}
	createdAt := event.CreatedAt.Time()
	if createdAt.Before(now.Add(-maxAge)) || createdAt.After(now.Add(maxAge)) {
		return "", adminauth.ErrStaleEvent
	}
	if tagValue(event.Tags, "u") != requestURL {
		return "", adminauth.ErrWrongURL
	}
	if !strings.EqualFold(tagValue(event.Tags, "method"), method) {
		return "", adminauth.ErrWrongMethod
	}
	return strings.ToLower(event.PubKey), nil
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
	username, err := a.findTrustrootsUsername(ctx, relayURL(rule), pubkey)
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
	event, err := a.findFollowList(ctx, relayURL(rule), owner)
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

func (a Authorizer) findTrustrootsUsername(ctx context.Context, relayURL, pubkey string) (string, error) {
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
			"kinds": []int{TrustrootsProfileKind, 0},
			"authors": []string{pubkey},
			"limit": 10,
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
		if username, ok := trustrootsUsernameFromEvent(event); ok {
			_ = conn.WriteJSON([]any{"CLOSE", subID})
			return username, nil
		}
	}
}

func (a Authorizer) findFollowList(ctx context.Context, relayURL, ownerPubkey string) (nostr.Event, error) {
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
			"kinds": []int{NIP02FollowListKind},
			"authors": []string{ownerPubkey},
			"limit": 1,
		},
	}); err != nil {
		return nostr.Event{}, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	for {
		event, done, err := readSubscriptionEvent(conn, subID)
		if err != nil {
			return nostr.Event{}, err
		}
		if done {
			return nostr.Event{}, ErrNoFollowList
		}
		if event.Kind == NIP02FollowListKind {
			_ = conn.WriteJSON([]any{"CLOSE", subID})
			return event, nil
		}
	}
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
			NIP05 string `json:"nip05"`
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
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", nil
	}
	if strings.HasPrefix(value, "npub") {
		prefix, decoded, err := nip19.Decode(value)
		if err != nil {
			return "", err
		}
		if prefix != "npub" {
			return "", fmt.Errorf("expected npub, got %s", prefix)
		}
		pubkey, ok := decoded.(string)
		if !ok {
			return "", fmt.Errorf("decoded npub was not a pubkey")
		}
		value = strings.ToLower(pubkey)
	}
	if !nostr.IsValidPublicKeyHex(value) {
		return "", fmt.Errorf("pubkey %q is not valid hex or npub", value)
	}
	return value, nil
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

func deadlineDuration(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		if d := time.Until(deadline); d > 0 {
			return d
		}
	}
	return 5 * time.Second
}

func tagValue(tags nostr.Tags, name string) string {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == name {
			return tag[1]
		}
	}
	return ""
}
