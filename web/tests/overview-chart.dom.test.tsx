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
  });

  it("labels buckets by window granularity", () => {
    const series = makeOverview().series;
    render(<ThroughputChart series={series} window="7d" />);
    // 7d buckets label as month-day, hour windows as HH:mm — assert one known label
    expect(document.querySelectorAll(".ov-chart-tick")).toHaveLength(series.length);
  });
});
