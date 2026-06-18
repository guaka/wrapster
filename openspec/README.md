# OpenSpec for Wrapster

OpenSpec captures behavior-level requirements for Wrapster so future agents and
maintainers can plan changes against shared intent instead of only chat history.

## Baseline scope

This initial baseline covers the security/API core:

- `relay-auth`: public WebSocket relay authentication and Trustroots NIP-05 access.
- `media-gateway`: public media API, private connector, and media secret boundaries.
- `service-adverts`: Nostr `kind:31388` service advert publishing and discovery.

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
