package relay

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
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
	body := rec.Body.String()
	if !strings.Contains(body, "Wrapster Admin") {
		t.Fatalf("expected admin HTML, got %q", body)
	}
	if !strings.Contains(body, "NIP-42 authenticated relay wrapper with additional services") {
		t.Fatalf("expected admin HTML to show the relay wrapper description")
	}
	if !strings.Contains(body, `Admin API requests are signed with NIP-98`) || !strings.Contains(body, `Relay users must complete NIP-42 authentication`) {
		t.Fatalf("expected admin header to explain the access policy on hover")
	}
	if !strings.Contains(body, `window.addEventListener("load", autoConnect)`) {
		t.Fatalf("expected admin HTML to auto-connect to NIP-07")
	}
	if strings.Contains(body, `getPublicKey`) {
		t.Fatalf("expected admin HTML to use signed event pubkey instead of getPublicKey")
	}
	if !strings.Contains(body, `/admin/api/overview`) {
		t.Fatalf("expected admin HTML to load the dashboard with one signed request")
	}
	if !strings.Contains(body, `Signed npub`) || !strings.Contains(body, `function npubEncode`) {
		t.Fatalf("expected admin HTML to show the signing npub on auth errors")
	}
	if !strings.Contains(body, `class="connect-button"`) || !strings.Contains(body, `connectButton.title = identityHoverText(data)`) || !strings.Contains(body, `Trustroots NIP-05: " + data.trustroots_nip05`) {
		t.Fatalf("expected admin HTML to show signed identity details inside the connected button")
	}
	if !strings.Contains(body, `<link rel="icon" href="/favicon.svg" type="image/svg+xml">`) {
		t.Fatalf("expected admin HTML to link the favicon")
	}
	if !strings.Contains(body, `class="footer-link" href="/examples/service-directory.html"`) || !strings.Contains(body, `Service directory`) {
		t.Fatalf("expected admin HTML to link the service directory from the footer")
	}
	if !strings.Contains(body, `max-width: none`) || !strings.Contains(body, `padding: 18px 24px`) || !strings.Contains(body, `font-size: 28px`) {
		t.Fatalf("expected admin HTML to use a full-width, compact shell")
	}
	if !strings.Contains(body, `id="advert-services"`) || !strings.Contains(body, `function publishAdvertToRelay`) {
		t.Fatalf("expected admin HTML to include service advert publishing controls")
	}
	if strings.Contains(body, `<h2>Advert Notes</h2>`) || strings.Contains(body, `id="advert-notes"`) {
		t.Fatalf("expected admin HTML to integrate advert notes into service cards")
	}
	if !strings.Contains(body, `function loadAdvertNotes`) || !strings.Contains(body, `function advertNotesForService`) || !strings.Contains(body, `Published adverts`) {
		t.Fatalf("expected admin HTML to show previously published advert notes inside service cards")
	}
	if !strings.Contains(body, `const NIP09_DELETE_KIND = 5`) || !strings.Contains(body, `Delete advert note`) || !strings.Contains(body, `function buildAdvertDeleteEvent`) {
		t.Fatalf("expected admin HTML to support deleting advert notes")
	}
	if !strings.Contains(body, `unauthenticatedReqTimer = window.setTimeout(sendRequest, 600)`) || !strings.Contains(body, `function waitForAuthRetry`) || !strings.Contains(body, `if (authAccepted) return false`) {
		t.Fatalf("expected admin HTML to use NIP-42-aware relay lookup for advert notes")
	}
	if !strings.Contains(body, `function proxyDetails`) || !strings.Contains(body, `Proxy access`) || !strings.Contains(body, `Proxy routes`) {
		t.Fatalf("expected admin HTML to show proxy settings inside the proxy service card")
	}
	if !strings.Contains(body, `function mediaDetails`) || !strings.Contains(body, `Access rules`) || !strings.Contains(body, `Media connector`) || !strings.Contains(body, `Media services`) {
		t.Fatalf("expected admin HTML to show media settings inside the media service card")
	}
	if !strings.Contains(body, `id="connector-dialog"`) || !strings.Contains(body, `MEDIA_CONNECTOR_BASE_URL`) || !strings.Contains(body, `CONNECTOR_SHARED_TOKEN`) || !strings.Contains(body, `function mediaConnectorValue`) {
		t.Fatalf("expected admin HTML to explain media connector setup from the media connector value")
	}
	if !strings.Contains(body, `id="generate-fips-nsec"`) || !strings.Contains(body, `id="fips-nsec"`) || !strings.Contains(body, `function generateFipsNsec`) || !strings.Contains(body, `bech32Encode("nsec"`) {
		t.Fatalf("expected admin HTML to include local FIPS nsec generation")
	}
	if !strings.Contains(body, `button.textContent = "not configured"`) || !strings.Contains(body, `connectorDialog.showModal()`) {
		t.Fatalf("expected unconfigured media connector value to open the setup modal")
	}
	if !strings.Contains(body, `id="access-dialog"`) || !strings.Contains(body, `function resolveNostrFollowAccess`) || !strings.Contains(body, `NIP02_FOLLOW_LIST_KIND`) || !strings.Contains(body, `TRUSTROOTS_PROFILE_KIND`) {
		t.Fatalf("expected admin HTML to query relay access rules and show access counts")
	}
	if !strings.Contains(body, `Checking contacts...`) || !strings.Contains(body, `accessDialog.showModal()`) || !strings.Contains(body, `kind: 22242`) || !strings.Contains(body, `Media owner contacts`) {
		t.Fatalf("expected admin HTML to make contact counts clickable with relay auth support")
	}
	if !strings.Contains(body, `label: "Media"`) || !strings.Contains(body, `button.textContent = advertButtonLabel(service)`) || !strings.Contains(body, `return service.service === "media" ? "Advertise Media" : "Advertise"`) {
		t.Fatalf("expected admin HTML to combine media services in one card with one media advertise action")
	}
	for _, removedConfigRow := range []string{`values["Proxy access"]`, `values["Proxy routes"]`, `values["Access rules"]`, `values["Media connector"]`, `values["Media services"]`} {
		if strings.Contains(body, removedConfigRow) {
			t.Fatalf("expected admin HTML to keep %s out of the configuration panel", removedConfigRow)
		}
	}
	if !strings.Contains(body, `cors-proxy`) {
		t.Fatalf("expected admin HTML to include proxy service advert support")
	}
	if !strings.Contains(body, `<h2>Relay Overview</h2>`) || !strings.Contains(body, `id="dashboard" class="dashboard-grid"`) || !strings.Contains(body, `function renderDashboard`) {
		t.Fatalf("expected admin HTML to show the admin dashboard")
	}
	if strings.Index(body, `<h2>Relay Overview</h2>`) > strings.Index(body, `<h2>Advertise Services</h2>`) {
		t.Fatalf("expected relay overview to appear before advertise services")
	}
	for _, dashboardText := range []string{`Public Relay`, `strfry`, `Auth Cache`, `NIP-05 Lookup Relays`, `"Configured": linesNode(relays.lookup || relays.additional || [])`} {
		if !strings.Contains(body, dashboardText) {
			t.Fatalf("expected admin dashboard to include %s", dashboardText)
		}
	}
	if !strings.Contains(body, `function relayAuthRequirement`) || !strings.Contains(body, `NIP-42 AUTH + `) || !strings.Contains(body, `NIP-05 (same pubkey)`) {
		t.Fatalf("expected admin dashboard auth row to explain the configured relay requirement")
	}
	if !strings.Contains(body, `id="query-dialog"`) || !strings.Contains(body, `function recentQueryValue`) || !strings.Contains(body, `openQueryDialog`) || !strings.Contains(body, `recent_queries`) {
		t.Fatalf("expected admin dashboard to show clickable recent public relay queries")
	}
	for _, removed := range []string{`<h2>Health</h2>`, `<h2>Relay</h2>`, `<h2>Relays</h2>`, `<h2>Auth Cache</h2>`, `<h2>Access Policy</h2>`, `<h2>Admin Policy</h2>`, `<h2>Service</h2>`, `id="health"`, `id="relays"`, `id="policy"`, `id="admin-policy"`, `id="service"`, `function renderHeaderHealth`, `function renderHeaderRelay`, `renderPolicy`, `Extra relays`, `"Auth TTL"`, `"Auth window"`, `"NIPs"`, `"Label namespace"`, `"Admin count"`, `"Admin auth max age"`} {
		if strings.Contains(body, removed) {
			t.Fatalf("expected admin HTML to omit %s", removed)
		}
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

func TestAdminSavesFIPSNsec(t *testing.T) {
	adminKey := nostr.GeneratePrivateKey()
	adminPubkey, err := nostr.GetPublicKey(adminKey)
	if err != nil {
		t.Fatalf("GetPublicKey returned error: %v", err)
	}
	now := time.Unix(1700000000, 0)
	fipsKey := nostr.GeneratePrivateKey()
	nsec, err := nip19.EncodePrivateKey(fipsKey)
	if err != nil {
		t.Fatalf("EncodePrivateKey returned error: %v", err)
	}
	fipsPubkey, err := nostr.GetPublicKey(fipsKey)
	if err != nil {
		t.Fatalf("GetPublicKey returned error: %v", err)
	}
	wantNpub, err := nip19.EncodePublicKey(fipsPubkey)
	if err != nil {
		t.Fatalf("EncodePublicKey returned error: %v", err)
	}
	nsecPath := filepath.Join(t.TempDir(), "fips", "nsec")
	server := &Server{
		FIPSNsecPath: nsecPath,
		AdminAuth: adminauth.Authorizer{
			Admins: map[string]struct{}{adminPubkey: {}},
			MaxAge: time.Minute,
			Now:    func() time.Time { return now },
		},
	}

	payload, err := json.Marshal(map[string]string{"nsec": nsec})
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}
	url := "http://wrapster.test/admin/api/fips-nsec"
	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(string(payload)))
	req.Header.Set("Authorization", adminSignedHeader(t, adminKey, url, http.MethodPost, now))
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := map[string]any{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if body["npub"] != wantNpub || body["saved"] != true {
		t.Fatalf("unexpected response: %#v", body)
	}
	saved, err := os.ReadFile(nsecPath)
	if err != nil {
		t.Fatalf("expected saved nsec: %v", err)
	}
	if strings.TrimSpace(string(saved)) != nsec {
		t.Fatalf("saved nsec mismatch")
	}
	if strings.Contains(rec.Body.String(), nsec) {
		t.Fatalf("response leaked nsec")
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
	server.recordRelayQuery(adminPubkey, []byte(`["REQ","recent-sub",{"kinds":[1],"limit":10}]`))

	status := getAdminJSON(t, server, adminKey, now, "/admin/api/status")
	health := status["health"].(map[string]any)
	if health["cache"] != true || health["upstream"] != false {
		t.Fatalf("unexpected health payload: %+v", health)
	}
	strfry := status["strfry"].(map[string]any)
	if strfry["url"] != "ws://127.0.0.1:1" || strfry["reachable"] != false {
		t.Fatalf("unexpected strfry payload: %+v", strfry)
	}
	if strfry["latency_ms"] != nil {
		t.Fatalf("expected unreachable strfry latency to be nil, got %+v", strfry["latency_ms"])
	}
	if strfry["checked_at"] != now.UTC().Format(time.RFC3339) {
		t.Fatalf("unexpected strfry checked_at: %+v", strfry)
	}
	if strfry["last_error"] == "" {
		t.Fatalf("expected unreachable strfry to include last_error: %+v", strfry)
	}
	if strfry["nip11"] != nil {
		t.Fatalf("expected unavailable strfry NIP-11 payload to be nil: %+v", strfry)
	}
	relay := status["relay"].(map[string]any)
	if relay["auth_cache_ttl"] != "24h" || relay["auth_event_window"] != "10m" {
		t.Fatalf("unexpected relay duration payload: %+v", relay)
	}
	recentQueries := relay["recent_queries"].([]any)
	if len(recentQueries) != 1 {
		t.Fatalf("recent queries = %+v", recentQueries)
	}
	query := recentQueries[0].(map[string]any)
	if query["pubkey"] != adminPubkey || query["subscription_id"] != "recent-sub" {
		t.Fatalf("unexpected recent query payload: %+v", query)
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
	if fips, ok := overview["fips"].(map[string]any); !ok || fips["state"] != "not_configured" {
		t.Fatalf("expected overview fips payload to be not_configured: %+v", overview["fips"])
	}
	if _, ok := overview["policy"].(map[string]any); !ok {
		t.Fatalf("expected overview policy payload: %+v", overview)
	}
}

func TestAdminOverviewReportsConfiguredFIPSIdentity(t *testing.T) {
	adminKey := nostr.GeneratePrivateKey()
	adminPubkey, err := nostr.GetPublicKey(adminKey)
	if err != nil {
		t.Fatalf("GetPublicKey returned error: %v", err)
	}
	now := time.Unix(1700000000, 0)

	fipsKey := nostr.GeneratePrivateKey()
	fipsNsec, err := nip19.EncodePrivateKey(fipsKey)
	if err != nil {
		t.Fatalf("EncodePrivateKey returned error: %v", err)
	}
	fipsPubkey, err := nostr.GetPublicKey(fipsKey)
	if err != nil {
		t.Fatalf("GetPublicKey returned error: %v", err)
	}
	wantNpub, err := nip19.EncodePublicKey(fipsPubkey)
	if err != nil {
		t.Fatalf("EncodePublicKey returned error: %v", err)
	}
	nsecPath := filepath.Join(t.TempDir(), "fips", "nsec")
	if err := os.MkdirAll(filepath.Dir(nsecPath), 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(nsecPath, []byte(fipsNsec+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	server := &Server{
		FIPSNsecPath: nsecPath,
		Upstream:     Upstream{URL: "ws://127.0.0.1:1"},
		AdminAuth: adminauth.Authorizer{
			Admins: map[string]struct{}{adminPubkey: {}},
			MaxAge: time.Minute,
			Now:    func() time.Time { return now },
		},
	}

	overview := getAdminJSON(t, server, adminKey, now, "/admin/api/overview")
	if overview["authenticated_pubkey"] != adminPubkey {
		t.Fatalf("unexpected overview authenticated pubkey: %+v", overview)
	}
	fips, ok := overview["fips"].(map[string]any)
	if !ok {
		t.Fatalf("expected overview fips payload: %+v", overview)
	}
	if fips["state"] != "configured" {
		t.Fatalf("expected configured fips state, got %+v", fips)
	}
	if fips["npub"] != wantNpub {
		t.Fatalf("unexpected fips npub: %+v", fips["npub"])
	}
}

func TestAdminStatusReportsReachableStrfryAndNIP11(t *testing.T) {
	adminKey := nostr.GeneratePrivateKey()
	adminPubkey, err := nostr.GetPublicKey(adminKey)
	if err != nil {
		t.Fatalf("GetPublicKey returned error: %v", err)
	}
	now := time.Unix(1700000000, 0)
	upgrader := websocket.Upgrader{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") == "application/nostr+json" {
			writeJSON(w, http.StatusOK, map[string]any{
				"name":           "wrapster upstream strfry",
				"description":    "Private upstream strfry for local wrapster development.",
				"supported_nips": []int{1, 11},
			})
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade returned error: %v", err)
			return
		}
		_ = conn.Close()
	}))
	defer upstream.Close()

	server := &Server{
		Upstream: Upstream{URL: "ws" + strings.TrimPrefix(upstream.URL, "http")},
		AdminAuth: adminauth.Authorizer{
			Admins: map[string]struct{}{adminPubkey: {}},
			MaxAge: time.Minute,
			Now:    func() time.Time { return now },
		},
	}

	status := getAdminJSON(t, server, adminKey, now, "/admin/api/status")
	health := status["health"].(map[string]any)
	if health["upstream"] != true {
		t.Fatalf("expected reachable strfry to keep health upstream true, got %+v", health)
	}
	strfry := status["strfry"].(map[string]any)
	if strfry["reachable"] != true || strfry["last_error"] != "" {
		t.Fatalf("unexpected reachable strfry payload: %+v", strfry)
	}
	if _, ok := strfry["latency_ms"].(float64); !ok {
		t.Fatalf("expected reachable strfry latency_ms number, got %+v", strfry["latency_ms"])
	}
	nip11 := strfry["nip11"].(map[string]any)
	if nip11["name"] != "wrapster upstream strfry" {
		t.Fatalf("unexpected strfry NIP-11 payload: %+v", nip11)
	}
}

func TestAdminConfigAdvertisableServicesIncludesProxyAndMedia(t *testing.T) {
	server := &Server{
		Upstream: Upstream{
			ProfileRelays: []string{"wss://nip42.trustroots.org"},
		},
		GenericProxy: proxy.New(proxy.Config{
			Prefix: "/proxy",
			Targets: map[string]string{
				"trustroots": "https://www.trustroots.org",
			},
		}),
		MediaGateway: media.Gateway{
			ServiceAccessRules: map[string][]string{
				"jellyfin": {"trustroots_nip05"},
			},
		},
	}

	payload := server.adminConfigPayload()
	relays := payload["relays"].(map[string]any)
	if lookup, additional := relays["lookup"].([]string), relays["additional"].([]string); len(lookup) != 1 || lookup[0] != "wss://nip42.trustroots.org" || len(additional) != 1 || additional[0] != lookup[0] {
		t.Fatalf("expected lookup relays to mirror additional relays, got %+v", relays)
	}
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
