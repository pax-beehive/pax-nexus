#!/bin/sh
set -eu

output="${1:-runs/groupmembench-v3-selection}"
domain="${GROUPMEMBENCH_DOMAIN:-Finance}"
seed="${GROUPMEMBENCH_SEED:-pax-eval-v3}"
per_category="${GROUPMEMBENCH_PER_CATEGORY:-5}"
total_cases="${GROUPMEMBENCH_TOTAL_CASES:-0}"
dataset_dir=".build/datasets/groupmembench/${domain}"

./scripts/fetch-groupmembench.sh "${domain}"
revision="$(tr -d '\n' < "${dataset_dir}/REVISION")"

if command -v groupmembench-select >/dev/null 2>&1; then
  selector=groupmembench-select
else
  selector="go run ./cmd/groupmembench-select"
fi
if command -v groupmembench-annotate >/dev/null 2>&1; then
  annotator=groupmembench-annotate
else
  annotator="go run ./cmd/groupmembench-annotate"
fi

run_selection() {
  # shellcheck disable=SC2086
  GOCACHE="${GOCACHE:-/tmp/team-memory-go-cache}" ${selector} \
    -mode full-domain \
    -conversation "${dataset_dir}/synthetic_domain_channels_rolevariants_${domain}.json" \
    -questions "${dataset_dir}/questions" \
    -output "${output}" \
    -domain "${domain}" \
    -revision "${revision}" \
    -seed "${seed}" \
    -per-category "${per_category}" \
    -total-cases "${total_cases}" \
    "$@"
}

# First pass: select cases and write the full-domain manifest exactly as
# before this script learned about annotations. This manifest carries no
# supporting_agent_ids yet — the annotator needs it on disk to read
# domain/producer/session-batches.json for authorship.
run_selection

# Judge credentials for the annotator: reuse the same DeepSeek credentials
# Team Note extraction already uses, unless the caller points elsewhere.
[ ! -f ./scripts/load-eval-v2-env.sh ] || . ./scripts/load-eval-v2-env.sh
judge_base_url="${GROUPMEMBENCH_JUDGE_BASE_URL:-${TEAM_MEMORY_EXTRACTOR_BASE_URL:-}}"
judge_api_key="${GROUPMEMBENCH_JUDGE_API_KEY:-${TEAM_MEMORY_EXTRACTOR_API_KEY:-${DEEPSEEK_API_KEY:-}}}"
judge_model="${GROUPMEMBENCH_JUDGE_MODEL:-${TEAM_MEMORY_EXTRACTOR_MODEL:-deepseek-v4-flash}}"

if [ -z "${judge_api_key}" ] || [ -z "${judge_base_url}" ]; then
  echo "GroupMemBench annotation skipped: no judge credentials (set GROUPMEMBENCH_JUDGE_API_KEY/GROUPMEMBENCH_JUDGE_BASE_URL, or TEAM_MEMORY_EXTRACTOR_API_KEY/TEAM_MEMORY_EXTRACTOR_BASE_URL via .env.eval-v2)." >&2
  echo "Eval v3 full-domain batches: ${output}/domain/producer/session-batches.json"
  echo "Eval v3 smoke manifest: ${output}/manifest.smoke.json"
  echo "Eval v3 acceptance manifest: ${output}/manifest.json (unannotated)"
  exit 0
fi

judge_script="${output}/.groupmembench-judge.py"
cat > "${judge_script}" <<'PYEOF'
import json
import os
import re
import sys
import urllib.error
import urllib.request

def tokenize(text):
    return set(re.findall(r"[a-z0-9]+", (text or "").lower()))

def top_candidates(question, answer, events, limit):
    query_tokens = tokenize(question) | tokenize(answer)
    if not query_tokens:
        return events[:limit]
    scored = []
    for index, event in enumerate(events):
        overlap = len(query_tokens & tokenize(event.get("content", "")))
        if overlap > 0:
            scored.append((overlap, -index, event))
    scored.sort(key=lambda item: (-item[0], item[1]))
    if not scored:
        return []
    return [event for _, _, event in scored[:limit]]

def call_judge(base_url, api_key, model, question, answer, candidates):
    lines = []
    for event in candidates:
        content = (event.get("content") or "").replace("\n", " ").strip()[:280]
        lines.append("- id=%s author=%s: %s" % (event.get("id", ""), event.get("author", ""), content))
    prompt = (
        "You are grounding a gold answer in a conversation's events.\n"
        "Question: %s\n"
        "Gold answer: %s\n\n"
        "Candidate events (id, author, content):\n%s\n\n"
        "Return strict JSON only, no prose: "
        "{\"supporting_event_ids\": [\"...\"]} listing only the ids of events "
        "whose content directly supplies a fact the gold answer asserts. "
        "If none do, return {\"supporting_event_ids\": []}."
    ) % (question, answer, "\n".join(lines) if lines else "(no candidate events found)")

    payload = json.dumps({
        "model": model,
        "temperature": 0,
        "messages": [{"role": "user", "content": prompt}],
    }).encode("utf-8")
    request = urllib.request.Request(
        base_url.rstrip("/") + "/chat/completions",
        data=payload,
        method="POST",
        headers={
            "Content-Type": "application/json",
            "Authorization": "Bearer " + api_key,
        },
    )
    with urllib.request.urlopen(request, timeout=90) as response:
        body = json.loads(response.read().decode("utf-8"))
    text = body["choices"][0]["message"]["content"]
    match = re.search(r"\{.*\}", text, re.DOTALL)
    if not match:
        return []
    parsed = json.loads(match.group(0))
    ids = parsed.get("supporting_event_ids", [])
    return [str(candidate_id) for candidate_id in ids if isinstance(candidate_id, (str, int))]

def main():
    request = json.load(sys.stdin)
    events = request.get("events") or []
    candidates = top_candidates(request.get("question", ""), request.get("answer", ""), events, 40)
    base_url = os.environ["GROUPMEMBENCH_JUDGE_RUNTIME_BASE_URL"]
    api_key = os.environ["GROUPMEMBENCH_JUDGE_RUNTIME_API_KEY"]
    model = os.environ["GROUPMEMBENCH_JUDGE_RUNTIME_MODEL"]
    try:
        supporting_event_ids = call_judge(base_url, api_key, model, request.get("question", ""), request.get("answer", ""), candidates)
    except (urllib.error.URLError, ValueError, KeyError, TimeoutError):
        supporting_event_ids = []
    json.dump({"supporting_event_ids": supporting_event_ids}, sys.stdout)

if __name__ == "__main__":
    main()
PYEOF

GROUPMEMBENCH_JUDGE_RUNTIME_BASE_URL="${judge_base_url}" \
  GROUPMEMBENCH_JUDGE_RUNTIME_API_KEY="${judge_api_key}" \
  GROUPMEMBENCH_JUDGE_RUNTIME_MODEL="${judge_model}" \
  GOCACHE="${GOCACHE:-/tmp/team-memory-go-cache}" ${annotator} \
  -manifest "${output}/manifest.json" \
  -output "${output}" \
  -judge-command python3 \
  -judge-args "${judge_script}" \
  -judge-model "${judge_model}"

# Second pass: re-run selection with the annotations file so the manifest
# (and smoke manifest) carry supporting_agent_ids for every high-confidence
# annotation. Selection is deterministic, so this reselects the same cases.
run_selection -annotations "${output}/annotations.json"

echo "Eval v3 full-domain batches: ${output}/domain/producer/session-batches.json"
echo "Eval v3 smoke manifest: ${output}/manifest.smoke.json"
echo "Eval v3 acceptance manifest: ${output}/manifest.json"
