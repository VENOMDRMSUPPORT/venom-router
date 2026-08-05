import { useMemo, useState, type ChangeEvent } from "react";
import { toast } from "@venom/design-system";
import { Badge, Button, Dialog, EmptyState, Input, Select } from "@venom/design-system/primitives";
import { ContextWindowDisplay, TypedErrorDisplay } from "@venom/design-system/domain";
import {
  AuthApiError,
  isSessionExpired,
  startDiscovery,
  startProbe,
  toApiError,
  type AccountProjection,
  type EffectiveOffering,
} from "../api/controlClient";
import CapabilityChips from "./CapabilityChips";
import { pollJobToTerminal, runWithConcurrency } from "./jobs";
import { deriveModelStatus, isOfferingEnabled, probeTargets, type ModelStatus } from "./modelStatus";
import ProviderLogo from "./ProviderLogo";

/** The provenance-derived prefix mark on a context-window badge: "≈" when
 * the shown token count came from the provider's own declared cap (not
 * probe-verified), "✓" when a context probe verified it, nothing when there
 * is no token count to qualify — ContextWindowDisplay already renders that
 * case as "ctx unknown" on its own. Mirrors ModelsSurface's own
 * ContextProvenanceMark (same inputs, same two marks); duplicated locally
 * rather than shared since it is a small, page-local presentation detail on
 * both sides. */
function ContextProvenanceMark(props: { tokens: number | null; provenance?: string }) {
  const { tokens, provenance } = props;
  if (tokens == null) return null;
  const title = "≈ declared by provider (not probe-verified) · ✓ verified by a context probe";
  if (provenance === "provider_cap") {
    return (
      <span className="vn-caption" title={title} aria-label="context declared by provider, not probe-verified">
        ≈
      </span>
    );
  }
  if (provenance === "native") {
    return (
      <span className="vn-caption" title={title} aria-label="context verified by context probe">
        ✓
      </span>
    );
  }
  return null;
}

export interface ModelTestReportProps {
  open: boolean;
  /** The account whose models are reported. */
  account: AccountProjection;
  /** The provider's display name for the dialog title. */
  providerName: string;
  /** THIS account's offerings (one per provider_model_id). */
  offerings: EffectiveOffering[];
  csrfToken: string;
  onSessionExpired: () => void;
  onClose: () => void;
  /** Triggers the page-level refetch (providers + accounts + offerings). */
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

/** Probe concurrency for "Test All" — bounded so a large catalog cannot
 * stampede the provider (the server's own probe gates still apply). */
const TEST_ALL_CONCURRENCY = 3;

/** How many capability chips render before collapsing into "+N". */
const CAPABILITY_CHIP_CAP = 6;

/**
 * The per-account Model Test Report (image 3): four derived stat tiles
 * (WORKING / FAILED / UNTESTED / ENABLED), Refresh Models (discovery job
 * polled to terminal), Test All (bounded-concurrency probes over EVERY
 * probeable capability across every listed model — not just one per model),
 * search + status filter, and one row per model with context/quality/cost
 * facts, capability chips, and the derived status badge.
 *
 * Chat is certified automatically the next time it succeeds in real use —
 * the server rejects a manual probe of it 422 — so it is never a clickable
 * chip; every OTHER capability chip (tools/context_window/structured_output/
 * vision) IS individually clickable to test just that one operation,
 * regardless of its current truth (untested, failed, or already-proven are
 * all legitimately re-testable) — see CapabilityChips' onTest.
 *
 * DELIBERATE DEVIATION from the documented UI: no per-model checkboxes,
 * no Enable All / Disable All, no "Save selection" — the backend has no
 * per-model enable/disable persistence, and an inert control would be a
 * lie. "Enabled" here is the server's own routability (certified AND
 * supported), reported, not toggled.
 */
export default function ModelTestReport(props: ModelTestReportProps) {
  const { open, account, providerName, offerings, csrfToken, onSessionExpired, onClose, onRefetch } = props;

  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState<StatusFilter>("all");
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState<{ done: number; total: number } | null>(null);
  const [actionError, setActionError] = useState<AuthApiError | null>(null);
  /** The offering_operation_id a single capability chip is currently
   * probing (Test All uses `progress` instead) — lets CapabilityChips show
   * a spinner on exactly that chip. Reset unconditionally in runAction's
   * finally, alongside busy/progress, since it is always null already for
   * Refresh Models/Test All. */
  const [probingOperationId, setProbingOperationId] = useState<string | null>(null);

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

  async function runAction(action: () => Promise<void>) {
    setBusy(true);
    setActionError(null);
    try {
      await action();
      onRefetch();
    } catch (err) {
      if (isSessionExpired(err)) {
        onSessionExpired();
        return;
      }
      setActionError(toApiError(err));
    } finally {
      setBusy(false);
      setProgress(null);
      setProbingOperationId(null);
    }
  }

  function handleRefreshModels() {
    void runAction(async () => {
      try {
        const handle = await startDiscovery(account.id, csrfToken);
        const job = await pollJobToTerminal(handle.job_id);
        if (job.status !== "completed") {
          throw new AuthApiError(0, {
            code: job.error?.code ?? `job_${job.status}`,
            message: job.error?.message ?? `The discovery job is ${job.status}.`,
            request_id: "",
            retryable: true,
          });
        }
        toast.success("Discovery completed", {
          detail: `Discovered models for ${account.provider}`,
        });
      } catch (err) {
        toast.danger("Discovery failed", {
          detail: err instanceof Error ? err.message : String(err),
        });
        throw err;
      }
    });
  }

  function handleTestAll() {
    const targets = listed.flatMap((o) => probeTargets(o));
    if (targets.length === 0) return;
    setProgress({ done: 0, total: targets.length });
    void runAction(async () => {
      try {
        const results = await runWithConcurrency(
          targets,
          TEST_ALL_CONCURRENCY,
          async (offeringOperationID) => {
            const handle = await startProbe(offeringOperationID, csrfToken);
            await pollJobToTerminal(handle.job_id);
          },
          (done) => setProgress({ done, total: targets.length }),
        );
        const failed = results.filter((r) => r.status === "rejected");
        const sessionExpired = failed.find((r) => r.status === "rejected" && isSessionExpired(r.reason));
        if (sessionExpired) {
          onSessionExpired();
          return;
        }
        if (failed.length > 0) {
          toast.danger("Model probe failed", {
            detail: `${failed.length} of ${targets.length} probe(s) failed`,
          });
          throw new AuthApiError(0, {
            code: "probe_batch_partial",
            message: `${failed.length} of ${targets.length} probe(s) could not run. The rest completed; statuses below are refreshed.`,
            request_id: "",
            retryable: true,
          });
        }
        toast.success("Model probe completed", {
          detail: `Probe for all ${targets.length} capabilities passed`,
        });
      } catch (err) {
        if (!(err instanceof AuthApiError && err.code === "probe_batch_partial")) {
          toast.danger("Model probe failed", {
            detail: err instanceof Error ? err.message : String(err),
          });
        }
        throw err;
      }
    });
  }

  /** One capability chip's own test action (CapabilityChips' onTest) —
   * probes exactly the operation the user clicked, not "the first
   * probeable one for this model" (the old single-button-per-row
   * behavior). */
  function handleTestCapability(offeringOperationId: string, operation: string) {
    setProbingOperationId(offeringOperationId);
    void runAction(async () => {
      try {
        const handle = await startProbe(offeringOperationId, csrfToken);
        await pollJobToTerminal(handle.job_id);
        toast.success("Model probe completed", {
          detail: `Probe for ${operation} passed`,
        });
      } catch (err) {
        toast.danger("Model probe failed", {
          detail: err instanceof Error ? err.message : String(err),
        });
        throw err;
      }
    });
  }

  return (
    <Dialog
      open={open}
      onClose={onClose}
      wide
      title={`Model Test Report: ${providerName}`}
      description={`Test models for ${providerName} and review what this account can route.`}
      footer={
        <Button variant="ghost" onClick={onClose}>
          Close
        </Button>
      }
    >
      <div className="flex flex-col gap-3">
        <p className="vn-caption">
          Chat certifies itself automatically the next time it succeeds in real use — it can&apos;t be tested here.
          Every other capability icon below (tools, vision, context window, structured output) is clickable: click
          one to run a test for just that capability.
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
          <Button variant="secondary" size="sm" icon="refresh-cw" disabled={busy} onClick={handleRefreshModels}>
            Refresh Models
          </Button>
          <Button variant="secondary" size="sm" icon="play" disabled={busy} onClick={handleTestAll}>
            Test All
          </Button>
          {progress ? (
            <span className="vn-caption" role="status">
              Testing {progress.done}/{progress.total}…
            </span>
          ) : null}
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

        {actionError ? (
          <TypedErrorDisplay code={actionError.code} message={actionError.message} retryable={actionError.retryable} tone="critical" />
        ) : null}

        {listed.length === 0 ? (
          <EmptyState
            icon="box"
            title={offerings.length === 0 ? "No models discovered yet" : "No models match"}
            description={
              offerings.length === 0
                ? "Run Refresh Models to fetch this account's catalog from the provider."
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
                        <Badge tone="info" mono icon="gauge" title="Local benchmark">
                          {offering.quality_score.toFixed(2)}
                        </Badge>
                      ) : (
                        <Badge tone="unknown" icon="circle-help">
                          Not rated
                        </Badge>
                      )}
                      {offering.cost.is_free === true ? (
                        <Badge tone="healthy" icon="hand-coins">
                          Free
                        </Badge>
                      ) : offering.cost.is_free === false ? (
                        <Badge tone="unknown" icon="credit-card">
                          Paid
                        </Badge>
                      ) : (
                        <Badge tone="unknown" icon="circle-help">
                          Cost unknown
                        </Badge>
                      )}
                      <CapabilityChips
                        capabilities={offering.capabilities}
                        cap={CAPABILITY_CHIP_CAP}
                        onTest={handleTestCapability}
                        disabled={busy}
                        probingOperationId={probingOperationId}
                      />
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
