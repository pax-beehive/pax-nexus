#!/usr/bin/env python3
"""Prepare answer-blind Session corpora from public memory benchmarks."""

from __future__ import annotations

import argparse
import copy
import hashlib
import json
import shutil
import tempfile
from collections import defaultdict
from pathlib import Path
from typing import Any, Callable, Iterable, Iterator


SCHEMA_VERSION = "pax-session-dataset/v1"
SELECTION_SEED = "pax-llmwiki-session-train-v1"
REVISIONS = {
    "longmemeval": "98d7416c24c778c2fee6e6f3006e7a073259d48f",
    "longmemeval-v2": "f152293e235517d504809563c833d7190b8c713b",
    "locomo": "3eb6f2c585f5e1699204e3c3bdf7adc5c28cb376",
}
DATASET_NAMES = ("longmemeval", "locomo", "longmemeval-v2")


def require(condition: bool, message: str) -> None:
    if not condition:
        raise RuntimeError(message)


def read_json(path: Path) -> Any:
    require(path.is_file(), f"Missing JSON file: {path}")
    return json.loads(path.read_text(encoding="utf-8"))


def read_jsonl(path: Path) -> Iterator[dict[str, Any]]:
    require(path.is_file(), f"Missing JSONL file: {path}")
    with path.open(encoding="utf-8") as source:
        for line_number, line in enumerate(source, start=1):
            if not line.strip():
                continue
            row = json.loads(line)
            require(isinstance(row, dict), f"{path}:{line_number} is not an object")
            yield row


def write_json(path: Path, payload: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(
        json.dumps(payload, ensure_ascii=False, indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
    )


def write_jsonl(path: Path, rows: Iterable[dict[str, Any]]) -> int:
    path.parent.mkdir(parents=True, exist_ok=True)
    count = 0
    with path.open("w", encoding="utf-8") as destination:
        for row in rows:
            destination.write(json.dumps(row, ensure_ascii=False, sort_keys=True) + "\n")
            count += 1
    return count


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        while chunk := source.read(1024 * 1024):
            digest.update(chunk)
    return digest.hexdigest()


def build_raw_inventory(raw_root: Path, *, validate_v2: bool = True) -> dict[str, Any]:
    files = {}
    for path in sorted(item for item in raw_root.rglob("*") if item.is_file()):
        relative = str(path.relative_to(raw_root))
        files[relative] = {
            "bytes": path.stat().st_size,
            "sha256": sha256_file(path),
        }

    v2_root = raw_root / "longmemeval-v2"
    checksum_path = v2_root / "checksums.sha256"
    official_mismatches = {}
    if validate_v2:
        require(checksum_path.is_file(), f"Missing official V2 checksums: {checksum_path}")
        for line in checksum_path.read_text(encoding="utf-8").splitlines():
            expected, relative = line.split(maxsplit=1)
            relative = relative.strip()
            inventory_key = f"longmemeval-v2/{relative}"
            require(inventory_key in files, f"Missing checksummed V2 file: {relative}")
            actual = files[inventory_key]["sha256"]
            if actual != expected:
                official_mismatches[relative] = {"expected": expected, "actual": actual}

    critical_mismatches = {
        path: mismatch
        for path, mismatch in official_mismatches.items()
        if path == "questions.jsonl"
        or path == "trajectories.jsonl"
        or path.startswith("haystacks/")
        or path.startswith("question_screenshots/")
    }
    require(
        not critical_mismatches,
        f"Official V2 data checksum mismatch: {critical_mismatches}",
    )
    return {
        "files": files,
        "official_v2_checksum_mismatches": official_mismatches,
        "notes": [
            (
                "The pinned Hugging Face snapshot serves README.md with SHA-256 "
                "c5de92eadfd8238802b476e446b05a766c312ab0f07518dbda434f914aa4df37, "
                "while its checksums.sha256 lists a stale non-data hash. All questions, "
                "trajectories, haystacks, and question screenshots must match."
            )
        ],
    }


def stable_rank(identifier: str) -> str:
    return hashlib.sha256(f"{SELECTION_SEED}\0{identifier}".encode()).hexdigest()


def stratified_train_ids(
    rows: Iterable[dict[str, Any]],
    *,
    identifier: Callable[[dict[str, Any]], str],
    stratum: Callable[[dict[str, Any]], str],
    per_stratum: int,
) -> set[str]:
    require(per_stratum > 0, "per_stratum must be positive")
    groups: dict[str, list[str]] = defaultdict(list)
    for row in rows:
        groups[stratum(row)].append(identifier(row))

    selected: set[str] = set()
    for name, identifiers in sorted(groups.items()):
        require(
            len(identifiers) >= per_stratum,
            f"Stratum {name!r} has {len(identifiers)} rows; need {per_stratum}",
        )
        selected.update(sorted(identifiers, key=stable_rank)[:per_stratum])
    return selected


def split_rows(
    rows: list[dict[str, Any]],
    identifier: Callable[[dict[str, Any]], str],
    train_ids: set[str],
) -> dict[str, list[dict[str, Any]]]:
    return {
        "full": list(rows),
        "train": [row for row in rows if identifier(row) in train_ids],
        "holdout": [row for row in rows if identifier(row) not in train_ids],
    }


def longmemeval_records(
    row: dict[str, Any],
) -> tuple[dict[str, Any], dict[str, Any], dict[str, Any]]:
    question_id = str(row["question_id"])
    sessions = []
    evidence_turn_ids = []
    session_ids = row["haystack_session_ids"]
    dates = row["haystack_dates"]
    raw_sessions = row["haystack_sessions"]
    require(
        len(session_ids) == len(dates) == len(raw_sessions),
        f"LongMemEval {question_id} has misaligned Session arrays",
    )

    for session_id, occurred_at, turns in zip(session_ids, dates, raw_sessions):
        clean_turns = []
        for turn_index, turn in enumerate(turns):
            if turn.get("has_answer") is True:
                evidence_turn_ids.append(f"{session_id}:turn:{turn_index}")
            clean_turn = {
                "role": turn["role"],
                "content": turn["content"],
            }
            clean_turns.append(clean_turn)
        sessions.append(
            {
                "session_id": str(session_id),
                "occurred_at": occurred_at,
                "turns": clean_turns,
            }
        )

    ingest = {
        "schema_version": SCHEMA_VERSION,
        "case_id": question_id,
        "source_kind": "chat-session-history",
        "sessions": sessions,
    }
    query = {
        "schema_version": SCHEMA_VERSION,
        "case_id": question_id,
        "asked_at": row["question_date"],
        "question": row["question"],
    }
    gold = {
        "schema_version": SCHEMA_VERSION,
        "case_id": question_id,
        "answer": row["answer"],
        "question_type": row["question_type"],
        "abstention": question_id.endswith("_abs"),
        "answer_session_ids": row["answer_session_ids"],
        "evidence_turn_ids": evidence_turn_ids,
    }
    return ingest, query, gold


def prepare_longmemeval(
    raw_root: Path,
    output_root: Path,
    *,
    train_per_stratum: int,
) -> dict[str, Any]:
    source_path = raw_root / "longmemeval" / "longmemeval_s_cleaned.json"
    rows = read_json(source_path)
    require(isinstance(rows, list) and rows, "LongMemEval-S must be a non-empty array")

    train_ids = stratified_train_ids(
        rows,
        identifier=lambda row: str(row["question_id"]),
        stratum=lambda row: (
            f'{row["question_type"]}:'
            f'{"abstention" if str(row["question_id"]).endswith("_abs") else "answerable"}'
        ),
        per_stratum=train_per_stratum,
    )
    splits = split_rows(rows, lambda row: str(row["question_id"]), train_ids)
    counts: dict[str, Any] = {}
    for split_name, split in splits.items():
        converted = [longmemeval_records(row) for row in split]
        split_root = output_root / split_name / "longmemeval"
        counts[split_name] = {
            "cases": len(converted),
            "ingest": write_jsonl(
                split_root / "maintainer" / "ingest.jsonl",
                (item[0] for item in converted),
            ),
            "queries": write_jsonl(
                split_root / "reader" / "query.jsonl",
                (item[1] for item in converted),
            ),
            "gold": write_jsonl(
                split_root / "evaluator" / "gold.jsonl",
                (item[2] for item in converted),
            ),
        }

    write_json(
        output_root / "manifests" / "longmemeval.json",
        {
            "schema_version": SCHEMA_VERSION,
            "dataset": "LongMemEval-S Cleaned",
            "revision": REVISIONS["longmemeval"],
            "raw_file": "raw/longmemeval/longmemeval_s_cleaned.json",
            "raw_sha256": sha256_file(source_path),
            "selection_seed": SELECTION_SEED,
            "train_per_stratum": train_per_stratum,
            "train_ids": sorted(train_ids),
            "counts": counts,
        },
    )
    return counts


def locomo_session_number(name: str) -> int:
    return int(name.removeprefix("session_"))


def locomo_records(
    row: dict[str, Any],
) -> tuple[
    dict[str, Any],
    list[dict[str, Any]],
    list[dict[str, Any]],
    list[dict[str, Any]],
    dict[str, Any],
    dict[str, Any],
]:
    sample_id = str(row["sample_id"])
    conversation = row["conversation"]
    session_names = sorted(
        (
            name
            for name, value in conversation.items()
            if name.startswith("session_")
            and not name.endswith("_date_time")
            and isinstance(value, list)
        ),
        key=locomo_session_number,
    )
    sessions = [
        {
            "session_id": f"{sample_id}:{name}",
            "occurred_at": conversation.get(f"{name}_date_time"),
            "turns": copy.deepcopy(conversation[name]),
        }
        for name in session_names
    ]
    ingest = {
        "schema_version": SCHEMA_VERSION,
        "case_id": sample_id,
        "source_kind": "long-running-conversation",
        "participants": [conversation["speaker_a"], conversation["speaker_b"]],
        "sessions": sessions,
    }

    queries = []
    gold = []
    adversarial = []
    for index, qa in enumerate(row["qa"]):
        case_id = f"{sample_id}:qa:{index:04d}"
        if qa["category"] == 5:
            adversarial.append(
                {
                    "schema_version": SCHEMA_VERSION,
                    "case_id": case_id,
                    "source_case_id": sample_id,
                    "question": qa["question"],
                    "category": qa["category"],
                    "upstream_answer": qa.get("answer"),
                    "upstream_adversarial_answer": qa.get("adversarial_answer"),
                    "evidence_dialog_ids": qa.get("evidence", []),
                    "scored_in_official_qa": False,
                }
            )
            continue
        require("answer" in qa, f"LoCoMo {case_id} category {qa['category']} has no answer")
        queries.append(
            {
                "schema_version": SCHEMA_VERSION,
                "case_id": case_id,
                "source_case_id": sample_id,
                "question": qa["question"],
            }
        )
        gold.append(
            {
                "schema_version": SCHEMA_VERSION,
                "case_id": case_id,
                "answer": qa["answer"],
                "category": qa["category"],
                "evidence_dialog_ids": qa.get("evidence", []),
            }
        )

    event_gold = {
        "schema_version": SCHEMA_VERSION,
        "case_id": sample_id,
        "event_summary": row["event_summary"],
    }
    generated_baselines = {
        "schema_version": SCHEMA_VERSION,
        "case_id": sample_id,
        "observation": row["observation"],
        "session_summary": row["session_summary"],
    }
    return ingest, queries, gold, adversarial, event_gold, generated_baselines


def prepare_locomo(
    raw_root: Path,
    output_root: Path,
    *,
    train_conversations: int,
) -> dict[str, Any]:
    source_path = raw_root / "locomo" / "locomo10.json"
    rows = read_json(source_path)
    require(isinstance(rows, list) and rows, "LoCoMo must be a non-empty array")
    require(
        0 < train_conversations < len(rows),
        "train_conversations must leave at least one LoCoMo holdout conversation",
    )
    train_ids = {
        str(row["sample_id"])
        for row in sorted(rows, key=lambda row: stable_rank(str(row["sample_id"])))[:train_conversations]
    }
    splits = split_rows(rows, lambda row: str(row["sample_id"]), train_ids)
    counts: dict[str, Any] = {}
    for split_name, split in splits.items():
        converted = [locomo_records(row) for row in split]
        split_root = output_root / split_name / "locomo"
        counts[split_name] = {
            "conversations": len(converted),
            "ingest": write_jsonl(
                split_root / "maintainer" / "ingest.jsonl",
                (item[0] for item in converted),
            ),
            "queries": write_jsonl(
                split_root / "reader" / "query.jsonl",
                (query for item in converted for query in item[1]),
            ),
            "gold": write_jsonl(
                split_root / "evaluator" / "gold.jsonl",
                (gold for item in converted for gold in item[2]),
            ),
            "adversarial": write_jsonl(
                split_root / "diagnostics" / "adversarial.jsonl",
                (adversarial for item in converted for adversarial in item[3]),
            ),
            "event_gold": write_jsonl(
                split_root / "evaluator" / "event-gold.jsonl",
                (item[4] for item in converted),
            ),
            "generated_baselines": write_jsonl(
                split_root / "diagnostics" / "generated-baselines.jsonl",
                (item[5] for item in converted),
            ),
        }

    write_json(
        output_root / "manifests" / "locomo.json",
        {
            "schema_version": SCHEMA_VERSION,
            "dataset": "LoCoMo",
            "revision": REVISIONS["locomo"],
            "license": "CC-BY-NC-4.0",
            "raw_file": "raw/locomo/locomo10.json",
            "raw_sha256": sha256_file(source_path),
            "selection_seed": SELECTION_SEED,
            "train_conversations": train_conversations,
            "train_ids": sorted(train_ids),
            "counts": counts,
        },
    )
    return counts


def v2_records(
    row: dict[str, Any],
    trajectory_ids: list[str],
) -> tuple[dict[str, Any], dict[str, Any], dict[str, Any]]:
    question_id = str(row["id"])
    ingest = {
        "schema_version": SCHEMA_VERSION,
        "case_id": question_id,
        "source_kind": "agent-trajectory-haystack",
        "trajectory_ids": trajectory_ids,
        "trajectory_store": "../../../common/longmemeval-v2-small/trajectories.jsonl",
    }
    query = {
        "schema_version": SCHEMA_VERSION,
        "case_id": question_id,
        "question": row["question"],
        "image_ref": (
            f'../../../../raw/longmemeval-v2/{row["image"]}'
            if row.get("image")
            else None
        ),
    }
    gold = {
        "schema_version": SCHEMA_VERSION,
        "case_id": question_id,
        "answer": row["answer"],
        "domain": row["domain"],
        "environment": row["environment"],
        "question_type": row["question_type"],
        "eval_function": row["eval_function"],
    }
    return ingest, query, gold


def crop_v2_trajectory_store(
    source_path: Path,
    destination_path: Path,
    selected_ids: set[str],
) -> dict[str, Any]:
    found: set[str] = set()
    destination_path.parent.mkdir(parents=True, exist_ok=True)
    with destination_path.open("w", encoding="utf-8") as destination:
        for row in read_jsonl(source_path):
            trajectory_id = str(row["id"])
            if trajectory_id not in selected_ids:
                continue
            destination.write(json.dumps(row, ensure_ascii=False, sort_keys=True) + "\n")
            found.add(trajectory_id)
    missing = selected_ids - found
    require(not missing, f"Missing {len(missing)} V2 trajectories: {sorted(missing)[:5]}")
    return {
        "trajectories": len(found),
        "sha256": sha256_file(destination_path),
    }


def prepare_longmemeval_v2(
    raw_root: Path,
    output_root: Path,
    *,
    train_per_stratum: int,
) -> dict[str, Any]:
    question_path = raw_root / "longmemeval-v2" / "questions.jsonl"
    trajectory_path = raw_root / "longmemeval-v2" / "trajectories.jsonl"
    haystack_path = raw_root / "longmemeval-v2" / "haystacks" / "lme_v2_small.json"
    rows = list(read_jsonl(question_path))
    haystacks = read_json(haystack_path)
    require(isinstance(haystacks, dict), "LongMemEval-V2 small haystack must be an object")

    train_ids = stratified_train_ids(
        rows,
        identifier=lambda row: str(row["id"]),
        stratum=lambda row: f'{row["domain"]}:{row["question_type"]}',
        per_stratum=train_per_stratum,
    )
    splits = split_rows(rows, lambda row: str(row["id"]), train_ids)
    all_small_trajectory_ids: set[str] = set()
    counts: dict[str, Any] = {}
    for split_name, split in splits.items():
        converted = []
        for row in split:
            question_id = str(row["id"])
            require(question_id in haystacks, f"Missing V2 small haystack for {question_id}")
            trajectory_ids = haystacks[question_id]
            all_small_trajectory_ids.update(trajectory_ids)
            converted.append(v2_records(row, trajectory_ids))
        split_root = output_root / split_name / "longmemeval-v2"
        counts[split_name] = {
            "cases": len(converted),
            "ingest": write_jsonl(
                split_root / "maintainer" / "ingest.jsonl",
                (item[0] for item in converted),
            ),
            "queries": write_jsonl(
                split_root / "reader" / "query.jsonl",
                (item[1] for item in converted),
            ),
            "gold": write_jsonl(
                split_root / "evaluator" / "gold.jsonl",
                (item[2] for item in converted),
            ),
        }

    cropped_store = crop_v2_trajectory_store(
        trajectory_path,
        output_root / "common" / "longmemeval-v2-small" / "trajectories.jsonl",
        all_small_trajectory_ids,
    )
    write_json(
        output_root / "manifests" / "longmemeval-v2.json",
        {
            "schema_version": SCHEMA_VERSION,
            "dataset": "LongMemEval-V2 Small",
            "revision": REVISIONS["longmemeval-v2"],
            "raw_questions": "raw/longmemeval-v2/questions.jsonl",
            "raw_trajectories": "raw/longmemeval-v2/trajectories.jsonl",
            "raw_haystack": "raw/longmemeval-v2/haystacks/lme_v2_small.json",
            "raw_sha256": {
                "questions": sha256_file(question_path),
                "trajectories": sha256_file(trajectory_path),
                "haystack": sha256_file(haystack_path),
            },
            "selection_seed": SELECTION_SEED,
            "train_per_domain_question_type": train_per_stratum,
            "train_ids": sorted(train_ids),
            "source_holdout_limitation": (
                "Questions are held out, but all questions within one domain share the "
                "same official 100-trajectory Small corpus."
            ),
            "cropped_small_store": cropped_store,
            "counts": counts,
        },
    )
    return counts


def recursive_keys(value: Any) -> Iterator[str]:
    if isinstance(value, dict):
        for key, child in value.items():
            yield key
            yield from recursive_keys(child)
    elif isinstance(value, list):
        for child in value:
            yield from recursive_keys(child)


def validate_answer_blind(
    output_root: Path,
    datasets: Iterable[str] = DATASET_NAMES,
) -> dict[str, Any]:
    forbidden_by_dataset = {
        "longmemeval": {
            "question",
            "answer",
            "question_type",
            "answer_session_ids",
            "evidence_turn_ids",
            "has_answer",
        },
        "locomo": {
            "question",
            "answer",
            "qa",
            "event_summary",
            "observation",
            "session_summary",
        },
        "longmemeval-v2": {
            "question",
            "answer",
            "question_type",
            "eval_function",
            "environment",
        },
    }
    checked = 0
    for split_name in ("full", "train", "holdout"):
        for dataset in datasets:
            forbidden = forbidden_by_dataset[dataset]
            path = output_root / split_name / dataset / "maintainer" / "ingest.jsonl"
            for row in read_jsonl(path):
                leaked = set(recursive_keys(row)) & forbidden
                require(not leaked, f"Gold/query leakage in {path}: {sorted(leaked)}")
                checked += 1
    return {"ingest_records_checked": checked, "answer_blind_status": "passed"}


def case_ids(path: Path) -> set[str]:
    rows = list(read_jsonl(path))
    identifiers = {str(row["case_id"]) for row in rows}
    require(len(identifiers) == len(rows), f"Duplicate case_id in {path}")
    return identifiers


def validate_partitions(
    output_root: Path,
    datasets: Iterable[str] = DATASET_NAMES,
) -> dict[str, Any]:
    checked = 0
    for dataset in datasets:
        role_files = (
            ("maintainer", "ingest.jsonl"),
            ("reader", "query.jsonl"),
            ("evaluator", "gold.jsonl"),
        )
        for role, filename in role_files:
            full = case_ids(output_root / "full" / dataset / role / filename)
            train = case_ids(output_root / "train" / dataset / role / filename)
            holdout = case_ids(output_root / "holdout" / dataset / role / filename)
            require(not train & holdout, f"{dataset} {role}/{filename} train/holdout overlap")
            require(
                train | holdout == full,
                f"{dataset} {role}/{filename} does not partition full",
            )
            checked += 1
        for split_name in ("full", "train", "holdout"):
            queries = case_ids(
                output_root / split_name / dataset / "reader" / "query.jsonl"
            )
            gold = case_ids(
                output_root / split_name / dataset / "evaluator" / "gold.jsonl"
            )
            require(queries == gold, f"{dataset} {split_name} query/gold IDs differ")
            checked += 1
    return {"partitions_checked": checked, "partition_status": "disjoint-and-complete"}


def validate_references(
    output_root: Path,
    datasets: Iterable[str] = DATASET_NAMES,
) -> dict[str, Any]:
    checked = 0
    if "longmemeval-v2" not in datasets:
        return {"references_checked": checked, "reference_status": "not-applicable"}
    for split_name in ("full", "train", "holdout"):
        ingest_path = (
            output_root
            / split_name
            / "longmemeval-v2"
            / "maintainer"
            / "ingest.jsonl"
        )
        for row in read_jsonl(ingest_path):
            store_path = (ingest_path.parent / row["trajectory_store"]).resolve()
            require(store_path.is_file(), f"Missing V2 trajectory store reference: {store_path}")
            checked += 1
        query_path = (
            output_root / split_name / "longmemeval-v2" / "reader" / "query.jsonl"
        )
        for row in read_jsonl(query_path):
            if row["image_ref"] is None:
                continue
            image_path = (query_path.parent / row["image_ref"]).resolve()
            require(image_path.is_file(), f"Missing V2 question image reference: {image_path}")
            checked += 1
    return {"references_checked": checked, "reference_status": "resolved"}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Prepare train/full/holdout Session datasets with ingest/query/gold isolation."
    )
    parser.add_argument(
        "--data-root",
        default=".build/datasets/llmwiki",
        help="Root containing raw/ and receiving prepared/",
    )
    parser.add_argument(
        "--dataset",
        choices=("all", *DATASET_NAMES),
        default="all",
        help="Prepare one installed dataset, or all datasets.",
    )
    parser.add_argument("--force", action="store_true", help="Replace an existing prepared directory")
    parser.add_argument("--longmemeval-train-per-stratum", type=int, default=4)
    parser.add_argument("--v2-train-per-stratum", type=int, default=2)
    parser.add_argument("--locomo-train-conversations", type=int, default=3)
    return parser.parse_args()


def prepare_selected_dataset(
    dataset: str,
    raw_root: Path,
    output_root: Path,
    args: argparse.Namespace,
) -> dict[str, Any]:
    if dataset == "longmemeval":
        return prepare_longmemeval(
            raw_root,
            output_root,
            train_per_stratum=args.longmemeval_train_per_stratum,
        )
    if dataset == "locomo":
        return prepare_locomo(
            raw_root,
            output_root,
            train_conversations=args.locomo_train_conversations,
        )
    return prepare_longmemeval_v2(
        raw_root,
        output_root,
        train_per_stratum=args.v2_train_per_stratum,
    )


def install_prepared_dataset(staging_root: Path, output_root: Path, dataset: str) -> None:
    output_root.mkdir(parents=True, exist_ok=True)
    for split_name in ("full", "train", "holdout"):
        source = staging_root / split_name / dataset
        destination = output_root / split_name / dataset
        destination.parent.mkdir(parents=True, exist_ok=True)
        if destination.exists():
            shutil.rmtree(destination)
        shutil.move(str(source), str(destination))

    if dataset == "longmemeval-v2":
        source = staging_root / "common" / "longmemeval-v2-small"
        destination = output_root / "common" / "longmemeval-v2-small"
        destination.parent.mkdir(parents=True, exist_ok=True)
        if destination.exists():
            shutil.rmtree(destination)
        shutil.move(str(source), str(destination))

    manifest_source = staging_root / "manifests" / f"{dataset}.json"
    manifest_destination = output_root / "manifests" / f"{dataset}.json"
    manifest_destination.parent.mkdir(parents=True, exist_ok=True)
    manifest_source.replace(manifest_destination)


def prepare_one(args: argparse.Namespace, data_root: Path, raw_root: Path) -> None:
    dataset = args.dataset
    output_root = data_root / "prepared"
    with tempfile.TemporaryDirectory(prefix=f".prepare-{dataset}-", dir=data_root) as directory:
        staging_root = Path(directory) / "prepared"
        staging_root.mkdir(parents=True)
        raw_inventory = build_raw_inventory(
            raw_root,
            validate_v2=dataset == "longmemeval-v2",
        )
        counts = prepare_selected_dataset(dataset, raw_root, staging_root, args)
        validation = {
            **validate_answer_blind(staging_root, (dataset,)),
            **validate_partitions(staging_root, (dataset,)),
            **validate_references(staging_root, (dataset,)),
        }
        install_prepared_dataset(staging_root, output_root, dataset)
        write_json(output_root / "manifests" / "raw-inventory.json", raw_inventory)
        summary_path = output_root / "SUMMARY.json"
        summary = read_json(summary_path) if summary_path.is_file() else {
            "schema_version": SCHEMA_VERSION,
        }
        summary["raw_inventory"] = {
            "files": len(raw_inventory["files"]),
            "official_v2_checksum_mismatches": raw_inventory[
                "official_v2_checksum_mismatches"
            ],
        }
        summary[dataset] = counts
        summary.setdefault("validation_by_dataset", {})[dataset] = validation
        write_json(summary_path, summary)
        print(json.dumps({dataset: counts, "validation": validation}, ensure_ascii=False, indent=2))


def main() -> None:
    args = parse_args()
    data_root = Path(args.data_root).expanduser().resolve()
    raw_root = data_root / "raw"
    output_root = data_root / "prepared"
    if args.dataset != "all":
        prepare_one(args, data_root, raw_root)
        return
    if output_root.exists():
        require(args.force, f"{output_root} already exists; pass --force to replace generated data")
        shutil.rmtree(output_root)
    output_root.mkdir(parents=True)

    raw_inventory = build_raw_inventory(raw_root)
    write_json(output_root / "manifests" / "raw-inventory.json", raw_inventory)
    summary = {
        "schema_version": SCHEMA_VERSION,
        "raw_inventory": {
            "files": len(raw_inventory["files"]),
            "official_v2_checksum_mismatches": raw_inventory[
                "official_v2_checksum_mismatches"
            ],
        },
        "longmemeval": prepare_selected_dataset("longmemeval", raw_root, output_root, args),
        "locomo": prepare_selected_dataset("locomo", raw_root, output_root, args),
        "longmemeval-v2": prepare_selected_dataset(
            "longmemeval-v2", raw_root, output_root, args
        ),
    }
    summary["validation"] = {
        **validate_answer_blind(output_root),
        **validate_partitions(output_root),
        **validate_references(output_root),
    }
    write_json(output_root / "SUMMARY.json", summary)
    print(json.dumps(summary, ensure_ascii=False, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
