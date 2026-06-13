package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	adminauth "github.com/trustroots/nostroots/vibe/wrapster/internal/admin"
)

type SetupHandler struct {
	Connector  *Connector
	ConfigPath string
	Auth       adminauth.Authorizer
}

func LoadConnectorMediaConfig(path string) (ConnectorMediaConfig, bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return ConnectorMediaConfig{}, false, nil
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ConnectorMediaConfig{}, false, nil
	}
	if err != nil {
		return ConnectorMediaConfig{}, false, err
	}
	var cfg ConnectorMediaConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		return ConnectorMediaConfig{}, false, err
	}
	if err := validateConnectorMediaConfig(cfg); err != nil {
		return ConnectorMediaConfig{}, false, err
	}
	return normalizedConnectorMediaConfig(cfg), true, nil
}

func SaveConnectorMediaConfig(path string, cfg ConnectorMediaConfig) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("connector config path is empty")
	}
	cfg = normalizedConnectorMediaConfig(cfg)
	if err := validateConnectorMediaConfig(cfg); err != nil {
		return err
	}
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".connector-config-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func (h SetupHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/setup" || r.URL.Path == "/setup/":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(setupHTML))
	case r.URL.Path == "/setup/api/status":
		h.status(w, r)
	case r.URL.Path == "/setup/api/config":
		h.config(w, r)
	case r.URL.Path == "/setup/api/test/jellyfin":
		h.test(w, r, "jellyfin")
	case r.URL.Path == "/setup/api/test/plex":
		h.test(w, r, "plex")
	default:
		http.NotFound(w, r)
	}
}

func (h SetupHandler) status(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg := h.connector().MediaConfig()
	writeJSON(w, http.StatusOK, map[string]any{
		"config_path": strings.TrimSpace(h.ConfigPath) != "",
		"admin_auth":  len(h.Auth.Admins) > 0,
		"services": map[string]any{
			"jellyfin": serviceSetupStatus(cfg.JellyfinBaseURL, cfg.JellyfinAPIKey),
			"plex":     serviceSetupStatus(cfg.PlexBaseURL, cfg.PlexToken),
		},
	})
}

func (h SetupHandler) config(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, redactedConnectorMediaConfig(h.connector().MediaConfig()))
	case http.MethodPut:
		if _, err := h.Auth.VerifyRequest(r); err != nil {
			writeJSON(w, setupAuthStatus(err), map[string]string{"error": err.Error()})
			return
		}
		var cfg ConnectorMediaConfig
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&cfg); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		cfg = h.mergeWithExistingSecrets(cfg)
		if err := validateConnectorMediaConfig(cfg); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := SaveConnectorMediaConfig(h.ConfigPath, cfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		h.connector().SetMediaConfig(cfg)
		writeJSON(w, http.StatusOK, redactedConnectorMediaConfig(cfg))
	default:
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h SetupHandler) test(w http.ResponseWriter, r *http.Request, service string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, err := h.Auth.VerifyRequest(r); err != nil {
		writeJSON(w, setupAuthStatus(err), map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	cfg := h.connector().MediaConfig()
	if r.Body != nil {
		var candidate ConnectorMediaConfig
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&candidate); err == nil {
			cfg = h.mergeWithExistingSecrets(candidate)
		}
	}
	var err error
	switch service {
	case "jellyfin":
		err = h.connector().testJellyfin(ctx, cfg)
	case "plex":
		err = h.connector().testPlex(ctx, cfg)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h SetupHandler) connector() *Connector {
	if h.Connector != nil {
		return h.Connector
	}
	return &Connector{}
}

func (h SetupHandler) mergeWithExistingSecrets(candidate ConnectorMediaConfig) ConnectorMediaConfig {
	existing := h.connector().MediaConfig()
	candidate = normalizedConnectorMediaConfig(candidate)
	if candidate.JellyfinBaseURL == "" {
		candidate.JellyfinAPIKey = ""
	} else if candidate.JellyfinAPIKey == "" {
		candidate.JellyfinAPIKey = existing.JellyfinAPIKey
	}
	if candidate.PlexBaseURL == "" {
		candidate.PlexToken = ""
	} else if candidate.PlexToken == "" {
		candidate.PlexToken = existing.PlexToken
	}
	return candidate
}

func (c *Connector) testJellyfin(ctx context.Context, cfg ConnectorMediaConfig) error {
	if strings.TrimSpace(cfg.JellyfinBaseURL) == "" {
		return errServiceNotConfigured
	}
	u, err := url.Parse(cfg.JellyfinBaseURL)
	if err != nil {
		return err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/System/Info"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	if cfg.JellyfinAPIKey != "" {
		req.Header.Set("X-Emby-Token", cfg.JellyfinAPIKey)
	}
	return c.checkSetupRequest(req, "jellyfin")
}

func (c *Connector) testPlex(ctx context.Context, cfg ConnectorMediaConfig) error {
	if strings.TrimSpace(cfg.PlexBaseURL) == "" {
		return errServiceNotConfigured
	}
	u, err := url.Parse(cfg.PlexBaseURL)
	if err != nil {
		return err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/identity"
	if cfg.PlexToken != "" {
		q := u.Query()
		q.Set("X-Plex-Token", cfg.PlexToken)
		u.RawQuery = q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	return c.checkSetupRequest(req, "plex")
}

func (c *Connector) checkSetupRequest(req *http.Request, service string) error {
	resp, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s returned %s", service, resp.Status)
	}
	return nil
}

func setupAuthStatus(err error) int {
	if errors.Is(err, adminauth.ErrNotAdmin) {
		return http.StatusForbidden
	}
	return http.StatusUnauthorized
}

func normalizedConnectorMediaConfig(cfg ConnectorMediaConfig) ConnectorMediaConfig {
	cfg.JellyfinBaseURL = strings.TrimRight(strings.TrimSpace(cfg.JellyfinBaseURL), "/")
	cfg.JellyfinAPIKey = strings.TrimSpace(cfg.JellyfinAPIKey)
	cfg.PlexBaseURL = strings.TrimRight(strings.TrimSpace(cfg.PlexBaseURL), "/")
	cfg.PlexToken = strings.TrimSpace(cfg.PlexToken)
	return cfg
}

func validateConnectorMediaConfig(cfg ConnectorMediaConfig) error {
	if err := validateSetupHTTPURL("jellyfin_base_url", cfg.JellyfinBaseURL); err != nil {
		return err
	}
	return validateSetupHTTPURL("plex_base_url", cfg.PlexBaseURL)
}

func validateSetupHTTPURL(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("%s must be http:// or https://", name)
	}
	return nil
}

func redactedConnectorMediaConfig(cfg ConnectorMediaConfig) map[string]any {
	return map[string]any{
		"jellyfin": serviceSetupStatus(cfg.JellyfinBaseURL, cfg.JellyfinAPIKey),
		"plex":     serviceSetupStatus(cfg.PlexBaseURL, cfg.PlexToken),
	}
}

func serviceSetupStatus(baseURL, token string) map[string]any {
	return map[string]any{
		"base_url":         strings.TrimSpace(baseURL),
		"configured":       strings.TrimSpace(baseURL) != "",
		"token_configured": strings.TrimSpace(token) != "",
	}
}

const setupHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Wrapster NAS Setup</title>
  <style>
    :root { color-scheme: light dark; font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    body { margin: 0; background: #f6f4ef; color: #20211f; }
    main { max-width: 920px; margin: 0 auto; padding: 32px 20px 48px; }
    header { display: flex; justify-content: space-between; gap: 16px; align-items: center; margin-bottom: 24px; }
    h1 { font-size: 28px; margin: 0; }
    .status { font-size: 14px; color: #5d635e; }
    .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(280px, 1fr)); gap: 16px; }
    section { background: #fffdfa; border: 1px solid #ddd8cc; border-radius: 8px; padding: 18px; }
    h2 { margin: 0 0 14px; font-size: 18px; }
    label { display: grid; gap: 6px; margin: 12px 0; font-size: 13px; color: #555b55; }
    input { min-height: 38px; border: 1px solid #c7c2b7; border-radius: 6px; padding: 0 10px; font: inherit; background: #fff; color: #20211f; }
    .actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 16px; }
    button { min-height: 38px; border: 1px solid #1c5f5a; border-radius: 6px; padding: 0 12px; font: inherit; background: #1f6f67; color: white; cursor: pointer; }
    button.secondary { background: transparent; color: #1f5f59; }
    button:disabled { opacity: .55; cursor: not-allowed; }
    pre { overflow: auto; white-space: pre-wrap; background: #f0ede6; border-radius: 6px; padding: 12px; font-size: 12px; }
    .identity-output { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 8px; align-items: center; }
    .identity-output input { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; }
    .ok { color: #18734f; }
    .bad { color: #9b2f28; }
    @media (prefers-color-scheme: dark) {
      body { background: #171815; color: #f4f0e8; }
      section { background: #20221e; border-color: #3c4038; }
      input { background: #171815; color: #f4f0e8; border-color: #55594f; }
      pre { background: #171815; }
      .status, label { color: #b8b2a6; }
      button.secondary { color: #8ad6ce; }
    }
  </style>
</head>
<body>
<main>
  <header>
    <div>
      <h1>Wrapster NAS Setup</h1>
      <div class="status" id="identity">NIP-07 not connected</div>
    </div>
    <button class="secondary" id="connect">Connect</button>
  </header>
  <div class="grid">
    <section>
      <h2>Jellyfin</h2>
      <label>Base URL <input id="jellyfin-url" placeholder="http://192.168.1.20:8096"></label>
      <label>API key <input id="jellyfin-key" type="password" autocomplete="off" placeholder="Leave blank to keep existing"></label>
      <div class="actions"><button id="test-jellyfin" class="secondary">Test</button></div>
    </section>
    <section>
      <h2>Plex</h2>
      <label>Base URL <input id="plex-url" placeholder="http://192.168.1.20:32400"></label>
      <label>Token <input id="plex-token" type="password" autocomplete="off" placeholder="Leave blank to keep existing"></label>
      <div class="actions"><button id="test-plex" class="secondary">Test</button></div>
    </section>
  </div>
  <section style="margin-top:16px">
    <h2>FIPS Identity</h2>
    <div class="status">Generate a fresh sidecar secret locally, then store it as <code>FIPS_HOME_NSEC</code>.</div>
    <div class="identity-output" style="margin-top:12px">
      <input id="fips-nsec" readonly placeholder="nsec1...">
      <button id="copy-fips-nsec" class="secondary">Copy</button>
    </div>
    <div class="actions">
      <button id="generate-fips-nsec" class="secondary">Generate nsec</button>
    </div>
  </section>
  <section style="margin-top:16px">
    <h2>Status</h2>
    <pre id="status">Loading...</pre>
    <div class="actions">
      <button id="save">Save settings</button>
      <button id="refresh" class="secondary">Refresh</button>
    </div>
  </section>
</main>
<script>
let currentPubkey = "";
const $ = (id) => document.getElementById(id);
function b64(json) {
  const bytes = new TextEncoder().encode(json);
  let text = "";
  bytes.forEach((b) => text += String.fromCharCode(b));
  return btoa(text);
}
const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l";
function bech32Encode(hrp, data) {
  const combined = data.concat(bech32Checksum(hrp, data));
  return hrp + "1" + combined.map((value) => bech32Charset[value]).join("");
}
function bech32Checksum(hrp, data) {
  const values = bech32HrpExpand(hrp).concat(data, [0, 0, 0, 0, 0, 0]);
  const mod = bech32Polymod(values) ^ 1;
  const checksum = [];
  for (let p = 0; p < 6; p++) checksum.push((mod >> (5 * (5 - p))) & 31);
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
  if (pad && bits > 0) out.push((acc << (toBits - bits)) & maxValue);
  else if (bits >= fromBits || ((acc << (toBits - bits)) & maxValue) !== 0) return [];
  return out;
}
async function connect() {
  if (!window.nostr) throw new Error("NIP-07 extension not found");
  currentPubkey = await window.nostr.getPublicKey();
  $("identity").textContent = "Connected " + currentPubkey.slice(0, 12) + "...";
}
async function signedFetch(path, options = {}) {
  if (!currentPubkey) await connect();
  const method = options.method || "GET";
  const url = new URL(path, location.href).toString();
  const event = await window.nostr.signEvent({
    kind: 27235,
    created_at: Math.floor(Date.now() / 1000),
    tags: [["u", url], ["method", method]],
    content: ""
  });
  const headers = new Headers(options.headers || {});
  headers.set("Authorization", "Nostr " + b64(JSON.stringify(event)));
  return fetch(path, {...options, headers});
}
async function load() {
  const [cfg, status] = await Promise.all([
    fetch("/setup/api/config").then(r => r.json()),
    fetch("/setup/api/status").then(r => r.json())
  ]);
  $("jellyfin-url").value = cfg.jellyfin?.base_url || "";
  $("plex-url").value = cfg.plex?.base_url || "";
  $("status").textContent = JSON.stringify(status, null, 2);
}
function payload() {
  return {
    jellyfin_base_url: $("jellyfin-url").value,
    jellyfin_api_key: $("jellyfin-key").value,
    plex_base_url: $("plex-url").value,
    plex_token: $("plex-token").value
  };
}
async function save() {
  const res = await signedFetch("/setup/api/config", {
    method: "PUT",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify(payload())
  });
  const body = await res.json();
  if (!res.ok) throw new Error(body.error || "save failed");
  $("jellyfin-key").value = "";
  $("plex-token").value = "";
  await load();
}
async function test(service) {
  const res = await signedFetch("/setup/api/test/" + service, {
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify(payload())
  });
  const body = await res.json();
  if (!res.ok) throw new Error(body.error || "test failed");
  await load();
}
function generateFipsNsec() {
  if (!window.crypto || typeof window.crypto.getRandomValues !== "function") throw new Error("Secure random generator unavailable");
  const bytes = new Uint8Array(32);
  window.crypto.getRandomValues(bytes);
  $("fips-nsec").value = bech32Encode("nsec", convertBits(Array.from(bytes), 8, 5, true));
  $("status").textContent = "Generated locally. Store it once; it is not saved by Wrapster.";
}
async function copyFipsNsec() {
  const value = $("fips-nsec").value;
  if (!value) throw new Error("Generate an nsec first");
  try { await navigator.clipboard.writeText(value); }
  catch {
    $("fips-nsec").select();
    document.execCommand("copy");
  }
  $("status").textContent = "Copied.";
}
function run(button, fn) {
  return async () => {
    button.disabled = true;
    try { await fn(); }
    catch (err) { $("status").textContent = String(err.message || err); }
    finally { button.disabled = false; }
  };
}
$("connect").onclick = run($("connect"), connect);
$("refresh").onclick = run($("refresh"), load);
$("save").onclick = run($("save"), save);
$("test-jellyfin").onclick = run($("test-jellyfin"), () => test("jellyfin"));
$("test-plex").onclick = run($("test-plex"), () => test("plex"));
$("generate-fips-nsec").onclick = run($("generate-fips-nsec"), generateFipsNsec);
$("copy-fips-nsec").onclick = run($("copy-fips-nsec"), copyFipsNsec);
load();
</script>
</body>
</html>`
