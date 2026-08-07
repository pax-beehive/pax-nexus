// RegionError absorbed two hand-rolled copies (AccessTreePage's legError and
// TodoPage's two dead "请稍后重试" lines) and grew an optional `message` to do
// it. The pages cover their own use; this pins the component's own contract,
// including the combination no page happens to use yet.

import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ApiError } from "../src/api/client";
import { RegionError } from "../src/components/RegionError";
import { setupDomTest } from "./helpers";

setupDomTest();

const note = () => document.querySelector(".note") as HTMLElement;

describe("RegionError", () => {
  it("derives message and tone from the error", () => {
    render(<RegionError error={new ApiError(500, "boom", "internal")} />);

    expect(note().textContent).toContain("Server error; try again later");
    expect(note().className).toContain("bad");
  });

  it("renders a caller-supplied message with the failure tone", () => {
    render(<RegionError message="Could not load the team’s people." />);

    expect(note().textContent).toContain("Could not load the team’s people.");
    // No error object to classify, so it must not silently fall through to
    // whatever noticeForError(undefined) happens to say -- or to the "ok" tone,
    // which would render a failure as an unstyled note.
    expect(note().className).toContain("bad");
  });

  it("lets a message override the derived text while the error still sets the tone", () => {
    render(
      <RegionError
        error={new ApiError(503, "boom", "storage_not_available")}
        message="建议暂时不可用。"
      />,
    );

    expect(note().textContent).toContain("建议暂时不可用。");
    expect(note().textContent).not.toContain("Storage statistics");
    expect(note().className).toContain("warn");
  });

  it("announces itself and offers a way out only when one is given", async () => {
    const onRetry = vi.fn();
    const { unmount } = render(<RegionError message="boom" onRetry={onRetry} />);

    // role=alert is on the component, not the call sites: having it on some
    // copies and not others is how the hand-rolled versions drifted apart.
    expect(note().getAttribute("role")).toBe("alert");
    await screen.getByRole("button", { name: "Retry" }).click();
    expect(onRetry).toHaveBeenCalledTimes(1);
    unmount();

    render(<RegionError message="boom" />);
    expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
  });
});
