# FIPS media gateway pilot

This is the FIPS-first path for the optional Wrapster media gateway. It keeps
the default Compose stack untouched and adds two separate stacks:

- `compose.fips-public.yml` for the public VPS running `wrapster` and `strfry`.
- `compose.fips-home.yml` for the home/NAS side running `wrapster-connector`.

The home stack includes a LAN setup UI for connecting Jellyfin and Plex without
editing environment variables after first boot.

## Installation prerequisites

Install Docker with the Compose plugin on both hosts. Each FIPS sidecar needs:

- access to `/dev/net/tun`
- `NET_ADMIN` capability
- IPv6 enabled in the container network
- inbound peer transport on `8443/tcp` for the public VPS
- outbound TCP access from the home/NAS side to the public VPS FIPS address
- outbound access to build or pull the FIPS sidecar image

The compose files include both a published image name and a local build recipe:

```sh
ghcr.io/guaka/wrapster-fips-sidecar:v0.3.0
```

If the image is already available, Compose can use it.
If GHCR denies access in Portainer, override `FIPS_SIDECAR_IMAGE` to a resolvable
local/public image and/or add GHCR credentials in Portainer.

If needed, you can still compile locally with:

```sh
FIPS_REF=v0.3.0 docker compose -f compose.fips-public.yml up -d --build
```

For setup-script changes you can use the `compose.*.build.yml` overrides to avoid
editing this published stack.

Create a deployment `.env` file on each host or export the values in the
shell before running Compose. Do not commit the `.env` files; they include
persistent FIPS private keys and connector tokens.

## Required values

Each stack needs its own persistent `nsec`, and each side needs the other
side's `npub` before the FIPS mesh can authenticate as peers.

To start, `FIPS_PUBLIC_NSEC` or `FIPS_HOME_NSEC` can be empty; the sidecar stays
in setup mode so the public Hub UI or home/NAS setup UI can generate and save a
local identity into the shared FIPS data volume. The UI shows the generated
sidecar `npub`; exchange those public values between the two hosts. Existing
`FIPS_PUBLIC_NSEC` or `FIPS_HOME_NSEC` env values still work as overrides.

For the default outbound-only NAS setup, leave `FIPS_HOME_ADDR` empty on the
public VPS. The public side registers the NAS peer identity and waits for the
NAS to open its outbound FIPS session. Set `FIPS_PUBLIC_ADDR` on the NAS to the
public side, for example `relay.guaka.org:8443`.

Public VPS environment:

```sh
FIPS_PUBLIC_NSEC=nsec1... # optional override; the Hub UI can save this
FIPS_HOME_NPUB=npub1...
FIPS_HOME_ADDR=                  # empty for outbound-only NAS
FIPS_HOME_ALIAS=home-media
MEDIA_CONNECTOR_TOKEN=change-me
MEDIA_GRANT_PUBKEYS=<comma-separated-user-pubkeys>
ADMIN_PUBKEYS=<comma-separated-admin-pubkeys>
```

Home/NAS environment:

```sh
FIPS_HOME_NSEC=nsec1... # optional override; the setup UI can save this
FIPS_PUBLIC_NPUB=npub1...
FIPS_PUBLIC_ADDR=relay.guaka.org:8443
FIPS_PUBLIC_ALIAS=public-wrapster
FIPS_PEER_TRANSPORT=tcp
CONNECTOR_SHARED_TOKEN=change-me
CONNECTOR_ADMIN_PUBKEYS=<comma-separated-admin-pubkeys>
```

For a tighter connector allowlist, replace the default `fd00::/8` mesh-wide
allowance with the public VPS FIPS address:

```sh
CONNECTOR_ALLOWED_CIDRS=<public-vps-fips-ipv6>/128,127.0.0.1/32,::1/128
```

## Start the public side

Copy or clone this repository on the public VPS, create `conf.toml`, and set
the public environment values listed above. Then start the stack:

```sh
cp conf.toml.example conf.toml
docker compose -f compose.fips-public.yml up --build -d
```

The public side exposes:

- `5542/tcp` for Wrapster.
- `8443/tcp` for FIPS TCP transport.

Wrapster connects to the home connector at:

```sh
MEDIA_CONNECTOR_BASE_URL=http://home-media.fips:22000
```

If `FIPS_PUBLIC_NSEC` is empty, open the public Hub UI and generate an
identity. The admin UI saves the secret into the `fips-public-data` volume and
shows the public `npub`; copy that `npub` into the home side as
`FIPS_PUBLIC_NPUB`. The FIPS sidecar starts automatically after the identity is
saved.

## Start the home/NAS side

Copy or clone this repository on the home/NAS host and set the home environment
values listed above. Then start the connector stack:

```sh
docker compose -f compose.fips-home.yml up -d
```

The home side exposes:

- `22001/tcp` for the LAN setup UI.

The home side does not need router port forwarding for FIPS. It only needs to
dial the public side over TCP, using `FIPS_PUBLIC_ADDR`.

Open the setup UI from the LAN:

```text
http://<nas-lan-address>:22001/setup
```

The setup UI shows redacted connector status to LAN clients. Saving or testing
Jellyfin/Plex settings requires a NIP-07 browser extension and a pubkey listed
in `CONNECTOR_ADMIN_PUBKEYS`.

The setup UI can also generate and save the home side FIPS identity. It stores
the secret in the `fips-home-data` volume and shows the public `npub`; copy that
`npub` into the public side as `FIPS_HOME_NPUB`.

Use the Jellyfin **Play random song** button in the NAS setup UI to test the
local Jellyfin URL/API key and get a step-by-step debug trace if Jellyfin does
not return a playable audio item.

Saved media settings are written to the connector data volume at:

```text
/data/connector-config.json
```

The connector applies saved settings immediately without a container restart,
and the FIPS sidecar starts automatically after its identity is saved.

For Portainer on NAS, paste `compose.fips-home.yml` directly into a new stack
and fill only the necessary env vars in the stack editor. If you need to build
from your own local sources instead, deploy the stack from shell with
`compose.fips-home.build.yml` on top, but the published stack version is intended
for Portainer where local file paths are unavailable.

Make sure the stack can pull the connector image. The published image reference in
`compose.fips-home.yml` is `ghcr.io/guaka/wrapster:latest`; if you must override it
in Portainer, set `WRAPSTER_CONNECTOR_IMAGE` to a valid, reachable image tag.

## Verify

On both sides:

```sh
docker compose -f compose.fips-public.yml exec fips-public fipsctl show status
docker compose -f compose.fips-public.yml exec fips-public fipsctl show peers
```

Use `compose.fips-home.yml` and `fips-home` for the home side.

From the public side, verify the home alias resolves:

```sh
docker compose -f compose.fips-public.yml exec wrapster getent hosts home-media.fips
```

Then call the public media status endpoint with a valid NIP-98 request. The
response should report the FIPS transport and connector status:

```json
{"transport":"fips","connector":{"services":{"jellyfin":{"configured":true}}}}
```

Jellyfin/Plex search and stream requests should then flow:

```text
client -> public Wrapster -> FIPS mesh -> home connector -> LAN media server
```

From the public Hub UI at `/admin`, use **Media Connector -> Play random
song** to test that full path. It selects a random Jellyfin audio item through
the public media API, fetches the stream through Wrapster, and shows debug steps
for connector lookup, Jellyfin query, and stream fetch.

## Automated local FIPS regression test

The local regression harness runs both public and home roles together using:

- `compose.fips-local-test.yml`
- `scripts/test-fips-compose.sh`
- `docs/fips-local-test.env.example`

Default topology behavior is outbound-only from home/NAS:

- `FIPS_HOME_ADDR` stays empty on the public side.
- `FIPS_PUBLIC_ADDR` on the home side points at the public sidecar (`fips-public:8443` in the local stack).
- Hub-to-NAS direct reachability is optional in this mode and not required for
  end-to-end success.

### Runbook and command meanings

```sh
./scripts/test-fips-compose.sh up
./scripts/test-fips-compose.sh smoke
./scripts/test-fips-compose.sh status
./scripts/test-fips-compose.sh down
```

- `up` starts all services from one compose file and then prints status.
- `smoke` asserts peer visibility/state, `home-media.fips` resolution, and connector
  route availability.
- `status` prints compose state and peer summaries to diagnose startup issues.
- `down` removes stack containers and networks for this project.

`smoke` failures indicate one of:

- peer identity mismatch or not connected,
- DNS/route issue on `home-media.fips`,
- connector API not reachable through the sidecar path,
- media API check failing when `MEDIA_STATUS_AUTHORIZATION` is configured.

See [docs/fips-local-test-compose.md](docs/fips-local-test-compose.md) for exact
smoke-check examples, reset procedure, and log snippets.

### Troubleshooting

| Symptom | Why it happens | Fix |
|---|---|---|
| Sidecar startup/state mismatch | one side shows setup mode or never connects | verify peer NPUB and NSEC values match opposite side; restart after env edits |
| Peer identity mismatch | expected peer pubkey appears different or missing | ensure `FIPS_PUBLIC_NPUB` and `FIPS_HOME_NPUB` are exchanged consistently |
| Connector allowlist/CIDR issue | `/connector/api/status` or media calls return not allowed | widen `CONNECTOR_ALLOWED_CIDRS` to include bridge/source addresses used by sidecar and test container |
| `home-media.fips` route/DNS issue | alias does not resolve in public side | confirm peer alias is set to `home-media` and sidecar has peer route logs |
