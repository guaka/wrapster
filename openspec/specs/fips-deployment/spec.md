# fips-deployment Specification

## Purpose

Define the public/home FIPS deployment contract that connects a public Wrapster
gateway to a private home connector without exposing Jellyfin, Plex, or FIPS
private keys.

## Requirements

### Requirement: Public and home stacks have distinct roles

The FIPS deployment SHALL keep the public Wrapster/strfry gateway role separate
from the home/NAS connector role that can reach LAN media servers.

#### Scenario: public stack startup

- **GIVEN** the public FIPS compose stack is deployed
- **WHEN** the stack starts
- **THEN** it runs the public Wrapster service, strfry, and public FIPS sidecar
- **AND** it routes media gateway traffic to the home connector through the FIPS mesh

#### Scenario: home stack startup

- **GIVEN** the home FIPS compose stack is deployed
- **WHEN** the stack starts
- **THEN** it runs the home FIPS sidecar and private connector
- **AND** it does not expose Jellyfin or Plex directly to the public internet

### Requirement: Peer identities must be exchanged before authenticated FIPS traffic

Each FIPS side SHALL know its own persistent `nsec` identity and the peer's
public `npub` before relying on the mesh for authenticated gateway-to-connector
traffic.

#### Scenario: generated identity

- **GIVEN** a side starts without a configured FIPS `nsec`
- **WHEN** an authorized setup flow generates and saves an identity
- **THEN** the side stores the secret in its persistent FIPS data volume
- **AND** the UI shows the public `npub` needed by the opposite side

#### Scenario: peer mismatch

- **GIVEN** a side has a peer `npub` configured
- **WHEN** the remote FIPS peer presents a different identity
- **THEN** the mesh does not treat the peer as the configured trusted peer
- **AND** media traffic does not flow through that unauthenticated peer session

### Requirement: Outbound-only home deployments are supported

The FIPS deployment SHALL support the default NAS-friendly mode where the home
side dials the public side and the public side SHALL NOT require a routable home
address.

#### Scenario: public side waits for home dial-out

- **GIVEN** the public stack has `FIPS_HOME_NPUB` configured and no home address
- **WHEN** the public side starts
- **THEN** it waits for the home side to open the FIPS session
- **AND** Wrapster can use the configured home alias once the session is established

#### Scenario: home side dials public peer

- **GIVEN** the home stack has the public peer npub and public address configured
- **WHEN** the home side starts
- **THEN** it opens an outbound FIPS connection to the public side
- **AND** no home router port forwarding is required for that default mode

### Requirement: FIPS deployment secrets remain private

FIPS deployment secrets MUST remain private. FIPS `nsec` values, connector
shared tokens, media API keys, and LAN media URLs MUST NOT be committed,
published in service adverts, or returned in public or browser-visible
deployment status.

#### Scenario: deployment documentation and examples

- **GIVEN** a maintainer follows FIPS deployment docs or examples
- **WHEN** the docs describe required values
- **THEN** secret values are represented as placeholders
- **AND** maintainers are told not to commit deployment `.env` files

#### Scenario: setup status display

- **GIVEN** a setup UI or Hub page displays FIPS and connector state
- **WHEN** it reports saved or configured values
- **THEN** it shows only public or redacted state
- **AND** it omits raw FIPS `nsec`, connector token, Jellyfin API key, Plex token, and LAN media URLs
