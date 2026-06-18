# admin-hub Specification

## Purpose

Expose a same-origin maintainer dashboard for inspecting Wrapster status,
authorization policy, media/FIPS state, and service adverts without making the
Hub a general public configuration API.

## Requirements

### Requirement: Admin API requires NIP-98 admin authorization

The admin API SHALL require valid NIP-98 authorization from a pubkey configured
as an admin before returning protected `/admin/api/*` responses.

#### Scenario: unauthorized admin API request

- **GIVEN** a browser requests a protected admin API route
- **WHEN** the request is missing NIP-98 authorization or is signed by a non-admin pubkey
- **THEN** Wrapster rejects the request
- **AND** the response does not include protected status, policy, auth cache, media, or FIPS details

#### Scenario: authorized admin API request

- **GIVEN** a browser signs an admin API request with a configured admin pubkey
- **WHEN** Wrapster verifies the NIP-98 request within the allowed age window
- **THEN** Wrapster returns the requested admin payload for that route

### Requirement: Hub dashboard remains same-origin and browser-signed

The Hub dashboard SHALL use the browser's NIP-07 signer to create NIP-98 admin
requests and MUST NOT embed admin secrets or long-lived credentials in the page.

#### Scenario: loading the admin page

- **GIVEN** a user opens `/admin`
- **WHEN** the dashboard needs protected data
- **THEN** it asks the browser signer for the current pubkey and signed events
- **AND** it does not rely on embedded bearer tokens or persisted admin credentials

#### Scenario: missing browser signer

- **GIVEN** a user opens `/admin` without a NIP-07 signer
- **WHEN** the dashboard tries to load protected admin data
- **THEN** protected admin API requests cannot be signed
- **AND** the dashboard does not bypass NIP-98 admin authorization

### Requirement: Admin write operations stay narrowly scoped

The admin API SHALL expose only explicit maintainer operations for configured
Hub workflows and MUST NOT become a generic arbitrary write interface.

#### Scenario: FIPS identity save

- **GIVEN** a configured admin signs a request to save a FIPS identity
- **WHEN** the request targets the supported FIPS identity endpoint
- **THEN** Wrapster validates and stores only the requested FIPS identity material through that workflow

#### Scenario: unsupported admin write

- **GIVEN** a configured admin signs a request to an unsupported admin write path
- **WHEN** Wrapster receives the request
- **THEN** Wrapster rejects or ignores the request
- **AND** no unrelated config, relay, proxy, or connector state is changed

### Requirement: Hub-visible FIPS data is public-safe

The Hub SHALL display FIPS identity and peer state in a form useful for setup
and diagnostics while MUST NOT expose stored FIPS `nsec` values or connector
tokens in overview/status payloads.

#### Scenario: FIPS status overview

- **GIVEN** the public side has a saved FIPS identity and configured peer values
- **WHEN** an authorized admin loads the Hub overview
- **THEN** the overview can show public npub, peer npub, peer address, and status
- **AND** the overview does not include raw FIPS `nsec` values or connector tokens

#### Scenario: FIPS peer check

- **GIVEN** an authorized admin asks the Hub to check a configured FIPS peer
- **WHEN** Wrapster runs the peer connectivity check
- **THEN** the result reports status and diagnostic steps
- **AND** the result does not reveal private secret keys or media credentials
