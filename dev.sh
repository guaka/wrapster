#!/bin/sh
set -eu

if [ "$#" -eq 0 ]; then
  set -- up --build
elif [ "$1" = "up" ]; then
  shift
  set -- up --build "$@"
fi

exec docker compose "$@"
