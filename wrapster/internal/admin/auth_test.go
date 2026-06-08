package admin

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

func TestVerifyHeader(t *testing.T) {
	privateKey := nostr.GeneratePrivateKey()
	pubkey, err := nostr.GetPublicKey(privateKey)
	if err != nil {
		t.Fatalf("GetPublicKey returned error: %v", err)
	}
	now := time.Unix(1700000000, 0)
	url := "https://wrapster.example/admin/api/status"

	authz := NewAuthorizer([]string{pubkey}, time.Minute)
	authz.Now = func() time.Time { return now }

	header := signedHeader(t, privateKey, url, http.MethodGet, now)
	got, err := authz.VerifyHeader(header, url, http.MethodGet)
	if err != nil {
		t.Fatalf("VerifyHeader returned error: %v", err)
	}
	if got != pubkey {
		t.Fatalf("expected pubkey %s, got %s", pubkey, got)
	}
}

func TestNewAuthorizerAcceptsAdminNpub(t *testing.T) {
	pubkey := "3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d"
	npub := "npub180cvv07tjdrrgpa0j7j7tmnyl2yr6yr7l8j4s3evf6u64th6gkwsyjh6w6"

	authz := NewAuthorizer([]string{npub}, time.Minute)

	if _, ok := authz.Admins[pubkey]; !ok {
		t.Fatalf("expected decoded npub to authorize %s", pubkey)
	}
}

func TestVerifyHeaderFailures(t *testing.T) {
	privateKey := nostr.GeneratePrivateKey()
	pubkey, err := nostr.GetPublicKey(privateKey)
	if err != nil {
		t.Fatalf("GetPublicKey returned error: %v", err)
	}
	otherKey := nostr.GeneratePrivateKey()
	now := time.Unix(1700000000, 0)
	url := "https://wrapster.example/admin/api/status"

	tests := []struct {
		name   string
		header string
		url    string
		method string
		admins []string
		want   error
	}{
		{
			name:   "missing",
			header: "",
			url:    url,
			method: http.MethodGet,
			admins: []string{pubkey},
			want:   ErrMissingAuthorization,
		},
		{
			name:   "wrong scheme",
			header: "Bearer token",
			url:    url,
			method: http.MethodGet,
			admins: []string{pubkey},
			want:   ErrWrongScheme,
		},
		{
			name:   "wrong url",
			header: signedHeader(t, privateKey, url, http.MethodGet, now),
			url:    "https://wrapster.example/admin/api/policy",
			method: http.MethodGet,
			admins: []string{pubkey},
			want:   ErrWrongURL,
		},
		{
			name:   "wrong method",
			header: signedHeader(t, privateKey, url, http.MethodPost, now),
			url:    url,
			method: http.MethodGet,
			admins: []string{pubkey},
			want:   ErrWrongMethod,
		},
		{
			name:   "stale",
			header: signedHeader(t, privateKey, url, http.MethodGet, now.Add(-2*time.Minute)),
			url:    url,
			method: http.MethodGet,
			admins: []string{pubkey},
			want:   ErrStaleEvent,
		},
		{
			name:   "non admin",
			header: signedHeader(t, otherKey, url, http.MethodGet, now),
			url:    url,
			method: http.MethodGet,
			admins: []string{pubkey},
			want:   ErrNotAdmin,
		},
		{
			name:   "bad signature",
			header: tamperedHeader(t, privateKey, url, http.MethodGet, now),
			url:    url,
			method: http.MethodGet,
			admins: []string{pubkey},
			want:   ErrBadSignature,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authz := NewAuthorizer(tt.admins, time.Minute)
			authz.Now = func() time.Time { return now }
			_, err := authz.VerifyHeader(tt.header, tt.url, tt.method)
			if !errors.Is(err, tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, err)
			}
		})
	}
}

func TestAbsoluteRequestURL(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://internal/admin/api/status?x=1", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "wrapster.example")

	got := AbsoluteRequestURL(req)
	want := "https://wrapster.example/admin/api/status?x=1"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func signedHeader(t *testing.T, privateKey, url, method string, createdAt time.Time) string {
	t.Helper()
	event := nostr.Event{
		CreatedAt: nostr.Timestamp(createdAt.Unix()),
		Kind:      NIP98EventKind,
		Tags:      nostr.Tags{{"u", url}, {"method", method}},
		Content:   "",
	}
	if err := event.Sign(privateKey); err != nil {
		t.Fatalf("failed to sign event: %v", err)
	}
	return encodeHeader(t, event)
}

func tamperedHeader(t *testing.T, privateKey, url, method string, createdAt time.Time) string {
	t.Helper()
	event := nostr.Event{
		CreatedAt: nostr.Timestamp(createdAt.Unix()),
		Kind:      NIP98EventKind,
		Tags:      nostr.Tags{{"u", url}, {"method", method}},
		Content:   "",
	}
	if err := event.Sign(privateKey); err != nil {
		t.Fatalf("failed to sign event: %v", err)
	}
	event.Content = "tampered"
	return encodeHeader(t, event)
}

func encodeHeader(t *testing.T, event nostr.Event) string {
	t.Helper()
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("failed to marshal event: %v", err)
	}
	return "Nostr " + base64.StdEncoding.EncodeToString(raw)
}
