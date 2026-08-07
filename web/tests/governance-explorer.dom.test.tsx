// Governance · Memory explorer 双栏页：左栏（NoteList，Task 6）与右栏
// （溯源链 + 召回表，Task 7）各自独立取数。核心不变量是：
//   - 左栏永远不因为打开一条笔记而多打请求（NoteList 自己管自己的分页）
//   - 打开一条笔记只发列表 + 详情两个请求——不多不少，诊断端点一次都不碰
//   - 任何一段缺记录，只影响那一段，其余段与整条链的可读性不受影响
import { describe, expect, it } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import { callsTo, jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";

setupDomTest();

const NOW = "2026-08-06T12:00:00Z";

function noteSummary(overrides: Record<string, unknown> = {}) {
  return {
    note_id: "note_01",
    kind: "handoff",
    subject: "用 Postgres 存 evidence",
    state: "active",
    origin_agent_id: "alice-codex",
    audience_agent_ids: [] as string[],
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
      kind: "handoff",
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
      related_subjects: [] as string[],
    },
    related_notes: [] as ReturnType<typeof noteSummary>[],
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

  it("左栏链接指向 /governance/memory/:id，行卡显示类型 Tag", async () => {
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
    // handoff 的中文标签，Kind 补回行卡（I1）。
    within(link).getByText("交接");
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

  it("没有 view.team-memory 能力时，/v1/admin/team-notes 零请求", async () => {
    // RequireCapability 挡在路由外层，未挂载页面组件就没有请求——门控本身
    // 不是这条用例要测的（navModel/legacy-routes 已经测过），这里钉住的是
    // 「组件不挂载 = 不发请求」这条更底层的不变量，防止有人把取数逻辑挪到
    // 门控外面。落地页是 Overview（view.operations 还在），顺带把它的必需
    // 端点也 stub 掉，让页面能正常落地而不是卡在 loading。
    const { fetchMock } = await renderApp({
      route: "/governance/memory",
      me: makeMe({ role: "admin", capabilities: ["view.operations"] }),
      fetch: (path) => {
        if (path.startsWith("/v1/admin/overview")) {
          return jsonResponse({
            from_time: NOW,
            to_time: NOW,
            generated_at: NOW,
            metrics: {
              evidence_captured: 0,
              live_notes: 0,
              notes_expiring_today: 0,
              recalls_served: 0,
              recall_accept_rate: 0,
              attention_count: 0,
            },
            series: [],
            note_mix: [],
            attention: [],
          });
        }
        if (path.startsWith("/v1/admin/operations/agents")) {
          return jsonResponse({ agents: [], from_time: NOW, to_time: NOW, generated_at: NOW });
        }
        if (path.startsWith("/v1/admin/operations/events")) {
          return jsonResponse({ events: [], generated_at: NOW });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    await screen.findByRole("heading", { name: "Overview" });
    expect(callsTo(fetchMock, "/v1/admin/team-notes")).toHaveLength(0);
  });

  it("点击「已解决」Seg 后，请求带 state=resolved", async () => {
    const { user, fetchMock } = await renderApp({
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
    await user.click(screen.getByRole("button", { name: "已解决" }));

    await waitFor(() => {
      expect(
        callsTo(fetchMock, "/v1/admin/team-notes").some((call) => call.path.includes("state=resolved")),
      ).toBe(true);
    });
  });

  it("Kind 下拉发出 kind 参数", async () => {
    const { user, fetchMock } = await renderApp({
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
    await user.selectOptions(screen.getByRole("combobox", { name: "按类型筛选" }), "blocker");

    await waitFor(() => {
      expect(
        callsTo(fetchMock, "/v1/admin/team-notes").some((call) => call.path.includes("kind=blocker")),
      ).toBe(true);
    });
  });

  // I5: Enter-only submission left mouse/touch users with no way to search at
  // all -- restores the Search button the old page had.
  it("点击搜索按钮（不按 Enter）也会带上 q= 发请求", async () => {
    const { user, fetchMock } = await renderApp({
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
    await user.type(screen.getByPlaceholderText("搜索主题或正文"), "postgres");
    await user.click(screen.getByRole("button", { name: "搜索" }));

    await waitFor(() => {
      expect(
        callsTo(fetchMock, "/v1/admin/team-notes").some((call) => call.path.includes("q=postgres")),
      ).toBe(true);
    });
  });
});

describe("Governance · Memory explorer — 右栏溯源链", () => {
  it("只打列表 + 详情两个请求，六段的阶段名都出现在页面上，诊断端点零请求", async () => {
    const { fetchMock } = await renderApp({
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
                rejection_reasons: [],
                budget_drop_reasons: [],
                hard_gate_failures: [],
              },
            ]),
          );
        }
        if (path.startsWith("/v1/admin/team-notes")) {
          return jsonResponse({ notes: [noteSummary()] });
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    const heading = await screen.findByRole("heading", { name: "用 Postgres 存 evidence" });
    const detail = heading.closest(".gv-note-detail") as HTMLElement;

    // 总量断言：右栏挂载期间只有列表 + 详情两个 /v1/admin/ 请求——不许多打
    // 诊断端点。只钉「同一个端点不重复打」（旧断言）钉不住这条：额外调一次
    // getExtractionDiagnostic 并 .catch(() => {}) 吞掉错误，旧断言照样全绿。
    const adminCalls = fetchMock.mock.calls
      .map(([url]) => String(url))
      .filter((url) => url.startsWith("/v1/admin/"));
    expect(adminCalls).toHaveLength(2);
    expect(adminCalls.some((url) => url.includes("/diagnostics/"))).toBe(false);
    expect(callsTo(fetchMock, "/v1/admin/team-notes/note_01")).toHaveLength(1);

    const provenance = within(detail)
      .getByRole("heading", { name: "它是怎么来的" })
      .closest("section")!;
    within(provenance).getByText("源事件");
    within(provenance).getByText("抽取");
    within(provenance).getByText("候选");
    within(provenance).getByText("版本");
    within(provenance).getByText("投递");
    // 第六段：召回决策——挂在笔记而非版本上，由 NoteRecalls 单独渲染。
    within(detail).getByRole("heading", { name: "每一次被端到 Agent 面前" });
    within(detail).getByText(/命中并投递/);
  });

  it("笔记头显示受众与 note_id · rev N · state；相关的事实块渲染 chips 与交叉链接", async () => {
    const detail = noteDetail([revision()]);
    detail.note.summary.audience_agent_ids = ["bob-claude", "carol-gemini"];
    detail.note.related_subjects = ["release owner"];
    detail.related_notes = [noteSummary({ note_id: "note_02", subject: "另一条相关事实" })];

    await renderApp({
      route: "/governance/memory/note_01",
      me: ME,
      fetch: (path) => {
        if (path === "/v1/admin/team-notes/note_01") return jsonResponse(detail);
        if (path.startsWith("/v1/admin/team-notes")) return jsonResponse({ notes: [] });
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    const heading = await screen.findByRole("heading", { name: "用 Postgres 存 evidence" });
    const panel = heading.closest(".gv-note-detail") as HTMLElement;
    // I3：受众 + note_id · rev N · state。
    within(panel).getByText(/bob-claude、carol-gemini/);
    within(panel).getByText(/note_01 · rev 2 · active/);
    // I2：related_subjects 变 chips，related_notes 是可点的交叉链接。
    within(panel).getByRole("heading", { name: "相关的事实" });
    within(panel).getByText("release owner");
    const relatedLink = within(panel).getByRole("link", { name: "另一条相关事实" });
    expect(relatedLink.getAttribute("href")).toBe("/governance/memory/note_02");
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
        kind: "handoff",
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

  it("右栏取数失败时左栏仍可用，且右栏确实显示了可重试的错误态", async () => {
    let noteGets = 0;
    const { user } = await renderApp({
      route: "/governance/memory/note_01",
      me: ME,
      fetch: (path) => {
        if (path === "/v1/admin/team-notes/note_01") {
          noteGets += 1;
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

    // I1: 右栏之前是不可重试的死路 <div>，现在复用 RegionError（含 Retry）。
    // 同样的文案还会作为全局 toast 出现一次，所以按容器限定查询范围。
    const retry = await screen.findByRole("button", { name: "Retry" });
    within(retry.closest(".gv-note-detail") as HTMLElement).getByText(
      "Server error; try again later",
    );
    expect(noteGets).toBe(1);

    // Retry 确实重新发出了 getTeamNote（不是死按钮）——在导航到别的笔记之前
    // 验证，避免下一步的导航把这个按钮换成另一条笔记的 Retry。
    await user.click(retry);
    await waitFor(() => expect(noteGets).toBe(2));

    const link = await screen.findByRole("link", { name: /另一条事实/ });
    expect(link.getAttribute("href")).toBe("/governance/memory/note_02");
    await user.click(link);
    // 左栏（NoteList）在整个交互过程中始终可用：链接一直在，点击不抛异常。
    await screen.findByRole("link", { name: /另一条事实/ });
  });

  // C1 item 4: only the reverse direction (右栏失败时左栏仍可用) had
  // coverage. This pins the direction the page's whole point rests on: a
  // broken left-column list must not take down the six-stage chain someone
  // followed a direct link to.
  it("左栏取数失败但右栏仍渲染完整链条", async () => {
    await renderApp({
      route: "/governance/memory/note_01",
      me: ME,
      fetch: (path) => {
        if (path === "/v1/admin/team-notes/note_01") {
          return jsonResponse(noteDetail([revision()]));
        }
        if (path.startsWith("/v1/admin/team-notes?")) {
          return jsonResponse({ code: "internal", message: "boom" }, 500);
        }
        throw new Error(`unexpected fetch: ${path}`);
      },
    });

    // 左栏塌成可重试错误。
    await screen.findByText("加载失败。");
    screen.getByRole("button", { name: "重试" });

    // 右栏仍然渲染完整链条：笔记头 + 六段的阶段名。
    const heading = await screen.findByRole("heading", { name: "用 Postgres 存 evidence" });
    const detail = heading.closest(".gv-note-detail") as HTMLElement;
    for (const stage of ["源事件", "抽取", "候选", "版本", "投递"]) {
      within(detail).getByText(stage);
    }
    within(detail).getByText("每一次被端到 Agent 面前");
  });
});
