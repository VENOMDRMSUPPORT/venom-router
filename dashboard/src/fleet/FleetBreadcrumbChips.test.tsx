import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import FleetBreadcrumbChips from "./FleetBreadcrumbChips";

afterEach(cleanup);

describe("FleetBreadcrumbChips", () => {
  it("uses the same design-system border role for selected and idle view chips", () => {
    render(
      <FleetBreadcrumbChips
        activeCount={2}
        totalCount={12}
        view="active"
        onViewChange={vi.fn()}
      />,
    );

    const active = screen.getByRole("button", { name: /active providers/i });
    const all = screen.getByRole("button", { name: /all integrations/i });
    expect(active.className).toContain("vn-provider-view-chip");
    expect(all.className).toContain("vn-provider-view-chip");
    expect(active.className).not.toContain("border-border-strong");
    expect(all.className).not.toContain("border-border-strong");
  });
});
