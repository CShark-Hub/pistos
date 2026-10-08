#!/bin/sh
# Builds release archives of the pistos core for every supported platform into dist/, with
# SHA-256 checksums. Packs are embedded in the binary; pack images are built locally by
# `pistos plugins install` in docker mode. Nothing is uploaded or published.
set -eu
version=${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}
out=dist
rm -rf "$out" && mkdir -p "$out"
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
	os=${target%/*}
	arch=${target#*/}
	dir="$out/$os-$arch"
	bin=pistos
	[ "$os" = windows ] && bin=pistos.exe
	mkdir -p "$dir"
	CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath \
		-ldflags "-s -w -X example.com/pistos/internal/cli.Version=$version" -o "$dir/$bin" ./cmd/pistos
	cp LICENSE README.md "$dir/"
	(cd "$out" && tar czf "pistos-$version-$os-$arch.tar.gz" "$os-$arch")
done
(cd "$out" && sha256sum pistos-*.tar.gz > SHA256SUMS)
echo "release artifacts in $out/"
