// Lifecycle 三张卡与它们的确认弹窗。
//
// 最关键的一条：销毁类确认框里列出的密钥，就是页面上那两张卡渲染的同一个
// 数组——不是重新数的。这条由「同一个 props 数组」保证，测试用变异验证。
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { AgentLifecycleCard } from "../src/pages/agent/AgentLifecycleCard";
import { resolveAgentAccess } from "../src/pages/agent/agentScope";
import { ToastProvider } from "../src/components/Toasts";
import {
  callsTo,
  jsonResponse,
  makeAgent,
  makeCredential,
  makeEnrollment,
  makeMe,
  makeMember,
  setupDomTest,
  stubFetch,
} from "./helpers";

setupDomTest();

function renderCard(options: {
  me?: ReturnType<typeof makeMe>;
  agent?: ReturnType<typeof makeAgent>;
  credentials?: ReturnType<typeof makeCredential>[];
  enrollments?: ReturnType<typeof makeEnrollment>[];
}) {
  const me = options.me ?? makeMe({ role: "member", membership_id: "mbr_01" });
  const agent = options.agent ?? makeAgent({ owner_membership_id: "mbr_01" });
  render(
    <MemoryRouter>
      <ToastProvider>
        <AgentLifecycleCard
          agent={agent}
          access={resolveAgentAccess(me, agent)}
          pendingEnrollments={options.enrollments ?? []}
          activeCredentials={options.credentials ?? []}
          onChanged={() => {}}
          refetch={() => Promise.resolve(agent)}
        />
      </ToastProvider>
    </MemoryRouter>,
  );
}

describe("卡的可见性", () => {
  it("member 看自己的活跃 Agent：暂停 + 退役，没有移交", () => {
    renderCard({});
    expect(screen.getByRole("button", { name: "暂停" })).toBeDefined();
    expect(screen.getByRole("button", { name: "退役" })).toBeDefined();
    expect(screen.queryByRole("button", { name: "移交" })).toBeNull();
    expect(screen.queryByRole("button", { name: "恢复" })).toBeNull();
  });

  it("suspended 状态下暂停换成恢复", () => {
    renderCard({ agent: makeAgent({ owner_membership_id: "mbr_01", status: "suspended" }) });
    expect(screen.getByRole("button", { name: "恢复" })).toBeDefined();
    expect(screen.queryByRole("button", { name: "暂停" })).toBeNull();
  });

  it("owner 看别人的 Agent：四种动作都在", () => {
    renderCard({
      me: makeMe({ role: "owner", membership_id: "mbr_01" }),
      agent: makeAgent({ owner_membership_id: "mbr_99" }),
    });
    expect(screen.getByRole("button", { name: "暂停" })).toBeDefined();
    expect(screen.getByRole("button", { name: "退役" })).toBeDefined();
    expect(screen.getByRole("button", { name: "移交" })).toBeDefined();
  });

  it("admin 看别人的 Agent：只有暂停", () => {
    renderCard({
      me: makeMe({ role: "admin", membership_id: "mbr_07" }),
      agent: makeAgent({ owner_membership_id: "mbr_99" }),
    });
    expect(screen.getByRole("button", { name: "暂停" })).toBeDefined();
    expect(screen.queryByRole("button", { name: "退役" })).toBeNull();
    expect(screen.queryByRole("button", { name: "移交" })).toBeNull();
  });

  it("退役后三张卡都消失，只剩终态说明", () => {
    renderCard({
      agent: makeAgent({ owner_membership_id: "mbr_01", status: "retired", retired_at: "2026-08-01T00:00:00Z" }),
    });
    expect(screen.queryByRole("button", { name: "暂停" })).toBeNull();
    expect(screen.queryByRole("button", { name: "退役" })).toBeNull();
    expect(screen.getByText(/终态，无法恢复/)).toBeDefined();
  });
});

describe("销毁类确认框的后果清单", () => {
  it("列出的密钥就是卡片拿到的那两个数组", async () => {
    const user = userEvent.setup();
    renderCard({
      credentials: [
        makeCredential({ credential_id: "cred_a", label: "mac-studio-01" }),
        makeCredential({ credential_id: "cred_b", label: "linux-box" }),
      ],
      enrollments: [makeEnrollment({ enrollment_id: "enr_a", credential_label: "待认领的机器" })],
    });

    await user.click(screen.getByRole("button", { name: "暂停" }));
    const dialog = screen.getByRole("dialog");
    expect(dialog.textContent).toContain("mac-studio-01");
    expect(dialog.textContent).toContain("linux-box");
    expect(dialog.textContent).toContain("待认领的机器");
    expect(dialog.textContent).toContain("2 把活跃密钥");
    expect(dialog.textContent).toContain("1 张未认领令牌");
  });

  it("某条腿没取到时说「可能更多」，而不是显示 0", async () => {
    const user = userEvent.setup();
    const me = makeMe({ role: "member", membership_id: "mbr_01" });
    const agent = makeAgent({ owner_membership_id: "mbr_01" });
    render(
      <MemoryRouter>
        <ToastProvider>
          <AgentLifecycleCard
            agent={agent}
            access={resolveAgentAccess(me, agent)}
            pendingEnrollments={undefined}
            activeCredentials={undefined}
            onChanged={() => {}}
            refetch={() => Promise.resolve(agent)}
          />
        </ToastProvider>
      </MemoryRouter>,
    );

    await user.click(screen.getByRole("button", { name: "暂停" }));
    const dialog = screen.getByRole("dialog");
    expect(dialog.textContent).toContain("没取到");
    expect(dialog.textContent).not.toContain("0 把活跃密钥");
  });
});

describe("动作请求", () => {
  it("暂停走 PATCH status=suspended，带 If-Match", async () => {
    const user = userEvent.setup();
    const fetchMock = stubFetch(() =>
      jsonResponse({ agent: makeAgent({ owner_membership_id: "mbr_01", status: "suspended" }) }),
    );
    renderCard({});

    await user.click(screen.getByRole("button", { name: "暂停" }));
    await user.click(screen.getByRole("button", { name: "暂停并销毁密钥" }));

    const patches = callsTo(fetchMock, "/v1/me/agents/agent-1", "PATCH");
    expect(patches).toHaveLength(1);
    expect(JSON.parse(String(patches[0].init.body))).toMatchObject({
      status: "suspended",
      resource_version: 7,
    });
    expect(patches[0].headers.get("If-Match")).toBe('"7"');
  });

  it("退役走 DELETE 且带 Idempotency-Key", async () => {
    const user = userEvent.setup();
    const fetchMock = stubFetch(() =>
      jsonResponse({ agent: makeAgent({ owner_membership_id: "mbr_01", status: "retired" }) }),
    );
    renderCard({});

    await user.click(screen.getByRole("button", { name: "退役" }));
    await user.click(screen.getByRole("button", { name: "永久退役" }));

    const deletes = callsTo(fetchMock, "/v1/me/agents/agent-1", "DELETE");
    expect(deletes).toHaveLength(1);
    expect(deletes[0].headers.get("Idempotency-Key")).toBeTruthy();
  });

  it("移交恒走 admin 端点，即使动作 scope 是 me", async () => {
    // owner 移交自己的 Agent：页面其余动作走 /v1/me/*，移交没有 me 端点。
    const user = userEvent.setup();
    const fetchMock = stubFetch((path) => {
      if (path.startsWith("/v1/admin/members")) {
        return jsonResponse({ members: [makeMember({ membership_id: "mbr_99", display_name: "Bob" })] });
      }
      return jsonResponse({ agent: makeAgent({ owner_membership_id: "mbr_99" }) });
    });
    renderCard({
      me: makeMe({ role: "owner", membership_id: "mbr_01" }),
      agent: makeAgent({ owner_membership_id: "mbr_01" }),
    });

    await user.click(screen.getByRole("button", { name: "移交" }));
    await user.selectOptions(await screen.findByLabelText("交给谁"), "mbr_99");
    await user.click(screen.getByRole("button", { name: "移交并吊销密钥" }));

    const posts = callsTo(fetchMock, "/v1/admin/agents/agent-1/transfer", "POST");
    expect(posts).toHaveLength(1);
    expect(JSON.parse(String(posts[0].init.body))).toMatchObject({
      target_membership_id: "mbr_99",
      resource_version: 7,
    });
  });
});
