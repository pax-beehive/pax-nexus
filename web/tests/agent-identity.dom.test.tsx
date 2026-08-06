// Identity 卡（组件级）：身份字段表单 + 乐观锁保存。
//
// 这是组件级测试，不经过 <App />：AgentIdentityCard 是纯受控组件
// （agent/access 由父组件传入，变更通过 onChanged 回传），所以直接渲染
// 组件即可，不需要装配整个页面。页面级用例留给 Task 10。
//
// AgentIdentityCard 通过 useErrorHandler -> useAuth 间接依赖 AuthContext，
// 所以还是要包一层真的 AuthProvider（不然 useAuth 直接抛错）；它挂载时会
// 打一次 GET /v1/me，这里统一桩掉，与本测试要断言的行为无关。
//
// Harness 用一个小 wrapper 组件持有 agent state，模拟父组件在收到
// onChanged 后把 agent prop 换掉这件事——尤其是 409 重取用例：服务端数据
// 必须覆盖本地草稿，而不是反过来。
import { useState } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { AgentProfile, HumanMe } from "../src/api/types";
import { AuthProvider } from "../src/auth/AuthContext";
import { ToastProvider } from "../src/components/Toasts";
import { AgentIdentityCard } from "../src/pages/agent/AgentIdentityCard";
import { resolveAgentAccess } from "../src/pages/agent/agentScope";
import {
  apiErrorResponse,
  callsTo,
  jsonResponse,
  makeAgent,
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

/** Wrapper owning `agent` state, standing in for the future detail page. */
function Harness({
  me,
  initial,
  refetch,
}: {
  me: HumanMe;
  initial: AgentProfile;
  refetch: () => Promise<AgentProfile>;
}) {
  const [agent, setAgent] = useState(initial);
  const access = resolveAgentAccess(me, agent);
  return (
    <AuthProvider>
      <ToastProvider>
        <AgentIdentityCard agent={agent} access={access} onChanged={setAgent} refetch={refetch} />
      </ToastProvider>
    </AuthProvider>
  );
}

const neverRefetch = () => Promise.reject(new Error("refetch should not be called"));

/** GET /v1/me for AuthProvider's boot fetch, plus a scenario-specific extra handler. */
function withMe(me: HumanMe, extra: FetchHandler) {
  return (path: string, init: RequestInit) => {
    if (path === "/v1/me" && (init.method ?? "GET") === "GET") return jsonResponse(me);
    return extra(path, init);
  };
}

describe("AgentIdentityCard", () => {
  it("member 看自己的 Agent：字段可编辑且有保存按钮", async () => {
    const me = makeMe({ role: "member", membership_id: "mbr_01" });
    const agent = makeAgent({ owner_membership_id: "mbr_01" });
    stubFetch(withMe(me, () => apiErrorResponse(500, "unexpected", "no extra calls expected")));

    render(<Harness me={me} initial={agent} refetch={neverRefetch} />);

    const name = (await screen.findByLabelText("显示名")) as HTMLInputElement;
    expect(name.value).toBe("Alice Codex");
    expect(name.disabled).toBe(false);
    expect((screen.getByLabelText("Runtime") as HTMLInputElement).value).toBe("codex");
    expect(screen.getByRole("button", { name: "保存改动" })).toBeDefined();
  });

  it("保存时 resource_version 同时进 body 与 If-Match", async () => {
    const user = userEvent.setup();
    const me = makeMe({ role: "member", membership_id: "mbr_01" });
    const agent = makeAgent({ owner_membership_id: "mbr_01" });
    const fetchMock = stubFetch(
      withMe(me, (path, init) => {
        if (path === "/v1/me/agents/agent-1" && init.method === "PATCH") {
          return jsonResponse({
            agent: makeAgent({ owner_membership_id: "mbr_01", display_name: "Renamed", resource_version: 8 }),
          });
        }
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      }),
    );

    render(<Harness me={me} initial={agent} refetch={neverRefetch} />);

    const name = await screen.findByLabelText("显示名");
    await user.clear(name);
    await user.type(name, "Renamed");
    await user.click(screen.getByRole("button", { name: "保存改动" }));

    const patches = await vi.waitFor(() => {
      const calls = callsTo(fetchMock, "/v1/me/agents/agent-1", "PATCH");
      expect(calls).toHaveLength(1);
      return calls;
    });
    expect(patches[0].headers.get("If-Match")).toBe('"7"');
    expect(JSON.parse(String(patches[0].init.body))).toMatchObject({
      display_name: "Renamed",
      resource_version: 7,
    });
  });

  it("409 冲突时重取并用服务端数据覆盖草稿，而不是反过来", async () => {
    const user = userEvent.setup();
    const me = makeMe({ role: "member", membership_id: "mbr_01" });
    const agent = makeAgent({ owner_membership_id: "mbr_01" });
    stubFetch(
      withMe(me, (path, init) => {
        if (path === "/v1/me/agents/agent-1" && init.method === "PATCH") {
          return apiErrorResponse(409, "resource_version_conflict", "stale");
        }
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      }),
    );
    // 自造的 refetch：模拟 409 之后重取拿到了别人已经落库的改动。父组件
    // （这里是 Harness）通过 onChanged 把它塞回 agent prop。
    const refetch = () =>
      Promise.resolve(
        makeAgent({
          owner_membership_id: "mbr_01",
          display_name: "Someone Else Renamed It",
          resource_version: 9,
        }),
      );

    render(<Harness me={me} initial={agent} refetch={refetch} />);

    const name = await screen.findByLabelText("显示名");
    await user.clear(name);
    await user.type(name, "My Local Draft");
    await user.click(screen.getByRole("button", { name: "保存改动" }));

    // 草稿被服务端数据替换——绝不静默覆盖别人的改动。
    expect(await screen.findByDisplayValue("Someone Else Renamed It")).toBeDefined();
    expect(screen.queryByDisplayValue("My Local Draft")).toBeNull();
  });

  it("admin 看别人的 Agent：字段只读且没有保存按钮", async () => {
    const me = makeMe({ role: "admin", membership_id: "mbr_07" });
    const agent = makeAgent({ owner_membership_id: "mbr_99" });
    stubFetch(withMe(me, () => apiErrorResponse(500, "unexpected", "no extra calls expected")));

    render(<Harness me={me} initial={agent} refetch={neverRefetch} />);

    expect(((await screen.findByLabelText("显示名")) as HTMLInputElement).disabled).toBe(true);
    expect(screen.queryByRole("button", { name: "保存改动" })).toBeNull();
  });
});
