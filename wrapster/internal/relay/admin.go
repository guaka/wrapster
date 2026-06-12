package relay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/trustroots/nostroots/vibe/wrapster/internal/access"
	adminauth "github.com/trustroots/nostroots/vibe/wrapster/internal/admin"
)

func (s *Server) adminIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(adminHTML))
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
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
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
	case "/admin/api/overview":
		s.adminOverview(w, r, pubkey)
	case "/admin/api/status":
		s.adminStatus(w, r, pubkey)
	case "/admin/api/identity":
		s.adminIdentity(w, r, pubkey)
	case "/admin/api/config":
		s.adminConfig(w, r, pubkey)
	case "/admin/api/auth-cache":
		s.adminAuthCache(w, r, pubkey)
	case "/admin/api/policy":
		s.adminPolicy(w, r, pubkey)
	default:
		http.NotFound(w, r)
	}
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
		"config":               s.adminConfigPayload(),
	})
}

// adminIdentity resolves the Trustroots NIP-05 for the signed-in admin pubkey
// by looking up the profile on the relays this deployment uses, then verifying
// it through Trustroots NIP-05.
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
	resp := map[string]any{
		"relays": map[string]any{
			"upstream":   s.Upstream.URL,
			"additional": orEmpty(s.Upstream.ProfileRelays),
		},
	}
	advertisable := []map[string]any{}

	if s.GenericProxy != nil {
		routes := map[string]string{}
		for route, target := range s.GenericProxy.Targets {
			routes[route] = target
		}
		resp["proxy"] = map[string]any{
			"prefix":          s.GenericProxy.Prefix,
			"access_rules":    orEmpty(s.GenericProxy.AccessRules),
			"routes":          routes,
			"default_target":  s.GenericProxy.DefaultTarget,
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
	resp["advertisable_services"] = advertisable

	return resp
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
	upstreamOK := false
	if conn, err := s.Upstream.Dial(ctx); err == nil {
		upstreamOK = true
		_ = conn.Close()
	}

	return map[string]any{
		"service": map[string]any{
			"name":                  "wrapster",
			"description":           "NIP-42 authenticated Trustroots relay wrapper",
			"authenticated_pubkey":  pubkey,
			"admin_pubkeys_count":   len(s.AdminAuth.Admins),
			"admin_auth_max_age_ms": s.AdminAuth.MaxAge.Milliseconds(),
		},
		"relay": map[string]any{
			"public_url":        s.PublicRelayURL,
			"upstream_url":      s.Upstream.URL,
			"auth_cache_ttl":    adminDuration(s.AuthCacheTTL),
			"auth_event_window": adminDuration(s.AuthEventMaxAge),
			"supported_nips":    []int{1, 5, 11, 42},
		},
		"health": map[string]any{
			"cache":    cacheOK,
			"upstream": upstreamOK,
		},
	}
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
			"description": "Users must complete NIP-42 authentication and resolve to the same pubkey through Trustroots NIP-05.",
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
<title>Wrapster Admin</title>
<style>
:root {
  color-scheme: light dark;
  --bg: #f7f7f4;
  --fg: #1f2520;
  --muted: #667064;
  --line: #d7ddd3;
  --panel: #ffffff;
  --accent: #227f69;
  --danger: #b72d45;
}
@media (prefers-color-scheme: dark) {
  :root {
    --bg: #111411;
    --fg: #eef2ea;
    --muted: #a5afa1;
    --line: #333c34;
    --panel: #191e1a;
    --accent: #64c7ad;
    --danger: #ff8798;
  }
}
* { box-sizing: border-box; }
body {
  margin: 0;
  background: var(--bg);
  color: var(--fg);
  font: 15px/1.5 system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
}
header, main { max-width: 1080px; margin: 0 auto; padding: 24px; }
header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  border-bottom: 1px solid var(--line);
}
h1 { margin: 0; font-size: 24px; font-weight: 700; }
h2 { margin: 0 0 12px; font-size: 16px; }
button,
.toolbar a {
  border: 1px solid var(--accent);
  border-radius: 6px;
  min-height: 40px;
  padding: 0 14px;
  font: inherit;
  font-weight: 650;
}
button {
  background: var(--accent);
  color: #fff;
  cursor: pointer;
}
button.secondary,
.toolbar a {
  background: transparent;
  color: var(--accent);
}
.toolbar a {
  display: inline-flex;
  align-items: center;
  text-decoration: none;
}
button:disabled {
  opacity: .55;
  cursor: default;
}
.toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.header-status {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  justify-content: flex-end;
}
.health-strip {
  display: flex;
  align-items: center;
  gap: 10px;
  color: var(--muted);
  font-size: 14px;
}
.health-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  white-space: nowrap;
}
.status {
  color: var(--muted);
  overflow-wrap: anywhere;
}
.grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
}
section {
  background: var(--panel);
  border: 1px solid var(--line);
  border-radius: 8px;
  padding: 18px;
  min-width: 0;
}
dl { margin: 0; display: grid; gap: 10px; }
.row {
  display: grid;
  grid-template-columns: minmax(110px, .8fr) minmax(0, 1.2fr);
  gap: 12px;
  align-items: start;
}
dt { color: var(--muted); }
dd { margin: 0; overflow-wrap: anywhere; }
.ok { color: var(--accent); font-weight: 700; }
.bad { color: var(--danger); font-weight: 700; }
.wide { grid-column: 1 / -1; }
.lines { display: grid; gap: 4px; }
.lines code {
  font: 13px/1.5 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  overflow-wrap: anywhere;
}
.muted-line { color: var(--muted); }
.advert-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 12px;
}
.advert-card {
  border: 1px solid var(--line);
  border-radius: 8px;
  padding: 14px;
  display: grid;
  gap: 10px;
}
.advert-title { font-weight: 700; }
.advert-meta { color: var(--muted); overflow-wrap: anywhere; }
.advert-details {
  display: grid;
  gap: 8px;
}
.advert-detail {
  display: grid;
  gap: 4px;
}
.advert-detail-label {
  color: var(--muted);
  font-size: 13px;
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
  gap: 4px;
}
.access-list {
  display: grid;
  gap: 8px;
  margin-top: 12px;
}
.access-person {
  display: grid;
  gap: 2px;
}
dialog {
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--panel);
  color: var(--fg);
  max-width: min(720px, calc(100vw - 32px));
  width: 720px;
  padding: 18px;
}
dialog::backdrop { background: rgb(0 0 0 / .35); }
form {
  display: grid;
  gap: 12px;
}
label {
  display: grid;
  gap: 6px;
  color: var(--muted);
}
input,
select,
textarea {
  border: 1px solid var(--line);
  border-radius: 6px;
  background: var(--bg);
  color: var(--fg);
  font: inherit;
  padding: 10px;
  width: 100%;
}
textarea { min-height: 96px; resize: vertical; }
.form-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  flex-wrap: wrap;
}
@media (max-width: 820px) {
  header { align-items: flex-start; flex-direction: column; }
  .header-status { justify-content: flex-start; }
  .grid { grid-template-columns: 1fr; }
}
</style>
</head>
<body>
<header title="Users must complete NIP-42 authentication and resolve to the same pubkey through Trustroots NIP-05.">
  <div>
    <h1>Wrapster Admin</h1>
    <div class="status">NIP-42 authenticated Trustroots relay wrapper</div>
    <div id="identity" class="status">Not signed in</div>
    <div id="nip05" class="status"></div>
  </div>
  <div class="header-status">
    <div id="health" class="health-strip" aria-label="Health"></div>
    <div class="toolbar">
      <a href="/examples/service-advert-browser.html">Example browser</a>
      <button id="connect">Connect</button>
    </div>
  </div>
</header>
<main class="grid">
  <section class="wide">
    <h2>Advertise Services</h2>
    <div id="advert-services" class="advert-grid"></div>
    <div id="advert-status" class="status"></div>
  </section>
  <section class="wide">
    <h2>Relays</h2>
    <dl id="relays"></dl>
  </section>
</main>
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
  <p>Run <code>wrapster-connector</code> on the private network that can reach Jellyfin or Plex, preferably on a WireGuard address.</p>
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
<script>
const SERVICE_ADVERT_KIND = 31388;
const NIP02_FOLLOW_LIST_KIND = 3;
const TRUSTROOTS_PROFILE_KIND = 10390;
const DEFAULT_ADVERT_RELAYS = ["wss://relay.guaka.org", "wss://nip42.trustroots.org"];
const state = { pubkey: "", npub: "", connecting: false, overview: null, accessRules: {} };
const connectButton = document.getElementById("connect");
const identity = document.getElementById("identity");
const advertDialog = document.getElementById("advert-dialog");
const advertForm = document.getElementById("advert-form");
const advertCancel = document.getElementById("advert-cancel");
const advertStatus = document.getElementById("advert-status");
const connectorDialog = document.getElementById("connector-dialog");
const connectorClose = document.getElementById("connector-close");
const accessDialog = document.getElementById("access-dialog");
const accessClose = document.getElementById("access-close");

connectButton.addEventListener("click", connect);
advertForm.addEventListener("submit", publishAdvertFromForm);
advertCancel.addEventListener("click", () => advertDialog.close());
connectorClose.addEventListener("click", () => connectorDialog.close());
accessClose.addEventListener("click", () => accessDialog.close());
window.addEventListener("load", autoConnect);

async function connect() {
  if (state.connecting) return;
  if (!window.nostr) {
    identity.textContent = "NIP-07 extension unavailable";
    return;
  }
  state.connecting = true;
  connectButton.disabled = true;
  identity.textContent = "Connecting...";
  try {
    await loadAll();
    showSignedIdentity(state.pubkey);
    connectButton.textContent = "Connected";
  } catch (err) {
    if (err.pubkey) showSignedIdentity(err.pubkey);
    else identity.textContent = err.message || String(err);
    connectButton.disabled = false;
    connectButton.textContent = "Connect";
  } finally {
    state.connecting = false;
  }
}

async function autoConnect() {
  if (window.nostr || await waitForNostr()) {
    await connect();
  }
}

async function waitForNostr() {
  for (let attempt = 0; attempt < 20; attempt++) {
    await new Promise(resolve => setTimeout(resolve, 100));
    if (window.nostr) return true;
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
  renderStatus(data.status || {});
  renderIdentity(data.identity || {});
  renderRelays(data.status || {}, data.config || {});
  renderAdvertServices(data);
}

function renderRelays(status, config) {
  const relays = config.relays || {};
  const relayStatus = status.relay || {};
  const values = {
    "Public URL": relayStatus.public_url || "-",
    "Upstream relay": relays.upstream || relayStatus.upstream_url || "-",
    "Extra relays": linesNode(relays.additional || [])
  };
  render("relays", values);
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
    const button = document.createElement("button");
    button.type = "button";
    button.textContent = advertButtonLabel(service);
    button.addEventListener("click", () => openAdvertDialog(service));
    card.append(button);
    root.append(card);
  }
}

function advertButtonLabel(service) {
  return service.service === "media" ? "Advertise Media" : "Advertise";
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
  const byType = new Map();
  const mediaEntries = mediaServiceEntries(config);
  const mediaTypes = new Set(mediaEntries.map(([name]) => normalizeToken(name)));
  function add(service) {
    const type = normalizeToken(service.service || service.name || "");
    if (!type) return;
    const normalized = Object.assign({}, service, { service: type });
    const existing = byType.get(type);
    if (existing) {
      Object.assign(existing, normalized);
      return;
    }
    byType.set(type, normalized);
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
      lines: mediaServiceEntries(config).map(([name, rule]) => name + " \u2192 " + (rule || "(no rule)"))
    }
  ];
}

function accessRulesNode(config) {
  const entries = Object.entries(config.access_rules || {}).sort(([a], [b]) => a.localeCompare(b));
  const wrap = document.createElement("div");
  wrap.className = "lines";
  if (!entries.length) {
    const span = document.createElement("span");
    span.className = "muted-line";
    span.textContent = "none";
    wrap.append(span);
    return wrap;
  }
  for (const [name, rule] of entries) {
    if (rule.type === "nostr_follow" && rule.owner_pubkey) {
      wrap.append(accessRuleLine(name, rule));
    } else {
      const code = document.createElement("code");
      code.textContent = accessRuleSummary(name, rule);
      wrap.append(code);
    }
  }
  return wrap;
}

function accessRuleLine(name, rule) {
  const line = document.createElement("div");
  line.className = "access-rule-line";
  const code = document.createElement("code");
  code.textContent = accessRuleSummary(name, rule);
  const button = document.createElement("button");
  button.type = "button";
  button.className = "detail-link";
  button.textContent = "loading access count...";
  button.disabled = true;
  button.addEventListener("click", () => openAccessRuleModal(name));
  line.append(code, button);
  loadAccessRule(name, rule, button);
  return line;
}

function accessRuleSummary(name, rule) {
  return name + ": " + rule.type + (rule.relay ? " @ " + rule.relay : "");
}

async function loadAccessRule(name, rule, button) {
  try {
    const people = await resolveNostrFollowAccess(rule);
    state.accessRules[name] = { rule, people, error: "" };
    button.textContent = people.length + " " + (people.length === 1 ? "person" : "people");
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
    status.textContent = result.people.length + " " + (result.people.length === 1 ? "person" : "people") + " can access media through this rule.";
  }
  for (const person of result.people) {
    const item = document.createElement("div");
    item.className = "access-person";
    const nameLine = document.createElement("div");
    nameLine.textContent = person.trustroots_nip05 || "Trustroots NIP-05 not found";
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
  const follows = await relayEvents(relay, { kinds: [NIP02_FOLLOW_LIST_KIND], authors: [owner], limit: 1 }, "wrapster-access-follows");
  const latest = follows.sort((a, b) => (b.created_at || 0) - (a.created_at || 0))[0];
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
    const socket = new WebSocket(relay);
    const request = ["REQ", subID, filter];
    let requested = false;
    const timeout = window.setTimeout(() => finish(new Error("Relay lookup timed out")), 12000);

    function finish(err) {
      window.clearTimeout(timeout);
      if (socket.readyState === WebSocket.OPEN) socket.close();
      if (err) reject(err); else resolve(events);
    }

    function sendRequest() {
      if (requested || socket.readyState !== WebSocket.OPEN) return;
      requested = true;
      socket.send(JSON.stringify(request));
    }

    async function authenticate(challenge) {
      try {
        const authEvent = await window.nostr.signEvent({
          kind: 22242,
          created_at: Math.floor(Date.now() / 1000),
          tags: [["relay", relay], ["challenge", String(challenge)]],
          content: ""
        });
        socket.send(JSON.stringify(["AUTH", authEvent]));
      } catch (err) {
        finish(new Error("Relay auth failed: " + (err.message || String(err))));
      }
    }

    socket.addEventListener("open", sendRequest);
    socket.addEventListener("message", async (message) => {
      let payload;
      try {
        payload = JSON.parse(message.data);
      } catch {
        return;
      }
      if (payload[0] === "AUTH") {
        await authenticate(payload[1]);
        requested = false;
        sendRequest();
      } else if (payload[0] === "EVENT" && payload[1] === subID) {
        events.push(payload[2]);
      } else if (payload[0] === "EOSE" && payload[1] === subID) {
        finish();
      } else if (payload[0] === "CLOSED" && payload[1] === subID) {
        finish(new Error(String(payload[2] || "Relay closed subscription")));
      }
    });
    socket.addEventListener("error", () => finish(new Error("Relay connection failed")));
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
      value: proxy.access_rule || "open"
    },
    {
      label: "Proxy routes",
      lines: Object.entries(proxy.routes || {})
        .map(([name, url]) => prefix + "/" + name + " \u2192 " + url)
        .sort()
    }
  ];
}

function openAdvertDialog(service) {
  if (!state.pubkey) {
    advertStatus.textContent = "Connect before publishing an advert.";
    return;
  }
  const relays = advertRelays(state.overview || {});
  const audience = Array.isArray(service.audience) ? service.audience : ["trustroots"];
  document.getElementById("advert-heading").textContent = "Advertise " + (service.label || service.service);
  document.getElementById("advert-service").value = service.service;
  document.getElementById("advert-title").value = service.title || ("Wrapster " + titleizeService(service.service));
  document.getElementById("advert-summary").value = service.summary || ("Request-gated " + titleizeService(service.service) + " access through Wrapster");
  document.getElementById("advert-slug").value = advertSlug(service.service);
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
    if (accepted.length) advertDialog.close();
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
  return {
    kind: SERVICE_ADVERT_KIND,
    created_at: Math.floor(Date.now() / 1000),
    tags,
    content: document.getElementById("advert-content").value.trim()
  };
}

function publishAdvertToRelay(relay, event) {
  return new Promise((resolve) => {
    let socket;
    let done = false;
    let sent = false;
    let authEventId = "";
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
        sendTimer = window.setTimeout(sendEvent, 1200);
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
        if (payload[2] === true) sendEvent();
        else finish(false, String(payload[3] || "auth rejected"));
      } else if (type === "OK" && payload[1] === event.id) {
        finish(payload[2] === true, String(payload[3] || ""));
      } else if (type === "NOTICE") {
        const notice = String(payload[1] || "");
        if (/auth-required|restricted/.test(notice) && !sent) return;
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
  const el = document.getElementById("nip05");
  el.title = state.npub || state.pubkey || "";
  if (data.trustroots_nip05) {
    el.textContent = "Trustroots NIP-05: " + data.trustroots_nip05;
    identity.textContent = "";
    identity.title = "";
  } else {
    el.textContent = "Trustroots NIP-05: none found";
  }
}

async function signedFetch(path) {
  const url = new URL(path, window.location.href).toString();
  const event = await window.nostr.signEvent({
    kind: 27235,
    created_at: Math.floor(Date.now() / 1000),
    tags: [["u", url], ["method", "GET"]],
    content: ""
  });
  if (event.pubkey) {
    state.pubkey = event.pubkey;
    state.npub = npubEncode(event.pubkey);
  }
  const raw = JSON.stringify(event);
  const encoded = btoa(String.fromCharCode(...new TextEncoder().encode(raw)));
  const response = await fetch(url, { headers: { Authorization: "Nostr " + encoded } });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) {
    const err = new Error(body.error || response.statusText);
    err.pubkey = event.pubkey || state.pubkey;
    err.npub = npubEncode(err.pubkey);
    throw err;
  }
  return body;
}

function renderStatus(data) {
  renderHeaderHealth(data.health || {});
}

function renderHeaderHealth(data) {
  const root = document.getElementById("health");
  root.replaceChildren();
  root.append(healthItem("Cache", data.cache), healthItem("Upstream", data.upstream));
}

function healthItem(label, value) {
  const item = document.createElement("span");
  item.className = "health-item";
  const name = document.createElement("span");
  name.textContent = label;
  item.append(name, bool(value));
  return item;
}

function showSignedIdentity(pubkey) {
  state.pubkey = pubkey || state.pubkey;
  state.npub = npubEncode(state.pubkey);
  identity.textContent = "Signed in";
  identity.title = state.npub || state.pubkey || "";
}

function render(id, values) {
  const root = document.getElementById(id);
  root.replaceChildren();
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

function bool(value) {
  const span = document.createElement("span");
  span.className = value ? "ok" : "bad";
  span.textContent = value ? "OK" : "Fail";
  return span;
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
