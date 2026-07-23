// DOM accessibility tests for the shared Modal (TM-WKS-004): dialog
// semantics, focus moved inside on open, Tab trapped, Escape closes, and
// focus restored to the trigger on close. Also covers the TM-WKS-001
// regression at DOM level: the Create Agent modal must open on origins
// where crypto.randomUUID does not exist.

import { describe, expect, it, vi } from "vitest";
import { fireEvent, screen, within } from "@testing-library/react";
import { jsonResponse, makeMe, renderApp, setupDomTest } from "./helpers";

setupDomTest();

function agentsFetch(path: string, init: RequestInit): Response {
  if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
  throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
}

async function openCreateAgentModal() {
  const app = await renderApp({ route: "/agents", me: makeMe(), fetch: agentsFetch });
  const trigger = await screen.findByRole("button", { name: "+ Create Agent" });
  await app.user.click(trigger);
  const dialog = await screen.findByRole("dialog", { name: "Create Agent" });
  return { ...app, trigger, dialog };
}

describe("shared Modal dialog semantics (Create Agent)", () => {
  it("exposes role=dialog, aria-modal and an accessible name from its title", async () => {
    const { dialog } = await openCreateAgentModal();
    expect(dialog.getAttribute("aria-modal")).toBe("true");
    // The accessible name comes from the visible title via aria-labelledby.
    const titleId = dialog.getAttribute("aria-labelledby");
    expect(titleId).toBeTruthy();
    expect(document.getElementById(titleId as string)?.textContent).toBe("Create Agent");
  });

  it("moves focus into the dialog on open and restores it to the trigger on close", async () => {
    const { user, trigger, dialog } = await openCreateAgentModal();

    const idInput = within(dialog).getByLabelText(/agent_id/);
    expect(document.activeElement).toBe(idInput);

    await user.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  it("closes on Escape and restores focus to the trigger", async () => {
    const { user, trigger } = await openCreateAgentModal();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  it("traps Tab within the dialog in both directions", async () => {
    const { dialog } = await openCreateAgentModal();

    const first = within(dialog).getByLabelText(/agent_id/);
    const last = within(dialog).getByRole("button", { name: "创建" });
    expect(document.activeElement).toBe(first);

    // Tab past the last control wraps to the first one.
    (last as HTMLElement).focus();
    fireEvent.keyDown(last, { key: "Tab" });
    expect(document.activeElement).toBe(first);

    // Shift+Tab before the first control wraps to the last one.
    fireEvent.keyDown(first, { key: "Tab", shiftKey: true });
    expect(document.activeElement).toBe(last);
  });
});

describe("action keys without crypto.randomUUID (plain-HTTP origins)", () => {
  it("opens Create Agent when only crypto.getRandomValues exists", async () => {
    vi.stubGlobal("crypto", {
      getRandomValues: (arr: Uint8Array) => arr.fill(3),
    });
    const { dialog } = await openCreateAgentModal();
    // The form renders, including the per-action key hint.
    within(dialog).getByText(/Idempotency-Key/);
    within(dialog).getByRole("button", { name: "创建" });
  });

  it("opens Create Agent when crypto is entirely unavailable", async () => {
    vi.stubGlobal("crypto", undefined);
    const { dialog } = await openCreateAgentModal();
    within(dialog).getByLabelText(/agent_id/);
  });
});
