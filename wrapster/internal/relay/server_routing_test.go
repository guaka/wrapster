package relay

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	adminauth "github.com/trustroots/nostroots/vibe/wrapster/internal/admin"
	"github.com/trustroots/nostroots/vibe/wrapster/internal/proxy"
)

func TestServerRoutesProxyUnderProxyPrefix(t *testing.T) {
	upstream := startProxyEchoUpstream(t)
	defer upstream.Close()

	server := &Server{
		GenericProxy: proxy.New(proxy.Config{
			Prefix:          "/proxy",
			DefaultTarget:   upstream.URL,
			Targets:         map[string]string{"trustroots": upstream.URL},
			UpstreamTimeout: time.Second,
			MaxBodyBytes:    1024,
		}),
	}

	prefixed := requestJSON(t, server, "/proxy/trustroots/api/users/alice?ok=1")
	if prefixed["path"] != "/api/users/alice?ok=1" {
		t.Fatalf("prefixed path = %q", prefixed["path"])
	}

	fallback := requestJSON(t, server, "/proxy/api/users/bob")
	if fallback["path"] != "/api/users/bob" {
		t.Fatalf("fallback path = %q", fallback["path"])
	}
}

func TestServiceDirectoryUsesConfiguredRelayURL(t *testing.T) {
	server := &Server{PublicRelayURL: "ws://localhost:5542"}
	req := httptest.NewRequest(http.MethodGet, "http://wrapster.test/examples/service-directory.html", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Service Directory") {
		t.Fatalf("expected service directory HTML")
	}
	if !strings.Contains(body, "ws://localhost:5542") {
		t.Fatalf("expected configured relay URL in service directory HTML")
	}
	if !strings.Contains(body, "is the Wrapster instance serving this page") {
		t.Fatalf("expected service directory HTML to describe the configured relay")
	}
	if !strings.Contains(body, "Each line is queried independently") {
		t.Fatalf("expected service directory HTML to explain relay querying")
	}
	if strings.Contains(body, "wss://relay.guaka.org") {
		t.Fatalf("expected service directory HTML not to hardcode production relay")
	}
}

func TestServiceDirectorySupportsNIP42WithNIP07(t *testing.T) {
	server := &Server{PublicRelayURL: "wss://nip42.trustroots.org"}
	req := httptest.NewRequest(http.MethodGet, "http://wrapster.test/examples/service-directory.html", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`window.addEventListener("load", autoRefresh)`,
		`waitForNostr`,
		`window.nostr.signEvent`,
		`kind: 22242`,
		`["AUTH", event]`,
		`["relay", relay]`,
		`["challenge", String(challenge)]`,
		`requires NIP-42 auth with a NIP-07 signer whose pubkey resolves through Trustroots NIP-05`,
		`Access is allowed when your NIP-07 pubkey has Trustroots NIP-05.`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected service directory HTML to contain %q", want)
		}
	}
}

func TestServerReservedRoutesAreNotProxied(t *testing.T) {
	proxyHits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits++
		_ = json.NewEncoder(w).Encode(map[string]string{"path": r.URL.RequestURI()})
	}))
	defer upstream.Close()

	server := &Server{
		GenericProxy: proxy.New(proxy.Config{
			Prefix:          "/proxy",
			DefaultTarget:   upstream.URL,
			Targets:         map[string]string{"trustroots": upstream.URL},
			UpstreamTimeout: time.Second,
			MaxBodyBytes:    1024,
		}),
	}

	tests := []struct {
		name       string
		path       string
		headers    map[string]string
		wantStatus int
	}{
		{name: "service directory", path: "/examples/service-directory.html", wantStatus: http.StatusOK},
		{name: "old service advert browser route", path: "/examples/service-advert-browser.html", wantStatus: http.StatusNotFound},
		{name: "admin index", path: adminauth.AdminRoute, wantStatus: http.StatusOK},
		{name: "admin api", path: adminauth.AdminAPIPolicy, wantStatus: http.StatusUnauthorized},
		{name: "media api", path: "/media/api/status", wantStatus: http.StatusUnauthorized},
		{name: "unknown", path: "/not-proxy", wantStatus: http.StatusNotFound},
		{
			name:       "nip11",
			path:       "/",
			headers:    map[string]string{"Accept": "application/nostr+json"},
			wantStatus: http.StatusOK,
		},
		{
			name: "websocket",
			path: "/",
			headers: map[string]string{
				"Connection": "Upgrade",
				"Upgrade":    "websocket",
			},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://wrapster.test"+tt.path, nil)
			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}
			rec := httptest.NewRecorder()

			server.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tt.wantStatus, rec.Code, rec.Body.String())
			}
		})
	}
	if proxyHits != 0 {
		t.Fatalf("reserved routes hit proxy %d times", proxyHits)
	}
}

func TestServerCombinedHealthIncludesProxy(t *testing.T) {
	upstream := startProxyEchoUpstream(t)
	defer upstream.Close()
	server := &Server{
		Upstream: Upstream{URL: "ws://127.0.0.1:1"},
		GenericProxy: proxy.New(proxy.Config{
			Prefix:          "/proxy",
			DefaultTarget:   upstream.URL,
			Targets:         map[string]string{"trustroots": upstream.URL},
			UpstreamTimeout: time.Second,
			MaxBodyBytes:    1024,
		}),
	}

	req := httptest.NewRequest(http.MethodGet, "http://wrapster.test/healthz", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503 with unavailable relay upstream, got %d", rec.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["service"] != "wrapster" {
		t.Fatalf("service = %v", got["service"])
	}
	proxyStatus := got["proxy"].(map[string]any)
	if proxyStatus["enabled"] != true || proxyStatus["service"] != "generic-proxy" {
		t.Fatalf("proxy status = %+v", proxyStatus)
	}
}

func TestWebsocketOriginPolicy(t *testing.T) {
	server := &Server{}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	wsURL := strings.TrimPrefix(httpServer.URL, "http://")
	allowedOrigin := "http://" + wsURL
	ws := "ws://" + wsURL + "/"

	conn, _, err := websocket.DefaultDialer.Dial(ws, http.Header{"Origin": {allowedOrigin}})
	if err != nil {
		t.Fatalf("websocket with same-origin should be allowed: %v", err)
	}
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("expected challenge message, got: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close allowed websocket: %v", err)
	}

	conn, _, err = websocket.DefaultDialer.Dial(ws, nil)
	if err != nil {
		t.Fatalf("websocket without origin should be allowed: %v", err)
	}
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("expected challenge message, got: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close no-origin websocket: %v", err)
	}

	conn, res, err := websocket.DefaultDialer.Dial(ws, http.Header{"Origin": {"https://attacker.example"}})
	if err == nil {
		_ = conn.Close()
		t.Fatal("websocket with foreign origin should be rejected")
	}
	if res == nil || res.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for rejected websocket origin, got %+v", res)
	}
}

func TestIsAllowedWebSocketOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://wrapster.localhost/", nil)

	tests := []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{
			name:   "same host",
			host:   "wrapster.localhost",
			origin: "http://wrapster.localhost",
			want:   true,
		},
		{
			name:   "same host different port",
			host:   "wrapster.localhost:9443",
			origin: "ws://wrapster.localhost:7777",
			want:   true,
		},
		{
			name:   "subdomain rejected",
			host:   "wrapster.localhost",
			origin: "https://evil.wrapster.localhost",
			want:   false,
		},
		{
			name:   "bad scheme rejected",
			host:   "wrapster.localhost",
			origin: "https://",
			want:   false,
		},
		{
			name:   "empty origin allowed",
			host:   "wrapster.localhost",
			origin: "",
			want:   true,
		},
		{
			name:   "invalid request host rejected",
			origin: "http://wrapster.localhost",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.host != "" {
				req.Host = tt.host
			} else {
				req.Host = ""
			}
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			} else {
				req.Header.Del("Origin")
			}
			if got := isAllowedWebSocketOrigin(req); got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestIsAllowedWebSocketOriginAdditionalCases(t *testing.T) {
	baseReq := httptest.NewRequest(http.MethodGet, "http://wrapster.localhost/", nil)

	tests := []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{
			name:   "case-insensitive host allowed",
			host:   "WRAPSTER.LOCALHOST",
			origin: "http://wrapster.localhost",
			want:   true,
		},
		{
			name:   "same host with path in origin",
			host:   "wrapster.localhost",
			origin: "https://wrapster.localhost/some/path?x=1",
			want:   true,
		},
		{
			name:   "same ipv4 host",
			host:   "127.0.0.1:9999",
			origin: "ws://127.0.0.1:7777",
			want:   true,
		},
		{
			name:   "same ipv6 host",
			host:   "[::1]:9999",
			origin: "wss://[::1]:7777",
			want:   true,
		},
		{
			name:   "origin without host is rejected",
			host:   "wrapster.localhost",
			origin: "wrapster.localhost",
			want:   false,
		},
		{
			name:   "origin with spaces is trimmed",
			host:   "wrapster.localhost",
			origin: "  http://wrapster.localhost  ",
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := baseReq.Clone(baseReq.Context())
			req.Host = tt.host
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			} else {
				req.Header.Del("Origin")
			}
			if got := isAllowedWebSocketOrigin(req); got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func requestJSON(t *testing.T, server http.Handler, path string) map[string]string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://wrapster.test"+path, nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func startProxyEchoUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"method": strings.ToUpper(r.Method),
			"path":   r.URL.RequestURI(),
			"body":   string(body),
		})
	}))
}
