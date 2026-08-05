import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import type { OfferingCapability } from "../api/controlClient";
import CapabilityChips from "./CapabilityChips";

afterEach(() => {
  cleanup();
});

function capability(overrides: Partial<OfferingCapability> = {}): OfferingCapability {
  return {
    operation: "chat",
    effective: true,
    state: "discovered",
    truth: "unknown",
    routable: false,
    provenance: "",
    ...overrides,
  };
}

describe("CapabilityChips", () => {
  it("renders one chip per capability up to the cap, with +N overflow beyond it", () => {
    const capabilities = [
      capability({ operation: "chat" }),
      capability({ operation: "coding" }),
      capability({ operation: "reasoning" }),
      capability({ operation: "structured_output" }),
      capability({ operation: "tools" }),
      capability({ operation: "vision" }),
      capability({ operation: "streaming" }),
      capability({ operation: "context_window" }),
    ];
    render(<CapabilityChips capabilities={capabilities} cap={6} />);

    expect(screen.getAllByRole("img")).toHaveLength(6);
    screen.getByText("+2");
  });

  it("renders nothing to overflow when capabilities are within the cap", () => {
    render(<CapabilityChips capabilities={[capability({ operation: "chat" })]} cap={6} />);
    expect(screen.getAllByRole("img")).toHaveLength(1);
    expect(screen.queryByText(/^\+\d+$/)).toBeNull();
  });

  it("gives each chip an aria-label equal to its operation", () => {
    render(<CapabilityChips capabilities={[capability({ operation: "vision" })]} cap={6} />);
    screen.getByRole("img", { name: "vision" });
  });

  it("marks a declared capability with the --declared modifier class and a 'declared' provenance line, without a probe claim", () => {
    render(
      <CapabilityChips
        capabilities={[capability({ operation: "tools", provenance: "declared", truth: "supported", state: "certified" })]}
        cap={6}
      />,
    );
    const chip = screen.getByRole("img", { name: "tools" });
    expect(chip.className).toContain("vnd-capability-icon-box--declared");
    expect(chip.getAttribute("title")).toContain("declared");
    expect(chip.getAttribute("title")).not.toContain("proven");
  });

  it("does NOT mark a probed capability with the --declared modifier class, and its tooltip claims a runtime probe", () => {
    render(
      <CapabilityChips
        capabilities={[capability({ operation: "chat", provenance: "probed", truth: "supported", state: "certified" })]}
        cap={6}
      />,
    );
    const chip = screen.getByRole("img", { name: "chat" });
    expect(chip.className).not.toContain("vnd-capability-icon-box--declared");
    expect(chip.getAttribute("title")).toContain("proven (runtime probe)");
  });

  it("keeps the plain two-line truth/state tooltip and no --declared class when provenance is empty", () => {
    render(
      <CapabilityChips
        capabilities={[capability({ operation: "chat", provenance: "", truth: "unknown", state: "discovered" })]}
        cap={6}
      />,
    );
    const chip = screen.getByRole("img", { name: "chat" });
    expect(chip.className).not.toContain("vnd-capability-icon-box--declared");
    const title = chip.getAttribute("title") ?? "";
    expect(title).not.toContain("Provenance");
    expect(title).toContain("Truth: unknown");
    expect(title).toContain("State: discovered");
  });

  it("renders a fallback message and no chips when there are no capabilities", () => {
    render(<CapabilityChips capabilities={[]} cap={6} />);
    expect(screen.queryAllByRole("img")).toHaveLength(0);
    screen.getByText(/no capability observed/i);
  });
});
