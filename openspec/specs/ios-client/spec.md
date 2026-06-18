# ios-client Specification

## Purpose

Provide a native iOS media client for authorized Wrapster Jellyfin and Plex
gateways without turning the app into a browser shell or file-sharing client.

## Requirements

### Requirement: iOS client stores and uses a local Nostr identity

The iOS client SHALL generate or import a Nostr private key and use the iOS
Keychain to persist it for signing Wrapster media requests.

#### Scenario: generated key

- **GIVEN** the iOS client has no configured Nostr key
- **WHEN** the user creates a local identity
- **THEN** the app stores the private key in the iOS Keychain
- **AND** future media requests can be signed with the corresponding pubkey

#### Scenario: imported key

- **GIVEN** the user imports an existing Nostr private key
- **WHEN** the key is accepted by the app
- **THEN** the app persists it in the iOS Keychain
- **AND** uses it for NIP-98 media authorization

### Requirement: iOS media requests use NIP-98

The iOS client SHALL sign Wrapster `/media/api/*` requests with NIP-98 using the
configured local Nostr identity.

#### Scenario: status check

- **GIVEN** the user has configured a Wrapster gateway URL and local Nostr key
- **WHEN** the app checks media connector status
- **THEN** it sends a NIP-98 signed request to the gateway status endpoint

#### Scenario: search request

- **GIVEN** the user enters a media search query
- **WHEN** the app searches Jellyfin or Plex through Wrapster
- **THEN** it signs the request with NIP-98
- **AND** it sends the query only to the configured Wrapster gateway route

### Requirement: iOS playback uses gateway stream URLs

The iOS client SHALL play authorized stream URLs returned through Wrapster media
routes and MUST NOT expose direct private Jellyfin or Plex URLs as the playback
contract.

#### Scenario: media result playback

- **GIVEN** a search result includes a Wrapster stream identifier or URL
- **WHEN** the user starts playback
- **THEN** the app plays through the Wrapster gateway stream route
- **AND** it does not require direct LAN access to the private media server

#### Scenario: out-of-scope file sharing

- **GIVEN** the user is using the iOS client
- **WHEN** the app presents media features
- **THEN** it does not offer torrent, seeding, download, or arbitrary file-sharing workflows

### Requirement: iOS service discovery remains request-oriented

The iOS client SHALL discover Plex and Jellyfin service adverts as public
metadata and help the user copy or prepare an access request instead of treating
adverts as credentials.

#### Scenario: discovered media advert

- **GIVEN** the app loads a valid Plex or Jellyfin service advert
- **WHEN** the user views the advert
- **THEN** the app can show public advert metadata and contact information
- **AND** it does not assume the user has stream access from the advert alone

#### Scenario: access request

- **GIVEN** a discovered advert declares a private request method
- **WHEN** the user wants access
- **THEN** the app helps prepare an access request message
- **AND** it does not send media credentials automatically
