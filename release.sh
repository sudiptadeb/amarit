#!/bin/sh
# Cut a release of amarit itself: build every target with the version and
# amarit.json stamped in, write SHA256SUMS and releases.json, and publish
# them as a GitHub release. The manifest carries the earlier releases over
# from the one published last, so the channel never forgets a version.
#
#   ./release.sh v0.5.0
set -eu
VERSION=${1:?usage: release.sh vX.Y.Z}
DIST=dist
NOTES=$(mktemp)

go vet ./... && go test ./...
rm -rf "$DIST" && mkdir -p "$DIST"
STAMP=$(go run ./cmd/amarit stamp -version "$VERSION")
for target in $(sed -n 's/.*"targets": \[\(.*\)\].*/\1/p' amarit.json | tr -d '", '); do
	GOOS=${target%/*} GOARCH=${target#*/} CGO_ENABLED=0 go build -trimpath \
		-ldflags "$STAMP -X main.version=$VERSION" \
		-o "$DIST/amarit-${target%/*}-${target#*/}" ./cmd/amarit
done
(cd "$DIST" && shasum -a 256 amarit-* > SHA256SUMS)
gh release download --pattern releases.json -O "$DIST/releases.json" 2>/dev/null || true
go run ./cmd/amarit release -version "$VERSION" -dist "$DIST" -out "$DIST/releases.json"

# Release notes: the CHANGELOG section for this version.
awk -v v="## $VERSION" '$0 == v {on = 1; next} /^## / {on = 0} on' CHANGELOG.md > "$NOTES"
gh release create "$VERSION" "$DIST"/* --title "$VERSION" --notes-file "$NOTES"
rm -f "$NOTES"
