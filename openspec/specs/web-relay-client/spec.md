# web-relay-client Specification

## Purpose

Provide a simple browser client for local Wrapster relay development using a
NIP-07 signer for NIP-42 relay auth and profile bootstrap events.

## Requirements

### Requirement: Browser relay client uses NIP-07 signing

The web relay client SHALL use `window.nostr.getPublicKey()` and
`window.nostr.signEvent()` for identity and event signing instead of storing
private keys in the app.

#### Scenario: signer available

- **GIVEN** the browser has a NIP-07 signer
- **WHEN** the user connects the client
- **THEN** the app reads the public key through NIP-07
- **AND** signs relay auth and events through the browser signer

#### Scenario: signer unavailable

- **GIVEN** the browser does not have a NIP-07 signer
- **WHEN** the user tries to sign relay auth or publish an event
- **THEN** the app cannot perform the signed action
- **AND** it does not ask the user to paste a private key into the static page

### Requirement: Relay connection handles NIP-42 challenges

The web relay client SHALL respond to Wrapster NIP-42 `AUTH` challenges with a
properly signed auth event for the configured relay URL.

#### Scenario: relay sends auth challenge

- **GIVEN** the web client is connected to the Wrapster WebSocket relay
- **WHEN** the relay sends an `AUTH` challenge
- **THEN** the client asks the NIP-07 signer to sign a kind `22242` auth event
- **AND** sends the signed auth event back to the relay

#### Scenario: auth rejected

- **GIVEN** the client sends a NIP-42 auth event
- **WHEN** Wrapster rejects the event or the Trustroots access check fails
- **THEN** the client remains unable to read or write protected relay data

### Requirement: Profile bootstrap is limited to self-signed identity events

The web relay client SHALL allow a user to publish a self-signed profile event
containing Trustroots identity metadata so a new key can bootstrap relay access.

#### Scenario: profile bootstrap publish

- **GIVEN** the user has a NIP-07 signer and Trustroots identity metadata
- **WHEN** the user publishes the profile bootstrap event
- **THEN** the event is signed by the user's own pubkey
- **AND** it contains the Trustroots username or NIP-05 metadata needed by Wrapster access checks

#### Scenario: publishing as another pubkey

- **GIVEN** the user is authenticated with one signer pubkey
- **WHEN** the user attempts to publish an event signed by a different pubkey
- **THEN** Wrapster rejects the event
- **AND** the client does not treat that event as successful bootstrap
