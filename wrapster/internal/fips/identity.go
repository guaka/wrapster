package fips

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
)

type Identity struct {
	Npub string `json:"npub"`
}

func SaveNsec(path, nsec string) (Identity, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return Identity{}, errors.New("FIPS nsec path is not configured")
	}
	identity, err := NsecIdentity(nsec)
	if err != nil {
		return Identity{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Identity{}, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".fips-nsec-*")
	if err != nil {
		return Identity{}, err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if _, err := tmp.WriteString(strings.TrimSpace(nsec) + "\n"); err != nil {
		_ = tmp.Close()
		return Identity{}, err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return Identity{}, err
	}
	if err := tmp.Close(); err != nil {
		return Identity{}, err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return Identity{}, err
	}
	return identity, nil
}

func NsecIdentity(nsec string) (Identity, error) {
	nsec = strings.TrimSpace(nsec)
	prefix, value, err := nip19.Decode(nsec)
	if err != nil {
		return Identity{}, fmt.Errorf("invalid nsec: %w", err)
	}
	if prefix != "nsec" {
		return Identity{}, errors.New("value must be an nsec")
	}
	privateKey, ok := value.(string)
	if !ok || len(privateKey) != 64 {
		return Identity{}, errors.New("invalid nsec private key")
	}
	pubkey, err := nostr.GetPublicKey(privateKey)
	if err != nil {
		return Identity{}, fmt.Errorf("invalid nsec private key: %w", err)
	}
	npub, err := nip19.EncodePublicKey(pubkey)
	if err != nil {
		return Identity{}, fmt.Errorf("encode npub: %w", err)
	}
	return Identity{Npub: npub}, nil
}
