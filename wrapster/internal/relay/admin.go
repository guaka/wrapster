package relay

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/trustroots/nostroots/vibe/wrapster/internal/access"
	adminauth "github.com/trustroots/nostroots/vibe/wrapster/internal/admin"
	"github.com/trustroots/nostroots/vibe/wrapster/internal/adminui"
	"github.com/trustroots/nostroots/vibe/wrapster/internal/buildinfo"
	"github.com/trustroots/nostroots/vibe/wrapster/internal/fips"
	"github.com/trustroots/nostroots/vibe/wrapster/internal/proxy"
)

func (s *Server) adminIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	html := adminui.InjectShared(adminHTML)
	html = strings.ReplaceAll(html, "{{WRAPSTER_HUB_NAME}}", adminauth.WrapsterHubName)
	html = strings.ReplaceAll(html, "{{BUILD_TIME}}", buildinfo.DisplayBuildTime())
	_, _ = w.Write([]byte(html))
}

func (s *Server) favicon(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(faviconSVG))
}

func (s *Server) adminAPI(w http.ResponseWriter, r *http.Request) {
	pubkey, err := s.AdminAuth.VerifyRequest(r)
	if err != nil {
		status := http.StatusUnauthorized
		if errors.Is(err, adminauth.ErrNotAdmin) {
			status = http.StatusForbidden
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}

	switch r.URL.Path {
	case adminauth.AdminAPIOverview:
		if !requireAdminMethod(w, r, http.MethodGet) {
			return
		}
		s.adminOverview(w, r, pubkey)
	case adminauth.AdminAPIStatus:
		if !requireAdminMethod(w, r, http.MethodGet) {
			return
		}
		s.adminStatus(w, r, pubkey)
	case adminauth.AdminAPIIdentity:
		if !requireAdminMethod(w, r, http.MethodGet) {
			return
		}
		s.adminIdentity(w, r, pubkey)
	case adminauth.AdminAPIConfig:
		if !requireAdminMethod(w, r, http.MethodGet) {
			return
		}
		s.adminConfig(w, r, pubkey)
	case adminauth.AdminAPIAuthCache:
		if !requireAdminMethod(w, r, http.MethodGet) {
			return
		}
		s.adminAuthCache(w, r, pubkey)
	case adminauth.AdminAPIPolicy:
		if !requireAdminMethod(w, r, http.MethodGet) {
			return
		}
		s.adminPolicy(w, r, pubkey)
	case adminauth.AdminAPIFipsNsec:
		if !requireAdminMethod(w, r, http.MethodPost) {
			return
		}
		s.adminFIPSNsec(w, r, pubkey)
	case adminauth.AdminAPIFipsPeerCheck:
		if !requireAdminMethod(w, r, http.MethodPost) {
			return
		}
		s.adminFIPSPeerCheck(w, r, pubkey)
	default:
		http.NotFound(w, r)
	}
}

func requireAdminMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

func (s *Server) adminFIPSNsec(w http.ResponseWriter, r *http.Request, pubkey string) {
	_ = pubkey
	log.Printf("admin API called: %s", adminauth.AdminAPIFipsNsec)
	var payload struct {
		Nsec string `json:"nsec"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&payload); err != nil {
		log.Printf("failed to decode admin fips-nsec payload: %v", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	log.Printf("admin save fips-nsec request from %s", pubkey)
	identity, err := fips.SaveNsec(s.FIPSNsecPath, payload.Nsec)
	if err != nil {
		log.Printf("failed to save admin fips-nsec: %v", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"saved": true,
		"npub":  identity.Npub,
	})
}

func (s *Server) adminFIPSPeerCheck(w http.ResponseWriter, r *http.Request, pubkey string) {
	_ = pubkey
	var payload struct {
		Npub string `json:"fips_peer_npub"`
		Addr string `json:"fips_peer_addr"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&payload); err != nil {
		log.Printf("failed to decode admin fips-peer-check payload: %v", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	status := fips.CheckPeerConnectivityWithDebugRequirePeer(payload.Npub, payload.Addr)
	if errorText, shouldReject := fips.ConnectivityError(status); shouldReject {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errorText})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) adminOverview(w http.ResponseWriter, r *http.Request, pubkey string) {
	statusCtx, statusCancel := context.WithTimeout(r.Context(), time.Second)
	defer statusCancel()
	cacheCtx, cacheCancel := context.WithTimeout(r.Context(), time.Second)
	defer cacheCancel()
	identityCtx, identityCancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer identityCancel()

	cache, err := s.adminAuthCachePayload(cacheCtx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated_pubkey": pubkey,
		"status":               s.adminStatusPayload(statusCtx, pubkey),
		"identity":             s.adminIdentityPayload(identityCtx, pubkey),
		"auth_cache":           cache,
		"policy":               s.adminPolicyPayload(pubkey),
		"fips":                 s.adminFIPSPayload(),
		"config":               s.adminConfigPayload(),
	})
}

func (s *Server) adminFIPSPayload() map[string]any {
	path := strings.TrimSpace(s.FIPSNsecPath)
	peerList := fips.PeerList(s.FIPSPeerNpub, s.FIPSPeerAddr, nil)
	out := map[string]any{
		"peer_npub":            strings.TrimSpace(s.FIPSPeerNpub),
		"peer_addr":            strings.TrimSpace(s.FIPSPeerAddr),
		"peer_configured":      strings.TrimSpace(s.FIPSPeerNpub) != "",
		"peer_addr_configured": strings.TrimSpace(s.FIPSPeerAddr) != "",
		"peers":                peerList,
	}
	if path == "" {
		out["state"] = "not_configured"
		return out
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		out["state"] = "file_error"
		out["error"] = err.Error()
		return out
	}

	identity, err := fips.NsecIdentity(string(raw))
	if err != nil {
		out["state"] = "invalid"
		out["error"] = err.Error()
		return out
	}

	out["state"] = "configured"
	out["npub"] = identity.Npub
	return out
}

// adminIdentity resolves the NIP-05 identifier for the signed-in admin pubkey
// by looking up the profile on the relays this deployment uses, then verifying
// it through NIP-05.
func (s *Server) adminIdentity(w http.ResponseWriter, r *http.Request, pubkey string) {
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()

	writeJSON(w, http.StatusOK, s.adminIdentityPayload(ctx, pubkey))
}

func (s *Server) adminIdentityPayload(ctx context.Context, pubkey string) map[string]any {
	resp := map[string]any{
		"authenticated_pubkey": pubkey,
		"trustroots_username":  nil,
		"trustroots_nip05":     nil,
		"verified":             false,
	}

	username, err := s.Upstream.FindTrustrootsUsername(ctx, pubkey)
	if err == nil && username != "" {
		resp["trustroots_username"] = username
		identifier := username
		if domain := s.trustrootsDomain(); domain != "" {
			identifier = username + "@" + domain
		}
		resp["trustroots_nip05"] = identifier
		resp["verified"] = s.NIP05.Verify(ctx, username, pubkey) == nil
	}

	return resp
}

// adminConfig returns a secret-free view of the effective configuration
// (relays, proxy routes, access rules, media services) for the admin dashboard.
func (s *Server) adminConfig(w http.ResponseWriter, r *http.Request, pubkey string) {
	_ = pubkey
	writeJSON(w, http.StatusOK, s.adminConfigPayload())
}

func (s *Server) adminConfigPayload() map[string]any {
	lookupRelays := orEmpty(s.Upstream.ProfileRelays)
	resp := map[string]any{
		"relays": map[string]any{
			"upstream":   s.Upstream.URL,
			"additional": lookupRelays,
			"lookup":     lookupRelays,
		},
	}
	advertisable := []map[string]any{}

	if s.GenericProxy != nil {
		routes := map[string]string{}
		for route, target := range s.GenericProxy.Targets {
			routes[route] = proxy.RedactURLUserinfo(target)
		}
		resp["proxy"] = map[string]any{
			"prefix":          s.GenericProxy.Prefix,
			"access_rules":    orEmpty(s.GenericProxy.AccessRules),
			"routes":          routes,
			"default_target":  proxy.RedactURLUserinfo(s.GenericProxy.DefaultTarget),
			"allowed_origins": len(s.GenericProxy.AllowedOrigins),
		}
		advertisable = append(advertisable, map[string]any{
			"name":     "proxy",
			"label":    "Proxy",
			"service":  "cors-proxy",
			"title":    "Wrapster CORS Proxy",
			"summary":  "Allowlisted browser proxy for Trustroots and community wiki calls",
			"access":   "request",
			"audience": []string{"community", "trustroots"},
			"extra_tags": [][]string{
				{"endpoint", s.adminProxyEndpoint()},
			},
		})

		rules := map[string]any{}
		for name, rule := range s.GenericProxy.Access.Rules {
			entry := map[string]any{
				"type":  rule.Type,
				"relay": rule.RelayURL,
			}
			switch rule.Type {
			case access.RuleTrustrootsNIP05:
				entry["nip05_base_url"] = rule.NIP05BaseURL
			case access.RuleNostrFollow:
				entry["relationship"] = rule.Relationship
				entry["owner_pubkey"] = rule.OwnerPubkey
			}
			if len(rule.DenyPubkeys) > 0 {
				entry["deny_count"] = len(rule.DenyPubkeys)
			}
			rules[name] = entry
		}
		resp["access_rules"] = rules
	}

	services := map[string][]string{}
	for service, rules := range s.MediaGateway.ServiceAccessRules {
		services[service] = orEmpty(rules)
		advertisable = append(advertisable, map[string]any{
			"name":     service,
			"label":    adminServiceLabel(service),
			"service":  service,
			"title":    "Wrapster " + adminServiceLabel(service),
			"summary":  "Request-gated " + adminServiceLabel(service) + " access through Wrapster",
			"access":   "request",
			"audience": []string{"trustroots"},
		})
	}
	resp["media"] = map[string]any{
		"connector_configured": strings.TrimSpace(s.MediaGateway.ConnectorBaseURL) != "",
		"services":             services,
	}
	if len(s.WikiAdverts) > 0 {
		wikis := map[string]any{}
		for slug, wiki := range s.WikiAdverts {
			wikis[slug] = adminWikiPayload(slug, wiki)
			advertisable = append(advertisable, adminWikiAdvertisable(slug, wiki))
		}
		resp["wiki"] = wikis
	}
	resp["advertisable_services"] = advertisable

	return resp
}

func (s *Server) adminProxyEndpoint() string {
	if s.GenericProxy == nil {
		return ""
	}
	base, ok := adminNIP11HTTPURL(s.PublicRelayURL)
	if !ok {
		return ""
	}
	return strings.TrimRight(base, "/") + s.GenericProxy.Prefix
}

func adminWikiPayload(slug string, wiki WikiAdvertDraft) map[string]any {
	return map[string]any{
		"slug":                 slug,
		"origin":               wiki.Origin,
		"label":                wiki.Label,
		"summary":              wiki.Summary,
		"wiki_path":            wiki.WikiPath,
		"wiki_api_path":        wiki.WikiAPIPath,
		"wiki_load_path":       wiki.WikiLoadPath,
		"wiki_main_page_path":  wiki.WikiMainPagePath,
		"wiki_main_page_title": wiki.WikiMainPageTitle,
		"proxy_route":          wiki.ProxyRoute,
		"status":               wiki.Status,
		"audience":             orEmpty(wiki.Audience),
	}
}

func adminWikiAdvertisable(slug string, wiki WikiAdvertDraft) map[string]any {
	label := strings.TrimSpace(wiki.Label)
	if label == "" {
		label = adminServiceLabel(slug)
	}
	summary := strings.TrimSpace(wiki.Summary)
	if summary == "" {
		summary = "Public wiki available through Wrapster"
	}
	status := strings.TrimSpace(wiki.Status)
	if status == "" {
		status = "active"
	}
	audience := wiki.Audience
	if len(audience) == 0 {
		audience = []string{"community"}
	}
	return map[string]any{
		"name":        "wiki-" + slug,
		"label":       label,
		"service":     "wiki",
		"title":       label,
		"summary":     summary,
		"status":      status,
		"access":      "public",
		"audience":    audience,
		"advert_slug": slug,
		"note_services": []string{
			"wiki",
		},
		"extra_tags": [][]string{
			{"wiki_origin", wiki.Origin},
			{"wiki_path", wiki.WikiPath},
			{"wiki_api_path", wiki.WikiAPIPath},
			{"wiki_load_path", wiki.WikiLoadPath},
			{"wiki_main_page_path", wiki.WikiMainPagePath},
			{"wiki_main_page_title", wiki.WikiMainPageTitle},
			{"proxy_route", wiki.ProxyRoute},
		},
	}
}

func adminServiceLabel(service string) string {
	parts := strings.FieldsFunc(service, func(r rune) bool {
		return r == '-' || r == '_'
	})
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func orEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func (s *Server) trustrootsDomain() string {
	u, err := url.Parse(s.NIP05.BaseURL)
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Host), "www.")
}

func (s *Server) adminStatus(w http.ResponseWriter, r *http.Request, pubkey string) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()

	writeJSON(w, http.StatusOK, s.adminStatusPayload(ctx, pubkey))
}

func (s *Server) adminStatusPayload(ctx context.Context, pubkey string) map[string]any {
	cacheOK := s.Cache == nil || s.Cache.Ping(ctx) == nil
	strfry := s.adminStrfryStatusPayload(ctx)
	upstreamOK, _ := strfry["reachable"].(bool)
	mediaConnector := s.MediaGateway.ConnectorStatus(ctx)
	mediaConnectorOK, _ := mediaConnector["reachable"].(bool)

	return map[string]any{
		"service": map[string]any{
			"name":                  "wrapster",
			"description":           "NIP-42 authenticated relay wrapper with additional services",
			"authenticated_pubkey":  pubkey,
			"admin_pubkeys_count":   len(s.AdminAuth.Admins),
			"admin_auth_max_age_ms": s.AdminAuth.MaxAge.Milliseconds(),
		},
		"relay": map[string]any{
			"public_url":        s.PublicRelayURL,
			"upstream_url":      s.Upstream.URL,
			"auth_cache_ttl":    adminDuration(s.AuthCacheTTL),
			"auth_event_window": adminDuration(s.AuthEventMaxAge),
			"supported_nips":    filterNIPsForAdmin([]int{1, 5, 11, 42}),
			"recent_queries":    s.recentRelayQueries(),
		},
		"health": map[string]any{
			"cache":           cacheOK,
			"upstream":        upstreamOK,
			"media_connector": mediaConnectorOK,
		},
		"strfry":          strfry,
		"media_connector": mediaConnector,
	}
}

func (s *Server) adminStrfryStatusPayload(ctx context.Context) map[string]any {
	checkedAt := s.now().UTC().Format(time.RFC3339)
	start := time.Now()
	reachable := false
	var latency any
	lastError := ""

	if conn, err := s.Upstream.Dial(ctx); err == nil {
		reachable = true
		latency = time.Since(start).Milliseconds()
		_ = conn.Close()
	} else {
		lastError = err.Error()
	}

	return map[string]any{
		"url":        s.Upstream.URL,
		"reachable":  reachable,
		"latency_ms": latency,
		"checked_at": checkedAt,
		"last_error": lastError,
		"nip11":      adminStrfryNIP11(ctx, s.Upstream.URL),
	}
}

func adminStrfryNIP11(ctx context.Context, relayURL string) any {
	infoURL, ok := adminNIP11HTTPURL(relayURL)
	if !ok {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, infoURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Accept", "application/nostr+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	var payload map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return nil
	}
	if rawNips, ok := payload["supported_nips"].([]any); ok {
		nips := make([]int, 0, len(rawNips))
		for _, rawNip := range rawNips {
			nip, ok := rawNip.(float64)
			if !ok {
				continue
			}
			nips = append(nips, int(nip))
		}
		payload["supported_nips"] = filterNIPsForAdmin(nips)
	}
	return payload
}

func adminNIP11HTTPURL(relayURL string) (string, bool) {
	u, err := url.Parse(relayURL)
	if err != nil || u.Host == "" {
		return "", false
	}
	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	default:
		return "", false
	}
	return u.String(), true
}

func adminDuration(d time.Duration) string {
	if d == 0 {
		return d.String()
	}
	if d%time.Hour == 0 {
		return strconv.FormatInt(int64(d/time.Hour), 10) + "h"
	}
	if d%time.Minute == 0 {
		return strings.TrimSuffix(d.String(), "0s")
	}
	return d.String()
}

func (s *Server) adminAuthCache(w http.ResponseWriter, r *http.Request, pubkey string) {
	_ = pubkey
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()

	body, err := s.adminAuthCachePayload(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) adminAuthCachePayload(ctx context.Context) (map[string]any, error) {
	if s.Cache == nil {
		return map[string]any{
			"enabled": false,
		}, nil
	}
	summary, err := s.Cache.Summary(ctx, s.now())
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"enabled":     true,
		"total":       summary.Total,
		"valid":       summary.Valid,
		"expired":     summary.Expired,
		"oldest_seen": unixTimeOrNil(summary.OldestUnix),
		"newest_seen": unixTimeOrNil(summary.NewestUnix),
	}, nil
}

func (s *Server) adminPolicy(w http.ResponseWriter, r *http.Request, pubkey string) {
	writeJSON(w, http.StatusOK, s.adminPolicyPayload(pubkey))
}

func (s *Server) adminPolicyPayload(pubkey string) map[string]any {
	return map[string]any{
		"authenticated_pubkey": pubkey,
		"access_rule": map[string]any{
			"name":        "trustroots-nip05",
			"description": "Users must complete NIP-42 authentication and resolve to the same pubkey through NIP-05 verification.",
			"profile_kinds": []int{
				TrustrootsProfileKind,
				0,
			},
			"profile_label_namespace": TrustrootsUsernameLabelNamespace,
		},
		"admin_rule": map[string]any{
			"name":        "configured-admin-pubkeys",
			"description": "Admin API requests must be signed with NIP-98 by a pubkey listed in ADMIN_PUBKEYS.",
			"admin_count": len(s.AdminAuth.Admins),
		},
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func unixTimeOrNil(unix int64) any {
	if unix == 0 {
		return nil
	}
	return time.Unix(unix, 0).UTC().Format(time.RFC3339)
}

func (s *Server) now() time.Time {
	if s.AdminAuth.Now != nil {
		return s.AdminAuth.Now()
	}
	return time.Now()
}

const adminHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<link rel="icon" href="/favicon.svg" type="image/svg+xml">
<title>{{WRAPSTER_HUB_NAME}}</title>
<style>
{{ADMIN_COMMON_CSS}}
body.signed-out main.auth-only {
  display: none;
}
body.signed-in main.auth-only {
  display: grid;
}
.fips-header-status .status-value { font-weight: 700; }
.dashboard-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 8px;
}
.dashboard-card {
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--panel-soft);
  padding: 10px;
  display: grid;
  gap: 8px;
  align-content: start;
}
.dashboard-card h3 {
  margin: 0;
  font-size: 14px;
  line-height: 1.2;
}
.dashboard-card .row {
  grid-template-columns: minmax(78px, .55fr) minmax(0, 1.45fr);
  gap: 6px;
  align-items: baseline;
}
.dashboard-card dt {
  font-size: 11px;
  font-weight: 650;
}
.dashboard-card dd {
  font: 12px/1.3 system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  overflow-wrap: anywhere;
}
.dashboard-card .lines {
  gap: 1px;
}
.dashboard-card .lines code {
  display: block;
  font-size: 11px;
  line-height: 1.3;
  white-space: normal;
  overflow-wrap: anywhere;
}
dl { margin: 0; display: grid; gap: 8px; }
.row {
  display: grid;
  grid-template-columns: minmax(110px, .8fr) minmax(0, 1.2fr);
  gap: 10px;
  align-items: start;
}
dt { color: var(--muted); }
dd { margin: 0; overflow-wrap: anywhere; }
.lines { display: grid; gap: 3px; }
.lines code {
  font: 12px/1.4 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  overflow-wrap: anywhere;
}
.advert-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
  gap: 10px;
}
.advert-card {
  border: 1px solid var(--line);
  border-radius: 8px;
  padding: 12px;
  display: grid;
  gap: 10px;
  align-content: start;
  background: var(--panel-soft);
}
.advert-title {
  font-size: 16px;
  line-height: 1.2;
  font-weight: 760;
}
.advert-meta {
  color: var(--muted);
  overflow-wrap: anywhere;
  font-size: 14px;
  font-weight: 650;
}
.advert-details {
  display: grid;
  gap: 10px;
  padding-top: 4px;
}
.advert-detail {
  display: grid;
  gap: 4px;
}
.advert-detail-label {
  color: var(--muted);
  font-size: 13px;
  font-weight: 650;
}
.advert-notes {
  display: grid;
  gap: 6px;
}
.advert-note {
  border: 1px solid var(--line);
  border-radius: 8px;
  padding: 8px;
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 8px;
  align-items: start;
}
.advert-note-main {
  display: grid;
  gap: 3px;
  min-width: 0;
}
.advert-note-heading {
  display: flex;
  gap: 8px;
  align-items: baseline;
  flex-wrap: wrap;
}
.advert-note-title { font-weight: 700; }
.advert-note-service {
  color: var(--accent);
  font-size: 13px;
  font-weight: 700;
}
.advert-note-summary { overflow-wrap: anywhere; }
.advert-note-meta {
  color: var(--muted);
  font-size: 12px;
  overflow-wrap: anywhere;
}
.advert-note-address {
  display: block;
  font: 12px/1.45 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  overflow-wrap: anywhere;
}
.detail-link {
  border: 0;
  background: transparent;
  color: var(--accent);
  cursor: pointer;
  font: inherit;
  font-weight: 650;
  min-height: 0;
  padding: 0;
  text-align: left;
  text-decoration: underline;
}
.access-rule-line {
  display: grid;
  gap: 10px;
}
.access-rule-card {
  display: grid;
  gap: 6px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--panel);
  padding: 10px;
}
.access-rule-head {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  align-items: start;
  flex-wrap: wrap;
}
.access-rule-title {
  color: var(--fg);
  font-weight: 730;
}
.access-rule-type {
  color: var(--muted);
  font-size: 12px;
  font-weight: 650;
}
.access-rule-meta {
  display: grid;
  gap: 3px;
  color: var(--muted);
  font-size: 13px;
}
.access-list {
  display: grid;
  gap: 6px;
  margin-top: 10px;
}
.access-person {
  display: grid;
  gap: 2px;
}
.query-list {
  display: grid;
  gap: 8px;
  margin-top: 10px;
}
.query-entry {
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--panel-soft);
  padding: 10px;
  display: grid;
  gap: 6px;
}
.query-entry-meta {
  color: var(--muted);
  font-size: 12px;
  overflow-wrap: anywhere;
}
.query-entry pre {
  margin: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font: 11px/1.4 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
dialog {
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--panel);
  color: var(--fg);
  max-width: min(720px, calc(100vw - 24px));
  width: min(720px, 100%);
  padding: 12px;
}
dialog::backdrop { background: rgb(0 0 0 / .35); }
form {
  display: grid;
  gap: 10px;
}
label {
  margin: 0;
}
@media (max-width: 820px) {
  .advert-grid { grid-template-columns: 1fr; }
}
</style>
</head>
<body class="signed-out">
<header>
  <div class="brand-block">
    <h1>{{WRAPSTER_HUB_NAME}}</h1>
    <div class="status policy-note" title="Admin API requests are signed with NIP-98. Relay users must complete NIP-42 authentication and resolve to the same pubkey through NIP-05 verification.">NIP-42 authenticated relay wrapper with additional services</div>
    <div id="identity" class="status identity-line">Not signed in</div>
  </div>
  <div class="header-status">
    <div class="toolbar">
      <button id="connect" class="connect-button">Connect</button>
    </div>
    <div id="header-fips-status" class="fips-header-status neutral">FIPS peer: checking status...</div>
  </div>
</header>
<main id="admin-main" class="grid auth-only" aria-hidden="true">
  <section class="wide">
    <h2>FIPS Identity</h2>
    <div class="identity-tool">
      <div class="status">Generate and activate a fresh FIPS sidecar identity for this deployment.</div>
      <div class="identity-output">
        <input id="fips-npub" readonly placeholder="npub1...">
        <button id="copy-fips-npub" class="secondary" type="button">Copy npub</button>
      </div>
      <div id="fips-secret-row" class="identity-output secret-output hidden">
        <input id="fips-nsec" readonly type="password" autocomplete="off" placeholder="nsec1...">
        <button id="reveal-fips-nsec" class="secondary icon-button" type="button" aria-label="Reveal nsec" title="Reveal nsec"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M2 12s4-7 10-7 10 7 10 7-4 7-10 7-10-7-10-7Z"></path><circle cx="12" cy="12" r="3"></circle></svg></button>
        <button id="copy-fips-nsec" class="secondary" type="button">Copy secret</button>
      </div>
      <div class="form-actions">
        <button id="generate-fips-nsec" type="button">Generate identity</button>
      </div>
      <div id="fips-nsec-status" class="status"></div>
      <label>NAS FIPS peer npub
        <input id="fips-peer-npub" placeholder="npub1...">
      </label>
      <div id="fips-peer-status" class="status">Enter a NAS peer npub to monitor connectivity.</div>
    </div>
  </section>
  <section class="wide">
    <h2>FIPS Peers</h2>
    <div id="fips-peers" class="status">No peers configured</div>
  </section>
  <section class="wide">
    <h2>Relay Overview</h2>
    <div id="dashboard" class="dashboard-grid"></div>
  </section>
  <section class="wide">
    <h2>Advertise Services</h2>
    <div id="advert-services" class="advert-grid"></div>
    <div id="advert-status" class="status"></div>
  </section>
</main>
<footer class="site-footer">
  <a class="footer-link" href="/examples/service-directory.html">Service directory</a>
  <span class="footer-meta">Build time: {{BUILD_TIME}}</span>
  <a class="github-link" href="https://github.com/guaka/wrapster" target="_blank" rel="noopener noreferrer" aria-label="guaka/wrapster on GitHub" title="guaka/wrapster">
    <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82A7.65 7.65 0 0 1 8 3.87c.68 0 1.36.09 2 .26 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8Z"></path></svg>
  </a>
</footer>
<dialog id="advert-dialog">
  <form id="advert-form" method="dialog">
    <h2 id="advert-heading">Advertise service</h2>
    <input id="advert-service" type="hidden">
    <label>Title
      <input id="advert-title" required maxlength="80">
    </label>
    <label>Summary
      <input id="advert-summary" required maxlength="180">
    </label>
    <label>Slug
      <input id="advert-slug" required maxlength="48">
    </label>
    <label>Status
      <select id="advert-state">
        <option value="active">active</option>
        <option value="paused">paused</option>
        <option value="full">full</option>
        <option value="retired">retired</option>
      </select>
    </label>
    <label>Access
      <select id="advert-access">
        <option value="request">request</option>
        <option value="invite">invite</option>
        <option value="members">members</option>
        <option value="public">public</option>
      </select>
    </label>
    <label>Audience
      <input id="advert-audience" placeholder="trustroots, community">
    </label>
    <label>Relays
      <textarea id="advert-relays" required></textarea>
    </label>
    <label>Description
      <textarea id="advert-content" required></textarea>
    </label>
    <div class="form-actions">
      <button id="advert-cancel" class="secondary" type="button">Cancel</button>
      <button id="advert-submit" type="submit">Publish</button>
    </div>
  </form>
</dialog>
<dialog id="connector-dialog">
  <h2>Configure Media Connector</h2>
  <p>Run <code>wrapster-connector</code> on the private network that can reach Jellyfin or Plex, preferably through FIPS or another private transport.</p>
  <dl>
    <div class="row">
      <dt>Gateway</dt>
      <dd>Set <code>MEDIA_CONNECTOR_BASE_URL</code> to the connector URL, for example <code>http://10.77.0.2:22000</code>.</dd>
    </div>
    <div class="row">
      <dt>Token</dt>
      <dd>If using a token, set gateway <code>MEDIA_CONNECTOR_TOKEN</code> to match connector <code>CONNECTOR_SHARED_TOKEN</code>.</dd>
    </div>
    <div class="row">
      <dt>Connector</dt>
      <dd>Set <code>CONNECTOR_LISTEN_ADDR</code> plus <code>JELLYFIN_BASE_URL</code>/<code>JELLYFIN_API_KEY</code> or <code>PLEX_BASE_URL</code>/<code>PLEX_TOKEN</code>.</dd>
    </div>
  </dl>
  <div class="form-actions">
    <button id="connector-close" class="secondary" type="button">Close</button>
  </div>
</dialog>
<dialog id="access-dialog">
  <h2 id="access-heading">Access Rule</h2>
  <div id="access-status" class="status"></div>
  <div id="access-list" class="access-list"></div>
  <div class="form-actions">
    <button id="access-close" class="secondary" type="button">Close</button>
  </div>
</dialog>
<dialog id="query-dialog">
  <h2>Recent Public Relay Queries</h2>
  <div id="query-list" class="query-list"></div>
  <div class="form-actions">
    <button id="query-close" class="secondary" type="button">Close</button>
  </div>
</dialog>
<script>
const SERVICE_ADVERT_KIND = 31388;
const NIP09_DELETE_KIND = 5;
const NIP02_FOLLOW_LIST_KIND = 3;
const TRUSTROOTS_PROFILE_KIND = 10390;
const DEFAULT_ADVERT_RELAYS = ["wss://relay.guaka.org", "wss://nip42.trustroots.org"];
const FIPS_PEER_STATUS_POLL_MS = 5000;
const FIPS_NSEC_STORAGE_KEY = "wrapster-admin-fips-nsec-v1";
const FIPS_PEER_STORAGE_KEY = "wrapster-admin-fips-peer-v1";
const state = { pubkey: "", npub: "", connecting: false, connected: false, overview: null, accessRules: {}, advertNotes: [] };
const fipsPeerCheckState = { timerId: null, running: null, peerKey: "" };
const adminMain = document.getElementById("admin-main");
const connectButton = document.getElementById("connect");
const identity = document.getElementById("identity");
const advertDialog = document.getElementById("advert-dialog");
const advertForm = document.getElementById("advert-form");
const advertCancel = document.getElementById("advert-cancel");
const advertStatus = document.getElementById("advert-status");
let currentAdvertService = null;
const connectorDialog = document.getElementById("connector-dialog");
const connectorClose = document.getElementById("connector-close");
const accessDialog = document.getElementById("access-dialog");
const accessClose = document.getElementById("access-close");
const queryDialog = document.getElementById("query-dialog");
const queryClose = document.getElementById("query-close");
const queryList = document.getElementById("query-list");
const fipsNpub = document.getElementById("fips-npub");
const fipsNsec = document.getElementById("fips-nsec");
const fipsSecretRow = document.getElementById("fips-secret-row");
const fipsNsecStatus = document.getElementById("fips-nsec-status");
const generateFipsNsecButton = document.getElementById("generate-fips-nsec");
const copyFipsNpubButton = document.getElementById("copy-fips-npub");
const copyFipsNsecButton = document.getElementById("copy-fips-nsec");
const revealFipsNsecButton = document.getElementById("reveal-fips-nsec");
const fipsPeerNpub = document.getElementById("fips-peer-npub");
const fipsPeerStatus = document.getElementById("fips-peer-status");
const fipsPeers = document.getElementById("fips-peers");
const headerFipsStatus = document.getElementById("header-fips-status");

connectButton.addEventListener("click", connect);
advertForm.addEventListener("submit", publishAdvertFromForm);
advertCancel.addEventListener("click", () => advertDialog.close());
connectorClose.addEventListener("click", () => connectorDialog.close());
accessClose.addEventListener("click", () => accessDialog.close());
queryClose.addEventListener("click", () => queryDialog.close());
generateFipsNsecButton.addEventListener("click", generateFipsNsec);
copyFipsNpubButton.addEventListener("click", copyFipsNpub);
copyFipsNsecButton.addEventListener("click", copyFipsNsec);
revealFipsNsecButton.addEventListener("click", toggleFipsNsec);
fipsPeerNpub.addEventListener("input", () => {
  saveCachedFIPSPeer(fipsPeerNpub.value);
  scheduleFIPSPeerConnectivityCheck(fipsPeerNpub.value);
});
window.addEventListener("load", autoConnect);

async function connect() {
  if (state.connecting || state.connected) return;
  if (!hasNIP07()) {
    connectButton.textContent = "NIP-07 unavailable";
    connectButton.title = "Install or enable a NIP-07 browser extension to sign admin requests.";
    setSignedIn(false);
    return;
  }
  state.connecting = true;
  connectButton.disabled = true;
  connectButton.classList.remove("connected");
  connectButton.textContent = "Connecting...";
  connectButton.title = "";
  state.connected = true;
  try {
    await loadAll();
    showSignedIdentity(state.pubkey);
    state.connected = true;
    setSignedIn(true);
    connectButton.disabled = false;
  } catch (err) {
    if (err.pubkey) showSignedIdentity(err.pubkey);
    else identity.textContent = err.message || String(err);
    state.connected = false;
    setSignedIn(false);
    connectButton.disabled = false;
    resetConnectButton();
  } finally {
    state.connecting = false;
  }
}

async function autoConnect() {
  connectButton.textContent = "Checking NIP-07...";
  connectButton.title = "Looking for a NIP-07 browser extension.";
  if (hasNIP07() || await waitForNostr()) {
    await connect();
    return;
  }
  identity.textContent = "NIP-07 extension not detected";
  resetConnectButton();
}

function hasNIP07() {
  return Boolean(window.nostr && typeof window.nostr.signEvent === "function");
}

function setSignedIn(signedIn) {
  document.body.classList.toggle("signed-in", signedIn);
  document.body.classList.toggle("signed-out", !signedIn);
  adminMain.hidden = !signedIn;
  adminMain.setAttribute("aria-hidden", signedIn ? "false" : "true");
}

async function waitForNostr() {
  for (let attempt = 0; attempt < 20; attempt++) {
    await new Promise(resolve => setTimeout(resolve, 100));
    if (hasNIP07()) return true;
  }
  return false;
}

async function loadAll() {
  const data = await signedFetch("/admin/api/overview");
  state.overview = data;
  if (data.authenticated_pubkey) state.pubkey = data.authenticated_pubkey;
  showSignedIdentity(state.pubkey);
  renderOverview(data);
}

function renderOverview(data) {
  renderIdentity(data.identity || {});
  renderDashboard(data.status || {}, data.config || {}, data.auth_cache || {});
  renderAdvertServices(data);
  renderFIPSIdentityStatus(data.fips || {});
  loadAdvertNotes(data);
}

function renderDashboard(status, config, authCache) {
  const root = document.getElementById("dashboard");
  root.replaceChildren();
  root.append(
    dashboardCard("Public Relay", publicRelayDashboard(status, config)),
    dashboardCard("strfry", strfryDashboard(status)),
    dashboardCard("Media Connector", mediaConnectorDashboard(status, config)),
    dashboardCard("Auth Cache", authCacheDashboard(authCache)),
    dashboardCard("NIP-05 Lookup Relays", lookupRelayDashboard(config))
  );
}

function publicRelayDashboard(status, config) {
  const relayStatus = status.relay || {};
  return {
    "URL": relayStatus.public_url || "-",
    "Auth": relayAuthRequirement(config),
    "Recent query": recentQueryValue(relayStatus.recent_queries || []),
    "Supported NIPs": (relayStatus.supported_nips || []).join(", ") || "-"
  };
}

function relayAuthRequirement(config) {
  const rules = Object.values(config.access_rules || {});
  const nip05 = rules.find((rule) => rule.type === "trustroots_nip05");
  const domain = nip05DomainLabel(nip05?.nip05_base_url) || "Trustroots.org";
  return "NIP-42 AUTH + " + domain + " NIP-05 (same pubkey)";
}

function nip05DomainLabel(baseURL) {
  try {
    const host = new URL(baseURL).hostname.replace(/^www\./, "");
    if (!host) return "";
    if (host === "trustroots.org") return "Trustroots.org";
    return host;
  } catch {
    return "";
  }
}

function recentQueryValue(queries) {
  if (!Array.isArray(queries) || !queries.length) {
    const span = document.createElement("span");
    span.className = "muted-line";
    span.textContent = "none yet";
    return span;
  }
  const button = document.createElement("button");
  button.type = "button";
  button.className = "detail-link";
  button.textContent = querySummary(queries[0]);
  button.addEventListener("click", () => openQueryDialog(queries));
  return button;
}

function openQueryDialog(queries) {
  queryList.replaceChildren();
  for (const query of queries) {
    const item = document.createElement("div");
    item.className = "query-entry";
    const meta = document.createElement("div");
    meta.className = "query-entry-meta";
    meta.textContent = [
      formatDateTime(query.at),
      shortHex(query.pubkey),
      query.subscription_id || "REQ"
    ].filter(Boolean).join(" · ");
    const filters = document.createElement("pre");
    filters.textContent = JSON.stringify(query.filters || [], null, 2);
    item.append(meta, filters);
    queryList.append(item);
  }
  queryDialog.showModal();
}

function querySummary(query) {
  return [
    shortHex(query.pubkey),
    query.subscription_id || "REQ",
    compactFilterSummary(query.filters || [])
  ].filter(Boolean).join(" · ");
}

function compactFilterSummary(filters) {
  const first = filters[0] || {};
  const parts = [];
  if (Array.isArray(first.kinds)) parts.push("kinds " + first.kinds.join(","));
  if (Array.isArray(first.authors)) parts.push(first.authors.length + " author" + (first.authors.length === 1 ? "" : "s"));
  if (Array.isArray(first["#p"])) parts.push(first["#p"].length + " #p");
  if (first.limit) parts.push("limit " + first.limit);
  if (parts.length) return parts.join("; ");
  const raw = JSON.stringify(first);
  return raw.length > 80 ? raw.slice(0, 77) + "..." : raw;
}

function shortHex(value) {
  value = String(value || "");
  if (value.length <= 16) return value;
  return value.slice(0, 8) + "..." + value.slice(-6);
}

function strfryDashboard(status) {
  const strfry = status.strfry || {};
  const nip11 = strfry.nip11 || {};
  const values = {
    "Status": statusValue(Boolean(strfry.reachable), strfry.reachable ? "reachable" : "unreachable"),
    "URL": strfry.url || status.relay?.upstream_url || "-",
    "Latency": strfry.latency_ms == null ? "-" : strfry.latency_ms + " ms",
    "Checked": formatDateTime(strfry.checked_at)
  };
  if (strfry.last_error) values["Error"] = strfry.last_error;
  if (nip11.name) values["Name"] = nip11.name;
  if (Array.isArray(nip11.supported_nips)) values["NIP-11 support"] = nip11.supported_nips.join(", ") || "-";
  return values;
}

function mediaConnectorDashboard(status, config) {
  const connector = status.media_connector || {};
  const configured = connector.configured !== false;
  const values = {
    "Status": statusValue(Boolean(connector.reachable), connector.reachable ? "reachable over " + (connector.transport || "private") : (configured ? "unreachable" : "not configured")),
    "Transport": connector.transport || "-",
    "Latency": connector.latency_ms == null ? "-" : connector.latency_ms + " ms",
    "Checked": formatDateTime(connector.checked_at)
  };
  if (connector.status_code != null) values["HTTP"] = String(connector.status_code);
  if (connector.last_error) values["Error"] = connector.last_error;
  const services = connector.connector?.services || {};
  const configuredServices = Object.entries(services)
    .filter(([, value]) => value && value.configured)
    .map(([name]) => name);
  if (configuredServices.length) values["Services"] = configuredServices.join(", ");
  const mediaServices = config.media?.services || {};
  if (configuredServices.includes("jellyfin") || Object.prototype.hasOwnProperty.call(mediaServices, "jellyfin")) {
    values["Random Jellyfin song"] = mediaSongTestNode();
  }
  return values;
}

function mediaSongTestNode() {
  const root = document.createElement("div");
  root.className = "song-test";
  const button = document.createElement("button");
  button.type = "button";
  button.className = "secondary";
  button.textContent = "Play random song";
  const output = document.createElement("div");
  output.className = "song-test hidden";
  button.addEventListener("click", () => runMediaSongTest(button, output));
  root.append(button, output);
  return root;
}

async function runMediaSongTest(button, output) {
  button.disabled = true;
  const original = button.textContent;
  button.textContent = "Testing...";
  try {
    await runRandomSongTest({
      root: output,
      titleMode: "status",
      startTitle: "Selecting random Jellyfin song...",
      startDetail: "requesting random Jellyfin audio through public media API",
      randomRequest: () => signedRequest("/media/api/services/jellyfin/random-song"),
      streamRequest: (streamURL) => signedRequest(streamURL)
    });
  } catch (err) {
    renderSongTest(output, String(err.message || err), [{name: "browser", ok: false, error: String(err.message || err)}], "", {titleMode: "status"});
  } finally {
    button.disabled = false;
    button.textContent = original;
  }
}

function authCacheDashboard(authCache) {
  return {
    "Status": statusValue(authCache.enabled !== false, authCache.enabled === false ? "disabled" : "enabled"),
    "Entries": numberOrDash(authCache.valid) + " valid / " + numberOrDash(authCache.total) + " total",
    "Expired": numberOrDash(authCache.expired),
    "Newest": formatDateTime(authCache.newest_seen)
  };
}

function lookupRelayDashboard(config) {
  const relays = config.relays || {};
  return {
    "Configured": linesNode(relays.lookup || relays.additional || [])
  };
}

function dashboardCard(title, values) {
  const card = document.createElement("div");
  card.className = "dashboard-card";
  const heading = document.createElement("h3");
  heading.textContent = title;
  const list = document.createElement("dl");
  card.append(heading, list);
  appendRows(list, values);
  return card;
}

function statusValue(ok, label) {
  const span = document.createElement("span");
  span.className = "status-value " + (ok ? "ok" : "bad");
  span.append(bool(ok, label), document.createTextNode(label));
  return span;
}

function numberOrDash(value) {
  return typeof value === "number" ? String(value) : "-";
}

function formatDateTime(value) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  return date.toLocaleString();
}

function renderAdvertServices(data) {
  const root = document.getElementById("advert-services");
  root.replaceChildren();
  const services = advertisedServices(data);
  if (!services.length) {
    const span = document.createElement("span");
    span.className = "muted-line";
    span.textContent = "No configured services";
    root.append(span);
    return;
  }
  for (const service of services) {
    const card = document.createElement("div");
    card.className = "advert-card";
    const title = document.createElement("div");
    title.className = "advert-title";
    title.textContent = service.label || service.service;
    const meta = document.createElement("div");
    meta.className = "advert-meta";
    meta.textContent = service.meta || ("service:" + service.service);
    card.append(title, meta);
    const details = advertServiceDetails(service);
    if (details) card.append(details);
    const notes = advertNotesForService(service);
    if (notes.length) card.append(advertNotesNode(notes));
    const button = document.createElement("button");
    button.type = "button";
    button.textContent = advertButtonLabel(service);
    button.addEventListener("click", () => openAdvertDialog(service));
    card.append(button);
    root.append(card);
  }
}

function renderFIPSIdentityStatus(fips) {
  renderFIPSPeers(fips.peers || fipsToLegacyPeerList(fips));
  const state = fips.state || "unknown";
  const peer = hydrateFIPSPeerInputs(fips);
  if (!peer.npub) {
    setHeaderFipsStatus("neutral", "FIPS peer: not configured");
  } else if (!peer.addr) {
    setHeaderFipsStatus("neutral", "FIPS peer: trying to establish connection to NAS");
  }
  if (state === "configured") {
    const npub = fips.npub || "unknown";
    fipsNpub.value = npub;
    renderCachedFIPSNsec(fips);
    fipsNsecStatus.textContent = "FIPS identity loaded (" + npub + ")";
    scheduleFIPSPeerConnectivityCheck(peer.npub, peer.addr);
    return;
  }
  if (state === "invalid") {
    clearCachedFIPSNsec();
    fipsNpub.value = "";
    fipsSecretRow.classList.add("hidden");
    fipsNsec.value = "";
    fipsNsecStatus.textContent = fips.error ? "FIPS identity file is invalid: " + fips.error : "FIPS identity file is invalid.";
    scheduleFIPSPeerConnectivityCheck(peer.npub, peer.addr);
    return;
  }
  if (state === "file_error") {
    fipsNpub.value = "";
    renderCachedFIPSNsec(fips);
    fipsNsecStatus.textContent = fips.error ? "FIPS identity unavailable: " + fips.error : "FIPS identity unavailable.";
    scheduleFIPSPeerConnectivityCheck(peer.npub, peer.addr);
    return;
  }
  if (state === "not_configured") {
    fipsNpub.value = "";
    renderCachedFIPSNsec(fips);
    fipsNsecStatus.textContent = "No FIPS identity configured yet.";
    if (peer.npub && !peer.addr) {
      setHeaderFipsStatus("neutral", "FIPS peer: trying to establish connection to NAS");
    }
    scheduleFIPSPeerConnectivityCheck(peer.npub, peer.addr);
    return;
  }
  renderCachedFIPSNsec(fips);
  fipsNsecStatus.textContent = "FIPS status: " + state + ".";
  scheduleFIPSPeerConnectivityCheck(peer.npub, peer.addr);
}

function scheduleFIPSPeerConnectivityCheck(npub, addr) {
  const target = {
    npub: String(npub || "").trim(),
    addr: String(addr || "").trim(),
  };
  const targetKey = target.npub + "|" + target.addr;

  if (!target.npub) {
    stopFIPSPeerConnectivityCheck();
    setHeaderFipsStatus("neutral", "FIPS peer: not configured");
    fipsPeerStatus.textContent = "Enter a NAS peer npub to monitor connectivity.";
    return;
  }

  if (fipsPeerCheckState.peerKey !== targetKey) {
    stopFIPSPeerConnectivityCheck();
    fipsPeerCheckState.peerKey = targetKey;
  }

  const run = () => {
    if (!state.connected) {
      const waitingText = "Trying to establish a signed session for FIPS checks.";
      if (!fipsPeerStatus.textContent || !fipsPeerStatus.textContent.includes("Trying to establish")) {
        fipsPeerStatus.textContent = waitingText;
        setHeaderFipsStatus("neutral", "FIPS peer: " + waitingText);
      }
      return;
    }
    if (fipsPeerCheckState.running) return;
    const check = runTestFIPSPeerConnection(true, target)
      .catch((err) => {
        fipsPeerStatus.textContent = String(err.message || err);
      })
      .finally(() => {
        if (fipsPeerCheckState.running === check) {
          fipsPeerCheckState.running = null;
        }
      });
    fipsPeerCheckState.running = check;
  };

  if (!fipsPeerCheckState.timerId) {
    fipsPeerCheckState.timerId = window.setInterval(run, FIPS_PEER_STATUS_POLL_MS);
  }
  if (!state.connected) {
    const waitingText = "Trying to establish a signed session for FIPS checks.";
    fipsPeerStatus.textContent = waitingText;
    setHeaderFipsStatus("neutral", "FIPS peer: " + waitingText);
  }
  void run();
}

function stopFIPSPeerConnectivityCheck() {
  if (fipsPeerCheckState.timerId) {
    window.clearInterval(fipsPeerCheckState.timerId);
    fipsPeerCheckState.timerId = null;
  }
  fipsPeerCheckState.running = null;
  fipsPeerCheckState.peerKey = "";
}

function fipsToLegacyPeerList(fips) {
  const peerNpub = String(fips?.peer_npub || "").trim();
  const peerAddr = String(fips?.peer_addr || "").trim();
  if (!peerNpub && !peerAddr) return [];
  return [{
    npub: peerNpub,
    addr: peerAddr,
    configured: Boolean(peerNpub),
    addr_configured: Boolean(peerAddr)
  }];
}

function renderFIPSPeers(peers) {
  renderFIPSPeerList(fipsPeers, peers);
}

{{ADMIN_COMMON_JS}}

function renderCachedFIPSNsec(fips) {
  const cached = getCachedFIPSNsec();
  if (!cached.nsec || (fips.npub && cached.npub && cached.npub !== fips.npub)) {
    fipsSecretRow.classList.add("hidden");
    fipsNsec.value = "";
    return;
  }
  fipsNsec.value = cached.nsec;
  fipsSecretRow.classList.remove("hidden");
  fipsNsec.type = "password";
}

function getCachedFIPSNsec() {
  try {
    const raw = window.localStorage.getItem(FIPS_NSEC_STORAGE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object") return {};
    const nsec = String(parsed.nsec || "").trim();
    const npub = String(parsed.npub || "").trim();
    return { nsec, npub, savedAt: parsed.savedAt || "" };
  } catch {
    return {};
  }
}

function getCachedFIPSPeer() {
  try {
    const raw = window.localStorage.getItem(FIPS_PEER_STORAGE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object") return {};
    const peerNpub = String(parsed.peer_npub || "").trim();
    return { peerNpub };
  } catch {
    return {};
  }
}

function saveCachedFIPSPeer(peerNpub) {
  try {
    window.localStorage.setItem(FIPS_PEER_STORAGE_KEY, JSON.stringify({
      peer_npub: String(peerNpub || "").trim(),
      updated_at: new Date().toISOString()
    }));
  } catch {
    // localStorage is optional: ignore cache failures.
  }
}

function hydrateFIPSPeerInputs(fips) {
  const payloadNpub = String(fips.peer_npub || "").trim();
  const cached = getCachedFIPSPeer();
  const peerNpub = payloadNpub || cached.peerNpub || "";
  const peerAddr = String(fips.peer_addr || "").trim();
  fipsPeerNpub.value = peerNpub;
  return { npub: peerNpub, addr: peerAddr };
}

async function runTestFIPSPeerConnection(auto = false, peer) {
  const checkedPeer = peer || {
    npub: fipsPeerNpub.value.trim(),
    addr: String(peer && peer.addr || "").trim()
  };
  if (!checkedPeer.npub) {
    setHeaderFipsStatus("neutral", "FIPS peer: not configured");
    if (!auto) {
      fipsPeerStatus.textContent = "Enter a NAS peer npub first.";
    }
    return {reachable: false, peer_npub: "", peer_addr: checkedPeer.addr};
  }
  saveCachedFIPSPeer(checkedPeer.npub);
  const checkingText = checkedPeer.addr
    ? "Trying to establish connection to " + checkedPeer.addr + "..."
    : "Trying to establish connection to NAS peer...";
  fipsPeerStatus.textContent = checkingText;
  setHeaderFipsStatus("neutral", checkingText);
  const data = await signedFetch("/admin/api/fips-peer-check", {
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify({
      fips_peer_npub: checkedPeer.npub,
      ...(checkedPeer.addr ? {fips_peer_addr: checkedPeer.addr} : {})
    })
  });
  renderFIPSPeers([{
    npub: checkedPeer.npub,
    addr: checkedPeer.addr,
    configured: Boolean(checkedPeer.npub),
    addr_configured: Boolean(checkedPeer.addr),
    check: data || {}
  }]);
  if (data && data.error) {
    if (data.peer_addr_set === false || data.transport_check_skipped === true) {
      const debug = formatFIPSPeerDebug(data.debug_steps);
      const text = (String(data.message || "Trying to establish connection") + (debug ? " (" + debug + ")" : ""));
      fipsPeerStatus.textContent = text;
      setHeaderFipsStatus("neutral", "FIPS peer: " + text);
    } else {
      const debug = formatFIPSPeerDebug(data.debug_steps);
      const text = "NAS peer check failed: " + data.error + (debug ? " (" + debug + ")" : "");
      fipsPeerStatus.textContent = text;
      setHeaderFipsStatus("bad", "FIPS peer: " + text);
    }
    return data;
  }
  saveCachedFIPSPeer(checkedPeer.npub);
  if (data.reachable) {
    const text = "dialable via " + (data.transport || "tcp") + (checkedPeer.addr ? " (" + checkedPeer.addr + ")" : "");
    fipsPeerStatus.textContent = text;
    setHeaderFipsStatus("ok", "FIPS peer: " + text);
    return data;
  }
  const note = data.error ? " " + data.error : "";
  const debug = formatFIPSPeerDebug(data.debug_steps);
  const debugText = debug ? " " + debug : "";
  if (checkedPeer.addr) {
    const text = "NAS peer not reachable:" + note + debugText;
    fipsPeerStatus.textContent = text;
    setHeaderFipsStatus("bad", "FIPS peer: " + text);
    return data;
  }
  const statusText = "Trying to establish connection (outbound session pending)." + note + debugText;
  fipsPeerStatus.textContent = "Waiting for outbound session.";
  setHeaderFipsStatus("neutral", "FIPS peer: " + statusText);
  return data;
}

function setHeaderFipsStatus(state, text) {
  setFIPSHeaderStatus(headerFipsStatus, state, text);
}

function saveCachedFIPSNsec(nsec, npub) {
  try {
    window.localStorage.setItem(FIPS_NSEC_STORAGE_KEY, JSON.stringify({
      nsec,
      npub,
      savedAt: new Date().toISOString()
    }));
  } catch {
    // localStorage is optional: ignore cache failures.
  }
}

function clearCachedFIPSNsec() {
  try {
    window.localStorage.removeItem(FIPS_NSEC_STORAGE_KEY);
  } catch {
    // best effort
  }
}

function advertButtonLabel(service) {
  return service.service === "media" ? "Advertise Media" : "Advertise";
}

async function loadAdvertNotes(data) {
  if (!state.pubkey) {
    state.advertNotes = [];
    renderAdvertServices(state.overview || {});
    advertStatus.textContent = "Connect to load published advert notes.";
    return;
  }
  const relays = advertRelays(data || state.overview || {});
  if (!relays.length) {
    state.advertNotes = [];
    renderAdvertServices(state.overview || {});
    advertStatus.textContent = "No relays configured for advert lookup.";
    return;
  }
  advertStatus.textContent = "Loading advert notes...";
  const results = await Promise.all(relays.map((relay) => loadAdvertNotesFromRelay(relay)));
  const notesByAddress = new Map();
  let failures = 0;
  for (const result of results) {
    if (!result.ok) {
      failures++;
      continue;
    }
    for (const event of result.events) upsertAdvertNote(notesByAddress, event, result.relay);
  }
  const notes = Array.from(notesByAddress.values()).sort((a, b) => (b.created_at || 0) - (a.created_at || 0));
  state.advertNotes = notes;
  renderAdvertServices(state.overview || {});
  const noteText = notes.length + " advert note" + (notes.length === 1 ? "" : "s");
  advertStatus.textContent = failures ? noteText + "; " + failures + " relay lookup" + (failures === 1 ? "" : "s") + " failed." : noteText + ".";
}

async function loadAdvertNotesFromRelay(relay) {
  try {
    const events = await relayEvents(relay, { kinds: [SERVICE_ADVERT_KIND], authors: [state.pubkey], "#t": ["nostr-service-advert"], limit: 50 }, "wrapster-adverts-" + Math.random().toString(36).slice(2));
    return { relay, ok: true, events };
  } catch (err) {
    return { relay, ok: false, error: err.message || String(err), events: [] };
  }
}

function upsertAdvertNote(notesByAddress, event, relay) {
  if (!event || event.kind !== SERVICE_ADVERT_KIND || event.pubkey !== state.pubkey) return;
  const d = tagValue(event, "d");
  const address = d ? SERVICE_ADVERT_KIND + ":" + event.pubkey + ":" + d : event.id;
  const existing = notesByAddress.get(address);
  if (existing) {
    existing.relays.add(relay);
    if ((event.created_at || 0) <= (existing.created_at || 0)) return;
  }
  const relays = existing ? existing.relays : new Set();
  relays.add(relay);
  notesByAddress.set(address, {
    id: event.id || "",
    pubkey: event.pubkey || "",
    kind: event.kind,
    created_at: event.created_at || 0,
    d,
    address,
    title: tagValue(event, "title") || "(untitled advert)",
    summary: tagValue(event, "summary") || "",
    service: tagValue(event, "service") || serviceFromTags(event) || "service",
    status: tagValue(event, "status") || "",
    content: event.content || "",
    relays
  });
}

function serviceFromTags(event) {
  for (const value of tagValues(event, "t")) {
    if (value.indexOf("service:") === 0) return value.slice("service:".length);
  }
  return "";
}

function advertNotesForService(service) {
  const serviceKeys = new Set((service.note_services || [service.service]).map(normalizeToken));
  return state.advertNotes.filter((note) => serviceKeys.has(normalizeToken(note.service)));
}

function advertNotesNode(notes) {
  const root = document.createElement("div");
  root.className = "advert-notes";
  const label = document.createElement("div");
  label.className = "advert-detail-label";
  label.textContent = "Published adverts";
  root.append(label);
  for (const note of notes) root.append(advertNoteCard(note));
  return root;
}

function advertNoteCard(note) {
  const card = document.createElement("div");
  card.className = "advert-note";
  const main = document.createElement("div");
  main.className = "advert-note-main";
  const heading = document.createElement("div");
  heading.className = "advert-note-heading";
  const title = document.createElement("div");
  title.className = "advert-note-title";
  title.textContent = note.title;
  const service = document.createElement("div");
  service.className = "advert-note-service";
  service.textContent = "service:" + note.service;
  heading.append(title, service);
  const summary = document.createElement("div");
  summary.className = "advert-note-summary";
  summary.textContent = note.summary || note.content || "No summary provided.";
  const meta = document.createElement("div");
  meta.className = "advert-note-meta";
  const updated = note.created_at ? new Date(note.created_at * 1000).toLocaleString() : "unknown";
  meta.textContent = "Updated " + updated + " on " + Array.from(note.relays).join(", ");
  main.append(heading, summary, meta);
  if (note.d) {
    const address = document.createElement("code");
    address.className = "advert-note-address";
    address.textContent = note.address;
    main.append(address);
  }
  const remove = document.createElement("button");
  remove.type = "button";
  remove.className = "icon-button";
  remove.title = "Delete advert note";
  remove.setAttribute("aria-label", "Delete advert note");
  remove.innerHTML = "<svg viewBox=\"0 0 24 24\" aria-hidden=\"true\"><path d=\"M3 6h18\"></path><path d=\"M8 6V4h8v2\"></path><path d=\"M6 6l1 15h10l1-15\"></path><path d=\"M10 11v6\"></path><path d=\"M14 11v6\"></path></svg>";
  remove.addEventListener("click", () => deleteAdvertNote(note, remove));
  card.append(main, remove);
  return card;
}

async function deleteAdvertNote(note, button) {
  if (!window.nostr || typeof window.nostr.signEvent !== "function") {
    advertStatus.textContent = "NIP-07 extension unavailable";
    return;
  }
  if (!window.confirm("Delete this advert note from the relays it was found on?")) return;
  button.disabled = true;
  advertStatus.textContent = "Signing deletion...";
  try {
    const signed = await window.nostr.signEvent(buildAdvertDeleteEvent(note));
    if (!signed.id) throw new Error("Signed deletion is missing an event id.");
    const relays = Array.from(note.relays);
    advertStatus.textContent = "Publishing deletion...";
    const results = await Promise.all(relays.map((relay) => publishAdvertToRelay(relay, signed)));
    const accepted = results.filter((result) => result.ok);
    const rejected = results.filter((result) => !result.ok);
    if (accepted.length) {
      const acceptedRelays = new Set(accepted.map((result) => result.relay));
      note.relays = new Set(relays.filter((relay) => !acceptedRelays.has(relay)));
      state.advertNotes = state.advertNotes.filter((item) => item.address !== note.address || item.relays.size);
    }
    renderAdvertServices(state.overview || {});
    advertStatus.textContent = accepted.length + "/" + results.length + " relays accepted deletion" + (rejected.length ? ": " + rejected.map((result) => result.relay + " " + result.error).join("; ") : "");
    if (!accepted.length) button.disabled = false;
  } catch (err) {
    advertStatus.textContent = err.message || String(err);
    button.disabled = false;
  }
}

function buildAdvertDeleteEvent(note) {
  const relay = Array.from(note.relays)[0] || "";
  const tags = [["k", String(SERVICE_ADVERT_KIND)]];
  if (note.id) tags.push(["e", note.id, relay]);
  if (note.d) tags.push(["a", note.address, relay]);
  return {
    kind: NIP09_DELETE_KIND,
    created_at: Math.floor(Date.now() / 1000),
    tags,
    content: "Deleted from Wrapster Hub"
  };
}

function advertServiceDetails(service) {
  const details = Array.isArray(service.details) ? service.details : [];
  if (!details.length) return null;
  const root = document.createElement("div");
  root.className = "advert-details";
  for (const detail of details) {
    const item = document.createElement("div");
    item.className = "advert-detail";
    const label = document.createElement("div");
    label.className = "advert-detail-label";
    label.textContent = detail.label || "";
    item.append(label);
    if (Array.isArray(detail.lines)) item.append(linesNode(detail.lines));
    else if (detail.value && detail.value.nodeType) item.append(detail.value);
    else {
      const value = document.createElement("div");
      value.textContent = detail.value || "-";
      item.append(value);
    }
    root.append(item);
  }
  return root;
}

function advertisedServices(data) {
  const config = data.config || {};
  const services = [];
  const byKey = new Map();
  const mediaEntries = mediaServiceEntries(config);
  const mediaTypes = new Set(mediaEntries.map(([name]) => normalizeToken(name)));
  function add(service) {
    const type = normalizeToken(service.service || service.name || "");
    if (!type) return;
    const normalized = Object.assign({}, service, { service: type, note_services: service.note_services || [type] });
    const key = type + ":" + normalizeToken(service.advert_slug || service.name || service.label || type);
    const existing = byKey.get(key);
    if (existing) {
      Object.assign(existing, normalized);
      return;
    }
    byKey.set(key, normalized);
    services.push(normalized);
  }
  for (const service of config.advertisable_services || []) {
    const type = normalizeToken(service.service || service.name || "");
    if (mediaTypes.has(type)) continue;
    add(service);
  }
  if (config.proxy) {
    add({
      name: "proxy",
      label: "Proxy",
      service: "cors-proxy",
      title: "Wrapster CORS Proxy",
      summary: "Allowlisted browser proxy for Trustroots and community wiki calls",
      access: "request",
      audience: ["community", "trustroots"],
      note_services: ["cors-proxy", "proxy"],
      extra_tags: proxyEndpointExtraTags(config.proxy),
      details: proxyDetails(config.proxy)
    });
  }
  if (mediaEntries.length) {
    add({
      name: "media",
      label: "Media",
      service: "media",
      meta: "services:" + mediaEntries.map(([name]) => name).join(", "),
      title: "Wrapster Media",
      summary: "Request-gated media services through Wrapster",
      access: "request",
      audience: ["trustroots"],
      note_services: ["media"].concat(mediaEntries.map(([name]) => name)),
      details: mediaDetails(config)
    });
  }
  return services.sort((a, b) => (a.label || a.service).localeCompare(b.label || b.service));
}

function mediaServiceEntries(config) {
  return Object.entries(config.media?.services || {}).sort(([a], [b]) => a.localeCompare(b));
}

function mediaDetails(config) {
  return [
    {
      label: "Access rules",
      value: accessRulesNode(config)
    },
    {
      label: "Media connector",
      value: mediaConnectorValue(config.media?.connector_configured)
    },
    {
      label: "Media services",
      lines: mediaServiceEntries(config).map(([name, rules]) => name + " \u2192 " + accessRuleListLabel(rules))
    }
  ];
}

function accessRuleListLabel(rules) {
  if (!Array.isArray(rules)) return rules || "(no rule)";
  return rules.length ? rules.join(" + ") : "(no rule)";
}

function accessRulesNode(config) {
  const entries = Object.entries(config.access_rules || {}).sort(([a], [b]) => a.localeCompare(b));
  const wrap = document.createElement("div");
  wrap.className = "access-rule-line";
  if (!entries.length) {
    const span = document.createElement("span");
    span.className = "muted-line";
    span.textContent = "none";
    wrap.append(span);
    return wrap;
  }
  for (const [name, rule] of entries) {
    wrap.append(accessRuleCard(name, rule));
  }
  return wrap;
}

function accessRuleCard(name, rule) {
  const card = document.createElement("div");
  card.className = "access-rule-card";
  const head = document.createElement("div");
  head.className = "access-rule-head";
  const titleWrap = document.createElement("div");
  const title = document.createElement("div");
  title.className = "access-rule-title";
  title.textContent = prettyRuleName(name);
  const type = document.createElement("div");
  type.className = "access-rule-type";
  type.textContent = accessRuleTypeLabel(rule);
  titleWrap.append(title, type);
  head.append(titleWrap);
  if (rule.type === "nostr_follow" && rule.owner_pubkey) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "detail-link";
    button.textContent = "Checking contacts...";
    button.disabled = true;
    button.addEventListener("click", () => openAccessRuleModal(name));
    head.append(button);
    loadAccessRule(name, rule, button);
  }
  const meta = document.createElement("div");
  meta.className = "access-rule-meta";
  for (const line of accessRuleMeta(name, rule)) {
    const row = document.createElement("div");
    row.textContent = line;
    meta.append(row);
  }
  card.append(head, meta);
  return card;
}

function accessRuleTypeLabel(rule) {
  if (rule.type === "nostr_follow") return "Owner follow list";
  if (rule.type === "trustroots_nip05") return "NIP-05";
  return titleizeService(rule.type || "Access rule");
}

function accessRuleMeta(name, rule) {
  const lines = [];
  if (rule.type === "nostr_follow") {
    lines.push("Allows contacts followed by the media owner.");
  } else if (rule.type === "trustroots_nip05") {
    lines.push("Requires the signed pubkey to match a NIP-05 identity.");
  }
  if (rule.relay) lines.push("Relay: " + rule.relay);
  if (rule.deny_count) lines.push(rule.deny_count + " denied " + (rule.deny_count === 1 ? "pubkey" : "pubkeys"));
  return lines.length ? lines : [name];
}

function prettyRuleName(name) {
  if (name === "media_owner_follows") return "Media owner contacts";
  if (name === "trustroots_nip05") return "Trustroots members";
  return titleizeService(name);
}

async function loadAccessRule(name, rule, button) {
  try {
    const people = await resolveNostrFollowAccess(rule);
    state.accessRules[name] = { rule, people, error: "" };
    button.textContent = people.length + " " + (people.length === 1 ? "contact" : "contacts");
  } catch (err) {
    state.accessRules[name] = { rule, people: [], error: err.message || String(err) };
    button.textContent = "lookup failed";
  } finally {
    button.disabled = false;
  }
}

function openAccessRuleModal(name) {
  const result = state.accessRules[name] || { people: [], error: "Lookup has not finished yet." };
  document.getElementById("access-heading").textContent = name;
  const status = document.getElementById("access-status");
  const list = document.getElementById("access-list");
  list.replaceChildren();
  if (result.error) {
    status.textContent = result.error;
  } else {
    status.textContent = result.people.length + " " + (result.people.length === 1 ? "contact" : "contacts") + " can access media through this rule.";
  }
  for (const person of result.people) {
    const item = document.createElement("div");
    item.className = "access-person";
    const nameLine = document.createElement("div");
    nameLine.textContent = person.trustroots_nip05 || "NIP-05 not found";
    const pubkeyLine = document.createElement("code");
    pubkeyLine.textContent = person.npub || person.pubkey;
    item.append(nameLine, pubkeyLine);
    list.append(item);
  }
  accessDialog.showModal();
}

async function resolveNostrFollowAccess(rule) {
  const relay = rule.relay || DEFAULT_ADVERT_RELAYS[1];
  const owner = String(rule.owner_pubkey || "").toLowerCase();
  if (!relay || !owner) throw new Error("Follow rule is missing relay or owner pubkey.");
  const follows = await relayEvents(relay, { kinds: [NIP02_FOLLOW_LIST_KIND], authors: [owner], limit: 25 }, "wrapster-access-follows");
  const latest = follows
    .filter((event) => (event.tags || []).some((tag) => tag[0] === "p" && tag[1]))
    .sort((a, b) => (b.created_at || 0) - (a.created_at || 0))[0];
  if (!latest) return [];
  const pubkeys = uniqueHexPubkeys((latest.tags || []).filter((tag) => tag[0] === "p").map((tag) => tag[1]));
  const profiles = await profileNames(relay, pubkeys);
  return pubkeys.map((pubkey) => ({
    pubkey,
    npub: npubEncode(pubkey),
    trustroots_nip05: profiles[pubkey] || ""
  }));
}

async function profileNames(relay, pubkeys) {
  const profiles = {};
  if (!pubkeys.length) return profiles;
  const events = await relayEvents(relay, { kinds: [TRUSTROOTS_PROFILE_KIND, 0], authors: pubkeys, limit: Math.max(20, pubkeys.length * 2) }, "wrapster-access-profiles");
  for (const event of events.sort((a, b) => (b.created_at || 0) - (a.created_at || 0))) {
    if (profiles[event.pubkey]) continue;
    const username = trustrootsUsername(event);
    if (username) profiles[event.pubkey] = username + "@trustroots.org";
  }
  return profiles;
}

function trustrootsUsername(event) {
  if (event.kind === TRUSTROOTS_PROFILE_KIND) {
    for (const tag of event.tags || []) {
      if (tag.length >= 3 && tag[0] === "l" && tag[2] === "org.trustroots:username") return normalizeTrustrootsUsername(tag[1]);
    }
  }
  if (event.kind === 0) {
    try {
      const profile = JSON.parse(event.content || "{}");
      const username = normalizeTrustrootsUsername(profile.trustrootsUsername || "");
      if (username) return username;
      const nip05 = String(profile.nip05 || "").toLowerCase();
      if (nip05.endsWith("@trustroots.org")) return normalizeTrustrootsUsername(nip05.slice(0, -"@trustroots.org".length));
    } catch {}
  }
  return "";
}

function normalizeTrustrootsUsername(username) {
  username = String(username || "").trim().toLowerCase();
  if (username.length < 3 || !/^[a-z0-9_.-]+$/.test(username)) return "";
  return username;
}

function uniqueHexPubkeys(pubkeys) {
  const seen = new Set();
  return pubkeys
    .map((pubkey) => String(pubkey || "").toLowerCase())
    .filter((pubkey) => /^[0-9a-f]{64}$/.test(pubkey))
    .filter((pubkey) => {
      if (seen.has(pubkey)) return false;
      seen.add(pubkey);
      return true;
    });
}

function relayEvents(relay, filter, subID) {
  return new Promise((resolve, reject) => {
    const events = [];
    const request = ["REQ", subID, filter];
    let socket;
    let requested = false;
    let done = false;
    let authEventId = "";
    let authAccepted = false;
    let unauthenticatedReqTimer = 0;
    const timeout = window.setTimeout(() => finish(), 14000);

    function finish(err) {
      if (done) return;
      done = true;
      window.clearTimeout(timeout);
      window.clearTimeout(unauthenticatedReqTimer);
      if (socket && socket.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify(["CLOSE", subID]));
        socket.close();
      }
      if (err) reject(err); else resolve(events);
    }

    function sendRequest() {
      if (done || requested || !socket || socket.readyState !== WebSocket.OPEN) return;
      requested = true;
      socket.send(JSON.stringify(request));
    }

    function canSignNIP42() {
      return window.nostr && typeof window.nostr.signEvent === "function";
    }

    function waitForAuthRetry(reason) {
      if (authAccepted) return false;
      if (!canSignNIP42()) return false;
      if (!/auth-required|restricted/i.test(reason)) return false;
      requested = false;
      window.clearTimeout(unauthenticatedReqTimer);
      return true;
    }

    async function authenticate(challenge) {
      window.clearTimeout(unauthenticatedReqTimer);
      if (!canSignNIP42()) {
        sendRequest();
        return;
      }
      try {
        const authEvent = await window.nostr.signEvent({
          kind: 22242,
          created_at: Math.floor(Date.now() / 1000),
          tags: [["relay", relay], ["challenge", String(challenge)]],
          content: ""
        });
        if (done || !socket || socket.readyState !== WebSocket.OPEN) return;
        authEventId = authEvent.id || "";
        socket.send(JSON.stringify(["AUTH", authEvent]));
      } catch (err) {
        finish(new Error("Relay auth failed: " + (err.message || String(err))));
      }
    }

    try {
      socket = new WebSocket(relay);
    } catch (err) {
      finish(err);
      return;
    }

    socket.addEventListener("open", () => {
      unauthenticatedReqTimer = window.setTimeout(sendRequest, 600);
    });
    socket.addEventListener("message", async (message) => {
      let payload;
      try {
        payload = JSON.parse(message.data);
      } catch {
        return;
      }
      if (payload[0] === "AUTH") {
        await authenticate(payload[1]);
      } else if (payload[0] === "OK" && payload[1] === authEventId) {
        if (payload[2] === true) {
          authAccepted = true;
          sendRequest();
        } else {
          finish(new Error(String(payload[3] || "Relay auth rejected")));
        }
      } else if (payload[0] === "EVENT" && payload[1] === subID) {
        events.push(payload[2]);
      } else if (payload[0] === "EOSE" && payload[1] === subID) {
        finish();
      } else if (payload[0] === "CLOSED" && payload[1] === subID) {
        const reason = String(payload[2] || "Relay closed subscription");
        if (waitForAuthRetry(reason)) return;
        finish(events.length ? null : new Error(reason));
      }
    });
    socket.addEventListener("error", () => finish(new Error("Relay connection failed")));
    socket.addEventListener("close", () => {
      if (!done) finish(events.length ? null : new Error("Relay connection closed"));
    });
  });
}

function mediaConnectorValue(configured) {
  if (configured) return "configured";
  const button = document.createElement("button");
  button.type = "button";
  button.className = "detail-link";
  button.textContent = "not configured";
  button.addEventListener("click", () => connectorDialog.showModal());
  return button;
}

function proxyDetails(proxy) {
  const prefix = proxy.prefix || "";
  return [
    {
      label: "Proxy access",
      value: accessRuleListLabel(proxy.access_rules)
    },
    {
      label: "Proxy routes",
      lines: Object.entries(proxy.routes || {})
        .map(([name, url]) => prefix + "/" + name + " \u2192 " + url)
        .sort()
    }
  ];
}

function proxyEndpointExtraTags(proxy) {
  const endpoint = publicProxyEndpoint(proxy);
  return endpoint ? [["endpoint", endpoint]] : [];
}

function publicProxyEndpoint(proxy) {
  const publicRelay = state.overview?.status?.relay?.public_url || "";
  if (!publicRelay || !proxy?.prefix) return "";
  try {
    const url = new URL(publicRelay);
    if (url.protocol === "wss:") url.protocol = "https:";
    else if (url.protocol === "ws:") url.protocol = "http:";
    else if (url.protocol !== "http:" && url.protocol !== "https:") return "";
    url.pathname = proxy.prefix;
    url.search = "";
    url.hash = "";
    return url.toString().replace(/\/$/, "");
  } catch {
    return "";
  }
}

function openAdvertDialog(service) {
  if (!state.pubkey) {
    advertStatus.textContent = "Connect before publishing an advert.";
    return;
  }
  currentAdvertService = service;
  const relays = advertRelays(state.overview || {});
  const audience = Array.isArray(service.audience) ? service.audience : ["trustroots"];
  document.getElementById("advert-heading").textContent = "Advertise " + (service.label || service.service);
  document.getElementById("advert-service").value = service.service;
  document.getElementById("advert-title").value = service.title || ("Wrapster " + titleizeService(service.service));
  document.getElementById("advert-summary").value = service.summary || ("Request-gated " + titleizeService(service.service) + " access through Wrapster");
  document.getElementById("advert-slug").value = service.advert_slug || advertSlug(service.service);
  document.getElementById("advert-state").value = service.status || "active";
  document.getElementById("advert-access").value = service.access || "request";
  document.getElementById("advert-audience").value = audience.join(", ");
  document.getElementById("advert-relays").value = relays.join("\n");
  document.getElementById("advert-content").value = advertContent(service.service, service.summary);
  advertStatus.textContent = "";
  advertDialog.showModal();
}

async function publishAdvertFromForm(event) {
  event.preventDefault();
  if (!window.nostr || typeof window.nostr.signEvent !== "function") {
    advertStatus.textContent = "NIP-07 extension unavailable";
    return;
  }
  const submit = document.getElementById("advert-submit");
  submit.disabled = true;
  advertStatus.textContent = "Signing advert...";
  try {
    const unsigned = buildAdvertEvent();
    const signed = await window.nostr.signEvent(unsigned);
    if (!signed.id) throw new Error("Signed advert is missing an event id.");
    if (signed.pubkey) showSignedIdentity(signed.pubkey);
    const relays = uniqueRelays(document.getElementById("advert-relays").value);
    if (!relays.length) throw new Error("Add at least one relay.");
    advertStatus.textContent = "Publishing advert...";
    const results = await Promise.all(relays.map((relay) => publishAdvertToRelay(relay, signed)));
    const accepted = results.filter((result) => result.ok);
    const rejected = results.filter((result) => !result.ok);
    advertStatus.textContent = accepted.length + "/" + results.length + " relays accepted" + (rejected.length ? ": " + rejected.map((result) => result.relay + " " + result.error).join("; ") : "");
    if (accepted.length) {
      advertDialog.close();
      loadAdvertNotes(state.overview || {});
    }
  } catch (err) {
    advertStatus.textContent = err.message || String(err);
  } finally {
    submit.disabled = false;
  }
}

function buildAdvertEvent() {
  const service = normalizeToken(document.getElementById("advert-service").value);
  const title = document.getElementById("advert-title").value.trim();
  const summary = document.getElementById("advert-summary").value.trim();
  const slug = normalizeToken(document.getElementById("advert-slug").value);
  const status = normalizeToken(document.getElementById("advert-state").value) || "active";
  const access = normalizeToken(document.getElementById("advert-access").value) || "request";
  const audiences = tokenList(document.getElementById("advert-audience").value);
  const relays = uniqueRelays(document.getElementById("advert-relays").value);
  const relayHint = relays[0] || "";
  if (!service || !title || !summary || !slug) {
    throw new Error("Service, title, summary, and slug are required.");
  }
  if (!state.pubkey) {
    throw new Error("Connect before publishing an advert.");
  }
  const tags = [
    ["d", service + ":" + slug],
    ["title", title],
    ["summary", summary],
    ["service", service],
    ["status", status],
    ["request", "nip17"],
    ["p", state.pubkey, relayHint, "contact"],
    ["t", "nostr-service-advert"],
    ["t", "service:" + service],
    ["t", "status:" + status],
    ["t", "access:" + access]
  ];
  for (const audience of audiences) tags.push(["t", "audience:" + audience]);
  for (const tag of advertExtraTags(currentAdvertService)) tags.push(tag);
  return {
    kind: SERVICE_ADVERT_KIND,
    created_at: Math.floor(Date.now() / 1000),
    tags,
    content: document.getElementById("advert-content").value.trim()
  };
}

function advertExtraTags(service) {
  const out = [];
  for (const tag of service?.extra_tags || []) {
    if (!Array.isArray(tag) || tag.length < 2) continue;
    const normalized = tag.map((value) => String(value || "").trim());
    if (!normalized[0] || !normalized[1]) continue;
    out.push(normalized);
  }
  return out;
}

function publishAdvertToRelay(relay, event) {
  return new Promise((resolve) => {
    let socket;
    let done = false;
    let sent = false;
    let authEventId = "";
    let authAccepted = false;
    let sendTimer = 0;
    let timeout = 0;

    function finish(ok, error) {
      if (done) return;
      done = true;
      window.clearTimeout(sendTimer);
      window.clearTimeout(timeout);
      if (socket && socket.readyState === WebSocket.OPEN) socket.close();
      resolve({ relay, ok, error: error || "" });
    }

    function sendEvent() {
      if (done || sent || !socket || socket.readyState !== WebSocket.OPEN) return;
      sent = true;
      socket.send(JSON.stringify(["EVENT", event]));
    }

    function canSignNIP42() {
      return window.nostr && typeof window.nostr.signEvent === "function";
    }

    function waitForAuthRetry(reason) {
      if (authAccepted) return false;
      if (!canSignNIP42()) return false;
      if (!/auth-required|restricted/i.test(reason)) return false;
      sent = false;
      window.clearTimeout(sendTimer);
      return true;
    }

    async function authenticate(challenge) {
      window.clearTimeout(sendTimer);
      try {
        const authEvent = await window.nostr.signEvent({
          kind: 22242,
          created_at: Math.floor(Date.now() / 1000),
          tags: [["relay", relay], ["challenge", String(challenge)]],
          content: ""
        });
        authEventId = authEvent.id || "";
        if (socket && socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify(["AUTH", authEvent]));
      } catch (err) {
        finish(false, "auth failed: " + (err.message || String(err)));
      }
    }

    try {
      socket = new WebSocket(relay);
    } catch (err) {
      finish(false, err.message || String(err));
      return;
    }

    timeout = window.setTimeout(() => finish(false, "publish timed out"), 12000);
    socket.addEventListener("open", () => {
      sendTimer = window.setTimeout(sendEvent, 500);
    });
    socket.addEventListener("message", async (message) => {
      let payload;
      try {
        payload = JSON.parse(message.data);
      } catch {
        return;
      }
      const type = payload[0];
      if (type === "AUTH") {
        await authenticate(payload[1]);
      } else if (type === "OK" && payload[1] === authEventId) {
        if (payload[2] === true) {
          authAccepted = true;
          sendEvent();
        } else {
          finish(false, String(payload[3] || "auth rejected"));
        }
      } else if (type === "OK" && payload[1] === event.id) {
        const reason = String(payload[3] || "");
        if (payload[2] !== true && waitForAuthRetry(reason)) return;
        finish(payload[2] === true, reason);
      } else if (type === "NOTICE") {
        const notice = String(payload[1] || "");
        if (waitForAuthRetry(notice)) return;
      }
    });
    socket.addEventListener("error", () => finish(false, "WebSocket error"));
    socket.addEventListener("close", () => finish(false, "connection closed"));
  });
}

function advertRelays(data) {
  const relays = [];
  const publicRelay = data.status?.relay?.public_url || "";
  if (publicRelay) relays.push(publicRelay);
  for (const relay of data.config?.relays?.additional || []) relays.push(relay);
  for (const relay of DEFAULT_ADVERT_RELAYS) relays.push(relay);
  return uniqueRelays(relays.join("\n"));
}

function uniqueRelays(raw) {
  const seen = new Set();
  return String(raw || "")
    .split(/[\s,]+/)
    .map((relay) => relay.trim())
    .filter(Boolean)
    .filter((relay) => /^wss?:\/\//.test(relay))
    .filter((relay) => {
      if (seen.has(relay)) return false;
      seen.add(relay);
      return true;
    });
}

function advertContent(service, summary) {
  if (service === "cors-proxy") {
    return "A Wrapster CORS proxy for static community clients.\n\nRequest the public proxy URL by Nostr DM. The proxy is allowlisted to configured upstreams and stores nothing.";
  }
  if (service === "wiki") {
    return (summary || "A public wiki available through Wrapster.") + "\n\nThis advert publishes public MediaWiki metadata for static clients.";
  }
  return (summary || "A community-hosted service behind Wrapster.") + "\n\nRequest access by Nostr DM. Private service endpoints and tokens stay server-side.";
}

function advertSlug(service) {
  const host = window.location.hostname || "wrapster";
  return normalizeToken(host.replace(/\./g, "-")) || normalizeToken(service);
}

function normalizeToken(value) {
  return String(value || "")
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

function tokenList(value) {
  return String(value || "")
    .split(/[\s,]+/)
    .map(normalizeToken)
    .filter(Boolean);
}

function tagValue(event, name) {
  const tag = (event.tags || []).find((item) => item[0] === name && item[1]);
  return tag ? String(tag[1]) : "";
}

function tagValues(event, name) {
  return (event.tags || [])
    .filter((item) => item[0] === name && item[1])
    .map((item) => String(item[1]));
}

function titleizeService(service) {
  return String(service || "")
    .split(/[-_]+/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

function linesNode(lines) {
  const wrap = document.createElement("div");
  wrap.className = "lines";
  if (!lines || !lines.length) {
    const span = document.createElement("span");
    span.className = "muted-line";
    span.textContent = "none";
    wrap.append(span);
    return wrap;
  }
  for (const line of lines) {
    const code = document.createElement("code");
    code.textContent = line;
    wrap.append(code);
  }
  return wrap;
}

function renderIdentity(data) {
  connectButton.replaceChildren();
  connectButton.classList.add("connected");
  connectButton.title = identityHoverText(data);
  const dot = document.createElement("span");
  dot.className = "connect-dot " + (data.verified ? "ok" : "bad");
  dot.setAttribute("aria-hidden", "true");
  const text = document.createElement("span");
  text.className = "connect-label";
  if (data.trustroots_nip05) {
    text.textContent = data.trustroots_nip05;
    identity.title = "";
  } else {
    text.textContent = "Connected";
  }
  connectButton.append(dot, text);
}

function identityHoverText(data) {
  const lines = [];
  if (data.verified) lines.push("Signed in and verified through NIP-05.");
  else if (state.pubkey) lines.push("Signed in, but NIP-05 was not verified.");
  else lines.push("Not signed in.");
  if (state.npub) lines.push("npub: " + state.npub);
  if (state.pubkey) lines.push("pubkey: " + state.pubkey);
  return lines.join("\n");
}

async function signedFetch(path, options = {}) {
  const response = await signedRequest(path, options);
  const body = await response.json().catch(() => ({}));
  if (!response.ok) {
    const err = new Error(body.error || response.statusText);
    err.pubkey = state.pubkey;
    err.npub = npubEncode(err.pubkey);
    throw err;
  }
  return body;
}

async function signedRequest(path, options = {}) {
  const method = (options.method || "GET").toUpperCase();
  const url = new URL(path, window.location.href).toString();
  const event = await window.nostr.signEvent({
    kind: 27235,
    created_at: Math.floor(Date.now() / 1000),
    tags: [["u", url], ["method", method]],
    content: ""
  });
  if (event.pubkey) {
    state.pubkey = event.pubkey;
    state.npub = npubEncode(event.pubkey);
  }
  const raw = JSON.stringify(event);
  const encoded = btoa(String.fromCharCode(...new TextEncoder().encode(raw)));
  const headers = new Headers(options.headers || {});
  headers.set("Authorization", "Nostr " + encoded);
  return fetch(url, {...options, method, headers});
}

function showSignedIdentity(pubkey) {
  state.pubkey = pubkey || state.pubkey;
  state.npub = npubEncode(state.pubkey);
  identity.textContent = "";
  identity.title = state.npub || state.pubkey || "";
}

function resetConnectButton() {
  connectButton.replaceChildren();
  connectButton.classList.remove("connected");
  connectButton.textContent = "Connect";
  connectButton.title = "";
}

function render(id, values) {
  const root = document.getElementById(id);
  root.replaceChildren();
  appendRows(root, values);
}

function appendRows(root, values) {
  for (const [key, value] of Object.entries(values)) {
    const wrap = document.createElement("div");
    wrap.className = "row";
    const dt = document.createElement("dt");
    const dd = document.createElement("dd");
    dt.textContent = key;
    if (value && value.nodeType) dd.append(value); else dd.textContent = String(value);
    wrap.append(dt, dd);
    root.append(wrap);
  }
}

function renderError(id, err) {
  const values = { Error: err.message || String(err) };
  if (err.npub) values["Signed npub"] = err.npub;
  if (err.pubkey) values["Signed pubkey"] = err.pubkey;
  render(id, values);
}

function bool(value, label) {
  const span = document.createElement("span");
  span.className = "status-dot " + (value ? "ok" : "bad");
  const state = value ? "healthy" : "needs attention";
  span.title = (label ? label + " " : "") + state;
  span.setAttribute("aria-label", (label ? label + " " : "") + state);
  return span;
}

async function generateFipsNsec() {
  if (!window.crypto || typeof window.crypto.getRandomValues !== "function") {
    fipsNsecStatus.textContent = "Secure random generator unavailable.";
    return;
  }
  const bytes = new Uint8Array(32);
  window.crypto.getRandomValues(bytes);
  const nsec = bech32Encode("nsec", convertBits(Array.from(bytes), 8, 5, true));
  fipsNsec.value = nsec;
  fipsNsec.type = "password";
  fipsSecretRow.classList.remove("hidden");
  fipsNpub.value = "";
  fipsNsecStatus.textContent = "Saving FIPS identity...";
  const data = await signedFetch("/admin/api/fips-nsec", {
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify({nsec})
  });
  fipsNpub.value = data.npub || "";
  saveCachedFIPSNsec(nsec, fipsNpub.value);
  fipsNsecStatus.textContent = "Saved. FIPS will start automatically.";
}

async function copyFipsNpub() {
  if (!fipsNpub.value) {
    fipsNsecStatus.textContent = "Generate an identity first.";
    return;
  }
  await copyText(fipsNpub, "Copied npub.");
}

async function copyFipsNsec() {
  if (!fipsNsec.value) {
    fipsNsecStatus.textContent = "Generate an identity first.";
    return;
  }
  await copyText(fipsNsec, "Copied secret.");
}

function toggleFipsNsec() {
  const revealing = fipsNsec.type === "password";
  fipsNsec.type = revealing ? "text" : "password";
  revealFipsNsecButton.title = revealing ? "Hide nsec" : "Reveal nsec";
  revealFipsNsecButton.setAttribute("aria-label", revealFipsNsecButton.title);
}

async function copyText(input, successMessage) {
  try {
    await navigator.clipboard.writeText(input.value);
    fipsNsecStatus.textContent = successMessage;
  } catch {
    input.select();
    document.execCommand("copy");
    fipsNsecStatus.textContent = successMessage;
  }
}

function npubEncode(hex) {
  const bytes = hexToBytes(hex);
  if (bytes.length !== 32) return "";
  return bech32Encode("npub", convertBits(bytes, 8, 5, true));
}

function hexToBytes(hex) {
  if (!/^[0-9a-f]{64}$/i.test(hex || "")) return [];
  const bytes = [];
  for (let i = 0; i < hex.length; i += 2) {
    bytes.push(parseInt(hex.slice(i, i + 2), 16));
  }
  return bytes;
}

const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l";

function bech32Encode(hrp, data) {
  const combined = data.concat(bech32Checksum(hrp, data));
  return hrp + "1" + combined.map(value => bech32Charset[value]).join("");
}

function bech32Checksum(hrp, data) {
  const values = bech32HrpExpand(hrp).concat(data, [0, 0, 0, 0, 0, 0]);
  const mod = bech32Polymod(values) ^ 1;
  const checksum = [];
  for (let p = 0; p < 6; p++) {
    checksum.push((mod >> (5 * (5 - p))) & 31);
  }
  return checksum;
}

function bech32HrpExpand(hrp) {
  const values = [];
  for (const char of hrp) values.push(char.charCodeAt(0) >> 5);
  values.push(0);
  for (const char of hrp) values.push(char.charCodeAt(0) & 31);
  return values;
}

function bech32Polymod(values) {
  const generator = [0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3];
  let chk = 1;
  for (const value of values) {
    const top = chk >>> 25;
    chk = (((chk & 0x1ffffff) << 5) ^ value) >>> 0;
    for (let i = 0; i < generator.length; i++) {
      if ((top >>> i) & 1) chk = (chk ^ generator[i]) >>> 0;
    }
  }
  return chk >>> 0;
}

function convertBits(data, fromBits, toBits, pad) {
  let acc = 0;
  let bits = 0;
  const maxValue = (1 << toBits) - 1;
  const maxAcc = (1 << (fromBits + toBits - 1)) - 1;
  const out = [];
  for (const value of data) {
    if (value < 0 || (value >> fromBits) !== 0) return [];
    acc = ((acc << fromBits) | value) & maxAcc;
    bits += fromBits;
    while (bits >= toBits) {
      bits -= toBits;
      out.push((acc >> bits) & maxValue);
    }
  }
  if (pad && bits > 0) {
    out.push((acc << (toBits - bits)) & maxValue);
  } else if (bits >= fromBits || ((acc << (toBits - bits)) & maxValue) !== 0) {
    return [];
  }
  return out;
}
</script>
</body>
</html>`

const faviconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">
<rect width="64" height="64" rx="14" fill="#1f2520"/>
<path d="M15 28c4-8 10-12 17-12s13 4 17 12" fill="none" stroke="#64c7ad" stroke-width="5" stroke-linecap="round"/>
<path d="M20 37c3-5 7-8 12-8s9 3 12 8" fill="none" stroke="#f7f7f4" stroke-width="5" stroke-linecap="round"/>
<path d="M32 17v31" fill="none" stroke="#64c7ad" stroke-width="5" stroke-linecap="round"/>
<path d="M20 49c5 0 9-3 12-9 3 6 7 9 12 9" fill="none" stroke="#64c7ad" stroke-width="5" stroke-linecap="round" stroke-linejoin="round"/>
<circle cx="32" cy="28" r="4" fill="#f7f7f4"/>
</svg>`
