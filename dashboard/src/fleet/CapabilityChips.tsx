import type { OfferingCapability } from "../api/controlClient";

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
 * earned certification to attribute.
 */
export default function CapabilityChips({ capabilities, cap }: CapabilityChipsProps) {
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
        const provenanceLine =
          c.provenance === "probed"
            ? "\nProvenance: proven (runtime probe)"
            : declared
              ? "\nProvenance: declared by provider"
              : "";
        const className = declared
          ? "vnd-capability-icon-box vnd-capability-icon-box--declared"
          : "vnd-capability-icon-box";
        return (
          <span
            key={c.operation}
            className={className}
            role="img"
            aria-label={c.operation}
            style={{
              "--cap-border-color": style.border,
              "--cap-bg-color": style.bg,
            } as React.CSSProperties}
            title={`${c.operation.toUpperCase()}${provenanceLine}\nTruth: ${c.truth}\nState: ${c.state}`}
          >
            <span
              className={`vn-icon vn-icon--${style.icon}`}
              style={{ width: 14, height: 14, color: style.color }}
              aria-hidden
            />
          </span>
        );
      })}
      {overflow > 0 ? <span className="vnd-capability-overflow-box">+{overflow}</span> : null}
    </span>
  );
}
