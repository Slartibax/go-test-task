#!/bin/sh
set -eu

cd /app
go mod download
exec go run ./cmd/api
