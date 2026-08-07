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
        title="One-time enrollment token"
        headline="Copy this now. We can't show it again."
        body="This token lets Alice Codex claim a long-lived key."
        value={TOKEN}
        valueLabel="token"
        expiresAt="2099-01-01T00:00:00Z"
        steps={["Run the command on the target machine", "The token is exchanged for a key and burns itself", "The key appears under “Active keys”"]}
        recovery="Nothing is broken. Cancel the pending claim and issue a new one."
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
    expect(screen.getByText("Run the command on the target machine")).toBeDefined();
    expect(screen.getByText(/paxl channel connect onprem/)).toBeDefined();
    expect(screen.getByRole("button", { name: /Copy token/ })).toBeDefined();
    expect(screen.getByRole("button", { name: /Copy client command/ })).toBeDefined();
  });

  it("没有 command 时不渲染命令块与第二个复制按钮", () => {
    render(
      <ToastProvider>
        <SecretCeremony
          title="One-time invitation token"
          headline="Copy this now."
          body="The invitation link."
          value={TOKEN}
          valueLabel="invitation link"
          steps={["Send the link to the recipient"]}
          recovery="Revoke it and create a new one."
          onClose={() => {}}
        />
      </ToastProvider>,
    );
    expect(screen.queryByRole("button", { name: /Copy client command/ })).toBeNull();
  });

  it("第一次点关闭只进入确认态，第二次才真的关", async () => {
    const user = userEvent.setup();
    let closed = 0;
    renderCeremony(() => {
      closed += 1;
    });

    await user.click(screen.getByRole("button", { name: "I've stored it — close" }));
    expect(closed).toBe(0);
    expect(screen.getByRole("button", { name: "Keep it open" })).toBeDefined();
    expect(screen.getByText(/only copy of this token/)).toBeDefined();

    await user.click(screen.getByRole("button", { name: "Yes, close it" }));
    expect(closed).toBe(1);
  });

  it("「先别关」退回未确认态", async () => {
    const user = userEvent.setup();
    let closed = 0;
    renderCeremony(() => {
      closed += 1;
    });

    await user.click(screen.getByRole("button", { name: "I've stored it — close" }));
    await user.click(screen.getByRole("button", { name: "Keep it open" }));
    expect(screen.getByRole("button", { name: "I've stored it — close" })).toBeDefined();
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

    await user.click(screen.getByRole("button", { name: "I've stored it — close" }));
    await user.click(screen.getByRole("button", { name: "Yes, close it" }));
    unmount();

    expect(JSON.stringify(localStorage)).not.toContain("secret-value");
    expect(JSON.stringify(sessionStorage)).not.toContain("secret-value");
    expect(window.location.href).not.toContain("secret-value");
  });
});
