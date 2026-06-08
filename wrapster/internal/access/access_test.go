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
