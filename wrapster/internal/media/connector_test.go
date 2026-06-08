package media

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConnectorRestrictsRemoteAddressAndToken(t *testing.T) {
	_, allowedNetwork, err := net.ParseCIDR("10.77.0.1/32")
	if err != nil {
		t.Fatalf("ParseCIDR returned error: %v", err)
	}
	connector := Connector{
		AllowedCIDRs: []*net.IPNet{allowedNetwork},
		SharedToken:  "secret",
	}

	tests := []struct {
		name       string
		remoteAddr string
		token      string
		wantStatus int
	}{
		{name: "wrong remote", remoteAddr: "10.77.0.2:1234", token: "secret", wantStatus: http.StatusForbidden},
		{name: "missing token", remoteAddr: "10.77.0.1:1234", wantStatus: http.StatusUnauthorized},
		{name: "allowed", remoteAddr: "10.77.0.1:1234", token: "secret", wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://connector.test/connector/api/status", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			rec := httptest.NewRecorder()

			connector.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tt.wantStatus, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestConnectorSearchesJellyfinThroughCuratedRoute(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Items" {
			t.Fatalf("unexpected upstream path %s", r.URL.Path)
		}
		if r.URL.Query().Get("SearchTerm") != "matrix" {
			t.Fatalf("unexpected query %s", r.URL.RawQuery)
		}
		if r.Header.Get("X-Emby-Token") != "jellyfin-token" {
			t.Fatalf("missing jellyfin token")
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"Items": []map[string]string{
				{"Id": "item123", "Name": "The Matrix", "Type": "Movie", "Overview": "Wake up."},
			},
		})
	})

	connector := Connector{
		JellyfinBaseURL: "http://jellyfin.test",
		JellyfinAPIKey:  "jellyfin-token",
		HTTPClient:      clientFor(upstream),
	}
	req := httptest.NewRequest(http.MethodGet, "http://connector.test/connector/api/services/jellyfin/search?q=matrix", nil)
	rec := httptest.NewRecorder()

	connector.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []MediaItem `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].StreamID != "item123" {
		t.Fatalf("unexpected items: %+v", body.Items)
	}
}

func TestConnectorStreamsPlexOnlyFromValidatedPartPath(t *testing.T) {
	var gotPath, gotRange string
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRange = r.Header.Get("Range")
		if r.URL.Query().Get("X-Plex-Token") != "plex-token" {
			t.Fatalf("missing plex token")
		}
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Range", "bytes 0-9/20")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("0123456789"))
	})

	connector := Connector{
		PlexBaseURL: "http://plex.test",
		PlexToken:   "plex-token",
		HTTPClient:  clientFor(upstream),
	}
	partPath := "/library/parts/123/456/file.mp4"
	streamID := base64.RawURLEncoding.EncodeToString([]byte(partPath))
	req := httptest.NewRequest(http.MethodGet, "http://connector.test/connector/api/services/plex/stream/"+streamID, nil)
	req.Header.Set("Range", "bytes=0-9")
	rec := httptest.NewRecorder()

	connector.ServeHTTP(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("expected status 206, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotPath != partPath || gotRange != "bytes=0-9" || strings.TrimSpace(rec.Body.String()) != "0123456789" {
		t.Fatalf("unexpected upstream path=%q range=%q body=%q", gotPath, gotRange, rec.Body.String())
	}

	badID := base64.RawURLEncoding.EncodeToString([]byte("/library/metadata/123"))
	badReq := httptest.NewRequest(http.MethodGet, "http://connector.test/connector/api/services/plex/stream/"+badID, nil)
	badRec := httptest.NewRecorder()

	connector.ServeHTTP(badRec, badReq)

	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid plex stream path to be rejected, got %d", badRec.Code)
	}
}
