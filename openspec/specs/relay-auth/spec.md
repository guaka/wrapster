# relay-auth Specification

## Purpose

Protect the public Wrapster relay so only authenticated Trustroots-linked Nostr
pubkeys can read from or write to the private upstream relay.

## Requirements

### Requirement: WebSocket clients authenticate with NIP-42

The relay SHALL send a NIP-42 authentication challenge to each WebSocket client
and SHALL reject relay `REQ` and `EVENT` messages until the client has provided
a valid auth event for the configured public relay URL and challenge.

#### Scenario: unauthenticated relay command

- **GIVEN** a WebSocket client has connected to Wrapster
- **WHEN** the client sends `REQ` or `EVENT` before successful NIP-42 auth
- **THEN** Wrapster rejects the command
- **AND** Wrapster does not forward it to the upstream relay

#### Scenario: valid NIP-42 auth

- **GIVEN** a WebSocket client receives a Wrapster auth challenge
- **WHEN** the client signs a valid NIP-42 auth event for that challenge and relay URL
- **THEN** Wrapster records the authenticated pubkey for the connection
- **AND** subsequent allowed relay commands can be evaluated for Trustroots access

### Requirement: Relay access requires Trustroots NIP-05 ownership

Wrapster SHALL authorize authenticated relay use only when the authenticated
pubkey resolves to the same Trustroots identity through NIP-05 verification.

#### Scenario: matching Trustroots identity

- **GIVEN** a client has completed NIP-42 auth
- **WHEN** Wrapster finds a Trustroots username for the pubkey and NIP-05 resolves to that same pubkey
- **THEN** Wrapster permits relay reads and writes for that connection

#### Scenario: missing or mismatched Trustroots identity

- **GIVEN** a client has completed NIP-42 auth
- **WHEN** Wrapster cannot find a Trustroots username or NIP-05 resolves to a different pubkey
- **THEN** Wrapster rejects relay reads and writes for that connection
- **AND** Wrapster does not forward rejected commands to the upstream relay

### Requirement: Upstream relay remains private

Wrapster SHALL proxy allowed relay traffic to the configured upstream relay and
MUST NOT expose the upstream strfry service directly through public HTTP routes.

#### Scenario: public relay entrypoint

- **GIVEN** a public client connects to the Wrapster service
- **WHEN** the client uses WebSocket relay traffic
- **THEN** Wrapster is the public relay boundary
- **AND** the private upstream relay is reached only by Wrapster

#### Scenario: relay metadata request

- **GIVEN** a public client requests relay metadata with `Accept: application/nostr+json`
- **WHEN** Wrapster returns NIP-11 metadata
- **THEN** the response describes the Wrapster relay auth requirements
- **AND** the response does not reveal private upstream connection details
