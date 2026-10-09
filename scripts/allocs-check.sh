#!/usr/bin/env bash
# Compares the allocations per operation of two benchmark runs and fails when one grew.
#
#   scripts/allocs-check.sh base.txt head.txt
#
# allocs/op does not depend on the machine, unlike the timings, so it is a check that can fail a
# build. Each benchmark is compared by the median over its runs. A benchmark is reported when its
# allocs/op rose by at least TOLERANCE (default 0: any increase) and is missing from neither file.
set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "usage: $0 base.txt head.txt" >&2
  exit 2
fi

awk -v tolerance="${TOLERANCE:-0}" '
function median(s, n,   arr, i, j, tmp) {
  n = split(s, arr, " ")
  for (i = 2; i <= n; i++) {
    tmp = arr[i] + 0
    for (j = i - 1; j >= 1 && arr[j] + 0 > tmp; j--) arr[j + 1] = arr[j]
    arr[j + 1] = tmp
  }
  return (n % 2) ? arr[(n + 1) / 2] : (arr[n / 2] + arr[n / 2 + 1]) / 2
}
FNR == 1 { file++; pkg = "" }
# go test ./... prints a "pkg:" line before the benchmarks of each package, and names such as
# BenchmarkGet repeat across packages, so the package is part of the key
/^pkg: / { pkg = $2 }
/^Benchmark/ {
  key = (pkg == "") ? $1 : pkg "." $1
  for (i = 2; i < NF; i++) if ($(i + 1) == "allocs/op") {
    if (file == 1) base[key] = base[key] " " $i; else head[key] = head[key] " " $i
  }
}
END {
  bad = 0; compared = 0
  for (name in head) {
    if (!(name in base)) continue
    compared++
    b = median(base[name]); h = median(head[name])
    if (h - b > tolerance) { printf "%-60s allocs/op %s -> %s\n", name, b, h; bad++ }
  }
  printf "compared %d benchmarks, %d with more allocations\n", compared, bad
  exit bad > 0
}' "$1" "$2"
