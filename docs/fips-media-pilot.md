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
- inbound peer transport on `2121/udp` or `8443/tcp`
- outbound access during the first image build, because the sidecar image
  builds FIPS from the configured upstream tag

The compose files default to FIPS `v0.3.0`. Pin another release by setting
`FIPS_REF` before building:

```sh
FIPS_REF=v0.3.0
```

Create a deployment `.env` file on each host or export the values in the
shell before running Compose. Do not commit the `.env` files; they include
persistent FIPS private keys and connector tokens.

## Required values

Each stack needs its own persistent `nsec`, and each side needs the other
side's `npub` plus reachable transport address before the FIPS mesh can connect.
The stacks can start without `FIPS_PUBLIC_NSEC` or `FIPS_HOME_NSEC`; in that
case the sidecar stays alive in setup mode so the public admin UI or home/NAS
setup UI can generate a local `nsec`. Generated secrets are not saved by
Wrapster. Store them in the deployment `.env`, then restart the stack to start
FIPS.

Public VPS environment:

```sh
FIPS_PUBLIC_NSEC=nsec1... # optional for first boot; required for FIPS to run
FIPS_HOME_NPUB=npub1...
FIPS_HOME_ADDR=home.example.org:2121
FIPS_HOME_ALIAS=home-media
MEDIA_CONNECTOR_TOKEN=change-me
MEDIA_GRANT_PUBKEYS=<comma-separated-user-pubkeys>
ADMIN_PUBKEYS=<comma-separated-admin-pubkeys>
```

Home/NAS environment:

```sh
FIPS_HOME_NSEC=nsec1... # optional for first boot; required for FIPS to run
FIPS_PUBLIC_NPUB=npub1...
FIPS_PUBLIC_ADDR=vps.example.org:2121
FIPS_PUBLIC_ALIAS=public-wrapster
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
- `2121/udp` for FIPS UDP transport.
- `8443/tcp` for FIPS TCP transport.

Wrapster connects to the home connector at:

```sh
MEDIA_CONNECTOR_BASE_URL=http://home-media.fips:22000
```

If `FIPS_PUBLIC_NSEC` is empty, open the public admin UI, generate an `nsec`,
save it to `.env`, and restart:

```sh
docker compose -f compose.fips-public.yml up -d
```

## Start the home/NAS side

Copy or clone this repository on the home/NAS host and set the home environment
values listed above. Then start the connector stack:

```sh
docker compose -f compose.fips-home.yml up --build -d
```

The home side exposes:

- `2121/udp` for FIPS UDP transport.
- `8443/tcp` for FIPS TCP transport.
- `22001/tcp` for the LAN setup UI.

Open the setup UI from the LAN:

```text
http://<nas-lan-address>:22001/setup
```

The setup UI shows redacted connector status to LAN clients. Saving or testing
Jellyfin/Plex settings requires a NIP-07 browser extension and a pubkey listed
in `CONNECTOR_ADMIN_PUBKEYS`.

The setup UI can also generate a fresh `nsec` locally for `FIPS_HOME_NSEC`.
Wrapster does not save generated FIPS secrets; store the generated value once
in your deployment secret store.

Saved media settings are written to the connector data volume at:

```text
/data/connector-config.json
```

The connector applies saved settings immediately without a container restart.

If `FIPS_HOME_NSEC` is empty, generate it in the setup UI, save it to `.env`,
and restart:

```sh
docker compose -f compose.fips-home.yml up -d
```

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
response should report:

```json
{"transport":"fips"}
```

Jellyfin/Plex search and stream requests should then flow:

```text
client -> public Wrapster -> FIPS mesh -> home connector -> LAN media server
```
