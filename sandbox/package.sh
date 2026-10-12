#!/bin/bash
# Makes a bundle you can copy to another server: the launcher, the profiles, the README and both images.
set -e
H=$(cd "$(dirname "$(readlink -f "$0")")" && pwd); OUT=${1:-$HOME/goharness-sandbox-bundle-$(date +%F).tar.gz}
docker image inspect goharness-sandbox:dev gh-proxy:dev >/dev/null   # build them first with ./build-image.sh
T=$(mktemp -d); D=$T/goharness-container; mkdir -p "$D/state"
cp -r "$H/goharness-run" "$H/Dockerfile.proxy" "$H/build-image.sh" "$H/package.sh" "$H/README.md" "$H/profiles" "$D/"
docker save goharness-sandbox:dev gh-proxy:dev | gzip > "$D/images.tar.gz"
tar -czf "$OUT" -C "$T" goharness-container && rm -rf "$T"
ls -lh "$OUT" | awk '{print "bundle:", $9, $5}'; sha256sum "$OUT"
