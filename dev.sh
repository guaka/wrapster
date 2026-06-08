#!/bin/sh
set -eu

# Dev stack: air live-reloads the Go binary in-container (see compose.dev.yml),
# so the latest code is always served and Go is recompiled only when needed.
if [ "$#" -eq 0 ]; then
  set -- up --build
elif [ "$1" = "up" ]; then
  shift
  set -- up --build "$@"
fi

if [ "$1" = "up" ] && [ ! -f conf.toml ]; then
  printf '%s\n' "Missing local conf.toml."
  printf '%s\n' "Create conf.toml from conf.toml.example and replace placeholder values before starting Wrapster."
  exit 1
fi

exec docker compose -f compose.yml -f compose.dev.yml "$@"
