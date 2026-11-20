#!/usr/bin/env bash
set -euo pipefail

version=${WARPMESH_VERSION:-dev}
if [[ "$version" == dev ]]; then
  version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
fi
commit=$(git rev-parse HEAD 2>/dev/null || echo unknown)
build_time=${SOURCE_DATE_EPOCH:+$(date -u -d "@$SOURCE_DATE_EPOCH" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -r "$SOURCE_DATE_EPOCH" +%Y-%m-%dT%H:%M:%SZ)}
build_time=${build_time:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}
mkdir -p .build
printf '%s\n' "$version" > .build/version
printf '%s\n' "$commit" > .build/commit
printf '%s\n' "$build_time" > .build/build-time
printf -- '-X github.com/pcc-258/warpmesh/internal/version.Version=%s -X github.com/pcc-258/warpmesh/internal/version.Commit=%s -X github.com/pcc-258/warpmesh/internal/version.BuildTime=%s' "$version" "$commit" "$build_time"
