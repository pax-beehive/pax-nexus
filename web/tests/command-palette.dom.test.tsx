import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { cleanup } from "@testing-library/react";
import { CommandPalette } from "../src/components/CommandPalette";
import { navSections } from "../src/app/navModel";
import type { Role } from "../src/api/types";
import { jsonResponse, makeMe, stubFetch } from "./helpers";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function renderPalette(onOpenChange = vi.fn(), role: Role = "admin") {
  const me = makeMe({ role });
  render(
    <MemoryRouter>
      <CommandPalette me={me} open onOpenChange={onOpenChange} sections={navSections(me)} />
    </MemoryRouter>,
  );
  return { user: userEvent.setup(), onOpenChange };
}

describe("CommandPalette", () => {
  it("lists navigation actions filtered by the query", async () => {
    stubFetch(() => jsonResponse({ agents: [] }));
    const { user } = renderPalette();

    await user.type(screen.getByRole("combobox", { name: "Search" }), "devices");
    await waitFor(() => screen.getByRole("option", { name: /Devices/ }));
    expect(screen.queryByRole("option", { name: /Audit trail/ })).toBeNull();
  });

  it("closes on Escape", async () => {
    stubFetch(() => jsonResponse({ agents: [] }));
    const { user, onOpenChange } = renderPalette();

    await user.keyboard("{Escape}");
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("moves the active option with the arrow keys", async () => {
    stubFetch(() => jsonResponse({ agents: [] }));
    const { user } = renderPalette();

    const input = screen.getByRole("combobox", { name: "Search" });
    await user.type(input, "a");
    await waitFor(() => expect(screen.getAllByRole("option").length).toBeGreaterThan(1));

    const first = screen.getAllByRole("option")[0];
    expect(first.getAttribute("aria-selected")).toBe("true");
    await user.keyboard("{ArrowDown}");
    expect(screen.getAllByRole("option")[1].getAttribute("aria-selected")).toBe("true");
  });

  it("surfaces agents returned by the search endpoint", async () => {
    stubFetch((path) => {
      if (path.startsWith("/v1/admin/agents")) {
        return jsonResponse({ agents: [{ agent_id: "codex-planner", display_name: "Codex Planner" }] });
      }
      if (path.startsWith("/v1/wiki/search")) return jsonResponse({ results: [] });
      throw new Error(`unexpected fetch: ${path}`);
    });
    const { user } = renderPalette();

    await user.type(screen.getByRole("combobox", { name: "Search" }), "codex");
    await waitFor(() => screen.getByRole("option", { name: /Codex Planner/ }));
  });

  it("still passes q to the admin search endpoint (no accidental double-filtering)", async () => {
    const fetchMock = stubFetch((path) => {
      if (path.startsWith("/v1/admin/agents")) {
        return jsonResponse({ agents: [{ agent_id: "codex-planner", display_name: "Codex Planner" }] });
      }
      if (path.startsWith("/v1/wiki/search")) return jsonResponse({ results: [] });
      throw new Error(`unexpected fetch: ${path}`);
    });
    const { user } = renderPalette(vi.fn(), "admin");

    await user.type(screen.getByRole("combobox", { name: "Search" }), "codex");
    await waitFor(() => screen.getByRole("option", { name: /Codex Planner/ }));

    const adminCall = fetchMock.mock.calls.find(([input]) => String(input).startsWith("/v1/admin/agents"));
    expect(adminCall).toBeDefined();
    expect(String(adminCall?.[0])).toContain("q=codex");
  });

  // /v1/me/agents 没有 q 参数，member 角色必须靠客户端过滤，否则搜出的是任意几个 agent。
  it("filters self-serve agent results client-side for non-admin roles", async () => {
    stubFetch((path) => {
      if (path.startsWith("/v1/me/agents")) {
        return jsonResponse({
          agents: [
            { agent_id: "research-bot", display_name: "Research Bot" },
            { agent_id: "codex-planner", display_name: "Codex Planner" },
            { agent_id: "billing-agent", display_name: "Billing Agent" },
          ],
        });
      }
      if (path.startsWith("/v1/wiki/search")) return jsonResponse({ results: [] });
      throw new Error(`unexpected fetch: ${path}`);
    });
    const { user } = renderPalette(vi.fn(), "member");

    await user.type(screen.getByRole("combobox", { name: "Search" }), "codex");
    await waitFor(() => screen.getByRole("option", { name: /Codex Planner/ }));
    expect(screen.queryByRole("option", { name: /Research Bot/ })).toBeNull();
    expect(screen.queryByRole("option", { name: /Billing Agent/ })).toBeNull();
  });

  // 搜索失败不能打断使用：静默降级成只剩导航动作，不弹错误。
  it("falls back to navigation actions when remote search fails", async () => {
    stubFetch((path) => {
      if (path.startsWith("/v1/admin/agents")) return jsonResponse({ code: "boom", message: "x" }, 500);
      if (path.startsWith("/v1/wiki/search")) return jsonResponse({ code: "boom", message: "x" }, 500);
      throw new Error(`unexpected fetch: ${path}`);
    });
    const { user } = renderPalette();

    await user.type(screen.getByRole("combobox", { name: "Search" }), "members");
    await waitFor(() => screen.getByRole("option", { name: /Members/ }));
    expect(screen.queryByText(/failed/i)).toBeNull();
  });
});
