package media

import "testing"

func TestParseServiceRoute(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		prefix    string
		wantOK    bool
		wantRoute serviceRoute
	}{
		{
			name:   "search",
			path:   "/media/api/services/jellyfin/search",
			prefix: "/media/api/services/",
			wantOK: true,
			wantRoute: serviceRoute{
				Service: "jellyfin",
				Action:  "search",
			},
		},
		{
			name:   "stream",
			path:   "/connector/api/services/plex/stream/abc123",
			prefix: "/connector/api/services/",
			wantOK: true,
			wantRoute: serviceRoute{
				Service:  "plex",
				Action:   "stream",
				StreamID: "abc123",
			},
		},
		{
			name:   "unknown service",
			path:   "/media/api/services/router/search",
			prefix: "/media/api/services/",
		},
		{
			name:   "extra search segment",
			path:   "/media/api/services/jellyfin/search/extra",
			prefix: "/media/api/services/",
		},
		{
			name:   "missing stream id",
			path:   "/connector/api/services/plex/stream/",
			prefix: "/connector/api/services/",
		},
		{
			name:   "unknown action",
			path:   "/connector/api/services/plex/delete",
			prefix: "/connector/api/services/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseServiceRoute(tt.path, tt.prefix)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.wantRoute {
				t.Fatalf("route = %#v, want %#v", got, tt.wantRoute)
			}
		})
	}
}
