#!/bin/sh
set -eu

if [ "$#" -eq 0 ]; then
  set -- up --build --watch
elif [ "$1" = "up" ]; then
  shift
  set -- up --build --watch "$@"
fi

if [ "$1" = "up" ] && [ ! -f conf.toml ]; then
  printf '%s\n' "Missing local conf.toml."
  printf '%s\n' "Create conf.toml from conf.toml.example and replace placeholder values before starting Wrapster."
  exit 1
fi

exec docker compose "$@"
