import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { Badge, RoleBadge } from "../src/components/Badge";
import { Button } from "../src/components/Button";

afterEach(cleanup);

describe("Button classes", () => {
  it("emits the Modernist btn class system", () => {
    render(<Button>Default</Button>);
    expect(screen.getByRole("button").className).toBe("btn btn-secondary");
  });

  it("maps variant and size to modifier classes", () => {
    render(
      <Button variant="primary" size="sm">
        Go
      </Button>,
    );
    expect(screen.getByRole("button").className).toBe("btn btn-primary btn-sm");
  });

  // 两色制：危险动作与主行动共用 accent，但 danger 仍是独立 variant，
  // 保证代码里的意图可读，也留出将来单独调整的接缝。
  it("renders danger with the primary appearance", () => {
    render(<Button variant="danger">Revoke</Button>);
    expect(screen.getByRole("button").className).toBe("btn btn-primary btn-danger");
  });

  it("appends caller className last", () => {
    render(<Button className="btn-block">Wide</Button>);
    expect(screen.getByRole("button").className).toBe("btn btn-secondary btn-block");
  });
});

describe("Badge classes", () => {
  // 需要人处理的状态用 attention（朱红），其余一律 neutral。
  it.each(["suspended", "pending"])("marks %s as attention", (status) => {
    render(<Badge status={status} />);
    expect(screen.getByText(status).className).toBe("tag tag-attention");
  });

  it.each(["active", "retired", "revoked", "expired", "accepted", "consumed", "removed"])(
    "marks %s as neutral",
    (status) => {
      render(<Badge status={status} />);
      expect(screen.getByText(status).className).toBe("tag tag-neutral");
    },
  );

  it("outlines elevated roles and keeps member neutral", () => {
    render(
      <>
        <RoleBadge role="owner" />
        <RoleBadge role="member" />
      </>,
    );
    expect(screen.getByText("owner").className).toBe("tag tag-outline");
    expect(screen.getByText("member").className).toBe("tag tag-neutral");
  });
});
