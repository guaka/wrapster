package proxy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/trustroots/nostroots/vibe/wrapster/internal/access"
)

func newTestProxy(targets map[string]string, defaultTarget string) *Proxy {
	return New(Config{
		DefaultTarget:   defaultTarget,
		Targets:         targets,
		UpstreamTimeout: time.Second,
		MaxBodyBytes:    32,
	})
}

func newPrefixedTestProxy(targets map[string]string, defaultTarget string) *Proxy {
	return New(Config{
		Prefix:          "/proxy",
		DefaultTarget:   defaultTarget,
		Targets:         targets,
		UpstreamTimeout: time.Second,
		MaxBodyBytes:    32,
	})
}

func startEchoUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("missing"))
			return
		}
		if r.URL.Path == "/binary" {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte{0, 1, 2, 255})
			return
		}
		if r.URL.Path == "/cookie" {
			w.Header().Add("Set-Cookie", "sid=abc; Domain=example.org; Path=/; SameSite=Strict; Secure; HttpOnly")
			w.Header().Add("Set-Cookie", "theme=light; Path=/old")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/redirect" {
			w.Header().Set("Location", "/welcome?ok=1")
			w.WriteHeader(http.StatusFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("grpc-status", "0")
		w.Header().Set("grpc-message", "ok")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"method":        r.Method,
			"path":          r.URL.RequestURI(),
			"authorization": r.Header.Get("Authorization"),
			"content_type":  r.Header.Get("Content-Type"),
			"grpc_web":      r.Header.Get("X-Grpc-Web"),
			"user_agent":    r.Header.Get("X-User-Agent"),
			"body":          string(body),
		})
	}))
}

func TestRoutesPlatformPrefixAndStripsIt(t *testing.T) {
	a := startEchoUpstream(t)
	defer a.Close()
	b := startEchoUpstream(t)
	defer b.Close()
	server := httptest.NewServer(newTestProxy(map[string]string{"trustroots": a.URL, "hitchwiki.org": b.URL}, ""))
	defer server.Close()

	res, err := http.Get(server.URL + "/hitchwiki.org/wiki/Paris?printable=yes")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got map[string]string
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["path"] != "/wiki/Paris?printable=yes" {
		t.Fatalf("path = %q", got["path"])
	}
}

func TestMountedProxyRoutesAndStripsProxyPrefix(t *testing.T) {
	a := startEchoUpstream(t)
	defer a.Close()
	b := startEchoUpstream(t)
	defer b.Close()
	server := httptest.NewServer(newPrefixedTestProxy(map[string]string{"trustroots": a.URL, "hitchwiki.org": b.URL}, ""))
	defer server.Close()

	res, err := http.Get(server.URL + "/proxy/hitchwiki.org/wiki/Paris?printable=yes")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got map[string]string
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["path"] != "/wiki/Paris?printable=yes" {
		t.Fatalf("path = %q", got["path"])
	}
}

func TestTargetUserinfoInjectsUpstreamBasicAuth(t *testing.T) {
	upstream := startEchoUpstream(t)
	defer upstream.Close()

	withCreds, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	withCreds.User = url.UserPassword("convidado", "bemvindo")

	server := httptest.NewServer(newTestProxy(map[string]string{"wiki.melancia.org": withCreds.String()}, ""))
	defer server.Close()

	res, err := http.Get(server.URL + "/wiki.melancia.org/api.php?action=query")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got map[string]string
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("convidado:bemvindo"))
	if got["authorization"] != want {
		t.Fatalf("authorization = %q, want %q", got["authorization"], want)
	}
}

func TestClientAuthorizationOverridesTargetUserinfo(t *testing.T) {
	upstream := startEchoUpstream(t)
	defer upstream.Close()

	withCreds, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	withCreds.User = url.UserPassword("convidado", "bemvindo")

	server := httptest.NewServer(newTestProxy(map[string]string{"wiki.melancia.org": withCreds.String()}, ""))
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/wiki.melancia.org/api.php", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer client-token")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got map[string]string
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["authorization"] != "Bearer client-token" {
		t.Fatalf("authorization = %q, want client token to take precedence", got["authorization"])
	}
}

func TestAccessRuleRequiresNIP98Authorization(t *testing.T) {
	upstream := startEchoUpstream(t)
	defer upstream.Close()
	server := httptest.NewServer(New(Config{
		Prefix:          "/proxy",
		Targets:         map[string]string{"trustroots": upstream.URL},
		AccessRules:     []string{"trustroots_nip05"},
		Access:          access.Authorizer{Rules: map[string]access.Rule{"trustroots_nip05": {Type: access.RuleTrustrootsNIP05}}},
		UpstreamTimeout: time.Second,
		MaxBodyBytes:    32,
	}))
	defer server.Close()

	res, err := http.Get(server.URL + "/proxy/trustroots/api")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", res.StatusCode)
	}
}

func TestAccessRuleAllowsAndStripsNIP98Authorization(t *testing.T) {
	key := nostr.GeneratePrivateKey()
	pubkey, err := nostr.GetPublicKey(key)
	if err != nil {
		t.Fatalf("GetPublicKey returned error: %v", err)
	}
	now := time.Unix(1700000000, 0)
	upstream := startEchoUpstream(t)
	defer upstream.Close()
	server := httptest.NewServer(New(Config{
		Prefix:      "/proxy",
		Targets:     map[string]string{"trustroots": upstream.URL},
		AccessRules: []string{"trustroots_nip05"},
		Access: access.Authorizer{
			Rules:  map[string]access.Rule{"trustroots_nip05": {Type: access.RuleTrustrootsNIP05}},
			MaxAge: time.Minute,
			Now:    func() time.Time { return now },
			TrustrootsVerifier: func(_ context.Context, _ access.Rule, gotPubkey string) error {
				if gotPubkey != pubkey {
					t.Fatalf("pubkey = %q, want %q", gotPubkey, pubkey)
				}
				return nil
			},
		},
		UpstreamTimeout: time.Second,
		MaxBodyBytes:    32,
	}))
	defer server.Close()

	url := server.URL + "/proxy/trustroots/api"
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", signedProxyNIP98Header(t, key, url, http.MethodGet, now))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	var got map[string]string
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["authorization"] != "" {
		t.Fatalf("authorization leaked upstream: %q", got["authorization"])
	}
}

func TestUnprefixedFallback(t *testing.T) {
	upstream := startEchoUpstream(t)
	defer upstream.Close()
	server := httptest.NewServer(newTestProxy(nil, upstream.URL))
	defer server.Close()

	res, err := http.Get(server.URL + "/api/users/alice")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got map[string]string
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["path"] != "/api/users/alice" {
		t.Fatalf("path = %q", got["path"])
	}
}

func TestMountedProxyFallback(t *testing.T) {
	upstream := startEchoUpstream(t)
	defer upstream.Close()
	server := httptest.NewServer(newPrefixedTestProxy(nil, upstream.URL))
	defer server.Close()

	res, err := http.Get(server.URL + "/proxy/api/users/alice")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got map[string]string
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["path"] != "/api/users/alice" {
		t.Fatalf("path = %q", got["path"])
	}
}

func TestUnknownPlatformReturns404(t *testing.T) {
	upstream := startEchoUpstream(t)
	defer upstream.Close()
	server := httptest.NewServer(newTestProxy(map[string]string{"trustroots": upstream.URL}, upstream.URL))
	defer server.Close()

	res, err := http.Get(server.URL + "/unknown/api/x")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", res.StatusCode)
	}
}

func TestCORSPreflightAndCredentials(t *testing.T) {
	upstream := startEchoUpstream(t)
	defer upstream.Close()
	p := newTestProxy(map[string]string{"trustroots": upstream.URL}, "")
	p.AllowedOrigins = map[string]struct{}{"https://example.github.io": {}}
	server := httptest.NewServer(p)
	defer server.Close()

	req, _ := http.NewRequest(http.MethodOptions, server.URL+"/trustroots/api/x", nil)
	req.Header.Set("Origin", "https://example.github.io")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if got := res.Header.Get("Access-Control-Allow-Origin"); got != "https://example.github.io" {
		t.Fatalf("allow origin = %q", got)
	}
	if got := res.Header.Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("allow credentials = %q", got)
	}
	if got := res.Header.Get("Access-Control-Allow-Headers"); got == "" {
		t.Fatal("missing allow headers")
	}
}

func TestDisallowedOriginReturns403(t *testing.T) {
	upstream := startEchoUpstream(t)
	defer upstream.Close()
	p := newTestProxy(map[string]string{"trustroots": upstream.URL}, "")
	p.AllowedOrigins = map[string]struct{}{"https://allowed.example": {}}
	server := httptest.NewServer(p)
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/trustroots/api/x", nil)
	req.Header.Set("Origin", "https://other.example")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d", res.StatusCode)
	}
}

func TestForwardedHeadersAndPostBody(t *testing.T) {
	upstream := startEchoUpstream(t)
	defer upstream.Close()
	server := httptest.NewServer(newTestProxy(map[string]string{"nomadwiki.org": upstream.URL}, ""))
	defer server.Close()

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/nomadwiki.org/api/pages", bytes.NewBufferString(`{"u":"a"}`))
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Grpc-Web", "1")
	req.Header.Set("X-User-Agent", "wiki-client")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got map[string]string
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["authorization"] != "Bearer tok" || got["content_type"] != "application/json" || got["grpc_web"] != "1" || got["user_agent"] != "wiki-client" {
		t.Fatalf("headers not forwarded: %#v", got)
	}
	if got["body"] != `{"u":"a"}` {
		t.Fatalf("body = %q", got["body"])
	}
	if res.Header.Get("grpc-status") != "0" || res.Header.Get("grpc-message") != "ok" {
		t.Fatalf("grpc headers not exposed: %v", res.Header)
	}
}

func TestBodyLimit(t *testing.T) {
	upstream := startEchoUpstream(t)
	defer upstream.Close()
	server := httptest.NewServer(newTestProxy(map[string]string{"trustroots": upstream.URL}, ""))
	defer server.Close()

	res, err := http.Post(server.URL+"/trustroots/api/x", "text/plain", bytes.NewBufferString("this body is longer than thirty two bytes"))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", res.StatusCode)
	}
}

func TestUpstreamStatusAndBinaryPassthrough(t *testing.T) {
	upstream := startEchoUpstream(t)
	defer upstream.Close()
	server := httptest.NewServer(newTestProxy(map[string]string{"trustroots": upstream.URL}, ""))
	defer server.Close()

	missing, err := http.Get(server.URL + "/trustroots/missing")
	if err != nil {
		t.Fatal(err)
	}
	defer missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("missing status = %d", missing.StatusCode)
	}

	binary, err := http.Get(server.URL + "/trustroots/binary")
	if err != nil {
		t.Fatal(err)
	}
	defer binary.Body.Close()
	body, _ := io.ReadAll(binary.Body)
	if !bytes.Equal(body, []byte{0, 1, 2, 255}) {
		t.Fatalf("binary body = %#v", body)
	}
}

func TestCookieRewriteHTTPAndHTTPS(t *testing.T) {
	upstream := startEchoUpstream(t)
	defer upstream.Close()
	server := httptest.NewServer(newTestProxy(map[string]string{"trustroots": upstream.URL}, ""))
	defer server.Close()

	res, err := http.Get(server.URL + "/trustroots/cookie")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	cookies := res.Header.Values("Set-Cookie")
	if len(cookies) != 2 {
		t.Fatalf("cookies = %#v", cookies)
	}
	if cookies[0] != "sid=abc; HttpOnly; Path=/trustroots; SameSite=Lax" {
		t.Fatalf("http cookie = %q", cookies[0])
	}

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/trustroots/cookie", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	httpsRes, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer httpsRes.Body.Close()
	httpsCookies := httpsRes.Header.Values("Set-Cookie")
	if httpsCookies[0] != "sid=abc; HttpOnly; Path=/trustroots; SameSite=None; Secure" {
		t.Fatalf("https cookie = %q", httpsCookies[0])
	}
}

func TestMountedProxyCookieAndRedirectUsePublicPrefix(t *testing.T) {
	upstream := startEchoUpstream(t)
	defer upstream.Close()
	server := httptest.NewServer(newPrefixedTestProxy(map[string]string{"trustroots": upstream.URL}, upstream.URL))
	defer server.Close()

	res, err := http.Get(server.URL + "/proxy/trustroots/cookie")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	cookies := res.Header.Values("Set-Cookie")
	if cookies[0] != "sid=abc; HttpOnly; Path=/proxy/trustroots; SameSite=Lax" {
		t.Fatalf("mounted cookie = %q", cookies[0])
	}

	client := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	redirect, err := client.Get(server.URL + "/proxy/trustroots/redirect")
	if err != nil {
		t.Fatal(err)
	}
	defer redirect.Body.Close()
	if got := redirect.Header.Get("Location"); got != "/proxy/trustroots/welcome?ok=1" {
		t.Fatalf("mounted location = %q", got)
	}
}

func TestRedirectLocationRewrite(t *testing.T) {
	upstream := startEchoUpstream(t)
	defer upstream.Close()
	server := httptest.NewServer(newTestProxy(map[string]string{"wiki.trustroots.org": upstream.URL}, ""))
	defer server.Close()
	client := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	res, err := client.Get(server.URL + "/wiki.trustroots.org/redirect")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if got := res.Header.Get("Location"); got != "/wiki.trustroots.org/welcome?ok=1" {
		t.Fatalf("location = %q", got)
	}
}

func TestUnsupportedMethod(t *testing.T) {
	upstream := startEchoUpstream(t)
	defer upstream.Close()
	server := httptest.NewServer(newTestProxy(map[string]string{"trustroots": upstream.URL}, ""))
	defer server.Close()

	req, _ := http.NewRequest(http.MethodPatch, server.URL+"/trustroots/api/x", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", res.StatusCode)
	}
}

func TestHealthz(t *testing.T) {
	upstream := startEchoUpstream(t)
	defer upstream.Close()
	server := httptest.NewServer(newTestProxy(map[string]string{"trustroots": upstream.URL}, upstream.URL))
	defer server.Close()

	res, err := http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["service"] != "generic-proxy" {
		t.Fatalf("service = %v", got["service"])
	}
}

func TestHealthzRedactsTargetUserinfo(t *testing.T) {
	upstream := startEchoUpstream(t)
	defer upstream.Close()
	withCreds, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	withCreds.User = url.UserPassword("convidado", "bemvindo")
	server := httptest.NewServer(newTestProxy(map[string]string{"wiki.melancia.org": withCreds.String()}, withCreds.String()))
	defer server.Close()

	res, err := http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte("convidado")) || bytes.Contains(body, []byte("bemvindo")) {
		t.Fatalf("health response leaked target userinfo: %s", body)
	}
	if !bytes.Contains(body, []byte(upstream.URL)) {
		t.Fatalf("health response = %s, want redacted upstream URL", body)
	}
}

func TestRedactTargetErrorRemovesGoRedactedUserinfo(t *testing.T) {
	target := "https://convidado:bemvindo@wiki.melancia.org"
	err := errors.New(`Head "https://convidado:***@wiki.melancia.org": context deadline exceeded`)
	got := redactTargetError(target, err)
	if strings.Contains(got, "convidado") || strings.Contains(got, "bemvindo") {
		t.Fatalf("redactTargetError leaked userinfo: %q", got)
	}
	if !strings.Contains(got, "https://wiki.melancia.org") {
		t.Fatalf("redactTargetError = %q, want redacted URL", got)
	}
}

func signedProxyNIP98Header(t *testing.T, privateKey, url, method string, createdAt time.Time) string {
	t.Helper()
	event := nostr.Event{
		CreatedAt: nostr.Timestamp(createdAt.Unix()),
		Kind:      27235,
		Tags:      nostr.Tags{{"u", url}, {"method", method}},
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
