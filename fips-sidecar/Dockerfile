ARG FIPS_REF=v0.3.0
ARG FIPS_BIN_IMAGE=ghcr.io/guaka/wrapster-fips-sidecar:v0.3.0

FROM ${FIPS_BIN_IMAGE} AS binary-source

FROM rust:1-bookworm AS builder

ARG FIPS_REF

RUN apt-get update \
    && apt-get install -y --no-install-recommends git pkg-config ca-certificates libclang-dev libdbus-1-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
RUN git clone --depth 1 --branch "${FIPS_REF}" https://github.com/jmcorgan/fips.git .
RUN cargo build --release \
    && cp target/release/fips target/release/fipsctl target/release/fipstop /usr/local/bin/

FROM debian:bookworm-slim AS runtime-base

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates dnsmasq iproute2 iputils-ping dnsutils curl \
    && rm -rf /var/lib/apt/lists/*

RUN printf '%s\n' \
    'port=53' \
    'listen-address=127.0.0.1' \
    'bind-interfaces' \
    'server=/fips/127.0.0.1#5354' \
    'server=127.0.0.11' \
    'no-resolv' \
    > /etc/dnsmasq.conf

FROM runtime-base AS runtime-repack

COPY --from=binary-source /usr/local/bin/fips /usr/local/bin/fipsctl /usr/local/bin/fipstop /usr/local/bin/
COPY fips-sidecar/entrypoint.sh /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]

FROM runtime-base AS runtime-build

COPY --from=builder /usr/local/bin/fips /usr/local/bin/fipsctl /usr/local/bin/fipstop /usr/local/bin/
COPY fips-sidecar/entrypoint.sh /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
