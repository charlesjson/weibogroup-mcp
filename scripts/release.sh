#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
out=dist/release
mkdir -p "$out"
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
mkdir "$stage/licenses"
goLicense="$(go env GOROOT)/LICENSE"
[[ -f "$goLicense" ]] || goLicense=/usr/share/licenses/go/LICENSE
cp "$goLicense" "$stage/licenses/go.txt"
go list -m -f '{{if not .Main}}{{.Path}} {{.Dir}}{{end}}' all | while read -r module dir; do
  [[ -n "$module" ]] || continue
  for license in "$dir"/LICENSE* "$dir"/COPYING*; do
    [[ -f "$license" ]] || continue
    cp "$license" "$stage/licenses/${module//\//_}-$(basename "$license")"
  done
done
cp README.md LICENSE THIRD_PARTY_NOTICES.md "$stage/"
for os in linux darwin windows; do
  for arch in amd64 arm64; do
    binary=weibogroup-mcp
    [[ "$os" != windows ]] || binary+=.exe
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -buildvcs=false -ldflags='-s -w' -o "$stage/$binary" .
    name="weibogroup-mcp-$os-$arch"
    if [[ "$os" == windows ]]; then
      (cd "$stage" && zip -q -r "$name.zip" "$binary" README.md LICENSE THIRD_PARTY_NOTICES.md licenses)
      mv "$stage/$name.zip" "$out/"
    else
      tar -C "$stage" -czf "$out/$name.tar.gz" "$binary" README.md LICENSE THIRD_PARTY_NOTICES.md licenses
    fi
  done
done
(cd "$out" && sha256sum *.tar.gz *.zip > SHA256SUMS)
