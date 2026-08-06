import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ThroughputChart } from "../src/pages/overview/ThroughputChart";
import { makeOverview } from "./operationsFixtures";
import { setupDomTest } from "./helpers";

setupDomTest();

describe("ThroughputChart", () => {
  it("renders one normalized bar per bucket per row with per-row peaks", () => {
    const series = makeOverview().series; // 6 buckets, known values
    render(<ThroughputChart series={series} window="1h" />);
    expect(screen.getByText("Evidence in")).toBeTruthy();
    expect(screen.getByText("Facts kept")).toBeTruthy();
    expect(screen.getByText("Recalls served")).toBeTruthy();
    // 3 rows x 6 buckets
    expect(document.querySelectorAll(".ov-chart-cell")).toHaveLength(18);
    // the peak bucket of the evidence row fills its track
    const evidenceBars = document.querySelectorAll('[data-row="evidence"] .ov-chart-bar');
    const heights = Array.from(evidenceBars).map((b) => (b as HTMLElement).style.height);
    expect(heights).toContain("100%");
    // verify per-row normalization: facts row also normalizes to its own peak
    const factsBars = document.querySelectorAll('[data-row="facts"] .ov-chart-bar');
    const factsHeights = Array.from(factsBars).map((b) => (b as HTMLElement).style.height);
    expect(factsHeights).toContain("100%");
  });

  it("labels buckets by window granularity", () => {
    const series = makeOverview().series;
    const firstBucketAt = series[0].bucket_at;

    // Test 7d format: M/D
    const { unmount: unmount7d } = render(<ThroughputChart series={series} window="7d" />);
    const d7d = new Date(firstBucketAt);
    const expectedLabel7d = `${d7d.getMonth() + 1}/${d7d.getDate()}`;
    const ticksDiv7d = document.querySelector(".ov-chart-ticks");
    expect(ticksDiv7d?.textContent).toContain(expectedLabel7d);
    unmount7d();

    // Test 1h format: HH:mm
    render(<ThroughputChart series={series} window="1h" />);
    const d1h = new Date(firstBucketAt);
    const hh = String(d1h.getHours()).padStart(2, "0");
    const mm = String(d1h.getMinutes()).padStart(2, "0");
    const expectedLabel1h = `${hh}:${mm}`;
    const ticksDiv1h = document.querySelector(".ov-chart-ticks");
    expect(ticksDiv1h?.textContent).toContain(expectedLabel1h);
    expect(document.querySelectorAll(".ov-chart-tick")).toHaveLength(series.length);
  });
});
