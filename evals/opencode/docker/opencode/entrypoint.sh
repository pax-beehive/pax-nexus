#!/bin/sh
set -eu

: "${PAXM_AGENT_ID:?PAXM_AGENT_ID is required}"
: "${PAXM_PROVIDER_TYPE:=team-memory}"
: "${TEAM_MEMORY_BASE_URL:=http://team-memory:8080}"
: "${PAXM_USER_ID:=eval-owner}"
: "${PAXM_PROVIDER_USER_ID:=${PAXM_USER_ID}}"
: "${PAXM_PROVIDER_AGENT_ID:=${PAXM_AGENT_ID}}"
: "${PAXM_RECALL_ENABLED:=1}"
: "${PAXM_WRITE_ENABLED:=1}"
: "${TEAM_MEMORY_PROVIDER_TIMEOUT:=90s}"
: "${TEAM_MEMORY_REQUEST_TIMEOUT:=60s}"
: "${PAXM_PASSIVE_MIN_RELEVANCE:=-1}"
: "${PAXM_PASSIVE_MIN_SCORE:=-1}"
: "${PAXM_PASSIVE_PROVIDER_TIMEOUT:=2s}"
: "${PAXM_INSERTION_MIN_SCORE:=0}"
: "${MEM0_SCORE_SEMANTICS:=distance}"
: "${MEM0_SEARCH_SCOPE_PAYLOAD:=top_level}"
: "${PAXM_EXPECTED_VERSION:=v0.1.29}"
: "${PAXM_BINARY:=/usr/local/bin/paxm}"
: "${PAXM_EVAL_CONSUMER_POLICY:=0}"
: "${PAXM_EVAL_RECALL_MODE:=passive}"
: "${PAXM_ACTIVE_RECALL_MAX_CALLS:=2}"

if [ "${PAXM_PASSIVE_MIN_RELEVANCE}" = "0" ] && [ "${PAXM_PASSIVE_MIN_SCORE}" = "0" ]; then
  echo "passive recall thresholds cannot both be 0 because paxm normalizes the zero-value profile to its defaults; use -1 to preserve raw top-k" >&2
  exit 1
fi

: "${PAXM_CONFIG_ROOT:=/tmp/eval-${PAXM_AGENT_ID}}"
: "${PAXM_PLUGIN_SOURCE:=/opt/paxm/paxm.js}"
: "${PAXM_ACTIVE_RECALL_TOOL_SOURCE:=/opt/team-memory/active_recall.ts}"
config_root="${PAXM_CONFIG_ROOT}"
paxm_config="${config_root}/paxm.yaml"
opencode_config="${config_root}/opencode"
mkdir -p "${opencode_config}/plugins" "${config_root}/data"
cp "${PAXM_PLUGIN_SOURCE}" "${opencode_config}/plugins/paxm.js"

case "${PAXM_PROVIDER_TYPE}" in
  team-memory)
    : "${TEAM_MEMORY_API_KEY:?TEAM_MEMORY_API_KEY is required for team-memory}"
    provider_entries="  memory:
    type: jsonrpc
    enabled: true
    transport: stdio
    command: /usr/local/bin/paxm-team-memory-provider
    timeout: ${TEAM_MEMORY_PROVIDER_TIMEOUT}
    env:
      TEAM_MEMORY_BASE_URL: \"${TEAM_MEMORY_BASE_URL}\"
      TEAM_MEMORY_API_KEY: \"${TEAM_MEMORY_API_KEY}\"
      PAXM_USER_ID: \"${PAXM_PROVIDER_USER_ID}\"
      PAXM_AGENT_ID: \"${PAXM_PROVIDER_AGENT_ID}\"
      TEAM_MEMORY_REQUEST_TIMEOUT: \"${TEAM_MEMORY_REQUEST_TIMEOUT}\""
    default_provider_entries="      - name: memory
        required: true"
    passive_provider_entries="${default_provider_entries}
        timeout: ${PAXM_PASSIVE_PROVIDER_TIMEOUT}"
    write_provider_entries="${default_provider_entries}"
    ;;
  mem0)
    : "${MEM0_BASE_URL:=http://mem0:8000}"
    : "${MEM0_RUN_ID:?MEM0_RUN_ID is required for eval isolation}"
    provider_entries="  memory:
    type: mem0
    enabled: true
    base_url: \"${MEM0_BASE_URL}\"
    api_key: \"${MEM0_API_KEY:-}\"
    user_id: \"${PAXM_PROVIDER_USER_ID}\"
    agent_id: \"${PAXM_PROVIDER_AGENT_ID}\"
    run_id: \"${MEM0_RUN_ID}\"
    score_semantics: \"${MEM0_SCORE_SEMANTICS}\"
    search_scope_payload: \"${MEM0_SEARCH_SCOPE_PAYLOAD}\""
    default_provider_entries="      - name: memory
        required: true"
    passive_provider_entries="${default_provider_entries}
        timeout: ${PAXM_PASSIVE_PROVIDER_TIMEOUT}"
    write_provider_entries="${default_provider_entries}"
    ;;
  team-memory-sqlite)
    : "${TEAM_MEMORY_API_KEY:?TEAM_MEMORY_API_KEY is required for team-memory-sqlite}"
    : "${PAXM_PRIVATE_SQLITE_PATH:?PAXM_PRIVATE_SQLITE_PATH is required for team-memory-sqlite}"
    provider_entries="  private:
    type: sqlite
    enabled: true
    path: \"${PAXM_PRIVATE_SQLITE_PATH}\"
  team:
    type: jsonrpc
    enabled: true
    transport: stdio
    command: /usr/local/bin/paxm-team-memory-provider
    timeout: ${TEAM_MEMORY_PROVIDER_TIMEOUT}
    env:
      TEAM_MEMORY_BASE_URL: \"${TEAM_MEMORY_BASE_URL}\"
      TEAM_MEMORY_API_KEY: \"${TEAM_MEMORY_API_KEY}\"
      PAXM_USER_ID: \"${PAXM_PROVIDER_USER_ID}\"
      PAXM_AGENT_ID: \"${PAXM_PROVIDER_AGENT_ID}\"
      TEAM_MEMORY_REQUEST_TIMEOUT: \"${TEAM_MEMORY_REQUEST_TIMEOUT}\""
    default_provider_entries="      - name: private
        required: true
      - name: team
        required: true"
    passive_provider_entries="      - name: private
        required: true
        timeout: ${PAXM_PASSIVE_PROVIDER_TIMEOUT}
      - name: team
        required: true
        timeout: ${PAXM_PASSIVE_PROVIDER_TIMEOUT}"
    write_provider_entries="${default_provider_entries}"
    ;;
  *)
    echo "unsupported PAXM_PROVIDER_TYPE: ${PAXM_PROVIDER_TYPE}" >&2
    exit 1
    ;;
esac

cat > "${paxm_config}" <<EOF
version: 1
providers:
${provider_entries}
recall_profiles:
  default:
    providers:
${default_provider_entries}
    max_results: 5
  passive:
    providers:
${passive_provider_entries}
    max_results: 5
    thresholds:
      min_relevance: ${PAXM_PASSIVE_MIN_RELEVANCE}
      min_score: ${PAXM_PASSIVE_MIN_SCORE}
  passive_initial:
    providers:
${passive_provider_entries}
    max_results: 5
    thresholds:
      min_relevance: ${PAXM_PASSIVE_MIN_RELEVANCE}
      min_score: ${PAXM_PASSIVE_MIN_SCORE}
write_profiles:
  ltm:
    tier: ltm
    providers:
${write_provider_entries}
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
            min_score: ${PAXM_INSERTION_MIN_SCORE}
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

agent_config=""
permission_config='    "*": "deny",
    "read": "allow",
    "glob": "allow",
    "grep": "allow"'
tools_config='    "*": false,
    "read": true,
    "glob": true,
    "grep": true'
if [ "${PAXM_EVAL_CONSUMER_POLICY}" = "1" ]; then
  case "${PAXM_EVAL_RECALL_MODE}" in
    direct)
      recall_policy='Use only the conversation passages supplied in the user prompt as evidence.
Do not search, inspect, or mention the workspace. Do not use tools. If the supplied
passages do not contain the answer, state directly that the information is unavailable.'
      ;;
    passive)
      recall_policy='Use recalled memory context as the only evidence. The consumer workspace
intentionally contains no source messages. Do not search, inspect, or mention the workspace.
Do not describe or propose searches, tool calls, or attempts. If recalled memory
does not contain the answer, state directly that the information is unavailable.'
      ;;
    hybrid)
      case "${PAXM_ACTIVE_RECALL_MAX_CALLS}" in
        1|2) ;;
        *)
          echo "PAXM_ACTIVE_RECALL_MAX_CALLS must be between 1 and 2" >&2
          exit 1
          ;;
      esac
      mkdir -p "${opencode_config}/tools"
      cp "${PAXM_ACTIVE_RECALL_TOOL_SOURCE}" "${opencode_config}/tools/active_recall.ts"
      recall_policy="Use passively recalled memory context first. If it is insufficient, you may call
active_recall with a focused query at most ${PAXM_ACTIVE_RECALL_MAX_CALLS} times. The consumer workspace
intentionally contains no source messages. Do not search, inspect, or mention the workspace,
and do not use any tool other than active_recall. If the available memory evidence does not
contain the answer, state directly that the information is unavailable."
      ;;
	 hint)
	  case "${PAXM_ACTIVE_RECALL_MAX_CALLS}" in
	    1|2) ;;
	    *)
	      echo "PAXM_ACTIVE_RECALL_MAX_CALLS must be between 1 and 2" >&2
	      exit 1
	      ;;
	  esac
	  mkdir -p "${opencode_config}/tools"
	  cp "${PAXM_ACTIVE_RECALL_TOOL_SOURCE}" "${opencode_config}/tools/active_recall.ts"
	  recall_policy="Treat [Recall hint - not evidence] as a navigation instruction, never as factual evidence.
Call active_recall with the hint's exact focused query when and only when such a hint is present,
using at most ${PAXM_ACTIVE_RECALL_MAX_CALLS} calls. The consumer workspace intentionally contains no source
messages. Do not search, inspect, or mention the workspace, and do not use any other tool. Answer only from
evidence returned by passive or active recall; otherwise state that the information is unavailable."
	  ;;
    *)
      echo "unsupported PAXM_EVAL_RECALL_MODE: ${PAXM_EVAL_RECALL_MODE}" >&2
      exit 1
      ;;
  esac
  cat > "${opencode_config}/eval-consumer-prompt.md" <<EOF
# Evaluation consumer policy

${recall_policy}

Answer directly and concisely without explaining your reasoning. Only if the
question requests an exact owner, name, date, time, timestamp, version, count,
or value, require the available evidence to state that exact slot for the same subject.
If that slot is missing, state that the information is unavailable.
For all other question types, answer normally from the available evidence.
EOF
  agent_config='  "agent": {
    "eval-consumer": {
      "mode": "primary",
      "prompt": "{file:./eval-consumer-prompt.md}",
      "permission": {"*": "deny"},
      "tools": {"*": false}
    }
  },'
  permission_config='    "*": "deny"'
  tools_config='    "*": false'
  if [ "${PAXM_EVAL_RECALL_MODE}" = "hybrid" ] || [ "${PAXM_EVAL_RECALL_MODE}" = "hint" ]; then
    agent_config='  "agent": {
    "eval-consumer": {
      "mode": "primary",
      "prompt": "{file:./eval-consumer-prompt.md}",
      "permission": {"*": "deny", "active_recall": "allow"},
      "tools": {"*": false, "active_recall": true}
    }
  },'
    permission_config='    "*": "deny",
    "active_recall": "allow"'
    tools_config='    "*": false,
    "active_recall": true'
  fi
fi

cat > "${opencode_config}/opencode.json" <<EOF
{
  "\$schema": "https://opencode.ai/config.json",
${agent_config}
  "permission": {
${permission_config}
  },
  "tools": {
${tools_config}
  }
}
EOF

export OPENCODE_CONFIG_DIR="${opencode_config}"
export OPENCODE_DISABLE_AUTOUPDATE=true
export OPENCODE_DISABLE_CLAUDE_CODE=true
export OPENCODE_DISABLE_LSP_DOWNLOAD=true
export PAXM_BINARY
export PAXM_CONFIG="${paxm_config}"
export PAXM_ACTIVE_RECALL_STATE_DIR="${config_root}/data/active-recall"
export PAXM_OPENCODE_RECALL="${PAXM_RECALL_ENABLED}"
export PAXM_OPENCODE_WRITE="${PAXM_WRITE_ENABLED}"

if [ "${PAXM_CONFIG_ONLY:-0}" = "1" ]; then
  exit 0
fi

actual_paxm_version="$("${PAXM_BINARY}" version)"
if [ "${actual_paxm_version}" != "${PAXM_EXPECTED_VERSION}" ]; then
  echo "paxm version ${actual_paxm_version} does not match required ${PAXM_EXPECTED_VERSION}" >&2
  exit 1
fi

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
	active_calls=0
	for counter in "${PAXM_ACTIVE_RECALL_STATE_DIR}"/*.count; do
	  [ -f "${counter}" ] || continue
	  count="$(tr -d '[:space:]' < "${counter}")"
	  case "${count}" in
	    ''|*[!0-9]*) continue ;;
	    *) active_calls=$((active_calls + count)) ;;
	  esac
	done
	printf '{"kind":"hook_active_recall","success":true,"call_count":%s}\n' "${active_calls}" >&2
	printf '{"kind":"hook_provider_config","provider_type":"%s"}\n' "${PAXM_PROVIDER_TYPE}" >&2
fi
exit "${status}"
