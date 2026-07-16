# This file is sourced by Eval v2 scripts and Make targets.

case "$-" in
  *a*) eval_v2_restore_allexport=false ;;
  *) eval_v2_restore_allexport=true; set -a ;;
esac

[ ! -f .env ] || . ./.env
[ ! -f "${EVAL_V2_ENV_FILE:-.env.eval-v2}" ] || . "${EVAL_V2_ENV_FILE:-.env.eval-v2}"

if "${eval_v2_restore_allexport}"; then
  set +a
fi
unset eval_v2_restore_allexport

: "${MEM0_DEEPSEEK_BASE_URL:=https://api.deepseek.com}"
: "${MEM0_EVAL_USER_ID:=groupmembench-shared-user}"
: "${MEM0_EVAL_AGENT_ID:=groupmembench-shared-agent}"
: "${MEM0_SCORE_SEMANTICS:=distance}"
: "${MEM0_SEARCH_SCOPE_PAYLOAD:=top_level}"
: "${EVAL_V2_JUDGE_MODEL:=deepseek/deepseek-v4-pro}"
: "${EVAL_V2_JUDGE_THINKING:=high}"
: "${PAXM_EXPECTED_VERSION:=v0.1.29}"
: "${PAXM_PASSIVE_MIN_RELEVANCE:=-1}"
: "${PAXM_PASSIVE_MIN_SCORE:=-1}"
: "${PAXM_PASSIVE_PROVIDER_TIMEOUT:=2s}"
: "${PAXM_INSERTION_MIN_SCORE:=0}"
: "${PAXM_EVAL_DIAGNOSTICS:=1}"
export MEM0_DEEPSEEK_BASE_URL MEM0_EVAL_USER_ID MEM0_EVAL_AGENT_ID MEM0_SCORE_SEMANTICS MEM0_SEARCH_SCOPE_PAYLOAD EVAL_V2_JUDGE_MODEL EVAL_V2_JUDGE_THINKING PAXM_EXPECTED_VERSION PAXM_PASSIVE_MIN_RELEVANCE PAXM_PASSIVE_MIN_SCORE PAXM_PASSIVE_PROVIDER_TIMEOUT PAXM_INSERTION_MIN_SCORE PAXM_EVAL_DIAGNOSTICS
