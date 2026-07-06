package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/trustroots/nostroots/vibe/wrapster/internal/access"
	"github.com/trustroots/nostroots/vibe/wrapster/internal/httpx"
)

type Gateway struct {
	ConnectorBaseURL   string
	ConnectorToken     string
	TransportLabel     string
	Auth               Authorizer
	Access             access.Authorizer
	ServiceAccessRules map[string][]string
	HTTPClient         *http.Client
}

func (g Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !httpx.RequireMethod(w, r, http.MethodGet) {
		return
	}
	switch {
	case r.URL.Path == "/media/api/status":
		pubkey, err := g.authorizeStatus(r)
		if err != nil {
			writeJSON(w, mediaAuthStatus(err), map[string]string{"error": err.Error()})
			return
		}
		if strings.TrimSpace(g.ConnectorBaseURL) == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "media connector is not configured"})
			return
		}
		g.proxyJSON(w, r, pubkey, "/connector/api/status", nil)
	case strings.HasPrefix(r.URL.Path, "/media/api/services/"):
		g.serviceRoute(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (g Gateway) serviceRoute(w http.ResponseWriter, r *http.Request) {
	route, ok := parseServiceRoute(r.URL.Path, "/media/api/services/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	pubkey, err := g.authorizeService(r, route.Service)
	if err != nil {
		writeJSON(w, mediaAuthStatus(err), map[string]string{"error": err.Error()})
		return
	}
	if strings.TrimSpace(g.ConnectorBaseURL) == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "media connector is not configured"})
		return
	}

	switch route.Action {
	case "random-song":
		g.proxyRandomSong(w, r, route.Service)
	case "search":
		g.proxyJSON(w, r, pubkey, "/connector/api/services/"+route.Service+"/search", r.URL.Query())
	case "stream":
		g.proxyStream(w, r, "/connector/api/services/"+route.Service+"/stream/"+url.PathEscape(route.StreamID))
	default:
		http.NotFound(w, r)
	}
}

func (g Gateway) proxyRandomSong(w http.ResponseWriter, r *http.Request, service string) {
	resp, err := g.connectorRequest(r, "/connector/api/services/"+service+"/random-song", nil)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "connector returned invalid JSON"})
		return
	}
	if resp.StatusCode == http.StatusOK {
		if item, ok := body["item"].(map[string]any); ok {
			if streamID, ok := item["stream_id"].(string); ok && streamID != "" {
				body["stream_url"] = "/media/api/services/" + service + "/stream/" + url.PathEscape(streamID)
			}
		}
	}
	writeJSON(w, resp.StatusCode, body)
}

func (g Gateway) authorizeStatus(r *http.Request) (string, error) {
	if pubkey, err := g.Auth.VerifyRequest(r); err == nil {
		return pubkey, nil
	}
	serviceRules := requiredServiceRuleSets(g.ServiceAccessRules)
	if len(serviceRules) == 0 {
		return g.Auth.VerifyRequest(r)
	}
	var lastErr error
	for _, rules := range serviceRules {
		if pubkey, err := g.Access.VerifyAllRequest(r, rules); err == nil {
			return pubkey, nil
		} else {
			lastErr = err
		}
	}
	return "", lastErr
}

func (g Gateway) authorizeService(r *http.Request, service string) (string, error) {
	if pubkey, err := g.Auth.VerifyRequest(r); err == nil {
		return pubkey, nil
	}
	if rules := cleanRuleNames(g.ServiceAccessRules[service]); len(rules) > 0 {
		return g.Access.VerifyAllRequest(r, rules)
	}
	return g.Auth.VerifyRequest(r)
}

func requiredServiceRuleSets(serviceRules map[string][]string) [][]string {
	seen := map[string]struct{}{}
	ruleSets := [][]string{}
	for _, rules := range serviceRules {
		rules = cleanRuleNames(rules)
		if len(rules) == 0 {
			continue
		}
		key := strings.Join(rules, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		ruleSets = append(ruleSets, rules)
	}
	return ruleSets
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

func mediaAuthStatus(err error) int {
	if status := access.HTTPStatus(err); status == http.StatusUnauthorized {
		return status
	}
	if errors.Is(err, ErrNotGranted) || errors.Is(err, ErrNoGrantPubkeys) {
		return http.StatusForbidden
	}
	return http.StatusForbidden
}

func (g Gateway) proxyJSON(w http.ResponseWriter, r *http.Request, pubkey, connectorPath string, query url.Values) {
	resp, err := g.connectorRequest(r, connectorPath, query)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	if connectorPath == "/connector/api/status" && resp.StatusCode == http.StatusOK {
		var connector map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&connector); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "connector returned invalid JSON"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"authenticated_pubkey": pubkey,
			"transport":            g.transportLabel(),
			"grants": map[string]any{
				"configured_count": len(g.Auth.Grants),
			},
			"connector": connector,
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (g Gateway) proxyStream(w http.ResponseWriter, r *http.Request, connectorPath string) {
	resp, err := g.connectorRequest(r, connectorPath, nil)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	httpx.CopyStreamHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (g Gateway) connectorRequest(r *http.Request, connectorPath string, query url.Values) (*http.Response, error) {
	return g.connectorRequestContext(r.Context(), connectorPath, query, r.Header.Get("Range"))
}

func (g Gateway) ConnectorStatus(ctx context.Context) map[string]any {
	checkedAt := time.Now().UTC().Format(time.RFC3339)
	if strings.TrimSpace(g.ConnectorBaseURL) == "" {
		return map[string]any{
			"configured":  false,
			"reachable":   false,
			"latency_ms":  nil,
			"checked_at":  checkedAt,
			"transport":   g.transportLabel(),
			"last_error":  "media connector is not configured",
			"status_code": nil,
		}
	}

	start := time.Now()
	resp, err := g.connectorRequestContext(ctx, "/connector/api/status", nil, "")
	if err != nil {
		return map[string]any{
			"configured":  true,
			"reachable":   false,
			"latency_ms":  nil,
			"checked_at":  checkedAt,
			"transport":   g.transportLabel(),
			"last_error":  err.Error(),
			"status_code": nil,
		}
	}
	defer resp.Body.Close()

	payload := map[string]any{}
	lastError := ""
	reachable := resp.StatusCode >= 200 && resp.StatusCode < 300
	if reachable {
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			reachable = false
			lastError = "connector returned invalid JSON"
		}
	} else {
		lastError = resp.Status
	}

	out := map[string]any{
		"configured":  true,
		"reachable":   reachable,
		"latency_ms":  time.Since(start).Milliseconds(),
		"checked_at":  checkedAt,
		"transport":   g.transportLabel(),
		"last_error":  lastError,
		"status_code": resp.StatusCode,
	}
	if len(payload) > 0 {
		out["connector"] = payload
	}
	return out
}

func (g Gateway) connectorRequestContext(ctx context.Context, connectorPath string, query url.Values, rangeHeader string) (*http.Response, error) {
	base, err := url.Parse(strings.TrimRight(g.ConnectorBaseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("media connector URL is invalid: %w", err)
	}
	base.Path = path.Join(base.Path, connectorPath)
	base.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return nil, err
	}
	if token := strings.TrimSpace(g.ConnectorToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if value := strings.TrimSpace(rangeHeader); value != "" {
		req.Header.Set("Range", value)
	}
	client := g.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return client.Do(req)
}

func (g Gateway) transportLabel() string {
	if label := strings.TrimSpace(g.TransportLabel); label != "" {
		return label
	}
	return "private"
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	httpx.WriteJSON(w, status, body)
}
