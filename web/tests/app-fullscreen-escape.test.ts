// Modernist Portal phase 6 task 2 deleted the pre-redesign "full-screen
// escape" pattern (a modal-like `.app-fullscreen` overlay with an
// `.app-back` button that Todos used to punch out of the shell into). Apps
// now render inside the shell like every other screen; there is no escape
// hatch left to regress into. This is a whole-tree grep guard rather than a
// single component's DOM test on purpose: the pattern could resurface in
// any app, not just Todos, and a per-component render test would miss that.
import { existsSync, readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { describe, expect, it } from "vitest";

const srcDir = fileURLToPath(new URL("../src", import.meta.url));

function listFiles(dir: string): string[] {
  return readdirSync(dir, { recursive: true })
    .map((entry) => path.join(dir, entry.toString()))
    .filter((entry) => /\.(tsx?|css)$/.test(entry));
}

describe("full-screen escape pattern is gone", () => {
  it("has no app-fullscreen or app-back reference anywhere under src", () => {
    const offenders: string[] = [];
    for (const file of listFiles(srcDir)) {
      const content = readFileSync(file, "utf8");
      if (/app-fullscreen|app-back/.test(content)) {
        offenders.push(path.relative(srcDir, file));
      }
    }
    expect(offenders).toEqual([]);
  });

  it("apps.css itself is gone (dead launcher styles, nothing referenced it)", () => {
    expect(existsSync(path.join(srcDir, "styles/features/apps.css"))).toBe(false);
  });
});
