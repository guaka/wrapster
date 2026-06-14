# Automated local FIPS regression test runbook

This runbook uses the helper stack and test script to reproduce the Hub <-> NAS media path
in one machine and validate the full media route end-to-end.

## Prerequisites

- Docker Desktop or Docker Engine with Compose plugin.
- Linux bridge networking support, with `/dev/net/tun` and `NET_ADMIN` capability available
  to containers.
- No outbound restrictions from the home/NAS sidecar container to the public sidecar peer
  and no inbound restrictions needed for Home in outbound-only mode.
- A concrete copy of `docs/fips-local-test.env.example` with real test values.
- Go toolchain on the host for smoke-time NIP-98 header generation (also used when building wrapster images).

## Startup and baseline checks

1. Copy the environment template:
   ```sh
   cp docs/fips-local-test.env.example docs/fips-local-test.env
   ```
2. Optional: rotate these fixed test credentials if you want isolated runs.
   `fips-local-test.env.example` already includes deterministic values for
   `FIPS_PUBLIC_NSEC`, `FIPS_HOME_NSEC`, `FIPS_PUBLIC_NPUB`,
   `FIPS_HOME_NPUB`, `MEDIA_CONNECTOR_TOKEN`, `CONNECTOR_SHARED_TOKEN`,
   `MEDIA_SMOKE_NSEC`, `MEDIA_SMOKE_NPUB`, and `MEDIA_GRANT_PUBKEYS`.
3. Start the stack:
   ```sh
   ./scripts/test-fips-compose.sh --env-file docs/fips-local-test.env up
   ```
4. Verify status:
   ```sh
   ./scripts/test-fips-compose.sh --env-file docs/fips-local-test.env status
   ```

Default topology is outbound-only from home to public:

- `FIPS_HOME_ADDR` is empty on the public side.
- Home side uses `FIPS_PUBLIC_ADDR=fips-public:8443`.
- Hub-to-NAS direct reachability is expected to fail by design in this mode and is not an error.

To run a direct-connect variant, set:

```sh
FIPS_HOME_ADDR=fips-home:8443
FIPS_PUBLIC_ADDR=fips-public:8443
```

in the env file. This is optional and should only be used when validating symmetric
connectivity assumptions.

## Smoke test checklist

Run:

```sh
./scripts/test-fips-compose.sh --env-file docs/fips-local-test.env smoke
```

Checklist and assertions:

- Public side peers: `fips-public` shows `FIPS_HOME_NPUB` and a connected state.
- Home side peers: `fips-home` shows `FIPS_PUBLIC_NPUB` and a connected state.
- Alias resolution: `getent hosts home-media.fips` succeeds from the public stack side.
- Sidecar connector health: `home-media.fips:22000/connector/api/status` returns JSON with
  `"services"`.
- Public media route: `/media/api/status` through wrapster returns JSON with `"connector"`
  and `"transport"` metadata. Smoke signs a fresh NIP-98 header from `MEDIA_SMOKE_NSEC` when
  `MEDIA_GRANT_PUBKEYS` includes the matching pubkey. Set `MEDIA_STATUS_AUTHORIZATION` manually
  to override the generated header, or clear `MEDIA_SMOKE_NSEC` to skip this assertion.

## Expected snippets

Sidecar peer summary should include a connected marker and peer identity:

```text
npub1...
status: connected
```

Connector status response example:

```json
{"services":{"jellyfin":{"configured":false}}}
```

Public media status example:

```json
{
  "authenticated_pubkey": "ce24e3cbe00d971cb6d76fe5ee86da0e1aa1bec8d8e6c273d3b1e3d4bcbd8abc",
  "transport": "fips",
  "connector": {
    "services": {
      "jellyfin": {"configured": false},
      "plex": {"configured": false}
    }
  }
}
```

Smoke generates the NIP-98 `Authorization` header at runtime via
`scripts/gen-nip98-auth/main.go` because headers expire after `MEDIA_AUTH_MAX_AGE` (default
`60s`). The signed URL must match the in-netns request target:
`http://127.0.0.1:5542/media/api/status`.

## Troubleshooting

| Check | What to look for | Likely fix |
|---|---|---|
| Sidecar startup or peer states | Logs show repeated setup mode, missing peer list entries, or repeated reconnect loops | Ensure both `FIPS_*_NSEC` and `FIPS_*_NPUB` are populated and correct for this deterministic test run. Rebuild the stack after env changes. |
| Peer identity mismatch | A peer entry exists but peer pubkey does not match expected counterpart | Recheck exchanged values and confirm `FIPS_PUBLIC_NPUB` on home equals public peer printout and `FIPS_HOME_NPUB` on public equals home printout. |
| Connector allowlist/CIDR issues | `/media/api/*` checks fail with 403 or “unreachable” status | Expand `CONNECTOR_ALLOWED_CIDRS` to include the public sidecar source address in this stack, then restart connector. |
| `home-media.fips` route/DNS issue | `getent` output is empty from wrapster container | Confirm public sidecar has peer alias `home-media` and check logs for routing/peer state mismatch. Restart both sidecars after env changes. |
| Media status 401/403 | `/media/api/status` rejects the NIP-98 header | Ensure `MEDIA_GRANT_PUBKEYS` matches `MEDIA_SMOKE_NSEC`, restart wrapster after env changes, and confirm smoke signs `http://127.0.0.1:5542/media/api/status`. |

## Cleanup and reset

```sh
./scripts/test-fips-compose.sh --env-file docs/fips-local-test.env down
```

To start clean, remove named volumes created by the local stack before rerun:

```sh
docker volume rm $(docker volume ls -q | rg "^wrapster-fips-local-test_")
```
