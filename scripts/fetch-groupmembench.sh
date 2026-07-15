#!/bin/sh
set -eu

domain="${1:-Finance}"
revision="${GROUPMEMBENCH_REVISION:-e2682e01ff490acfe4fac2940159dce60307dfc9}"
case "${domain}" in
  Finance|Technology|Healthcare|Manufacturing) ;;
  *)
    echo "unsupported GroupMemBench domain: ${domain}" >&2
    exit 2
    ;;
esac

destination_dir=".build/datasets/groupmembench/${domain}"
destination="${destination_dir}/synthetic_domain_channels_rolevariants_${domain}.json"
url="https://huggingface.co/datasets/kimperyang/GroupMemBench/resolve/main/data/final/${domain}/synthetic_domain_channels_rolevariants_${domain}.json"
questions_dir="${destination_dir}/questions"
mkdir -p "${destination_dir}" "${questions_dir}"

if [ ! -s "${destination}" ] || ! jq -e 'type == "object" and length > 0' "${destination}" >/dev/null 2>&1; then
  temporary="${destination}.tmp"
  curl -fL "${url}" -o "${temporary}"
  jq -e 'type == "object" and length > 0' "${temporary}" >/dev/null
  mv "${temporary}" "${destination}"
fi

for category in multi_hop knowledge_update temporal user_implicit term_ambiguity abstention; do
  question_file="${questions_dir}/${category}.jsonl"
  question_url="https://raw.githubusercontent.com/UCSB-NLP-Chang/GroupMemBench/${revision}/questions/${domain}/${category}.jsonl"
  if [ ! -s "${question_file}" ] || ! jq -e -s 'length > 0 and all(.[]; .id and .question and .answer and .asking_user_id)' "${question_file}" >/dev/null 2>&1; then
    temporary="${question_file}.tmp"
    curl -fL "${question_url}" -o "${temporary}"
    jq -e -s 'length > 0 and all(.[]; .id and .question and .answer and .asking_user_id)' "${temporary}" >/dev/null
    mv "${temporary}" "${question_file}"
  fi
done

printf '%s\n' "${revision}" > "${destination_dir}/REVISION"

message_count="$(jq '[.[][]] | length' "${destination}")"
question_count="$(jq -s 'length' "${questions_dir}"/*.jsonl)"
echo "GroupMemBench ${domain}: ${message_count} messages, ${question_count} questions at ${destination_dir}"
