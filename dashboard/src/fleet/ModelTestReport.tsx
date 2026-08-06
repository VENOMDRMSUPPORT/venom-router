import { useMemo, useState, type ChangeEvent } from "react";
import { Badge, Button, Dialog, EmptyState, Input, Select } from "@venom/design-system/primitives";
import {
  ContextWindowDisplay,
  ModelCapabilitySet,
  type CapabilityProvenances,
  type CapabilityTruths,
} from "@venom/design-system/domain";
import { type AccountProjection, type EffectiveOffering } from "../api/controlClient";
import { benchmarkProvenanceTitle } from "./benchmarkProvenance";
import { ContextProvenanceMark } from "./ContextProvenanceMark";
import { deriveModelStatus, isOfferingEnabled, type ModelStatus } from "./modelStatus";
import ProviderLogo from "./ProviderLogo";

export interface ModelTestReportProps {
  open: boolean;
  /** The account whose models are reported. */
  account: AccountProjection;
  /** The provider's display name for the dialog title. */
  providerName: string;
  /** THIS account's offerings (one per provider_model_id). */
  offerings: EffectiveOffering[];
  /** Unused now that this dialog is a report and triggers no server action
   * of its own — kept because FleetOverview.tsx:517-530 (the only mount) still
   * passes it, and that mount's signature is out of scope for this change. */
  csrfToken: string;
  /** Unused for the same reason as csrfToken. */
  onSessionExpired: () => void;
  onClose: () => void;
  /** Unused for the same reason as csrfToken — a report triggers nothing
   * that would need a refetch. */
  onRefetch: () => void;
}

type StatusFilter = "all" | ModelStatus;

const FILTER_OPTIONS = [
  { value: "all", label: "All Models" },
  { value: "working", label: "Working" },
  { value: "failed", label: "Failed" },
  { value: "untested", label: "Untested" },
];

const STATUS_BADGE: Record<ModelStatus, { tone: "healthy" | "critical" | "unknown"; label: string }> = {
  working: { tone: "healthy", label: "WORKING" },
  failed: { tone: "critical", label: "FAILED" },
  untested: { tone: "unknown", label: "UNTESTED" },
};

/** How many capability chips render before collapsing into "+N". */
const CAPABILITY_CHIP_CAP = 6;

/**
 * The per-account Model Report (image 3): four derived stat tiles
 * (WORKING / FAILED / UNTESTED / ENABLED), a search box and status filter
 * over the account's discovered offerings, and one row per model with
 * context/quality facts, capability chips, and the derived status badge.
 *
 * This is a REPORT, not a test console: every fact shown here — the status
 * badge, the capability chips, the context-window provenance mark — is a
 * truth the server already stated (a certified probe, or the provider's own
 * declaration), never something this dialog triggers. To pull a fresh
 * catalog or re-probe a capability, use the provider row's own actions on
 * the Providers page.
 *
 * DELIBERATE DEVIATION from the documented UI: no per-model checkboxes,
 * no Enable All / Disable All, no "Save selection" — the backend has no
 * per-model enable/disable persistence, and an inert control would be a
 * lie. "Enabled" here is the server's own routability (certified AND
 * supported), reported, not toggled.
 */
export default function ModelTestReport(props: ModelTestReportProps) {
  const { open, account, providerName, offerings, onClose } = props;

  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState<StatusFilter>("all");

  const stats = useMemo(() => {
    const counts = { working: 0, failed: 0, untested: 0, enabled: 0 };
    for (const offering of offerings) {
      counts[deriveModelStatus(offering)] += 1;
      if (isOfferingEnabled(offering)) counts.enabled += 1;
    }
    return counts;
  }, [offerings]);

  const listed = useMemo(() => {
    const query = search.trim().toLowerCase();
    return offerings.filter((o) => {
      if (filter !== "all" && deriveModelStatus(o) !== filter) return false;
      if (query === "") return true;
      return (
        (o.display_name ?? "").toLowerCase().includes(query) ||
        o.provider_model_id.toLowerCase().includes(query)
      );
    });
  }, [offerings, search, filter]);

  return (
    <Dialog
      open={open}
      onClose={onClose}
      wide
      title={`Model Report: ${providerName}`}
      description={`What ${providerName} exposes on this account, and what each model has proven or declared it can do.`}
      footer={
        <Button variant="ghost" onClick={onClose}>
          Close
        </Button>
      }
    >
      <div className="flex flex-col gap-3">
        <p className="vn-caption">
          The models this account exposes, what each one can do, and where that fact came from — a certified probe,
          or the provider&apos;s own declaration.
        </p>

        <div className="vnd-report-tiles">
          <div className="vnd-report-tile">
            <span className="vnd-report-tile-label">Working</span>
            <span className="vnd-report-tile-value vnd-report-tile-value--healthy">{stats.working}</span>
          </div>
          <div className="vnd-report-tile">
            <span className="vnd-report-tile-label">Failed</span>
            <span className="vnd-report-tile-value vnd-report-tile-value--critical">{stats.failed}</span>
          </div>
          <div className="vnd-report-tile">
            <span className="vnd-report-tile-label">Untested</span>
            <span className="vnd-report-tile-value vnd-report-tile-value--muted">{stats.untested}</span>
          </div>
          <div
            className="vnd-report-tile"
            title="Models with at least one routable (certified + supported) capability"
          >
            <span className="vnd-report-tile-label">Enabled</span>
            <span className="vnd-report-tile-value">{stats.enabled}</span>
          </div>
        </div>

        <div className="vnd-report-toolbar">
          <span className="vn-toolbar-spacer" style={{ flex: 1 }} />
          <Input
            type="search"
            placeholder="Search models…"
            aria-label="Search models"
            value={search}
            onChange={(e: ChangeEvent<HTMLInputElement>) => setSearch(e.target.value)}
          />
          <Select
            aria-label="Filter models by status"
            options={FILTER_OPTIONS}
            value={filter}
            onChange={(e: ChangeEvent<HTMLSelectElement>) => setFilter(e.target.value as StatusFilter)}
          />
        </div>

        {listed.length === 0 ? (
          <EmptyState
            icon="box"
            title={offerings.length === 0 ? "No models discovered yet" : "No models match"}
            description={
              offerings.length === 0
                ? "Use this account's \"Fetch models from provider\" action to pull its catalog."
                : "Try a different search or status filter."
            }
          />
        ) : (
          <div className="vnd-report-rows">
            {listed.map((offering) => {
              const status = deriveModelStatus(offering);
              const badge = STATUS_BADGE[status];
              return (
                <div key={offering.provider_model_id} className="vnd-report-row" data-testid={`report-row-${offering.provider_model_id}`}>
                  <div className="vnd-report-row-body">
                    <div className="vnd-model-name-group">
                      <ProviderLogo slug={account.provider} name={providerName} size="md" />
                      <div className="vnd-model-names">
                        <span className="vnd-account-email">{offering.display_name || offering.provider_model_id}</span>
                        <span className="vn-caption vn-mono-xs">{offering.provider_model_id}</span>
                      </div>
                    </div>
                    <div className="vnd-report-row-facts" data-testid={`report-facts-${offering.provider_model_id}`}>
                      <ContextProvenanceMark tokens={offering.effective_context_tokens} provenance={offering.context_provenance} />
                      <ContextWindowDisplay
                        tokens={offering.effective_context_tokens}
                        verified={offering.context_provenance === "native"}
                        source={offering.context_provenance || undefined}
                      />
                      {offering.quality_known ? (
                        // EffectiveOffering carries no latest_benchmark (that
                        // field lives on ModelGroup, which this modal never
                        // receives), so this calls the SAME shared helper the
                        // Live Models page uses, with null — the honest,
                        // undated "Local benchmark" state, never a fabricated
                        // date.
                        <Badge tone="info" mono icon="gauge" title={benchmarkProvenanceTitle(null)}>
                          {offering.quality_score.toFixed(2)}
                        </Badge>
                      ) : (
                        <Badge tone="unknown" icon="circle-help">
                          Not rated
                        </Badge>
                      )}
                      {(() => {
                        // Owner requirement (2026-08-06, reversing
                        // 2026-08-05a): the design system's ModelCapabilitySet
                        // renders icon + text label per capability, so
                        // `vision` and `reasoning` read apart without a
                        // hover. It has no built-in cap/overflow concept, so
                        // the "+N" collapse (a deliberate owner requirement,
                        // still needed once a model exposes many
                        // capabilities) is computed here, exactly as
                        // CapabilityChips did: slice to CAPABILITY_CHIP_CAP,
                        // show the remainder as "+N".
                        //
                        // `provenances` (fix round 1, restoring a 2026-08-05
                        // requirement CapabilityChips' deletion silently
                        // dropped): a "declared" capability must read apart
                        // from a "probed" one without hovering.
                        const shown = offering.capabilities.slice(0, CAPABILITY_CHIP_CAP);
                        const overflow = offering.capabilities.length - shown.length;
                        const truths: CapabilityTruths = {};
                        const provenances: CapabilityProvenances = {};
                        for (const c of shown) {
                          truths[c.operation] = c.truth;
                          provenances[c.operation] = c.provenance;
                        }
                        return (
                          <>
                            <ModelCapabilitySet
                              capabilities={shown.map((c) => c.operation)}
                              truths={truths}
                              provenances={provenances}
                            />
                            {overflow > 0 ? (
                              <span className="vnd-capability-overflow-box">+{overflow}</span>
                            ) : null}
                          </>
                        );
                      })()}
                    </div>
                  </div>
                  <span data-testid={`report-status-${offering.provider_model_id}`}>
                    <Badge tone={badge.tone} mono>
                      {badge.label}
                    </Badge>
                  </span>
                </div>
              );
            })}
          </div>
        )}
      </div>
    </Dialog>
  );
}
