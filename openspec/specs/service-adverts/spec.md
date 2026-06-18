# service-adverts Specification

## Purpose

Define how Wrapster-adjacent apps publish and consume public Nostr service
adverts without turning discovery metadata into credentials or private network
configuration.

## Requirements

### Requirement: Service adverts use kind 31388 metadata

Service adverts SHALL use addressable Nostr event kind `31388` with required
metadata tags for stable identity, display, service type, status, request method,
contact, and searchable `t` tags.

#### Scenario: valid service advert

- **GIVEN** an event is intended as a public service advert
- **WHEN** the event is published
- **THEN** it uses kind `31388`
- **AND** it includes `d`, `title`, `summary`, `service`, `status`, `request`, contact `p`, and `t` tags for `nostr-service-advert`, service, status, and access metadata

#### Scenario: searchable mirrored tags

- **GIVEN** a service advert includes canonical `service` and `status` tags
- **WHEN** the advert is published
- **THEN** it also includes matching searchable `t` tags such as `service:jellyfin` and `status:active`

### Requirement: Clients validate and de-duplicate adverts

Clients SHALL ignore malformed adverts in normal user views and SHALL
de-duplicate addressable adverts by `31388:<pubkey>:<d>` using the newest valid
event as the current advert.

#### Scenario: malformed advert

- **GIVEN** a relay returns a kind `31388` event
- **WHEN** the event lacks required advert metadata or uses unsupported required values
- **THEN** the client does not show it in normal user-facing advert lists

#### Scenario: duplicate addressable advert

- **GIVEN** a relay returns multiple valid adverts with the same `pubkey` and `d` tag
- **WHEN** the client builds the service list
- **THEN** the client keeps the newest advert for that address
- **AND** the client avoids showing duplicate entries for the same service address

### Requirement: Advert metadata stays public-safe

Service adverts MUST NOT publish private connector URLs, LAN-only media URLs,
access tokens, invite secrets, API keys, FIPS `nsec` values, or operator-only
notes. Private access details SHALL be exchanged only after approval through an
out-of-band private channel such as NIP-17 direct messages.

#### Scenario: access request metadata

- **GIVEN** an advert describes a service that requires approval
- **WHEN** a client renders the request flow
- **THEN** it uses the advert contact and request method to help the user start a private access request
- **AND** it does not infer, display, or send private service credentials from the advert

#### Scenario: public endpoint metadata

- **GIVEN** an advert includes an endpoint-like value
- **WHEN** a client displays or uses that value
- **THEN** the value is treated only as public metadata for that service type
- **AND** it is not treated as proof of private connector, media-server, or credential access
