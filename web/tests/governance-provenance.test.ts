// 溯源链的派生。这是阶段 5 的核心不变量：六段链条完全由一次 getTeamNote
// 的响应构成，且任何一段缺失都只让那一段显示「没有记录」，不拖垮整条链。
import { describe, expect, it } from "vitest";
import { buildProvenance, describeRecall } from "../src/pages/governance/provenance";
import type {
  ExplorerRevision,
  TeamNoteDetail,
  TeamNoteSummary,
} from "../src/api/types";

function summary(overrides: Partial<TeamNoteSummary> = {}): TeamNoteSummary {
  return {
    note_id: "note_01", kind: "decision", subject: "用 Postgres 存 evidence",
    state: "live", origin_agent_id: "alice-codex", audience_agent_ids: [],
    revision: 2, created_at: "2026-08-01T00:00:00Z", updated_at: "2026-08-02T00:00:00Z",
    soft_expires_at: "2026-09-01T00:00:00Z", hard_expires_at: "2026-10-01T00:00:00Z",
    ...overrides,
  };
}

function revision(overrides: Partial<ExplorerRevision> = {}): ExplorerRevision {
  return {
    revision: 2, candidate_id: "cand_02", operation: "update",
    body: "决定用 Postgres 存 evidence，理由是……", related_subjects: [],
    created_at: "2026-08-02T00:00:00Z",
    extraction: {
      run_id: "run_02", user_id: "usr_01", agent_id: "alice-codex",
      session_id: "sess_09", from_sequence: 10, to_sequence: 42,
      model: "deepseek-v4", prompt_version: "p7", status: "completed",
      input_tokens: 1200, output_tokens: 300, created_at: "2026-08-02T00:00:00Z",
    },
    evidence: [
      { event_id: "evt_a", user_id: "usr_01", agent_id: "alice-codex", session_id: "sess_09",
        sequence: 10, type: "message", content: "…", visibility: "team",
        occurred_at: "2026-08-02T00:00:00Z", captured_at: "2026-08-02T00:00:01Z" },
      { event_id: "evt_b", user_id: "usr_01", agent_id: "alice-codex", session_id: "sess_09",
        sequence: 11, type: "message", content: "…", visibility: "team",
        occurred_at: "2026-08-02T00:00:02Z", captured_at: "2026-08-02T00:00:03Z" },
    ],
    deliveries: [],
    candidate: {
      candidate_id: "cand_02", action: "update", kind: "decision",
      subject: "用 Postgres 存 evidence", body: "…", origin_agent_id: "alice-codex",
      evidence_event_ids: ["evt_a", "evt_b"], admission_status: "admitted",
      created_at: "2026-08-02T00:00:00Z", resulting_note_id: "note_01",
    },
    ...overrides,
  };
}

function detail(revisions: ExplorerRevision[]): TeamNoteDetail {
  return {
    note: {
      summary: summary(), body: "…", origin_user_id: "usr_01",
      origin_session_id: "sess_09", related_subjects: [],
    },
    related_notes: [], revisions, recall_observations: [],
  };
}

describe("buildProvenance", () => {
  it("每个版本产出固定顺序的五段", () => {
    const [rev] = buildProvenance(detail([revision()]));
    expect(rev.revision).toBe(2);
    expect(rev.steps.map((s) => s.stage)).toEqual([
      "source", "extraction", "candidate", "revision", "delivery",
    ]);
    expect(rev.steps.every((s) => s.missing)).toBe(false);
  });

  it("源事件段带上条数与 session、引用是事件 id", () => {
    const [rev] = buildProvenance(detail([revision()]));
    const source = rev.steps[0];
    expect(source.missing).toBe(false);
    expect(source.title).toContain("2");
    expect(source.body).toContain("sess_09");
    expect(source.ref).toContain("evt_a");
  });

  it("抽取段带模型与 prompt 版本，引用是 run_id", () => {
    const [rev] = buildProvenance(detail([revision()]));
    const extraction = rev.steps[1];
    expect(extraction.title).toContain("deepseek-v4");
    expect(extraction.title).toContain("p7");
    expect(extraction.ref).toBe("run_02");
  });

  it("候选被拒时把拒绝理由写进说明", () => {
    const rejected = revision({
      candidate: {
        candidate_id: "cand_03", action: "create", kind: "fact", subject: "x", body: "y",
        origin_agent_id: "alice-codex", evidence_event_ids: [],
        admission_status: "rejected", rejection_reason: "duplicate_of_note_01",
        created_at: "2026-08-02T00:00:00Z",
      },
    });
    const [rev] = buildProvenance(detail([rejected]));
    expect(rev.steps[2].body).toContain("duplicate_of_note_01");
  });

  it("投递为空时该段标记 missing，其余段不受影响", () => {
    const [rev] = buildProvenance(detail([revision({ deliveries: [] })]));
    expect(rev.steps[4].missing).toBe(true);
    expect(rev.steps[4].body).toContain("没有");
    expect(rev.steps.slice(0, 4).every((s) => s.missing)).toBe(false);
  });

  it("源事件为空时只有那一段 missing", () => {
    const [rev] = buildProvenance(detail([revision({ evidence: [] })]));
    expect(rev.steps[0].missing).toBe(true);
    expect(rev.steps[1].missing).toBe(false);
  });

  it("多版本按 revision 逆序，最新在前", () => {
    const chain = buildProvenance(
      detail([revision({ revision: 1 }), revision({ revision: 3 }), revision({ revision: 2 })]),
    );
    expect(chain.map((r) => r.revision)).toEqual([3, 2, 1]);
  });

  it("revisions 为空时返回空数组（调用方渲染正向空态）", () => {
    expect(buildProvenance(detail([]))).toEqual([]);
  });

  it("resolve 操作允许空正文，不判定为 missing", () => {
    const resolved = revision({ operation: "resolve", body: "" });
    const [rev] = buildProvenance(detail([resolved]));
    expect(rev.steps[3].missing).toBe(false);
    expect(rev.steps[3].body).not.toContain("没有");
  });

  it("非 resolve 操作正文为空仍判定为 missing", () => {
    const noBody = revision({ operation: "update", body: "" });
    const [rev] = buildProvenance(detail([noBody]));
    expect(rev.steps[3].missing).toBe(true);
    expect(rev.steps[3].body).toContain("没有");
  });
});

describe("describeRecall", () => {
  it("投递成功且无拒因时说命中并投递", () => {
    expect(
      describeRecall({
        observation_id: 1, recipient_agent_id: "bob-claude", recipient_session_id: "sess_10",
        occurred_at: "2026-08-03T00:00:00Z", delivered: true,
        rejection_reasons: [], budget_drop_reasons: [], hard_gate_failures: [],
      }),
    ).toContain("命中并投递");
  });

  it("把三类原因拼成一句话", () => {
    const text = describeRecall({
      observation_id: 2, recipient_agent_id: "bob-claude", recipient_session_id: "sess_10",
      occurred_at: "2026-08-03T00:00:00Z", delivered: false,
      rejection_reasons: ["low_similarity"], budget_drop_reasons: ["token_budget"],
      hard_gate_failures: ["audience_mismatch"],
    });
    expect(text).toContain("low_similarity");
    expect(text).toContain("token_budget");
    expect(text).toContain("audience_mismatch");
  });

  it("未投递且三类原因都空时不谎报原因", () => {
    const text = describeRecall({
      observation_id: 3, recipient_agent_id: "bob-claude", recipient_session_id: "sess_10",
      occurred_at: "2026-08-03T00:00:00Z", delivered: false,
      rejection_reasons: [], budget_drop_reasons: [], hard_gate_failures: [],
    });
    expect(text).not.toContain("命中并投递");
    expect(text).toMatch(/没有记录|未说明/);
  });
});
