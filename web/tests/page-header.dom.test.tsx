// PageHeader is the convergence of four near-identical header blocks
// (.page-head / .gv-head / .wiki-head / .ag-head) written across the seven
// portal phases. A shared component with no test of its own is how the next
// phase quietly grows a fifth copy, so its contract is pinned here rather
// than left to whichever page test happens to touch a heading.

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { PageHeader } from "../src/components/PageHeader";
import { setupDomTest } from "./helpers";

setupDomTest();

const head = () => document.querySelector(".page-head") as HTMLElement;

describe("PageHeader", () => {
  it("renders kicker, title, lede and actions in one header", () => {
    render(
      <PageHeader
        kicker="Governance · 追一条事实"
        title="这是从哪来的？"
        lede="一句说明"
        actions={<button type="button">Act</button>}
      />,
    );

    expect(head().querySelector(".kicker")?.textContent).toBe("Governance · 追一条事实");
    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("这是从哪来的？");
    screen.getByRole("button", { name: "Act" });
  });

  // The text column must stay one element with the actions as its sibling:
  // that is what makes `justify-content: space-between` put the actions on the
  // right. Nesting the actions inside the text column still renders every
  // string, so only a structural assertion catches it.
  it("keeps actions a sibling of the text column, not a child", () => {
    render(<PageHeader kicker="K" title="T" actions={<button type="button">Act</button>} />);

    expect(head().children).toHaveLength(2);
    const [text, actions] = Array.from(head().children);
    expect(text.querySelector("h1")).toBeTruthy();
    expect(text.querySelector("button")).toBeNull();
    expect(actions.textContent).toBe("Act");
  });

  it("omits the kicker entirely when none is given", () => {
    render(<PageHeader title="My Agents" />);

    expect(head().querySelector(".kicker")).toBeNull();
    // An empty <span class="kicker"> would still reserve its line-height.
    expect(head().querySelector("div")?.firstElementChild?.tagName).toBe("H1");
  });

  it("wraps a string lede in the default treatment and renders a node as-is", () => {
    const { unmount } = render(<PageHeader title="T" lede="plain" />);
    const wrapped = head().querySelector("p");
    expect(wrapped?.className).toBe("muted flush");
    expect(wrapped?.textContent).toBe("plain");
    unmount();

    // Governance's dimmed lede and wiki's narrow one pass their own <p>; the
    // component must not second-guess the class list (colour treatment of
    // low-contrast text is a separate, still-open design-system question).
    render(<PageHeader title="T" lede={<p className="lede-dim">dim</p>} />);
    expect(head().querySelector("p")?.className).toBe("lede-dim");
  });

  it("applies the bleed and align-start modifiers only when asked", () => {
    const { unmount } = render(<PageHeader title="T" />);
    expect(head().className).toBe("page-head");
    unmount();

    render(<PageHeader title="T" variant="bleed" alignStart />);
    expect(head().className.split(" ").sort()).toEqual(["align-start", "bleed", "page-head"]);
  });
});

// Two cascade properties the convergence depends on. jsdom does not apply the
// stylesheet, so no DOM test can observe either one -- they would break
// silently in a browser with every test still green. Asserted against the
// source instead, which is the only place they are observable at all.
describe("page-head cascade", () => {
  const layout = readFileSync("src/styles/layout.css", "utf8");

  it("narrows every header variant at the 640 breakpoint, not just the base", () => {
    // A media query does not raise specificity: `.page-head` (0,1,0) inside
    // @media loses to `.page-head.bleed` (0,2,0) outside it, so a bleed header
    // would stack on a phone but keep the wide gap.
    const mobile = layout.match(/@media \(max-width: 640px\) \{([\s\S]*?)\n\}/)?.[1] ?? "";
    expect(mobile).toContain(".page-head.bleed");
    expect(mobile).toContain(".page-head.align-start");
  });

  it("qualifies the lede rules by element so .flush cannot override them", () => {
    // Both give the lede a full margin; `.flush { margin: 0 }` is declared
    // later in this same file at equal specificity, so an unqualified
    // `.lede-narrow` loses to it the moment a caller writes both.
    expect(layout).toMatch(/^p\.lede-dim\s*\{/m);
    expect(layout).toMatch(/^p\.lede-narrow\s*\{/m);
    expect(layout).toMatch(/\[data-theme="arcade"\] p\.lede-dim/);
    // And .flush really is the later, equal-specificity rule this guards against.
    expect(layout.indexOf(".flush {")).toBeGreaterThan(layout.indexOf("p.lede-narrow"));
  });

  it("collapses every split ratio at its breakpoint, for the same reason", () => {
    const wide = layout.match(/@media \(max-width: 900px\) \{([\s\S]*?)\n\}/)?.[1] ?? "";
    expect(wide).toContain(".split.wide-left");
    expect(wide).toContain(".split.wide-right");
  });
});

// The point of the convergence is that the four old classes are gone. Nothing
// at runtime notices if one comes back -- a page would simply render
// unstyled-but-present markup and every DOM test would stay green -- so this
// guards the source directly, the same way agents-md-consistency guards the
// component list.
describe("the superseded header and column classes are gone", () => {
  const SUPERSEDED = ["gv-head", "wiki-head", "ag-head", "todo-columns", "ag-columns", "gv-pipeline-columns"];

  function sourceFiles(dir: string): string[] {
    return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
      const full = join(dir, entry.name);
      if (entry.isDirectory()) return sourceFiles(full);
      return /\.(tsx?|css)$/.test(entry.name) ? [full] : [];
    });
  }

  /** Comments legitimately name the old classes to explain the history. */
  const stripComments = (text: string) =>
    text.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");

  it("no source file uses them in a class attribute or selector", () => {
    const offenders: string[] = [];
    for (const file of sourceFiles("src")) {
      const text = stripComments(readFileSync(file, "utf8"));
      for (const name of SUPERSEDED) {
        // `(?!-)` keeps live descendants like `.ag-head-facts` out: those are
        // different classes that merely share a prefix, and matching them
        // would make this guard unfixable without renaming working code.
        const selector = new RegExp(`\\.${name}(?!-)[\\s,{:]`);
        const attribute = new RegExp(`className="[^"]*\\b${name}(?!-)\\b`);
        if (selector.test(text) || attribute.test(text)) offenders.push(`${file}: ${name}`);
      }
    }
    expect(offenders).toEqual([]);
  });

  // The guard is only worth having if it actually fires, and a regex that
  // silently matches nothing looks identical to a clean tree.
  it("would catch a reintroduced class", () => {
    const selector = new RegExp(`\\.gv-head(?!-)[\\s,{:]`);
    expect(selector.test(".gv-head { display: flex; }")).toBe(true);
    expect(selector.test(".gv-head-facts { gap: 0; }")).toBe(false);
    expect(stripComments("/* .gv-head was here */").trim()).toBe("");
  });
});
