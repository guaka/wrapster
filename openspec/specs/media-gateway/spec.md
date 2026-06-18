# media-gateway Specification

## Purpose

Allow authorized users to search and stream configured Jellyfin or Plex media
through Wrapster without exposing the private connector, LAN media servers, or
media credentials.

## Requirements

### Requirement: Public media API requires signed authorization

Wrapster SHALL require NIP-98 authorization for public `/media/api/*` requests
and SHALL allow access only through configured media grants or configured
service access rules.

#### Scenario: unsigned media request

- **GIVEN** the media gateway is configured
- **WHEN** a client calls `/media/api/status` or `/media/api/services/*` without valid NIP-98 authorization
- **THEN** Wrapster rejects the request
- **AND** Wrapster does not contact the private connector

#### Scenario: authorized service request

- **GIVEN** a client signs a valid NIP-98 request with an authorized pubkey
- **WHEN** the client calls a supported Jellyfin or Plex search, stream, status, or random-song route
- **THEN** Wrapster forwards only the corresponding fixed connector route
- **AND** Wrapster returns the connector result through the public media API

### Requirement: Gateway exposes only fixed connector capabilities

The public media gateway SHALL proxy only the supported status, search, stream,
and random-song connector capabilities and MUST NOT expose arbitrary connector
paths or private LAN paths.

#### Scenario: unsupported public media path

- **GIVEN** a client is authorized for media access
- **WHEN** the client requests an unsupported `/media/api/*` path
- **THEN** Wrapster returns not found
- **AND** no arbitrary connector or LAN URL is requested

#### Scenario: stream route forwarding

- **GIVEN** a client is authorized for a supported media service
- **WHEN** the client requests `/media/api/services/{service}/stream/{stream_id}`
- **THEN** Wrapster forwards the escaped stream identifier to the matching connector stream route
- **AND** Wrapster does not allow the stream identifier to choose a different connector path

### Requirement: Private connector protects media-server access

The connector SHALL accept requests only from allowed network addresses and, when
configured, only with the shared connector token before contacting Jellyfin or
Plex.

#### Scenario: disallowed connector caller

- **GIVEN** a request reaches the connector from a remote address outside the allowed CIDRs
- **WHEN** the caller requests any connector API path
- **THEN** the connector rejects the request
- **AND** the connector does not contact Jellyfin or Plex

#### Scenario: missing connector token

- **GIVEN** the connector has a shared token configured
- **WHEN** a caller omits the required token
- **THEN** the connector rejects the request
- **AND** the connector does not contact Jellyfin or Plex

### Requirement: Media secrets remain private

Wrapster and the connector MUST NOT expose private connector base URLs, connector
tokens, Jellyfin or Plex base URLs, Jellyfin API keys, Plex tokens, LAN-only
addresses, or FIPS `nsec` values in public media responses, service adverts, or
browser-visible configuration.

#### Scenario: public media status response

- **GIVEN** the media gateway can reach a configured connector
- **WHEN** an authorized client requests `/media/api/status`
- **THEN** the response reports public status such as transport label and service configuration state
- **AND** the response omits private URLs, tokens, API keys, and FIPS secret keys

#### Scenario: media service advert

- **GIVEN** Wrapster or a client publishes a service advert for media access
- **WHEN** the advert describes Jellyfin, Plex, or a media gateway
- **THEN** the advert may describe public access metadata
- **AND** the advert MUST NOT include private connector URLs, LAN media URLs, tokens, API keys, or FIPS `nsec` values
