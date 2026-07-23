// enrollmentConnectCommand: self-describing (three-segment) tokens embed the
// origin and need no --url; legacy two-segment tokens keep the explicit one.
// See docs/decisions/2026-07-22-self-describing-enrollment-token.md.

import { describe, expect, it } from "vitest";
import { enrollmentConnectCommand, isSelfDescribingEnrollmentToken } from "../src/lib/enrollment";

describe("isSelfDescribingEnrollmentToken", () => {
  it("accepts three-segment tm_enroll tokens", () => {
    expect(isSelfDescribingEnrollmentToken("tm_enroll_enr_01.secret.aHR0cHM6Ly9leGFtcGxl")).toBe(true);
  });

  it("rejects legacy two-segment tokens and other prefixes", () => {
    expect(isSelfDescribingEnrollmentToken("tm_enroll_enr_01.secret")).toBe(false);
    expect(isSelfDescribingEnrollmentToken("tm_invite_inv_01.secret.more")).toBe(false);
    expect(isSelfDescribingEnrollmentToken("")).toBe(false);
  });
});

describe("enrollmentConnectCommand", () => {
  it("omits --url for self-describing tokens", () => {
    const token = "tm_enroll_enr_01.secret.aHR0cHM6Ly9leGFtcGxl";
    expect(enrollmentConnectCommand(token, "https://portal.example")).toBe(
      `paxl channel connect onprem --enrollment-token ${token}`,
    );
  });

  it("includes the portal origin as --url for legacy tokens", () => {
    expect(enrollmentConnectCommand("tm_enroll_enr_01.secret", "https://portal.example")).toBe(
      "paxl channel connect onprem --url https://portal.example --enrollment-token tm_enroll_enr_01.secret",
    );
  });
});
