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
  `kind:31388` service discovery convention plus a read-only browser example.

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
manually on localhost, WireGuard, or another private network when media routes
are needed. See [wrapster/README.md](wrapster/README.md) for connector,
WireGuard, and production configuration.

### Generic proxy

The generic proxy helps browser-only static clients reach configured upstreams
without adding a backend to the app. It stores nothing and only proxies targets
listed in local `conf.toml`.

Proxy routes live under `/proxy/*`. For example,
`/proxy/hitchwiki.org/wiki/Paris` forwards to
`https://hitchwiki.org/wiki/Paris`.

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

- [fips](https://fips.network/)
- other Nostr services from ecosystems like Yunohost, Umbrel, or Start9

## Nostr Service Advert

Wrapster also includes an experimental service discovery convention for apps
like Radio Guaka/pleXtr, Wrapster frontends, and simple community directories
that want to find community-hosted services without exposing private endpoints:

- [Nostr Service Advert v1](docs/nostr-service-advert.md)
- [Integration guide](docs/service-advert-integration.md)
- [Read-only example browser](examples/service-advert-browser.html)

When Wrapster is running, the example browser is also served at
`/examples/service-advert-browser.html`.
