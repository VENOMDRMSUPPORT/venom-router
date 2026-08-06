import { afterEach, describe, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import type { ComponentProps } from "react";
import ProviderRow from "./ProviderRow";
import type { AccountProjection, Provider } from "../api/controlClient";

afterEach(cleanup);

const PROVIDER: Provider = {
  id: "opencode-zen",
  display_name: "OpenCode Zen",
  description: "An API-key provider.",
  auth_mode: "api_key",
  funding: { mode: "owner_policy", locked: false, non_expiring: false, fixed: null },
  capabilities: [],
  configured: true,
  missing_env: [],
};

function account(overrides: Partial<AccountProjection> = {}): AccountProjection {
  return {
    id: "acct-1",
    provider: "opencode-zen",
    external_id: "ext-1",
    auth_type: "api_key",
    connection_state: "connected",
    health_state: "healthy",
    reauth_in_progress: false,
    identity: {},
    funding: { funding: "free", source: "owner_policy", locked: false, version: "v1" },
    display_status: "healthy",
    eligibility: { eligible: true },
    quota: [],
    created_at: "2026-07-01T00:00:00Z",
    updated_at: "2026-07-01T00:00:00Z",
    ...overrides,
  } as AccountProjection;
}

function renderRow(overrides: Partial<ComponentProps<typeof ProviderRow>> = {}) {
  render(
    <ProviderRow
      provider={PROVIDER}
      accounts={[account()]}
      uniqueModelCount={12}
      workingModelCount={3}
      accountModelCounts={() => null}
      expanded={false}
      onToggleExpand={vi.fn()}
      onAddAccount={vi.fn()}
      onOpenModelReport={vi.fn()}
      csrfToken="csrf-token"
      onSessionExpired={vi.fn()}
      onChanged={vi.fn()}
      {...overrides}
    />,
  );
}

describe("ProviderRow — headline model counts lead with verified-working", () => {
  it("shows the live models count", () => {
    renderRow({ workingModelCount: 3, uniqueModelCount: 12 });
    screen.getByText("3 Live Models");
  });

  it("renders an honest dash when the offerings read is unknown", () => {
    renderRow({ workingModelCount: null, uniqueModelCount: null });
    screen.getByText("— Live Models");
  });

  it("keeps the verified-working explanation in the tooltip", () => {
    renderRow({ workingModelCount: 3, uniqueModelCount: 12 });
    screen.getByTitle("3 out of 12 models verified working");
  });
});
