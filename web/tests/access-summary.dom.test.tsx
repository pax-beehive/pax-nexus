import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { AccessSummary } from "../src/pages/management/AccessSummary";
import { makeAgent, makeDevice, makeMember, setupDomTest } from "./helpers";

setupDomTest();

describe("AccessSummary", () => {
  it("renders three cells with human-worded breakdowns", () => {
    render(
      <AccessSummary
        snapshot={{
          members: [
            makeMember({ membership_id: "m1", role: "owner" }),
            makeMember({ membership_id: "m2", role: "member" }),
          ],
          devices: [makeDevice({ credential_id: "d1", created_by_membership_id: "m1" })],
          agents: [makeAgent({ agent_id: "a1", status: "active" })],
        }}
      />,
    );

    expect(screen.getByText("2")).toBeTruthy();
    screen.getByText("1 owner · 0 admins · 1 member");
    screen.getByText("1 connected · 0 revoked · 1 person has no machine");
    screen.getByText("1 active · 0 suspended · 0 retired");
  });

  it("shows an em dash instead of a number when a leg failed", () => {
    render(
      <AccessSummary snapshot={{ members: [makeMember({ membership_id: "m1" })] }} />,
    );

    // 两格失败 → 两个 —，而不是两个 0：0 会被读成「确实一台都没有」。
    expect(screen.getAllByText("—")).toHaveLength(2);
    expect(screen.getAllByText("Could not be loaded")).toHaveLength(2);
  });
});
