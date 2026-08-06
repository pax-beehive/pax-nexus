import { describe, expect, it } from "vitest";
import { getOverview } from "../src/api/queries";
import { jsonResponse, stubFetch } from "./helpers";
import { makeOverview } from "./operationsFixtures";

describe("getOverview", () => {
  it("hits /v1/admin/overview with the window param and parses the body", async () => {
    const fetchMock = stubFetch((url) => {
      expect(url).toContain("/v1/admin/overview?window=7d");
      return jsonResponse(makeOverview());
    });
    const out = await getOverview("7d");
    expect(out.metrics.attention_count).toBe(makeOverview().metrics.attention_count);
    expect(fetchMock).toHaveBeenCalledOnce();
  });
});
