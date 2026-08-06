// Governance · Memory explorer 双栏页：左栏（NoteList，Task 6）与右栏
// （溯源链 + 召回表，Task 7）各自独立取数。核心不变量是：
//   - 左栏永远不因为打开一条笔记而多打请求（NoteList 自己管自己的分页）
//   - 打开一条笔记只发一次 getTeamNote——六段链条全从这一次响应里取
//   - 任何一段缺记录，只影响那一段，其余段与整条链的可读性不受影响
import { describe, expect, it } from "vitest";
import { screen, within } from "@testing-library/react";
import { callsTo, jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";

setupDomTest();

const NOW = "2026-08-06T12:00:00Z";

function noteSummary(overrides: Record<string, unknown> = {}) {
  return {
    note_id: "note_01",
    kind: "decision",
    subject: "用 Postgres 存 evidence",
    state: "active",
    origin_agent_id: "alice-codex",
    audience_agent_ids: [],
    revision: 2,
    created_at: NOW,
    updated_at: NOW,
    soft_expires_at: "2026-09-01T00:00:00Z",
    hard_expires_at: "2026-10-01T00:00:00Z",
    ...overrides,
  };
}

function revision(overrides: Record<string, unknown> = {}) {
  return {
    revision: 2,
    candidate_id: "cand_02",
    operation: "update",
    body: "决定用 Postgres 存 evidence，理由是……",
    related_subjects: [],
    created_at: NOW,
    extraction: {
      run_id: "run_02",
      user_id: "usr_01",
      agent_id: "alice-codex",
      session_id: "sess_09",
      from_sequence: 10,
      to_sequence: 42,
      model: "deepseek-v4",
      prompt_version: "p7",
      status: "completed",
      input_tokens: 1200,
      output_tokens: 300,
      created_at: NOW,
    },
    evidence: [
      {
        event_id: "evt_a",
        user_id: "usr_01",
        agent_id: "alice-codex",
        session_id: "sess_09",
        sequence: 10,
        type: "message",
        content: "…",
        visibility: "team",
        occurred_at: NOW,
        captured_at: NOW,
      },
    ],
    deliveries: [
      {
        recipient_user_id: "usr_02",
        recipient_agent_id: "bob-claude",
        recipient_session_id: "sess_10",
        delivered_at: NOW,
        context_tokens: 40,
      },
    ],
    candidate: {
      candidate_id: "cand_02",
      action: "update",
      kind: "decision",
      subject: "用 Postgres 存 evidence",
      body: "……",
      origin_agent_id: "alice-codex",
      evidence_event_ids: ["evt_a"],
      admission_status: "admitted",
      created_at: NOW,
      resulting_note_id: "note_01",
    },
    ...overrides,
  };
}

function noteDetail(revisions = [revision()], recalls: unknown[] = []) {
  return {
    note: {
      summary: noteSummary(),
      body: "决定用 Postgres 存 evidence。",
      origin_user_id: "usr_01",
      origin_session_id: "sess_09",
      related_subjects: [],
    },
    related_notes: [],
    revisions,
    recall_observations: recalls,
  };
}

const ME = makeMe({ role: "owner", capabilities: ["view.team-memory"] });

describe("Governance · Memory explorer — 左栏骨架", () => {
  it("/governance/memory 渲染左栏，且不发 getTeamNote", async () => {
    const { fetchMock } = await renderApp({
      route: "/governance/memory",
      me: ME,
      fetch: (path) => {
        if (path.startsWith("/v1/admin/team-notes")) {
          return jsonResponse({ notes: [noteSummary()] });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByRole("link", { name: /用 Postgres 存 evidence/ });
    expect(callsTo(fetchMock, "/v1/admin/team-notes/note_01")).toHaveLength(0);
  });

  it("左栏链接指向 /governance/memory/:id", async () => {
    await renderApp({
      route: "/governance/memory",
      me: ME,
      fetch: (path) => {
        if (path.startsWith("/v1/admin/team-notes")) {
          return jsonResponse({ notes: [noteSummary()] });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    const link = (await screen.findByRole("link", {
      name: /用 Postgres 存 evidence/,
    })) as HTMLAnchorElement;
    expect(link.getAttribute("href")).toBe("/governance/memory/note_01");
  });

  it("左栏取数失败时塌成可重试错误", async () => {
    let calls = 0;
    const { user } = await renderApp({
      route: "/governance/memory",
      me: ME,
      fetch: (path) => {
        if (path.startsWith("/v1/admin/team-notes")) {
          calls += 1;
          if (calls === 1) return jsonResponse({ code: "internal", message: "boom" }, 500);
          return jsonResponse({ notes: [noteSummary()] });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByText("加载失败。");
    await user.click(screen.getByRole("button", { name: "重试" }));
    await screen.findByRole("link", { name: /用 Postgres 存 evidence/ });
  });
});

describe("Governance · Memory explorer — 右栏溯源链", () => {
  it("getTeamNote 只被调用一次，且五段的阶段名都出现在页面上", async () => {
    const { fetchMock } = await renderApp({
      route: "/governance/memory/note_01",
      me: ME,
      fetch: (path) => {
        if (path === "/v1/admin/team-notes/note_01") return jsonResponse(noteDetail());
        if (path.startsWith("/v1/admin/team-notes")) {
          return jsonResponse({ notes: [noteSummary()] });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByRole("heading", { name: "用 Postgres 存 evidence" });
    expect(callsTo(fetchMock, "/v1/admin/team-notes/note_01")).toHaveLength(1);
    const provenance = screen.getByRole("heading", { name: "它是怎么来的" }).closest("section")!;
    within(provenance).getByText("源事件");
    within(provenance).getByText("抽取");
    within(provenance).getByText("候选");
    within(provenance).getByText("版本");
    within(provenance).getByText("投递");
  });

  it("某段 missing 时只有那段显示「没有记录」，其余段正常", async () => {
    const missingDelivery = revision({ deliveries: [] });
    await renderApp({
      route: "/governance/memory/note_01",
      me: ME,
      fetch: (path) => {
        if (path === "/v1/admin/team-notes/note_01") {
          return jsonResponse(noteDetail([missingDelivery]));
        }
        if (path.startsWith("/v1/admin/team-notes")) return jsonResponse({ notes: [] });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    const heading = await screen.findByRole("heading", { name: "用 Postgres 存 evidence" });
    const detail = heading.closest(".gv-note-detail") as HTMLElement;
    // 投递段没有记录。
    within(detail).getByText("没有投递记录。");
    // 其余四段仍正常渲染内容，不受影响：源事件带 session、抽取带模型、候选带状态。
    expect(within(detail).getAllByText(/sess_09/).length).toBeGreaterThan(0);
    within(detail).getByText(/deepseek-v4/);
    within(detail).getByText(/已采纳/);
  });

  it("多版本时每个版本各自的溯源链都渲染出来", async () => {
    const older = revision({
      revision: 1,
      candidate_id: "cand_01",
      body: "最早决定用 SQLite，后来才改成 Postgres。",
      created_at: "2026-08-01T00:00:00Z",
      candidate: {
        candidate_id: "cand_01",
        action: "create",
        kind: "decision",
        subject: "用 SQLite 存 evidence",
        body: "……",
        origin_agent_id: "alice-codex",
        evidence_event_ids: ["evt_a"],
        admission_status: "admitted",
        created_at: "2026-08-01T00:00:00Z",
        resulting_note_id: "note_01",
      },
    });
    await renderApp({
      route: "/governance/memory/note_01",
      me: ME,
      fetch: (path) => {
        if (path === "/v1/admin/team-notes/note_01") {
          return jsonResponse(noteDetail([older, revision()]));
        }
        if (path.startsWith("/v1/admin/team-notes")) return jsonResponse({ notes: [] });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    const heading = await screen.findByRole("heading", { name: "用 Postgres 存 evidence" });
    const detail = heading.closest(".gv-note-detail") as HTMLElement;
    within(detail).getByRole("heading", { name: "版本 2" });
    within(detail).getByRole("heading", { name: "版本 1" });
    within(detail).getByText(/最早决定用 SQLite/);
  });

  it("revisions 为空时整块走正向空态而不是错误", async () => {
    await renderApp({
      route: "/governance/memory/note_01",
      me: ME,
      fetch: (path) => {
        if (path === "/v1/admin/team-notes/note_01") return jsonResponse(noteDetail([]));
        if (path.startsWith("/v1/admin/team-notes")) return jsonResponse({ notes: [] });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    const heading = await screen.findByRole("heading", { name: "用 Postgres 存 evidence" });
    const detail = heading.closest(".gv-note-detail") as HTMLElement;
    expect(within(detail).queryByText(/failed|加载失败/i)).toBeNull();
    within(detail).getByText(/还没有版本历史/);
  });

  it("召回表把三类原因拼成一句话", async () => {
    await renderApp({
      route: "/governance/memory/note_01",
      me: ME,
      fetch: (path) => {
        if (path === "/v1/admin/team-notes/note_01") {
          return jsonResponse(
            noteDetail([revision()], [
              {
                observation_id: 41,
                recipient_agent_id: "bob-claude",
                recipient_session_id: "sess_10",
                occurred_at: NOW,
                delivered: false,
                rejection_reasons: ["low_similarity"],
                budget_drop_reasons: ["token_budget"],
                hard_gate_failures: ["audience_mismatch"],
              },
            ]),
          );
        }
        if (path.startsWith("/v1/admin/team-notes")) return jsonResponse({ notes: [] });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByRole("heading", { name: "用 Postgres 存 evidence" });
    const reasons = await screen.findByText(/low_similarity/);
    expect(reasons.textContent).toContain("token_budget");
    expect(reasons.textContent).toContain("audience_mismatch");
  });

  it("召回原因数组为 null（后端 omitempty）时不崩溃", async () => {
    await renderApp({
      route: "/governance/memory/note_01",
      me: ME,
      fetch: (path) => {
        if (path === "/v1/admin/team-notes/note_01") {
          return jsonResponse(
            noteDetail([revision()], [
              {
                observation_id: 41,
                recipient_agent_id: "bob-claude",
                recipient_session_id: "sess_10",
                occurred_at: NOW,
                delivered: true,
                rejection_reasons: null,
                budget_drop_reasons: null,
                hard_gate_failures: null,
              },
            ]),
          );
        }
        if (path.startsWith("/v1/admin/team-notes")) return jsonResponse({ notes: [] });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByRole("heading", { name: "用 Postgres 存 evidence" });
    screen.getByText(/命中并投递/);
  });

  it("右栏取数失败时左栏仍可用", async () => {
    const { user } = await renderApp({
      route: "/governance/memory/note_01",
      me: ME,
      fetch: (path) => {
        if (path === "/v1/admin/team-notes/note_01") {
          return jsonResponse({ code: "internal", message: "boom" }, 500);
        }
        // 只匹配带 query string 的列表端点，不能用 startsWith 兜底——那会连
        // /v1/admin/team-notes/note_02（点进第二条笔记后的详情请求）也一并
        // 吞掉，返回列表 Page 形状而不是 TeamNoteDetail。
        if (path.startsWith("/v1/admin/team-notes?")) {
          return jsonResponse({ notes: [noteSummary({ note_id: "note_02", subject: "另一条事实" })] });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    const link = await screen.findByRole("link", { name: /另一条事实/ });
    expect(link.getAttribute("href")).toBe("/governance/memory/note_02");
    await user.click(link);
    // 左栏（NoteList）在整个交互过程中始终可用：链接一直在，点击不抛异常。
    await screen.findByRole("link", { name: /另一条事实/ });
  });
});
