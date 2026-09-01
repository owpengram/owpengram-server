#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: ./scripts/start-docker.sh [options]

Initialization options (used only when deploy/docker/.env is absent):
  --advertise-ip IP
  --public-base-url URL
  --public-web-base-url URL
  --admin-bind-ip IP
  --host-network          Direct host networking for the monolith (default).
  --bridge-network        Docker port publishing compatibility mode.
  --allow-insecure-development-auth

Other options:
  --build                 Build local images instead of pulling published images.
  --help
EOF
}

script_dir=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(CDPATH='' cd -- "$script_dir/.." && pwd)
docker_dir="$repo_root/deploy/docker"
compose_path="$docker_dir/compose.yaml"
env_path="$docker_dir/.env"
generator_path="$script_dir/new-docker-env.sh"

advertise_ip=
public_base_url=
public_web_base_url=
admin_bind_ip=
network_mode=
allow_insecure=false
build=false
initialization_options=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --advertise-ip) [[ $# -ge 2 ]] || { printf '%s requires a value\n' "$1" >&2; exit 1; }; advertise_ip=$2; initialization_options=true; shift 2 ;;
    --public-base-url) [[ $# -ge 2 ]] || { printf '%s requires a value\n' "$1" >&2; exit 1; }; public_base_url=$2; initialization_options=true; shift 2 ;;
    --public-web-base-url) [[ $# -ge 2 ]] || { printf '%s requires a value\n' "$1" >&2; exit 1; }; public_web_base_url=$2; initialization_options=true; shift 2 ;;
    --admin-bind-ip) [[ $# -ge 2 ]] || { printf '%s requires a value\n' "$1" >&2; exit 1; }; admin_bind_ip=$2; initialization_options=true; shift 2 ;;
    --host-network) network_mode=host; initialization_options=true; shift ;;
    --bridge-network) network_mode=bridge; initialization_options=true; shift ;;
    --allow-insecure-development-auth) allow_insecure=true; initialization_options=true; shift ;;
    --build) build=true; shift ;;
    --help|-h) usage; exit 0 ;;
    *) printf 'start-docker: unknown argument: %s\n' "$1" >&2; exit 1 ;;
  esac
done

if [[ ! -f "$env_path" ]]; then
  [[ -n "$advertise_ip" ]] || advertise_ip=127.0.0.1
  generator_args=(--advertise-ip "$advertise_ip")
  [[ -z "$public_base_url" ]] || generator_args+=(--public-base-url "$public_base_url")
  [[ -z "$public_web_base_url" ]] || generator_args+=(--public-web-base-url "$public_web_base_url")
  [[ -z "$admin_bind_ip" ]] || generator_args+=(--admin-bind-ip "$admin_bind_ip")
  [[ "$network_mode" != host ]] || generator_args+=(--host-network)
  [[ "$network_mode" != bridge ]] || generator_args+=(--bridge-network)
  [[ "$allow_insecure" = false ]] || generator_args+=(--allow-insecure-development-auth)
  "$generator_path" "${generator_args[@]}"
elif [[ "$initialization_options" = true ]]; then
  printf 'start-docker: deploy/docker/.env already exists; initialization options were ignored to preserve credentials and deployment identity.\n' >&2
fi

env_value() { awk -F= -v key="$1" '$1 == key { print substr($0, length(key) + 2); exit }' "$env_path"; }

if [[ "$(env_value TELESRV_DEPLOYMENT_PROFILE)" != main-monolith-v1 ]]; then
  printf 'start-docker: %s belongs to an older or different topology; move it aside and rerun so credentials are regenerated safely.\n' "$env_path" >&2
  exit 1
fi

configured_host_network=$(env_value TELESRV_SERVER_HOST_NETWORK)
[[ -n "$configured_host_network" ]] || configured_host_network=true
case "$configured_host_network" in true|false) ;; *) printf 'start-docker: TELESRV_SERVER_HOST_NETWORK must be true or false\n' >&2; exit 1 ;; esac

compose=(docker compose --project-directory "$docker_dir" --env-file "$env_path" --file "$compose_path")
if [[ "$configured_host_network" = false ]]; then compose+=(--file "$docker_dir/compose.bridge-network.yaml"); fi
"${compose[@]}" version >/dev/null
"${compose[@]}" config --quiet

if [[ "$build" = true ]]; then "${compose[@]}" build --pull; else "${compose[@]}" pull; fi
if ! "${compose[@]}" up --detach --no-build --wait --wait-timeout 600; then
  "${compose[@]}" logs --no-color --tail 160 || true
  exit 1
fi

"${compose[@]}" ps --all
printf 'gramsrv main Docker stack is ready. Configuration: %s\n' "$env_path"
if [[ "$(env_value TELESRV_PHONE_CODE_DELIVERY_PROVIDER)" = development ]]; then printf 'Development login code: %s\n' "$(env_value TELESRV_DEV_AUTH_CODE)"; fi
printf 'MTProto: %s:%s\n' "$(env_value TELESRV_ADVERTISE_IP)" "$(env_value TELESRV_SERVER_PORT)"
if [[ "$(env_value TELESRV_TURN_ENABLE)" = true ]]; then
  printf 'TURN/STUN: udp://%s:%s\n' "$(env_value TELESRV_TURN_ADVERTISE_IP)" "$(env_value TELESRV_TURN_UDP_PORT)"
  turn_relay_max_port=$(env_value TELESRV_TURN_RELAY_MAX_PORT)
  if [[ "$configured_host_network" = false ]]; then turn_relay_max_port=$(env_value TELESRV_TURN_BRIDGE_RELAY_MAX_PORT); fi
  printf 'TURN relay UDP range: %s-%s\n' "$(env_value TELESRV_TURN_RELAY_MIN_PORT)" "$turn_relay_max_port"
fi
admin_host=$(env_value TELESRV_ADMIN_BIND_IP)
if [[ "$admin_host" = 0.0.0.0 || "$admin_host" = :: ]]; then admin_host=$(env_value TELESRV_ADVERTISE_IP); fi
[[ "$admin_host" != *:* ]] || admin_host="[$admin_host]"
printf 'Admin UI: http://%s:%s (password is stored in %s)\n' "$admin_host" "$(env_value TELESRV_ADMIN_PORT)" "$env_path"
