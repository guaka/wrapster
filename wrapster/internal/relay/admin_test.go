package relay

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
	adminauth "github.com/trustroots/nostroots/vibe/wrapster/internal/admin"
	"github.com/trustroots/nostroots/vibe/wrapster/internal/media"
	"github.com/trustroots/nostroots/vibe/wrapster/internal/proxy"
	"github.com/trustroots/nostroots/vibe/wrapster/internal/store"
)

func TestAdminIndex(t *testing.T) {
	server := &Server{}
	req := httptest.NewRequest(http.MethodGet, "http://wrapster.test/admin", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Wrapster Admin") {
		t.Fatalf("expected admin HTML, got %q", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `window.addEventListener("load", autoConnect)`) {
		t.Fatalf("expected admin HTML to auto-connect to NIP-07")
	}
	if strings.Contains(rec.Body.String(), `getPublicKey`) {
		t.Fatalf("expected admin HTML to use signed event pubkey instead of getPublicKey")
	}
	if !strings.Contains(rec.Body.String(), `/admin/api/overview`) {
		t.Fatalf("expected admin HTML to load the dashboard with one signed request")
	}
	if !strings.Contains(rec.Body.String(), `Signed npub`) || !strings.Contains(rec.Body.String(), `function npubEncode`) {
		t.Fatalf("expected admin HTML to show the signing npub on auth errors")
	}
	if !strings.Contains(rec.Body.String(), `<link rel="icon" href="/favicon.svg" type="image/svg+xml">`) {
		t.Fatalf("expected admin HTML to link the favicon")
	}
	if !strings.Contains(rec.Body.String(), `href="/examples/service-advert-browser.html"`) {
		t.Fatalf("expected admin HTML to link the example browser")
	}
	if !strings.Contains(rec.Body.String(), `id="advert-services"`) || !strings.Contains(rec.Body.String(), `function publishAdvertToRelay`) {
		t.Fatalf("expected admin HTML to include service advert publishing controls")
	}
	if !strings.Contains(rec.Body.String(), `cors-proxy`) {
		t.Fatalf("expected admin HTML to include proxy service advert support")
	}
	if !strings.Contains(rec.Body.String(), `"Auth TTL": compactDuration`) || !strings.Contains(rec.Body.String(), `function compactDuration`) {
		t.Fatalf("expected admin HTML to compact verbose duration strings")
	}
}

func TestFavicon(t *testing.T) {
	server := &Server{}
	req := httptest.NewRequest(http.MethodGet, "http://wrapster.test/favicon.svg", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "image/svg+xml" {
		t.Fatalf("Content-Type = %q", got)
	}
	if !strings.Contains(rec.Body.String(), `<svg`) || !strings.Contains(rec.Body.String(), `#64c7ad`) {
		t.Fatalf("expected favicon SVG, got %q", rec.Body.String())
	}
}

func TestAdminAPIRequiresSignedAdminRequest(t *testing.T) {
	adminKey := nostr.GeneratePrivateKey()
	adminPubkey, err := nostr.GetPublicKey(adminKey)
	if err != nil {
		t.Fatalf("GetPublicKey returned error: %v", err)
	}
	otherKey := nostr.GeneratePrivateKey()
	now := time.Unix(1700000000, 0)

	server := &Server{
		AdminAuth: adminauth.Authorizer{
			Admins: map[string]struct{}{adminPubkey: {}},
			MaxAge: time.Minute,
			Now:    func() time.Time { return now },
		},
	}

	tests := []struct {
		name       string
		header     string
		wantStatus int
	}{
		{
			name:       "missing auth",
			header:     "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "non admin",
			header:     adminSignedHeader(t, otherKey, "http://wrapster.test/admin/api/policy", http.MethodGet, now),
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "admin",
			header:     adminSignedHeader(t, adminKey, "http://wrapster.test/admin/api/policy", http.MethodGet, now),
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://wrapster.test/admin/api/policy", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()

			server.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tt.wantStatus, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAdminStatusAndAuthCache(t *testing.T) {
	adminKey := nostr.GeneratePrivateKey()
	adminPubkey, err := nostr.GetPublicKey(adminKey)
	if err != nil {
		t.Fatalf("GetPublicKey returned error: %v", err)
	}
	now := time.Unix(1700000000, 0)

	cache, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer cache.Close()
	if err := cache.Put(t.Context(), "pubkey", "alice", now.Add(-time.Minute), now.Add(time.Hour)); err != nil {
		t.Fatalf("Put returned error: %v", err)
	}

	server := &Server{
		PublicRelayURL:  "ws://wrapster.test",
		Upstream:        Upstream{URL: "ws://127.0.0.1:1", Lookup: time.Nanosecond},
		Cache:           cache,
		AuthCacheTTL:    24 * time.Hour,
		AuthEventMaxAge: 10 * time.Minute,
		AdminAuth: adminauth.Authorizer{
			Admins: map[string]struct{}{adminPubkey: {}},
			MaxAge: time.Minute,
			Now:    func() time.Time { return now },
		},
	}

	status := getAdminJSON(t, server, adminKey, now, "/admin/api/status")
	health := status["health"].(map[string]any)
	if health["cache"] != true || health["upstream"] != false {
		t.Fatalf("unexpected health payload: %+v", health)
	}
	relay := status["relay"].(map[string]any)
	if relay["auth_cache_ttl"] != "24h" || relay["auth_event_window"] != "10m" {
		t.Fatalf("unexpected relay duration payload: %+v", relay)
	}

	cacheSummary := getAdminJSON(t, server, adminKey, now, "/admin/api/auth-cache")
	if cacheSummary["total"].(float64) != 1 || cacheSummary["valid"].(float64) != 1 {
		t.Fatalf("unexpected cache summary: %+v", cacheSummary)
	}

	overview := getAdminJSON(t, server, adminKey, now, "/admin/api/overview")
	if overview["authenticated_pubkey"] != adminPubkey {
		t.Fatalf("unexpected overview authenticated pubkey: %+v", overview)
	}
	if _, ok := overview["status"].(map[string]any); !ok {
		t.Fatalf("expected overview status payload: %+v", overview)
	}
	if overviewCache, ok := overview["auth_cache"].(map[string]any); !ok || overviewCache["total"].(float64) != 1 {
		t.Fatalf("expected overview auth cache payload: %+v", overview)
	}
	if _, ok := overview["policy"].(map[string]any); !ok {
		t.Fatalf("expected overview policy payload: %+v", overview)
	}
}

func TestAdminConfigAdvertisableServicesIncludesProxyAndMedia(t *testing.T) {
	server := &Server{
		GenericProxy: proxy.New(proxy.Config{
			Prefix: "/proxy",
			Targets: map[string]string{
				"trustroots": "https://www.trustroots.org",
			},
		}),
		MediaGateway: media.Gateway{
			ServiceAccessRules: map[string]string{
				"jellyfin": "trustroots_nip05",
			},
		},
	}

	payload := server.adminConfigPayload()
	services, ok := payload["advertisable_services"].([]map[string]any)
	if !ok {
		t.Fatalf("expected advertisable services payload, got %+v", payload["advertisable_services"])
	}

	seen := map[string]bool{}
	for _, service := range services {
		seen[service["service"].(string)] = true
	}
	if !seen["cors-proxy"] {
		t.Fatalf("expected proxy to be advertised as cors-proxy, got %+v", services)
	}
	if !seen["jellyfin"] {
		t.Fatalf("expected media service to be advertisable, got %+v", services)
	}
}

func getAdminJSON(t *testing.T, server *Server, privateKey string, now time.Time, path string) map[string]any {
	t.Helper()
	url := "http://wrapster.test" + path
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", adminSignedHeader(t, privateKey, url, http.MethodGet, now))
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	return body
}

func adminSignedHeader(t *testing.T, privateKey, url, method string, createdAt time.Time) string {
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
