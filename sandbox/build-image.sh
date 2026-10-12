#!/bin/bash
# Builds the sandbox image from a machine that has goharness, rtk and Node 22 installed.
# Needs: Docker, a Linux x86-64 goharness binary built against glibc (the image is Ubuntu 24.04), rtk, and a Node 22 folder.
#   GOHARNESS_BIN=~/projects/goharness/goharness RTK_BIN=~/.local/bin/rtk NODE_DIR=~/.nvm/versions/node/v22.23.3 ./build-image.sh
set -e
H=$(cd "$(dirname "$(readlink -f "$0")")" && pwd); mkdir -p "$H/ctx" && cd "$H/ctx"
cp "${GOHARNESS_BIN:-$([ -x "$H/../goharness" ] && echo "$H/../goharness" || echo "$HOME/projects/goharness/goharness")}" goharness
cp "${RTK_BIN:-$HOME/.local/bin/rtk}" rtk
rm -rf node && cp -r "${NODE_DIR:-$HOME/.nvm/versions/node/v22.23.3}" node
cat > Dockerfile <<'DOCKER'
FROM ubuntu:24.04
RUN apt-get update && apt-get install -y --no-install-recommends git ripgrep ca-certificates curl \
 && rm -rf /var/lib/apt/lists/* && mkdir -p /home/agent /work && chmod 777 /home/agent /work
COPY node /opt/node
COPY goharness rtk /usr/local/bin/
ENV PATH=/opt/node/bin:$PATH HOME=/home/agent
WORKDIR /work
ENTRYPOINT ["goharness"]
DOCKER
docker build -t goharness-sandbox:dev .
docker build -t gh-proxy:dev -f "$H/Dockerfile.proxy" "$H"
