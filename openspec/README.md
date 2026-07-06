# OpenSpec for Wrapster

OpenSpec captures behavior-level requirements for Wrapster so future agents and
maintainers can plan changes against shared intent instead of only chat history.

## Current scope

Specs are grouped into the server-side security core and the client apps that
consume it.

### Security core

Public boundaries, authentication/authorization, and secret handling:

- `relay-auth`: public WebSocket relay authentication and Trustroots NIP-05 access.
- `media-gateway`: public media API, private connector, and media secret boundaries.
- `service-adverts`: Nostr `kind:31388` service advert publishing and discovery.
- `admin-hub`: same-origin maintainer dashboard and NIP-98 admin API.
- `fips-deployment`: public/home FIPS deployment contract and peer identity exchange.
- `generic-proxy`: allowlisted `/proxy/*` access to configured upstreams.

### Client apps

User-facing surfaces that authenticate against the security core:

- `web-relay-client`: browser relay dev client using a NIP-07 signer.
- `wikistr-client`: legacy GitHub Pages redirect to Nostroots wikistr.
- `ios-client`: native iOS media client for authorized Jellyfin/Plex gateways.

Add more specs when a change needs them; do not backfill every internal package
just for completeness.

## When to use OpenSpec

Create an OpenSpec change before implementing behavior changes that affect:

- public HTTP/WebSocket APIs
- authentication, authorization, or secret handling
- configuration variables or deployment behavior
- FIPS/public-home networking contracts
- user-visible app or admin workflows

Small test-only edits, typo fixes, and purely internal refactors can skip an
OpenSpec change unless they carry behavior risk.

## Workflow

Use the OpenSpec CLI from the repo root:

```sh
openspec validate --specs --strict
```

For future behavior changes, create a change proposal with the OpenSpec workflow,
review the requirements delta, implement the work, validate the specs, then
archive the change so `openspec/specs/` stays current.

## Security note

Specs should explicitly state negative requirements for sensitive areas. In this
repo, private connector URLs, media server URLs, media tokens, connector tokens,
and FIPS `nsec` values must remain server-side/private unless a spec explicitly
defines a safe public representation.
