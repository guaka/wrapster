package fips

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/nbd-wtf/go-nostr"
	adminauth "github.com/trustroots/nostroots/vibe/wrapster/internal/admin"
)

// CheckPeerConnectivity builds the compact FIPS peer check payload used by the
// setup status endpoint.
func CheckPeerConnectivity(peerNpub, peerAddr string) map[string]any {
	return checkPeerConnectivity(peerNpub, peerAddr, false, false)
}

// CheckPeerConnectivityWithDebug builds the FIPS peer check payload with
// diagnostic steps for the relay admin check endpoint.
func CheckPeerConnectivityWithDebug(peerNpub, peerAddr string) map[string]any {
	return checkPeerConnectivity(peerNpub, peerAddr, true, true)
}

func checkPeerConnectivity(peerNpub, peerAddr string, includeDebug, requirePeerNpub bool) map[string]any {
	peerNpub = strings.TrimSpace(peerNpub)
	peerAddr = strings.TrimSpace(peerAddr)
	peerAddrSet := peerAddr != ""
	status := map[string]any{
		"peer_npub":               peerNpub,
		"peer_addr":               peerAddr,
		"peer_npub_ok":            true,
		"peer_addr_set":           peerAddrSet,
		"transport_check_skipped": !peerAddrSet,
		"reachable":               false,
		"transport":               InferPeerTransport(peerAddr),
	}
	steps := make([]map[string]any, 0, 3)
	if includeDebug {
		defer func() {
			status["debug_steps"] = steps
		}()
	}

	if peerNpub == "" && requirePeerNpub {
		status["peer_npub_ok"] = false
		status["error"] = "fips_peer_npub is not set"
		return status
	}
	if normalized := adminauth.NormalizePubkey(peerNpub); peerNpub != "" && (normalized == "" || !nostr.IsValidPublicKeyHex(normalized)) {
		status["peer_npub_ok"] = false
		status["error"] = "fips_peer_npub must be a valid npub or hex public key"
		return status
	}

	if !peerAddrSet {
		if peerNpub == "" {
			status["state"] = "not_configured"
			status["message"] = "FIPS peer is not configured"
			return status
		}
		message := "peer identity is configured; add the public peer address to test outbound transport"
		if includeDebug {
			message = "NAS peer identity is configured; waiting for the NAS to open its outbound FIPS session"
			addPeerCheckStep(&steps, "transport", time.Now(), true, "peer address not configured; waiting for outbound NAS session", nil)
		}
		status["state"] = "waiting_for_outbound_peer"
		status["message"] = message
		return status
	}

	parseStarted := time.Now()
	peerHost, peerPort, splitErr := net.SplitHostPort(peerAddr)
	if includeDebug {
		addPeerCheckStep(&steps, "parse", parseStarted, splitErr == nil, "peer address "+peerAddr, splitErr)
	}
	if splitErr != nil {
		status["error"] = "fips_peer_addr must be host:port"
		return status
	}

	var checkErr error
	if includeDebug {
		resolveStarted := time.Now()
		peerAddresses, resolveErr := net.LookupHost(peerHost)
		resolveDetail := "resolved to " + strings.Join(peerAddresses, ", ")
		addPeerCheckStep(&steps, "dns", resolveStarted, resolveErr == nil, resolveDetail, resolveErr)
		if resolveErr != nil {
			checkErr = resolveErr
		}
	}
	transport := InferPeerTransport(peerAddr)
	status["transport"] = transport
	if checkErr == nil {
		connectStarted := time.Now()
		var reachable bool
		reachable, transport, checkErr = TestPeerAddress(peerAddr, transport)
		status["transport"] = transport
		status["reachable"] = reachable
		if includeDebug {
			connectDetail := "connect to " + peerHost + ":" + peerPort + " over " + transport
			addPeerCheckStep(&steps, "transport", connectStarted, checkErr == nil, connectDetail, checkErr)
		}
	}
	if checkErr != nil {
		status["error"] = checkErr.Error()
	}
	return status
}

// InferPeerTransport chooses the FIPS transport used for a host:port address.
func InferPeerTransport(addr string) string {
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

// TestPeerAddress probes a FIPS peer address over the requested transport.
func TestPeerAddress(addr, transport string) (bool, string, error) {
	switch strings.ToLower(transport) {
	case "udp":
		if err := testPeerUDP(addr); err != nil {
			return false, "udp", err
		}
		return true, "udp", nil
	default:
		if err := testPeerTCP(addr); err != nil {
			return false, "tcp", err
		}
		return true, "tcp", nil
	}
}

func addPeerCheckStep(steps *[]map[string]any, name string, started time.Time, ok bool, detail string, err error) {
	step := map[string]any{
		"name":        name,
		"ok":          ok,
		"detail":      detail,
		"duration_ms": time.Since(started).Milliseconds(),
	}
	if err != nil {
		step["error"] = err.Error()
	}
	*steps = append(*steps, step)
}

func testPeerTCP(addr string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

func testPeerUDP(addr string) error {
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
