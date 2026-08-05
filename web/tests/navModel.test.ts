// 导航可见性是纯函数，单独测掉它，DOM 测试就只需要验证渲染而不必穷举角色。
import { describe, expect, it } from "vitest";
import { landingPath, navSections, sectionForPath } from "../src/app/navModel";
import { makeMe, makeSaasMe } from "./helpers";

const sectionIds = (me = makeMe()) => navSections(me).map((s) => s.id);
const itemsOf = (id: string, me = makeMe()) =>
  navSections(me).find((s) => s.id === id)?.items.map((i) => i.to) ?? [];

describe("navSections", () => {
  it("shows member only management / apps / settings", () => {
    const me = makeMe({ role: "member", capabilities: [] });
    expect(sectionIds(me)).toEqual(["management", "apps", "settings"]);
  });

  it("hides overview without the server-issued view.operations capability", () => {
    expect(sectionIds(makeMe({ role: "owner", capabilities: [] }))).not.toContain("overview");
  });

  it("shows overview once view.operations is granted", () => {
    expect(sectionIds(makeMe({ capabilities: ["view.operations"] }))).toContain("overview");
  });

  it("gives member a management section with no admin sub-items", () => {
    expect(itemsOf("management", makeMe({ role: "member" }))).toEqual(["/management"]);
  });

  it("gives admin the full management sub-navigation", () => {
    expect(itemsOf("management", makeMe({ role: "admin" }))).toEqual([
      "/management",
      "/management/members",
      "/management/devices",
      "/management/agents",
      "/management/invitations",
    ]);
  });

  it("builds governance from audit role plus server capabilities", () => {
    const me = makeMe({ role: "admin", capabilities: ["view.operations", "view.team-memory"] });
    expect(itemsOf("governance", me)).toEqual([
      "/governance/audit",
      "/governance/sessions",
      "/governance/pipeline",
      "/governance/memory",
    ]);
  });

  it("drops the governance section entirely when nothing inside is visible", () => {
    expect(sectionIds(makeMe({ role: "member" }))).not.toContain("governance");
  });

  it("shows Team settings only in the saas profile", () => {
    expect(itemsOf("settings", makeMe({ role: "owner" }))).toEqual([
      "/settings/memory",
      "/settings/usage",
      "/settings/appearance",
    ]);
    expect(itemsOf("settings", makeSaasMe({ role: "owner" }))[0]).toBe("/settings/team");
  });
});

describe("landingPath", () => {
  it("lands admins on overview when they can see it", () => {
    expect(landingPath(makeMe({ capabilities: ["view.operations"] }))).toBe("/overview");
  });

  it("lands everyone else on management", () => {
    expect(landingPath(makeMe({ role: "member" }))).toBe("/management");
  });
});

describe("sectionForPath", () => {
  it("matches the section owning the deepest path", () => {
    const sections = navSections(makeMe({ role: "admin" }));
    expect(sectionForPath(sections, "/management/devices/dev_01")?.id).toBe("management");
    expect(sectionForPath(sections, "/governance/audit")?.id).toBe("governance");
  });

  it("returns undefined for a path outside every section", () => {
    expect(sectionForPath(navSections(makeMe()), "/join")).toBeUndefined();
  });

  it("rejects near-miss paths that share a prefix but are not real descendants", () => {
    const sections = navSections(makeMe({ role: "admin" }));
    // /management-archive and /managementXYZ share the /management prefix but
    // are not real descendants. The slash boundary guard must stop them.
    expect(sectionForPath(sections, "/management-archive")).toBeUndefined();
    expect(sectionForPath(sections, "/managementXYZ")).toBeUndefined();
    expect(sectionForPath(sections, "/settings_backup")).toBeUndefined();
  });

  it("uses longest-prefix-match, not first-match-in-order", () => {
    // Hand-constructed sections with genuinely nested prefixes. navSections() only
    // emits disjoint top-level sections, so we must build this manually to test
    // the longest-prefix tie-break when first-match would give the wrong answer.
    const sections: NavSection[] = [
      {
        id: "outer",
        label: "Outer",
        to: "/management",
        items: [{ to: "/management", label: "Root" }],
      },
      {
        id: "inner",
        label: "Inner",
        to: "/management/devices",
        items: [{ to: "/management/devices", label: "Devices" }],
      },
    ];
    // Under first-match-in-order, /management/devices/dev_01 would match the
    // "outer" section (prefix /management matches). Under longest-prefix,
    // /management/devices matches longer, so "inner" wins.
    expect(sectionForPath(sections, "/management/devices/dev_01")?.id).toBe("inner");
  });
});
