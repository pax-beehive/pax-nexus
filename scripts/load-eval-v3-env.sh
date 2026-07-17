# This file is sourced by Eval v3 scripts and Make targets.

. ./scripts/load-eval-v2-env.sh

: "${MEM0_EVAL_USER_ID:=groupmembench-shared-user}"
: "${MEM0_EVAL_AGENT_ID:=groupmembench-shared-agent}"
: "${EVAL_V3_COMPOSE_FILE:=evals/v2/compose.yaml}"
: "${EVAL_V3_COMPOSE_PROJECT:=pax-nexus-eval-v3}"
export MEM0_EVAL_USER_ID MEM0_EVAL_AGENT_ID EVAL_V3_COMPOSE_FILE EVAL_V3_COMPOSE_PROJECT
