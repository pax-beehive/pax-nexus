// routeKey collapses same-route param navigation to one ErrorBoundary key
// (AppShell.tsx) — only /apps/wiki/:slug does this; every other parameterised
// route keeps remounting per record on purpose (fix round 2).

import { describe, expect, it } from "vitest";
import { routeKey } from "../src/app/routeKey";

describe("routeKey", () => {
  it("collapses /apps/wiki/:slug to one key regardless of the selected page", () => {
    expect(routeKey("/apps/wiki/alpha")).toBe("/apps/wiki/:slug");
    expect(routeKey("/apps/wiki/beta")).toBe("/apps/wiki/:slug");
  });

  it("does not collapse bare /apps/wiki into the :slug key — it is a different route", () => {
    expect(routeKey("/apps/wiki")).toBe("/apps/wiki");
    expect(routeKey("/apps/wiki")).not.toBe(routeKey("/apps/wiki/alpha"));
  });

  it("keeps remounting per record on other parameterised routes", () => {
    expect(routeKey("/management/agents/a1")).not.toBe(routeKey("/management/agents/a2"));
    expect(routeKey("/management/devices/d1")).not.toBe(routeKey("/management/devices/d2"));
    expect(routeKey("/governance/memory/n1")).not.toBe(routeKey("/governance/memory/n2"));
  });
});
