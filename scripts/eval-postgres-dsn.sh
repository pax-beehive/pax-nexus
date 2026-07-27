#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_root"

usage() {
  echo "usage: $0 <compose-project> <compose-file>" >&2
  exit 2
}

project="${1:-}"
compose_file="${2:-}"
[ -n "$project" ] || usage
[ -n "$compose_file" ] || usage

# Overridable command seam. Tests point this at a stub script so the port
# mapping can be exercised without Docker or the network. Default behaviour
# is unchanged: the real docker CLI.
EVAL_DOCKER_CMD="${EVAL_DOCKER_CMD:-docker}"

# compose.yaml publishes Postgres as "0:5432" so concurrent eval stacks get
# an ephemeral host port instead of colliding on a fixed one. Resolve the
# port docker actually assigned rather than assuming a pinned value.
mapping=""
if ! mapping=$($EVAL_DOCKER_CMD compose -p "$project" -f "$compose_file" port postgres 5432 2>&1); then
  echo "eval-postgres-dsn: failed to resolve postgres port for compose project '${project}' (file ${compose_file}): ${mapping}" >&2
  exit 1
fi

# Only the first published address is used; docker may print one line per
# address family (e.g. IPv4 and IPv6) but they share the same host port.
first_line=$(printf '%s\n' "$mapping" | head -n 1)

# Strip everything up to and including the LAST colon. A naive first-colon
# cut mangles IPv6-style mappings such as "[::]:56001".
port=${first_line##*:}

case "$port" in
  ''|*[!0-9]*)
    echo "eval-postgres-dsn: could not parse a port from docker compose port output for project '${project}' (file ${compose_file}): '${first_line}'" >&2
    exit 1
    ;;
esac

# Credentials match evals/v2/compose.yaml (POSTGRES_USER / POSTGRES_PASSWORD
# / POSTGRES_DB all set to team_memory).
echo "postgres://team_memory:team_memory@127.0.0.1:${port}/team_memory?sslmode=disable"
