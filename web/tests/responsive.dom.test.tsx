// jsdom 不做布局，所以断言的是「窄屏下渲染哪套 DOM」，而不是像素。
// matchMedia 在 jsdom 里不存在，这里按测试需要打桩。
//
// 文件后半部分是**样式表层**的回归钉子：真正的溢出只有浏览器量得到，但导致
// 溢出的那几条规则（写死的 sticky 偏移、只匹配直接子元素的表格选择器、压不动
// 的固定 min-width）是可以在这里钉住的。像素仍需人工过一遍浏览器。
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { jsonResponse, makeMe, makeMember, renderApp, setupDomTest } from "./helpers";

setupDomTest();

function stubMatchMedia(matches: boolean): void {
  vi.stubGlobal(
    "matchMedia",
    (query: string) =>
      ({
        matches,
        media: query,
        onchange: null,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn(),
      }) as unknown as MediaQueryList,
  );
}

function shellFetch(path: string, init: RequestInit): Response {
  if (path.startsWith("/v1/me/agents")) return jsonResponse({ agents: [] });
  // role: "admin" lands on /management's access tree (AdminAccessTree),
  // whose snapshot pulls all three admin list legs at once. members is the
  // spine — without it the page is the tree's full error card. devices and
  // agents are not: a missing leg still renders the root level, but with
  // "Could not be loaded" in its summary tiles. Stub all three so these
  // tests exercise a real, fully loaded page; they assert only on the top
  // bar, so a degraded page underneath would go unnoticed.
  if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [] });
  if (path.startsWith("/v1/admin/devices")) return jsonResponse({ devices: [] });
  if (path.startsWith("/v1/admin/agents")) return jsonResponse({ agents: [] });
  if (path.startsWith("/v1/teams")) return jsonResponse({ teams: [] });
  throw new Error(`unexpected fetch: ${init.method ?? "GET"} ${path}`);
}

describe("responsive top bar", () => {
  it("renders the inline section nav on wide viewports", async () => {
    stubMatchMedia(false);
    await renderApp({ route: "/management", me: makeMe({ role: "admin" }), fetch: shellFetch });

    screen.getByRole("navigation", { name: "Sections" });
    expect(screen.queryByRole("button", { name: "Menu" })).toBeNull();
  });

  it("collapses the section nav into a menu on narrow viewports", async () => {
    stubMatchMedia(true);
    const { user } = await renderApp({
      route: "/management",
      me: makeMe({ role: "admin" }),
      fetch: shellFetch,
    });

    expect(screen.queryByRole("navigation", { name: "Sections" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Menu" }));
    const menu = screen.getByRole("menu", { name: "Sections" });
    expect(menu).toBeTruthy();
  });
});

// jsdom 环境下 import.meta.url 是 http:，读文件要用 cwd（vitest 的 cwd = web/）。
const layout = readFileSync(resolve(process.cwd(), "src/styles/layout.css"), "utf8");

/** The selector list of the narrow-viewport "tables scroll themselves" rule. */
function narrowTableSelectors(): string {
  const rule = layout.match(/\n([^{}\n]*)\{[^{}]*display: block; overflow-x: auto;/);
  if (!rule) throw new Error("the narrow-viewport table rule is gone from layout.css");
  return rule[1].trim();
}

describe("sticky sub-navigation offset (RESP-3)", () => {
  // The sub-nav used to be pinned at a hardcoded 57px (53px narrow) while the
  // top bar measured 62px, so its top 5-9px hid behind the bar once scrolled.
  // A number that happens to match today is not a fix: both must read the
  // same custom property, and the bar's height must be declared, not implied.
  it("pins the sub-nav to the declared top-bar height", () => {
    expect(layout).toMatch(/--topbar-h:\s*\d+px;/);
    expect(layout).toMatch(/\.topbar\s*\{[^}]*height: var\(--topbar-h\);/);
    expect(layout).toMatch(/\.subnav\s*\{[^}]*top: var\(--topbar-h\);/);
  });

  it("has no hardcoded sub-nav offset left at any breakpoint", () => {
    const offsets = [...layout.matchAll(/\.subnav\s*\{[^}]*?top:\s*([^;]+);/g)].map((m) =>
      m[1].trim(),
    );
    expect(offsets.length).toBeGreaterThan(0);
    expect(offsets.every((value) => value === "var(--topbar-h)")).toBe(true);
  });
});

describe("wide tables scroll inside their card (RESP-2)", () => {
  it("matches a table that is not a direct child of .card", () => {
    // A child combinator silently stops matching the moment a page wraps its
    // table in a container, and the failure mode — the page body scrolling
    // horizontally — is exactly what jsdom cannot see.
    // Detached on purpose: a failing assertion must not leave a stray
    // <table> in the document for the next case to trip over.
    const card = document.createElement("div");
    card.className = "card";
    card.innerHTML = "<div><table></table></div>";

    const table = card.querySelector("table") as HTMLTableElement;
    expect(table.matches(narrowTableSelectors())).toBe(true);
  });

  it("matches the real Members table", async () => {
    await renderApp({
      route: "/management/members",
      me: makeMe(),
      fetch: (path, init) => {
        if (path.startsWith("/v1/admin/members")) {
          return jsonResponse({ members: [makeMember()] });
        }
        return shellFetch(path, init);
      },
    });

    await screen.findByRole("heading", { name: "Members" });
    const table = screen.getByRole("table");
    expect(table.matches(narrowTableSelectors())).toBe(true);
  });
});

describe("top bar fits below the design baseline (RESP-1)", () => {
  // The bar had an intrinsic width of 1458px, so every viewport from 901 to
  // 1457 scrolled the page body — against the plan's own "1280 must not
  // overflow" criterion. The fixed minimums were the bulk of it.
  it("gives the brand and the search affordance no fixed minimum width", () => {
    const brand = layout.match(/\.topbar-brand\s*\{([^}]*)\}/)?.[1] ?? "";
    const search = layout.match(/\.topbar-search\s*\{([^}]*)\}/)?.[1] ?? "";
    expect(brand).not.toMatch(/min-width:\s*[1-9]/);
    expect(search).not.toMatch(/min-width:\s*[1-9]/);
  });

  it("lets the section nav compress and never pushes overflow onto the page", () => {
    const nav = layout.match(/\.topbar-nav\s*\{([^}]*)\}/)?.[1] ?? "";
    expect(nav).toMatch(/min-width: 0/);
    expect(nav).toMatch(/overflow-x: auto/);
    // Fixed 20px padding on five items alone cost ~200px of the overflow.
    expect(layout).toMatch(/\.topbar-nav a\s*\{[^}]*padding: 0 clamp\(/);
  });
});
