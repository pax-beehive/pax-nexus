// Team Note 类型的中文标签 + 筛选枚举。取值来自后端 NoteKind 的 5 个常量
// （internal/teamnote/ledger.go:29-35）：status / blocker / handoff /
// artifact_reference / source_span——是有界枚举，不是自由文本，所以左栏用
// <select> 而不是自由输入框。未知取值（理论上不会出现）原样透出英文兜底，
// 不吞、不报错。
export type NoteKindFilter =
  | ""
  | "status"
  | "blocker"
  | "handoff"
  | "artifact_reference"
  | "source_span";

const NOTE_KIND_LABELS: Record<string, string> = {
  status: "Status",
  blocker: "Blocker",
  handoff: "Handoff",
  artifact_reference: "Artifact reference",
  source_span: "Source span",
};

export const NOTE_KIND_OPTIONS: { value: NoteKindFilter; label: string }[] = [
  { value: "", label: "All kinds" },
  { value: "status", label: "Status" },
  { value: "blocker", label: "Blocker" },
  { value: "handoff", label: "Handoff" },
  { value: "artifact_reference", label: "Artifact reference" },
  { value: "source_span", label: "Source span" },
];

export function noteKindLabel(kind: string): string {
  return NOTE_KIND_LABELS[kind] ?? kind;
}
