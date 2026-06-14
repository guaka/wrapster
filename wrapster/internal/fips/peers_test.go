package fips

import "testing"

func TestPeerListEmpty(t *testing.T) {
	list := PeerList("", "", nil)
	if len(list) != 0 {
		t.Fatalf("expected empty peer list, got %#v", list)
	}
}

func TestPeerListMinimal(t *testing.T) {
	npub := "npub1abc"
	list := PeerList(npub, "", nil)
	if len(list) != 1 {
		t.Fatalf("expected 1 peer entry, got %d", len(list))
	}
	if list[0].Npub != npub {
		t.Fatalf("expected npub %s, got %s", npub, list[0].Npub)
	}
	if list[0].Addr != "" {
		t.Fatalf("expected empty addr, got %s", list[0].Addr)
	}
	if !list[0].Configured {
		t.Fatal("expected configured=true")
	}
	if list[0].AddrConfigured {
		t.Fatal("expected addr_configured=false")
	}
	if list[0].Check != nil {
		t.Fatalf("expected empty check map, got %+v", list[0].Check)
	}
}

func TestPeerListWithCheck(t *testing.T) {
	check := map[string]any{"reachable": true, "peer_addr": "example.org:8443"}
	list := PeerList("npub1def", "example.org:8443", check)
	if len(list) != 1 {
		t.Fatalf("expected 1 peer entry, got %d", len(list))
	}
	if list[0].Check == nil || list[0].Check["reachable"] != true {
		t.Fatalf("expected check payload, got %#v", list[0].Check)
	}
}
