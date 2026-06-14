package media

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Connector struct {
	AllowedCIDRs    []*net.IPNet
	SharedToken     string
	JellyfinBaseURL string
	JellyfinAPIKey  string
	PlexBaseURL     string
	PlexToken       string
	FIPSPeerNpub    string
	FIPSPeerAddr    string
	HTTPClient      *http.Client
	mu              sync.RWMutex
}

type ConnectorMediaConfig struct {
	JellyfinBaseURL string `json:"jellyfin_base_url"`
	JellyfinAPIKey  string `json:"jellyfin_api_key"`
	PlexBaseURL     string `json:"plex_base_url"`
	PlexToken       string `json:"plex_token"`
	FIPSPeerNpub    string `json:"fips_peer_npub"`
	FIPSPeerAddr    string `json:"fips_peer_addr"`
}

type MediaItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Summary  string `json:"summary,omitempty"`
	StreamID string `json:"stream_id,omitempty"`
}

type RandomSongTest struct {
	Service   string           `json:"service"`
	Item      MediaItem        `json:"item,omitempty"`
	StreamURL string           `json:"stream_url,omitempty"`
	Debug     []map[string]any `json:"debug"`
}

func ParseCIDRs(values []string) ([]*net.IPNet, error) {
	out := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", value, err)
		}
		out = append(out, network)
	}
	return out, nil
}

func (c *Connector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !c.allowedRemote(r.RemoteAddr) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "remote address is not allowed"})
		return
	}
	if !c.allowedToken(r.Header.Get("Authorization")) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "connector token is required"})
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	switch {
	case r.URL.Path == "/connector/api/status":
		c.status(w)
	case strings.HasPrefix(r.URL.Path, "/connector/api/services/"):
		c.serviceRoute(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (c *Connector) SetMediaConfig(cfg ConnectorMediaConfig) {
	cfg = normalizedConnectorMediaConfig(cfg)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.JellyfinBaseURL = cfg.JellyfinBaseURL
	c.JellyfinAPIKey = cfg.JellyfinAPIKey
	c.PlexBaseURL = cfg.PlexBaseURL
	c.PlexToken = cfg.PlexToken
	c.FIPSPeerNpub = cfg.FIPSPeerNpub
	c.FIPSPeerAddr = cfg.FIPSPeerAddr
}

func (c *Connector) MediaConfig() ConnectorMediaConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return ConnectorMediaConfig{
		JellyfinBaseURL: c.JellyfinBaseURL,
		JellyfinAPIKey:  c.JellyfinAPIKey,
		PlexBaseURL:     c.PlexBaseURL,
		PlexToken:       c.PlexToken,
		FIPSPeerNpub:    c.FIPSPeerNpub,
		FIPSPeerAddr:    c.FIPSPeerAddr,
	}
}

func (c *Connector) status(w http.ResponseWriter) {
	cfg := c.MediaConfig()
	writeJSON(w, http.StatusOK, map[string]any{
		"services": map[string]any{
			"jellyfin": map[string]bool{"configured": cfg.JellyfinBaseURL != ""},
			"plex":     map[string]bool{"configured": cfg.PlexBaseURL != ""},
		},
	})
}

func (c *Connector) serviceRoute(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/connector/api/services/")
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	service, action := parts[0], parts[1]
	if !validService(service) {
		http.NotFound(w, r)
		return
	}

	switch action {
	case "random-song":
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		c.randomSong(w, r, service)
	case "search":
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		c.search(w, r, service)
	case "stream":
		if len(parts) != 3 || parts[2] == "" {
			http.NotFound(w, r)
			return
		}
		c.stream(w, r, service, parts[2])
	default:
		http.NotFound(w, r)
	}
}

func (c *Connector) randomSong(w http.ResponseWriter, r *http.Request, service string) {
	var result RandomSongTest
	var err error
	switch service {
	case "jellyfin":
		result, err = c.RandomJellyfinSong(r.Context(), c.MediaConfig())
	case "plex":
		result, err = c.RandomPlexSong(r.Context(), c.MediaConfig())
	default:
		http.NotFound(w, r)
		return
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

func (c *Connector) search(w http.ResponseWriter, r *http.Request, service string) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "q is required"})
		return
	}

	var (
		items []MediaItem
		err   error
	)
	switch service {
	case "jellyfin":
		items, err = c.searchJellyfin(r, query)
	case "plex":
		items, err = c.searchPlex(r, query)
	}
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, errServiceNotConfigured) {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"service": service,
		"items":   items,
	})
}

func (c *Connector) stream(w http.ResponseWriter, r *http.Request, service, streamID string) {
	var (
		req *http.Request
		err error
	)
	switch service {
	case "jellyfin":
		req, err = c.jellyfinStreamRequest(r, streamID)
	case "plex":
		req, err = c.plexStreamRequest(r, streamID)
	}
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errServiceNotConfigured) {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	if value := r.Header.Get("Range"); value != "" {
		req.Header.Set("Range", value)
	}

	resp, err := c.client().Do(req)
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

func (c *Connector) allowedRemote(remoteAddr string) bool {
	if len(c.AllowedCIDRs) == 0 {
		return true
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, network := range c.AllowedCIDRs {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func (c *Connector) allowedToken(header string) bool {
	token := strings.TrimSpace(c.SharedToken)
	if token == "" {
		return true
	}
	return strings.TrimSpace(header) == "Bearer "+token
}

func (c *Connector) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

var errServiceNotConfigured = errors.New("service is not configured")

type jellyfinSearchResponse struct {
	Items []struct {
		ID       string `json:"Id"`
		Name     string `json:"Name"`
		Type     string `json:"Type"`
		Overview string `json:"Overview"`
	} `json:"Items"`
}

func (c *Connector) searchJellyfin(r *http.Request, query string) ([]MediaItem, error) {
	cfg := c.MediaConfig()
	if cfg.JellyfinBaseURL == "" {
		return nil, errServiceNotConfigured
	}
	u, err := url.Parse(cfg.JellyfinBaseURL)
	if err != nil {
		return nil, err
	}
	u.Path = path.Join(u.Path, "/Items")
	q := u.Query()
	q.Set("SearchTerm", query)
	q.Set("Recursive", "true")
	q.Set("Limit", "20")
	q.Set("IncludeItemTypes", "Movie,Series,Episode,Audio,MusicAlbum,MusicArtist")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if cfg.JellyfinAPIKey != "" {
		req.Header.Set("X-Emby-Token", cfg.JellyfinAPIKey)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("jellyfin search returned %s", resp.Status)
	}
	var body jellyfinSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	items := make([]MediaItem, 0, len(body.Items))
	for _, item := range body.Items {
		items = append(items, MediaItem{
			ID:       item.ID,
			Name:     item.Name,
			Type:     item.Type,
			Summary:  item.Overview,
			StreamID: item.ID,
		})
	}
	return items, nil
}

func (c *Connector) RandomJellyfinSong(ctx context.Context, cfg ConnectorMediaConfig) (RandomSongTest, error) {
	debug := []map[string]any{}
	addStep := func(name string, started time.Time, ok bool, detail string, err error) {
		step := map[string]any{
			"name":        name,
			"ok":          ok,
			"detail":      detail,
			"duration_ms": time.Since(started).Milliseconds(),
		}
		if err != nil {
			step["error"] = err.Error()
		}
		debug = append(debug, step)
	}
	result := RandomSongTest{Service: "jellyfin", Debug: debug}
	if cfg.JellyfinBaseURL == "" {
		err := errServiceNotConfigured
		result.Debug = append(result.Debug, map[string]any{"name": "config", "ok": false, "detail": "jellyfin_base_url is not configured", "error": err.Error()})
		return result, err
	}

	started := time.Now()
	u, err := url.Parse(cfg.JellyfinBaseURL)
	addStep("parse_base_url", started, err == nil, cfg.JellyfinBaseURL, err)
	if err != nil {
		result.Debug = debug
		return result, err
	}
	u.Path = path.Join(u.Path, "/Items")
	q := u.Query()
	q.Set("Recursive", "true")
	q.Set("IncludeItemTypes", "Audio")
	q.Set("SortBy", "Random")
	q.Set("Limit", "1")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		result.Debug = debug
		return result, err
	}
	if cfg.JellyfinAPIKey != "" {
		req.Header.Set("X-Emby-Token", cfg.JellyfinAPIKey)
	}

	started = time.Now()
	resp, err := c.client().Do(req)
	addStep("query_random_audio", started, err == nil, redactedURL(u), err)
	if err != nil {
		result.Debug = debug
		return result, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("jellyfin random song returned %s", resp.Status)
		addStep("http_status", time.Now(), false, resp.Status, err)
		result.Debug = debug
		return result, err
	}
	var body jellyfinSearchResponse
	started = time.Now()
	err = json.NewDecoder(resp.Body).Decode(&body)
	addStep("decode_response", started, err == nil, fmt.Sprintf("%d item(s)", len(body.Items)), err)
	if err != nil {
		result.Debug = debug
		return result, err
	}
	if len(body.Items) == 0 {
		err := errors.New("jellyfin returned no audio items")
		addStep("select_song", time.Now(), false, "no Audio item returned", err)
		result.Debug = debug
		return result, err
	}
	item := body.Items[0]
	result.Item = MediaItem{
		ID:       item.ID,
		Name:     item.Name,
		Type:     item.Type,
		Summary:  item.Overview,
		StreamID: item.ID,
	}
	result.Debug = debug
	return result, nil
}

func (c *Connector) RandomPlexSong(ctx context.Context, cfg ConnectorMediaConfig) (RandomSongTest, error) {
	cfg = normalizedConnectorMediaConfig(cfg)
	debug := []map[string]any{}
	addStep := func(name string, started time.Time, ok bool, detail string, err error) {
		step := map[string]any{
			"name":        name,
			"ok":          ok,
			"detail":      detail,
			"duration_ms": time.Since(started).Milliseconds(),
		}
		if err != nil {
			step["error"] = err.Error()
		}
		debug = append(debug, step)
	}
	result := RandomSongTest{Service: "plex", Debug: debug}
	if cfg.PlexBaseURL == "" {
		err := errServiceNotConfigured
		result.Debug = append(result.Debug, map[string]any{"name": "config", "ok": false, "detail": "plex_base_url is not configured", "error": err.Error()})
		return result, err
	}

	queryReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/", nil)
	if err != nil {
		return result, err
	}
	started := time.Now()
	candidates, err := c.searchPlexWithConfig(queryReq, cfg, "a")
	addStep("query_random_audio", started, err == nil, redactedURL(func() *url.URL {
		u, _ := url.Parse(cfg.PlexBaseURL)
		if u == nil {
			return nil
		}
		u.Path = path.Join(u.Path, "/hubs/search")
		q := u.Query()
		q.Set("query", "a")
		q.Set("limit", "20")
		if cfg.PlexToken != "" {
			q.Set("X-Plex-Token", "redacted")
		}
		u.RawQuery = q.Encode()
		return u
	}()), err)
	if err != nil {
		result.Debug = debug
		return result, err
	}
	audioItems := make([]MediaItem, 0, len(candidates))
	for _, item := range candidates {
		if item.StreamID == "" {
			continue
		}
		if strings.EqualFold(item.Type, "track") {
			audioItems = append(audioItems, item)
		}
	}
	if len(audioItems) == 0 {
		err := errors.New("plex returned no audio items")
		addStep("select_song", time.Now(), false, "no track items returned", err)
		result.Debug = debug
		return result, err
	}
	selection := time.Now().UnixNano() % int64(len(audioItems))
	item := audioItems[int(selection)]
	addStep("select_song", time.Now(), true, item.Name, nil)
	result.Item = item
	result.Debug = debug
	return result, nil
}

func redactedURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	clone := *u
	values := clone.Query()
	for _, key := range []string{"api_key", "X-Emby-Token", "X-Plex-Token"} {
		if values.Has(key) {
			values.Set(key, "redacted")
		}
	}
	clone.RawQuery = values.Encode()
	return clone.String()
}

var jellyfinIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func (c *Connector) jellyfinStreamRequest(r *http.Request, streamID string) (*http.Request, error) {
	return c.jellyfinStreamRequestWithConfig(r, c.MediaConfig(), streamID)
}

func (c *Connector) jellyfinStreamRequestWithConfig(r *http.Request, cfg ConnectorMediaConfig, streamID string) (*http.Request, error) {
	cfg = normalizedConnectorMediaConfig(cfg)
	if cfg.JellyfinBaseURL == "" {
		return nil, errServiceNotConfigured
	}
	if !jellyfinIDPattern.MatchString(streamID) {
		return nil, errors.New("invalid jellyfin stream id")
	}
	u, err := url.Parse(cfg.JellyfinBaseURL)
	if err != nil {
		return nil, err
	}
	u.Path = path.Join(u.Path, "/Items", streamID, "Download")
	if cfg.JellyfinAPIKey != "" {
		q := u.Query()
		q.Set("api_key", cfg.JellyfinAPIKey)
		u.RawQuery = q.Encode()
	}
	return http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
}

func (c *Connector) jellyfinBrowserAudioRequestWithConfig(r *http.Request, cfg ConnectorMediaConfig, streamID string) (*http.Request, error) {
	cfg = normalizedConnectorMediaConfig(cfg)
	if cfg.JellyfinBaseURL == "" {
		return nil, errServiceNotConfigured
	}
	if !jellyfinIDPattern.MatchString(streamID) {
		return nil, errors.New("invalid jellyfin stream id")
	}
	u, err := url.Parse(cfg.JellyfinBaseURL)
	if err != nil {
		return nil, err
	}
	u.Path = path.Join(u.Path, "/Audio", streamID, "universal")
	q := u.Query()
	q.Set("AudioCodec", "mp3")
	q.Set("Container", "mp3")
	q.Set("MaxStreamingBitrate", "192000")
	q.Set("TranscodingContainer", "mp3")
	q.Set("TranscodingProtocol", "http")
	if cfg.JellyfinAPIKey != "" {
		q.Set("api_key", cfg.JellyfinAPIKey)
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if cfg.JellyfinAPIKey != "" {
		req.Header.Set("X-Emby-Token", cfg.JellyfinAPIKey)
	}
	req.Header.Set("Accept", "audio/mpeg")
	return req, nil
}

type plexSearchResponse struct {
	XMLName  xml.Name       `xml:"MediaContainer"`
	Hubs     []plexHub      `xml:"Hub"`
	Metadata []plexMetadata `xml:"Metadata"`
}

type plexHub struct {
	Metadata []plexMetadata `xml:"Metadata"`
}

type plexMetadata struct {
	RatingKey string      `xml:"ratingKey,attr"`
	Key       string      `xml:"key,attr"`
	Title     string      `xml:"title,attr"`
	Type      string      `xml:"type,attr"`
	Summary   string      `xml:"summary,attr"`
	Media     []plexMedia `xml:"Media"`
}

type plexMedia struct {
	Parts []plexPart `xml:"Part"`
}

type plexPart struct {
	Key string `xml:"key,attr"`
}

func (c *Connector) searchPlex(r *http.Request, query string) ([]MediaItem, error) {
	return c.searchPlexWithConfig(r, c.MediaConfig(), query)
}

func (c *Connector) searchPlexWithConfig(r *http.Request, cfg ConnectorMediaConfig, query string) ([]MediaItem, error) {
	cfg = normalizedConnectorMediaConfig(cfg)
	if cfg.PlexBaseURL == "" {
		return nil, errServiceNotConfigured
	}
	u, err := url.Parse(cfg.PlexBaseURL)
	if err != nil {
		return nil, err
	}
	u.Path = path.Join(u.Path, "/hubs/search")
	q := u.Query()
	q.Set("query", query)
	q.Set("limit", "20")
	if cfg.PlexToken != "" {
		q.Set("X-Plex-Token", cfg.PlexToken)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("plex search returned %s", resp.Status)
	}
	var body plexSearchResponse
	if err := xml.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	items := make([]MediaItem, 0)
	collect := func(metadata []plexMetadata) {
		for _, item := range metadata {
			media := MediaItem{
				ID:      firstNonEmpty(item.RatingKey, item.Key),
				Name:    item.Title,
				Type:    item.Type,
				Summary: item.Summary,
			}
			if partKey := firstPlexPartKey(item); validPlexPartPath(partKey) {
				media.StreamID = base64.RawURLEncoding.EncodeToString([]byte(partKey))
			}
			items = append(items, media)
		}
	}
	collect(body.Metadata)
	for _, hub := range body.Hubs {
		collect(hub.Metadata)
	}
	return items, nil
}

func firstPlexPartKey(item plexMetadata) string {
	for _, media := range item.Media {
		for _, part := range media.Parts {
			if part.Key != "" {
				return part.Key
			}
		}
	}
	return ""
}

var plexPartPathPattern = regexp.MustCompile(`^/library/parts/[0-9]+/[0-9]+/[^/?#]+$`)

func validPlexPartPath(value string) bool {
	return plexPartPathPattern.MatchString(value)
}

func (c *Connector) plexStreamRequest(r *http.Request, streamID string) (*http.Request, error) {
	return c.plexStreamRequestWithConfig(r, c.MediaConfig(), streamID)
}

func (c *Connector) plexStreamRequestWithConfig(r *http.Request, cfg ConnectorMediaConfig, streamID string) (*http.Request, error) {
	cfg = normalizedConnectorMediaConfig(cfg)
	if cfg.PlexBaseURL == "" {
		return nil, errServiceNotConfigured
	}
	raw, err := base64.RawURLEncoding.DecodeString(streamID)
	if err != nil {
		return nil, errors.New("invalid plex stream id")
	}
	partPath := string(raw)
	if !validPlexPartPath(partPath) {
		return nil, errors.New("invalid plex stream path")
	}
	u, err := url.Parse(cfg.PlexBaseURL)
	if err != nil {
		return nil, err
	}
	u.Path = path.Join(u.Path, partPath)
	if cfg.PlexToken != "" {
		q := u.Query()
		q.Set("X-Plex-Token", cfg.PlexToken)
		u.RawQuery = q.Encode()
	}
	return http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
