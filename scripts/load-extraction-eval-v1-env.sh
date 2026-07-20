# This file is sourced by extraction-eval-v1 scripts.

case "$-" in
  *a*) extraction_eval_restore_allexport=false ;;
  *) extraction_eval_restore_allexport=true; set -a ;;
esac

[ ! -f .env ] || . ./.env
[ ! -f "${EXTRACTION_EVAL_ENV_FILE:-.env.extraction-eval-v1}" ] || . "${EXTRACTION_EVAL_ENV_FILE:-.env.extraction-eval-v1}"

if "${extraction_eval_restore_allexport}"; then
  set +a
fi
unset extraction_eval_restore_allexport
