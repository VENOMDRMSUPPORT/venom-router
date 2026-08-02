import { useState } from "react";
import {
  CapabilityTruthBadge,
  CertificationStateBadge,
  CertificationTimeline,
  ProbeResultSummary,
  RoutableIndicator,
  type CapabilityTruth,
  type CertState,
  type ProbeExecutionState,
} from "@venom/design-system/domain";
import { getCertification, type CertificationRead } from "../api/controlClient";

export interface CertificationSummaryOperation {
  offeringOperationId: string;
  operation: string;
  state: CertState;
  truth: CapabilityTruth;
}

export interface CertificationSummaryProps {
  operations: CertificationSummaryOperation[];
}

interface DetailState {
  loading: boolean;
  data?: CertificationRead;
  error?: string;
}

/**
 * One account's certification-state summary (P3c-UI-001), composed
 * ENTIRELY from the frozen `@venom/design-system` model-intelligence
 * components — no local re-derivation of state/truth/routability, no
 * fabricated probe-execution value.
 *
 * The list row renders straight from `operations` (already-known
 * state/truth, no HTTP call). Expanding a row fetches
 * GET /offerings/{id}/certification ONCE — cached in `details`, never
 * re-fetched on a later re-expand — and additionally renders the
 * probe-execution dimension (RoutableIndicator's own "certified ∧
 * supported" conjunction already covers the crown requirement: certified +
 * unknown/unsupported reads as not routable, certified + supported reads
 * as routable) plus the server's own review_reasons verbatim.
 */
export default function CertificationSummary(props: CertificationSummaryProps) {
  const { operations } = props;
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [details, setDetails] = useState<Record<string, DetailState>>({});

  // Zero operations render NOTHING: the mount stays (callers never need a
  // conditional), but a lone "—" for an account whose certification data
  // lives on the Model Test Report surface would be dead space claiming
  // "no data tracked" — absence of rows here is not that claim.
  if (operations.length === 0) {
    return null;
  }

  function toggle(op: CertificationSummaryOperation) {
    const id = op.offeringOperationId;
    if (expandedId === id) {
      setExpandedId(null);
      return;
    }
    setExpandedId(id);
    if (details[id]) return; // already fetched once — never re-fetch on re-expand
    setDetails((prev) => ({ ...prev, [id]: { loading: true } }));
    getCertification(id)
      .then((data) => setDetails((prev) => ({ ...prev, [id]: { loading: false, data } })))
      .catch((err) =>
        setDetails((prev) => ({
          ...prev,
          [id]: { loading: false, error: err instanceof Error ? err.message : String(err) },
        })),
      );
  }

  return (
    <div className="flex flex-col gap-2">
      {operations.map((op) => {
        const id = op.offeringOperationId;
        const isExpanded = expandedId === id;
        const detail = details[id];
        return (
          <div key={id} className="flex flex-col gap-1">
            <button
              type="button"
              className="flex items-center gap-2"
              aria-expanded={isExpanded}
              onClick={() => toggle(op)}
            >
              <span className="vn-mono-xs">{op.operation}</span>
              <CertificationStateBadge state={op.state} />
              <CapabilityTruthBadge truth={op.truth} />
              <RoutableIndicator state={op.state} truths={{ [op.operation]: op.truth }} required={[op.operation]} />
            </button>
            {isExpanded ? (
              <div className="flex flex-col gap-1" style={{ paddingLeft: "var(--space-4)" }}>
                {!detail || detail.loading ? (
                  <span className="vn-caption">Loading certification detail…</span>
                ) : detail.error ? (
                  <span className="vn-caption">{detail.error}</span>
                ) : (
                  <CertificationDetail data={detail.data as CertificationRead} />
                )}
              </div>
            ) : null}
          </div>
        );
      })}
    </div>
  );
}

function CertificationDetail(props: { data: CertificationRead }) {
  const { data } = props;
  return (
    <>
      {data.probe_execution ? (
        <ProbeResultSummary
          operation={data.operation}
          execution={data.probe_execution as ProbeExecutionState}
          truth={data.capability_truth as CapabilityTruth}
          at={data.certified_at ?? undefined}
        />
      ) : (
        <span className="vn-caption" title="No probe has run for this offering-operation yet">
          — no probe has run yet
        </span>
      )}
      <CertificationTimeline state={data.state as CertState} />
      {data.review_reasons.length > 0 ? (
        <div className="vn-caption" role="list" aria-label="Review reasons">
          {data.review_reasons.map((reason) => (
            <span role="listitem" key={reason}>
              {reason}
            </span>
          ))}
        </div>
      ) : null}
    </>
  );
}
