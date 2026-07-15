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
: "${PAXM_PASSIVE_MIN_RELEVANCE:=-1}"
: "${PAXM_PASSIVE_MIN_SCORE:=-1}"
: "${PAXM_INSERTION_MIN_SCORE:=0}"
: "${PAXM_EVAL_DIAGNOSTICS:=1}"
export MEM0_DEEPSEEK_BASE_URL PAXM_PASSIVE_MIN_RELEVANCE PAXM_PASSIVE_MIN_SCORE PAXM_INSERTION_MIN_SCORE PAXM_EVAL_DIAGNOSTICS
