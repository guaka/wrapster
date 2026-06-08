package media

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nbd-wtf/go-nostr"
	adminauth "github.com/trustroots/nostroots/vibe/wrapster/internal/admin"
)

var (
	ErrNotGranted     = errors.New("pubkey is not granted media access")
	ErrBadSignature   = errors.New("authorization event signature is invalid")
	ErrStaleEvent     = errors.New("authorization event timestamp is outside allowed age")
	ErrWrongKind      = errors.New("authorization event must be kind 27235")
	ErrWrongURL       = errors.New("authorization event URL does not match request")
	ErrWrongMethod    = errors.New("authorization event method does not match request")
	ErrNoGrantPubkeys = errors.New("no media grant pubkeys are configured")
)

type Authorizer struct {
	Grants map[string]struct{}
	MaxAge time.Duration
	Now    func() time.Time
}

func NewAuthorizer(grantPubkeys []string, maxAge time.Duration) Authorizer {
	grants := make(map[string]struct{}, len(grantPubkeys))
	for _, pubkey := range grantPubkeys {
		pubkey = strings.ToLower(strings.TrimSpace(pubkey))
		if pubkey != "" {
			grants[pubkey] = struct{}{}
		}
	}
	return Authorizer{Grants: grants, MaxAge: maxAge}
}

func (a Authorizer) VerifyRequest(r *http.Request) (string, error) {
	event, err := adminauth.EventFromAuthorization(r.Header.Get("Authorization"))
	if err != nil {
		return "", err
	}
	return a.VerifyEvent(event, adminauth.AbsoluteRequestURL(r), r.Method)
}

func (a Authorizer) VerifyEvent(event nostr.Event, requestURL, method string) (string, error) {
	if event.Kind != adminauth.NIP98EventKind {
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
	if len(a.Grants) == 0 {
		return "", ErrNoGrantPubkeys
	}

	pubkey := strings.ToLower(event.PubKey)
	if _, ok := a.Grants[pubkey]; !ok {
		return "", ErrNotGranted
	}
	return pubkey, nil
}

func tagValue(tags nostr.Tags, name string) string {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == name {
			return tag[1]
		}
	}
	return ""
}
