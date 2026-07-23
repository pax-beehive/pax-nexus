// DOM tests for the recoverable error boundaries (TM-WKS-003): a failing
// route keeps the shell and navigation usable, a failing modal keeps the
// page behind it usable, recovery actions work, and error logging never
// carries secret material (only the error name and message).

import { useState } from "react";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ErrorBoundary } from "../src/components/ErrorBoundary";
import { Modal } from "../src/components/Modal";
import { jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";

setupDomTest();

/** Silence (and record) React's own error logging plus boundary logs. */
function silenceConsoleError() {
  return vi.spyOn(console, "error").mockImplementation(() => {});
}

describe("route boundary: a failing route keeps the shell usable", () => {
  function brokenThenHealedAgentsFetch() {
    let healed = false;
    return {
      heal: () => {
        healed = true;
      },
      fetch: (path: string, init: RequestInit): Response => {
        if (path.startsWith("/v1/me/agents")) {
          // A malformed payload crashes the My Agents render; the backend
          // healing is simulated by flipping the payload for later loads.
          return jsonResponse({ agents: healed ? [] : [null] });
        }
        if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [] });
        throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
      },
    };
  }

  it("renders a recovery card in place of the route, nav and identity survive", async () => {
    const consoleSpy = silenceConsoleError();
    const { fetch } = brokenThenHealedAgentsFetch();
    await renderApp({ route: "/agents", me: makeMe(), fetch });

    // The route content is replaced by a recovery card...
    const alert = await screen.findByRole("alert");
    within(alert).getByRole("button", { name: "重试" });
    within(alert).getByRole("button", { name: "返回 My Agents" });
    // ...while the shell, navigation and identity stay put.
    expect(screen.getByRole("link", { name: "Members" })).toBeTruthy();
    expect(screen.getByText("alice@example.com")).toBeTruthy();

    // The log line is minimal: region + error name/message, nothing else.
    const line = consoleSpy.mock.calls
      .map((args) => args.map(String).join(" "))
      .find((text) => text.startsWith("[portal] route render error:"));
    expect(line).toBeDefined();
    expect(line).toContain("TypeError");
    expect(line).not.toContain("alice@example.com");
    expect(line).not.toContain("componentStack");
  });

  it("navigating to another route recovers the content area", async () => {
    silenceConsoleError();
    const { fetch } = brokenThenHealedAgentsFetch();
    const { user } = await renderApp({ route: "/agents", me: makeMe(), fetch });

    await screen.findByRole("alert");
    await user.click(screen.getByRole("link", { name: "Members" }));

    // The failing region is left behind; the new route renders normally.
    await screen.findByRole("heading", { name: "Members" });
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("retry remounts the route and recovers once the backend responds sanely", async () => {
    silenceConsoleError();
    const { fetch, heal } = brokenThenHealedAgentsFetch();
    const { user } = await renderApp({ route: "/agents", me: makeMe(), fetch });

    const alert = await screen.findByRole("alert");
    heal();
    await user.click(within(alert).getByRole("button", { name: "重试" }));

    await screen.findByRole("heading", { name: "My Agents" });
    expect(screen.queryByRole("alert")).toBeNull();
    await screen.findByText("还没有 Agent，点击右上角创建。");
  });
});

describe("modal boundary: a failing modal keeps the page behind it usable", () => {
  function Bomb(_props: { secret: string }): JSX.Element {
    throw new Error("boom");
  }

  function ModalHarness() {
    const [open, setOpen] = useState(false);
    const [pageActions, setPageActions] = useState(0);
    return (
      <div>
        <button onClick={() => setOpen(true)}>open settings</button>
        <button onClick={() => setPageActions((n) => n + 1)}>page action {pageActions}</button>
        {open && (
          <Modal title="Settings" onClose={() => setOpen(false)}>
            <Bomb secret="tm_invite_supersecret" />
          </Modal>
        )}
      </div>
    );
  }

  it("keeps the modal chrome and the page behind it alive; closing restores focus", async () => {
    const consoleSpy = silenceConsoleError();
    const user = userEvent.setup();
    render(<ModalHarness />);
    const trigger = screen.getByRole("button", { name: "open settings" });
    await user.click(trigger);

    // The dialog chrome survives even though its content crashed.
    const dialog = await screen.findByRole("dialog", { name: "Settings" });
    const alert = within(dialog).getByRole("alert");
    expect(within(alert).getByRole("button", { name: "关闭" })).toBeTruthy();

    // The page behind the modal still works.
    await user.click(screen.getByRole("button", { name: /page action/ }));
    screen.getByRole("button", { name: "page action 1" });

    // The secret prop passed to the crashing component never reaches logs.
    const logged = consoleSpy.mock.calls.map((args) => args.map(String).join(" ")).join("\n");
    expect(logged).not.toContain("tm_invite_supersecret");
    expect(logged).toContain("[portal] modal render error: Error: boom");

    // Closing the failed modal restores focus to its trigger.
    await user.click(within(alert).getByRole("button", { name: "关闭" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });
});

describe("app boundary: the outermost failure renders a safe recovery page", () => {
  it("shows a full-page recovery with a reload action instead of a blank document", () => {
    silenceConsoleError();
    function Bomb(): JSX.Element {
      throw new Error("app exploded");
    }
    render(
      <ErrorBoundary region="app" fullPage>
        <Bomb />
      </ErrorBoundary>,
    );

    const alert = screen.getByRole("alert");
    within(alert).getByText("页面渲染出错");
    within(alert).getByRole("button", { name: "重新加载" });
  });
});
