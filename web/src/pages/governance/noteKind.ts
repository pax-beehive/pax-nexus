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
  status: "状态",
  blocker: "阻塞",
  handoff: "交接",
  artifact_reference: "产出引用",
  source_span: "原文片段",
};

export const NOTE_KIND_OPTIONS: { value: NoteKindFilter; label: string }[] = [
  { value: "", label: "全部类型" },
  { value: "status", label: "状态" },
  { value: "blocker", label: "阻塞" },
  { value: "handoff", label: "交接" },
  { value: "artifact_reference", label: "产出引用" },
  { value: "source_span", label: "原文片段" },
];

export function noteKindLabel(kind: string): string {
  return NOTE_KIND_LABELS[kind] ?? kind;
}
