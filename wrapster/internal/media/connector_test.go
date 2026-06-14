package media

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
	adminauth "github.com/trustroots/nostroots/vibe/wrapster/internal/admin"
	"github.com/trustroots/nostroots/vibe/wrapster/internal/buildinfo"
)

func newSignedSetup(t *testing.T, connector *Connector) (SetupHandler, string, time.Time) {
	t.Helper()
	key := nostr.GeneratePrivateKey()
	pubkey, err := nostr.GetPublicKey(key)
	if err != nil {
		t.Fatalf("GetPublicKey returned error: %v", err)
	}
	now := time.Unix(1700000000, 0)
	return SetupHandler{
		Connector:  connector,
		ConfigPath: filepath.Join(t.TempDir(), "connector-config.json"),
		Auth: adminauth.Authorizer{
			Admins: map[string]struct{}{pubkey: {}},
			MaxAge: time.Minute,
			Now:    func() time.Time { return now },
		},
	}, key, now
}

func TestSetupHandlerSavesMediaConfigWithSignedAdmin(t *testing.T) {
	connector := &Connector{}
	setup, key, now := newSignedSetup(t, connector)
	peerKey := nostr.GeneratePrivateKey()
	peerNpub, err := nostr.GetPublicKey(peerKey)
	if err != nil {
		t.Fatalf("GetPublicKey returned error: %v", err)
	}
	peerNpub, err = nip19.EncodePublicKey(peerNpub)
	if err != nil {
		t.Fatalf("EncodePublicKey returned error: %v", err)
	}

	body := `{"jellyfin_base_url":"http://jellyfin.local:8096/","jellyfin_api_key":"jelly-key","plex_base_url":"http://plex.local:32400","plex_token":"plex-token","fips_peer_npub":"` + peerNpub + `","fips_peer_addr":"relay.example.org:2121"}`
	url := "http://nas.test/setup/api/config"
	req := httptest.NewRequest(http.MethodPut, url, strings.NewReader(body))
	req.Header.Set("Authorization", signedHeader(t, key, url, http.MethodPut, now))
	rec := httptest.NewRecorder()

	setup.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cfg := connector.MediaConfig()
	if cfg.JellyfinBaseURL != "http://jellyfin.local:8096" || cfg.JellyfinAPIKey != "jelly-key" || cfg.PlexToken != "plex-token" {
		t.Fatalf("connector config not applied: %#v", cfg)
	}
	if cfg.FIPSPeerNpub != peerNpub {
		t.Fatalf("peer npub not stored: %#v", cfg.FIPSPeerNpub)
	}
	if cfg.FIPSPeerAddr != "relay.example.org:2121" {
		t.Fatalf("peer addr not stored: %#v", cfg.FIPSPeerAddr)
	}
	if _, err := os.Stat(setup.ConfigPath); err != nil {
		t.Fatalf("expected saved config file: %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, url, nil)
	getRec := httptest.NewRecorder()
	setup.ServeHTTP(getRec, getReq)
	if strings.Contains(getRec.Body.String(), "jelly-key") || strings.Contains(getRec.Body.String(), "plex-token") {
		t.Fatalf("expected secrets to be redacted, got %s", getRec.Body.String())
	}
	if !strings.Contains(getRec.Body.String(), `"token_configured":true`) {
		t.Fatalf("expected redacted token status, got %s", getRec.Body.String())
	}
	if !strings.Contains(getRec.Body.String(), `"fips_peer_npub":"`+peerNpub+`"`) {
		t.Fatalf("expected peer npub in saved config: %s", getRec.Body.String())
	}
	if !strings.Contains(getRec.Body.String(), `"fips_peer_addr":"relay.example.org:2121"`) {
		t.Fatalf("expected peer addr in saved config: %s", getRec.Body.String())
	}
}

func TestSetupHandlerServesFIPSNsecGenerator(t *testing.T) {
	previousBuildTime := buildinfo.BuildTime
	buildinfo.BuildTime = "2026-06-14T10:00:00Z"
	defer func() { buildinfo.BuildTime = previousBuildTime }()

	setup := SetupHandler{Connector: &Connector{}}

	for _, path := range []string{"/setup", "/admin"} {
		req := httptest.NewRequest(http.MethodGet, "http://nas.test"+path, nil)
		rec := httptest.NewRecorder()
		setup.ServeHTTP(rec, req)

		if path == "/setup" {
			if rec.Code != http.StatusFound {
				t.Fatalf("expected status 302 for %s, got %d: %s", path, rec.Code, rec.Body.String())
			}
			location := rec.Header().Get("Location")
			if location != "/admin" {
				t.Fatalf("expected setup redirect to /admin, got %q", location)
			}
			continue
		}

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200 for %s, got %d: %s", path, rec.Code, rec.Body.String())
		}

		body := rec.Body.String()
		if !strings.Contains(body, `id="generate-fips-nsec"`) || !strings.Contains(body, `id="fips-nsec"`) || !strings.Contains(body, `function generateFipsNsec`) || !strings.Contains(body, `bech32Encode("nsec"`) {
			t.Fatalf("expected setup UI to include local FIPS nsec generation")
		}
		if !strings.Contains(body, `id="test-fips-peer"`) || !strings.Contains(body, `id="fips-peer-check-result"`) {
			t.Fatalf("expected setup UI to include FIPS peer test controls")
		}
		if !strings.Contains(body, `id="fips-peer-npub"`) || !strings.Contains(body, `id="fips-peer-addr"`) {
			t.Fatalf("expected setup UI to include FIPS peer fields")
		}
		if !strings.Contains(body, `id="jellyfin-url-link"`) || !strings.Contains(body, `id="jellyfin-token-link"`) {
			t.Fatalf("expected Jellyfin setup quick links")
		}
		if !strings.Contains(body, `id="plex-url-link"`) || !strings.Contains(body, `id="plex-token-link"`) {
			t.Fatalf("expected Plex setup quick links")
		}
		if !strings.Contains(body, `href="/setup/favicon.svg"`) {
			t.Fatalf("expected setup UI to include local favicon")
		}
		if !regexp.MustCompile(`Build time: \d{4}-\d{2}-\d{2} \d{2}:\d{2}`).MatchString(body) || !strings.Contains(body, `href="https://github.com/guaka/wrapster"`) {
			t.Fatalf("expected setup UI to include build-time and GitHub footer metadata")
		}
	}
}

func TestSetupHandlerServesFavicon(t *testing.T) {
	setup := SetupHandler{Connector: &Connector{}}
	req := httptest.NewRequest(http.MethodGet, "http://nas.test/setup/favicon.svg", nil)
	rec := httptest.NewRecorder()

	setup.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/svg+xml" {
		t.Fatalf("unexpected content type: %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "<svg") {
		t.Fatalf("expected svg body, got: %s", rec.Body.String())
	}
}

func TestSetupHandlerSavesFIPSNsec(t *testing.T) {
	connector := &Connector{}
	setup, key, now := newSignedSetup(t, connector)
	setup.FIPSNsecPath = filepath.Join(t.TempDir(), "fips", "nsec")
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
	payload, err := json.Marshal(map[string]string{"nsec": nsec})
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}

	url := "http://nas.test/setup/api/fips-nsec"
	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(string(payload)))
	req.Header.Set("Authorization", signedHeader(t, key, url, http.MethodPost, now))
	rec := httptest.NewRecorder()

	setup.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if body["npub"] != wantNpub || body["saved"] != true {
		t.Fatalf("unexpected response: %#v", body)
	}
	saved, err := os.ReadFile(setup.FIPSNsecPath)
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

func TestSetupHandlerRejectsUnsignedSave(t *testing.T) {
	setup := SetupHandler{
		Connector:  &Connector{},
		ConfigPath: filepath.Join(t.TempDir(), "connector-config.json"),
		Auth:       adminauth.NewAuthorizer([]string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, time.Minute),
	}
	req := httptest.NewRequest(http.MethodPut, "http://nas.test/setup/api/config", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	setup.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSetupHandlerRejectsInvalidFipsPeerNpub(t *testing.T) {
	setup, key, now := newSignedSetup(t, &Connector{})
	url := "http://nas.test/setup/api/config"
	body := `{"fips_peer_npub":"not-a-npub"}`
	req := httptest.NewRequest(http.MethodPut, url, strings.NewReader(body))
	req.Header.Set("Authorization", signedHeader(t, key, url, http.MethodPut, now))
	rec := httptest.NewRecorder()

	setup.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSetupHandlerRejectsInvalidFipsPeerAddr(t *testing.T) {
	peerKey := nostr.GeneratePrivateKey()
	peerNpub, err := nostr.GetPublicKey(peerKey)
	if err != nil {
		t.Fatalf("GetPublicKey returned error: %v", err)
	}
	peerNpub, err = nip19.EncodePublicKey(peerNpub)
	if err != nil {
		t.Fatalf("EncodePublicKey returned error: %v", err)
	}

	setup, key, now := newSignedSetup(t, &Connector{})
	url := "http://nas.test/setup/api/config"
	body := `{"fips_peer_npub":"` + peerNpub + `","fips_peer_addr":"not-host-port"}`
	req := httptest.NewRequest(http.MethodPut, url, strings.NewReader(body))
	req.Header.Set("Authorization", signedHeader(t, key, url, http.MethodPut, now))
	rec := httptest.NewRecorder()

	setup.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSetupHandlerPreservesExistingSecretsOnBlankSave(t *testing.T) {
	connector := &Connector{}
	connector.SetMediaConfig(ConnectorMediaConfig{
		JellyfinBaseURL: "http://jellyfin.local:8096",
		JellyfinAPIKey:  "existing-jellyfin-key",
		PlexBaseURL:     "http://plex.local:32400",
		PlexToken:       "existing-plex-token",
	})
	setup, key, now := newSignedSetup(t, connector)
	url := "http://nas.test/setup/api/config"
	body := `{"jellyfin_base_url":"http://jellyfin.local:8096","plex_base_url":"http://plex.local:32400"}`
	req := httptest.NewRequest(http.MethodPut, url, strings.NewReader(body))
	req.Header.Set("Authorization", signedHeader(t, key, url, http.MethodPut, now))
	rec := httptest.NewRecorder()

	setup.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cfg := connector.MediaConfig()
	if cfg.JellyfinAPIKey != "existing-jellyfin-key" || cfg.PlexToken != "existing-plex-token" {
		t.Fatalf("expected secrets to be preserved, got %#v", cfg)
	}
}

func TestSetupHandlerClearsSecretsWhenServiceIsDisabled(t *testing.T) {
	connector := &Connector{}
	connector.SetMediaConfig(ConnectorMediaConfig{
		JellyfinBaseURL: "http://jellyfin.local:8096",
		JellyfinAPIKey:  "existing-jellyfin-key",
		PlexBaseURL:     "http://plex.local:32400",
		PlexToken:       "existing-plex-token",
	})
	setup, key, now := newSignedSetup(t, connector)
	url := "http://nas.test/setup/api/config"
	body := `{"jellyfin_base_url":"","plex_base_url":"http://plex.local:32400"}`
	req := httptest.NewRequest(http.MethodPut, url, strings.NewReader(body))
	req.Header.Set("Authorization", signedHeader(t, key, url, http.MethodPut, now))
	rec := httptest.NewRecorder()

	setup.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cfg := connector.MediaConfig()
	if cfg.JellyfinAPIKey != "" || cfg.PlexToken != "existing-plex-token" {
		t.Fatalf("expected disabled service secret to clear, got %#v", cfg)
	}
}

func TestSetupHandlerTestsSubmittedConfig(t *testing.T) {
	var gotPath, gotToken string
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotToken = r.Header.Get("X-Emby-Token")
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})
	connector := &Connector{HTTPClient: clientFor(upstream)}
	setup, key, now := newSignedSetup(t, connector)
	url := "http://nas.test/setup/api/test/jellyfin"
	body := `{"jellyfin_base_url":"http://jellyfin.test","jellyfin_api_key":"submitted-key"}`
	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Authorization", signedHeader(t, key, url, http.MethodPost, now))
	rec := httptest.NewRecorder()

	setup.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/System/Info" || gotToken != "submitted-key" {
		t.Fatalf("unexpected test request path=%q token=%q", gotPath, gotToken)
	}
}

func TestSetupHandlerFipsPeerCheck(t *testing.T) {
	setup, key, now := newSignedSetup(t, &Connector{})
	payload := map[string]string{
		"fips_peer_npub": "",
		"fips_peer_addr": "relay.example.org:8443",
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	url := "http://nas.test/setup/api/fips-peer-check"
	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(string(raw)))
	req.Header.Set("Authorization", signedHeader(t, key, url, http.MethodPost, now))
	rec := httptest.NewRecorder()

	setup.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var bodyResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &bodyResp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if bodyResp["ok"] != false {
		t.Fatalf("expected ok=false, got %#v", bodyResp["ok"])
	}
	check, ok := bodyResp["check"].(map[string]any)
	if !ok {
		t.Fatalf("expected check payload: %#v", bodyResp["check"])
	}
	if check["peer_npub_ok"] != false {
		t.Fatalf("expected peer_npub_ok=false, got %#v", check["peer_npub_ok"])
	}
}

func TestSetupHandlerStatusReportsFIPSPeerConnectivity(t *testing.T) {
	setup := SetupHandler{Connector: &Connector{}}
	req := httptest.NewRequest(http.MethodGet, "http://nas.test/setup/api/status", nil)
	rec := httptest.NewRecorder()

	setup.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	fipsPeer, ok := body["fips_peer"].(map[string]any)
	if !ok {
		t.Fatalf("expected fips_peer payload, got %#v", body["fips_peer"])
	}
	check, ok := fipsPeer["check"].(map[string]any)
	if !ok {
		t.Fatalf("expected fips_peer.check payload, got %#v", fipsPeer["check"])
	}
	if check["peer_npub_ok"] != false {
		t.Fatalf("expected peer_npub_ok=false, got %#v", check["peer_npub_ok"])
	}
	if check["error"] == nil {
		t.Fatalf("expected connectivity error when peer npub is unset")
	}
}

func TestConnectorRestrictsRemoteAddressAndToken(t *testing.T) {
	_, allowedNetwork, err := net.ParseCIDR("10.77.0.1/32")
	if err != nil {
		t.Fatalf("ParseCIDR returned error: %v", err)
	}
	connector := Connector{
		AllowedCIDRs: []*net.IPNet{allowedNetwork},
		SharedToken:  "secret",
	}

	tests := []struct {
		name       string
		remoteAddr string
		token      string
		wantStatus int
	}{
		{name: "wrong remote", remoteAddr: "10.77.0.2:1234", token: "secret", wantStatus: http.StatusForbidden},
		{name: "missing token", remoteAddr: "10.77.0.1:1234", wantStatus: http.StatusUnauthorized},
		{name: "allowed", remoteAddr: "10.77.0.1:1234", token: "secret", wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://connector.test/connector/api/status", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			rec := httptest.NewRecorder()

			connector.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tt.wantStatus, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestConnectorSearchesJellyfinThroughCuratedRoute(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Items" {
			t.Fatalf("unexpected upstream path %s", r.URL.Path)
		}
		if r.URL.Query().Get("SearchTerm") != "matrix" {
			t.Fatalf("unexpected query %s", r.URL.RawQuery)
		}
		if r.Header.Get("X-Emby-Token") != "jellyfin-token" {
			t.Fatalf("missing jellyfin token")
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"Items": []map[string]string{
				{"Id": "item123", "Name": "The Matrix", "Type": "Movie", "Overview": "Wake up."},
			},
		})
	})

	connector := Connector{
		JellyfinBaseURL: "http://jellyfin.test",
		JellyfinAPIKey:  "jellyfin-token",
		HTTPClient:      clientFor(upstream),
	}
	req := httptest.NewRequest(http.MethodGet, "http://connector.test/connector/api/services/jellyfin/search?q=matrix", nil)
	rec := httptest.NewRecorder()

	connector.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []MediaItem `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].StreamID != "item123" {
		t.Fatalf("unexpected items: %+v", body.Items)
	}
}

func TestConnectorStreamsPlexOnlyFromValidatedPartPath(t *testing.T) {
	var gotPath, gotRange string
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRange = r.Header.Get("Range")
		if r.URL.Query().Get("X-Plex-Token") != "plex-token" {
			t.Fatalf("missing plex token")
		}
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Range", "bytes 0-9/20")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("0123456789"))
	})

	connector := Connector{
		PlexBaseURL: "http://plex.test",
		PlexToken:   "plex-token",
		HTTPClient:  clientFor(upstream),
	}
	partPath := "/library/parts/123/456/file.mp4"
	streamID := base64.RawURLEncoding.EncodeToString([]byte(partPath))
	req := httptest.NewRequest(http.MethodGet, "http://connector.test/connector/api/services/plex/stream/"+streamID, nil)
	req.Header.Set("Range", "bytes=0-9")
	rec := httptest.NewRecorder()

	connector.ServeHTTP(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("expected status 206, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotPath != partPath || gotRange != "bytes=0-9" || strings.TrimSpace(rec.Body.String()) != "0123456789" {
		t.Fatalf("unexpected upstream path=%q range=%q body=%q", gotPath, gotRange, rec.Body.String())
	}

	badID := base64.RawURLEncoding.EncodeToString([]byte("/library/metadata/123"))
	badReq := httptest.NewRequest(http.MethodGet, "http://connector.test/connector/api/services/plex/stream/"+badID, nil)
	badRec := httptest.NewRecorder()

	connector.ServeHTTP(badRec, badReq)

	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid plex stream path to be rejected, got %d", badRec.Code)
	}
}
