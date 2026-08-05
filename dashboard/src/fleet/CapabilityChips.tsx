import type { OfferingCapability } from "../api/controlClient";
import { PROBEABLE_OPERATIONS } from "./modelStatus";

/** Per-capability badge styling. The hues are a CATEGORICAL palette declared
 * once in fleet.css (`--vnd-cap-*`) — raw color literals are forbidden here
 * because they cannot follow a theme, and the fallback below already reads its
 * colors through var() the same way. Same values, same pixels. */
const CAPABILITY_STYLE: Record<string, { border: string; bg: string; color: string; icon: string }> = {
  chat: { border: "var(--vnd-cap-chat)", bg: "var(--vnd-cap-chat-bg)", color: "var(--vnd-cap-chat)", icon: "message-square" }, // Orange
  coding: { border: "var(--vnd-cap-coding)", bg: "var(--vnd-cap-coding-bg)", color: "var(--vnd-cap-coding)", icon: "code" }, // Purple
  reasoning: { border: "var(--vnd-cap-reasoning)", bg: "var(--vnd-cap-reasoning-bg)", color: "var(--vnd-cap-reasoning)", icon: "brain" }, // Yellow/Gold
  structured_output: { border: "var(--vnd-cap-structured)", bg: "var(--vnd-cap-structured-bg)", color: "var(--vnd-cap-structured)", icon: "braces" }, // Teal
  tools: { border: "var(--vnd-cap-tools)", bg: "var(--vnd-cap-tools-bg)", color: "var(--vnd-cap-tools)", icon: "wrench" }, // Cyan
  vision: { border: "var(--vnd-cap-vision)", bg: "var(--vnd-cap-vision-bg)", color: "var(--vnd-cap-vision)", icon: "eye" }, // Blue
  streaming: { border: "var(--vnd-cap-streaming)", bg: "var(--vnd-cap-streaming-bg)", color: "var(--vnd-cap-streaming)", icon: "radio" }, // Pink
};

function getCapabilityStyle(operation: string) {
  return CAPABILITY_STYLE[operation] || {
    border: "var(--border-default)",
    bg: "var(--surface-sunken)",
    color: "var(--text-secondary)",
    icon: "box"
  };
}

export interface CapabilityChipsProps {
  capabilities: OfferingCapability[];
  /** How many chips render before collapsing the rest into "+N". */
  cap: number;
  /** When provided, a probeable capability (PROBEABLE_OPERATIONS: tools,
   * context_window, structured_output, vision) that carries an
   * offering_operation_id becomes a clickable button calling this with
   * (offering_operation_id, operation) instead of a static icon. Chat/
   * streaming and any capability with no offering_operation_id stay static
   * regardless of this prop — the server has nothing to probe for them.
   * Omitting it (every read-only caller, e.g. ModelsSurface) keeps every
   * chip a plain, non-interactive `role="img"` span exactly as before. */
  onTest?: (offeringOperationId: string, operation: string) => void;
  /** Disables every test button (e.g. another action in the same dialog is
   * already in flight) without changing which chips are interactive. */
  disabled?: boolean;
  /** The offering_operation_id currently probing, if any — that one chip's
   * button is disabled and shows a spinner in place of its icon. */
  probingOperationId?: string | null;
}

/**
 * CapabilityChips — the ONE shared capability-chip renderer (icons + tooltip,
 * provenance-aware). Owner requirement (2026-08-05): capabilities are ALWAYS
 * icon chips with tooltips, never words. A `provenance: "declared"` chip
 * additionally carries `vnd-capability-icon-box--declared` (a dashed border)
 * so the distinction reads WITHOUT hovering; `"probed"` and `""` render the
 * plain solid-border box. The tooltip's provenance line follows the same
 * split — probed claims a runtime probe, declared claims provider say-so,
 * and "" (not certified+supported) omits the line entirely since there is no
 * earned certification to attribute. Truth additionally tints the box:
 * `unsupported` gets `--failed` (a real negative result, worth flagging),
 * `unknown` gets `--untested` (nothing proven either way yet) — `supported`
 * keeps the plain per-operation color the box already had.
 */
export default function CapabilityChips({ capabilities, cap, onTest, disabled, probingOperationId }: CapabilityChipsProps) {
  if (capabilities.length === 0) {
    return <span className="vn-caption">No capability observed for this model yet.</span>;
  }

  const shown = capabilities.slice(0, cap);
  const overflow = capabilities.length - shown.length;

  return (
    <span className="vnd-report-chips">
      {shown.map((c) => {
        const style = getCapabilityStyle(c.operation);
        const declared = c.provenance === "declared";
        const failed = c.truth === "unsupported";
        const untested = c.truth === "unknown";
        const provenanceLine =
          c.provenance === "probed"
            ? "\nProvenance: proven (runtime probe)"
            : declared
              ? "\nProvenance: declared by provider"
              : "";
        const testable = onTest !== undefined && PROBEABLE_OPERATIONS.has(c.operation) && !!c.offering_operation_id;
        const probing = testable && probingOperationId === c.offering_operation_id;
        const className = [
          "vnd-capability-icon-box",
          declared ? "vnd-capability-icon-box--declared" : "",
          failed ? "vnd-capability-icon-box--failed" : "",
          untested ? "vnd-capability-icon-box--untested" : "",
        ]
          .filter(Boolean)
          .join(" ");
        const style2 = {
          "--cap-border-color": style.border,
          "--cap-bg-color": style.bg,
        } as React.CSSProperties;
        const title = `${c.operation.toUpperCase()}${provenanceLine}\nTruth: ${c.truth}\nState: ${c.state}${testable ? "\nClick to test" : ""}`;
        const icon = probing ? (
          <span className="vnd-capability-icon-box__spinner" aria-hidden />
        ) : (
          <span
            className={`vn-icon vn-icon--${style.icon}`}
            style={{ width: 14, height: 14, color: style.color }}
            aria-hidden
          />
        );

        if (testable) {
          return (
            <button
              key={c.operation}
              type="button"
              className={className}
              style={style2}
              title={title}
              aria-label={`Test ${c.operation}`}
              disabled={disabled || probing}
              onClick={() => onTest!(c.offering_operation_id!, c.operation)}
            >
              {icon}
            </button>
          );
        }

        return (
          <span key={c.operation} className={className} role="img" aria-label={c.operation} style={style2} title={title}>
            {icon}
          </span>
        );
      })}
      {overflow > 0 ? <span className="vnd-capability-overflow-box">+{overflow}</span> : null}
    </span>
  );
}
