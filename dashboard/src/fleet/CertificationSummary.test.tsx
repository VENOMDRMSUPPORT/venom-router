import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { assertNoAxeViolations } from "../test/axe";
import type { CertificationRead } from "../api/controlClient";
import * as controlClient from "../api/controlClient";
import CertificationSummary, { type CertificationSummaryOperation } from "./CertificationSummary";

vi.mock("../api/controlClient", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/controlClient")>();
  return {
    ...actual,
    getCertification: vi.fn(),
  };
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

function op(overrides: Partial<CertificationSummaryOperation> = {}): CertificationSummaryOperation {
  return {
    offeringOperationId: "op-1",
    operation: "tools",
    state: "certified",
    truth: "supported",
    ...overrides,
  };
}

function certRead(overrides: Partial<CertificationRead> = {}): CertificationRead {
  return {
    offering_operation_id: "op-1",
    account_id: "acct-1",
    provider_model_id: "model-1",
    operation: "tools",
    state: "certified",
    capability_truth: "supported",
    version: 1,
    certified_at: null,
    certified_and_supported: true,
    review_reasons: [],
    ...overrides,
  };
}

function getCertificationMock() {
  return vi.mocked(controlClient.getCertification);
}

describe("CertificationSummary — certification states", () => {
  it("renders every certification state, each with its own badge", () => {
    const states: CertificationSummaryOperation["state"][] = [
      "discovered",
      "observed",
      "probing",
      "certified",
      "suspended",
      "expired",
    ];
    render(
      <CertificationSummary
        operations={states.map((state, i) => op({ offeringOperationId: `op-${i}`, state }))}
      />,
    );
    for (const state of states) {
      screen.getByTitle(`certification: ${state}`);
    }
  });

  it("renders capability truth via the frozen CapabilityTruthBadge, never a hand-rolled element", () => {
    render(<CertificationSummary operations={[op({ truth: "unsupported" })]} />);
    screen.getByTitle("capability truth: unsupported");
  });
});

describe("CertificationSummary — the routability conjunction (the crown requirement)", () => {
  it("certified + unknown reads as not routable, and certified + supported (positive control) reads as routable", () => {
    render(
      <CertificationSummary
        operations={[
          op({ offeringOperationId: "op-unknown", state: "certified", truth: "unknown" }),
          op({ offeringOperationId: "op-supported", state: "certified", truth: "supported" }),
        ]}
      />,
    );
    screen.getByText("Not routable yet");
    screen.getByText("Routable");
  });

  it("certified + unsupported is also not routable", () => {
    render(<CertificationSummary operations={[op({ state: "certified", truth: "unsupported" })]} />);
    screen.getByText("Not routable yet");
  });

  it("a non-certified state (e.g. probing) reads as not routable regardless of truth", () => {
    render(<CertificationSummary operations={[op({ state: "probing", truth: "supported" })]} />);
    screen.getByText("Not routable");
    expect(screen.queryByText("Not routable yet")).toBeNull();
  });
});

describe("CertificationSummary — probe execution", () => {
  it("no probe has run renders the unknown treatment, never a fabricated pending probe", async () => {
    getCertificationMock().mockResolvedValue(
      certRead({ state: "observed", capability_truth: "unknown", certified_and_supported: false }),
    );
    render(<CertificationSummary operations={[op({ state: "observed", truth: "unknown" })]} />);

    fireEvent.click(screen.getByRole("button"));
    await screen.findByText(/no probe has run yet/i);
    expect(screen.queryByTitle(/probe execution: pending/i)).toBeNull();
  });

  it("a probe that has run renders its real execution state", async () => {
    getCertificationMock().mockResolvedValue(certRead({ probe_execution: "succeeded" }));
    render(<CertificationSummary operations={[op()]} />);

    fireEvent.click(screen.getByRole("button"));
    await screen.findByTitle(/probe execution: succeeded/i);
  });
});

describe("CertificationSummary — review reasons come from the server", () => {
  it("renders exactly the reasons the server returned", async () => {
    getCertificationMock().mockResolvedValueOnce(certRead({ review_reasons: ["capability_not_certified"] }));
    render(<CertificationSummary operations={[op({ offeringOperationId: "op-a" })]} />);

    fireEvent.click(screen.getByRole("button"));
    const list = await screen.findByRole("list", { name: /review reasons/i });
    const items = within(list)
      .getAllByRole("listitem")
      .map((el) => el.textContent);
    expect(items).toEqual(["capability_not_certified"]);
  });

  it("renders no reason element at all when review_reasons is empty — never a fabricated fallback", async () => {
    getCertificationMock().mockResolvedValueOnce(certRead({ review_reasons: [] }));
    render(<CertificationSummary operations={[op({ offeringOperationId: "op-b" })]} />);

    fireEvent.click(screen.getByRole("button"));
    await waitFor(() => expect(getCertificationMock()).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole("list", { name: /review reasons/i })).toBeNull();
  });
});

describe("CertificationSummary — the list row makes no certification request", () => {
  it("a collapsed row issues zero /certification calls; expanding issues exactly one, even across re-collapse/re-expand", async () => {
    getCertificationMock().mockResolvedValue(certRead());
    render(<CertificationSummary operations={[op()]} />);

    expect(getCertificationMock()).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button")); // expand
    await waitFor(() => expect(getCertificationMock()).toHaveBeenCalledTimes(1));

    fireEvent.click(screen.getByRole("button")); // collapse
    fireEvent.click(screen.getByRole("button")); // re-expand
    expect(getCertificationMock()).toHaveBeenCalledTimes(1);
  });
});

describe("CertificationSummary — empty state", () => {
  it("renders NOTHING for zero tracked operations — the mount stays, the output is empty", () => {
    const { container } = render(<CertificationSummary operations={[]} />);
    // No lone dash, no buttons, no fabricated rows: an account whose
    // certification data lives on the Model Test Report surface gets no
    // dead placeholder here.
    expect(container.firstChild).toBeNull();
  });
});

describe("CertificationSummary — accessibility", () => {
  it("has zero axe violations for a mixed collapsed/expanded summary", async () => {
    getCertificationMock().mockResolvedValue(certRead({ review_reasons: ["capability_not_certified"] }));
    const { container } = render(
      <CertificationSummary
        operations={[
          op({ offeringOperationId: "op-1", state: "certified", truth: "supported" }),
          op({ offeringOperationId: "op-2", state: "observed", truth: "unknown" }),
        ]}
      />,
    );
    fireEvent.click(screen.getAllByRole("button")[0]);
    await waitFor(() => expect(getCertificationMock()).toHaveBeenCalledTimes(1));

    await assertNoAxeViolations(container);
  });
});
