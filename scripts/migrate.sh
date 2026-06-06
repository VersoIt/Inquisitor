#!/usr/bin/env sh
set -eu

CONFIG="${CONFIG:-configs/config.example.yaml}"
MIGRATIONS="${MIGRATIONS:-migrations}"

go run ./cmd/migrate -config "$CONFIG" -migrations "$MIGRATIONS"
