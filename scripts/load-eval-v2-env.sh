# This file is sourced by Eval v2 scripts and Make targets.

case "$-" in
  *a*) eval_v2_restore_allexport=false ;;
  *) eval_v2_restore_allexport=true; set -a ;;
esac

eval_v2_base_env_file="${EVAL_V2_BASE_ENV_FILE:-.env}"
case "${eval_v2_base_env_file}" in
  /*|./*|../*) ;;
  *) eval_v2_base_env_file="./${eval_v2_base_env_file}" ;;
esac
[ ! -f "${eval_v2_base_env_file}" ] || . "${eval_v2_base_env_file}"
eval_v2_env_file="${EVAL_V2_ENV_FILE:-.env.eval-v2}"
case "${eval_v2_env_file}" in
  /*|./*|../*) ;;
  *) eval_v2_env_file="./${eval_v2_env_file}" ;;
esac
[ ! -f "${eval_v2_env_file}" ] || . "${eval_v2_env_file}"

[ -z "${PAXM_SOURCE_DIR_OVERRIDE:-}" ] || PAXM_SOURCE_DIR="${PAXM_SOURCE_DIR_OVERRIDE}"
[ -z "${PAXM_EXPECTED_VERSION_OVERRIDE:-}" ] || PAXM_EXPECTED_VERSION="${PAXM_EXPECTED_VERSION_OVERRIDE}"
[ -z "${EVAL_V2_POSTGRES_PORT_OVERRIDE:-}" ] || EVAL_V2_POSTGRES_PORT="${EVAL_V2_POSTGRES_PORT_OVERRIDE}"
[ -z "${EVAL_V2_POSTGRES_DSN_OVERRIDE:-}" ] || EVAL_V2_POSTGRES_DSN="${EVAL_V2_POSTGRES_DSN_OVERRIDE}"
[ -z "${TEAM_MEMORY_EXTRACTOR_MODE_OVERRIDE:-}" ] || TEAM_MEMORY_EXTRACTOR_MODE="${TEAM_MEMORY_EXTRACTOR_MODE_OVERRIDE}"
[ -z "${PAXM_PASSIVE_PROVIDER_TIMEOUT_OVERRIDE:-}" ] || PAXM_PASSIVE_PROVIDER_TIMEOUT="${PAXM_PASSIVE_PROVIDER_TIMEOUT_OVERRIDE}"
[ -z "${TEAM_MEMORY_EXTRACTOR_MODEL_OVERRIDE:-}" ] || TEAM_MEMORY_EXTRACTOR_MODEL="${TEAM_MEMORY_EXTRACTOR_MODEL_OVERRIDE}"
[ -z "${TEAM_MEMORY_EXTRACTOR_BASE_URL_OVERRIDE:-}" ] || TEAM_MEMORY_EXTRACTOR_BASE_URL="${TEAM_MEMORY_EXTRACTOR_BASE_URL_OVERRIDE}"
[ -z "${TEAM_MEMORY_EXTRACTOR_API_KEY_OVERRIDE:-}" ] || TEAM_MEMORY_EXTRACTOR_API_KEY="${TEAM_MEMORY_EXTRACTOR_API_KEY_OVERRIDE}"

if "${eval_v2_restore_allexport}"; then
  set +a
fi
unset eval_v2_restore_allexport
unset eval_v2_env_file
unset eval_v2_base_env_file

: "${MEM0_DEEPSEEK_BASE_URL:=https://api.deepseek.com}"
: "${MEM0_EVAL_USER_ID:=groupmembench-shared-user}"
: "${MEM0_EVAL_AGENT_ID:=groupmembench-shared-agent}"
: "${MEM0_SCORE_SEMANTICS:=distance}"
: "${MEM0_SEARCH_SCOPE_PAYLOAD:=top_level}"
: "${EVAL_V2_JUDGE_MODEL:=deepseek/deepseek-v4-pro}"
: "${EVAL_V2_JUDGE_THINKING:=high}"
: "${TEAM_MEMORY_EXTRACTION_CONTEXT_MODE:=rolling}"
: "${TEAM_MEMORY_EXTRACTION_VERSION:=v2}"
: "${TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY:=source-clause-v1}"
: "${TEAM_MEMORY_EXTRACTION_COMPACTION_ENABLED:=false}"
: "${TEAM_MEMORY_EXTRACTION_SUMMARY_ENABLED:=true}"
: "${TEAM_MEMORY_EXTRACTION_SUMMARY_TRIGGER_TOKENS:=8192}"
: "${TEAM_MEMORY_EXTRACTION_SUMMARY_TAIL_TOKENS:=16384}"
: "${TEAM_MEMORY_EXTRACTION_COMPACT_START_TOKENS:=12288}"
: "${TEAM_MEMORY_EXTRACTION_COMPACT_TOKENS:=16384}"
: "${PAXM_EXPECTED_VERSION:=v0.2.0}"
: "${PAXM_PASSIVE_MIN_RELEVANCE:=-1}"
: "${PAXM_PASSIVE_MIN_SCORE:=-1}"
: "${PAXM_PASSIVE_PROVIDER_TIMEOUT:=2s}"
# PAXM_PASSIVE_PROVIDER_ALLOCATION caps how many hits each recall provider may
# contribute to the passive-recall result before the router's global top-N
# truncation runs, so a provider with a large corpus cannot crowd every other
# provider out. Default 2, chosen for the eval's actual shape: the
# private_sqlite_plus_team_note arm has 2 providers and passive recall's
# max_results is 5, so 2 providers x 2 = 4 <= 5 (precondition: numProviders x
# allocation <= overall limit), and 2 is well under
# providerCandidateLimit(5) = 15 (precondition: allocation <= per-provider
# over-fetch cap). 3 would already violate the first precondition (2 x 3 = 6 >
# 5) and buy nothing over 2 for this shape.
: "${PAXM_PASSIVE_PROVIDER_ALLOCATION:=2}"
: "${PAXM_INSERTION_MIN_SCORE:=0}"
: "${PAXM_EVAL_DIAGNOSTICS:=1}"
export MEM0_DEEPSEEK_BASE_URL MEM0_EVAL_USER_ID MEM0_EVAL_AGENT_ID MEM0_SCORE_SEMANTICS MEM0_SEARCH_SCOPE_PAYLOAD EVAL_V2_JUDGE_MODEL EVAL_V2_JUDGE_THINKING TEAM_MEMORY_EXTRACTION_CONTEXT_MODE TEAM_MEMORY_EXTRACTION_VERSION TEAM_MEMORY_EXTRACTION_CANDIDATE_STRATEGY TEAM_MEMORY_EXTRACTION_COMPACTION_ENABLED TEAM_MEMORY_EXTRACTION_SUMMARY_ENABLED TEAM_MEMORY_EXTRACTION_SUMMARY_TRIGGER_TOKENS TEAM_MEMORY_EXTRACTION_SUMMARY_TAIL_TOKENS TEAM_MEMORY_EXTRACTION_COMPACT_START_TOKENS TEAM_MEMORY_EXTRACTION_COMPACT_TOKENS PAXM_EXPECTED_VERSION PAXM_PASSIVE_MIN_RELEVANCE PAXM_PASSIVE_MIN_SCORE PAXM_PASSIVE_PROVIDER_TIMEOUT PAXM_PASSIVE_PROVIDER_ALLOCATION PAXM_INSERTION_MIN_SCORE PAXM_EVAL_DIAGNOSTICS
