# Wiki Instruction Preset Chips Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Chip-based Generation card — single-select language chips and multi-select style-preset chips that compose into the existing `custom_instructions` free text, with lossless round-trip.

**Architecture:** Frontend-only. A pure module owns the preset list and compose/decompose logic; WikiStatusPage swaps the language dropdown for chips and adds the preset chip group. Storage, API, and backend are untouched — chips are an input method for the same two fields.

**Tech Stack:** React + vitest (jsdom DOM tests).

**Spec:** `docs/superpowers/specs/2026-07-30-wiki-instruction-presets-design.md`

## Global Constraints

- Branch `feat/wiki-instruction-presets`, stacked on `feat/wiki-generation-settings` (PR #41). Frontend files only (`web/`).
- `PUT /v1/wiki/settings` body shape unchanged: `{language, custom_instructions}`.
- Composition format (exact): selected preset sentences in `INSTRUCTION_PRESETS` array order joined with `"\n"`, then — only if the additional text (trimmed) is non-empty — `"\n\n"` + trimmed additional text; whole result trimmed. No selections and no text → `""`.
- Decompose: exact whole-sentence match per preset (chip lights up, sentence removed); leftover text (redundant blank runs collapsed to one blank line, trimmed) goes to the textarea. A hand-edited sentence must NOT match and must land in the textarea intact.
- The 2000 limit applies to the COMPOSED string, counted in Unicode code points (`Array.from(s).length` — matches the backend's rune count for all practical inputs); counter shows composed remaining; save disabled when negative.
- Language semantics identical to #41: "" = Follow source; Custom chip shows the text input; loading a non-preset language selects Custom and fills the input.
- Gates: `cd web && npx tsc --noEmit && npx vitest run` (full suite).

---

### Task 1: Preset module — compose/decompose pure functions

**Files:**
- Create: `web/src/lib/instructionPresets.ts`
- Test: `web/tests/instructionPresets.test.ts`

**Interfaces:**
- Consumes: nothing.
- Produces (Task 2 imports these exact names):

```ts
export interface InstructionPreset { id: string; label: string; sentence: string }
export const INSTRUCTION_PRESETS: InstructionPreset[];
export const INSTRUCTIONS_LIMIT = 2000;
export function composeInstructions(selectedIds: string[], additional: string): string;
export function decomposeInstructions(stored: string): { selectedIds: string[]; additional: string };
export function codePointLength(value: string): number;
```

- [ ] **Step 1: Write the failing tests**

`web/tests/instructionPresets.test.ts`:

```ts
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
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd web && npx vitest run tests/instructionPresets.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement the module**

`web/src/lib/instructionPresets.ts`:

```ts
// Style presets for wiki generation instructions. Chips are an input method:
// the stored value stays plain custom_instructions text, so hand-written or
// legacy values survive untouched (they land in the additional-text field).
export interface InstructionPreset {
  id: string;
  label: string;
  sentence: string;
}

export const INSTRUCTION_PRESETS: InstructionPreset[] = [
  {
    id: "tables",
    label: "Prefer tables",
    sentence: "Prefer tables over prose when presenting comparisons, options, or structured data.",
  },
  {
    id: "newcomer",
    label: "Newcomer-friendly",
    sentence:
      "Write for readers new to the team: briefly explain team-specific terms and acronyms on first use.",
  },
  {
    id: "bullets",
    label: "Concise bullets",
    sentence: "Prefer concise bullet points over long paragraphs.",
  },
  {
    id: "english-terms",
    label: "Keep English terms",
    sentence:
      "Keep technical terms, code identifiers, and product names in English even when writing in another language.",
  },
  {
    id: "tldr",
    label: "TL;DR first",
    sentence: "Start every page with a one-paragraph TL;DR summary.",
  },
  {
    id: "examples",
    label: "Include examples",
    sentence: "Include concrete examples or code snippets where they clarify a decision or process.",
  },
];

export const INSTRUCTIONS_LIMIT = 2000;

export function composeInstructions(selectedIds: string[], additional: string): string {
  const sentences = INSTRUCTION_PRESETS.filter((preset) => selectedIds.includes(preset.id)).map(
    (preset) => preset.sentence,
  );
  const extra = additional.trim();
  const parts = [sentences.join("\n"), extra].filter((part) => part !== "");
  return parts.join("\n\n").trim();
}

export function decomposeInstructions(stored: string): {
  selectedIds: string[];
  additional: string;
} {
  let remainder = stored;
  const selectedIds: string[] = [];
  for (const preset of INSTRUCTION_PRESETS) {
    if (remainder.includes(preset.sentence)) {
      selectedIds.push(preset.id);
      remainder = remainder.replace(preset.sentence, "");
    }
  }
  return {
    selectedIds,
    additional: remainder.replace(/\n{3,}/g, "\n\n").trim(),
  };
}

export function codePointLength(value: string): number {
  return Array.from(value).length;
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web && npx vitest run tests/instructionPresets.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/instructionPresets.ts web/tests/instructionPresets.test.ts
git commit -m "feat(web): instruction preset catalog with compose/decompose round-trip"
```

---

### Task 2: Chip-based Generation card

**Files:**
- Modify: `web/src/pages/WikiStatusPage.tsx`
- Modify: `web/src/styles.css` (chip styles, `.wiki-preset-chips`)
- Test: `web/tests/wiki-status.dom.test.tsx`

**Interfaces:**
- Consumes: everything Task 1 produces (exact names above), plus the page's existing state/helpers from #41: `language`/`customLanguage`/`instructions` state, `CUSTOM_LANGUAGE` sentinel, `LANGUAGE_PRESETS`, `applySettings`, `saveSettings`, `updateWikiSettings`.
- Produces: UI only.

- [ ] **Step 1: Update/add the failing DOM tests**

In `web/tests/wiki-status.dom.test.tsx` (follow the existing fetch-stub harness; existing generation-card tests will need updating from select-based to chip-based queries):

1. **Language chips replace the select**: default state renders buttons (or `role="radio"`) "Follow source evidence", "简体中文", "English", "Custom…" with Follow source selected; clicking "简体中文" then Save → PUT body `language === "简体中文"`.
2. **Non-preset language falls into Custom**: stub GET returning `{"language":"日本語",...}` → Custom chip selected, text input contains 日本語 (rework of the existing custom-fallback test).
3. **Preset chips compose**: click "Prefer tables" and "Concise bullets" chips, type "extra guidance" in Additional instructions, Save → PUT body `custom_instructions === composeInstructions(["tables","bullets"], "extra guidance")` (import the module in the test and assert against it — pins UI↔module consistency).
4. **Stored text lights chips**: stub GET returning `custom_instructions` = two preset sentences + `"\n\n"` + "hand-written note" → those two chips selected, textarea contains only "hand-written note".
5. **Edited sentence stays text**: stub GET with one sentence altered by one word → that chip NOT selected, altered sentence in the textarea.
6. **Over-limit disables save**: select one chip and fill the textarea so the composed length exceeds 2000 code points → Save button disabled and the counter shows a negative/zero-crossed state message.

Run: `cd web && npx vitest run tests/wiki-status.dom.test.tsx` — expected FAIL.

- [ ] **Step 2: Rework the card**

In `WikiStatusPage.tsx`:

- Replace the language `<select>` with a single-select chip row rendered from `["", ...LANGUAGE_PRESETS.slice(1), CUSTOM_LANGUAGE]`-equivalent options (labels: "Follow source evidence", "简体中文", "English", "Custom…"). Keep all existing state semantics: chips set `language`; `CUSTOM_LANGUAGE` shows the existing custom-language text input; `applySettings`'s non-preset fallback keeps working.
- Add preset chip state `const [selectedPresets, setSelectedPresets] = useState<string[]>([])` toggled by a multi-select chip row rendered from `INSTRUCTION_PRESETS` (preserve `INSTRUCTION_PRESETS` array order in state: toggle ON = re-derive as `INSTRUCTION_PRESETS.filter(...)` ids, not push order).
- `applySettings` additionally runs `decomposeInstructions(settings.custom_instructions)` → `setSelectedPresets(result.selectedIds)`, `setInstructions(result.additional)`.
- Textarea label → "Additional instructions"; remove its hard `maxLength` (composed counter governs; backend still validates).
- Composed value: `const composed = composeInstructions(selectedPresets, instructions)`; counter: `INSTRUCTIONS_LIMIT - codePointLength(composed)` characters left, shown at the card bottom; Save disabled when negative (in addition to the existing busy condition).
- `saveSettings` sends `custom_instructions: composed` (replaces `instructions.trim()`).
- Chips markup: `<button type="button" className={selected ? "chip selected" : "chip"} aria-pressed={...}>` inside `.wiki-preset-chips` containers (one row for language with a group label, one for style presets). Add scoped CSS in `styles.css` next to the `.wiki-generation` block, following that file's single-line rule convention.

- [ ] **Step 3: Run gates**

Run: `cd web && npx tsc --noEmit && npx vitest run`
Expected: PASS — full suite, including reworked pre-existing generation tests.

- [ ] **Step 4: Commit**

```bash
git add web/
git commit -m "feat(web): chip-based language and style presets on the generation card"
```

---

### Task 3: Verification + stacked PR

- [ ] **Step 1: Full gates** — `cd web && npx tsc --noEmit && npx vitest run`; `go build ./...` (sanity — no Go changes expected, `git status` must show only web/ + docs/ changes).

- [ ] **Step 2: Push and open PR**

```bash
git push -u origin feat/wiki-instruction-presets
gh pr create --base feat/wiki-generation-settings --title "feat(web): chip-based language and style presets on the generation card" --body "<summary: chips as input method over unchanged storage/API; compose/decompose round-trip; stacked on #41>"
```

PR base `feat/wiki-generation-settings` (stacked on #41); retarget after #41 merges.
