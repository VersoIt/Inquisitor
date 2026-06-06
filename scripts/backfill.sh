#!/usr/bin/env sh
set -eu

CONFIG="${CONFIG:-configs/config.example.yaml}"
SYMBOLS="${SYMBOLS:-BTCUSDT,ETHUSDT}"
INTERVALS="${INTERVALS:-1}"
LIMIT="${LIMIT:-1000}"

set -- -config "$CONFIG" -symbols "$SYMBOLS" -intervals "$INTERVALS" -limit "$LIMIT"
if [ -n "${START:-}" ]; then
  set -- "$@" -start "$START"
fi
if [ -n "${END:-}" ]; then
  set -- "$@" -end "$END"
fi

go run ./cmd/backfill "$@"
