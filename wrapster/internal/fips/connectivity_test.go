package fips

import "testing"

func TestInferPeerTransport(t *testing.T) {
	tests := []struct {
		addr string
		want string
	}{
		{addr: "example.org:2121", want: "udp"},
		{addr: "example.org:8443", want: "tcp"},
		{addr: "example.org:1234", want: "tcp"},
		{addr: "not-host-port", want: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got := InferPeerTransport(tt.addr); got != tt.want {
				t.Fatalf("InferPeerTransport(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}

func TestCheckPeerConnectivityWaitingForOutboundPeer(t *testing.T) {
	status := CheckPeerConnectivityWithDebug("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "")

	if status["state"] != "waiting_for_outbound_peer" {
		t.Fatalf("state = %#v, want waiting_for_outbound_peer", status["state"])
	}
	if status["reachable"] != false || status["transport_check_skipped"] != true {
		t.Fatalf("unexpected status: %#v", status)
	}
	steps, ok := status["debug_steps"].([]map[string]any)
	if !ok || len(steps) != 1 || steps[0]["name"] != "transport" {
		t.Fatalf("unexpected debug steps: %#v", status["debug_steps"])
	}
}

func TestCheckPeerConnectivityRejectsInvalidNpub(t *testing.T) {
	status := CheckPeerConnectivityWithDebugRequirePeer("not-a-npub", "")

	if status["peer_npub_ok"] != false {
		t.Fatalf("peer_npub_ok = %#v, want false", status["peer_npub_ok"])
	}
	if status["error"] != "fips_peer_npub must be a valid npub or hex public key" {
		t.Fatalf("unexpected error: %#v", status["error"])
	}
}

func TestCheckPeerConnectivityRejectsMissingRequiredNpub(t *testing.T) {
	status := CheckPeerConnectivityWithDebugRequirePeer("", "")

	if status["peer_npub_ok"] != false {
		t.Fatalf("peer_npub_ok = %#v, want false", status["peer_npub_ok"])
	}
	if status["error"] != "fips_peer_npub is not set" {
		t.Fatalf("unexpected error: %#v", status["error"])
	}
	errorText, reject := ConnectivityError(status)
	if !reject || errorText != "fips_peer_npub is required" {
		t.Fatalf("ConnectivityError = %q, %v", errorText, reject)
	}
}

func TestCheckPeerConnectivityRejectsInvalidHostPort(t *testing.T) {
	status := CheckPeerConnectivityWithDebugRequirePeer("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "not-host-port")

	if status["error"] != "fips_peer_addr must be host:port" {
		t.Fatalf("unexpected status: %#v", status)
	}
	errorText, reject := ConnectivityError(status)
	if !reject || errorText != "fips_peer_addr must be host:port" {
		t.Fatalf("ConnectivityError = %q, %v", errorText, reject)
	}
}

func TestConnectivityErrorIgnoresWaitingForPeerAddress(t *testing.T) {
	check := map[string]any{
		"error":         "temporary note",
		"peer_npub_ok":  true,
		"peer_addr_set": false,
	}

	errorText, reject := ConnectivityError(check)

	if reject || errorText != "" {
		t.Fatalf("ConnectivityError = %q, %v", errorText, reject)
	}
}
