// Cross-screen acceptance item 4 (Modernist Portal phase 7 entry cleanup,
// task 5): AGENTS.md's §Web frontend describes the styles/features and
// components directory listings inline, in prose, with no automated check
// tying them to the real tree. Phases 4/5/6 each drifted silently — a docs
// task hand-audits AGENTS.md once, and the next feature branch adds a file
// without anyone remembering to update the list again. Task 4 of this phase
// hand-audited it (ls web/src/styles/features, ls web/src/components) and
// found it already stale — entry.css didn't exist yet when that audit ran,
// so it never made the list even after Task 4 fixed everything else. This
// file turns that hand-audit into a standing guard: read the real
// directories, read AGENTS.md, diff them.
import { readdirSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { describe, expect, it } from "vitest";

const repoRoot = fileURLToPath(new URL("../../", import.meta.url));
const agentsMd = readFileSync(path.join(repoRoot, "AGENTS.md"), "utf8");

function cssFilesIn(dir: string): string[] {
  return readdirSync(path.join(repoRoot, dir))
    .filter((f) => f.endsWith(".css"))
    .sort();
}

function componentFilesIn(dir: string): string[] {
  return readdirSync(path.join(repoRoot, dir))
    .filter((f) => f.endsWith(".tsx") && !f.endsWith(".test.tsx"))
    .sort();
}

describe("AGENTS.md §Web frontend matches the real web/ tree", () => {
  it("names every web/src/styles/features/*.css file (`ls web/src/styles/features/` is the source of truth)", () => {
    const actual = cssFilesIn("web/src/styles/features");
    // Real regression this guards: entry.css landed after the last hand
    // audit and was never added to the prose list (see phase7 task 4/5
    // reports). If this ever fails again, `ls web/src/styles/features/` and
    // add the missing name(s) to the bullet in AGENTS.md's §Web frontend.
    const missing = actual.filter((f) => !agentsMd.includes(`\`${f}\``));
    expect(missing).toEqual([]);
  });

  it("names every top-level web/src/components/*.tsx file (`ls web/src/components/` is the source of truth)", () => {
    const actual = componentFilesIn("web/src/components");
    const missing = actual.filter((f) => {
      const base = f.replace(/\.tsx$/, "");
      // AGENTS.md cites components either as `Name` or `Name.tsx` depending
      // on the surrounding sentence; either spelling counts as documented.
      return !agentsMd.includes(`\`${base}\``) && !agentsMd.includes(`\`${f}\``);
    });
    expect(missing).toEqual([]);
  });

  it("mentions the components/wiki/ subdirectory and its three files", () => {
    const actual = componentFilesIn("web/src/components/wiki");
    expect(actual).toEqual(["RelationList.tsx", "TopicTree.tsx", "WikiMarkdown.tsx"]);
    for (const f of actual) {
      const base = f.replace(/\.tsx$/, "");
      expect(agentsMd.includes(`\`${base}\``)).toBe(true);
    }
  });

  // Phase 7's design brief called for deleting a "full-screen apps render
  // outside PortalShell" rule from AGENTS.md. Task 4 found — and this
  // independently re-confirms — that no such rule text ever existed in
  // AGENTS.md to begin with (the file's Web frontend section predates every
  // Modernist phase's docs task). Pin the absence so it can't silently
  // reappear out of sync with the actual deleted pattern (guarded at the
  // source-tree level by tests/app-fullscreen-escape.test.ts).
  it("has no 'fullscreen renders outside the shell' rule text", () => {
    expect(agentsMd).not.toMatch(/fullscreen|全屏/);
    expect(agentsMd).not.toMatch(/PortalShell/);
  });
});
