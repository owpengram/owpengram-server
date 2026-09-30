#!/usr/bin/env bash
# Builds the same self-contained release archives .github/workflows/build.yml
# produces in CI, locally -- Linux/macOS counterpart to
# scripts/build-release.ps1 (kept in lockstep with it; see that script's own
# doc comment for the full picture of what goes into an archive and why).
#
#   ./scripts/build-release.sh                       just this machine's platform
#   ./scripts/build-release.sh --all                  all 4 CI platforms
#   ./scripts/build-release.sh --platform linux/amd64 --platform windows/amd64
#   ./scripts/build-release.sh --version v1.4.0
#   ./scripts/build-release.sh --skip-pgcache         skip the Postgres pre-bundle download
#   ./scripts/build-release.sh --skip-web-build       skip npm ci && npm run build
#   ./scripts/build-release.sh --out-dir dist
#
# For each target platform: cross-compiles owpengram-server, owpengram-
# admin-panel and owpengram-ctl (CGO_ENABLED=0, so no C toolchain is needed
# even when cross-compiling), assembles a staging directory with
# .env.example, data/langpack, data/sticker-seed, deploy/docker-compose.yml
# and the right start.sh/start.bat launcher, pre-bundles the embedded-
# PostgreSQL binaries for that platform into data/pgcache/ (see
# internal/embeddedpg.go's CachePath doc comment -- this is what makes the
# portable edition's first start need zero network access), and packages
# the result into a .tar.gz (or .zip for Windows targets) under dist/.
set -euo pipefail
cd "$(dirname "$0")/.."

# Must match internal/embeddedpg.go's pgVersion constant -- there is no
# automated check tying these together, same caveat as the CI workflow and
# build-release.ps1.
PG_VERSION=17.5.0

ok()   { printf '[build-release] %s\n' "$*"; }
warn() { printf '[build-release] WARNING: %s\n' "$*" >&2; }
die()  { printf '[build-release] ERROR: %s\n' "$*" >&2; exit 1; }
usage() { sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'; }

VERSION=dev
OUT_DIR=dist
SKIP_PGCACHE=0
SKIP_WEB_BUILD=0
ALL=0
PLATFORMS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --all) ALL=1; shift ;;
    --platform) [[ $# -ge 2 ]] || die "--platform requires a value"; PLATFORMS+=("$2"); shift 2 ;;
    --version) [[ $# -ge 2 ]] || die "--version requires a value"; VERSION=$2; shift 2 ;;
    --out-dir) [[ $# -ge 2 ]] || die "--out-dir requires a value"; OUT_DIR=$2; shift 2 ;;
    --skip-pgcache) SKIP_PGCACHE=1; shift ;;
    --skip-web-build) SKIP_WEB_BUILD=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown argument: $1 (see --help)" ;;
  esac
done

command -v go >/dev/null 2>&1 || die "Go is not on PATH -- install it from https://go.dev/dl/"

if [[ ${#PLATFORMS[@]} -eq 0 ]]; then
  if [[ "$ALL" -eq 1 ]]; then
    PLATFORMS=(linux/amd64 linux/arm64 windows/amd64 windows/arm64)
  else
    host_goos=$(go env GOOS)
    host_goarch=$(go env GOARCH)
    PLATFORMS=("$host_goos/$host_goarch")
    ok "No --platform given, building only for the host platform: $host_goos/$host_goarch (pass --all for every CI platform)."
  fi
fi

if [[ "$SKIP_WEB_BUILD" -eq 0 ]]; then
  ok "Building admin panel web assets (npm ci && npm run build)..."
  (cd cmd/telesrv-admin/web && npm ci && npm run build)
else
  warn "Skipping web asset build (--skip-web-build) -- the admin binary will embed whatever is already in cmd/telesrv-admin/web/dist."
fi

mkdir -p "$OUT_DIR"

# goos/goarch -> zonkyio/embedded-postgres-binaries os/arch. Empty means "no
# published artifact" -- pre-bundling is skipped for that platform.
pg_os_arch() {
  case "$1/$2" in
    linux/amd64)   echo "linux amd64" ;;
    linux/arm64)   echo "linux arm64v8" ;;
    windows/amd64) echo "windows amd64" ;;
    *)             echo "" ;;
  esac
}

for platform in "${PLATFORMS[@]}"; do
  goos=${platform%%/*}
  goarch=${platform##*/}
  suffix=""
  [[ "$goos" == "windows" ]] && suffix=".exe"

  ok "=== $goos/$goarch ==="
  stage_name="owpengram-server-$VERSION-$goos-$goarch"
  stage_dir="$OUT_DIR/staging/$stage_name"
  rm -rf "$stage_dir"
  mkdir -p "$stage_dir/bin" "$stage_dir/data" "$stage_dir/deploy"

  ok "Building binaries..."
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags="-s -w" -o "$stage_dir/bin/owpengram-server$suffix" ./cmd/telesrv
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags="-s -w" -o "$stage_dir/bin/owpengram-admin-panel$suffix" ./cmd/telesrv-admin
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags="-s -w" -o "$stage_dir/owpengram-ctl$suffix" ./cmd/telesrv-ctl

  ok "Assembling archive contents..."
  cp .env.example "$stage_dir/.env.example"
  cp release/README.md "$stage_dir/README.md"
  cp deploy/docker-compose.yml "$stage_dir/deploy/docker-compose.yml"
  cp -r data/langpack "$stage_dir/data/langpack"
  cp -r data/sticker-seed "$stage_dir/data/sticker-seed"
  if [[ "$goos" == "windows" ]]; then
    cp release/start.bat "$stage_dir/start.bat"
  else
    cp release/start.sh "$stage_dir/start.sh"
    chmod +x "$stage_dir/start.sh" "$stage_dir/owpengram-ctl" "$stage_dir/bin/owpengram-server" "$stage_dir/bin/owpengram-admin-panel"
  fi

  if [[ "$SKIP_PGCACHE" -eq 0 ]]; then
    read -r pg_os pg_arch <<<"$(pg_os_arch "$goos" "$goarch")"
    if [[ -n "$pg_os" ]]; then
      ok "Pre-bundling embedded PostgreSQL ($pg_os/$pg_arch)..."
      jar_url="https://repo1.maven.org/maven2/io/zonky/test/postgres/embedded-postgres-binaries-$pg_os-$pg_arch/$PG_VERSION/embedded-postgres-binaries-$pg_os-$pg_arch-$PG_VERSION.jar"
      tmp_jar=$(mktemp)
      if curl -fsSL "$jar_url" -o "$tmp_jar"; then
        mkdir -p "$stage_dir/data/pgcache"
        unzip -p "$tmp_jar" '*.txz' > "$stage_dir/data/pgcache/embedded-postgres-binaries-$pg_os-$pg_arch-$PG_VERSION.txz"
        ok "Bundled embedded-postgres-binaries-$pg_os-$pg_arch-$PG_VERSION.txz"
      else
        warn "Could not pre-bundle embedded PostgreSQL for $pg_os/$pg_arch -- portable edition will download it on first start instead."
      fi
      rm -f "$tmp_jar"
    else
      warn "No published embedded-PostgreSQL binaries for $goos/$goarch -- portable edition will download them on first start instead."
    fi
  fi

  ok "Packaging..."
  if [[ "$goos" == "windows" ]]; then
    archive="$stage_name.zip"
    command -v zip >/dev/null 2>&1 || die "zip is not installed (needed to package Windows archives) -- install it (e.g. apt install zip / pacman -S zip)"
    rm -f "$OUT_DIR/$archive"
    (cd "$OUT_DIR/staging" && zip -rq "../$archive" "$stage_name")
  else
    archive="$stage_name.tar.gz"
    rm -f "$OUT_DIR/$archive"
    (cd "$OUT_DIR/staging" && tar -czf "../$archive" "$stage_name")
  fi
  ok "-> $OUT_DIR/$archive"

  # The archive above already has everything this held -- no reason to
  # leave a second, uncompressed copy (bin/ alone can be 100MB+) lying
  # around after every run.
  rm -rf "$stage_dir"
done

rmdir "$OUT_DIR/staging" 2>/dev/null || true
ok "Done. Archives are in $OUT_DIR/."
