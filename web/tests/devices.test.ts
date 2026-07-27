// Unit tests for the device cascade-preview helper (doc sections 5.10, 8.6):
// the preview keeps only live credential rows, deduplicated per agent.

import { describe, expect, it } from "vitest";
import { aliveProvisionedAgents } from "../src/lib/devices";
import { makeDeviceAgent } from "./helpers";

describe("aliveProvisionedAgents", () => {
  it("returns an empty list for empty input", () => {
    expect(aliveProvisionedAgents([])).toEqual([]);
  });

  it("drops revoked credential-history rows", () => {
    const alive = makeDeviceAgent();
    const revoked = makeDeviceAgent({
      agent_id: "old-agent",
      credential_id: "cred_00",
      revoked_at: "2026-07-24T18:07:00Z",
    });
    expect(aliveProvisionedAgents([alive, revoked])).toEqual([alive]);
  });

  it("deduplicates an agent_id to its newest live row", () => {
    const older = makeDeviceAgent({ credential_id: "cred_02", created_at: "2026-07-24T18:05:00Z" });
    const newer = makeDeviceAgent({ credential_id: "cred_09", created_at: "2026-07-25T09:00:00Z" });
    expect(aliveProvisionedAgents([newer, older])).toEqual([newer]);
    expect(aliveProvisionedAgents([older, newer])).toEqual([newer]);
  });

  it("keeps distinct agents", () => {
    const a = makeDeviceAgent();
    const b = makeDeviceAgent({ agent_id: "personal-claude", credential_id: "cred_03" });
    expect(aliveProvisionedAgents([a, b])).toHaveLength(2);
  });
});
