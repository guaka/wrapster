#!/bin/sh
set -eu

: "${FIPS_NSEC:?FIPS_NSEC is required}"

FIPS_UDP_BIND="${FIPS_UDP_BIND:-0.0.0.0:2121}"
FIPS_TCP_BIND="${FIPS_TCP_BIND:-0.0.0.0:8443}"
FIPS_TUN_MTU="${FIPS_TUN_MTU:-1280}"
FIPS_UDP_MTU="${FIPS_UDP_MTU:-1472}"
FIPS_PEER_TRANSPORT="${FIPS_PEER_TRANSPORT:-udp}"

mkdir -p /etc/fips

if [ -n "${FIPS_PEER_NPUB:-}" ] && [ -n "${FIPS_PEER_ADDR:-}" ]; then
  FIPS_PEER_ALIAS="${FIPS_PEER_ALIAS:-peer}"
  cat > /tmp/fips-peers.yaml <<EOF
  - npub: "${FIPS_PEER_NPUB}"
    alias: "${FIPS_PEER_ALIAS}"
    addresses:
      - transport: ${FIPS_PEER_TRANSPORT}
        addr: "${FIPS_PEER_ADDR}"
    connect_policy: auto_connect
EOF
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

dnsmasq
exec fips --config /etc/fips/fips.yaml
