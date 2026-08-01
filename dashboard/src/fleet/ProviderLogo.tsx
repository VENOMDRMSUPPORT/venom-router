import { useState } from "react";
import { Mark } from "@venom/design-system/primitives";
import { providerLogoSrc } from "./providerLogos";

export interface ProviderLogoProps {
  /** Canonical provider slug (catalog id), e.g. "opencode-zen". */
  slug: string;
  /** Human display name — the img alt / fallback mark's accessible label. */
  name: string;
  size?: "sm" | "md" | "lg";
}

/**
 * ProviderLogo — an edge-safe provider identity frame. Brand artwork fills
 * the frame without an inline white tile; transparent corners reveal the
 * semantic secondary surface. Missing/broken artwork falls back to Mark.
 */
export default function ProviderLogo(props: ProviderLogoProps) {
  const { slug, name, size = "md" } = props;
  const [failed, setFailed] = useState(false);
  const src = providerLogoSrc(slug);

  if (!src || failed) {
    return <Mark name={slug} size={size} label={name} className={`vn-provider-logo vn-provider-logo--${size}`} />;
  }

  const cls = [
    "vn-provider-logo",
    `vn-provider-logo--${size}`,
    slug === "agnes-ai" ? "vn-provider-logo--edge-to-edge" : "",
  ].filter(Boolean).join(" ");
  return (
    <span className={cls}>
      <img
        src={src}
        alt={name}
        onError={() => setFailed(true)}
      />
    </span>
  );
}
