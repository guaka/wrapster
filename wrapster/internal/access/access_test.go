package access

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

func TestOwnerFollowListAllowsRequester(t *testing.T) {
	ownerKey := nostr.GeneratePrivateKey()
	ownerPubkey := mustPubkey(t, ownerKey)
	requesterKey := nostr.GeneratePrivateKey()
	requesterPubkey := mustPubkey(t, requesterKey)
	followList := signedFollowList(t, ownerKey, nostr.Tags{{"p", requesterPubkey, "wss://relay.example"}})
	now := time.Unix(1700000000, 0)
	req := signedRequest(t, requesterKey, "http://wrapster.test/media/api/services/jellyfin/search?q=matrix", now)

	authz := Authorizer{
		Rules: map[string]Rule{
			"media_owner_follows": {
				Type:         RuleNostrFollow,
				RelayURL:     "wss://nip42.trustroots.org",
				OwnerPubkey:  ownerPubkey,
				Relationship: "owner_follows_user",
			},
		},
		MaxAge: time.Minute,
		Now:    func() time.Time { return now },
		DialURL: func(_ context.Context, relayURL string) (RelayConn, error) {
			if relayURL != "wss://nip42.trustroots.org" {
				t.Fatalf("relayURL = %q", relayURL)
			}
			return newFakeRelayConn("wrapster-access-follow", followList), nil
		},
	}

	pubkey, err := authz.VerifyRequest(req, "media_owner_follows")
	if err != nil {
		t.Fatal(err)
	}
	if pubkey != requesterPubkey {
		t.Fatalf("pubkey = %q, want %q", pubkey, requesterPubkey)
	}
}

func TestOwnerFollowListDeniesNonFollowedRequester(t *testing.T) {
	ownerKey := nostr.GeneratePrivateKey()
	ownerPubkey := mustPubkey(t, ownerKey)
	requesterKey := nostr.GeneratePrivateKey()
	otherPubkey := mustPubkey(t, nostr.GeneratePrivateKey())
	followList := signedFollowList(t, ownerKey, nostr.Tags{{"p", otherPubkey, "wss://relay.example"}})
	now := time.Unix(1700000000, 0)
	req := signedRequest(t, requesterKey, "http://wrapster.test/media/api/services/plex/search?q=matrix", now)

	authz := Authorizer{
		Rules: map[string]Rule{
			"media_owner_follows": {
				Type:        RuleNostrFollow,
				RelayURL:    "wss://nip42.trustroots.org",
				OwnerPubkey: ownerPubkey,
			},
		},
		MaxAge: time.Minute,
		Now:    func() time.Time { return now },
		DialURL: func(context.Context, string) (RelayConn, error) {
			return newFakeRelayConn("wrapster-access-follow", followList), nil
		},
	}

	if _, err := authz.VerifyRequest(req, "media_owner_follows"); err != ErrNotAllowed {
		t.Fatalf("VerifyRequest error = %v, want %v", err, ErrNotAllowed)
	}
}

func TestVerifyAllRequestRequiresEveryRule(t *testing.T) {
	key := nostr.GeneratePrivateKey()
	pubkey := mustPubkey(t, key)
	now := time.Unix(1700000000, 0)
	req := signedRequest(t, key, "http://wrapster.test/media/api/services/jellyfin/search?q=matrix", now)
	authz := Authorizer{
		Rules: map[string]Rule{
			"trustroots_nip05":    {Type: RuleTrustrootsNIP05},
			"media_owner_follows": {Type: RuleNostrFollow, OwnerPubkey: pubkey},
		},
		MaxAge: time.Minute,
		Now:    func() time.Time { return now },
		TrustrootsVerifier: func(_ context.Context, _ Rule, gotPubkey string) error {
			if gotPubkey != pubkey {
				t.Fatalf("pubkey = %q, want %q", gotPubkey, pubkey)
			}
			return nil
		},
		FollowVerifier: func(context.Context, Rule, string) error {
			return ErrNotAllowed
		},
	}

	_, err := authz.VerifyAllRequest(req, []string{"trustroots_nip05", "media_owner_follows"})
	if err != ErrNotAllowed {
		t.Fatalf("VerifyAllRequest error = %v, want %v", err, ErrNotAllowed)
	}

	authz.FollowVerifier = func(_ context.Context, _ Rule, gotPubkey string) error {
		if gotPubkey != pubkey {
			t.Fatalf("pubkey = %q, want %q", gotPubkey, pubkey)
		}
		return nil
	}
	gotPubkey, err := authz.VerifyAllRequest(req, []string{"trustroots_nip05", "media_owner_follows"})
	if err != nil {
		t.Fatal(err)
	}
	if gotPubkey != pubkey {
		t.Fatalf("pubkey = %q, want %q", gotPubkey, pubkey)
	}
}

func TestFindTrustrootsUsernameFallsBackWhenFirstRelayCloses(t *testing.T) {
	userKey := nostr.GeneratePrivateKey()
	userPubkey := mustPubkey(t, userKey)
	profile := signedTrustrootsProfile(t, userKey, "alice")

	var dialed []string
	authz := Authorizer{
		MaxAge: time.Minute,
		DialURL: func(_ context.Context, relayURL string) (RelayConn, error) {
			dialed = append(dialed, relayURL)
			if relayURL == "wss://nip42.trustroots.org" {
				return newFakeClosedRelayConn(), nil
			}
			return newFakeRelayConn("wrapster-access-profile", profile), nil
		},
	}

	username, err := authz.findTrustrootsUsername(
		context.Background(),
		relayURLsForRule(Rule{RelayURL: "wss://nip42.trustroots.org"}),
		userPubkey,
	)
	if err != nil {
		t.Fatalf("findTrustrootsUsername returned error: %v", err)
	}
	if username != "alice" {
		t.Fatalf("username = %q, want %q", username, "alice")
	}
	if len(dialed) < 2 || dialed[0] != "wss://nip42.trustroots.org" {
		t.Fatalf("expected fallback after first relay closed, dialed = %v", dialed)
	}
}

func TestFindTrustrootsUsernamePropagatesCloseWhenAllRelaysClose(t *testing.T) {
	authz := Authorizer{
		MaxAge: time.Minute,
		DialURL: func(context.Context, string) (RelayConn, error) {
			return newFakeClosedRelayConn(), nil
		},
	}

	_, err := authz.findTrustrootsUsername(
		context.Background(),
		[]string{"wss://a.example", "wss://b.example"},
		mustPubkey(t, nostr.GeneratePrivateKey()),
	)
	if err == nil {
		t.Fatal("expected error when every relay closes the subscription")
	}
	if err == ErrNoTrustrootsName {
		t.Fatalf("expected the close error to propagate, got %v", ErrNoTrustrootsName)
	}
}

func signedTrustrootsProfile(t *testing.T, privateKey, username string) nostr.Event {
	t.Helper()
	event := nostr.Event{
		CreatedAt: nostr.Timestamp(1700000000),
		Kind:      TrustrootsProfileKind,
		Tags:      nostr.Tags{{"l", username, TrustrootsUsernameLabelNamespace}},
		Content:   "",
	}
	if err := event.Sign(privateKey); err != nil {
		t.Fatalf("failed to sign trustroots profile: %v", err)
	}
	return event
}

func signedRequest(t *testing.T, privateKey, url string, createdAt time.Time) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	event := nostr.Event{
		CreatedAt: nostr.Timestamp(createdAt.Unix()),
		Kind:      27235,
		Tags:      nostr.Tags{{"u", url}, {"method", http.MethodGet}},
	}
	if err := event.Sign(privateKey); err != nil {
		t.Fatalf("failed to sign NIP-98 event: %v", err)
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("failed to marshal NIP-98 event: %v", err)
	}
	req.Header.Set("Authorization", "Nostr "+base64.StdEncoding.EncodeToString(raw))
	return req
}

func signedFollowList(t *testing.T, privateKey string, tags nostr.Tags) nostr.Event {
	t.Helper()
	event := nostr.Event{
		CreatedAt: nostr.Timestamp(1700000000),
		Kind:      NIP02FollowListKind,
		Tags:      tags,
		Content:   "",
	}
	if err := event.Sign(privateKey); err != nil {
		t.Fatalf("failed to sign follow list: %v", err)
	}
	return event
}

func mustPubkey(t *testing.T, privateKey string) string {
	t.Helper()
	pubkey, err := nostr.GetPublicKey(privateKey)
	if err != nil {
		t.Fatalf("GetPublicKey returned error: %v", err)
	}
	return pubkey
}

type fakeRelayConn struct {
	messages [][]byte
	index    int
}

func newFakeRelayConn(subID string, event nostr.Event) *fakeRelayConn {
	eventMessage, _ := json.Marshal([]any{"EVENT", subID, event})
	eoseMessage, _ := json.Marshal([]any{"EOSE", subID})
	return &fakeRelayConn{messages: [][]byte{eventMessage, eoseMessage}}
}

func newFakeClosedRelayConn() *fakeRelayConn {
	closedMessage, _ := json.Marshal([]any{"CLOSED", "wrapster-access-profile", "auth-required: NIP-42 authentication required"})
	return &fakeRelayConn{messages: [][]byte{closedMessage}}
}

func (c *fakeRelayConn) WriteJSON(any) error {
	return nil
}

func (c *fakeRelayConn) ReadMessage() (int, []byte, error) {
	message := c.messages[c.index]
	c.index++
	return 1, message, nil
}

func (c *fakeRelayConn) SetReadDeadline(time.Time) error {
	return nil
}

func (c *fakeRelayConn) Close() error {
	return nil
}
