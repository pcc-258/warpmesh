#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/../.."
mkdir -p packages
build_dir=$(mktemp -d)
trap 'rm -rf "$build_dir"' EXIT

for target in linux-amd64 linux-arm64 windows-amd64 darwin-amd64 darwin-arm64; do
  os=${target%-*}
  arch=${target#*-}
  suffix=''
  if [[ "$os" == windows ]]; then suffix='.exe'; fi
  folder="$build_dir/warpmesh-$target"
  mkdir -p "$folder"
  for component in server agent; do
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath \
      -o "$folder/warpmesh-$component$suffix" "./cmd/$component"
  done
  cp LICENSE README.md "$folder/"
  if [[ "$os" == windows ]]; then
    (cd "$build_dir" && zip -qr "$OLDPWD/packages/warpmesh-$target.zip" "warpmesh-$target")
  else
    tar -czf "packages/warpmesh-$target.tar.gz" -C "$build_dir" "warpmesh-$target"
  fi
done

(cd packages && sha256sum warpmesh-*.tar.gz warpmesh-*.zip > SHA256SUMS && sha256sum -c SHA256SUMS)
git rev-parse HEAD > packages/COMMIT.txt
