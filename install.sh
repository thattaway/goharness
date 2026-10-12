#!/bin/sh
# Installs goharness on a Linux server: checks prerequisites, clones (or
# updates) the repo, builds it, and optionally sets it up as a systemd
# user service. Mirrors the steps in README.md's "Installing on a new
# server" section - keep the two in sync if either changes.
#
# Meant to be run via:
#   curl -LsSf <raw-url-to-this-file> | sh
#   curl -LsSf <raw-url-to-this-file> | sh -s -- --service
#
# Written in POSIX sh, not bash: when piped into `sh` directly like
# above, the shebang line is ignored and whatever /bin/sh happens to be
# (dash on Debian/Ubuntu, which does not support bashisms like `set -o
# pipefail`) runs it. The whole body is deferred into main(), called only
# on the last line, so a connection that truncates mid-download fails on
# a parse error instead of running a partial script.
set -eu

REPO_URL="http://172.16.10.24:3000/thattaway/goharness.git"
INSTALL_DIR="${GOHARNESS_INSTALL_DIR:-$HOME/projects/goharness}"
MIN_GO_MAJOR=1
MIN_GO_MINOR=27
MIN_NODE_MAJOR=20

WITH_SERVICE=0
ADDR="0.0.0.0:8787"
PROJECTS_ROOT="$HOME/projects"
WEB_FLAGS="--allow-any-dir"

usage() {
  cat <<EOF
Usage: install.sh [--service] [--addr ADDR] [--projects-root DIR] [--dir DIR]

  --service              also install and start the goharness-web systemd
                         user service (default: build only, run manually)
  --addr ADDR            address for the web service to listen on
                         (default: $ADDR)
  --projects-root DIR    projects root for the web service
                         (default: $PROJECTS_ROOT)
  --dir DIR              where to clone/build goharness
                         (default: $INSTALL_DIR)

When piping into sh, pass flags after '-s --':
  curl -LsSf <url> | sh -s -- --service
EOF
}

log() { printf '==> %s\n' "$1"; }
fail() { printf 'error: %s\n' "$1" >&2; exit 1; }

check_go() {
  command -v go >/dev/null 2>&1 || fail "go is not installed. Install Go ${MIN_GO_MAJOR}.${MIN_GO_MINOR}+ from https://go.dev/dl/ and re-run this script."
  ver=$(go version | grep -oE 'go[0-9]+\.[0-9]+' | head -1 | tr -d 'go')
  if [ -z "$ver" ]; then
    fail "could not parse 'go version' output - is the go binary on PATH working correctly?"
  fi
  major=${ver%%.*}
  minor=${ver##*.}
  if [ "$major" -lt "$MIN_GO_MAJOR" ] || { [ "$major" -eq "$MIN_GO_MAJOR" ] && [ "$minor" -lt "$MIN_GO_MINOR" ]; }; then
    fail "go $ver found, but goharness needs ${MIN_GO_MAJOR}.${MIN_GO_MINOR}+. Upgrade from https://go.dev/dl/ and re-run."
  fi
  log "go $ver OK"
}

check_node() {
  command -v node >/dev/null 2>&1 || fail "node is not installed. Install Node ${MIN_NODE_MAJOR}+ (e.g. 'nvm install --lts') and re-run this script."
  nodever=$(node -v | tr -d 'v')
  nodemajor=${nodever%%.*}
  if [ "$nodemajor" -lt "$MIN_NODE_MAJOR" ]; then
    fail "node v$nodever found, but the frontend build needs Node ${MIN_NODE_MAJOR}+ (vite fails on older Node). Run 'nvm install --lts' and re-run this script."
  fi
  log "node v$nodever OK"
}

check_prereqs() {
  command -v git >/dev/null 2>&1 || fail "git is not installed."
  command -v make >/dev/null 2>&1 || fail "make is not installed."
  check_go
  check_node
}

clone_or_update() {
  if [ -d "$INSTALL_DIR/.git" ]; then
    log "existing checkout found at $INSTALL_DIR, pulling latest"
    git -C "$INSTALL_DIR" pull
  else
    log "cloning into $INSTALL_DIR"
    mkdir -p "$(dirname "$INSTALL_DIR")"
    git clone "$REPO_URL" "$INSTALL_DIR"
  fi
}

build() {
  log "building (make build)"
  make -C "$INSTALL_DIR" build
}

install_service() {
  log "installing goharness-web systemd user service"
  make -C "$INSTALL_DIR" install-service ADDR="$ADDR" PROJECTS_ROOT="$PROJECTS_ROOT" WEB_FLAGS="$WEB_FLAGS"
  systemctl --user enable --now goharness-web
  log "enabling linger so the service survives logout"
  loginctl enable-linger "$USER" || log "could not enable linger automatically - run: loginctl enable-linger $USER"
}

main() {
  while [ $# -gt 0 ]; do
    case "$1" in
      --service) WITH_SERVICE=1; shift ;;
      --addr) ADDR="$2"; shift 2 ;;
      --projects-root) PROJECTS_ROOT="$2"; shift 2 ;;
      --dir) INSTALL_DIR="$2"; shift 2 ;;
      -h|--help) usage; exit 0 ;;
      *) echo "unknown argument: $1" >&2; usage >&2; exit 1 ;;
    esac
  done

  check_prereqs
  clone_or_update
  build

  if [ "$WITH_SERVICE" -eq 1 ]; then
    install_service
    log "done. goharness-web is running - check 'systemctl --user status goharness-web' and ~/.config/goharness/web_token for the access token."
  else
    log "done. Binary is at $INSTALL_DIR/goharness"
    log "  run '$INSTALL_DIR/goharness' for the terminal UI"
    log "  run '$INSTALL_DIR/goharness web' for the web UI"
    log "  re-run with --service to install it as a background service instead"
  fi
}

main "$@"
