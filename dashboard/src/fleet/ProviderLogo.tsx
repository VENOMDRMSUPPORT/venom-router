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
 * ProviderLogo — the provider's official logo PNG inside the design
 * system's Mark frame (fixed square, token-rounded hairline border,
 * identical dimensions to the letter avatar it replaces). Falls back to
 * the DS letter Mark when no logo asset ships for the slug, or when the
 * image fails to load at runtime — never a broken image.
 */
export default function ProviderLogo(props: ProviderLogoProps) {
  const { slug, name, size = "md" } = props;
  const [failed, setFailed] = useState(false);
  const src = providerLogoSrc(slug);

  if (!src || failed) {
    return <Mark name={slug} size={size} label={name} />;
  }

  const cls = ["vn-mark", size !== "md" ? "vn-mark--" + size : ""].filter(Boolean).join(" ");
  return (
    <span className={cls}>
      <img
        src={src}
        alt={name}
        style={{ width: "70%", height: "70%", objectFit: "contain" }}
        onError={() => setFailed(true)}
      />
    </span>
  );
}
