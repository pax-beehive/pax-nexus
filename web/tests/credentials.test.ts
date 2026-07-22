import { describe, expect, it } from "vitest";
import { deriveCredentialStatus } from "../src/lib/credentials";

const NOW = new Date("2026-07-21T18:00:00Z");

describe("deriveCredentialStatus", () => {
  it("is revoked when revoked_at is set, even if also expired", () => {
    expect(
      deriveCredentialStatus(
        { revoked_at: "2026-07-20T00:00:00Z", expires_at: "2026-07-01T00:00:00Z" },
        NOW,
      ),
    ).toBe("revoked");
  });

  it("is expired when expires_at is in the past", () => {
    expect(deriveCredentialStatus({ expires_at: "2026-07-21T17:59:59Z" }, NOW)).toBe("expired");
  });

  it("is active when expires_at is in the future", () => {
    expect(deriveCredentialStatus({ expires_at: "2026-10-21T18:00:00Z" }, NOW)).toBe("active");
  });

  it("is active when neither field is set", () => {
    expect(deriveCredentialStatus({}, NOW)).toBe("active");
  });
});
