#!/bin/sh
set -eu

FIPS_UDP_BIND="${FIPS_UDP_BIND:-0.0.0.0:2121}"
FIPS_TCP_BIND="${FIPS_TCP_BIND:-0.0.0.0:8443}"
FIPS_TUN_MTU="${FIPS_TUN_MTU:-1280}"
FIPS_UDP_MTU="${FIPS_UDP_MTU:-1472}"
FIPS_PEER_TRANSPORT="${FIPS_PEER_TRANSPORT:-udp}"
FIPS_PEER_ALIAS="${FIPS_PEER_ALIAS:-peer}"
FIPS_NSEC_PATH="${FIPS_NSEC_PATH:-/fips-data/nsec}"
FIPS_DATA_UID="${FIPS_DATA_UID:-10001}"
FIPS_DATA_GID="${FIPS_DATA_GID:-10001}"
DNSMASQ_STARTED=0

mkdir -p /etc/fips

start_dnsmasq() {
  if [ "${DNSMASQ_STARTED}" = "0" ]; then
    dnsmasq
    DNSMASQ_STARTED=1
  fi
}

load_fips_nsec() {
  mkdir -p "$(dirname "${FIPS_NSEC_PATH}")"
  if [ -w "$(dirname "${FIPS_NSEC_PATH}")" ]; then
    chown "${FIPS_DATA_UID}:${FIPS_DATA_GID}" "$(dirname "${FIPS_NSEC_PATH}")" || true
    chmod 0770 "$(dirname "${FIPS_NSEC_PATH}")" || true
  fi

  if [ -n "${FIPS_NSEC:-}" ]; then
    printf '%s' "${FIPS_NSEC}"
    return
  fi
  if [ -r "${FIPS_NSEC_PATH}" ]; then
    sed -n '1{s/[[:space:]]*$//;p;q;}' "${FIPS_NSEC_PATH}"
  fi
}

FIPS_NSEC="$(load_fips_nsec || true)"
if [ -z "${FIPS_NSEC}" ]; then
  printf '%s\n' "FIPS nsec is not set; starting sidecar in setup mode."
  printf '%s\n' "Generate an identity in the Wrapster admin/setup UI. FIPS will start after it is saved."
  start_dnsmasq
  while [ -z "${FIPS_NSEC}" ]; do
    sleep 2
    FIPS_NSEC="$(load_fips_nsec || true)"
  done
  printf '%s\n' "FIPS identity saved; starting FIPS."
fi

if [ -n "${FIPS_PEER_NPUB:-}" ]; then
  if [ -n "${FIPS_PEER_ADDR:-}" ]; then
    printf '  - npub: "%s"\n' "${FIPS_PEER_NPUB}" > /tmp/fips-peers.yaml
    printf '    alias: "%s"\n' "${FIPS_PEER_ALIAS}" >> /tmp/fips-peers.yaml
    printf '    addresses:\n' >> /tmp/fips-peers.yaml
    printf '      - transport: %s\n' "${FIPS_PEER_TRANSPORT}" >> /tmp/fips-peers.yaml
    printf '        addr: "%s"\n' "${FIPS_PEER_ADDR}" >> /tmp/fips-peers.yaml
    printf '    connect_policy: auto_connect\n' >> /tmp/fips-peers.yaml
  else
    printf '  - npub: "%s"\n' "${FIPS_PEER_NPUB}" > /tmp/fips-peers.yaml
    printf '    alias: "%s"\n' "${FIPS_PEER_ALIAS}" >> /tmp/fips-peers.yaml
    printf '    addresses: []\n' >> /tmp/fips-peers.yaml
    printf '    connect_policy: auto_connect\n' >> /tmp/fips-peers.yaml
    printf 'FIPS peer address not set; registering passive peer %s and waiting for inbound/outbound mesh session.\n' "${FIPS_PEER_ALIAS}" >&2
  fi
else
  printf '  []\n' > /tmp/fips-peers.yaml
fi

cat > /etc/fips/fips.yaml <<EOF
node:
  identity:
    nsec: "${FIPS_NSEC}"

tun:
  enabled: true
  name: fips0
  mtu: ${FIPS_TUN_MTU}

dns:
  enabled: true
  bind_addr: "127.0.0.1"

transports:
  udp:
    bind_addr: "${FIPS_UDP_BIND}"
    mtu: ${FIPS_UDP_MTU}
  tcp:
    bind_addr: "${FIPS_TCP_BIND}"

peers:
$(cat /tmp/fips-peers.yaml)
EOF

start_dnsmasq
exec fips --config /etc/fips/fips.yaml
