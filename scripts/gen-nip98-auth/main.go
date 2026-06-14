package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
)

const nip98EventKind = 27235

func main() {
	nsec := flag.String("nsec", "", "private key (nsec or hex)")
	url := flag.String("url", "", "request URL for NIP-98 u tag")
	method := flag.String("method", "GET", "HTTP method for NIP-98 method tag")
	flag.Parse()

	if strings.TrimSpace(*nsec) == "" || strings.TrimSpace(*url) == "" {
		fmt.Fprintln(os.Stderr, "usage: gen-nip98-auth --nsec <nsec> --url <url> [--method GET]")
		os.Exit(2)
	}

	privateKey, err := decodePrivateKey(*nsec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "decode private key: %v\n", err)
		os.Exit(1)
	}

	event := nostr.Event{
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Kind:      nip98EventKind,
		Tags:      nostr.Tags{{"u", *url}, {"method", strings.ToUpper(*method)}},
		Content:   "",
	}
	if err := event.Sign(privateKey); err != nil {
		fmt.Fprintf(os.Stderr, "sign event: %v\n", err)
		os.Exit(1)
	}

	raw, err := json.Marshal(event)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal event: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Nostr %s\n", base64.StdEncoding.EncodeToString(raw))
}

func decodePrivateKey(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(value), "nsec1") {
		prefix, decoded, err := nip19.Decode(value)
		if err != nil {
			return "", err
		}
		if prefix != "nsec" {
			return "", fmt.Errorf("value must be an nsec")
		}
		privateKey, ok := decoded.(string)
		if !ok || len(privateKey) != 64 {
			return "", fmt.Errorf("invalid nsec private key")
		}
		return privateKey, nil
	}
	if len(value) == 64 {
		return value, nil
	}
	return "", fmt.Errorf("private key must be nsec or 64-char hex")
}
