#!/usr/bin/env python3
from __future__ import annotations

import hashlib
import importlib.util
import io
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path
from types import SimpleNamespace


SCRIPT_PATH = Path(__file__).with_name("prepare_llmwiki_session_datasets.py")
SPEC = importlib.util.spec_from_file_location("prepare_llmwiki_session_datasets", SCRIPT_PATH)
assert SPEC is not None and SPEC.loader is not None
DATASETS = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(DATASETS)


class SessionDatasetPreparationTest(unittest.TestCase):
    def test_given_strata_when_selecting_twice_then_train_ids_are_stable_and_balanced(self) -> None:
        rows = [
            {"id": f"{group}-{index}", "group": group}
            for group in ("a", "b")
            for index in range(5)
        ]

        first = DATASETS.stratified_train_ids(
            rows,
            identifier=lambda row: row["id"],
            stratum=lambda row: row["group"],
            per_stratum=2,
        )
        second = DATASETS.stratified_train_ids(
            reversed(rows),
            identifier=lambda row: row["id"],
            stratum=lambda row: row["group"],
            per_stratum=2,
        )

        self.assertEqual(first, second)
        self.assertEqual(2, sum(identifier.startswith("a-") for identifier in first))
        self.assertEqual(2, sum(identifier.startswith("b-") for identifier in first))

    def test_given_longmemeval_gold_labels_when_converting_then_ingest_is_answer_blind(self) -> None:
        row = {
            "question_id": "case-1",
            "question_type": "knowledge-update",
            "question": "What changed?",
            "answer": "The new value",
            "question_date": "2026-07-24",
            "answer_session_ids": ["session-1"],
            "haystack_session_ids": ["session-1"],
            "haystack_dates": ["2026-07-23"],
            "haystack_sessions": [
                [
                    {
                        "role": "user",
                        "content": "Use the new value.",
                        "has_answer": True,
                    }
                ]
            ],
        }

        ingest, query, gold = DATASETS.longmemeval_records(row)

        self.assertEqual("Use the new value.", ingest["sessions"][0]["turns"][0]["content"])
        self.assertNotIn("has_answer", ingest["sessions"][0]["turns"][0])
        self.assertNotIn("question", set(DATASETS.recursive_keys(ingest)))
        self.assertEqual("What changed?", query["question"])
        self.assertEqual(["session-1:turn:0"], gold["evidence_turn_ids"])

    def test_given_locomo_derived_annotations_when_converting_then_only_raw_turns_are_ingested(self) -> None:
        row = {
            "sample_id": "sample-1",
            "conversation": {
                "speaker_a": "A",
                "speaker_b": "B",
                "session_1_date_time": "2026-07-23",
                "session_1": [{"speaker": "A", "dia_id": "d1", "text": "Hello"}],
            },
            "qa": [{"question": "Who spoke?", "answer": "A", "category": 1, "evidence": ["d1"]}],
            "event_summary": {"events_session_1": ["A said hello"]},
            "observation": {"session_1_observation": "A greeted B"},
            "session_summary": {"session_1_summary": "A and B greeted"},
        }

        ingest, queries, gold, adversarial, event_gold, baselines = DATASETS.locomo_records(row)

        ingest_keys = set(DATASETS.recursive_keys(ingest))
        self.assertFalse({"qa", "event_summary", "observation", "session_summary"} & ingest_keys)
        self.assertEqual("Who spoke?", queries[0]["question"])
        self.assertEqual(["d1"], gold[0]["evidence_dialog_ids"])
        self.assertEqual([], adversarial)
        self.assertIn("event_summary", event_gold)
        self.assertIn("session_summary", baselines)

    def test_given_locomo_category_five_when_converting_then_it_is_not_scored_as_gold(self) -> None:
        row = {
            "sample_id": "sample-1",
            "conversation": {
                "speaker_a": "A",
                "speaker_b": "B",
                "session_1_date_time": "2026-07-23",
                "session_1": [{"speaker": "A", "dia_id": "d1", "text": "Hello"}],
            },
            "qa": [
                {
                    "question": "A misleading question?",
                    "category": 5,
                    "adversarial_answer": "plausible but wrong",
                    "evidence": ["d1"],
                }
            ],
            "event_summary": {},
            "observation": {},
            "session_summary": {},
        }

        _, queries, gold, adversarial, _, _ = DATASETS.locomo_records(row)

        self.assertEqual([], queries)
        self.assertEqual([], gold)
        self.assertEqual("plausible but wrong", adversarial[0]["upstream_adversarial_answer"])
        self.assertFalse(adversarial[0]["scored_in_official_qa"])

    def test_given_leaked_ingest_when_validating_then_preparation_fails(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            output_root = Path(directory)
            for split_name in ("full", "train", "holdout"):
                for dataset in ("longmemeval", "locomo", "longmemeval-v2"):
                    DATASETS.write_jsonl(
                        output_root / split_name / dataset / "maintainer" / "ingest.jsonl",
                        [{"case_id": "safe"}],
                    )
            DATASETS.write_jsonl(
                output_root
                / "train"
                / "longmemeval"
                / "maintainer"
                / "ingest.jsonl",
                [{"case_id": "leak", "answer": "visible"}],
            )

            with self.assertRaisesRegex(RuntimeError, "Gold/query leakage"):
                DATASETS.validate_answer_blind(output_root)

    def test_given_stale_document_checksum_when_inventorying_then_data_files_still_gate(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            raw_root = Path(directory)
            v2_root = raw_root / "longmemeval-v2"
            v2_root.mkdir()
            questions = b'{"id":"q1"}\n'
            (v2_root / "questions.jsonl").write_bytes(questions)
            (v2_root / "README.md").write_text("current", encoding="utf-8")
            questions_sha = hashlib.sha256(questions).hexdigest()
            (v2_root / "checksums.sha256").write_text(
                f"{questions_sha}  questions.jsonl\n"
                f"{'0' * 64}  README.md\n",
                encoding="utf-8",
            )

            inventory = DATASETS.build_raw_inventory(raw_root)

            self.assertIn("README.md", inventory["official_v2_checksum_mismatches"])
            self.assertNotIn("questions.jsonl", inventory["official_v2_checksum_mismatches"])

            (v2_root / "questions.jsonl").write_text("changed", encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "Official V2 data checksum mismatch"):
                DATASETS.build_raw_inventory(raw_root)

    def test_given_one_dataset_when_preparing_then_other_prepared_data_is_preserved(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            data_root = Path(directory)
            raw_path = data_root / "raw" / "locomo" / "locomo10.json"
            rows = []
            for index in range(4):
                sample_id = f"sample-{index}"
                rows.append(
                    {
                        "sample_id": sample_id,
                        "conversation": {
                            "speaker_a": "A",
                            "speaker_b": "B",
                            "session_1_date_time": "2026-07-31",
                            "session_1": [
                                {"speaker": "A", "dia_id": f"d-{index}", "text": "Hello"}
                            ],
                        },
                        "qa": [
                            {
                                "question": "Who spoke?",
                                "answer": "A",
                                "category": 1,
                                "evidence": [f"d-{index}"],
                            }
                        ],
                        "event_summary": {},
                        "observation": {},
                        "session_summary": {},
                    }
                )
            DATASETS.write_json(raw_path, rows)
            sentinel = data_root / "prepared" / "manifests" / "other.json"
            DATASETS.write_json(sentinel, {"keep": True})
            args = SimpleNamespace(
                dataset="locomo",
                locomo_train_conversations=3,
                longmemeval_train_per_stratum=4,
                v2_train_per_stratum=2,
            )

            with redirect_stdout(io.StringIO()):
                DATASETS.prepare_one(args, data_root, data_root / "raw")

            self.assertTrue(sentinel.is_file())
            manifest = DATASETS.read_json(
                data_root / "prepared" / "manifests" / "locomo.json"
            )
            self.assertEqual(3, manifest["counts"]["train"]["conversations"])
            self.assertTrue(
                (data_root / "prepared" / "train" / "locomo" / "maintainer" / "ingest.jsonl").is_file()
            )


if __name__ == "__main__":
    unittest.main()
