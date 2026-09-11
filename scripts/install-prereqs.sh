#!/usr/bin/env bash
# Installs everything owpengram-server.sh checks for, so a fresh machine needs
# one command instead of a shopping list: Go, Python 3 (+ the panel's packages
# in a venv), Docker and OpenSSL.
#
#   ./scripts/install-prereqs.sh            install whatever is missing
#   ./scripts/install-prereqs.sh --dry-run  only report what it would install
#   ./scripts/install-prereqs.sh --yes      no confirmation prompt
#
# Supported: Arch (pacman) and Ubuntu/Debian (apt). Anything else is reported
# rather than guessed at -- a wrong package manager on a production box is a
# worse outcome than a clear "install these yourself".
#
# Root is taken once, up front, and held for the whole run: a sudo prompt
# appearing ten minutes in, after the user walked away, is how half-installed
# machines happen.
set -euo pipefail
cd "$(dirname "$0")/.."
REPO_ROOT="$PWD"

ok()   { printf '[ok] %s\n' "$*"; }
info() { printf '[..] %s\n' "$*"; }
warn() { printf '[WARN] %s\n' "$*"; }
die()  { printf '[ERROR] %s\n' "$*" >&2; exit 1; }

DRY_RUN=0
ASSUME_YES=0
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=1 ;;
    --yes|-y)  ASSUME_YES=1 ;;
    -h|--help) sed -n '2,14p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) die "unknown argument: $arg" ;;
  esac
done

# --- distro ------------------------------------------------------------------
# ID_LIKE is what makes derivatives work without listing every one of them:
# EndeavourOS says ID_LIKE=arch, Pop!_OS and Mint say ID_LIKE=ubuntu/debian.
FAMILY=""
DISTRO_NAME="unknown"
if [[ -r /etc/os-release ]]; then
  # shellcheck disable=SC1091
  . /etc/os-release
  DISTRO_NAME="${PRETTY_NAME:-$ID}"
  case " ${ID:-} ${ID_LIKE:-} " in
    *" arch "*)             FAMILY="arch" ;;
    *" ubuntu "*|*" debian "*) FAMILY="debian" ;;
  esac
fi
[[ -n "$FAMILY" ]] || die "unsupported distribution: ${DISTRO_NAME}. Supported: Arch and Ubuntu/Debian. Install Go 1.25+, Python 3, Docker and OpenSSL by hand, then run ./owpengram-server.sh"

# --- what is missing ---------------------------------------------------------
have() { command -v "$1" >/dev/null 2>&1; }

# Go's own version gate: go.mod asks for 1.25, and a too-old toolchain fails at
# build time with a message that does not obviously point back here.
GO_MIN_MINOR=25
go_is_recent_enough() {
  have go || return 1
  local v minor
  v="$(go env GOVERSION 2>/dev/null || true)"   # e.g. go1.25.7
  minor="${v#go1.}"
  minor="${minor%%.*}"
  [[ "$minor" =~ ^[0-9]+$ ]] && (( minor >= GO_MIN_MINOR ))
}

venv_python() { printf '%s/.venv/bin/python' "$REPO_ROOT"; }

# Same precedence owpengram-server.sh uses when it picks an interpreter: the
# venv when one exists, otherwise whatever python3 is on PATH. Checking only the
# venv would report the packages missing on a machine that already has them
# installed system-wide, and build a venv nobody asked for.
pydeps_satisfied() {
  local py
  if [[ -x "$(venv_python)" ]]; then
    py="$(venv_python)"
  elif have python3; then
    py="python3"
  else
    return 1
  fi
  [[ -z "$("$py" tui-panel/check_deps.py 2>/dev/null)" ]]
}

NEEDED=()
go_is_recent_enough                || NEEDED+=("go")
have python3                       || NEEDED+=("python")
have docker                        || NEEDED+=("docker")
have openssl                       || NEEDED+=("openssl")
# The venv is built with python3, so it can only be settled after Python is.
pydeps_satisfied                   || NEEDED+=("pydeps")

if [[ ${#NEEDED[@]} -eq 0 ]]; then
  ok "All prerequisites are already installed."
  exit 0
fi

echo "== Missing prerequisites on ${DISTRO_NAME} =="
for item in "${NEEDED[@]}"; do
  case "$item" in
    go)      echo "  - Go 1.${GO_MIN_MINOR}+ (builds owpengram-server and the admin panel)" ;;
    python)  echo "  - Python 3 (runs the server-panel TUI)" ;;
    pydeps)  echo "  - Python packages: textual, psutil, cryptography (into ./.venv)" ;;
    docker)  echo "  - Docker (runs PostgreSQL, Redis and MinIO)" ;;
    openssl) echo "  - OpenSSL (exports the server's RSA public key for clients)" ;;
  esac
done
echo

if [[ "$DRY_RUN" -eq 1 ]]; then
  ok "Dry run -- nothing was installed."
  exit 0
fi

if [[ "$ASSUME_YES" -ne 1 ]]; then
  read -r -p "Install these now? This needs root. [Y/n] " answer
  case "${answer:-y}" in
    [Yy]|[Yy][Ee][Ss]|"") ;;
    *) die "cancelled" ;;
  esac
fi

# Ask for the password once, then refresh the timestamp in the background so a
# long Go download or apt run does not hit a second prompt mid-way.
if [[ $EUID -ne 0 ]]; then
  have sudo || die "sudo is not installed and this is not running as root"
  sudo -v || die "could not acquire root"
  while true; do sudo -n true 2>/dev/null; sleep 50; done &
  SUDO_KEEPALIVE=$!
  trap 'kill "$SUDO_KEEPALIVE" 2>/dev/null || true' EXIT
  SUDO="sudo"
else
  SUDO=""
fi

needs() { local x; for x in "${NEEDED[@]}"; do [[ "$x" == "$1" ]] && return 0; done; return 1; }

# --- package manager ---------------------------------------------------------
APT_UPDATED=0
apt_update_once() {
  if [[ "$APT_UPDATED" -eq 0 ]]; then
    $SUDO apt-get update -qq
    APT_UPDATED=1
  fi
}
pkg_install() {
  case "$FAMILY" in
    arch)   $SUDO pacman -S --needed --noconfirm "$@" ;;
    debian) apt_update_once; $SUDO env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "$@" ;;
  esac
}

# --- Python + OpenSSL --------------------------------------------------------
if needs python; then
  info "Installing Python 3"
  case "$FAMILY" in
    arch)   pkg_install python ;;
    # python3-venv is separate on Debian/Ubuntu and is what the panel's
    # dependencies go into: both distros mark the system Python
    # externally-managed, so pip into it is refused by design.
    debian) pkg_install python3 python3-venv python3-pip ;;
  esac
  ok "Python installed: $(python3 --version)"
fi

if needs openssl; then
  info "Installing OpenSSL"
  pkg_install openssl
  ok "OpenSSL installed: $(openssl version)"
fi

# --- Go ----------------------------------------------------------------------
# Arch ships a current Go; Debian/Ubuntu do not (24.04 is still on 1.22, below
# what go.mod needs), so there the official tarball is the only sane source.
install_go_tarball() {
  local version arch tarball url tmp expected actual
  version="$(curl -fsSL 'https://go.dev/VERSION?m=text' | head -n1)"
  [[ "$version" == go* ]] || die "could not determine the latest Go version"
  case "$(uname -m)" in
    x86_64)  arch="amd64" ;;
    aarch64) arch="arm64" ;;
    armv7l)  arch="armv6l" ;;
    *) die "unsupported CPU architecture for the Go tarball: $(uname -m)" ;;
  esac
  tarball="${version}.linux-${arch}.tar.gz"
  url="https://go.dev/dl/${tarball}"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN

  info "Downloading ${tarball}"
  curl -fsSL "$url" -o "$tmp/$tarball"

  # The published checksum lives in go.dev's release index, so the download is
  # verified against the site's metadata rather than trusted on arrival.
  expected="$(curl -fsSL 'https://go.dev/dl/?mode=json&include=all' \
    | python3 -c 'import json,sys
want = sys.argv[1]
for release in json.load(sys.stdin):
    for f in release.get("files", []):
        if f.get("filename") == want:
            print(f.get("sha256", ""))
            raise SystemExit
' "$tarball")"
  [[ -n "$expected" ]] || die "no published checksum for ${tarball}"
  actual="$(sha256sum "$tmp/$tarball" | cut -d' ' -f1)"
  [[ "$actual" == "$expected" ]] || die "checksum mismatch for ${tarball}: expected ${expected}, got ${actual}"
  ok "Checksum verified"

  $SUDO rm -rf /usr/local/go
  $SUDO tar -C /usr/local -xzf "$tmp/$tarball"
  # /usr/local/go/bin is not on a default PATH; drop a profile snippet so new
  # shells find it. The current shell still will not, hence the note at the end.
  printf 'export PATH=$PATH:/usr/local/go/bin\n' | $SUDO tee /etc/profile.d/go.sh >/dev/null
  $SUDO chmod 0644 /etc/profile.d/go.sh
  export PATH="$PATH:/usr/local/go/bin"
}

GO_NEEDS_NEW_SHELL=0
if needs go; then
  info "Installing Go"
  case "$FAMILY" in
    arch)   pkg_install go ;;
    debian) install_go_tarball; GO_NEEDS_NEW_SHELL=1 ;;
  esac
  ok "Go installed: $(go version)"
fi

# --- Docker ------------------------------------------------------------------
DOCKER_NEEDS_RELOGIN=0
install_docker_debian() {
  local codename repo_distro
  # Derivatives (Mint, Pop!_OS) carry their own codename that Docker's repo
  # does not publish; UBUNTU_CODENAME is the upstream one it does.
  # shellcheck disable=SC1091
  . /etc/os-release
  codename="${UBUNTU_CODENAME:-${VERSION_CODENAME:-}}"
  [[ -n "$codename" ]] || die "could not determine the distribution codename for Docker's repository"
  case " ${ID:-} ${ID_LIKE:-} " in
    *" ubuntu "*) repo_distro="ubuntu" ;;
    *)            repo_distro="debian" ;;
  esac

  pkg_install ca-certificates curl gnupg
  $SUDO install -m 0755 -d /etc/apt/keyrings
  $SUDO curl -fsSL "https://download.docker.com/linux/${repo_distro}/gpg" -o /etc/apt/keyrings/docker.asc
  $SUDO chmod a+r /etc/apt/keyrings/docker.asc
  printf 'deb [arch=%s signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/%s %s stable\n' \
    "$(dpkg --print-architecture)" "$repo_distro" "$codename" \
    | $SUDO tee /etc/apt/sources.list.d/docker.list >/dev/null
  APT_UPDATED=0   # the new repository has to be picked up
  pkg_install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
}

if needs docker; then
  info "Installing Docker"
  case "$FAMILY" in
    arch)   pkg_install docker docker-compose ;;
    debian) install_docker_debian ;;
  esac
  if have systemctl; then
    $SUDO systemctl enable --now docker
    ok "Docker service started"
  else
    warn "systemd not found -- start the Docker daemon yourself before running ./owpengram-server.sh"
  fi
  # Without this every docker call needs sudo, which the panel does not use.
  if [[ $EUID -ne 0 ]] && ! id -nG "$USER" | tr ' ' '\n' | grep -qx docker; then
    $SUDO usermod -aG docker "$USER"
    DOCKER_NEEDS_RELOGIN=1
  fi
  ok "Docker installed: $(docker --version)"
fi

# --- the panel's Python packages ---------------------------------------------
# Always a venv, never the system interpreter: Arch and Debian both refuse pip
# into it (PEP 668), and owpengram-server.sh already prefers ./.venv when present.
if needs pydeps; then
  info "Installing the panel's Python packages into ./.venv"
  [[ -x "$(venv_python)" ]] || python3 -m venv "$REPO_ROOT/.venv"
  "$(venv_python)" -m pip install --quiet --upgrade pip
  "$(venv_python)" -m pip install --quiet -r tui-panel/requirements-panel.txt
  ok "Python packages installed"
fi

echo
ok "Prerequisites are ready."
if [[ "$GO_NEEDS_NEW_SHELL" -eq 1 ]]; then
  echo "     Go was installed to /usr/local/go. Open a new shell, or run:"
  echo "       export PATH=\$PATH:/usr/local/go/bin"
fi
if [[ "$DOCKER_NEEDS_RELOGIN" -eq 1 ]]; then
  echo "     You were added to the 'docker' group. Log out and back in (or run"
  echo "     'newgrp docker') before docker works without sudo."
fi
echo "     Then start the server with: ./owpengram-server.sh"
