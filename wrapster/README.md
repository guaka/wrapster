# wrapster

`wrapster` is the public service. It accepts public WebSocket clients, requires
NIP-42 authentication before reads and writes, checks that the authenticated
pubkey is linked to a Trustroots username via NIP-05, and then proxies allowed
relay traffic to strfry. It also serves a same-port read-only admin dashboard at
`/admin`, NIP-98 protected media gateway routes at `/media/api/*`, and a generic
allowlisted browser proxy under `/proxy/*`.

## Installation

### Docker image

From the repo root, build the combined service image:

```sh
docker build -f Dockerfile.wrapster -t wrapster .
```

The image contains both binaries:

- `wrapster`, the public relay, admin, proxy, and media gateway service.
- `wrapster-connector`, the private Jellyfin/Plex connector.

The default container command starts `wrapster`. Start the connector by
overriding the command with `wrapster-connector`.

### Source build

Install Go 1.25 or newer plus a C toolchain for SQLite CGO support. Then build
from this directory:

```sh
go build ./cmd/wrapster
go build ./cmd/wrapster-connector
```

The resulting binaries read configuration from environment variables and, for
proxy/media access rules, an optional `conf.toml`.

## Run locally

From the repo root:

```sh
docker compose up --build wrapster strfry
```

The compose stack starts:

- `wrapster` on `ws://localhost:5542`
- `strfry` as an internal upstream on `ws://strfry:5543`
- `strfry-data`, a clean unseeded named volume that persists across restarts
- `wrapster-data`, a named volume for the SQLite auth cache

To reset the local strfry database:

```sh
docker compose down -v
```

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `LISTEN_ADDR` | `:5542` | HTTP/WebSocket listen address. |
| `PUBLIC_RELAY_URL` | `ws://localhost:5542` | Relay URL expected in NIP-42 auth events. |
| `UPSTREAM_RELAY_URL` | `ws://strfry:5543` | Private upstream relay URL. |
| `RELAY_UPSTREAM_TIMEOUT` | `UPSTREAM_TIMEOUT` or `5s` | Relay upstream lookup timeout. |
| `AUTH_CACHE_PATH` | `./auth-cache.db` | SQLite auth cache path. |
| `TRUSTROOTS_NIP05_BASE_URL` | `https://www.trustroots.org/.well-known/nostr.json` | NIP-05 endpoint. |
| `AUTH_CACHE_TTL` | `24h` | Successful authorization cache lifetime. |
| `AUTH_EVENT_MAX_AGE` | `10m` | Allowed timestamp skew for NIP-42 auth events. |
| `ADMIN_PUBKEYS` | empty | Comma-separated hex or `npub...` pubkeys allowed to use `/admin/api/*`. |
| `ADMIN_AUTH_MAX_AGE` | `60s` | Allowed timestamp skew for NIP-98 admin requests. |
| `MEDIA_CONNECTOR_BASE_URL` | empty | Private connector URL, for example `http://10.77.0.2:22000`. |
| `MEDIA_CONNECTOR_TOKEN` | empty | Optional bearer token sent from the gateway to the connector. |
| `MEDIA_TRANSPORT_LABEL` | `private` | Label returned by `/media/api/status`, for example `fips` in the FIPS stack. |
| `MEDIA_GRANT_PUBKEYS` | empty | Comma-separated hex pubkeys allowed to use `/media/api/*`. |
| `MEDIA_AUTH_MAX_AGE` | `60s` | Allowed timestamp skew for NIP-98 media requests. |
| `MEDIA_HTTP_TIMEOUT` | `30s` | Gateway timeout for connector calls. |
| `TARGETS_CONFIG_PATH` | empty | Optional path to a TOML proxy targets file. If empty, `wrapster` searches upward for `conf.toml`. |
| `ALLOWED_ORIGINS` | empty | Comma-separated browser origins allowed to use proxy credentials. Empty reflects any browser origin. |
| `PROXY_UPSTREAM_TIMEOUT` | `UPSTREAM_TIMEOUT` or `15s` | Generic proxy per-request upstream timeout. |
| `PROXY_MAX_BODY_BYTES` | `MAX_BODY_BYTES` or `10485760` | Generic proxy maximum request body size. |

Local `conf.toml` can also define admin `npub...` values, named access rules,
the proxy access rule, and per-service media access rules. Environment
`ADMIN_PUBKEYS` and `MEDIA_GRANT_PUBKEYS` are still supported for static grants.

For the initial production deployment at `relay.guaka.org`, set:

```sh
PUBLIC_RELAY_URL=wss://relay.guaka.org
```

## FIPS media gateway

The FIPS pilot is the preferred split-stack media gateway path when the public
Wrapster service runs on a VPS and Jellyfin/Plex stay on a private home or NAS
network. It uses a FIPS sidecar on each side:

- `compose.fips-public.yml` runs `fips-public`, `wrapster`, and `strfry` on the
  public VPS.
- `compose.fips-home.yml` runs `fips-home` and `wrapster-connector` on the
  home/NAS side.

The FIPS sidecar service uses the published image name
`ghcr.io/guaka/wrapster-fips-sidecar:v0.3.0` when available, and includes a
local build recipe for first boot or fallback. The entrypoint is bind-mounted
from the repo so setup-script changes do not require rebuilding the Rust FIPS
binary.

In the FIPS stack, the public gateway uses:

```sh
MEDIA_CONNECTOR_BASE_URL=http://home-media.fips:22000
MEDIA_TRANSPORT_LABEL=fips
```

The home connector listens privately on `:22000` and serves the LAN-only setup
UI on `:22001`. The setup UI can generate a local FIPS `nsec`, test Jellyfin or
Plex settings, and save connector settings to the connector data volume.

See [../docs/fips-media-pilot.md](../docs/fips-media-pilot.md) for the
two-host installation checklist, required environment variables, exposed ports,
and verification commands.

## Private connector media gateway

The media gateway has two sides:

- `wrapster` on the public VPS.
- `wrapster-connector` on the home network, reachable only through FIPS or
  another private transport.

Suggested private transport addressing:

- VPS peer: `10.77.0.1`
- home peer: `10.77.0.2`
- connector listen address: `10.77.0.2:22000`
- gateway connector URL: `http://10.77.0.2:22000`

Public media routes:

- `GET /media/api/status`
- `GET /media/api/services/jellyfin/search?q=<query>`
- `GET /media/api/services/jellyfin/stream/<stream_id>`
- `GET /media/api/services/plex/search?q=<query>`
- `GET /media/api/services/plex/stream/<stream_id>`

Every public media request must be signed with NIP-98. Access can come from
the legacy static `MEDIA_GRANT_PUBKEYS` list or from a configured media service
rule, such as a NIP-02 owner-follow rule for Jellyfin and Plex. The gateway
only forwards the fixed routes above and does not expose arbitrary connector or
LAN paths.

Connector configuration:

| Variable | Default | Description |
| --- | --- | --- |
| `CONNECTOR_LISTEN_ADDR` | `:22000` | Connector listen address. In production, bind to the private transport address. |
| `CONNECTOR_SETUP_LISTEN_ADDR` | `:22001` | LAN setup UI listen address. |
| `CONNECTOR_CONFIG_PATH` | `/data/connector-config.json` | Saved connector media settings path. |
| `CONNECTOR_ALLOWED_CIDRS` | `10.77.0.1/32,127.0.0.1/32,::1/128` | Remote addresses allowed to call connector APIs. |
| `CONNECTOR_SHARED_TOKEN` | empty | Optional bearer token required from the public gateway. |
| `CONNECTOR_ADMIN_PUBKEYS` | empty | Comma-separated hex or `npub...` pubkeys allowed to save/test setup UI settings. |
| `CONNECTOR_SETUP_AUTH_MAX_AGE` | `60s` | Allowed timestamp skew for NIP-98 setup UI write/test requests. |
| `JELLYFIN_BASE_URL` | empty | Local Jellyfin URL, for example `http://192.168.1.20:8096`. |
| `JELLYFIN_API_KEY` | empty | Jellyfin API key used only by the connector. |
| `PLEX_BASE_URL` | empty | Local Plex URL, for example `http://192.168.1.20:32400`. |
| `PLEX_TOKEN` | empty | Plex token used only by the connector. |
| `CONNECTOR_HTTP_TIMEOUT` | `30s` | Connector timeout for local Plex/Jellyfin calls. |

Example home connector:

```sh
CONNECTOR_LISTEN_ADDR=10.77.0.2:22000 \
CONNECTOR_SHARED_TOKEN=change-me \
JELLYFIN_BASE_URL=http://192.168.1.20:8096 \
JELLYFIN_API_KEY=... \
PLEX_BASE_URL=http://192.168.1.20:32400 \
PLEX_TOKEN=... \
wrapster-connector
```

Example public gateway media settings:

```sh
MEDIA_CONNECTOR_BASE_URL=http://10.77.0.2:22000
MEDIA_CONNECTOR_TOKEN=change-me
MEDIA_GRANT_PUBKEYS=<comma-separated-nostr-pubkeys>
```

## Generic proxy

The generic proxy stores nothing and only forwards requests to targets listed in
local `conf.toml` or the file named by `TARGETS_CONFIG_PATH`.

Routes live under `/proxy/*`:

| Route | Upstream |
| --- | --- |
| `/proxy/trustroots/*` | `trustroots` target |
| `/proxy/hitchwiki.org/*` | `hitchwiki.org` target |
| `/proxy/nomadwiki.org/*` | `nomadwiki.org` target |
| `/proxy/wiki.trustroots.org/*` | `wiki.trustroots.org` target |
| `/proxy/*` | `trustroots` fallback when that target is configured |

The target prefix is stripped before forwarding. For example,
`/proxy/hitchwiki.org/wiki/Paris` is forwarded to
`https://hitchwiki.org/wiki/Paris`.

The targets file can be a simple URL list. Route prefixes are derived from
hostnames, with `https://www.trustroots.org` exposed as `/proxy/trustroots/*`:

```toml
targets = [
  "https://www.trustroots.org",
  "https://hitchwiki.org",
  "https://nomadwiki.org",
  "https://wiki.trustroots.org",
]
```

The friendly config can define one global `access_rule` that applies to every
configured proxy and media target. Groups may add cumulative requirements with
`additional_access_rule`, so the media services below require both Trustroots
NIP-05 and the owner's Nostr follow list:

```toml
owner_npub = "npub1..."
additional_relays = ["wss://nip42.trustroots.org"]
access_rule = {"nip05_domain": "trustroots.org"}

[proxy_group.hospex]
urls = ["https://www.trustroots.org", "https://hitchwiki.org"]

[proxy_group.media]
urls = ["fips_jellyfin", "fips_plex"]
additional_access_rule = ["nostr_follow"]
```

Protected requests must include a NIP-98 `Authorization: Nostr ...` header and
the authenticated pubkey must pass all required rules for the target.

The older `[targets]` table format is also supported when a deployment needs
explicit route prefixes.

## Admin dashboard

Open `http://localhost:5542/admin` in a browser with a NIP-07 extension. The UI
uses `window.nostr.getPublicKey()` and `window.nostr.signEvent()` to make
NIP-98 signed requests to:

- `GET /admin/api/status`
- `GET /admin/api/auth-cache`
- `GET /admin/api/policy`

Only pubkeys listed in `ADMIN_PUBKEYS` can read those endpoints. Values may be
hex public keys or `npub...` public keys. The dashboard does not expose write
operations.

## Service directory

Open `http://localhost:5542/examples/service-directory.html` to browse
published service adverts through the running Wrapster server.

## Behavior

- The relay sends `["AUTH", "<challenge>"]` after a WebSocket connection opens.
- `REQ` and `EVENT` are rejected until the client sends a valid NIP-42 auth
  event.
- The auth event must be kind `22242`, signed by the pubkey, include the
  connection challenge, and include the configured public relay URL.
- The authenticated pubkey must have a profile event (kind `10390` or kind `0`)
  that declares a Trustroots username. The relay checks the configured upstream
  plus `wss://relay.trustroots.org` and `wss://relay.nomadwiki.org`. The
  username must resolve through Trustroots NIP-05 to the same pubkey.
- Authenticated users may only publish events signed by their authenticated
  pubkey.

The upstream strfry relay should not be exposed publicly in production. Public
clients should connect only to `wrapster`.
