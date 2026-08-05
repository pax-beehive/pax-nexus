// 旧书签必须继续可用。重定向表是数据，这里逐条遍历断言 —— 漏一条就红。
import { describe, expect, it } from "vitest";
import { LEGACY_ROUTES, resolveLegacy } from "../src/app/legacyRoutes";

describe("legacy route table", () => {
  it("covers every pre-redesign route", () => {
    const froms = LEGACY_ROUTES.map((r) => r.from).sort();
    expect(froms).toEqual(
      [
        "/agents",
        "/agents/:agentId",
        "/admin/agents",
        "/admin/agents/:agentId",
        "/admin/audit",
        "/admin/devices",
        "/admin/devices/:credentialId",
        "/admin/explorer",
        "/admin/explorer/notes/:noteId",
        "/admin/invitations",
        "/admin/members",
        "/admin/operations",
        "/admin/pulse",
        "/admin/session-audit",
        "/apps",
        "/team",
        "/todo",
        "/wiki",
        "/wiki/browse",
      ].sort(),
    );
  });

  it("never redirects to another legacy path", () => {
    const froms = new Set(LEGACY_ROUTES.map((r) => r.from));
    for (const route of LEGACY_ROUTES) {
      expect(froms.has(route.to)).toBe(false);
    }
  });

  it.each([
    ["/agents", "", "/management"],
    ["/admin/members", "", "/management/members"],
    ["/admin/pulse", "", "/overview"],
    ["/admin/session-audit", "", "/governance/sessions"],
    ["/admin/operations", "", "/governance/pipeline"],
    ["/admin/explorer", "", "/governance/memory"],
    ["/apps", "", "/apps/wiki"],
    ["/todo", "", "/apps/todos"],
    ["/wiki", "", "/settings/memory"],
  ])("resolves %s%s to %s", (pathname, search, expected) => {
    expect(resolveLegacy(pathname, search)).toBe(expected);
  });

  it("carries path parameters through", () => {
    expect(resolveLegacy("/agents/agent-1", "")).toBe("/management/agents/agent-1");
    expect(resolveLegacy("/admin/devices/dev_01", "")).toBe("/management/devices/dev_01");
    expect(resolveLegacy("/admin/explorer/notes/note_01", "")).toBe(
      "/governance/memory/note_01",
    );
  });

  // 旧 wiki 深链把 slug 放在 query 里，新路由把它放进 path；revision 仍留在 query。
  it("moves the wiki page slug from query into the path", () => {
    expect(resolveLegacy("/wiki/browse", "?page=lake-retention")).toBe(
      "/apps/wiki/lake-retention",
    );
    expect(resolveLegacy("/wiki/browse", "?page=lake-retention&revision=r2")).toBe(
      "/apps/wiki/lake-retention?revision=r2",
    );
    expect(resolveLegacy("/wiki/browse", "")).toBe("/apps/wiki");
  });

  // /wiki?page=… 曾是更早的 wiki 深链形态，也要落到阅读器而不是设置页。
  it("routes the oldest wiki deep link to the reader, not to settings", () => {
    expect(resolveLegacy("/wiki", "?page=lake-retention")).toBe("/apps/wiki/lake-retention");
    expect(resolveLegacy("/wiki", "")).toBe("/settings/memory");
  });

  it("returns undefined for a path that was never legacy", () => {
    expect(resolveLegacy("/management", "")).toBeUndefined();
    expect(resolveLegacy("/join", "")).toBeUndefined();
  });
});
