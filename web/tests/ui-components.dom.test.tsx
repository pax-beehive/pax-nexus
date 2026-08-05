import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { Card } from "../src/components/Card";
import { Crumbs } from "../src/components/Crumbs";
import { DataTable } from "../src/components/DataTable";
import { EmptyState } from "../src/components/EmptyState";
import { Field } from "../src/components/Field";
import { MetricTile } from "../src/components/MetricTile";
import { Seg } from "../src/components/Seg";
import { Tag } from "../src/components/Tag";

afterEach(cleanup);

describe("Card", () => {
  it("renders kicker, title, meta and children", () => {
    render(
      <Card kicker="Governance" title="Pipeline health" meta={<span>updated 2m ago</span>}>
        <p>body</p>
      </Card>,
    );
    screen.getByText("Governance");
    screen.getByText("Pipeline health");
    screen.getByText("updated 2m ago");
    screen.getByText("body");
  });

  it("omits optional slots entirely", () => {
    const { container } = render(<Card>only body</Card>);
    expect(container.querySelector(".card-kicker")).toBeNull();
    expect(container.querySelector(".card-title")).toBeNull();
  });
});

describe("Tag", () => {
  it("defaults to the neutral tone", () => {
    render(<Tag>active</Tag>);
    expect(screen.getByText("active").className).toBe("tag tag-neutral");
  });

  it("renders the attention tone", () => {
    render(<Tag tone="attention">suspended</Tag>);
    expect(screen.getByText("suspended").className).toBe("tag tag-attention");
  });
});

describe("Seg", () => {
  it("marks the selected option and reports changes", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <Seg
        label="Time window"
        options={[
          { value: "1h", label: "1h" },
          { value: "24h", label: "24h" },
        ]}
        value="1h"
        onChange={onChange}
      />,
    );
    const group = screen.getByRole("group", { name: "Time window" });
    expect(within(group).getByRole("button", { name: "1h" }).getAttribute("aria-pressed")).toBe(
      "true",
    );
    await user.click(within(group).getByRole("button", { name: "24h" }));
    expect(onChange).toHaveBeenCalledWith("24h");
  });
});

describe("Field", () => {
  it("associates the label with the control and exposes hint and error", () => {
    render(
      <Field label="Device name" htmlFor="dev-name" hint="Shown in the access tree" error="Required">
        <input id="dev-name" />
      </Field>,
    );
    expect(screen.getByLabelText("Device name")).toBeTruthy();
    screen.getByText("Shown in the access tree");
    screen.getByText("Required");
  });
});

describe("DataTable", () => {
  const columns = [
    { key: "name", header: "Name", render: (row: { name: string }) => row.name },
  ];

  it("renders headers and rows", () => {
    render(
      <DataTable
        caption="Agents"
        columns={columns}
        rows={[{ name: "codex-planner" }]}
        rowKey={(row) => row.name}
        empty="No agents"
      />,
    );
    screen.getByRole("columnheader", { name: "Name" });
    screen.getByRole("cell", { name: "codex-planner" });
  });

  it("renders the empty message instead of an empty table body", () => {
    render(
      <DataTable
        caption="Agents"
        columns={columns}
        rows={[]}
        rowKey={(row) => row.name}
        empty="No agents"
      />,
    );
    screen.getByText("No agents");
    expect(screen.queryByRole("cell")).toBeNull();
  });
});

describe("MetricTile", () => {
  it("renders label, value, unit and note", () => {
    render(<MetricTile label="Time to remember" value="1.9" unit="s" note="p95" />);
    screen.getByText("Time to remember");
    screen.getByText("1.9");
    screen.getByText("s");
    screen.getByText("p95");
  });
});

describe("Crumbs", () => {
  it("links every item except the last", () => {
    render(
      <MemoryRouter>
        <Crumbs
          items={[
            { label: "Access tree", to: "/management" },
            { label: "mac-studio-01" },
          ]}
        />
      </MemoryRouter>,
    );
    screen.getByRole("link", { name: "Access tree" });
    expect(screen.queryByRole("link", { name: "mac-studio-01" })).toBeNull();
    expect(screen.getByText("mac-studio-01").getAttribute("aria-current")).toBe("page");
  });
});

describe("EmptyState", () => {
  it("renders title, body and action", () => {
    render(
      <EmptyState mark="W" title="No pages yet" body="Pages appear after ingestion." action={<button>Refresh</button>} />,
    );
    screen.getByRole("heading", { name: "No pages yet" });
    screen.getByText("Pages appear after ingestion.");
    screen.getByRole("button", { name: "Refresh" });
  });
});
