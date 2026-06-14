#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
COMPOSE_FILE="${ROOT_DIR}/compose.fips-local-test.yml"
DEFAULT_ENV_FILE="${ROOT_DIR}/docs/fips-local-test.env.example"
ENV_FILE="${FIPS_LOCAL_TEST_ENV_FILE:-$DEFAULT_ENV_FILE}"
PROJECT_NAME="${FIPS_LOCAL_TEST_PROJECT_NAME:-wrapster-fips-local-test}"
COMMAND=""

usage() {
  cat <<'EOF'
Usage: scripts/test-fips-compose.sh [options] <command>

Commands:
  up      Build and start the local FIPS test stack.
  smoke   Run connectivity and media-path assertions.
  status  Show compose status and peer summaries.
  logs    Follow compose logs for all services.
  down    Stop and remove the local FIPS test stack.

Options:
  --env-file <file>   Env file passed to docker compose (default: docs/fips-local-test.env.example)
  --project <name>    Compose project name (default: wrapster-fips-local-test)
  -h, --help          Show this help text.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    up|smoke|status|logs|down)
      if [ -n "$COMMAND" ]; then
        echo "Only one command can be used at a time." >&2
        usage
        exit 64
      fi
      COMMAND="$1"
      shift
      ;;
    --env-file)
      shift
      [ "$#" -gt 0 ] || { echo "missing value for --env-file" >&2; exit 64; }
      ENV_FILE="$1"
      shift
      ;;
    --project)
      shift
      [ "$#" -gt 0 ] || { echo "missing value for --project" >&2; exit 64; }
      PROJECT_NAME="$1"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage
      exit 64
      ;;
  esac
done

if [ -z "$COMMAND" ]; then
  COMMAND="status"
fi

if [ ! -f "$ENV_FILE" ]; then
  echo "Env file not found: $ENV_FILE" >&2
  echo "Copy docs/fips-local-test.env.example and set concrete values." >&2
  exit 1
fi

compose() {
  docker compose --env-file "$ENV_FILE" -p "$PROJECT_NAME" -f "$COMPOSE_FILE" "$@"
}

compose_exec() {
  compose exec -T "$@"
}

compose_exec_wrapster() {
  compose_exec wrapster "$@"
}

compose_exec_client() {
  compose_exec fips-test-client "$@"
}

assert_env_loaded() {
  set -a
  # shellcheck disable=SC1090
  . "$ENV_FILE"
  set +a

  required=(
    FIPS_PUBLIC_NSEC
    FIPS_HOME_NSEC
    FIPS_PUBLIC_NPUB
    FIPS_HOME_NPUB
    MEDIA_CONNECTOR_TOKEN
    CONNECTOR_SHARED_TOKEN
  )

  for key in "${required[@]}"; do
    value="${!key:-}"
    if [ -z "$value" ]; then
      echo "Missing required env var: $key" >&2
      exit 1
    fi
  done

  for key in FIPS_PUBLIC_NSEC FIPS_HOME_NSEC FIPS_PUBLIC_NPUB FIPS_HOME_NPUB; do
    value="${!key}"
    case "$value" in
      REPLACE_WITH_* )
        echo "Replace test placeholders in $ENV_FILE for $key before running." >&2
        exit 1
        ;;
    esac
  done
}

retry_compose_exec() {
  local service="$1"
  local description="$2"
  shift 2

  local output=""
  local attempt=1
  local max_attempts="${FIPS_TEST_RETRY_ATTEMPTS:-30}"
  local delay_seconds="${FIPS_TEST_RETRY_DELAY_SECONDS:-2}"

  while true; do
    if output="$(compose_exec "$service" "$@" 2>&1)"; then
      printf '%s\n' "$output"
      return 0
    fi

    if [ "$attempt" -ge "$max_attempts" ]; then
      echo "${description} failed after ${max_attempts} attempts." >&2
      printf '%s\n' "$output" >&2
      return 1
    fi

    echo "Waiting for ${description} (attempt ${attempt}/${max_attempts})." >&2
    attempt=$((attempt + 1))
    sleep "$delay_seconds"
  done
}

service_peers() {
  local service="$1"
  local expected="${2:-}"

  local output
  if ! output="$(retry_compose_exec "$service" "peer list on ${service}" fipsctl show peers)"; then
    echo "Peer check failed for $service" >&2
    return 1
  fi

  if [ -n "$expected" ] && ! printf '%s\n' "$output" | grep -qF "$expected"; then
    echo "Expected peer marker '$expected' not found in $service peer list." >&2
    printf '%s\n' "$output" >&2
    return 1
  fi

  if ! printf '%s\n' "$output" | grep -qiE 'connected|active|up|running'; then
    echo "Peer list for $service did not report a connected state for '$expected'." >&2
    printf '%s\n' "$output" >&2
    return 1
  fi

  printf '%s\n' "$output"
}

assert_alias_dns() {
  local output
  if ! output="$(retry_compose_exec wrapster 'home side DNS for home-media.fips' sh -lc 'getent hosts home-media.fips 2>/dev/null')"; then
    echo "DNS assertion failed: home-media.fips not resolvable from public side." >&2
    return 1
  fi

  if ! printf '%s\n' "$output" | grep -qF "home-media.fips"; then
    echo "home-media.fips did not appear in DNS output." >&2
    printf '%s\n' "$output" >&2
    return 1
  fi
  printf '%s\n' "$output"
}

assert_connector_status() {
  local response

  if [ -n "${MEDIA_CONNECTOR_TOKEN:-}" ]; then
    if ! response="$(retry_compose_exec fips-test-client 'connector status via sidecar alias' sh -lc "curl -fsS -H 'Authorization: Bearer ${MEDIA_CONNECTOR_TOKEN}' http://home-media.fips:22000/connector/api/status")"; then
      echo "Connector status endpoint not reachable via sidecar alias home-media.fips." >&2
      return 1
    fi
  else
    if ! response="$(retry_compose_exec fips-test-client 'connector status via sidecar alias' sh -lc "curl -fsS http://home-media.fips:22000/connector/api/status")"; then
      echo "Connector status endpoint not reachable via sidecar alias home-media.fips." >&2
      return 1
    fi
  fi

  if ! printf '%s\n' "$response" | grep -q '"services"'; then
    echo "Unexpected connector status payload:" >&2
    printf '%s\n' "$response" >&2
    return 1
  fi

  printf '%s\n' "$response"
}

media_status_url() {
  printf '%s' 'http://127.0.0.1:5542/media/api/status'
}

build_media_status_authorization() {
  if [ -n "${MEDIA_STATUS_AUTHORIZATION:-}" ]; then
    return 0
  fi

  if [ -z "${MEDIA_SMOKE_NSEC:-}" ]; then
    return 2
  fi

  local url header
  url="$(media_status_url)"
  if ! header="$(cd "${ROOT_DIR}/wrapster" && go run "${ROOT_DIR}/scripts/gen-nip98-auth/main.go" --nsec "${MEDIA_SMOKE_NSEC}" --url "${url}" --method GET)"; then
    echo "Failed to generate NIP-98 media status authorization." >&2
    return 1
  fi

  MEDIA_STATUS_AUTHORIZATION="${header}"
  export MEDIA_STATUS_AUTHORIZATION
  return 0
}

assert_wrapster_media_status() {
  if [ -z "${MEDIA_STATUS_AUTHORIZATION:-}" ]; then
    return 2
  fi

  local response url
  url="$(media_status_url)"
  if ! response="$(retry_compose_exec fips-test-client 'public media status endpoint' sh -lc "curl -fsS -H \"Authorization: ${MEDIA_STATUS_AUTHORIZATION}\" ${url}")"; then
    echo "Public-side media status check failed." >&2
    return 1
  fi

  if ! printf '%s\n' "$response" | grep -q '"connector"'; then
    echo "Public-side media status payload did not include connector metadata." >&2
    printf '%s\n' "$response" >&2
    return 1
  fi

  if ! printf '%s\n' "$response" | grep -q '"transport"'; then
    echo "Public-side media status payload did not include transport metadata." >&2
    printf '%s\n' "$response" >&2
    return 1
  fi

  printf '%s\n' "$response"
}

assert_critical_services_running() {
  local service
  local failed=0

  for service in fips-public wrapster; do
    if ! compose ps --status running --format '{{.Service}}' | grep -qxF "$service"; then
      echo "Required service is not running: $service" >&2
      compose logs --no-color --tail 40 "$service" >&2 || true
      failed=1
    fi
  done

  if [ "$failed" -ne 0 ]; then
    echo "Stack startup failed. Fix the errors above and rerun up." >&2
    return 1
  fi
}

cmd_up() {
  assert_env_loaded
  compose up -d --build
  assert_critical_services_running
  cmd_status
}

cmd_smoke() {
  local media_result=0

  assert_env_loaded

  echo "== public side peers =="
  service_peers fips-public "${FIPS_HOME_NPUB:-}"

  echo "== home side peers =="
  service_peers fips-home "${FIPS_PUBLIC_NPUB:-}"

  echo "== home-media.fips resolution from public side =="
  assert_alias_dns

  echo "== connector status via sidecar alias =="
  assert_connector_status

  echo "== public Wrapster media status via /media/api/status =="
  if ! build_media_status_authorization; then
    auth_result=$?
    if [ "$auth_result" -ne 2 ]; then
      echo "Failed to prepare media status authorization." >&2
      return 1
    fi
  fi

  media_result=2
  if assert_wrapster_media_status; then
    media_result=0
  else
    media_result=$?
  fi
  if [ "$media_result" -eq 0 ]; then
    echo "Wrapster media status check passed."
  elif [ "$media_result" -eq 2 ]; then
    echo "Wrapster media status check skipped (set MEDIA_SMOKE_NSEC and MEDIA_GRANT_PUBKEYS in $ENV_FILE, or MEDIA_STATUS_AUTHORIZATION to override)."
  else
    echo "Wrapster media status check failed." >&2
    return 1
  fi

  echo "Smoke checks complete."
}

cmd_status() {
  compose ps
  compose_exec fips-public fipsctl show status || true
  compose_exec fips-home fipsctl show status || true
  echo "---"
  service_peers fips-public "${FIPS_HOME_NPUB:-}"
  echo "---"
  service_peers fips-home "${FIPS_PUBLIC_NPUB:-}"
}

cmd_logs() {
  compose logs -f
}

cmd_down() {
  compose down --remove-orphans
}

case "$COMMAND" in
  up)
    cmd_up
    ;;
  smoke)
    cmd_smoke
    ;;
  status)
    assert_env_loaded
    cmd_status
    ;;
  logs)
    cmd_logs
    ;;
  down)
    cmd_down
    ;;
esac
