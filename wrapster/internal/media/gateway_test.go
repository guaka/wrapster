package media

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/trustroots/nostroots/vibe/wrapster/internal/access"
	adminauth "github.com/trustroots/nostroots/vibe/wrapster/internal/admin"
)

func TestGatewayRequiresGrantedNIP98Request(t *testing.T) {
	grantedKey := nostr.GeneratePrivateKey()
	grantedPubkey, err := nostr.GetPublicKey(grantedKey)
	if err != nil {
		t.Fatalf("GetPublicKey returned error: %v", err)
	}
	otherKey := nostr.GeneratePrivateKey()
	now := time.Unix(1700000000, 0)

	connector := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/connector/api/status" {
			t.Fatalf("unexpected connector path %s", r.URL.Path)
		}
		writeJSON(w, http.StatusOK, map[string]any{"services": map[string]any{}})
	})

	gateway := Gateway{
		ConnectorBaseURL: "http://connector.test",
		HTTPClient:       clientFor(connector),
		Auth: Authorizer{
			Grants: map[string]struct{}{grantedPubkey: {}},
			MaxAge: time.Minute,
			Now:    func() time.Time { return now },
		},
	}

	tests := []struct {
		name       string
		key        string
		wantStatus int
	}{
		{name: "missing", wantStatus: http.StatusUnauthorized},
		{name: "not granted", key: otherKey, wantStatus: http.StatusForbidden},
		{name: "granted", key: grantedKey, wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://wrapster.test/media/api/status", nil)
			if tt.key != "" {
				req.Header.Set("Authorization", signedHeader(t, tt.key, "http://wrapster.test/media/api/status", http.MethodGet, now))
			}
			rec := httptest.NewRecorder()

			gateway.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tt.wantStatus, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestGatewayForwardsOnlyKnownMediaRoutes(t *testing.T) {
	key := nostr.GeneratePrivateKey()
	pubkey, err := nostr.GetPublicKey(key)
	if err != nil {
		t.Fatalf("GetPublicKey returned error: %v", err)
	}
	now := time.Unix(1700000000, 0)

	var gotPath, gotQuery string
	connector := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
	})

	gateway := Gateway{
		ConnectorBaseURL: "http://connector.test",
		HTTPClient:       clientFor(connector),
		Auth: Authorizer{
			Grants: map[string]struct{}{pubkey: {}},
			MaxAge: time.Minute,
			Now:    func() time.Time { return now },
		},
	}

	url := "http://wrapster.test/media/api/services/jellyfin/search?q=matrix"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", signedHeader(t, key, url, http.MethodGet, now))
	rec := httptest.NewRecorder()

	gateway.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/connector/api/services/jellyfin/search" || gotQuery != "q=matrix" {
		t.Fatalf("unexpected connector request %s?%s", gotPath, gotQuery)
	}

	badURL := "http://wrapster.test/media/api/services/router/search?q=secrets"
	badReq := httptest.NewRequest(http.MethodGet, badURL, nil)
	badReq.Header.Set("Authorization", signedHeader(t, key, badURL, http.MethodGet, now))
	badRec := httptest.NewRecorder()

	gateway.ServeHTTP(badRec, badReq)

	if badRec.Code != http.StatusNotFound {
		t.Fatalf("expected unknown service to be hidden, got %d", badRec.Code)
	}
}

func TestGatewayStatusUsesConfiguredTransportLabel(t *testing.T) {
	key := nostr.GeneratePrivateKey()
	pubkey, err := nostr.GetPublicKey(key)
	if err != nil {
		t.Fatalf("GetPublicKey returned error: %v", err)
	}
	now := time.Unix(1700000000, 0)
	connector := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"services": map[string]any{}})
	})
	gateway := Gateway{
		ConnectorBaseURL: "http://connector.test",
		TransportLabel:   "fips",
		HTTPClient:       clientFor(connector),
		Auth: Authorizer{
			Grants: map[string]struct{}{pubkey: {}},
			MaxAge: time.Minute,
			Now:    func() time.Time { return now },
		},
	}
	url := "http://wrapster.test/media/api/status"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", signedHeader(t, key, url, http.MethodGet, now))
	rec := httptest.NewRecorder()

	gateway.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body["transport"] != "fips" {
		t.Fatalf("transport = %q, want fips", body["transport"])
	}
}

func TestGatewayConnectorStatusReportsReachableConnector(t *testing.T) {
	var gotToken string
	connector := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/connector/api/status" {
			t.Fatalf("unexpected connector path %s", r.URL.Path)
		}
		gotToken = r.Header.Get("Authorization")
		writeJSON(w, http.StatusOK, map[string]any{
			"services": map[string]any{
				"jellyfin": map[string]bool{"configured": true},
			},
		})
	})
	gateway := Gateway{
		ConnectorBaseURL: "http://home-media.fips:22000",
		ConnectorToken:   "connector-secret",
		TransportLabel:   "fips",
		HTTPClient:       clientFor(connector),
	}

	status := gateway.ConnectorStatus(context.Background())

	if gotToken != "Bearer connector-secret" {
		t.Fatalf("connector token = %q, want bearer token", gotToken)
	}
	if status["configured"] != true || status["reachable"] != true || status["transport"] != "fips" || status["status_code"] != http.StatusOK {
		t.Fatalf("unexpected connector status: %+v", status)
	}
	if status["last_error"] != "" {
		t.Fatalf("expected empty last_error, got %+v", status["last_error"])
	}
	if status["connector"] == nil {
		t.Fatalf("expected connector payload: %+v", status)
	}
}

func TestGatewayConnectorStatusReportsUnavailableConnector(t *testing.T) {
	gateway := Gateway{TransportLabel: "fips"}

	status := gateway.ConnectorStatus(context.Background())

	if status["configured"] != false || status["reachable"] != false || status["transport"] != "fips" {
		t.Fatalf("unexpected connector status: %+v", status)
	}
	if status["last_error"] != "media connector is not configured" {
		t.Fatalf("unexpected last_error: %+v", status["last_error"])
	}
}

func TestGatewayUsesConfiguredFollowAccessRuleForMediaServices(t *testing.T) {
	key := nostr.GeneratePrivateKey()
	pubkey, err := nostr.GetPublicKey(key)
	if err != nil {
		t.Fatalf("GetPublicKey returned error: %v", err)
	}
	now := time.Unix(1700000000, 0)
	connector := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
	})
	gateway := Gateway{
		ConnectorBaseURL: "http://connector.test",
		HTTPClient:       clientFor(connector),
		Access: access.Authorizer{
			Rules: map[string]access.Rule{
				"media_owner_follows": {Type: access.RuleNostrFollow, OwnerPubkey: pubkey},
			},
			MaxAge: time.Minute,
			Now:    func() time.Time { return now },
			FollowVerifier: func(_ context.Context, _ access.Rule, gotPubkey string) error {
				if gotPubkey != pubkey {
					t.Fatalf("pubkey = %q, want %q", gotPubkey, pubkey)
				}
				return nil
			},
		},
		ServiceAccessRules: map[string][]string{
			"jellyfin": {"media_owner_follows"},
			"plex":     {"media_owner_follows"},
		},
	}

	for _, service := range []string{"jellyfin", "plex"} {
		t.Run(service, func(t *testing.T) {
			url := "http://wrapster.test/media/api/services/" + service + "/search?q=matrix"
			req := httptest.NewRequest(http.MethodGet, url, nil)
			req.Header.Set("Authorization", signedHeader(t, key, url, http.MethodGet, now))
			rec := httptest.NewRecorder()

			gateway.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestGatewayDeniesConfiguredFollowAccessRuleMiss(t *testing.T) {
	key := nostr.GeneratePrivateKey()
	now := time.Unix(1700000000, 0)
	gateway := Gateway{
		ConnectorBaseURL: "http://connector.test",
		HTTPClient: clientFor(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("connector should not be reached")
		})),
		Access: access.Authorizer{
			Rules: map[string]access.Rule{
				"media_owner_follows": {Type: access.RuleNostrFollow, OwnerPubkey: "owner"},
			},
			MaxAge: time.Minute,
			Now:    func() time.Time { return now },
			FollowVerifier: func(context.Context, access.Rule, string) error {
				return access.ErrNotAllowed
			},
		},
		ServiceAccessRules: map[string][]string{"jellyfin": {"media_owner_follows"}},
	}
	url := "http://wrapster.test/media/api/services/jellyfin/search?q=matrix"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", signedHeader(t, key, url, http.MethodGet, now))
	rec := httptest.NewRecorder()

	gateway.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGatewayStatusRequiresCompleteServiceRuleSet(t *testing.T) {
	key := nostr.GeneratePrivateKey()
	pubkey, err := nostr.GetPublicKey(key)
	if err != nil {
		t.Fatalf("GetPublicKey returned error: %v", err)
	}
	now := time.Unix(1700000000, 0)
	gateway := Gateway{
		ConnectorBaseURL: "http://connector.test",
		HTTPClient: clientFor(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("connector should not be reached")
		})),
		Access: access.Authorizer{
			Rules: map[string]access.Rule{
				"trustroots_nip05":    {Type: access.RuleTrustrootsNIP05},
				"media_owner_follows": {Type: access.RuleNostrFollow, OwnerPubkey: pubkey},
			},
			MaxAge: time.Minute,
			Now:    func() time.Time { return now },
			TrustrootsVerifier: func(context.Context, access.Rule, string) error {
				return nil
			},
			FollowVerifier: func(context.Context, access.Rule, string) error {
				return access.ErrNotAllowed
			},
		},
		ServiceAccessRules: map[string][]string{
			"jellyfin": {"trustroots_nip05", "media_owner_follows"},
		},
	}
	url := "http://wrapster.test/media/api/status"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", signedHeader(t, key, url, http.MethodGet, now))
	rec := httptest.NewRecorder()

	gateway.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGatewayForwardsStreamRangeAndConnectorToken(t *testing.T) {
	key := nostr.GeneratePrivateKey()
	pubkey, err := nostr.GetPublicKey(key)
	if err != nil {
		t.Fatalf("GetPublicKey returned error: %v", err)
	}
	now := time.Unix(1700000000, 0)

	connector := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer connector-secret" {
			t.Fatalf("unexpected connector token %q", got)
		}
		if got := r.Header.Get("Range"); got != "bytes=0-9" {
			t.Fatalf("unexpected Range header %q", got)
		}
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Range", "bytes 0-9/100")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("0123456789"))
	})

	gateway := Gateway{
		ConnectorBaseURL: "http://connector.test",
		ConnectorToken:   "connector-secret",
		HTTPClient:       clientFor(connector),
		Auth: Authorizer{
			Grants: map[string]struct{}{pubkey: {}},
			MaxAge: time.Minute,
			Now:    func() time.Time { return now },
		},
	}
	url := "http://wrapster.test/media/api/services/jellyfin/stream/item123"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", signedHeader(t, key, url, http.MethodGet, now))
	req.Header.Set("Range", "bytes=0-9")
	rec := httptest.NewRecorder()

	gateway.ServeHTTP(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("expected status 206, got %d", rec.Code)
	}
	if rec.Header().Get("Content-Range") != "bytes 0-9/100" || strings.TrimSpace(rec.Body.String()) != "0123456789" {
		t.Fatalf("unexpected stream response headers=%v body=%q", rec.Header(), rec.Body.String())
	}
}

func signedHeader(t *testing.T, privateKey, url, method string, createdAt time.Time) string {
	t.Helper()
	event := nostr.Event{
		CreatedAt: nostr.Timestamp(createdAt.Unix()),
		Kind:      adminauth.NIP98EventKind,
		Tags:      nostr.Tags{{"u", url}, {"method", method}},
		Content:   "",
	}
	if err := event.Sign(privateKey); err != nil {
		t.Fatalf("failed to sign event: %v", err)
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("failed to marshal event: %v", err)
	}
	return "Nostr " + base64.StdEncoding.EncodeToString(raw)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func clientFor(handler http.Handler) *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			return rec.Result(), nil
		}),
	}
}
