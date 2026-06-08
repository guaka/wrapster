package admin

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

const NIP98EventKind = 27235

var (
	ErrMissingAuthorization = errors.New("missing Nostr authorization")
	ErrWrongScheme          = errors.New("authorization scheme must be Nostr")
	ErrBadEncoding          = errors.New("authorization event is not valid base64 JSON")
	ErrWrongKind            = errors.New("authorization event must be kind 27235")
	ErrBadSignature         = errors.New("authorization event signature is invalid")
	ErrStaleEvent           = errors.New("authorization event timestamp is outside allowed age")
	ErrWrongURL             = errors.New("authorization event URL does not match request")
	ErrWrongMethod          = errors.New("authorization event method does not match request")
	ErrNotAdmin             = errors.New("pubkey is not an admin")
)

type Authorizer struct {
	Admins map[string]struct{}
	MaxAge time.Duration
	Now    func() time.Time
}

func NewAuthorizer(adminPubkeys []string, maxAge time.Duration) Authorizer {
	admins := make(map[string]struct{}, len(adminPubkeys))
	for _, pubkey := range adminPubkeys {
		pubkey = NormalizePubkey(pubkey)
		if pubkey != "" {
			admins[pubkey] = struct{}{}
		}
	}
	return Authorizer{Admins: admins, MaxAge: maxAge}
}

func NormalizePubkey(pubkey string) string {
	pubkey = strings.ToLower(strings.TrimSpace(pubkey))
	if strings.HasPrefix(pubkey, "npub") {
		if decoded, ok := decodeNpub(pubkey); ok {
			return decoded
		}
	}
	return pubkey
}

func decodeNpub(value string) (string, bool) {
	hrp, data, ok := decodeBech32(value)
	if !ok || hrp != "npub" {
		return "", false
	}
	converted, ok := convertBits(data, 5, 8, false)
	if !ok || len(converted) < 32 {
		return "", false
	}
	return fmt.Sprintf("%x", converted[:32]), true
}

func decodeBech32(value string) (string, []byte, bool) {
	if value != strings.ToLower(value) {
		return "", nil, false
	}
	separator := strings.LastIndexByte(value, '1')
	if separator < 1 || separator+7 > len(value) {
		return "", nil, false
	}
	hrp := value[:separator]
	rawData := value[separator+1:]
	data := make([]byte, len(rawData))
	for i, r := range rawData {
		index := strings.IndexRune(bech32Charset, r)
		if index < 0 {
			return "", nil, false
		}
		data[i] = byte(index)
	}
	if !bech32ChecksumOK(hrp, data) {
		return "", nil, false
	}
	return hrp, data[:len(data)-6], true
}

const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

func bech32ChecksumOK(hrp string, data []byte) bool {
	values := make([]byte, 0, len(hrp)*2+1+len(data))
	for _, r := range hrp {
		if r < 33 || r > 126 {
			return false
		}
		values = append(values, byte(r>>5))
	}
	values = append(values, 0)
	for _, r := range hrp {
		values = append(values, byte(r&31))
	}
	values = append(values, data...)
	return bech32Polymod(values) == 1
}

func bech32Polymod(values []byte) uint32 {
	chk := uint32(1)
	generator := [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	for _, value := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(value)
		for i := range generator {
			if (top>>i)&1 == 1 {
				chk ^= generator[i]
			}
		}
	}
	return chk
}

func convertBits(data []byte, fromBits, toBits uint, pad bool) ([]byte, bool) {
	acc := uint(0)
	bits := uint(0)
	maxValue := uint((1 << toBits) - 1)
	maxAcc := uint((1 << (fromBits + toBits - 1)) - 1)
	out := make([]byte, 0, len(data)*int(fromBits)/int(toBits))
	for _, value := range data {
		v := uint(value)
		if v>>fromBits != 0 {
			return nil, false
		}
		acc = ((acc << fromBits) | v) & maxAcc
		bits += fromBits
		for bits >= toBits {
			bits -= toBits
			out = append(out, byte((acc>>bits)&maxValue))
		}
	}
	if pad {
		if bits > 0 {
			out = append(out, byte((acc<<(toBits-bits))&maxValue))
		}
	} else if bits >= fromBits || ((acc<<(toBits-bits))&maxValue) != 0 {
		return nil, false
	}
	return out, true
}

func (a Authorizer) VerifyRequest(r *http.Request) (string, error) {
	return a.VerifyHeader(r.Header.Get("Authorization"), AbsoluteRequestURL(r), r.Method)
}

func (a Authorizer) VerifyHeader(header, requestURL, method string) (string, error) {
	event, err := EventFromAuthorization(header)
	if err != nil {
		return "", err
	}
	pubkey, err := a.VerifyEvent(event, requestURL, method)
	if err != nil {
		return "", err
	}
	return pubkey, nil
}

func (a Authorizer) VerifyEvent(event nostr.Event, requestURL, method string) (string, error) {
	if event.Kind != NIP98EventKind {
		return "", ErrWrongKind
	}
	ok, err := event.CheckSignature()
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrBadSignature, err)
	}
	if !ok {
		return "", ErrBadSignature
	}

	maxAge := a.MaxAge
	if maxAge <= 0 {
		maxAge = time.Minute
	}
	now := time.Now()
	if a.Now != nil {
		now = a.Now()
	}
	createdAt := event.CreatedAt.Time()
	if createdAt.Before(now.Add(-maxAge)) || createdAt.After(now.Add(maxAge)) {
		return "", ErrStaleEvent
	}

	if tagValue(event.Tags, "u") != requestURL {
		return "", ErrWrongURL
	}
	if !strings.EqualFold(tagValue(event.Tags, "method"), method) {
		return "", ErrWrongMethod
	}

	pubkey := strings.ToLower(event.PubKey)
	if _, ok := a.Admins[pubkey]; !ok {
		return "", ErrNotAdmin
	}
	return pubkey, nil
}

func EventFromAuthorization(header string) (nostr.Event, error) {
	header = strings.TrimSpace(header)
	if header == "" {
		return nostr.Event{}, ErrMissingAuthorization
	}
	scheme, encoded, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Nostr") || strings.TrimSpace(encoded) == "" {
		return nostr.Event{}, ErrWrongScheme
	}

	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nostr.Event{}, ErrBadEncoding
	}
	var event nostr.Event
	if err := json.Unmarshal(raw, &event); err != nil {
		return nostr.Event{}, ErrBadEncoding
	}
	return event, nil
}

func AbsoluteRequestURL(r *http.Request) string {
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return scheme + "://" + host + r.URL.RequestURI()
}

func tagValue(tags nostr.Tags, name string) string {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == name {
			return tag[1]
		}
	}
	return ""
}
