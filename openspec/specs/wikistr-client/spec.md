# wikistr-client Specification

## Purpose

Provide a static, read-only browser surface for public wiki adverts discovered
on Nostr and accessed through authorized Wrapster proxy routes.

## Requirements

### Requirement: Wikistr discovers public wiki and proxy adverts

Wikistr SHALL discover public `kind:31388` service adverts from configured
relays and pair accepted `service:wiki` adverts with matching
`service:cors-proxy` adverts before building usable wiki configs.

#### Scenario: matching wiki and proxy adverts

- **GIVEN** relays return a valid wiki advert and a compatible proxy advert
- **WHEN** Wikistr builds available wiki configs
- **THEN** it creates a public wiki option using the advert metadata and proxy route

#### Scenario: missing matching proxy advert

- **GIVEN** relays return a valid wiki advert without a compatible proxy advert
- **WHEN** Wikistr builds available wiki configs
- **THEN** it does not create a usable proxied wiki config for that advert

### Requirement: Wikistr routes wiki state through the URL hash

Wikistr SHALL encode the active wiki slug and page title in the URL hash so the
static app can load and share wiki views without a backend route.

#### Scenario: wiki home hash

- **GIVEN** a wiki advert has slug `nomadwiki`
- **WHEN** the user navigates to `#nomadwiki`
- **THEN** Wikistr selects that wiki
- **AND** loads the configured main page for it

#### Scenario: article hash

- **GIVEN** a wiki advert has slug `nomadwiki`
- **WHEN** the user navigates to `#nomadwiki/en/Lisbon`
- **THEN** Wikistr selects that wiki
- **AND** requests the corresponding page through the configured proxy route

### Requirement: Wikistr proxy calls use NIP-98 authorization

Wikistr SHALL use NIP-98 authorization for MediaWiki API, rendered page, and
same-wiki resource requests sent through Wrapster proxy routes.

#### Scenario: proxied API request

- **GIVEN** a selected wiki config has a matching Wrapster proxy endpoint
- **WHEN** Wikistr requests MediaWiki API data
- **THEN** it signs the exact proxied URL with NIP-98
- **AND** sends the request through the proxy endpoint

#### Scenario: same-wiki resource request

- **GIVEN** a rendered wiki page references a same-wiki resource
- **WHEN** Wikistr loads that resource
- **THEN** it normalizes the resource URL through the selected proxy route
- **AND** rejects external or script-like resource URLs

### Requirement: Wikistr ships no private wiki credentials

Wikistr MUST remain a backend-free static app and MUST NOT include private wiki
presets, private source links, access tokens, or embedded credentials.

#### Scenario: static app distribution

- **GIVEN** Wikistr is built or served as static files
- **WHEN** a user opens the app
- **THEN** no backend API is required for the app shell
- **AND** the shipped files do not contain private wiki credentials or private source links

#### Scenario: future private wiki delivery

- **GIVEN** private per-user wiki adverts are added later
- **WHEN** Wikistr receives private wiki metadata
- **THEN** it must arrive through a private mechanism such as NIP-44 or NIP-17
- **AND** it is not hardcoded into the public static app
