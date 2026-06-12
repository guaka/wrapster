package relay

import (
	_ "embed"
	"html"
	"net/http"
	"strings"
)

//go:embed static/service-directory.html
var serviceAdvertBrowserHTML string

const serviceAdvertBrowserRelayPlaceholder = "{{PUBLIC_RELAY_URL}}"

func (s *Server) serviceAdvertBrowser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == http.MethodHead {
		return
	}
	relayURL := s.PublicRelayURL
	if relayURL == "" {
		relayURL = "ws://localhost:5542"
	}
	htmlBody := strings.ReplaceAll(serviceAdvertBrowserHTML, serviceAdvertBrowserRelayPlaceholder, html.EscapeString(relayURL))
	_, _ = w.Write([]byte(htmlBody))
}
