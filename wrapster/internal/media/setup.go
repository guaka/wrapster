package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nbd-wtf/go-nostr"
	adminauth "github.com/trustroots/nostroots/vibe/wrapster/internal/admin"
	"github.com/trustroots/nostroots/vibe/wrapster/internal/adminui"
	"github.com/trustroots/nostroots/vibe/wrapster/internal/buildinfo"
	"github.com/trustroots/nostroots/vibe/wrapster/internal/fips"
)

type SetupHandler struct {
	Connector    *Connector
	ConfigPath   string
	FIPSNsecPath string
	Auth         adminauth.Authorizer
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
	case r.URL.Path == "/admin" || r.URL.Path == "/admin/":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		html := adminui.InjectShared(setupHTML)
		html = strings.ReplaceAll(html, "{{BUILD_TIME}}", buildinfo.DisplayBuildTime())
		_, _ = w.Write([]byte(html))
	case r.URL.Path == "/setup" || r.URL.Path == "/setup/":
		http.Redirect(w, r, "/admin", http.StatusFound)
		return
	case r.URL.Path == "/setup/favicon.svg" || r.URL.Path == "/setup/favicon.ico":
		h.favicon(w, r)
	case r.URL.Path == "/setup/api/status":
		h.status(w, r)
	case r.URL.Path == "/setup/api/config":
		h.config(w, r)
	case r.URL.Path == "/setup/api/fips-nsec":
		h.fipsNsec(w, r)
	case r.URL.Path == "/setup/api/fips-peer-check":
		h.fipsPeerCheck(w, r)
	case r.URL.Path == "/setup/api/test/jellyfin":
		h.test(w, r, "jellyfin")
	case r.URL.Path == "/setup/api/test/jellyfin-random-song":
		h.testJellyfinRandomSong(w, r)
	case strings.HasPrefix(r.URL.Path, "/setup/api/test/jellyfin-random-song/stream/"):
		h.streamJellyfinRandomSong(w, r)
	case r.URL.Path == "/setup/api/test/plex":
		h.test(w, r, "plex")
	default:
		http.NotFound(w, r)
	}
}

func (h SetupHandler) testJellyfinRandomSong(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, err := h.Auth.VerifyRequest(r); err != nil {
		writeJSON(w, setupAuthStatus(err), map[string]string{"error": err.Error()})
		return
	}
	cfg := h.connector().MediaConfig()
	if r.Body != nil {
		var candidate ConnectorMediaConfig
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&candidate); err == nil {
			cfg = h.mergeWithExistingSecrets(candidate)
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	result, err := h.connector().RandomJellyfinSong(ctx, cfg)
	if result.Item.StreamID != "" {
		result.StreamURL = "/setup/api/test/jellyfin-random-song/stream/" + url.PathEscape(result.Item.StreamID)
	}
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, errServiceNotConfigured) {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, map[string]any{
			"error": err.Error(),
			"debug": result.Debug,
		})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h SetupHandler) streamJellyfinRandomSong(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, err := h.Auth.VerifyRequest(r); err != nil {
		writeJSON(w, setupAuthStatus(err), map[string]string{"error": err.Error()})
		return
	}
	streamID := strings.TrimPrefix(r.URL.Path, "/setup/api/test/jellyfin-random-song/stream/")
	if streamID == "" {
		http.NotFound(w, r)
		return
	}
	cfg := h.connector().MediaConfig()
	if r.Method == http.MethodPost && r.Body != nil {
		var candidate ConnectorMediaConfig
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&candidate); err == nil {
			cfg = h.mergeWithExistingSecrets(candidate)
		}
	}
	req, err := h.connector().jellyfinStreamRequestWithConfig(r, cfg, streamID)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errServiceNotConfigured) {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	resp, err := h.connector().client().Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	defer resp.Body.Close()
	for _, name := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if value := resp.Header.Get(name); value != "" {
			w.Header().Set(name, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (h SetupHandler) fipsNsec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, err := h.Auth.VerifyRequest(r); err != nil {
		writeJSON(w, setupAuthStatus(err), map[string]string{"error": err.Error()})
		return
	}
	var payload struct {
		Nsec string `json:"nsec"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	identity, err := fips.SaveNsec(h.FIPSNsecPath, payload.Nsec)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"saved": true,
		"npub":  identity.Npub,
	})
}

func (h SetupHandler) fipsPeerCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, err := h.Auth.VerifyRequest(r); err != nil {
		writeJSON(w, setupAuthStatus(err), map[string]string{"error": err.Error()})
		return
	}
	var payload struct {
		FIPSPeerNpub string `json:"fips_peer_npub"`
		FIPSPeerAddr string `json:"fips_peer_addr"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	check := checkFIPSPeerConnectivity(payload.FIPSPeerNpub, payload.FIPSPeerAddr)
	reachable, _ := check["reachable"].(bool)
	peerAddrSet, _ := check["peer_addr_set"].(bool)
	if !reachable && peerAddrSet {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"check": check,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"check": check,
	})
}

func (h SetupHandler) status(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg := h.connector().MediaConfig()
	peerCheck := checkFIPSPeerConnectivity(cfg.FIPSPeerNpub, cfg.FIPSPeerAddr)
	peerList := fips.PeerList(cfg.FIPSPeerNpub, cfg.FIPSPeerAddr, peerCheck)
	writeJSON(w, http.StatusOK, map[string]any{
		"config_path": strings.TrimSpace(h.ConfigPath) != "",
		"admin_auth":  len(h.Auth.Admins) > 0,
		"fips":        h.setupFIPSPayload(),
		"fips_peers":  peerList,
		"services": map[string]any{
			"jellyfin": serviceSetupStatus(cfg.JellyfinBaseURL, cfg.JellyfinAPIKey),
			"plex":     serviceSetupStatus(cfg.PlexBaseURL, cfg.PlexToken),
		},
		"fips_peer": map[string]any{
			"npub":            cfg.FIPSPeerNpub,
			"peer_addr":       cfg.FIPSPeerAddr,
			"configured":      strings.TrimSpace(cfg.FIPSPeerNpub) != "",
			"addr_configured": strings.TrimSpace(cfg.FIPSPeerAddr) != "",
			"check":           peerCheck,
		},
	})
}

func (h SetupHandler) setupFIPSPayload() map[string]any {
	path := strings.TrimSpace(h.FIPSNsecPath)
	out := map[string]any{
		"configured": false,
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
	out["configured"] = true
	return out
}

func checkFIPSPeerConnectivity(peerNpub, peerAddr string) map[string]any {
	peerNpub = strings.TrimSpace(peerNpub)
	peerAddr = strings.TrimSpace(peerAddr)
	status := map[string]any{
		"peer_npub":               peerNpub,
		"peer_addr":               peerAddr,
		"peer_npub_ok":            true,
		"peer_addr_set":           peerAddr != "",
		"transport_check_skipped": peerAddr == "",
		"reachable":               false,
		"transport":               inferFIPSPeerTransport(peerAddr),
	}

	if peerNpub == "" {
		status["peer_npub_ok"] = false
		status["error"] = "fips_peer_npub is not set"
		return status
	}
	if adminauth.NormalizePubkey(peerNpub) == "" {
		status["peer_npub_ok"] = false
		status["error"] = "fips_peer_npub must be a valid npub or hex public key"
		return status
	}

	if peerAddr == "" {
		status["state"] = "waiting_for_peer_addr"
		status["message"] = "peer identity is configured; add the public peer address to test outbound transport"
		return status
	}
	if _, _, err := net.SplitHostPort(peerAddr); err != nil {
		status["error"] = "fips_peer_addr must be host:port"
		return status
	}
	transport := inferFIPSPeerTransport(peerAddr)
	status["transport"] = transport
	reachable, usedTransport, err := testFIPSPeerAddress(peerAddr, transport)
	status["transport"] = usedTransport
	status["reachable"] = reachable
	if err != nil {
		status["error"] = err.Error()
	}
	return status
}

func inferFIPSPeerTransport(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "unknown"
	}
	switch port {
	case "2121":
		return "udp"
	case "8443":
		return "tcp"
	default:
		return "tcp"
	}
}

func testFIPSPeerAddress(addr, transport string) (bool, string, error) {
	switch strings.ToLower(transport) {
	case "udp":
		if err := testFIPSPeerUDP(addr); err != nil {
			return false, "udp", err
		}
		return true, "udp", nil
	default:
		if err := testFIPSPeerTCP(addr); err != nil {
			return false, "tcp", err
		}
		return true, "tcp", nil
	}
}

func testFIPSPeerTCP(addr string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

func testFIPSPeerUDP(addr string) error {
	conn, err := net.DialTimeout("udp", addr, 4*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetWriteDeadline(time.Now().Add(4 * time.Second)); err != nil {
		return err
	}
	_, err = conn.Write([]byte{0})
	return err
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
	candidate.FIPSPeerNpub = strings.TrimSpace(candidate.FIPSPeerNpub)
	candidate.FIPSPeerAddr = strings.TrimSpace(candidate.FIPSPeerAddr)
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
	cfg.FIPSPeerNpub = strings.TrimSpace(cfg.FIPSPeerNpub)
	cfg.FIPSPeerAddr = strings.TrimSpace(cfg.FIPSPeerAddr)
	return cfg
}

func validateConnectorMediaConfig(cfg ConnectorMediaConfig) error {
	if err := validateSetupHTTPURL("jellyfin_base_url", cfg.JellyfinBaseURL); err != nil {
		return err
	}
	if err := validateSetupHTTPURL("plex_base_url", cfg.PlexBaseURL); err != nil {
		return err
	}
	if err := validateFIPSPeerNpub(cfg.FIPSPeerNpub); err != nil {
		return err
	}
	return validateFIPSPeerAddr(cfg.FIPSPeerAddr)
}

func validateFIPSPeerNpub(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if normalized := adminauth.NormalizePubkey(value); normalized == "" || !nostr.IsValidPublicKeyHex(normalized) {
		return fmt.Errorf("fips_peer_npub must be a valid npub or hex pubkey")
	}
	return nil
}

func validateFIPSPeerAddr(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if _, _, err := net.SplitHostPort(value); err != nil {
		return fmt.Errorf("fips_peer_addr must be host:port")
	}
	return nil
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
		"jellyfin":       serviceSetupStatus(cfg.JellyfinBaseURL, cfg.JellyfinAPIKey),
		"plex":           serviceSetupStatus(cfg.PlexBaseURL, cfg.PlexToken),
		"fips_peer_npub": strings.TrimSpace(cfg.FIPSPeerNpub),
		"fips_peer_addr": strings.TrimSpace(cfg.FIPSPeerAddr),
	}
}

func (h SetupHandler) favicon(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	_, _ = w.Write([]byte(setupFaviconSVG))
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
    <link rel="icon" href="/setup/favicon.svg" type="image/svg+xml">
  <style>
    {{ADMIN_COMMON_CSS}}
    #setup-content {
      display: grid;
      gap: 14px;
    }
    main > header {
      width: 100%;
    }
    #setup-content.hidden {
      display: none !important;
    }
    .service-box {
      align-content: start;
    }
    #status,
    #fips-peers,
    #fips-peer-check-result {
      border: 1px solid var(--line);
      border-radius: 8px;
      background: rgba(5, 8, 13, .62);
      padding: 12px;
    }
    #fips-peer-check-result.hidden {
      display: none !important;
    }
  </style>
</head>
<body>
<main>
  <header>
    <div class="brand-block">
      <h1>Wrapster NAS Setup</h1>
      <div class="status identity-line" id="identity">NIP-07 not connected</div>
    </div>
    <div class="header-status">
      <div class="toolbar">
        <button class="connect-button" id="connect">Connect</button>
        <div id="connect-status" class="connect-status hidden"></div>
      </div>
      <div id="header-fips-status" class="fips-header-status neutral">FIPS peer: checking status...</div>
    </div>
  </header>
  <div id="setup-content" class="hidden">
    <section>
      <h2>FIPS Identity</h2>
      <div class="status">Generate and activate a fresh FIPS sidecar identity for this deployment.</div>
      <div class="identity-output">
        <input id="fips-npub" readonly placeholder="npub1...">
        <button id="copy-fips-npub" class="secondary">Copy npub</button>
      </div>
      <div id="fips-secret-row" class="identity-output secret-output hidden">
        <input id="fips-nsec" readonly type="password" autocomplete="off" placeholder="nsec1...">
        <button id="reveal-fips-nsec" class="secondary icon-button" aria-label="Reveal nsec" title="Reveal nsec"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M2 12s4-7 10-7 10 7 10 7-4 7-10 7-10-7-10-7Z"></path><circle cx="12" cy="12" r="3"></circle></svg></button>
        <button id="copy-fips-nsec" class="secondary">Copy secret</button>
      </div>
      <div class="actions">
        <button id="generate-fips-nsec" class="secondary">Generate identity</button>
      </div>
    </section>
    <section>
      <h2>FIPS Peer</h2>
      <label>Public wrapster npub
        <input id="fips-peer-npub" placeholder="npub1...">
      </label>
      <label>Public wrapster FIPS address (host:port; optional)
        <input id="fips-peer-addr" placeholder="public.example.org:8443">
      </label>
      <div class="actions">
        <button id="test-fips-peer" class="secondary">Test FIPS peer</button>
      </div>
      <div id="fips-peer-check-result" class="hidden"></div>
    </section>
    <section>
      <h2>FIPS Peers</h2>
      <div id="fips-peers" class="status">No peers configured</div>
    </section>
    <div class="grid">
      <section class="service-box">
        <h2>Jellyfin</h2>
        <label>Base URL <input id="jellyfin-url" placeholder="http://192.168.1.20:8096"></label>
        <div class="field-links">
          <a id="jellyfin-url-link" class="field-link" href="" target="_blank" rel="noopener noreferrer" aria-disabled="true">Open Jellyfin</a>
          <a id="jellyfin-token-link" class="field-link" href="" target="_blank" rel="noopener noreferrer" aria-disabled="true">Get Jellyfin API key</a>
        </div>
        <label>API key <input id="jellyfin-key" type="password" autocomplete="off" placeholder="Leave blank to keep existing"></label>
        <div class="actions">
          <button id="test-jellyfin" class="secondary">Test</button>
          <button id="test-jellyfin-random-song" class="secondary">Play random song</button>
        </div>
        <div id="jellyfin-song-test" class="song-test hidden"></div>
      </section>
      <section class="service-box">
        <h2>Plex</h2>
        <label>Base URL <input id="plex-url" placeholder="http://192.168.1.20:32400"></label>
        <div class="field-links">
          <a id="plex-url-link" class="field-link" href="" target="_blank" rel="noopener noreferrer" aria-disabled="true">Open Plex</a>
          <a id="plex-token-link" class="field-link" href="" target="_blank" rel="noopener noreferrer" aria-disabled="true">Get Plex token</a>
        </div>
        <label>Token <input id="plex-token" type="password" autocomplete="off" placeholder="Leave blank to keep existing"></label>
        <div class="actions"><button id="test-plex" class="secondary">Test</button></div>
      </section>
    </div>
    <section>
      <h2>Status</h2>
      <div id="status">Loading...</div>
      <div class="actions">
        <button id="save">Save settings</button>
        <button id="refresh" class="secondary">Refresh</button>
      </div>
    </section>
    <footer class="site-footer">
      <span class="footer-meta">Build time: {{BUILD_TIME}}</span>
      <a class="github-link" href="https://github.com/guaka/wrapster" target="_blank" rel="noopener noreferrer" aria-label="guaka/wrapster on GitHub" title="guaka/wrapster">
        <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82A7.65 7.65 0 0 1 8 3.87c.68 0 1.36.09 2 .26 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8Z"></path></svg>
      </a>
    </footer>
  </div>
</main>
<script>
let currentPubkey = "";
const $ = (id) => document.getElementById(id);
const FIPS_NSEC_STORAGE_KEY = "wrapster-setup-fips-nsec-v1";
const FIPS_PEER_STORAGE_KEY = "wrapster-setup-fips-peer-v1";
const JELLYFIN_KEY_STORAGE_KEY = "wrapster-setup-jellyfin-api-key-v1";
const PLEX_TOKEN_STORAGE_KEY = "wrapster-setup-plex-token-v1";
function isValidHexPubkey(value) {
  return /^[0-9a-fA-F]{64}$/.test(String(value || ""));
}
function setSetupVisible(visible) {
  const content = $("setup-content");
  if (content) {
    content.classList.toggle("hidden", !visible);
  }
}
function b64(json) {
  const bytes = new TextEncoder().encode(json);
  let text = "";
  bytes.forEach((b) => text += String.fromCharCode(b));
  return btoa(text);
}
const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l";
const jellyfinDefaultPort = 8096;
const plexDefaultPort = 32400;
const jellyfinTokenHelpPath = "/web/#/dashboard/keys";
const plexTokenHelpPath = "/web/index.html";
function defaultJellyfinURL() {
  return "http://" + location.hostname + ":" + jellyfinDefaultPort;
}
function defaultPlexURL() {
  return "http://" + location.hostname + ":" + plexDefaultPort;
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
function saveCachedFIPSNsec(nsec, npub) {
  try {
    window.localStorage.setItem(FIPS_NSEC_STORAGE_KEY, JSON.stringify({
      nsec: String(nsec || ""),
      npub: String(npub || ""),
      savedAt: new Date().toISOString()
    }));
  } catch {
    // localStorage is optional: ignore cache failures.
  }
}
function getCachedFIPSPeer() {
  try {
    const raw = window.localStorage.getItem(FIPS_PEER_STORAGE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object") return {};
    const peerNpub = String(parsed.peer_npub || "").trim();
    const peerAddr = String(parsed.peer_addr || "").trim();
    return { peerNpub, peerAddr };
  } catch {
    return {};
  }
}
function getCachedJellyfinAPIKey() {
  try {
    const raw = window.localStorage.getItem(JELLYFIN_KEY_STORAGE_KEY);
    if (!raw) return "";
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object") return "";
    return String(parsed.key || "").trim();
  } catch {
    return "";
  }
}
function getCachedPlexToken() {
  try {
    const raw = window.localStorage.getItem(PLEX_TOKEN_STORAGE_KEY);
    if (!raw) return "";
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object") return "";
    return String(parsed.token || "").trim();
  } catch {
    return "";
  }
}
function saveCachedJellyfinAPIKey(value) {
  try {
    const key = String(value || "").trim();
    if (!key) {
      window.localStorage.removeItem(JELLYFIN_KEY_STORAGE_KEY);
      return;
    }
    window.localStorage.setItem(JELLYFIN_KEY_STORAGE_KEY, JSON.stringify({
      key,
      updated_at: new Date().toISOString()
    }));
  } catch {
    // localStorage is optional: ignore cache failures.
  }
}
function saveCachedPlexToken(value) {
  try {
    const token = String(value || "").trim();
    if (!token) {
      window.localStorage.removeItem(PLEX_TOKEN_STORAGE_KEY);
      return;
    }
    window.localStorage.setItem(PLEX_TOKEN_STORAGE_KEY, JSON.stringify({
      token,
      updated_at: new Date().toISOString()
    }));
  } catch {
    // localStorage is optional: ignore cache failures.
  }
}
function saveCachedFIPSPeer(peerNpub, peerAddr) {
  try {
    window.localStorage.setItem(FIPS_PEER_STORAGE_KEY, JSON.stringify({
      peer_npub: String(peerNpub || "").trim(),
      peer_addr: String(peerAddr || "").trim(),
      updated_at: new Date().toISOString()
    }));
  } catch {
    // localStorage is optional: ignore cache failures.
  }
}
function hydrateFIPSState() {
  const cachedNsec = getCachedFIPSNsec();
  const cachedPeer = getCachedFIPSPeer();
  if (!$("fips-npub").value && cachedNsec.npub) {
    $("fips-npub").value = cachedNsec.npub;
  }
  if (cachedNsec.npub && cachedNsec.npub === $("fips-npub").value && cachedNsec.nsec) {
    $("fips-nsec").value = cachedNsec.nsec;
    $("fips-secret-row").classList.remove("hidden");
    $("fips-nsec").type = "password";
  } else {
    $("fips-secret-row").classList.add("hidden");
  }
  if (!$("fips-peer-npub").value && cachedPeer.peerNpub) {
    $("fips-peer-npub").value = cachedPeer.peerNpub;
  }
  if (!$("fips-peer-addr").value && cachedPeer.peerAddr) {
    $("fips-peer-addr").value = cachedPeer.peerAddr;
  }
}
function persistFIPSPeerInputs() {
  saveCachedFIPSPeer($("fips-peer-npub").value, $("fips-peer-addr").value);
}
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
function hexToBytes(value) {
  if (value.length % 2 !== 0) return [];
  const out = [];
  for (let i = 0; i < value.length; i += 2) {
    const byte = Number.parseInt(value.slice(i, i + 2), 16);
    if (!Number.isInteger(byte) || byte < 0 || byte > 255) return [];
    out.push(byte);
  }
  return out;
}
function toNpub(publicKey) {
  const trimmed = String(publicKey || "").toLowerCase();
  if (!/^[0-9a-f]{64}$/.test(trimmed)) return "";
  const bytes = hexToBytes(trimmed);
  if (!bytes.length) return "";
  const data = convertBits(bytes, 8, 5, true);
  return bech32Encode("npub", data);
}
function setConnectStatus(message, kind = "neutral") {
  const node = $("connect-status");
  if (!node) return;
  if (!message) {
    node.textContent = "";
    node.className = "connect-status hidden";
    return;
  }
  node.className = "connect-status " + (kind || "neutral");
  node.textContent = message;
}
function renderConnectButton() {
  const connectButton = $("connect");
  const statusNode = $("identity");
  if (!connectButton || !statusNode) return;
  if (!currentPubkey) {
    connectButton.classList.remove("connected");
    connectButton.disabled = false;
    connectButton.textContent = "Connect";
    connectButton.title = "";
    return;
  }
  const npub = toNpub(currentPubkey);
  const display = npub ? "Connected: " + npub : "Connected";
  const dot = document.createElement("span");
  const label = document.createElement("span");
  dot.className = "connect-dot ok";
  label.className = "connect-label";
  label.textContent = display;
  connectButton.classList.add("connected");
  connectButton.disabled = false;
  connectButton.title = display;
  connectButton.replaceChildren(dot, label);
  statusNode.title = display;
}
function updateIdentityStatus() {
  const idNode = $("identity");
  if (!currentPubkey) {
    idNode.textContent = "NIP-07 not connected";
    setConnectStatus("NIP-07 not connected", "bad");
    renderConnectButton();
    return;
  }
  const npub = toNpub(currentPubkey);
  const display = npub ? "Connected " + npub : "Connected " + currentPubkey;
  idNode.textContent = display;
  setConnectStatus("", "ok");
  renderConnectButton();
}
async function connect() {
  const connectButton = $("connect");
  if (!window.nostr) {
    currentPubkey = "";
    renderConnectButton();
    setConnectStatus("NIP-07 extension not found", "bad");
    throw new Error("NIP-07 extension not found");
  }
  connectButton.classList.remove("connected");
  connectButton.disabled = true;
  connectButton.textContent = "Connecting...";
  setConnectStatus("Connecting to NIP-07...", "neutral");
  const pubkey = await window.nostr.getPublicKey();
  if (!isValidHexPubkey(pubkey)) {
    currentPubkey = "";
    renderConnectButton();
    setConnectStatus("Invalid NIP-07 public key", "bad");
    throw new Error("NIP-07 returned an invalid public key");
  }
  currentPubkey = pubkey;
  updateIdentityStatus();
  setSetupVisible(true);
  setConnectStatus("", "ok");
  renderConnectButton();
}
function hasNIP07() {
  return Boolean(window.nostr && typeof window.nostr.getPublicKey === "function");
}
async function autoConnect() {
  const connectButton = $("connect");
  setSetupVisible(false);
  connectButton.textContent = "Checking NIP-07...";
  connectButton.classList.remove("connected");
  connectButton.disabled = true;
  setConnectStatus("Checking NIP-07...", "neutral");
  try {
    if (hasNIP07() || await waitForNostr()) {
      await connect();
      await load();
      return;
    }
    if (currentPubkey) {
      updateIdentityStatus();
      await load();
    } else {
      $("identity").textContent = "NIP-07 extension not detected";
      setConnectStatus("NIP-07 extension not detected", "bad");
      renderConnectButton();
      throw new Error("NIP-07 extension not detected");
    }
  } catch (err) {
    if (err) {
      $("identity").textContent = "NIP-07 not connected";
      renderConnectButton();
      setConnectStatus(String(err.message || err), "bad");
      $("status").textContent = String(err.message || err);
    }
  } finally {
    connectButton.disabled = false;
  }
}
async function waitForNostr() {
  for (let attempt = 0; attempt < 20; attempt++) {
    await new Promise((resolve) => setTimeout(resolve, 100));
    if (hasNIP07()) return true;
  }
  return false;
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
function serviceURL(value) {
  const trimmed = String(value || "").trim();
  if (!trimmed) return "";
  const withScheme = /^[a-z]+:\/\//i.test(trimmed) ? trimmed : "http://" + trimmed;
  try {
    const parsed = new URL(withScheme);
    return parsed.origin + parsed.pathname.replace(/\/+$/, "");
  } catch {
    return "";
  }
}
function serviceLink(id, href, canUse) {
  const link = $(id);
  if (!canUse || !href) {
    link.removeAttribute("href");
    link.setAttribute("aria-disabled", "true");
    return;
  }
  link.setAttribute("href", href);
  link.setAttribute("aria-disabled", "false");
}
function updateServiceLinks() {
  const jellyfinBase = serviceURL($("jellyfin-url").value);
  serviceLink("jellyfin-url-link", jellyfinBase, Boolean(jellyfinBase));
  serviceLink(
    "jellyfin-token-link",
    jellyfinBase ? jellyfinBase + jellyfinTokenHelpPath : "",
    Boolean(jellyfinBase)
  );
  const plexBase = serviceURL($("plex-url").value);
  serviceLink("plex-url-link", plexBase, Boolean(plexBase));
  serviceLink(
    "plex-token-link",
    plexBase ? plexBase + plexTokenHelpPath : "",
    Boolean(plexBase)
  );
}
{{ADMIN_COMMON_JS}}
function serviceStatusText(baseURL, tokenConfigured) {
  if (!baseURL) return "Not configured";
  return tokenConfigured ? (baseURL + " (token set)") : (baseURL + " (token missing)");
}
function renderStatus(data) {
  const root = $("status");
  root.textContent = "";
  if (!data || typeof data !== "object") {
    root.appendChild(statusLine("Status API", "Unavailable", false));
    return;
  }
  root.appendChild(statusLine("Admin auth", data.admin_auth ? "Enabled" : "Disabled", Boolean(data.admin_auth)));
  root.appendChild(statusLine("Config path", data.config_path ? "Configured" : "Missing", Boolean(data.config_path)));
  const jellyfin = data.services?.jellyfin || {};
  const plex = data.services?.plex || {};
  const peer = data.fips_peer || {};
  root.appendChild(statusLine(
    "Jellyfin",
    serviceStatusText(jellyfin.base_url || "", Boolean(jellyfin.token_configured)),
    Boolean(jellyfin.configured)
  ));
  root.appendChild(statusLine(
    "Plex",
    serviceStatusText(plex.base_url || "", Boolean(plex.token_configured)),
    Boolean(plex.configured)
  ));
  const fips = data.fips || {};
  root.appendChild(statusLine(
    "FIPS identity",
    fips.npub || "Not configured",
    Boolean(fips.configured)
  ));
  const peers = data.fips_peers || [];
  renderFipsPeerList(peers);
}

function renderFipsPeerList(peers) {
  const root = $("fips-peers");
  if (!root) return;
  root.textContent = "";
  const list = Array.isArray(peers) ? peers : [];
  if (!list.length) {
    root.appendChild(statusLine("FIPS peers", "No peers configured", false));
    return;
  }
  for (let i = 0; i < list.length; i++) {
    const peer = list[i] || {};
    const npub = String(peer.npub || "").trim();
    const addr = String(peer.addr || "").trim();
    const check = peer.check || {};
    const identity = npub || "Not configured";
    const location = addr || "no address";
    const summary = peerStatusFromCheck(check);
    const statusText = identity + " — " + location + " • " + summary.text.replace(/^FIPS peer: /, "");
    root.appendChild(statusLine("Peer " + (i + 1), statusText, summary.state === "ok" ? true : summary.state === "neutral" ? "neutral" : false));
  }
}

function renderFipsPeerCheckStatus(check) {
  const node = $("fips-peer-check-result");
  if (!node) return;
  const peerCheck = check || {};
  const summary = peerStatusFromCheck(peerCheck);
  setHeaderFipsStatus(summary.state, summary.text);
  let ok = false;
  let state = "bad";
  let value = "Not set";
  if (peerCheck.transport_check_skipped || peerCheck.peer_addr_set === false) {
    ok = true;
    state = "neutral";
    const npub = peerCheck.peer_npub || "configured peer";
    value = "Identity accepted for " + npub + "; add the public peer address to test outbound transport";
  } else if (peerCheck.error) {
    value = String(peerCheck.error) + (peerCheck.transport ? " (" + peerCheck.transport + ")" : "");
  } else if (peerCheck.reachable) {
    ok = true;
    state = "ok";
    value = "Reachable via " + (peerCheck.transport || "tcp");
  } else if (peerCheck.peer_addr || peerCheck.peer_npub) {
    value = "Not reachable";
  }
  node.classList.remove("hidden");
  node.textContent = "";
  node.appendChild(statusLine("FIPS peer connectivity", value, state));
}

function setHeaderFipsStatus(state, text) {
  const node = $("header-fips-status");
  if (!node) return;
  node.classList.remove("ok", "bad", "neutral");
  node.classList.add(state || "neutral");
  node.textContent = text || "FIPS peer: not checked";
}
async function load() {
  if (!currentPubkey) return;
  const [cfg, status] = await Promise.all([
    fetch("/setup/api/config").then(r => r.json()),
    fetch("/setup/api/status").then(r => r.json())
  ]);
  $("jellyfin-url").value = cfg.jellyfin?.base_url || defaultJellyfinURL();
  $("plex-url").value = cfg.plex?.base_url || defaultPlexURL();
  const cachedJellyfinKey = getCachedJellyfinAPIKey();
  const cachedPlexToken = getCachedPlexToken();
  if (cfg.jellyfin && cfg.jellyfin.token_configured === false && cachedJellyfinKey && !$("jellyfin-key").value) {
    $("jellyfin-key").value = cachedJellyfinKey;
  }
  if (cfg.plex && cfg.plex.token_configured === false && cachedPlexToken && !$("plex-token").value) {
    $("plex-token").value = cachedPlexToken;
  }
  const fips = status?.fips || {};
  if (fips.npub) {
    $("fips-npub").value = fips.npub;
  }
  $("fips-peer-npub").value = cfg.fips_peer_npub || "";
  $("fips-peer-addr").value = cfg.fips_peer_addr || "";
  hydrateFIPSState();
  if (cfg.fips_peer_npub || cfg.fips_peer_addr) {
    saveCachedFIPSPeer(cfg.fips_peer_npub || "", cfg.fips_peer_addr || "");
  }
  updateServiceLinks();
  renderStatus(status);
  renderFipsPeerCheckStatus(status?.fips_peer?.check);
}
function payload() {
  return {
    jellyfin_base_url: $("jellyfin-url").value,
    jellyfin_api_key: $("jellyfin-key").value,
    plex_base_url: $("plex-url").value,
    plex_token: $("plex-token").value,
    fips_peer_npub: $("fips-peer-npub").value,
    fips_peer_addr: $("fips-peer-addr").value
  };
}
async function save() {
  saveCachedFIPSPeer($("fips-peer-npub").value, $("fips-peer-addr").value);
  saveCachedJellyfinAPIKey($("jellyfin-key").value);
  saveCachedPlexToken($("plex-token").value);
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
  const body = await readResponseJSON(res);
  if (!res.ok) throw new Error(body.error || "test failed");
  await load();
}
async function testJellyfinRandomSong() {
  const root = $("jellyfin-song-test");
  renderSongTest(root, "Selecting random song...", [{name: "browser", ok: true, detail: "requesting random Jellyfin audio item"}], "");
  const res = await signedFetch("/setup/api/test/jellyfin-random-song", {
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify(payload())
  });
  const body = await readResponseJSON(res);
  if (!res.ok) {
    renderSongTest(root, body.error || "Random song test failed", body.debug || body, "");
    throw new Error(body.error || "random song test failed");
  }
  const streamURL = body.stream_url;
  if (!streamURL) {
    renderSongTest(root, "Random song selected, but no stream URL was returned", body.debug || body, "");
    throw new Error("random song stream URL missing");
  }
  const streamRes = await signedFetch(streamURL, {
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify(payload())
  });
  if (!streamRes.ok) {
    const text = await streamRes.text();
    const debug = (body.debug || []).concat([{name: "stream", ok: false, detail: streamRes.status + " " + streamRes.statusText, error: text}]);
    renderSongTest(root, body.item?.name || "Random song stream failed", debug, "");
    throw new Error(text || "random song stream failed");
  }
  const blob = await streamRes.blob();
  const audioURL = URL.createObjectURL(blob);
  const debug = (body.debug || []).concat([{name: "stream", ok: true, detail: streamRes.status + " " + streamRes.statusText + ", " + blob.size + " bytes"}]);
  renderSongTest(root, body.item?.name || "Random Jellyfin song", debug, audioURL);
  $("status").textContent = "Random Jellyfin song is playing.";
}
function statusLineFromPeerCheck(label, peerCheck) {
  if (!peerCheck) {
    return statusLine(label, "No response", false);
  }
  if (peerCheck.error) {
    const transport = peerCheck.transport ? " (" + peerCheck.transport + ")" : "";
    return statusLine(label, String(peerCheck.error) + transport, false);
  }
  if (peerCheck.transport_check_skipped || peerCheck.peer_addr_set === false) {
    return statusLine(label, "Identity accepted; add public address to test outbound transport", "neutral");
  }
  if (peerCheck.reachable) {
    return statusLine(label, "Outbound dial works via " + (peerCheck.transport || "tcp"), true);
  }
  if (peerCheck.peer_addr || peerCheck.peer_npub) {
    return statusLine(label, "Not reachable", false);
  }
  return statusLine(label, "Not set", false);
}
async function readResponseJSON(res) {
  const text = await res.text();
  if (!text) return {};
  try {
    return JSON.parse(text);
  } catch {
    return {error: "invalid JSON response"};
  }
}
async function generateFipsNsec() {
  if (!window.crypto || typeof window.crypto.getRandomValues !== "function") throw new Error("Secure random generator unavailable");
  const bytes = new Uint8Array(32);
  window.crypto.getRandomValues(bytes);
  const nsec = bech32Encode("nsec", convertBits(Array.from(bytes), 8, 5, true));
  $("fips-nsec").value = nsec;
  $("fips-nsec").type = "password";
  $("fips-secret-row").classList.remove("hidden");
  $("fips-npub").value = "";
  $("status").textContent = "Saving FIPS identity...";
  const res = await signedFetch("/setup/api/fips-nsec", {
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify({nsec})
  });
  const body = await res.json();
  if (!res.ok) throw new Error(body.error || "FIPS identity save failed");
  $("fips-npub").value = body.npub || "";
  saveCachedFIPSNsec($("fips-nsec").value, $("fips-npub").value);
  $("status").textContent = "Saved. FIPS will start automatically.";
}
async function testFipsPeer() {
  saveCachedFIPSPeer($("fips-peer-npub").value, $("fips-peer-addr").value);
  const resultNode = $("fips-peer-check-result");
  if (resultNode) {
    resultNode.classList.remove("hidden");
    resultNode.textContent = "";
    resultNode.appendChild(statusLineFromPeerCheck("FIPS peer connectivity", {peer_npub: $("fips-peer-npub").value, peer_addr: $("fips-peer-addr").value, error: "Checking..."}));
  }
  const res = await signedFetch("/setup/api/fips-peer-check", {
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify({
      fips_peer_npub: $("fips-peer-npub").value,
      fips_peer_addr: $("fips-peer-addr").value
    })
  });
  const body = await readResponseJSON(res);
  saveCachedFIPSPeer($("fips-peer-npub").value, $("fips-peer-addr").value);
  const check = body.check || {};
  renderFipsPeerCheckStatus(check);
  if (!res.ok) {
    const error = typeof body.error === "string" ? body.error : "FIPS peer test failed";
    throw new Error(error);
  }
  if (!check || !check.reachable) {
    throw new Error("FIPS peer outbound transport is not reachable");
  }
  $("status").textContent = "FIPS peer outbound transport is reachable";
}
async function copyFipsNpub() {
  const value = $("fips-npub").value;
  if (!value) throw new Error("Generate an identity first");
  await copyText($("fips-npub"), "Copied npub.");
}
async function copyFipsNsec() {
  const value = $("fips-nsec").value;
  if (!value) throw new Error("Generate an identity first");
  await copyText($("fips-nsec"), "Copied secret.");
}
function toggleFipsNsec() {
  const input = $("fips-nsec");
  const revealing = input.type === "password";
  input.type = revealing ? "text" : "password";
  $("reveal-fips-nsec").title = revealing ? "Hide nsec" : "Reveal nsec";
  $("reveal-fips-nsec").setAttribute("aria-label", $("reveal-fips-nsec").title);
}
async function copyText(input, successMessage) {
  try { await navigator.clipboard.writeText(input.value); }
  catch {
    input.select();
    document.execCommand("copy");
  }
  $("status").textContent = successMessage;
}
function run(button, fn) {
  return async () => {
    button.disabled = true;
    try { await fn(); }
    catch (err) { $("status").textContent = String(err.message || err); }
    finally { button.disabled = false; }
  };
}
$("connect").onclick = run($("connect"), async () => {
  await connect();
  await load();
});
$("refresh").onclick = run($("refresh"), load);
$("save").onclick = run($("save"), save);
$("test-jellyfin").onclick = run($("test-jellyfin"), () => test("jellyfin"));
$("test-jellyfin-random-song").onclick = run($("test-jellyfin-random-song"), testJellyfinRandomSong);
$("test-plex").onclick = run($("test-plex"), () => test("plex"));
$("test-fips-peer").onclick = run($("test-fips-peer"), testFipsPeer);
$("jellyfin-url").addEventListener("input", updateServiceLinks);
$("plex-url").addEventListener("input", updateServiceLinks);
$("jellyfin-key").addEventListener("input", () => {
  saveCachedJellyfinAPIKey($("jellyfin-key").value);
});
$("plex-token").addEventListener("input", () => {
  saveCachedPlexToken($("plex-token").value);
});
$("generate-fips-nsec").onclick = run($("generate-fips-nsec"), generateFipsNsec);
$("copy-fips-npub").onclick = run($("copy-fips-npub"), copyFipsNpub);
$("copy-fips-nsec").onclick = run($("copy-fips-nsec"), copyFipsNsec);
$("reveal-fips-nsec").onclick = run($("reveal-fips-nsec"), toggleFipsNsec);
$("fips-peer-npub").addEventListener("input", persistFIPSPeerInputs);
$("fips-peer-addr").addEventListener("input", persistFIPSPeerInputs);
window.addEventListener("load", autoConnect);
</script>
</body>
</html>`

const setupFaviconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">
<rect width="64" height="64" rx="14" fill="#651515"/>
<path d="M15 28c4-8 10-12 17-12s13 4 17 12" fill="none" stroke="#ff7f7f" stroke-width="5" stroke-linecap="round"/>
<path d="M20 37c3-5 7-8 12-8s9 3 12 8" fill="none" stroke="#ffd8a0" stroke-width="5" stroke-linecap="round"/>
<path d="M32 17v31" fill="none" stroke="#ff7f7f" stroke-width="5" stroke-linecap="round"/>
<path d="M20 49c5 0 9-3 12-9 3 6 7 9 12 9" fill="none" stroke="#ff7f7f" stroke-width="5" stroke-linecap="round" stroke-linejoin="round"/>
<circle cx="32" cy="28" r="4" fill="#ffd8a0"/>
</svg>`
