/** The provenance-derived prefix mark on a context-window badge (owner
 * requirement, 2026-08-05c): "≈" when the shown token count came from the
 * provider's own declared cap (`models.ContextProviderCap`, i.e. NOT
 * probe-verified), "✓" when it came from the canonical native fact a context
 * probe wrote back (`models.ContextNative`), and no mark at all when there is
 * no token count to qualify — `ContextWindowDisplay` already renders that case
 * as the word "ctx unknown" on its own, and a ≈/✓ beside it would be a claim
 * about a number that was never shown.
 *
 * This reuses the SAME `tokens`/`provenance` inputs `ContextWindowDisplay`
 * already receives; it never re-implements that component's "200K" token
 * formatting — it only prepends a small marker in front of it.
 *
 * Shared by both model surfaces (Live Models and the per-account Model
 * Report modal) — it used to exist as two byte-identical copies, one of them
 * commented as deliberately duplicated. There was never a reason for two: the
 * inputs and the two honest states (declared vs probe-verified) are the same
 * on both surfaces. */
export function ContextProvenanceMark(props: { tokens: number | null; provenance?: string }) {
  const { tokens, provenance } = props;
  if (tokens == null) return null;

  const title = "≈ declared by provider (not probe-verified) · ✓ verified by a context probe";
  if (provenance === "provider_cap") {
    return (
      <span
        className="vn-caption"
        title={title}
        aria-label="context declared by provider, not probe-verified"
      >
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
