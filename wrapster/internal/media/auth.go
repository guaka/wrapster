package media

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/nbd-wtf/go-nostr"
	adminauth "github.com/trustroots/nostroots/vibe/wrapster/internal/admin"
)

var (
	ErrNotGranted     = errors.New("pubkey is not granted media access")
	ErrBadSignature   = adminauth.ErrBadSignature
	ErrStaleEvent     = adminauth.ErrStaleEvent
	ErrWrongKind      = adminauth.ErrWrongKind
	ErrWrongURL       = adminauth.ErrWrongURL
	ErrWrongMethod    = adminauth.ErrWrongMethod
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
	pubkey, err := adminauth.VerifyNIP98Event(event, requestURL, method, a.MaxAge, a.Now)
	if err != nil {
		return "", err
	}
	if len(a.Grants) == 0 {
		return "", ErrNoGrantPubkeys
	}

	if _, ok := a.Grants[pubkey]; !ok {
		return "", ErrNotGranted
	}
	return pubkey, nil
}
