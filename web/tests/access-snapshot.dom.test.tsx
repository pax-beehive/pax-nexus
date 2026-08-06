import { describe, expect, it } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { useAccessSnapshot } from "../src/pages/management/useAccessSnapshot";
import { jsonResponse, makeAgent, makeDevice, makeMember, setupDomTest, stubFetch } from "./helpers";

setupDomTest();

describe("useAccessSnapshot", () => {
  it("returns one consistent snapshot of all three lists", async () => {
    stubFetch((path) => {
      if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [makeMember()] });
      if (path.startsWith("/v1/admin/devices")) return jsonResponse({ devices: [makeDevice()] });
      if (path.startsWith("/v1/admin/agents")) return jsonResponse({ agents: [makeAgent()] });
      throw new Error(`unexpected fetch: ${path}`);
    });

    const { result } = renderHook(() => useAccessSnapshot());

    await waitFor(() => expect(result.current.status).toBe("ready"));
    expect(result.current.snapshot?.members).toHaveLength(1);
    expect(result.current.snapshot?.devices).toHaveLength(1);
    expect(result.current.snapshot?.agents).toHaveLength(1);
  });

  it("fails the whole snapshot when the members leg fails", async () => {
    stubFetch((path) => {
      if (path.startsWith("/v1/admin/members")) throw new Error("members down");
      if (path.startsWith("/v1/admin/devices")) return jsonResponse({ devices: [] });
      if (path.startsWith("/v1/admin/agents")) return jsonResponse({ agents: [] });
      throw new Error(`unexpected fetch: ${path}`);
    });

    const { result } = renderHook(() => useAccessSnapshot());

    await waitFor(() => expect(result.current.status).toBe("error"));
    expect(result.current.error).toBeDefined();
  });

  it("stays ready and leaves devices undefined when only that leg fails", async () => {
    stubFetch((path) => {
      if (path.startsWith("/v1/admin/members")) return jsonResponse({ members: [makeMember()] });
      if (path.startsWith("/v1/admin/devices")) throw new Error("devices down");
      if (path.startsWith("/v1/admin/agents")) return jsonResponse({ agents: [makeAgent()] });
      throw new Error(`unexpected fetch: ${path}`);
    });

    const { result } = renderHook(() => useAccessSnapshot());

    await waitFor(() => expect(result.current.status).toBe("ready"));
    // undefined，不是 []：空列表和取不到必须在界面上长得不一样。
    expect(result.current.snapshot?.devices).toBeUndefined();
    expect(result.current.snapshot?.agents).toHaveLength(1);
  });
});
