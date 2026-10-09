#!/usr/bin/env bash
# Runs the benchmarks of the module and writes the raw output for benchstat.
#
#   scripts/bench.sh [output-file]
#
# Environment:
#   COUNT      runs per benchmark (default 10; benchstat needs at least 6 for confidence intervals)
#   CPUS       value of -cpu (default "1,4,16")
#   BENCHTIME  value of -benchtime (default 200ms)
#   PACKAGES   packages to run (default ./... without machine, which needs hardware access)
#   SKIP       benchmarks to skip (default: the ones that are slow by design)
#
# Benchmarks that need Redis, RabbitMQ or etcd skip themselves when the service is missing.
# `docker compose -f test/docker-compose.yaml up -d` starts Redis and RabbitMQ.
#
# Compare two runs with: benchstat before.txt after.txt
set -euo pipefail

out="${1:-bench.txt}"
count="${COUNT:-10}"
cpus="${CPUS:-1,4,16}"
benchtime="${BENCHTIME:-200ms}"
skip="${SKIP:-GenerateKeyPair|DeriveKey|DeriveArgon2Key|GeneratorID}"

if [ -n "${PACKAGES:-}" ]; then
  packages="$PACKAGES"
else
  packages="$(go list ./... | grep -v '/machine$')"
fi

# shellcheck disable=SC2086
go test -run '^$' -bench . -benchmem -count "$count" -cpu "$cpus" -benchtime "$benchtime" -skip "$skip" $packages | tee "$out"
