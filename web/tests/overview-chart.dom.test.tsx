import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ThroughputChart } from "../src/pages/overview/ThroughputChart";
import { makeOverview } from "./operationsFixtures";
import { setupDomTest } from "./helpers";

setupDomTest();

describe("ThroughputChart", () => {
  const SPAN_24H = { fromTime: "2026-07-21T12:00:00Z", toTime: "2026-07-22T12:00:00Z" };
  const SPAN_7D = { fromTime: "2026-07-15T12:00:00Z", toTime: "2026-07-22T12:00:00Z" };

  it("renders one normalized bar per bucket per row with per-row peaks", () => {
    const series = makeOverview().series; // 6 buckets, known values
    render(<ThroughputChart series={series} {...SPAN_24H} />);
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

  // This used to assert the opposite rule -- same series, two different
  // `window` props, two different label formats -- which is the defect issue
  // #84 describes, written down as a contract. The format now follows the span
  // the points actually cover, so the same series relabels only when the span
  // it is declared to cover changes.
  it("labels buckets by the span the points cover", () => {
    const series = makeOverview().series;
    const firstBucketAt = series[0].bucket_at;
    const d = new Date(firstBucketAt);

    // A week-long span: M/D.
    const { unmount } = render(<ThroughputChart series={series} {...SPAN_7D} />);
    expect(document.querySelector(".ov-chart-ticks")?.textContent).toContain(
      `${d.getMonth() + 1}/${d.getDate()}`,
    );
    unmount();

    // The same points declared as a day: HH:mm.
    render(<ThroughputChart series={series} {...SPAN_24H} />);
    const hh = String(d.getHours()).padStart(2, "0");
    const mm = String(d.getMinutes()).padStart(2, "0");
    expect(document.querySelector(".ov-chart-ticks")?.textContent).toContain(`${hh}:${mm}`);
    expect(document.querySelectorAll(".ov-chart-tick")).toHaveLength(series.length);
  });
});
