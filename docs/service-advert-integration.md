# Nostr Service Advert Integration Guide

This guide is for applications that want to discover service adverts published
with `kind:31388`, such as Radio Guaka/pleXtr, Wrapster frontends, or simple
community directories.

Read the protocol first: [Nostr Service Advert v1](./nostr-service-advert.md).

## Discovery flow

1. Let users choose relays. A useful default for Trustroots-adjacent apps is:
   - `wss://relay.guaka.org`
   - `wss://nip42.trustroots.org`
2. Query each relay for `kind:31388` and `#t:nostr-service-advert`.
3. Optionally add `#t` filters for service, status, access, or audience.
4. Validate required tags after fetching each event.
5. De-duplicate by `31388:<pubkey>:<d>`.
6. Display public metadata only.

Example filter:

```json
{
  "kinds": [31388],
  "#t": ["nostr-service-advert", "service:jellyfin", "status:active"],
  "limit": 100
}
```

## Why `t` tags are mirrored

Nostr relays support tag filters such as `#e`, `#p`, and `#t` for single-letter
tags. They do not generally support filtering directly on multi-letter tags
like `service` or `status`.

For that reason, the canonical service metadata appears in explicit tags:

```json
["service", "jellyfin"]
["status", "active"]
```

The same values are mirrored into searchable `t` tags:

```json
["t", "service:jellyfin"]
["t", "status:active"]
```

Use `#t` filters to reduce relay results. Use the canonical tags to render and
validate the advert.

## Rendering guidance

Display:

- title
- summary
- service type
- status
- access policy tags
- audience tags, if present
- Markdown description
- contact pubkey and relay hint

Do not display:

- guessed internal URLs
- private service endpoints from out-of-band replies
- access tokens, invite links, or operator-only notes

If an advert includes a URL in `content`, treat it as public text. The v1
convention recommends not publishing private endpoints.

## Requesting access

In v1, `["request", "nip17"]` means access starts with a NIP-17 private direct
message to the contact `p` tag marked `contact`.

Apps can provide a button such as "Request by Nostr DM" that opens the user's
preferred Nostr client or copies the contact npub. The message should be
written by the user and should not send secrets automatically.

Suggested message template:

```text
Hi, I found your <service> advert on Nostr. I would like to request access.

My profile/community context:
<short explanation>
```

The operator decides whether to reply with an invite, a gateway URL, or a
decline. NIP-98 HTTP auth and NIP-42 relay auth can be used by services after
discovery, but they are outside the advert's public metadata contract.

For Wrapster media gateway deployments, the URL shared after approval should be
the public gateway URL, not the WireGuard address, local Plex/Jellyfin URL, or
connector URL. Keep values such as `MEDIA_CONNECTOR_BASE_URL`,
`JELLYFIN_BASE_URL`, `PLEX_BASE_URL`, and media tokens server-side.

## Validation checklist

A client should accept an advert when all of these are true:

- `kind` is `31388`.
- `d` exists and contains `service-type:slug`.
- `title`, `summary`, `service`, `status`, and `request` exist.
- `request` is `nip17`.
- `status` is one of `active`, `paused`, `full`, or `retired`.
- A `p` tag exists with marker `contact`.
- `t` includes `nostr-service-advert`.
- `t` includes `service:<service>`.
- `t` includes `status:<status>`.
- At least one `t` tag starts with `access:`.

Invalid adverts should be ignored in normal user views. Developer tools may
show them with warnings.

## Example client

See [examples/service-directory.html](../examples/service-directory.html)
for a no-build read-only service directory.

## Radio Guaka / pleXtr notes

Radio Guaka is a good consumer and publisher for this convention because it
already has the same basic Nostr shape:

- fixed community relays: `wss://relay.guaka.org` and
  `wss://nip42.trustroots.org`
- browser-side `nostr-tools` usage for querying and publishing
- `#plextr` / `#radioguaka` activity tags for chat, favorites, and now-playing
- Nostr-backed identity plus server-side friend checks for Plex browsing and
  streaming
- same-origin proxying for private upstream services like Music Assistant

Recommended integration path:

- Publish and query service adverts with the same relay list Radio Guaka
  already uses.
- Show `service:plextr`, `service:plex`, `service:music-assistant`, and
  `service:internet-radio` adverts in a discovery view or settings panel.
- Keep private upstream values such as `PLEX_BASE_URL`, `PLEX_TOKEN`,
  `MUSIC_ASSISTANT_BASE_URL`, and `MUSIC_ASSISTANT_TOKEN` out of adverts.
- Use the advert contact `p` tag for NIP-17 access requests.
- After access is granted, configure Radio Guaka through its existing backend
  paths rather than using advert metadata as credentials.

Suggested Radio Guaka queries:

```json
{
  "kinds": [31388],
  "#t": ["nostr-service-advert", "service:plextr"],
  "limit": 100
}
```

```json
{
  "kinds": [31388],
  "#t": ["nostr-service-advert", "service:music-assistant"],
  "limit": 100
}
```
