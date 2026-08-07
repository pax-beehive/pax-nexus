// 密钥两卡：三态、吊销、历史开关、发放到仪式的完整链路。
//
// 组件级测试（不经过 <App />），与 agent-identity.dom.test.tsx 同构：
// AgentKeysSection 通过 useErrorHandler -> useAuth 间接依赖 AuthContext，
// 所以要包一层真的 AuthProvider（否则 useAuth 直接抛错）；它挂载时会打一次
// GET /v1/me，用 withMe 统一桩掉，与本文件要断言的行为无关。
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { AgentProfile, HumanMe } from "../src/api/types";
import { AuthProvider } from "../src/auth/AuthContext";
import { AgentKeysSection } from "../src/pages/agent/AgentKeysSection";
import { resolveAgentAccess } from "../src/pages/agent/agentScope";
import type { AgentKeys } from "../src/pages/agent/useAgentKeys";
import { ToastProvider } from "../src/components/Toasts";
import {
  apiErrorResponse,
  callsTo,
  jsonResponse,
  makeAgent,
  makeCredential,
  makeEnrollment,
  makeMe,
  resetBrowserState,
  setupDomTest,
  stubFetch,
  type FetchHandler,
} from "./helpers";

setupDomTest();

// setupDomTest() only resets browser state (incl. the CSRF cookie) in
// afterEach; the first test in a file otherwise boots with no cookie set,
// so every test here re-primes it up front.
beforeEach(() => {
  resetBrowserState();
});

/** GET /v1/me for AuthProvider's boot fetch, plus a scenario-specific extra handler. */
function withMe(me: HumanMe, extra: FetchHandler): FetchHandler {
  return (path, init) => {
    if (path === "/v1/me" && (init.method ?? "GET") === "GET") return jsonResponse(me);
    return extra(path, init);
  };
}

function renderKeys(
  keys: {
    enrollments: { items?: unknown[]; error?: unknown; loading: boolean };
    credentials: { items?: unknown[]; error?: unknown; loading: boolean };
    reload?: () => void;
  },
  opts: {
    me?: HumanMe;
    agent?: AgentProfile;
    fetch?: FetchHandler;
  } = {},
) {
  const me = opts.me ?? makeMe({ role: "member", membership_id: "mbr_01" });
  const agent = opts.agent ?? makeAgent({ owner_membership_id: "mbr_01" });
  const fetchMock = stubFetch(
    withMe(
      me,
      opts.fetch ?? (() => apiErrorResponse(500, "unexpected", "no extra calls expected")),
    ),
  );
  render(
    <AuthProvider>
      <ToastProvider>
        <AgentKeysSection
          agent={agent}
          access={resolveAgentAccess(me, agent)}
          keys={{ reload: keys.reload ?? (() => {}), ...keys } as unknown as AgentKeys}
        />
      </ToastProvider>
    </AuthProvider>,
  );
  return { fetchMock, agent, me };
}

describe("三态", () => {
  it("加载中两张卡各显示加载态", () => {
    renderKeys({ enrollments: { loading: true }, credentials: { loading: true } });
    expect(screen.getAllByText("加载中…").length).toBe(2);
  });

  it("一条腿失败不影响另一条", () => {
    renderKeys({
      enrollments: { error: new Error("boom"), loading: false },
      credentials: { items: [makeCredential({ label: "mac-studio-01" })], loading: false },
    });
    expect(screen.getByRole("button", { name: "Retry" })).toBeDefined();
    expect(screen.getByText("mac-studio-01")).toBeDefined();
  });

  it("空列表走正向空态而不是错误", () => {
    renderKeys({
      enrollments: { items: [], loading: false },
      credentials: { items: [], loading: false },
    });
    expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
    expect(screen.getByText(/还没有待认领的令牌/)).toBeDefined();
    expect(screen.getByText(/还没有任何密钥/)).toBeDefined();
  });
});

// 此前每一次 renderKeys 都传 enrollments: { items: [] } 或 { error }——从没
// 有一条用例渲染过一条真实的待认领令牌，所以「取消」按钮、EnrollmentRows
// 的渲染路径都是零覆盖的死角。
describe("待认领令牌", () => {
  it("非空渲染 + 取消带 Idempotency-Key 并走动作 scope", async () => {
    const user = userEvent.setup();
    const { fetchMock } = renderKeys(
      {
        enrollments: {
          items: [makeEnrollment({ enrollment_id: "enr_01", credential_label: "raspberry-pi" })],
          loading: false,
        },
        credentials: { items: [], loading: false },
      },
      { fetch: () => jsonResponse({ enrollment: makeEnrollment({ enrollment_id: "enr_01" }) }) },
    );

    expect(screen.getByText("raspberry-pi")).toBeDefined();
    await user.click(screen.getByRole("button", { name: "取消" }));
    await user.click(screen.getByRole("button", { name: "确认取消" }));

    const calls = await vi.waitFor(() => {
      const found = callsTo(fetchMock, "/v1/me/agents/agent-1/enrollments/enr_01", "DELETE");
      expect(found).toHaveLength(1);
      return found;
    });
    expect(calls[0].headers.get("Idempotency-Key")).toBeTruthy();
  });
});

describe("吊销", () => {
  it("吊销密钥带 Idempotency-Key 并走动作 scope", async () => {
    const user = userEvent.setup();
    const { fetchMock } = renderKeys(
      {
        enrollments: { items: [], loading: false },
        credentials: {
          items: [makeCredential({ credential_id: "cred_01", label: "mac-studio-01" })],
          loading: false,
        },
      },
      { fetch: () => jsonResponse({ credential: makeCredential() }) },
    );

    await user.click(screen.getByRole("button", { name: "吊销" }));
    await user.click(screen.getByRole("button", { name: "确认吊销" }));

    const calls = await vi.waitFor(() => {
      const found = callsTo(fetchMock, "/v1/me/agents/agent-1/credentials/cred_01", "DELETE");
      expect(found).toHaveLength(1);
      return found;
    });
    expect(calls[0].headers.get("Idempotency-Key")).toBeTruthy();
  });
});

describe("历史", () => {
  it("默认不请求历史，点开后才请求且不带 status", async () => {
    const user = userEvent.setup();
    const { fetchMock } = renderKeys(
      {
        enrollments: { items: [], loading: false },
        credentials: { items: [], loading: false },
      },
      {
        fetch: () =>
          jsonResponse({ credentials: [makeCredential({ revoked_at: "2026-01-01T00:00:00Z" })] }),
      },
    );

    expect(callsTo(fetchMock, "/v1/me/agents/agent-1/credentials", "GET")).toHaveLength(0);
    await user.click(screen.getByRole("button", { name: "显示密钥历史" }));

    await screen.findByText(/revoked/);
    const calls = callsTo(fetchMock, "/v1/me/agents/agent-1/credentials", "GET");
    expect(calls).toHaveLength(1);
    expect(calls[0].path).not.toContain("status=");
  });

  // owner/admin 查看自己名下的 Agent 是唯一一种 readScope 与 actScope 分叉
  // 的组合（readScope 因为 view.all-agents 能力解析成 "admin"，actScope
  // 因为 isSelf 解析成 "me"）；用 member 自查自己的 Agent 测不出这个区别,
  // 因为那种组合下两个 scope 都是 "me"。这条用例把「历史区必须跟实时区
  // 同源、走 actScope」的选择钉住——把 AgentKeysSection.tsx 里两处历史调用
  // 点改回 access.readScope，这条用例必须变红。
  it("owner 查看自己名下的 Agent：历史走 me scope，不是 admin scope", async () => {
    const user = userEvent.setup();
    const me = makeMe({ role: "owner", membership_id: "mbr_01" });
    const agent = makeAgent({ owner_membership_id: "mbr_01" });
    const { fetchMock } = renderKeys(
      {
        enrollments: { items: [], loading: false },
        credentials: { items: [], loading: false },
      },
      {
        me,
        agent,
        fetch: () => jsonResponse({ credentials: [makeCredential()] }),
      },
    );

    await user.click(screen.getByRole("button", { name: "显示密钥历史" }));
    await screen.findByText("Alice MacBook");

    expect(callsTo(fetchMock, "/v1/me/agents/agent-1/credentials", "GET")).toHaveLength(1);
    expect(callsTo(fetchMock, "/v1/admin/agents/agent-1/credentials", "GET")).toHaveLength(0);
  });

  // 同上一条的镜像，但打在 EnrollmentHistory 上：任务 8 那轮修复只有
  // CredentialHistory 落了测试（e4bd241），EnrollmentHistory 里同样的
  // access.actScope 选择（:207）此前一直裸奔——把它改回 access.readScope，
  // 这条用例必须变红。
  it("owner 查看自己名下的 Agent：令牌历史走 me scope，不是 admin scope", async () => {
    const user = userEvent.setup();
    const me = makeMe({ role: "owner", membership_id: "mbr_01" });
    const agent = makeAgent({ owner_membership_id: "mbr_01" });
    const { fetchMock } = renderKeys(
      {
        enrollments: { items: [], loading: false },
        credentials: { items: [], loading: false },
      },
      {
        me,
        agent,
        fetch: () => jsonResponse({ enrollments: [makeEnrollment()] }),
      },
    );

    await user.click(screen.getByRole("button", { name: "显示令牌历史" }));
    await screen.findByText("Alice MacBook");

    expect(callsTo(fetchMock, "/v1/me/agents/agent-1/enrollments", "GET")).toHaveLength(1);
    expect(callsTo(fetchMock, "/v1/admin/agents/agent-1/enrollments", "GET")).toHaveLength(0);
  });
});

describe("发放到仪式", () => {
  it("发放成功后进入全屏仪式，关闭时提示令牌仍可兑换", async () => {
    const user = userEvent.setup();
    renderKeys(
      {
        enrollments: { items: [], loading: false },
        credentials: { items: [], loading: false },
      },
      {
        fetch: () =>
          jsonResponse({
            enrollment_id: "enr_01",
            token: "tm_enroll_x.secret-value",
            expires_at: "2099-01-01T00:00:00Z",
          }),
      },
    );

    await user.click(screen.getByRole("button", { name: "发放接入权限" }));
    await user.type(screen.getByLabelText(/它会在哪台机器上跑/), "mac-studio-01");
    await user.click(screen.getByRole("button", { name: "发放一次性令牌" }));

    expect(await screen.findByText("tm_enroll_x.secret-value")).toBeDefined();
    // 仪式文案必须对齐设备/邀请两处调用点的模板（同一份「只展示一次」的
    // 警告 + 紧迫感 headline），不是一处脱节的自造文案。
    expect(screen.getByText("一次性接入令牌 · 只展示一次，不存任何地方")).toBeDefined();
    // 命令块必须真的渲染出 Agent 侧的接入命令（enrollmentConnectCommand），
    // 不是设备侧的 deviceConnectCommand——只断言按钮名（"复制接入命令"）
    // 抓不住两条命令被对调，见 devices.dom.test.tsx:80-87 的同型注释。
    expect(screen.getByText(/paxl channel connect onprem/)).toBeDefined();
    await user.click(screen.getByRole("button", { name: "我已保存，关闭" }));
    await user.click(screen.getByRole("button", { name: "确定关闭" }));

    expect(screen.queryByText("tm_enroll_x.secret-value")).toBeNull();
    expect(JSON.stringify(sessionStorage)).not.toContain("secret-value");
    expect(JSON.stringify(localStorage)).not.toContain("secret-value");
  });

  it("admin 看别人的 Agent 时没有发放按钮", () => {
    const me = makeMe({ role: "admin", membership_id: "mbr_07" });
    const agent = makeAgent({ owner_membership_id: "mbr_99" });
    renderKeys(
      {
        enrollments: { items: [], loading: false },
        credentials: { items: [], loading: false },
      },
      { me, agent },
    );
    expect(screen.queryByRole("button", { name: "发放接入权限" })).toBeNull();
  });
});
