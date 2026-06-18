# generic-proxy Specification

## Purpose

Allow static browser clients to reach configured public upstreams through
Wrapster while keeping proxy access allowlisted, authenticated, and constrained
to known targets.

## Requirements

### Requirement: Proxy routes map only to configured targets

The generic proxy SHALL forward `/proxy/*` requests only to targets configured
in `conf.toml` or the configured targets file.

#### Scenario: configured host prefix

- **GIVEN** a proxy target is configured for a host route such as `hitchwiki.org`
- **WHEN** a client requests `/proxy/hitchwiki.org/wiki/Paris`
- **THEN** Wrapster forwards the stripped path to that configured upstream
- **AND** the client cannot choose an arbitrary upstream host from the URL

#### Scenario: unknown proxy prefix

- **GIVEN** no configured proxy target matches the requested prefix
- **WHEN** a client requests that `/proxy/*` path
- **THEN** Wrapster does not forward the request to an arbitrary host

### Requirement: Protected proxy requests require configured access rules

Proxy requests SHALL require NIP-98 authorization when the matched target has
configured access rules, and the signed pubkey SHALL pass all required rules for
that target.

#### Scenario: missing proxy authorization

- **GIVEN** a proxy target requires access rules
- **WHEN** the request omits valid NIP-98 authorization
- **THEN** Wrapster rejects the proxy request
- **AND** the configured upstream does not receive the request

#### Scenario: cumulative proxy access rules

- **GIVEN** a target inherits a global access rule and a group-specific additional rule
- **WHEN** a signed proxy request is evaluated
- **THEN** the authenticated pubkey must pass every required rule before forwarding

### Requirement: Proxy responses stay scoped to the proxy path

The proxy SHALL preserve browser usability for configured upstreams while
rewriting or constraining response details that would escape the `/proxy/*`
boundary.

#### Scenario: upstream redirect

- **GIVEN** a configured upstream returns a redirect to its own host
- **WHEN** Wrapster returns the response to the browser
- **THEN** the redirect location is rewritten or constrained to the matching `/proxy/*` route

#### Scenario: upstream cookie

- **GIVEN** a configured upstream returns a cookie
- **WHEN** Wrapster returns the response to the browser
- **THEN** the cookie path is scoped to the matching proxy route
- **AND** the cookie is not broadened to unrelated Wrapster paths

### Requirement: Proxy request size and origin policy are bounded

The generic proxy SHALL enforce configured request body limits and browser
origin policy before forwarding requests to configured upstreams.

#### Scenario: oversized proxy request

- **GIVEN** a request body exceeds the configured proxy maximum body size
- **WHEN** the client sends it to `/proxy/*`
- **THEN** Wrapster rejects the request
- **AND** the upstream does not receive the oversized body

#### Scenario: disallowed browser origin

- **GIVEN** proxy allowed origins are configured
- **WHEN** a browser request comes from an origin outside that list
- **THEN** Wrapster does not grant credentialed proxy access for that origin
