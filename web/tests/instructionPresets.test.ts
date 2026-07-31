import { describe, expect, it } from "vitest";
import {
  composeInstructions,
  decomposeInstructions,
  codePointLength,
  INSTRUCTION_PRESETS,
} from "../src/lib/instructionPresets";

const tables = INSTRUCTION_PRESETS[0];
const bullets = INSTRUCTION_PRESETS.find((p) => p.id === "bullets")!;

describe("instruction presets", () => {
  it("composes selected sentences in array order plus additional text", () => {
    expect(composeInstructions([bullets.id, tables.id], "  extra guidance  ")).toBe(
      `${tables.sentence}\n${bullets.sentence}\n\nextra guidance`,
    );
    expect(composeInstructions([], "")).toBe("");
    expect(composeInstructions([tables.id], "")).toBe(tables.sentence);
    expect(composeInstructions([], "solo text")).toBe("solo text");
  });

  it("round-trips: decompose recovers chips and additional text", () => {
    const stored = composeInstructions([tables.id, bullets.id], "extra guidance");
    expect(decomposeInstructions(stored)).toEqual({
      selectedIds: [tables.id, bullets.id],
      additional: "extra guidance",
    });
    expect(decomposeInstructions("")).toEqual({ selectedIds: [], additional: "" });
  });

  it("leaves hand-edited sentences in the additional text", () => {
    const edited = tables.sentence.replace("tables", "TABLES");
    const result = decomposeInstructions(`${edited}\n\n${bullets.sentence}`);
    expect(result.selectedIds).toEqual([bullets.id]);
    expect(result.additional).toBe(edited);
  });

  it("counts code points, not UTF-16 units", () => {
    expect(codePointLength("语言ab")).toBe(4);
    expect(codePointLength("𝄞")).toBe(1); // astral char is 2 UTF-16 units
  });

  it("ignores preset sentences embedded in other text (not whole-line match)", () => {
    const embedded = "Also, " + tables.sentence + " when listing steps.";
    const result = decomposeInstructions(embedded);
    expect(result.selectedIds).toEqual([]);
    expect(result.additional).toBe(embedded);
  });
});
