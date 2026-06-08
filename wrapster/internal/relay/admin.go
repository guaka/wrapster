package relay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

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
	case "/admin/api/status":
		s.adminStatus(w, r, pubkey)
	case "/admin/api/auth-cache":
		s.adminAuthCache(w, r, pubkey)
	case "/admin/api/policy":
		s.adminPolicy(w, r, pubkey)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) adminStatus(w http.ResponseWriter, r *http.Request, pubkey string) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()

	cacheOK := s.Cache == nil || s.Cache.Ping(ctx) == nil
	upstreamOK := false
	if conn, err := s.Upstream.Dial(ctx); err == nil {
		upstreamOK = true
		_ = conn.Close()
	}

	writeJSON(w, http.StatusOK, map[string]any{
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
			"auth_cache_ttl":    s.AuthCacheTTL.String(),
			"auth_event_window": s.AuthEventMaxAge.String(),
			"supported_nips":    []int{1, 5, 11, 42},
		},
		"health": map[string]any{
			"cache":    cacheOK,
			"upstream": upstreamOK,
		},
	})
}

func (s *Server) adminAuthCache(w http.ResponseWriter, r *http.Request, pubkey string) {
	_ = pubkey
	if s.Cache == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled": false,
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	summary, err := s.Cache.Summary(ctx, s.now())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":     true,
		"total":       summary.Total,
		"valid":       summary.Valid,
		"expired":     summary.Expired,
		"oldest_seen": unixTimeOrNil(summary.OldestUnix),
		"newest_seen": unixTimeOrNil(summary.NewestUnix),
	})
}

func (s *Server) adminPolicy(w http.ResponseWriter, r *http.Request, pubkey string) {
	writeJSON(w, http.StatusOK, map[string]any{
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
	})
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
button {
  border: 1px solid var(--accent);
  border-radius: 6px;
  background: var(--accent);
  color: #fff;
  min-height: 40px;
  padding: 0 14px;
  font: inherit;
  font-weight: 650;
  cursor: pointer;
}
button.secondary {
  background: transparent;
  color: var(--accent);
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
@media (max-width: 820px) {
  header { align-items: flex-start; flex-direction: column; }
  .grid { grid-template-columns: 1fr; }
}
</style>
</head>
<body>
<header>
  <div>
    <h1>Wrapster Admin</h1>
    <div id="identity" class="status">Not signed in</div>
  </div>
  <div class="toolbar">
    <button id="connect">Connect</button>
    <button id="refresh" class="secondary" disabled>Refresh</button>
  </div>
</header>
<main class="grid">
  <section>
    <h2>Health</h2>
    <dl id="health"></dl>
  </section>
  <section>
    <h2>Relay</h2>
    <dl id="relay"></dl>
  </section>
  <section>
    <h2>Auth Cache</h2>
    <dl id="cache"></dl>
  </section>
  <section>
    <h2>Access Policy</h2>
    <dl id="policy"></dl>
  </section>
  <section>
    <h2>Admin Policy</h2>
    <dl id="admin-policy"></dl>
  </section>
  <section>
    <h2>Service</h2>
    <dl id="service"></dl>
  </section>
</main>
<script>
const state = { pubkey: "" };
const connectButton = document.getElementById("connect");
const refreshButton = document.getElementById("refresh");
const identity = document.getElementById("identity");

connectButton.addEventListener("click", connect);
refreshButton.addEventListener("click", loadAll);

async function connect() {
  if (!window.nostr) {
    identity.textContent = "NIP-07 extension unavailable";
    return;
  }
  state.pubkey = await window.nostr.getPublicKey();
  identity.textContent = state.pubkey;
  refreshButton.disabled = false;
  await loadAll();
}

async function loadAll() {
  await Promise.all([
    loadStatus(),
    loadCache(),
    loadPolicy()
  ]);
}

async function signedFetch(path) {
  const url = new URL(path, window.location.href).toString();
  const event = await window.nostr.signEvent({
    kind: 27235,
    created_at: Math.floor(Date.now() / 1000),
    tags: [["u", url], ["method", "GET"]],
    content: ""
  });
  const raw = JSON.stringify(event);
  const encoded = btoa(String.fromCharCode(...new TextEncoder().encode(raw)));
  const response = await fetch(url, { headers: { Authorization: "Nostr " + encoded } });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(body.error || response.statusText);
  }
  return body;
}

async function loadStatus() {
  try {
    const data = await signedFetch("/admin/api/status");
    render("health", {
      Cache: bool(data.health.cache),
      Upstream: bool(data.health.upstream)
    });
    render("relay", {
      "Public URL": data.relay.public_url,
      "Upstream URL": data.relay.upstream_url,
      "Auth TTL": data.relay.auth_cache_ttl,
      "Auth window": data.relay.auth_event_window,
      "NIPs": data.relay.supported_nips.join(", ")
    });
    render("service", {
      Name: data.service.name,
      Description: data.service.description,
      "Admin count": data.service.admin_pubkeys_count,
      "Admin auth max age": data.service.admin_auth_max_age_ms + " ms"
    });
  } catch (err) {
    renderError("health", err);
  }
}

async function loadCache() {
  try {
    const data = await signedFetch("/admin/api/auth-cache");
    render("cache", {
      Enabled: bool(data.enabled),
      Total: data.total ?? "0",
      Valid: data.valid ?? "0",
      Expired: data.expired ?? "0",
      "Oldest seen": data.oldest_seen || "-",
      "Newest seen": data.newest_seen || "-"
    });
  } catch (err) {
    renderError("cache", err);
  }
}

async function loadPolicy() {
  try {
    const data = await signedFetch("/admin/api/policy");
    render("policy", {
      Name: data.access_rule.name,
      Description: data.access_rule.description,
      "Profile kinds": data.access_rule.profile_kinds.join(", "),
      "Label namespace": data.access_rule.profile_label_namespace
    });
    render("admin-policy", {
      Name: data.admin_rule.name,
      Description: data.admin_rule.description,
      "Admin count": data.admin_rule.admin_count
    });
  } catch (err) {
    renderError("policy", err);
    renderError("admin-policy", err);
  }
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
  render(id, { Error: err.message || String(err) });
}

function bool(value) {
  const span = document.createElement("span");
  span.className = value ? "ok" : "bad";
  span.textContent = value ? "OK" : "Fail";
  return span;
}
</script>
</body>
</html>`
