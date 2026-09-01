#!/bin/sh
set -eu

umask 077

command_name="${1##*/}"

require_secret() {
  name="$1"
  value="$(printenv "$name" 2>/dev/null || true)"
  normalized="$(printf '%s' "$value" | tr '[:upper:]' '[:lower:]')"
  case "$normalized" in
    ""|*changeme*|*change-me*|*replace-me*)
      echo "telesrv: required secret $name is missing or still uses a placeholder" >&2
      exit 64
      ;;
  esac
}

require_value() {
  name="$1"
  value="$(printenv "$name" 2>/dev/null || true)"
  normalized="$(printf '%s' "$value" | tr '[:upper:]' '[:lower:]')"
  case "$normalized" in
    ""|*changeme*|*change-me*|*replace-me*)
      echo "telesrv: required setting $name is missing or still uses a placeholder" >&2
      exit 64
      ;;
  esac
}

initialize_server_key() {
  private_key="${TELESRV_RSA_KEY:-/var/lib/telesrv/server_rsa.pem}"
  identity_mode="$(printf '%s' "${TELESRV_RSA_IDENTITY_MODE:-generated}" | tr '[:upper:]' '[:lower:]')"
  embedded_private_key=/usr/share/telesrv/keys/test-server-rsa.pem.b64
  key_dir=$(dirname -- "$private_key")

  mkdir -p "$key_dir"
  case "$identity_mode" in
    generated) ;;
    test)
      if [ ! -f "$private_key" ]; then
        if [ ! -r "$embedded_private_key" ]; then
          echo "telesrv: test RSA identity requested, but this image does not contain the published test key; use the server-test target or set TELESRV_RSA_IDENTITY_MODE=generated" >&2
          exit 66
        fi
        temporary_key="$private_key.tmp.$$"
        trap 'rm -f "$temporary_key"' EXIT HUP INT TERM
        base64 -d "$embedded_private_key" >"$temporary_key"
        chmod 0600 "$temporary_key"
        mv "$temporary_key" "$private_key"
        trap - EXIT HUP INT TERM
        echo "telesrv: WARNING using the published main test RSA identity; its private key is public" >&2
      fi
      ;;
    *)
      echo "telesrv: TELESRV_RSA_IDENTITY_MODE must be test or generated" >&2
      exit 64
      ;;
  esac

  if [ -f "$private_key" ]; then
    if ! openssl rsa -in "$private_key" -check -noout >/dev/null 2>&1; then
      echo "telesrv: $private_key is not a valid RSA private key" >&2
      exit 65
    fi
    chmod 0600 "$private_key"
  fi
}

case "$command_name" in
  telesrv)
    require_value TELESRV_ADVERTISE_IP
    require_value TELESRV_PUBLIC_BASE_URL
    require_value TELESRV_PUBLIC_WEB_BASE_URL
    require_secret TELESRV_POSTGRES_DSN
    require_secret TELESRV_REDIS_PASSWORD
    require_secret TELESRV_ADMIN_API_TOKEN
    require_secret TELESRV_TURN_SECRET
    initialize_server_key
    ;;
  telesrv-admin)
    require_secret TELESRV_POSTGRES_DSN
    require_secret TELESRV_ADMIN_API_TOKEN
    require_secret TELESRV_ADMIN_SESSION_KEY
    admin_password="$(printenv TELESRV_ADMIN_UI_PASSWORD 2>/dev/null || true)"
    admin_token="$(printenv TELESRV_ADMIN_UI_TOKEN 2>/dev/null || true)"
    if [ -n "$admin_password" ]; then
      require_secret TELESRV_ADMIN_UI_PASSWORD
    elif [ -n "$admin_token" ]; then
      require_secret TELESRV_ADMIN_UI_TOKEN
    else
      echo "telesrv: TELESRV_ADMIN_UI_PASSWORD or TELESRV_ADMIN_UI_TOKEN is required" >&2
      exit 64
    fi
    ;;
esac

exec "$@"
