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
	case r.URL.Path == adminauth.AdminRoute || r.URL.Path == adminauth.AdminRouteSlash:
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		html := adminui.InjectShared(setupHTML)
		html = strings.ReplaceAll(html, "{{WRAPSTER_NAS_NAME}}", adminauth.WrapsterNASName)
		html = strings.ReplaceAll(html, "{{BUILD_TIME}}", buildinfo.DisplayBuildTime())
		_, _ = w.Write([]byte(html))
	case r.URL.Path == adminauth.SetupRoute || r.URL.Path == adminauth.SetupRouteSlash:
		http.Redirect(w, r, adminauth.AdminRoute, http.StatusFound)
		return
	case r.URL.Path == adminauth.SetupAPIFaviconSVG || r.URL.Path == adminauth.SetupAPIFaviconICO:
		h.favicon(w, r)
	case r.URL.Path == adminauth.SetupAPIStatus:
		h.status(w, r)
	case r.URL.Path == adminauth.SetupAPIConfig:
		h.config(w, r)
	case r.URL.Path == adminauth.SetupAPIFipsNsec:
		h.fipsNsec(w, r)
	case r.URL.Path == adminauth.SetupAPIFipsPeerCheck:
		h.fipsPeerCheck(w, r)
	case r.URL.Path == adminauth.SetupAPITestJellyfin:
		h.test(w, r, "jellyfin")
	case r.URL.Path == adminauth.SetupAPITestJellyfinRandomSong:
		h.testJellyfinRandomSong(w, r)
	case strings.HasPrefix(r.URL.Path, adminauth.SetupAPITestJellyfinRandomSongStream):
		h.streamJellyfinRandomSong(w, r)
	case r.URL.Path == adminauth.SetupAPITestPlex:
		h.test(w, r, "plex")
	case r.URL.Path == adminauth.SetupAPITestPlexRandomSong:
		h.testPlexRandomSong(w, r)
	case strings.HasPrefix(r.URL.Path, adminauth.SetupAPITestPlexRandomSongStream):
		h.streamPlexRandomSong(w, r)
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

	requests := []func() (*http.Request, error){
		func() (*http.Request, error) {
			return h.connector().jellyfinBrowserAudioRequestWithConfig(r, cfg, streamID)
		},
		func() (*http.Request, error) { return h.connector().jellyfinStreamRequestWithConfig(r, cfg, streamID) },
	}

	var finalResp *http.Response
	var finalErr error
	finalStatus := http.StatusBadGateway
	for _, build := range requests {
		req, buildErr := build()
		if buildErr != nil {
			if errors.Is(buildErr, errServiceNotConfigured) {
				status := http.StatusServiceUnavailable
				writeJSON(w, status, map[string]string{"error": buildErr.Error()})
				return
			}
			finalErr = buildErr
			finalStatus = http.StatusBadRequest
			continue
		}
		resp, doErr := h.connector().client().Do(req)
		if doErr != nil {
			finalErr = doErr
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			finalResp = resp
			break
		}
		finalErr = fmt.Errorf("stream request failed: %s", resp.Status)
		resp.Body.Close()
	}

	if finalResp == nil {
		if finalErr != nil {
			writeJSON(w, finalStatus, map[string]string{"error": finalErr.Error()})
			return
		}
		writeJSON(w, finalStatus, map[string]string{"error": "stream request failed"})
		return
	}
	defer finalResp.Body.Close()
	for _, name := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if value := finalResp.Header.Get(name); value != "" {
			w.Header().Set(name, value)
		}
	}
	w.WriteHeader(finalResp.StatusCode)
	_, _ = io.Copy(w, finalResp.Body)
}

func (h SetupHandler) testPlexRandomSong(w http.ResponseWriter, r *http.Request) {
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
	result, err := h.connector().RandomPlexSong(ctx, cfg)
	if result.Item.StreamID != "" {
		result.StreamURL = "/setup/api/test/plex-random-song/stream/" + url.PathEscape(result.Item.StreamID)
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

func (h SetupHandler) streamPlexRandomSong(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, err := h.Auth.VerifyRequest(r); err != nil {
		writeJSON(w, setupAuthStatus(err), map[string]string{"error": err.Error()})
		return
	}
	streamID := strings.TrimPrefix(r.URL.Path, "/setup/api/test/plex-random-song/stream/")
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

	req, err := h.connector().plexStreamRequestWithConfig(r, cfg, streamID)
	if errors.Is(err, errServiceNotConfigured) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}

	resp, err := h.connector().client().Do(req)
	if err != nil {
		http.Error(w, "failed to start Plex stream", http.StatusBadGateway)
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
	check := fips.CheckPeerConnectivityWithDebug(payload.FIPSPeerNpub, payload.FIPSPeerAddr)
	reachable, _ := check["reachable"].(bool)
	peerAddrSet, _ := check["peer_addr_set"].(bool)
	peerNpubOK, _ := check["peer_npub_ok"].(bool)
	if errorText, shouldReject := fips.ConnectivityError(check); shouldReject {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": errorText,
			"check": check,
		})
		return
	}
	if !peerNpubOK || (!reachable && peerAddrSet) {
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
	peerCheck := fips.CheckPeerConnectivityWithDebug(cfg.FIPSPeerNpub, cfg.FIPSPeerAddr)
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
    <title>{{WRAPSTER_NAS_NAME}}</title>
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
    #status {
      border: 1px solid var(--line);
      border-radius: 8px;
      background: rgba(5, 8, 13, .62);
      padding: 12px;
    }
  </style>
  <script>
    {{ADMIN_COMMON_JS}}
  </script>
</head>
<body>
<main>
  <header>
    <div class="brand-block">
      <h1>{{WRAPSTER_NAS_NAME}}</h1>
      <div class="status identity-line" id="identity">NIP-07 not connected</div>
    </div>
    <div class="header-status">
      <div class="toolbar">
        <button class="connect-button" id="connect">Connect</button>
        <div id="connect-status" class="connect-status hidden"></div>
      </div>
      <div id="header-fips-status" class="fips-header-status neutral">FIPS peer: checking status...</div>
      <div id="status" class="status">Loading...</div>
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
      <h2>Upstream FIPS/Relay</h2>
      <label>Upstream host
        <input id="fips-peer-upstream" placeholder="upstream.example.org">
      </label>
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
        <div class="actions">
          <button id="test-plex-random-song" class="secondary">Play random song</button>
        </div>
        <div id="plex-song-test" class="song-test hidden"></div>
      </section>
    </div>
    <section>
      <div class="actions">
        <button id="save">Save settings</button>
        <button id="refresh" class="secondary">Refresh</button>
      </div>
    </section>
    <footer class="site-footer">
      <span class="footer-meta">{{BUILD_TIME}}</span>
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
const FIPS_PEER_STATUS_POLL_MS = 5000;
const fipsDefaultTCPPort = "8443";
let currentFIPSPeerNpub = "";
let setupStatusPollTimer = null;
let setupLoadRequest = null;
let setupFIPSPeerCheckRequest = null;
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
function upstreamFIPSAddrFrom(value) {
  const raw = String(value || "").trim();
  if (!raw) return "";
  try {
    const parsed = new URL(/^[a-z]+:\/\//i.test(raw) ? raw : "fips://" + raw);
    if (!parsed.hostname) return "";
    return parsed.hostname + ":" + (parsed.port || fipsDefaultTCPPort);
  } catch {
    return raw;
  }
}
function upstreamFIPSDisplayFromAddr(value) {
  const addr = String(value || "").trim();
  if (!addr) return "";
  try {
    const parsed = new URL("fips://" + addr);
    if (parsed.port === fipsDefaultTCPPort) return parsed.hostname;
  } catch {
    // Keep the configured value if it is not URL-like.
  }
  return addr;
}
function upstreamFIPSAddr() {
  return upstreamFIPSAddrFrom($("fips-peer-upstream").value);
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
  if (!currentFIPSPeerNpub && cachedPeer.peerNpub) {
    currentFIPSPeerNpub = cachedPeer.peerNpub;
  }
  if (!$("fips-peer-upstream").value && cachedPeer.peerAddr) {
    $("fips-peer-upstream").value = upstreamFIPSDisplayFromAddr(cachedPeer.peerAddr);
  }
}
function persistFIPSPeerInputs() {
  saveCachedFIPSPeer(currentFIPSPeerNpub, upstreamFIPSAddr());
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
  const display = npub || "Connected";
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
  const display = npub ? npub : currentPubkey;
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
  startSetupStatusPolling();
  renderConnectButton();
}
function startSetupStatusPolling() {
  if (setupStatusPollTimer) {
    return;
  }
  setupStatusPollTimer = window.setInterval(() => {
    if (!currentPubkey) return;
    void load();
  }, FIPS_PEER_STATUS_POLL_MS);
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
async function signedJSON(path, options = {}) {
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
  const response = await fetch(path, {...options, method, headers});
  let body = {};
  try {
    body = await response.json();
  } catch {}
  return {response, body};
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
function renderStatus(data) {
  const root = $("status");
  root.textContent = "";
  if (!data || typeof data !== "object") {
    root.appendChild(statusLine("Status API", "Unavailable", false));
    return;
  }
  const fipsPeerCheck = data?.fips_peer?.check || {};
  const fipsPeerSummary = renderFIPSPeerCheckResult(root, fipsPeerCheck, {
    label: "FIPS peer connectivity",
    setHeader: setHeaderFipsStatus
  });
  const fipsPeerDebug = formatFIPSPeerDebug(fipsPeerCheck.debug_steps);
  if (fipsPeerDebug) {
    root.appendChild(statusLine(
      "FIPS peer debug",
      fipsPeerDebug,
      fipsPeerSummary ? fipsPeerSummary.state : false
    ));
  } else {
    if (!fipsPeerSummary) {
      root.appendChild(statusLine("FIPS peer connectivity", "No response", false));
    }
  }
  renderFIPSPeerList(root, data.fips_peers || []);
}
async function runSetupFIPSPeerConnectivityCheck() {
  const checkedPeer = {
    npub: String(currentFIPSPeerNpub || "").trim(),
    addr: String(upstreamFIPSAddr()).trim(),
  };
  if (!checkedPeer.npub) {
    setHeaderFipsStatus("neutral", "FIPS peer: not configured");
    return;
  }
  if (setupFIPSPeerCheckRequest) return setupFIPSPeerCheckRequest;
  const checkingText = checkedPeer.addr
    ? "Trying to establish FIPS connection to " + checkedPeer.addr + "..."
    : "Trying to establish FIPS connection with hub...";
  setHeaderFipsStatus("neutral", "FIPS peer: " + checkingText);
  setupFIPSPeerCheckRequest = (async () => {
    const {response, body} = await signedJSON("/setup/api/fips-peer-check", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({
        fips_peer_npub: checkedPeer.npub,
        ...(checkedPeer.addr ? {fips_peer_addr: checkedPeer.addr} : {})
      })
    });
    const check = body?.check || {};
    const summary = peerStatusFromCheck(check);
    if (summary.text) {
      setHeaderFipsStatus(summary.state, summary.text);
    }
    if (!response.ok && response.status !== 400) {
      setHeaderFipsStatus("bad", "FIPS peer: " + (body.error || response.statusText || "peer check failed"));
    }
  })();
  try {
    await setupFIPSPeerCheckRequest;
  } finally {
    setupFIPSPeerCheckRequest = null;
  }
}

function setHeaderFipsStatus(state, text) {
  setFIPSHeaderStatus($("header-fips-status"), state, text);
}
async function load() {
  if (!currentPubkey) return;
  if (setupLoadRequest) return setupLoadRequest;
  setupLoadRequest = (async () => {
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
  currentFIPSPeerNpub = cfg.fips_peer_npub || "";
  $("fips-peer-upstream").value = upstreamFIPSDisplayFromAddr(cfg.fips_peer_addr || "");
  hydrateFIPSState();
  if (cfg.fips_peer_npub || cfg.fips_peer_addr) {
    saveCachedFIPSPeer(cfg.fips_peer_npub || "", cfg.fips_peer_addr || "");
  }
  updateServiceLinks();
  renderStatus(status);
  await runSetupFIPSPeerConnectivityCheck();
  })();
  try {
    return await setupLoadRequest;
  } finally {
    setupLoadRequest = null;
  }
}
function payload() {
  return {
    jellyfin_base_url: $("jellyfin-url").value,
    jellyfin_api_key: $("jellyfin-key").value,
    plex_base_url: $("plex-url").value,
    plex_token: $("plex-token").value,
    fips_peer_npub: currentFIPSPeerNpub,
    fips_peer_addr: upstreamFIPSAddr()
  };
}
async function save() {
  saveCachedFIPSPeer(currentFIPSPeerNpub, upstreamFIPSAddr());
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
  await runRandomSongTest({
    root: $("jellyfin-song-test"),
    randomRequest: () => signedFetch("/setup/api/test/jellyfin-random-song", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify(payload())
    }),
    streamRequest: (streamURL) => signedFetch(streamURL, {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify(payload())
    }),
    startDetail: "requesting random audio item",
    successTitle: "Random song selected",
    requestDebugName: "setup_api",
    requestFailureTitle: "Random song test failed",
    onSuccess: () => {
      $("status").textContent = "Random Jellyfin song is playing.";
    }
  });
}
async function testPlexRandomSong() {
  await runRandomSongTest({
    root: $("plex-song-test"),
    randomRequest: () => signedFetch("/setup/api/test/plex-random-song", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify(payload())
    }),
    streamRequest: (streamURL) => signedFetch(streamURL, {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify(payload())
    }),
    startDetail: "requesting random audio item",
    successTitle: "Random song selected",
    requestDebugName: "setup_api",
    requestFailureTitle: "Random song test failed",
    onSuccess: () => {
      $("status").textContent = "Random Plex song is playing.";
    }
  });
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
$("test-jellyfin-random-song").onclick = run($("test-jellyfin-random-song"), testJellyfinRandomSong);
$("test-plex-random-song").onclick = run($("test-plex-random-song"), testPlexRandomSong);
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
$("fips-peer-upstream").addEventListener("input", persistFIPSPeerInputs);
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
