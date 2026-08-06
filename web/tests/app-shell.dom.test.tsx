// 外壳的 DOM 契约：顶栏项按角色渲染、subnav 跟随当前分区、用户菜单能登出。
// 可见性的穷举在 tests/navModel.test.ts，这里只验证渲染与交互。
import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { jsonResponse, makeMe, makeMember, renderApp, setupDomTest } from "./helpers";

setupDomTest();

function shellFetch(path: string, init: RequestInit): Response {
  if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
  if (path.startsWith("/v1/admin/agents")) return jsonResponse({ agents: [] });
  if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [] });
  if (path.startsWith("/v1/teams")) return jsonResponse({ teams: [] });
  throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
}

function topbar(): HTMLElement {
  return screen.getByRole("navigation", { name: "Sections" });
}

// Regression guard for the branch's worst defect: /management used to
// dispatch AdminAgentsPage for admin-likes, and the "+ Create Agent" modal
// lived only on the member fork's agent list — so an owner had no way
// anywhere in the portal to register a personal agent. Phase 3 replaces the
// Management root with a
// real access tree for admin+ (member gets MyAgentsLevel directly), and
// gives owner/admin their own create-agent entry back through the tree:
// people level → click yourself → your own machine level. "+ Create Agent"
// now exists at exactly those two places in the portal, and the cases below
// prove both are reachable.
describe("Management root", () => {
  it("gives a member the create-agent trigger at /management", async () => {
    await renderApp({
      route: "/management",
      me: makeMe({ role: "member" }),
      fetch: shellFetch,
    });

    await screen.findByRole("heading", { name: "My Agents" });
    expect(screen.getByRole("button", { name: "+ Create Agent" })).toBeTruthy();
  });

  it.each(["owner", "admin"] as const)(
    "lands a %s on the access tree at /management",
    async (role) => {
      await renderApp({
        route: "/management",
        me: makeMe({ role }),
        fetch: shellFetch,
      });

      // admin+ land on the access tree; its own-machine level is where
      // their create-agent trigger lives (see the case below).
      await screen.findByRole("heading", { name: "Access flows downward" });
    },
  );

  it.each(["owner", "admin"] as const)(
    "gives a %s the create-agent trigger on their own-machine level (people level → click yourself)",
    async (role) => {
      const { user } = await renderApp({
        route: "/management",
        me: makeMe({ role, membership_id: "mbr_01" }),
        fetch: (path, init) => {
          if (path.startsWith("/v1/admin/members")) {
            return jsonResponse({
              members: [
                makeMember({
                  membership_id: "mbr_01",
                  display_name: "Alice",
                  email: "alice@example.com",
                  role,
                }),
              ],
            });
          }
          if (path.startsWith("/v1/admin/devices")) return jsonResponse({ devices: [] });
          return shellFetch(path, init);
        },
      });

      await screen.findByRole("heading", { name: "Access flows downward" });
      await user.click(await screen.findByText("Alice"));

      await screen.findByRole("button", { name: "+ Create Agent" });
    },
  );

  it("keeps the team-wide agent list at /management/agents for an owner", async () => {
    await renderApp({
      route: "/management/agents",
      me: makeMe({ role: "owner" }),
      fetch: (path, init) => {
        if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [] });
        return shellFetch(path, init);
      },
    });

    await screen.findByRole("heading", { name: "All Agents" });
    const sub = screen.getByRole("navigation", { name: "Section pages" });
    within(sub).getByRole("link", { name: "Agents" });
  });
});

describe("AppShell top bar", () => {
  it("renders only the sections a member may see", async () => {
    await renderApp({
      route: "/management",
      me: makeMe({ role: "member" }),
      fetch: shellFetch,
    });

    const nav = topbar();
    within(nav).getByRole("link", { name: "Management" });
    within(nav).getByRole("link", { name: "Apps" });
    within(nav).getByRole("link", { name: "Settings" });
    expect(within(nav).queryByRole("link", { name: "Overview" })).toBeNull();
    expect(within(nav).queryByRole("link", { name: "Governance" })).toBeNull();
  });

  it("marks the active section and renders its sub-navigation", async () => {
    await renderApp({
      route: "/management/members",
      me: makeMe({ role: "admin" }),
      fetch: (path, init) => {
        if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [] });
        return shellFetch(path, init);
      },
    });

    expect(
      within(topbar()).getByRole("link", { name: "Management" }).getAttribute("aria-current"),
    ).toBe("page");
    const sub = screen.getByRole("navigation", { name: "Section pages" });
    within(sub).getByRole("link", { name: "Access tree" });
    within(sub).getByRole("link", { name: "Members" });
    within(sub).getByRole("link", { name: "Devices" });
  });

  it("hides the sub-navigation for a section with no sub-pages", async () => {
    // /overview renders OverviewPage (阶段 2b): it polls the overview
    // aggregate and the operations agents endpoint (writers block); both
    // need minimal successful shapes or the region renders an error instead
    // of settling, and the assertion below wouldn't test its own point.
    await renderApp({
      route: "/overview",
      me: makeMe({ capabilities: ["view.operations"] }),
      fetch: (path, init) => {
        if (path.startsWith("/v1/admin/overview")) {
          return jsonResponse({
            from_time: "2026-08-04T00:00:00Z",
            to_time: "2026-08-04T01:00:00Z",
            generated_at: "2026-08-04T01:00:00Z",
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
          return jsonResponse({
            agents: [],
            from_time: "2026-08-04T00:00:00Z",
            to_time: "2026-08-04T01:00:00Z",
            generated_at: "2026-08-04T01:00:00Z",
          });
        }
        if (path.startsWith("/v1/admin/operations/events")) {
          return jsonResponse({ events: [], generated_at: "2026-08-04T01:00:00Z" });
        }
        return shellFetch(path, init);
      },
    });

    await screen.findByRole("heading", { name: "Overview" });
    expect(screen.queryByRole("navigation", { name: "Section pages" })).toBeNull();
  });

  it("signs out from the user menu", async () => {
    const { user, fetchMock } = await renderApp({
      route: "/management",
      me: makeMe({ role: "member" }),
      fetch: (path, init) => {
        if (path === "/v1/auth/logout") return jsonResponse({});
        return shellFetch(path, init);
      },
    });

    await user.click(screen.getByRole("button", { name: /alice@example\.com/ }));
    await user.click(screen.getByRole("menuitem", { name: "Sign out" }));

    expect(
      fetchMock.mock.calls.some(([input, init]) =>
        String(input) === "/v1/auth/logout" && (init as RequestInit)?.method === "POST",
      ),
    ).toBe(true);
  });

  it("closes the user menu on Escape", async () => {
    const { user } = await renderApp({
      route: "/management",
      me: makeMe({ role: "member" }),
      fetch: shellFetch,
    });

    await user.click(screen.getByRole("button", { name: /alice@example\.com/ }));
    screen.getByRole("menu");
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("menu")).toBeNull();
  });
});
