package fips

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
)

func TestNsecIdentityDerivesNpub(t *testing.T) {
	privateKey := nostr.GeneratePrivateKey()
	wantPubkey, err := nostr.GetPublicKey(privateKey)
	if err != nil {
		t.Fatalf("GetPublicKey returned error: %v", err)
	}
	wantNpub, err := nip19.EncodePublicKey(wantPubkey)
	if err != nil {
		t.Fatalf("EncodePublicKey returned error: %v", err)
	}
	nsec, err := nip19.EncodePrivateKey(privateKey)
	if err != nil {
		t.Fatalf("EncodePrivateKey returned error: %v", err)
	}

	identity, err := NsecIdentity(" \n" + nsec + "\t")

	if err != nil {
		t.Fatalf("NsecIdentity returned error: %v", err)
	}
	if identity.Npub != wantNpub {
		t.Fatalf("npub = %q, want %q", identity.Npub, wantNpub)
	}
}

func TestNsecIdentityRejectsNonNsec(t *testing.T) {
	privateKey := nostr.GeneratePrivateKey()
	pubkey, err := nostr.GetPublicKey(privateKey)
	if err != nil {
		t.Fatalf("GetPublicKey returned error: %v", err)
	}
	npub, err := nip19.EncodePublicKey(pubkey)
	if err != nil {
		t.Fatalf("EncodePublicKey returned error: %v", err)
	}

	if _, err := NsecIdentity(npub); err == nil || !strings.Contains(err.Error(), "value must be an nsec") {
		t.Fatalf("expected non-nsec error, got %v", err)
	}
}

func TestSaveNsecWritesTrimmedSecretWithPrivatePermissions(t *testing.T) {
	privateKey := nostr.GeneratePrivateKey()
	nsec, err := nip19.EncodePrivateKey(privateKey)
	if err != nil {
		t.Fatalf("EncodePrivateKey returned error: %v", err)
	}
	path := filepath.Join(t.TempDir(), "nested", "fips-nsec")

	if _, err := SaveNsec(path, "\n"+nsec+" "); err != nil {
		t.Fatalf("SaveNsec returned error: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(body) != nsec+"\n" {
		t.Fatalf("saved nsec = %q, want trimmed secret plus newline", string(body))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat returned error: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}
}

func TestSaveNsecRejectsEmptyPath(t *testing.T) {
	if _, err := SaveNsec("", "nsec1example"); err == nil || !strings.Contains(err.Error(), "path is not configured") {
		t.Fatalf("expected empty path error, got %v", err)
	}
}
