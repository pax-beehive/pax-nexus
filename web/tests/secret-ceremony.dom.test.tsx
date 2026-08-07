// 一次性密钥仪式。三条不变量：两段式关闭、Esc/遮罩不关、关闭后三处存储
// 都不含令牌。
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SecretCeremony } from "../src/components/SecretCeremony";
import { ToastProvider } from "../src/components/Toasts";
import { setupDomTest } from "./helpers";

setupDomTest();

const TOKEN = "tm_enroll_denr_01.https://portal.example.com.secret-value";

function renderCeremony(onClose = () => {}) {
  return render(
    <ToastProvider>
      <SecretCeremony
        title="一次性接入令牌"
        headline="现在就复制。我们没法再给你看一次。"
        body="这串令牌让 Alice Codex 换取一把长期密钥。"
        value={TOKEN}
        valueLabel="令牌"
        expiresAt="2099-01-01T00:00:00Z"
        steps={["在目标机器上执行命令", "令牌兑换成密钥并自毁", "密钥出现在「活跃密钥」"]}
        recovery="什么都不会坏。取消这条待认领记录、重新发一张即可。"
        command="paxl channel connect onprem --enrollment-token <token>"
        onClose={onClose}
      />
    </ToastProvider>,
  );
}

describe("SecretCeremony", () => {
  it("渲染令牌、倒计时、三步说明与命令块", () => {
    renderCeremony();
    expect(screen.getByText(TOKEN)).toBeDefined();
    expect(screen.getByRole("dialog").getAttribute("aria-modal")).toBe("true");
    expect(screen.getByText("在目标机器上执行命令")).toBeDefined();
    expect(screen.getByText(/paxl channel connect onprem/)).toBeDefined();
    expect(screen.getByRole("button", { name: /复制令牌/ })).toBeDefined();
    expect(screen.getByRole("button", { name: /复制接入命令/ })).toBeDefined();
  });

  it("没有 command 时不渲染命令块与第二个复制按钮", () => {
    render(
      <ToastProvider>
        <SecretCeremony
          title="一次性邀请令牌"
          headline="现在就复制。"
          body="邀请链接。"
          value={TOKEN}
          valueLabel="邀请链接"
          steps={["把链接发给对方"]}
          recovery="吊销后重建即可。"
          onClose={() => {}}
        />
      </ToastProvider>,
    );
    expect(screen.queryByRole("button", { name: /复制接入命令/ })).toBeNull();
  });

  it("第一次点关闭只进入确认态，第二次才真的关", async () => {
    const user = userEvent.setup();
    let closed = 0;
    renderCeremony(() => {
      closed += 1;
    });

    await user.click(screen.getByRole("button", { name: "我已保存，关闭" }));
    expect(closed).toBe(0);
    expect(screen.getByRole("button", { name: "先别关" })).toBeDefined();
    expect(screen.getByText(/再也看不到/)).toBeDefined();

    await user.click(screen.getByRole("button", { name: "确定关闭" }));
    expect(closed).toBe(1);
  });

  it("「先别关」退回未确认态", async () => {
    const user = userEvent.setup();
    let closed = 0;
    renderCeremony(() => {
      closed += 1;
    });

    await user.click(screen.getByRole("button", { name: "我已保存，关闭" }));
    await user.click(screen.getByRole("button", { name: "先别关" }));
    expect(screen.getByRole("button", { name: "我已保存，关闭" })).toBeDefined();
    expect(closed).toBe(0);
  });

  it("Escape 与点击遮罩都不关闭", async () => {
    const user = userEvent.setup();
    let closed = 0;
    const { container } = renderCeremony(() => {
      closed += 1;
    });

    await user.keyboard("{Escape}");
    expect(closed).toBe(0);

    const backdrop = container.querySelector(".ceremony");
    expect(backdrop).not.toBeNull();
    await user.click(backdrop as Element);
    expect(closed).toBe(0);
    expect(screen.getByText(TOKEN)).toBeDefined();
  });

  it("令牌不进入 localStorage、sessionStorage 或 URL", async () => {
    const user = userEvent.setup();
    const { unmount } = renderCeremony();

    await user.click(screen.getByRole("button", { name: "我已保存，关闭" }));
    await user.click(screen.getByRole("button", { name: "确定关闭" }));
    unmount();

    expect(JSON.stringify(localStorage)).not.toContain("secret-value");
    expect(JSON.stringify(sessionStorage)).not.toContain("secret-value");
    expect(window.location.href).not.toContain("secret-value");
  });
});
