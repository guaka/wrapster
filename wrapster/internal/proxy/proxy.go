package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/trustroots/nostroots/vibe/wrapster/internal/access"
)

type Config struct {
	Prefix          string
	DefaultTarget   string
	Targets         map[string]string
	AllowedOrigins  []string
	UpstreamTimeout time.Duration
	MaxBodyBytes    int64
	AccessRules     []string
	Access          access.Authorizer
}

type Proxy struct {
	Prefix          string
	DefaultTarget   string
	Targets         map[string]string
	AllowedOrigins  map[string]struct{}
	UpstreamTimeout time.Duration
	MaxBodyBytes    int64
	Client          *http.Client
	AccessRules     []string
	Access          access.Authorizer
}

func New(cfg Config) *Proxy {
	allowed := make(map[string]struct{}, len(cfg.AllowedOrigins))
	for _, origin := range cfg.AllowedOrigins {
		allowed[origin] = struct{}{}
	}
	targets := make(map[string]string, len(cfg.Targets))
	for platform, target := range cfg.Targets {
		targets[platform] = strings.TrimRight(target, "/")
	}
	timeout := cfg.UpstreamTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	maxBody := cfg.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = 10 * 1024 * 1024
	}
	return &Proxy{
		Prefix:          cleanPrefix(cfg.Prefix),
		DefaultTarget:   strings.TrimRight(cfg.DefaultTarget, "/"),
		Targets:         targets,
		AllowedOrigins:  allowed,
		UpstreamTimeout: timeout,
		MaxBodyBytes:    maxBody,
		AccessRules:     cleanRuleNames(cfg.AccessRules),
		Access:          cfg.Access,
		Client: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func cleanRuleNames(ruleNames []string) []string {
	out := make([]string, 0, len(ruleNames))
	for _, ruleName := range ruleNames {
		if ruleName = strings.TrimSpace(ruleName); ruleName != "" {
			out = append(out, ruleName)
		}
	}
	return out
}

func cleanPrefix(prefix string) string {
	prefix = strings.TrimRight(prefix, "/")
	if prefix == "" || prefix == "/" {
		return ""
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	return prefix
}

var allowedMethods = map[string]struct{}{
	http.MethodGet:     {},
	http.MethodPost:    {},
	http.MethodPut:     {},
	http.MethodDelete:  {},
	http.MethodOptions: {},
}

var forwardedHeaders = []string{
	"authorization",
	"content-type",
	"accept",
	"cookie",
	"x-grpc-web",
	"x-user-agent",
}

const allowHeaders = "authorization,content-type,accept,cookie,x-grpc-web,x-user-agent"

type route struct {
	Upstream    string
	ForwardPath string
	Platform    string
	PathScope   string
	PublicBase  string
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cors, corsAllowed := p.corsFor(r)
	if p.isHealthPath(r.URL.Path) {
		p.health(w, cors)
		return
	}
	if r.Method == http.MethodOptions {
		if !corsAllowed {
			writeJSON(w, http.StatusForbidden, cors, map[string]string{"error": "origin_not_allowed"})
			return
		}
		for key, values := range cors {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !corsAllowed {
		writeJSON(w, http.StatusForbidden, cors, map[string]string{"error": "origin_not_allowed"})
		return
	}
	if _, ok := allowedMethods[r.Method]; !ok {
		headers := copyHeaders(cors)
		headers.Set("Allow", "GET,POST,PUT,DELETE,OPTIONS")
		writeJSON(w, http.StatusMethodNotAllowed, headers, map[string]string{"error": "method_not_allowed"})
		return
	}
	routed, ok := p.route(r.URL)
	if !ok {
		writeJSON(w, http.StatusNotFound, cors, map[string]string{"error": "no_upstream"})
		return
	}
	if len(p.AccessRules) > 0 {
		if _, err := p.Access.VerifyAllRequest(r, p.AccessRules); err != nil {
			writeJSON(w, access.HTTPStatus(err), cors, map[string]string{"error": err.Error()})
			return
		}
	}
	p.forward(w, r, routed, cors)
}

func (p *Proxy) route(reqURL *url.URL) (route, bool) {
	path := reqURL.EscapedPath()
	if path == "" {
		path = "/"
	}
	path, ok := p.stripPrefix(path)
	if !ok {
		return route{}, false
	}
	trimmed := strings.TrimPrefix(path, "/")
	segment := trimmed
	if i := strings.IndexByte(trimmed, '/'); i >= 0 {
		segment = trimmed[:i]
	}
	if target, ok := p.Targets[segment]; ok {
		rest := strings.TrimPrefix(trimmed[len(segment):], "/")
		forward := "/" + rest
		if rest == "" {
			forward = "/"
		}
		if reqURL.RawQuery != "" {
			forward += "?" + reqURL.RawQuery
		}
		return route{
			Upstream:    target,
			ForwardPath: forward,
			Platform:    segment,
			PathScope:   p.publicPath("/" + segment),
			PublicBase:  p.publicPath("/" + segment),
		}, true
	}
	if segment != "" && looksLikePlatform(segment) {
		return route{}, false
	}
	if p.DefaultTarget == "" {
		return route{}, false
	}
	forward := path
	if reqURL.RawQuery != "" {
		forward += "?" + reqURL.RawQuery
	}
	return route{Upstream: p.DefaultTarget, ForwardPath: forward, PathScope: p.publicPath(""), PublicBase: p.publicPath("")}, true
}

func (p *Proxy) stripPrefix(path string) (string, bool) {
	if p.Prefix == "" {
		return path, true
	}
	if path == p.Prefix {
		return "/", true
	}
	if strings.HasPrefix(path, p.Prefix+"/") {
		return strings.TrimPrefix(path, p.Prefix), true
	}
	return "", false
}

func (p *Proxy) isHealthPath(path string) bool {
	if p.Prefix == "" {
		return path == "/healthz"
	}
	return path == p.Prefix+"/healthz"
}

func (p *Proxy) publicPath(path string) string {
	if p.Prefix == "" {
		if path == "" {
			return "/"
		}
		return path
	}
	if path == "" || path == "/" {
		return p.Prefix
	}
	return p.Prefix + path
}

func looksLikePlatform(segment string) bool {
	return segment != "api" && segment != "assets" && segment != "images" && segment != "img" && segment != "static" && segment != "uploads" && segment != ""
}

func (p *Proxy) forward(w http.ResponseWriter, r *http.Request, routed route, cors http.Header) {
	var body io.Reader
	if r.Body != nil && r.Method != http.MethodGet {
		read, tooLarge, err := readBody(r.Body, p.MaxBodyBytes)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, cors, map[string]string{"error": "invalid_body", "message": err.Error()})
			return
		}
		if tooLarge {
			writeJSON(w, http.StatusRequestEntityTooLarge, cors, map[string]string{"error": "body_too_large"})
			return
		}
		if len(read) > 0 {
			body = bytes.NewReader(read)
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), p.UpstreamTimeout)
	defer cancel()
	upstreamReq, err := http.NewRequestWithContext(ctx, r.Method, routed.Upstream+routed.ForwardPath, body)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, cors, map[string]string{"error": "gateway_error", "message": err.Error()})
		return
	}
	for _, name := range forwardedHeaders {
		if value := r.Header.Get(name); value != "" {
			if strings.EqualFold(name, "authorization") && isNostrAuthorization(value) {
				continue
			}
			upstreamReq.Header.Set(name, value)
		}
	}

	upstream, err := p.Client.Do(upstreamReq)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, cors, map[string]string{"error": "gateway_error", "message": err.Error()})
		return
	}
	defer upstream.Body.Close()

	headers := copyHeaders(cors)
	copyUpstreamHeader(headers, upstream.Header, "content-type")
	copyUpstreamHeader(headers, upstream.Header, "content-length")
	copyUpstreamHeader(headers, upstream.Header, "grpc-status")
	copyUpstreamHeader(headers, upstream.Header, "grpc-message")
	if location := upstream.Header.Get("location"); location != "" {
		headers.Set("Location", rewriteLocation(location, routed))
	}
	for _, cookie := range rewriteSetCookies(upstream.Header.Values("Set-Cookie"), routed.PathScope, isHTTPSRequest(r)) {
		headers.Add("Set-Cookie", cookie)
	}

	for key, values := range headers {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(upstream.StatusCode)
	if _, err := io.Copy(w, upstream.Body); err != nil {
		return
	}
}

func isNostrAuthorization(value string) bool {
	scheme, _, ok := strings.Cut(strings.TrimSpace(value), " ")
	return ok && strings.EqualFold(scheme, "Nostr")
}

func (p *Proxy) corsFor(r *http.Request) (http.Header, bool) {
	origin := r.Header.Get("Origin")
	headers := http.Header{}
	headers.Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
	headers.Set("Access-Control-Allow-Headers", allowHeaders)
	headers.Set("Access-Control-Expose-Headers", "grpc-status,grpc-message")
	headers.Set("Access-Control-Max-Age", "86400")
	if origin == "" {
		headers.Set("Access-Control-Allow-Origin", "*")
		return headers, true
	}
	if len(p.AllowedOrigins) > 0 {
		if _, ok := p.AllowedOrigins[origin]; !ok {
			return headers, false
		}
	}
	headers.Set("Access-Control-Allow-Origin", origin)
	headers.Set("Access-Control-Allow-Credentials", "true")
	headers.Set("Vary", "Origin")
	return headers, true
}

func (p *Proxy) health(w http.ResponseWriter, cors http.Header) {
	writeJSON(w, http.StatusOK, cors, p.Status())
}

func (p *Proxy) Status() map[string]any {
	targets := make(map[string]string, len(p.Targets))
	for platform, target := range p.Targets {
		targets[platform] = target
	}
	checks := p.TargetChecks()
	return map[string]any{
		"service":                "generic-proxy",
		"prefix":                 p.Prefix,
		"targets":                targets,
		"target_health":          checks,
		"default_target_enabled": p.DefaultTarget != "",
		"default_target": map[string]any{
			"url": p.DefaultTarget,
			"ok":  checks["default"].OK,
		},
		"allowed_origins_configured": len(p.AllowedOrigins),
		"max_body_bytes":             p.MaxBodyBytes,
		"upstream_timeout":           p.UpstreamTimeout.String(),
	}
}

type TargetCheck struct {
	URL    string `json:"url"`
	OK     bool   `json:"ok"`
	Status int    `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
}

func (p *Proxy) TargetChecks() map[string]TargetCheck {
	checks := make(map[string]TargetCheck, len(p.Targets)+1)
	for platform, target := range p.Targets {
		checks[platform] = p.checkTarget(target)
	}
	if p.DefaultTarget != "" {
		checks["default"] = p.checkTarget(p.DefaultTarget)
	}
	return checks
}

func (p *Proxy) checkTarget(target string) TargetCheck {
	timeout := p.UpstreamTimeout
	if timeout > 2*time.Second {
		timeout = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, target, nil)
	if err != nil {
		return TargetCheck{URL: target, Error: err.Error()}
	}
	res, err := p.Client.Do(req)
	if err != nil {
		return TargetCheck{URL: target, Error: err.Error()}
	}
	defer res.Body.Close()
	return TargetCheck{URL: target, OK: res.StatusCode < 500, Status: res.StatusCode}
}

func rewriteLocation(location string, routed route) string {
	first := strings.Split(location, ",")[0]
	first = strings.TrimSpace(first)
	if routed.PublicBase == "" || (routed.Platform == "" && routed.PublicBase == "/") {
		return first
	}
	u, err := url.Parse(first)
	if err != nil {
		return routed.PublicBase + "/" + strings.TrimPrefix(first, "/")
	}
	if !u.IsAbs() {
		if strings.HasPrefix(first, "/") {
			return routed.PublicBase + first
		}
		return routed.PublicBase + "/" + first
	}
	return routed.PublicBase + u.EscapedPath() + querySuffix(u)
}

func querySuffix(u *url.URL) string {
	if u.RawQuery == "" {
		return ""
	}
	return "?" + u.RawQuery
}

func rewriteSetCookies(cookies []string, pathScope string, secureCrossSite bool) []string {
	out := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		parts := strings.Split(cookie, ";")
		if len(parts) == 0 {
			continue
		}
		rewritten := []string{strings.TrimSpace(parts[0])}
		for _, attr := range parts[1:] {
			attr = strings.TrimSpace(attr)
			lower := strings.ToLower(attr)
			if lower == "" ||
				strings.HasPrefix(lower, "domain=") ||
				strings.HasPrefix(lower, "path=") ||
				strings.HasPrefix(lower, "samesite=") ||
				lower == "secure" {
				continue
			}
			rewritten = append(rewritten, attr)
		}
		scope := pathScope
		if scope == "" {
			scope = "/"
		}
		rewritten = append(rewritten, "Path="+scope)
		if secureCrossSite {
			rewritten = append(rewritten, "SameSite=None", "Secure")
		} else {
			rewritten = append(rewritten, "SameSite=Lax")
		}
		out = append(out, strings.Join(rewritten, "; "))
	}
	return out
}

func isHTTPSRequest(r *http.Request) bool {
	return r.TLS != nil ||
		strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") ||
		strings.EqualFold(r.Header.Get("X-Forwarded-Ssl"), "on")
}

func copyUpstreamHeader(dst http.Header, src http.Header, name string) {
	if value := src.Get(name); value != "" {
		dst.Set(name, value)
	}
}

func copyHeaders(src http.Header) http.Header {
	dst := make(http.Header, len(src))
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
	return dst
}

func writeJSON(w http.ResponseWriter, status int, headers http.Header, body any) {
	for key, values := range headers {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func readBody(r io.Reader, max int64) ([]byte, bool, error) {
	limited := io.LimitReader(r, max+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, false, err
	}
	if int64(len(body)) > max {
		return nil, true, nil
	}
	return body, false, nil
}
