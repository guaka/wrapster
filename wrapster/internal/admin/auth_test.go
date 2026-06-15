package admin

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

const testAdminOrigin = "https://wrapster.example"
const testInternalOrigin = "http://internal"

func TestVerifyHeader(t *testing.T) {
	privateKey := nostr.GeneratePrivateKey()
	pubkey, err := nostr.GetPublicKey(privateKey)
	if err != nil {
		t.Fatalf("GetPublicKey returned error: %v", err)
	}
	now := time.Unix(1700000000, 0)
	url := testAdminOrigin + AdminAPIStatus

	authz := NewAuthorizer([]string{pubkey}, time.Minute)
	authz.Now = func() time.Time { return now }

	header := signedHeader(t, privateKey, url, http.MethodGet, now)
	got, err := authz.VerifyHeader(header, url, http.MethodGet)
	if err != nil {
		t.Fatalf("VerifyHeader returned error: %v", err)
	}
	if got != pubkey {
		t.Fatalf("expected pubkey %s, got %s", pubkey, got)
	}
}

func TestNewAuthorizerAcceptsAdminNpub(t *testing.T) {
	pubkey := "3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d"
	npub := "npub180cvv07tjdrrgpa0j7j7tmnyl2yr6yr7l8j4s3evf6u64th6gkwsyjh6w6"

	authz := NewAuthorizer([]string{npub}, time.Minute)

	if _, ok := authz.Admins[pubkey]; !ok {
		t.Fatalf("expected decoded npub to authorize %s", pubkey)
	}
}

func TestVerifyHeaderFailures(t *testing.T) {
	privateKey := nostr.GeneratePrivateKey()
	pubkey, err := nostr.GetPublicKey(privateKey)
	if err != nil {
		t.Fatalf("GetPublicKey returned error: %v", err)
	}
	otherKey := nostr.GeneratePrivateKey()
	now := time.Unix(1700000000, 0)
	url := testAdminOrigin + AdminAPIStatus

	tests := []struct {
		name   string
		header string
		url    string
		method string
		admins []string
		want   error
	}{
		{
			name:   "missing",
			header: "",
			url:    url,
			method: http.MethodGet,
			admins: []string{pubkey},
			want:   ErrMissingAuthorization,
		},
		{
			name:   "wrong scheme",
			header: "Bearer token",
			url:    url,
			method: http.MethodGet,
			admins: []string{pubkey},
			want:   ErrWrongScheme,
		},
		{
			name:   "wrong url",
			header: signedHeader(t, privateKey, url, http.MethodGet, now),
			url:    testAdminOrigin + AdminAPIPolicy,
			method: http.MethodGet,
			admins: []string{pubkey},
			want:   ErrWrongURL,
		},
		{
			name:   "wrong method",
			header: signedHeader(t, privateKey, url, http.MethodPost, now),
			url:    url,
			method: http.MethodGet,
			admins: []string{pubkey},
			want:   ErrWrongMethod,
		},
		{
			name:   "stale",
			header: signedHeader(t, privateKey, url, http.MethodGet, now.Add(-2*time.Minute)),
			url:    url,
			method: http.MethodGet,
			admins: []string{pubkey},
			want:   ErrStaleEvent,
		},
		{
			name:   "non admin",
			header: signedHeader(t, otherKey, url, http.MethodGet, now),
			url:    url,
			method: http.MethodGet,
			admins: []string{pubkey},
			want:   ErrNotAdmin,
		},
		{
			name:   "bad signature",
			header: tamperedHeader(t, privateKey, url, http.MethodGet, now),
			url:    url,
			method: http.MethodGet,
			admins: []string{pubkey},
			want:   ErrBadSignature,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authz := NewAuthorizer(tt.admins, time.Minute)
			authz.Now = func() time.Time { return now }
			_, err := authz.VerifyHeader(tt.header, tt.url, tt.method)
			if !errors.Is(err, tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, err)
			}
		})
	}
}

func TestEventFromAuthorization(t *testing.T) {
	privateKey := nostr.GeneratePrivateKey()
	url := testAdminOrigin + AdminAPIStatus
	validHeader := signedHeader(t, privateKey, url, http.MethodGet, time.Unix(1700000000, 0))
	headerPayload := base64.StdEncoding.EncodeToString([]byte(`{"kind":`))

	tests := []struct {
		name    string
		header  string
		wantErr error
	}{
		{
			name:    "valid",
			header:  validHeader,
			wantErr: nil,
		},
		{
			name:    "valid scheme case-insensitive",
			header:  "NoStr " + validHeader[len("Nostr "):],
			wantErr: nil,
		},
		{
			name:    "trimmed valid header",
			header:  "  " + validHeader + "  ",
			wantErr: nil,
		},
		{
			name:    "missing",
			header:  "",
			wantErr: ErrMissingAuthorization,
		},
		{
			name:    "wrong scheme",
			header:  "Bearer token",
			wantErr: ErrWrongScheme,
		},
		{
			name:    "missing token",
			header:  "Nostr",
			wantErr: ErrWrongScheme,
		},
		{
			name:    "bad base64",
			header:  "Nostr !!!",
			wantErr: ErrBadEncoding,
		},
		{
			name:    "malformed json",
			header:  "Nostr " + headerPayload,
			wantErr: ErrBadEncoding,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := EventFromAuthorization(tt.header)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestVerifyRequestUsesAbsoluteRequestURL(t *testing.T) {
	privateKey := nostr.GeneratePrivateKey()
	pubkey, err := nostr.GetPublicKey(privateKey)
	if err != nil {
		t.Fatalf("GetPublicKey returned error: %v", err)
	}
	now := time.Unix(1700000000, 0)
	signedURL := testAdminOrigin + AdminAPIStatus
	header := signedHeader(t, privateKey, signedURL, http.MethodGet, now)

	tests := []struct {
		name           string
		requestTarget  string
		forwardedProto string
		forwardedHost  string
		wantErr        error
	}{
		{
			name:           "accepts forwarded host and proto",
			requestTarget:  testInternalOrigin + AdminAPIStatus,
			forwardedProto: "https",
			forwardedHost:  "wrapster.example",
		},
		{
			name:           "rejects tampered host",
			requestTarget:  testInternalOrigin + AdminAPIStatus,
			forwardedProto: "https",
			forwardedHost:  "attacker.example",
			wantErr:        ErrWrongURL,
		},
	}

	authz := NewAuthorizer([]string{pubkey}, time.Minute)
	authz.Now = func() time.Time { return now }

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.requestTarget, nil)
			req.Header.Set("Authorization", header)
			if tt.forwardedProto != "" {
				req.Header.Set("X-Forwarded-Proto", tt.forwardedProto)
			}
			if tt.forwardedHost != "" {
				req.Header.Set("X-Forwarded-Host", tt.forwardedHost)
			}

			_, err := authz.VerifyRequest(req)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestAbsoluteRequestURL(t *testing.T) {
	tests := []struct {
		name           string
		requestTarget  string
		setTLS         bool
		forwardedProto string
		forwardedHost  string
		want           string
	}{
		{
			name:          "default to request host and http",
			requestTarget: testInternalOrigin + AdminAPIStatus + "?x=1",
			want:          testInternalOrigin + AdminAPIStatus + "?x=1",
		},
		{
			name:           "respect sanitized forwarded proto/host",
			requestTarget:  testInternalOrigin + AdminAPIStatus + "?x=1",
			forwardedProto: "https",
			forwardedHost:  "wrapster.example",
			want:           testAdminOrigin + AdminAPIStatus + "?x=1",
		},
		{
			name:           "ignore invalid forwarded proto",
			requestTarget:  testInternalOrigin + AdminAPIStatus + "?x=1",
			forwardedProto: "ftp",
			want:           testInternalOrigin + AdminAPIStatus + "?x=1",
		},
		{
			name:          "ignore invalid forwarded host",
			requestTarget: testInternalOrigin + AdminAPIStatus + "?x=1",
			forwardedHost: "bad host",
			want:          testInternalOrigin + AdminAPIStatus + "?x=1",
		},
		{
			name:           "trim forwarded host",
			requestTarget:  testInternalOrigin + AdminAPIStatus + "?x=1",
			forwardedHost:  "  wrapster.example  ",
			forwardedProto: "https",
			want:           testAdminOrigin + AdminAPIStatus + "?x=1",
		},
		{
			name:           "allow forwarded host with port",
			requestTarget:  testInternalOrigin + AdminAPIStatus + "/spaces/%2Fslash?x=1",
			forwardedProto: "https",
			forwardedHost:  "wrapster.example:9443",
			want:           "https://wrapster.example:9443/admin/api/status/spaces/%2Fslash?x=1",
		},
		{
			name:           "allow forwarded host with ipv6",
			requestTarget:  testInternalOrigin + AdminAPIStatus + "?x=1",
			forwardedProto: "https",
			forwardedHost:  "[::1]:9443",
			want:           "https://[::1]:9443/admin/api/status?x=1",
		},
		{
			name:          "ignore invalid forwarded host credentials",
			requestTarget: testInternalOrigin + AdminAPIStatus + "?x=1",
			forwardedHost: "user@wrapster.example",
			want:          testInternalOrigin + AdminAPIStatus + "?x=1",
		},
		{
			name:           "prefer direct TLS when no trusted forwarded headers",
			requestTarget:  testInternalOrigin + AdminAPIStatus + "?x=1",
			setTLS:         true,
			forwardedProto: "ftp",
			want:           "https://internal" + AdminAPIStatus + "?x=1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.requestTarget, nil)
			if tt.setTLS {
				req.TLS = &tls.ConnectionState{}
			}
			if tt.forwardedProto != "" {
				req.Header.Set("X-Forwarded-Proto", tt.forwardedProto)
			}
			if tt.forwardedHost != "" {
				req.Header.Set("X-Forwarded-Host", tt.forwardedHost)
			}

			got := AbsoluteRequestURL(req)
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestAbsoluteRequestScheme(t *testing.T) {
	tests := []struct {
		name           string
		setTLS         bool
		forwardedProto string
		want           string
	}{
		{
			name:           "use https when forwarded",
			forwardedProto: "https",
			want:           "https",
		},
		{
			name:           "ignore invalid forwarded protocol",
			forwardedProto: "gopher",
			want:           "http",
		},
		{
			name:           "prefer tls when provided",
			setTLS:         true,
			forwardedProto: "http",
			want:           "https",
		},
		{
			name: "default to http",
			want: "http",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, testInternalOrigin+AdminAPIStatus, nil)
			if tt.setTLS {
				req.TLS = &tls.ConnectionState{}
			}
			if tt.forwardedProto != "" {
				req.Header.Set("X-Forwarded-Proto", tt.forwardedProto)
			}

			got := absoluteRequestScheme(req)
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestSanitizeForwardedProto(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{name: "valid https", input: "https", want: "https"},
		{name: "valid http", input: "http", want: "http"},
		{name: "uppercase", input: "HTTPS", want: "https"},
		{name: "multiple entries", input: "https, http", want: "https"},
		{name: "invalid", input: "gopher", want: ""},
		{name: "empty", input: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeForwardedProto(tt.input)
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestSanitizeForwardedHost(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{name: "valid", input: "wrapster.example", want: "wrapster.example"},
		{name: "valid with spaces", input: "  wrapster.example  ", want: "wrapster.example"},
		{name: "valid with port", input: "wrapster.example:9443", want: "wrapster.example:9443"},
		{name: "first host only", input: "wrapster.example, attacker.example", want: "wrapster.example"},
		{name: "invalid space", input: "bad host", want: ""},
		{name: "invalid path", input: "wrapster.example/api", want: ""},
		{name: "invalid credentials", input: "user@wrapster.example", want: ""},
		{name: "invalid port", input: "wrapster.example:not-a-port", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeForwardedHost(tt.input)
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func signedHeader(t *testing.T, privateKey, url, method string, createdAt time.Time) string {
	t.Helper()
	event := nostr.Event{
		CreatedAt: nostr.Timestamp(createdAt.Unix()),
		Kind:      NIP98EventKind,
		Tags:      nostr.Tags{{"u", url}, {"method", method}},
		Content:   "",
	}
	if err := event.Sign(privateKey); err != nil {
		t.Fatalf("failed to sign event: %v", err)
	}
	return encodeHeader(t, event)
}

func tamperedHeader(t *testing.T, privateKey, url, method string, createdAt time.Time) string {
	t.Helper()
	event := nostr.Event{
		CreatedAt: nostr.Timestamp(createdAt.Unix()),
		Kind:      NIP98EventKind,
		Tags:      nostr.Tags{{"u", url}, {"method", method}},
		Content:   "",
	}
	if err := event.Sign(privateKey); err != nil {
		t.Fatalf("failed to sign event: %v", err)
	}
	event.Content = "tampered"
	return encodeHeader(t, event)
}

func encodeHeader(t *testing.T, event nostr.Event) string {
	t.Helper()
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("failed to marshal event: %v", err)
	}
	return "Nostr " + base64.StdEncoding.EncodeToString(raw)
}
