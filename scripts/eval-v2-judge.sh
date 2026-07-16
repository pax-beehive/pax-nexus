#!/bin/sh
set -eu

: "${PAX_EVAL_QUESTION:?PAX_EVAL_QUESTION is required}"
: "${PAX_EVAL_EXPECTED:?PAX_EVAL_EXPECTED is required}"
: "${PAX_EVAL_ANSWER:?PAX_EVAL_ANSWER is required}"

compose_file="${EVAL_V2_COMPOSE_FILE:-evals/v2/compose.yaml}"
project_name="${EVAL_V2_COMPOSE_PROJECT:-pax-nexus-eval-v2}"

. ./scripts/load-eval-v2-env.sh

: "${EVAL_V2_JUDGE_MODEL:=deepseek/deepseek-v4-pro}"
: "${EVAL_V2_JUDGE_THINKING:=high}"

# Compose validates required variables for every service. Judge calls use the
# existing OpenCode image without dependencies or memory plugins.
TEAM_MEMORY_API_KEYS="${TEAM_MEMORY_API_KEYS:-{}}"
export TEAM_MEMORY_API_KEYS

prompt="You are a strict judge evaluating whether an agent's answer matches the gold answer for a question.
Consider paraphrases correct if they have the same meaning as the gold answer.
First provide a brief reasoning paragraph. Then provide the final judgment on a new line using the format:
Final: Correct
or
Final: Incorrect

Question:
${PAX_EVAL_QUESTION}

Gold Answer:
${PAX_EVAL_EXPECTED}

Agent Answer:
${PAX_EVAL_ANSWER}"

docker compose -p "${project_name}" -f "${compose_file}" run --rm --no-deps \
  --entrypoint opencode \
  opencode run --pure --format json --model "${EVAL_V2_JUDGE_MODEL}" \
  --variant "${EVAL_V2_JUDGE_THINKING}" "${prompt}"
