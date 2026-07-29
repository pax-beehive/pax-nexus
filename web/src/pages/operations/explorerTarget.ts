// Explorer diagnostic deep links: the Operations console opens a drawer for
// `?detail=extraction_run|channel_envelope&id=...` when the viewer may
// inspect Team Memory content.

export type ExplorerDrawerTarget =
  | { kind: "extraction"; id: string }
  | { kind: "channel"; id: string };

export function explorerTargetFromLocation(enabled: boolean): ExplorerDrawerTarget | null {
  if (!enabled) return null;
  const params = new URLSearchParams(window.location.search);
  const id = params.get("id")?.trim();
  if (!id) return null;
  switch (params.get("detail")) {
    case "extraction_run":
      return { kind: "extraction", id };
    case "channel_envelope":
      return { kind: "channel", id };
    default:
      return null;
  }
}
