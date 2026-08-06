// 发放接入令牌表单。重点：人类标签映射回正确的原始权限名、两个 Seg 换算
// 正确、以及一次性密钥端点的「不盲目重试」纪律。
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { IssueAccessModal } from "../src/components/IssueAccessModal";
import { ToastProvider } from "../src/components/Toasts";
import { apiErrorResponse, jsonResponse, setupDomTest, stubFetch } from "./helpers";

setupDomTest();

// This modal isn't wired behind renderApp() (out of scope for this task), so
// nothing else seeds the double-submit CSRF cookie humanFetch requires on
// mutations. setupDomTest() only resets it in afterEach, which leaves the
// very first test in the file without one; set it explicitly up front.
beforeEach(() => {
  document.cookie = "tm_csrf=test-csrf";
});

function renderModal(overrides: {
  onCreated?: (s: { enrollment_id: string; token: string; expires_at: string }) => void;
  onMaybeCreated?: () => void;
} = {}) {
  return render(
    <ToastProvider>
      <IssueAccessModal
        agentId="agent-1"
        agentName="Alice Codex"
        onClose={() => {}}
        onCreated={overrides.onCreated ?? (() => {})}
        onMaybeCreated={overrides.onMaybeCreated ?? (() => {})}
      />
    </ToastProvider>,
  );
}

describe("IssueAccessModal", () => {
  it("默认勾选记录会话与检索记忆，提交时发出原始权限名", async () => {
    const user = userEvent.setup();
    const fetchMock = stubFetch(() =>
      jsonResponse({ enrollment_id: "enr_01", token: "tm_enroll_x.y", expires_at: "2099-01-01T00:00:00Z" }),
    );
    renderModal();

    await user.type(screen.getByLabelText(/它会在哪台机器上跑/), "mac-studio-01");
    await user.click(screen.getByRole("button", { name: "发放一次性令牌" }));

    const body = JSON.parse(String(fetchMock.mock.calls[0][1].body));
    expect(body.credential_label).toBe("mac-studio-01");
    expect(body.permissions).toEqual(["observe", "search"]);
    expect(body.expires_in_seconds).toBe(900);
    // 默认 90 天：只断言是个未来时刻，不钉死具体毫秒。
    expect(new Date(body.credential_expires_at).getTime()).toBeGreaterThan(Date.now());
  });

  it("人类标签勾选后映射回对应的原始权限名", async () => {
    const user = userEvent.setup();
    const fetchMock = stubFetch(() =>
      jsonResponse({ enrollment_id: "enr_01", token: "t", expires_at: "2099-01-01T00:00:00Z" }),
    );
    renderModal();

    await user.type(screen.getByLabelText(/它会在哪台机器上跑/), "mac-studio-01");
    await user.click(screen.getByLabelText(/发送给其他 Agent/));
    await user.click(screen.getByRole("button", { name: "发放一次性令牌" }));

    const body = JSON.parse(String(fetchMock.mock.calls[0][1].body));
    expect(body.permissions).toEqual(["observe", "search", "channel_send"]);
  });

  it("「不过期」不发送 credential_expires_at", async () => {
    const user = userEvent.setup();
    const fetchMock = stubFetch(() =>
      jsonResponse({ enrollment_id: "enr_01", token: "t", expires_at: "2099-01-01T00:00:00Z" }),
    );
    renderModal();

    await user.type(screen.getByLabelText(/它会在哪台机器上跑/), "mac-studio-01");
    await user.click(screen.getByRole("button", { name: "不过期" }));
    await user.click(screen.getByRole("button", { name: "发放一次性令牌" }));

    const body = JSON.parse(String(fetchMock.mock.calls[0][1].body));
    expect("credential_expires_at" in body).toBe(false);
  });

  it("认领窗口 5m 换算成 300 秒", async () => {
    const user = userEvent.setup();
    const fetchMock = stubFetch(() =>
      jsonResponse({ enrollment_id: "enr_01", token: "t", expires_at: "2099-01-01T00:00:00Z" }),
    );
    renderModal();

    await user.type(screen.getByLabelText(/它会在哪台机器上跑/), "mac-studio-01");
    await user.click(screen.getByRole("button", { name: "5 分钟" }));
    await user.click(screen.getByRole("button", { name: "发放一次性令牌" }));

    expect(JSON.parse(String(fetchMock.mock.calls[0][1].body)).expires_in_seconds).toBe(300);
  });

  it("机器名为空或权限全不选时本地拦下，不发请求", async () => {
    const user = userEvent.setup();
    const fetchMock = stubFetch(() => {
      throw new Error("不该发出请求");
    });
    renderModal();

    await user.click(screen.getByRole("button", { name: "发放一次性令牌" }));
    expect(screen.getByText(/得先说清楚它在哪台机器上跑/)).toBeDefined();

    await user.type(screen.getByLabelText(/它会在哪台机器上跑/), "mac-studio-01");
    await user.click(screen.getByLabelText(/记录它的会话/));
    await user.click(screen.getByLabelText(/检索团队记忆/));
    await user.click(screen.getByRole("button", { name: "发放一次性令牌" }));
    expect(screen.getByText(/至少要选一项/)).toBeDefined();

    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("4xx 保留表单让用户改", async () => {
    const user = userEvent.setup();
    stubFetch(() => apiErrorResponse(422, "invalid_argument", "bad label"));
    renderModal();

    await user.type(screen.getByLabelText(/它会在哪台机器上跑/), "mac-studio-01");
    await user.click(screen.getByRole("button", { name: "发放一次性令牌" }));

    expect(await screen.findByText(/被拒绝（HTTP 422）/)).toBeDefined();
    expect(screen.getByLabelText(/它会在哪台机器上跑/)).toBeDefined();
  });

  it("5xx 不重试：只发一次请求，走 onMaybeCreated", async () => {
    // 一次性密钥端点没有 Idempotency-Key，盲目重试可能凭空多出一条待认领
    // 记录（doc 3.3）。
    const user = userEvent.setup();
    const onMaybeCreated = vi.fn();
    const fetchMock = stubFetch(() => apiErrorResponse(503, "unavailable", "down"));
    renderModal({ onMaybeCreated });

    await user.type(screen.getByLabelText(/它会在哪台机器上跑/), "mac-studio-01");
    await user.click(screen.getByRole("button", { name: "发放一次性令牌" }));

    await vi.waitFor(() => expect(onMaybeCreated).toHaveBeenCalledTimes(1));
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("请求不带 Idempotency-Key", async () => {
    const user = userEvent.setup();
    const fetchMock = stubFetch(() =>
      jsonResponse({ enrollment_id: "enr_01", token: "t", expires_at: "2099-01-01T00:00:00Z" }),
    );
    renderModal();

    await user.type(screen.getByLabelText(/它会在哪台机器上跑/), "mac-studio-01");
    await user.click(screen.getByRole("button", { name: "发放一次性令牌" }));

    const headers = new Headers(fetchMock.mock.calls[0][1].headers);
    expect(headers.get("Idempotency-Key")).toBeNull();
  });
});
