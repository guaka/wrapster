# Wrapster Web

Small browser client for the Wrapster relay. It uses a NIP-07 browser extension
through `window.nostr.getPublicKey()` and `window.nostr.signEvent()` to sign
NIP-42 relay authentication challenges and Nostr events.

Run Wrapster from the repo root:

```sh
./dev.sh
```

Serve this folder from another terminal:

```sh
cd apps/web
python3 -m http.server 5173
```

Open `http://localhost:5173`. The default relay is `ws://localhost:5542`.

Use the profile panel to publish a kind `0` event containing
`trustrootsUsername` and `nip05`. Wrapster allows that self-signed profile event
before NIP-42 auth so a Trustroots-linked key can bootstrap relay access.
