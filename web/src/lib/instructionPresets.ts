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
