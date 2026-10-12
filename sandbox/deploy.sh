#!/bin/bash
# deploy.sh user@host [--dir DIR] [--base-url URL] [--model NAME] [--force-images]
# Installs (or updates) the sandbox on another server over ssh. Needs on the target: Docker, and your user in the docker group.
#   - copies the launcher, README and default profiles (profiles that already exist on the target are kept)
#   - sends the images only if the target does not already have the same ones
#   - optionally sets BASE_URL / MODEL in the target's profiles/default.conf
#   - ends with a check that the sandbox starts and reaches the model server
set -euo pipefail
H=$(cd "$(dirname "$(readlink -f "$0")")" && pwd)
TARGET=""; DIR='$HOME/goharness-container'; BASE_URL=""; MODEL=""; FORCE=0
while [ $# -gt 0 ]; do case "$1" in
  --dir) DIR=$2; shift 2;; --base-url) BASE_URL=$2; shift 2;; --model) MODEL=$2; shift 2;; --force-images) FORCE=1; shift;;
  -h|--help) sed -n 2,7p "$0"; exit 0;; -*) echo "unknown option $1"; exit 1;; *) TARGET=$1; shift;; esac; done
[ -n "$TARGET" ] || { sed -n 2,7p "$0"; exit 1; }
SSH=${DEPLOY_SSH:-ssh}          # tests replace this
run() { $SSH "$TARGET" "$@"; }
say() { echo "==> $*"; }
die() { echo "ERROR: $*" >&2; exit 1; }

say "checking $TARGET"
run 'docker version --format "docker {{.Server.Version}}"' || die "cannot run docker on $TARGET (no ssh access, or the user is not in the docker group)"

say "copying launcher and README to $DIR"
tar -C "$H" -cf - goharness-run Dockerfile.proxy build-image.sh package.sh deploy.sh README.md \
  | run "mkdir -p $DIR && tar -xf - -C $DIR && chmod +x $DIR/goharness-run $DIR/*.sh"
say "copying profiles (any that already exist there are kept)"
tar -C "$H" -cf - profiles | run "mkdir -p $DIR && tar -xf - --skip-old-files -C $DIR"

for img in goharness-sandbox:dev gh-proxy:dev; do
  mine=$(docker image inspect -f '{{.Id}}' "$img") || die "image $img is not built here: run build-image.sh first"
  theirs=$(run "docker image inspect -f '{{.Id}}' $img 2>/dev/null" || true)
  if [ "$mine" = "$theirs" ] && [ $FORCE = 0 ]; then say "$img is already on $TARGET and up to date"
  else
    say "sending $img ($(docker image inspect -f '{{.Size}}' "$img" | awk '{printf "%d MB", $1/1e6}'), compressed on the wire)"
    docker save "$img" | gzip | run 'gunzip | docker load' >/dev/null
  fi
done

[ -z "$BASE_URL" ] || { say "setting BASE_URL=$BASE_URL"; run "sed -i 's#^BASE_URL=[^ ]*#BASE_URL=$BASE_URL#' $DIR/profiles/default.conf"; }
[ -z "$MODEL" ] || { say "setting MODEL=$MODEL"; run "sed -i 's#^MODEL=[^ ]*#MODEL=$MODEL#' $DIR/profiles/default.conf"; }

say "checking that the sandbox starts and reaches the model server"
run "cd $DIR && P=\$(mktemp -d) && ./goharness-run --exec 'echo \"sandbox up, user \$(id -u), project mounted: \$(ls -d /work)\"; printf \"model server /models via the relay -> HTTP %s\n\" \"\$(curl -s -m 8 -o /dev/null -w %{http_code} \$GH_BASE_URL/models)\"' \$P; rmdir \$P"
say "done. On $TARGET run:  $DIR/goharness-run <project-dir>"
