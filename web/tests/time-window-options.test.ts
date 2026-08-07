// The window selector's option set, derived from the deployment's event
// retention (issue #86). Before this, both pages offered a fixed ["1h","24h",
// "7d"] unconditionally, so any deployment configured below 7 days showed a
// 7d button that always failed with a generic 400.

import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { formatRetention, timeWindowOptions } from "../src/lib/operations";

const HOUR = 60 * 60;
const DAY = 24 * HOUR;

describe("timeWindowOptions", () => {
  it("offers every window when retention is unknown", () => {
    // Undefined = no response yet, or a backend that predates the field.
    // Guessing a ceiling here would hide windows that do work.
    expect(timeWindowOptions(undefined).map((o) => o.disabled)).toEqual([false, false, false]);
    expect(timeWindowOptions(undefined).every((o) => o.title === undefined)).toBe(true);
  });

  it("disables windows longer than retention and names the ceiling", () => {
    const options = timeWindowOptions(DAY);

    expect(options.map((o) => [o.value, o.disabled])).toEqual([
      ["1h", false],
      ["24h", false],
      ["7d", true],
    ]);
    expect(options[2].title).toBe("This deployment keeps 24h of events");
    // An enabled option carries no explanation -- there is nothing to explain.
    expect(options[1].title).toBeUndefined();
  });

  // The backend rejects windows strictly greater than retention, so a window
  // of exactly retention must stay offered. Pinned in both directions because
  // an off-by-one here silently removes a working window.
  it("keeps a window equal to retention, drops the one past it", () => {
    expect(timeWindowOptions(DAY).find((o) => o.value === "24h")?.disabled).toBe(false);
    expect(timeWindowOptions(DAY - 1).find((o) => o.value === "24h")?.disabled).toBe(true);
    expect(timeWindowOptions(7 * DAY).find((o) => o.value === "7d")?.disabled).toBe(false);
    expect(timeWindowOptions(7 * DAY - 1).find((o) => o.value === "7d")?.disabled).toBe(true);
  });

  // A malformed value must degrade to "unknown", not to "nothing works".
  // Taking 0 literally disables every window and explains it as "0s", which
  // is a worse outcome than the unactionable 400 this feature replaces.
  it("treats a non-positive or non-finite retention as unknown", () => {
    for (const bad of [0, -1, Number.NaN, Number.POSITIVE_INFINITY]) {
      const options = timeWindowOptions(bad);
      expect(options.every((o) => !o.disabled)).toBe(true);
      expect(options.every((o) => o.title === undefined)).toBe(true);
    }
  });

  it("disables everything but 1h at the configured retention floor", () => {
    // 24h is the backend's minimum, so this is the narrowest real deployment.
    expect(timeWindowOptions(DAY).filter((o) => o.disabled).map((o) => o.value)).toEqual(["7d"]);
    // And a hypothetical sub-floor value still degrades sensibly.
    expect(timeWindowOptions(HOUR).filter((o) => !o.disabled).map((o) => o.value)).toEqual(["1h"]);
  });
});

// A disabled control that looks identical to an enabled one is worse than no
// disabling at all: the reader gets no signal and no explanation, just a
// button that ignores clicks. `.seg button` carries no `.btn` class, so it
// never picked up the global `.btn:disabled` treatment. jsdom applies no
// stylesheet, so this is only observable in the source.
describe("the seg control has a visible disabled state", () => {
  const components = readFileSync("src/styles/components.css", "utf8");

  it("dims disabled options and suppresses their hover", () => {
    expect(components).toMatch(/\.seg button:disabled\s*\{[^}]*opacity/);
    expect(components).toMatch(/\.seg button:disabled\s*\{[^}]*cursor:\s*not-allowed/);
    // Hover must exclude disabled, or the control still lights up under the
    // pointer and reads as clickable.
    expect(components).toMatch(/\.seg button:not\(\.on\):not\(:disabled\):hover/);
  });
});

describe("formatRetention", () => {
  it("uses the largest whole unit that divides evenly", () => {
    expect(formatRetention(7 * DAY)).toBe("7d");
    expect(formatRetention(2 * DAY)).toBe("2d");
    expect(formatRetention(36 * HOUR)).toBe("36h");
    expect(formatRetention(90 * 60)).toBe("90m");
    expect(formatRetention(45)).toBe("45s");
  });

  // The retention floor is the case most likely to be seen, and "1d" would be
  // a word that appears nowhere else on the page -- the preset button next to
  // it says "24h" and the deployment is configured as `24h`.
  it("renders the 24h floor in the same vocabulary as the preset buttons", () => {
    expect(formatRetention(DAY)).toBe("24h");
  });
});
