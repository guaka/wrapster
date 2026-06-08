# Nostr Service Advert v1

Nostr Service Advert is an experimental convention for publishing public
metadata about a service instance on Nostr. It is intended for tools such as
Wrapster frontends, Radio Guaka/pleXtr, or simple community directories that
need to discover services like Jellyfin, Plex, CORS proxies, or other
community-hosted resources without exposing private endpoint URLs.

This convention deliberately avoids NIP-99 and NIP-89 in v1. It uses a custom
addressable event so clients can query a single purpose-built surface.

## Event kind

Service adverts use addressable event kind `31388`.

NIP-01 defines kinds in the `30000 <= n < 40000` range as addressable by
`kind`, author pubkey, and `d` tag. Relays may retain only the latest event for
each `kind:pubkey:d` address.

## Service types

The `service` tag is intentionally simple: it names the advertised service or
gateway type in lowercase. Common starting values:

- `jellyfin`
- `plex`
- `plextr`
- `music-assistant`
- `cors-proxy`
- `internet-radio`

Apps may introduce new service types. New values should be short, lowercase,
dash-separated, and mirrored as `["t", "service:<service-type>"]`.

## Event shape

The event `content` field is Markdown. It should describe the service and how
access requests are handled. It must not include private service endpoint URLs
unless the operator intentionally runs a public service.

Machine-readable fields live in tags.

```json
{
  "kind": 31388,
  "content": "A small Jellyfin library for trusted hospitality contacts.\n\nDM me with your Trustroots profile or community context if you would like access.",
  "tags": [
    ["d", "jellyfin:alice-media"],
    ["title", "Alice's Jellyfin"],
    ["summary", "Media streaming for trusted travel community contacts"],
    ["service", "jellyfin"],
    ["status", "active"],
    ["request", "nip17"],
    [
      "p",
      "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      "wss://relay.guaka.org",
      "contact"
    ],
    ["t", "nostr-service-advert"],
    ["t", "service:jellyfin"],
    ["t", "status:active"],
    ["t", "access:request"],
    ["t", "audience:trustroots"]
  ]
}
```

## Required tags

Each valid advert must include:

- `d`: stable identifier in the form `<service-type>:<slug>`.
- `title`: short display title.
- `summary`: one sentence summary.
- `service`: lowercase service type, for example `jellyfin` or `plex`.
- `status`: one controlled status value.
- `request`: access request mechanism. v1 requires `nip17`.
- `p`: contact pubkey with marker `contact`. A relay hint should be present.
- `t`: `nostr-service-advert`.
- `t`: `service:<service-type>`.
- `t`: `status:<status>`.
- `t`: at least one `access:<policy>`.

Clients should ignore adverts that are missing `d`, `service`, `title`,
`summary`, `status`, `request`, or a contact `p` tag. Clients may still show a
warning for malformed adverts during debugging.

## Controlled values

`status` values:

- `active`: the service is accepting requests.
- `paused`: the service exists but is temporarily not accepting requests.
- `full`: capacity is currently full.
- `retired`: the service should no longer be requested.

`access` tag values:

- `request`: access may be requested manually.
- `invite`: access requires an invitation.
- `members`: access is for members of a named community or group.
- `public`: access is publicly available, though endpoint details may still be
  hidden behind a gateway.

`audience` tag values are optional:

- `trustroots`
- `friends`
- `mutuals`
- `community`

Unknown `access:*` and `audience:*` values should be displayed as plain text
instead of rejected. This allows communities to experiment without breaking
older clients.

## Access requests

The public advert describes the service, not the private endpoint. In v1,
request flow is NIP-17 private messaging:

1. The client displays the contact `p` tag as the request target.
2. The user sends a NIP-17 direct message to the contact pubkey.
3. The operator replies with next steps, an invite, or a gateway URL if they
   decide to grant access.

Services may later use NIP-98 for HTTP authentication or NIP-42 for relay
authentication, but the advert itself should not promise that a user is
authorized. It only publishes discovery metadata and a request path.

## Publishing relays

Operators should publish each service advert to both initial community relays:

- `wss://relay.guaka.org`
- `wss://nip42.trustroots.org`

`relay.guaka.org` is the primary relay for the initial deployment. Publishing
to both relays gives clients a simple redundant discovery path while keeping the
advert metadata identical.

## Querying

Clients should query configured relays with:

```json
{
  "kinds": [31388],
  "#t": ["nostr-service-advert"],
  "limit": 100
}
```

To filter for Jellyfin adverts:

```json
{
  "kinds": [31388],
  "#t": ["nostr-service-advert", "service:jellyfin"],
  "limit": 100
}
```

Standard Nostr tag filters work with single-letter tags, so the `service`,
`status`, and `access` values are mirrored as `t` tags for discovery. Clients
must read the canonical multi-letter tags from the event after fetching it.

## De-duplication

Because `31388` is addressable, clients should group adverts by:

```text
31388:<pubkey>:<d>
```

When multiple events share an address, show the newest `created_at`. If two
events have the same timestamp, keep the event with the lexicographically lower
event id, matching NIP-01 replaceable event tie-breaking.

## Sample events

### Jellyfin

```json
{
  "kind": 31388,
  "content": "A small Jellyfin library for trusted hospitality contacts.\n\nDM me with your Trustroots profile or community context if you would like access.",
  "tags": [
    ["d", "jellyfin:alice-media"],
    ["title", "Alice's Jellyfin"],
    ["summary", "Media streaming for trusted travel community contacts"],
    ["service", "jellyfin"],
    ["status", "active"],
    ["request", "nip17"],
    [
      "p",
      "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      "wss://relay.guaka.org",
      "contact"
    ],
    ["t", "nostr-service-advert"],
    ["t", "service:jellyfin"],
    ["t", "status:active"],
    ["t", "access:request"],
    ["t", "audience:trustroots"]
  ]
}
```

### Plex

```json
{
  "kind": 31388,
  "content": "Plex access for people I already know through hospitality networks.\n\nPlease send a short Nostr DM explaining how we know each other.",
  "tags": [
    ["d", "plex:bruno-home"],
    ["title", "Bruno's Plex"],
    ["summary", "Invite-only media access for existing contacts"],
    ["service", "plex"],
    ["status", "full"],
    ["request", "nip17"],
    [
      "p",
      "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
      "wss://relay.guaka.org",
      "contact"
    ],
    ["t", "nostr-service-advert"],
    ["t", "service:plex"],
    ["t", "status:full"],
    ["t", "access:invite"],
    ["t", "audience:friends"]
  ]
}
```

### Radio Guaka / pleXtr

```json
{
  "kind": 31388,
  "content": "A pleXtr gateway for friend-gated Plex music playback.\n\nRequest access by Nostr DM. The advert does not publish the Plex base URL or token.",
  "tags": [
    ["d", "plextr:guaka-music"],
    ["title", "Guaka's pleXtr"],
    ["summary", "Friend-gated Plex music browsing and playback through pleXtr"],
    ["service", "plextr"],
    ["status", "active"],
    ["request", "nip17"],
    [
      "p",
      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "wss://relay.guaka.org",
      "contact"
    ],
    ["t", "nostr-service-advert"],
    ["t", "service:plextr"],
    ["t", "status:active"],
    ["t", "access:members"],
    ["t", "audience:friends"],
    ["t", "audience:trustroots"]
  ]
}
```

### Music Assistant

```json
{
  "kind": 31388,
  "content": "A Music Assistant proxy for experimenting with shared music search and playback.\n\nRequest access by Nostr DM. The Music Assistant base URL and bearer token stay server-side.",
  "tags": [
    ["d", "music-assistant:living-room"],
    ["title", "Living Room Music Assistant"],
    ["summary", "Request-gated Music Assistant proxy for shared listening experiments"],
    ["service", "music-assistant"],
    ["status", "paused"],
    ["request", "nip17"],
    [
      "p",
      "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
      "wss://relay.guaka.org",
      "contact"
    ],
    ["t", "nostr-service-advert"],
    ["t", "service:music-assistant"],
    ["t", "status:paused"],
    ["t", "access:request"],
    ["t", "audience:community"]
  ]
}
```

### CORS proxy

```json
{
  "kind": 31388,
  "content": "A Wrapster CORS proxy for static community wiki clients.\n\nRequest the public proxy URL by Nostr DM. The proxy is allowlisted to configured upstreams and stores nothing.",
  "tags": [
    ["d", "cors-proxy:community-wikis"],
    ["title", "Community Wiki CORS Proxy"],
    ["summary", "Allowlisted browser proxy for Trustroots and community wiki calls"],
    ["service", "cors-proxy"],
    ["status", "active"],
    ["request", "nip17"],
    [
      "p",
      "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
      "wss://relay.guaka.org",
      "contact"
    ],
    ["t", "nostr-service-advert"],
    ["t", "service:cors-proxy"],
    ["t", "status:active"],
    ["t", "access:request"],
    ["t", "audience:community"],
    ["t", "audience:trustroots"]
  ]
}
```

## References

- [NIP-01: Basic protocol flow description](https://nips.nostr.com/1)
- [NIP-17: Private Direct Messages](https://nips.nostr.com/17)
- [NIP-42: Authentication of clients to relays](https://nips.nostr.com/42)
- [NIP-98: HTTP Auth](https://nips.nostr.com/98)
