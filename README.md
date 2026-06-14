# wrapster

:''Party Like It's 1999''

Wrapster is a small Nostr-adjacent service wrapper for Trustroots and
community-hosted service experiments. The repo currently contains:

- [`wrapster`](wrapster): public service with a NIP-42 authenticated relay,
  Trustroots NIP-05 verification, a read-only admin dashboard, optional media
  gateway routes, and a generic allowlisted browser proxy under `/proxy/*`.
- [`wrapster-connector`](wrapster/cmd/wrapster-connector): private-side
  connector for Jellyfin/Plex search and streaming. It is built into the
  `wrapster` Docker image but is not started by the default compose stack.
- [Nostr Service Advert docs](docs/nostr-service-advert.md): experimental
  `kind:31388` service discovery convention plus a read-only service directory.
- [`apps/ios`](apps/ios): native SwiftUI iOS media client for searching and
  playing authorized Plex/Jellyfin streams through Wrapster with NIP-98 auth.
- [`apps/web`](apps/web): simple browser relay client that connects to Wrapster
  with a NIP-07 signer and NIP-42 relay authentication.

## Services

### wrapster

`wrapster` is the public service. In the local compose stack it listens on
`ws://localhost:5542` and serves:

- NIP-42 authenticated WebSocket relay access backed by private strfry
- Trustroots NIP-05 verification as the access rule
- read-only admin UI at `/admin`
- NIP-98 protected admin APIs at `/admin/api/*`
- optional NIP-98 protected media gateway APIs at `/media/api/*`
- generic proxy routes at `/proxy/*`

The compose stack also starts `strfry` as an internal upstream relay. It is not
published directly to the host.

`wrapster-connector` is the private side of the optional media gateway. Run it
manually on localhost, FIPS, or another private network when media routes are
needed. See [wrapster/README.md](wrapster/README.md) for connector, FIPS, and
production configuration.

### Generic proxy

The generic proxy helps browser-only static clients reach configured upstreams
without adding a backend to the app. It stores nothing and only proxies targets
listed in local `conf.toml`.

Proxy routes live under `/proxy/*`. For example,
`/proxy/hitchwiki.org/wiki/Paris` forwards to
`https://hitchwiki.org/wiki/Paris`.

## Installation

### Local development

Install Docker with the Compose plugin, clone this repo, and create a local
configuration file:

```sh
cp conf.toml.example conf.toml
```

Edit `conf.toml` and replace the placeholder `owner_npub` with the Nostr owner
pubkey for the deployment. Then start the local development stack:

```sh
./dev.sh
```

This builds the `wrapster` image locally, starts the public wrapper plus an
internal strfry relay, and keeps data in Docker named volumes.

### Production compose install

On a public host with Docker and the Compose plugin installed:

```sh
cp conf.toml.example conf.toml
```

The default `compose.yml` is local-friendly. For a public non-FIPS deployment,
add a `compose.override.yml` or deployment-specific compose file that sets the
public relay URL and any admin or media grants you need:

```yaml
services:
  wrapster:
    environment:
      PUBLIC_RELAY_URL: ${PUBLIC_RELAY_URL:?set PUBLIC_RELAY_URL}
      ADMIN_PUBKEYS: ${ADMIN_PUBKEYS:-}
      MEDIA_GRANT_PUBKEYS: ${MEDIA_GRANT_PUBKEYS:-}
```

Set those values in the shell or an `.env` file:

```sh
PUBLIC_RELAY_URL=wss://relay.example.org
ADMIN_PUBKEYS=<comma-separated-admin-pubkeys>
MEDIA_GRANT_PUBKEYS=<comma-separated-media-user-pubkeys>
```

Then start the service:

```sh
docker compose up --build -d wrapster strfry
```

For the optional media gateway, run `wrapster-connector` only on a private
network and point `MEDIA_CONNECTOR_BASE_URL` at that private connector URL.
See [wrapster/README.md](wrapster/README.md) for the full service
configuration.

### FIPS media gateway install

FIPS is the preferred pilot path for connecting the public Wrapster service to
a private home/NAS media connector without exposing Jellyfin or Plex directly.
It uses two compose files:

- `compose.fips-public.yml` on the public VPS.
- `compose.fips-home.yml` on the home/NAS side.

FIPS hosts need Docker Compose, access to `/dev/net/tun`, `NET_ADMIN`
capability for the sidecar container, IPv6 enabled in the container network,
and reachable FIPS transport ports between peers. The default compose files
publish `2121/udp` and `8443/tcp`; the public stack also publishes Wrapster on
`5542/tcp`, and the home stack publishes the LAN setup UI on `22001/tcp`.

The FIPS sidecar and connector services use published image names:

```sh
ghcr.io/guaka/wrapster-fips-sidecar:v0.3.0
ghcr.io/guaka/wrapster:latest
```

If the sidecar image is not available locally, Docker can pull it from GHCR.
If GHCR rejects the pull with `denied`, the image is being treated as private or
unavailable for your NAS credentials. In Portainer this is solved by either:

1. Registering GHCR in Portainer with a PAT (read:packages), and leaving
   `FIPS_SIDECAR_IMAGE` unset (defaults to GHCR tag), or
2. Pointing `FIPS_SIDECAR_IMAGE` at a tag you can pull unauthenticated.

If you need to iterate on local FIPS sidecar code, use
`compose.fips-home.build.yml` or `compose.fips-public.build.yml` on the shell with
`up -d --build`. Rebuild the Wrapster connector image locally when connector code
changes are needed.

If deployment fails with `pull access denied for wrapster`, check the connector image
that Portainer is trying to pull. The published image is now `ghcr.io/guaka/wrapster:latest`;
set `WRAPSTER_CONNECTOR_IMAGE=ghcr.io/guaka/wrapster:latest` explicitly in the stack
environment before redeploying.

You can start the stacks before the FIPS `nsec` values exist. Without an
`nsec`, the sidecar stays in setup mode so the public admin UI or home/NAS setup
UI can generate and save one into the shared FIPS data volume. The UI shows the
resulting `npub`; exchange those `npub` values, set a shared connector token,
and the sidecar starts automatically once its identity is saved. Existing
`FIPS_PUBLIC_NSEC` or `FIPS_HOME_NSEC` env values still work as an override.

Start the public side on the VPS:

```sh
docker compose -f compose.fips-public.yml up --build -d
```

Start the home side on the home/NAS host:

```sh
docker compose -f compose.fips-home.yml up -d
```
If deploying the home side through Portainer stacks, the `wrapster-connector` image
must be resolvable from the NAS. The new default is:

```sh
ghcr.io/guaka/wrapster:latest
docker build -f fips-sidecar/Dockerfile -t ghcr.io/guaka/wrapster-fips-sidecar:v0.3.0 .
```

Then in Portainer set `WRAPSTER_CONNECTOR_IMAGE=ghcr.io/guaka/wrapster:latest` (or your own tag)
before deployment.

If the sidecar pull fails, also set:

```sh
FIPS_SIDECAR_IMAGE=wrapster-fips-sidecar:latest
```

after building that tag on a machine that can build it.

For Portainer on NAS:

1. Open **Stacks** and create a new stack.
2. Paste `compose.fips-home.yml` into the editor.
3. Fill in only the env vars needed by your deployment (at minimum:
   `WRAPSTER_CONNECTOR_IMAGE`, `FIPS_PUBLIC_NPUB`, and the connector credentials).
   The recommended value now is:

   ```text
   WRAPSTER_CONNECTOR_IMAGE=ghcr.io/guaka/wrapster:latest
   ```

   (or `WRAPSTER_CONNECTOR_IMAGE=wrapster:latest` if you build on the NAS host).
4. Deploy, then check logs for both services:

```sh
docker compose -f compose.fips-home.yml logs -f fips-home wrapster-connector
```

Open the home setup UI from the LAN at
`http://<nas-lan-address>:22001/setup` to configure Jellyfin or Plex. The full
FIPS deployment checklist lives in
[docs/fips-media-pilot.md](docs/fips-media-pilot.md).

## Run

Start the default local stack:

```sh
./dev.sh
```

This starts:

- `wrapster` on `ws://localhost:5542`
- `strfry` as the internal upstream for `wrapster`

Public service stack:

```sh
docker compose up --build wrapster strfry
```

To pass other compose commands through the helper:

```sh
./dev.sh down
```

## Potential future plugin targets

- other Nostr services from ecosystems like Yunohost, Umbrel, or Start9

## Nostr Service Advert

Wrapster also includes an experimental service discovery convention for apps
like Radio Guaka/pleXtr, Wrapster frontends, and simple community directories
that want to find community-hosted services without exposing private endpoints:

- [Nostr Service Advert v1](docs/nostr-service-advert.md)
- [Integration guide](docs/service-advert-integration.md)
- [Service directory](examples/service-directory.html)

When Wrapster is running, the service directory is also served at
`/examples/service-directory.html`.

The simple web relay client lives in [`apps/web`](apps/web). Serve that folder
locally and open it in a browser with a NIP-07 extension to connect to
`ws://localhost:5542`.
