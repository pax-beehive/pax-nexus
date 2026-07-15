#!/bin/sh
set -eu

: "${PAXM_AGENT_ID:?PAXM_AGENT_ID is required}"
: "${PAXM_PROVIDER_TYPE:=team-memory}"
: "${TEAM_MEMORY_BASE_URL:=http://team-memory:8080}"
: "${PAXM_USER_ID:=eval-owner}"
: "${PAXM_RECALL_ENABLED:=1}"
: "${PAXM_WRITE_ENABLED:=1}"
: "${TEAM_MEMORY_PROVIDER_TIMEOUT:=90s}"
: "${TEAM_MEMORY_REQUEST_TIMEOUT:=60s}"

config_root="/tmp/eval-${PAXM_AGENT_ID}"
paxm_config="${config_root}/paxm.yaml"
opencode_config="${config_root}/opencode"
mkdir -p "${opencode_config}/plugins" "${config_root}/data"
cp /opt/paxm/paxm.js "${opencode_config}/plugins/paxm.js"

case "${PAXM_PROVIDER_TYPE}" in
  team-memory)
    : "${TEAM_MEMORY_API_KEY:?TEAM_MEMORY_API_KEY is required for team-memory}"
    provider_config="type: jsonrpc
    enabled: true
    transport: stdio
    command: /usr/local/bin/paxm-team-memory-provider
    timeout: ${TEAM_MEMORY_PROVIDER_TIMEOUT}
    env:
      TEAM_MEMORY_BASE_URL: \"${TEAM_MEMORY_BASE_URL}\"
      TEAM_MEMORY_API_KEY: \"${TEAM_MEMORY_API_KEY}\"
      PAXM_USER_ID: \"${PAXM_USER_ID}\"
      PAXM_AGENT_ID: \"${PAXM_AGENT_ID}\"
      TEAM_MEMORY_REQUEST_TIMEOUT: \"${TEAM_MEMORY_REQUEST_TIMEOUT}\""
    ;;
  mem0)
    : "${MEM0_BASE_URL:=http://mem0:8000}"
    : "${MEM0_RUN_ID:?MEM0_RUN_ID is required for eval isolation}"
    provider_config="type: mem0
    enabled: true
    base_url: \"${MEM0_BASE_URL}\"
    api_key: \"${MEM0_API_KEY:-}\"
    user_id: \"${PAXM_USER_ID}\"
    run_id: \"${MEM0_RUN_ID}\""
    ;;
  *)
    echo "unsupported PAXM_PROVIDER_TYPE: ${PAXM_PROVIDER_TYPE}" >&2
    exit 1
    ;;
esac

cat > "${paxm_config}" <<EOF
version: 1
providers:
  memory:
    ${provider_config}
recall_profiles:
  default:
    providers:
      - name: memory
        required: true
    max_results: 5
  passive:
    providers:
      - name: memory
        required: true
    max_results: 5
    thresholds:
      min_relevance: 0.75
      min_score: 0.75
  passive_initial:
    providers:
      - name: memory
        required: true
    max_results: 5
    thresholds:
      min_relevance: 0.35
      min_score: 0.35
write_profiles:
  ltm:
    tier: ltm
    providers:
      - name: memory
        required: true
agents:
  opencode:
    enabled: true
    agent_id: "${PAXM_AGENT_ID}"
    hooks:
      user_input:
        recall:
          enabled: true
          profile: passive
          query_template: "{{ .prompt }}"
          max_results: 5
          insertion:
            min_score: 0.8
            max_items: 5
            require_query_terms: false
      turn_end:
        write:
          enabled: true
          profile: ltm
          template: "{{ .safe_text }}"
          mode: turn_end
          buffer:
            enabled: true
            flush: true
capture_queue:
  path: "${config_root}/data/capture.sqlite"
  retry_min: 100ms
  max_attempts: 3
EOF

cat > "${opencode_config}/opencode.json" <<EOF
{
  "\$schema": "https://opencode.ai/config.json",
  "permission": {
    "*": "deny",
    "read": "allow",
    "glob": "allow",
    "grep": "allow"
  },
  "tools": {
    "*": false,
    "read": true,
    "glob": true,
    "grep": true
  }
}
EOF

export OPENCODE_CONFIG_DIR="${opencode_config}"
export OPENCODE_DISABLE_AUTOUPDATE=true
export OPENCODE_DISABLE_CLAUDE_CODE=true
export OPENCODE_DISABLE_LSP_DOWNLOAD=true
export PAXM_BINARY=/usr/local/bin/paxm
export PAXM_CONFIG="${paxm_config}"
export PAXM_OPENCODE_RECALL="${PAXM_RECALL_ENABLED}"
export PAXM_OPENCODE_WRITE="${PAXM_WRITE_ENABLED}"

if [ "$#" -eq 0 ]; then
  exec sleep infinity
fi

set +e
opencode "$@"
status=$?
set -e
if [ "${PAXM_WRITE_ENABLED}" = "1" ]; then
  paxm --config "${paxm_config}" __hook-control --shutdown
fi
if [ "${PAXM_EVAL_DIAGNOSTICS:-0}" = "1" ]; then
  paxm --config "${paxm_config}" logs --tail 100 --json >&2 || true
fi
exit "${status}"
