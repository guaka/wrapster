package fips

import "strings"

// FIPSPeerStatus is a compact payload for UI views that display configured peers.
type FIPSPeerStatus struct {
	Npub           string         `json:"npub"`
	Addr           string         `json:"addr"`
	Configured     bool           `json:"configured"`
	AddrConfigured bool           `json:"addr_configured"`
	Check          map[string]any `json:"check,omitempty"`
}

// PeerList builds a list payload from the configured peer fields so both admin UIs
// can render peers using the same shape.
func PeerList(npub, addr string, check map[string]any) []FIPSPeerStatus {
	peerNpub := strings.TrimSpace(npub)
	peerAddr := strings.TrimSpace(addr)
	if peerNpub == "" && peerAddr == "" {
		return nil
	}
	entry := FIPSPeerStatus{
		Npub:           peerNpub,
		Addr:           peerAddr,
		Configured:     peerNpub != "",
		AddrConfigured: peerAddr != "",
	}
	if check != nil {
		entry.Check = check
	}
	return []FIPSPeerStatus{entry}
}
