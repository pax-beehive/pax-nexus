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
