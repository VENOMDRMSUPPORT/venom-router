import * as React from "react";

/** Deterministic initials from a name/slug ("opencode-zen" -> "OZ"). */
function initials(name: string) {
  const parts = String(name).split(/[\s_-]+/).filter(Boolean);
  return (parts.length >= 2 ? parts[0][0] + parts[1][0] : String(name).slice(0, 2)).toUpperCase();
}

export interface MarkProps {
  /** Name/slug used to derive deterministic initials when no `src` image is given. */
  name: string;
  src?: string;
  size?: "sm" | "md" | "lg";
  label?: string;
  className?: string;
}

export function Mark(props: MarkProps) {
  const { name, src, size = "md", label, className = "" } = props;
  const cls = ["vn-mark", size !== "md" ? "vn-mark--" + size : "", className].filter(Boolean).join(" ");
  return (
    <span className={cls} role="img" aria-label={label || name}>
      {src ? <img src={src} alt="" style={{ width: "70%", height: "70%", objectFit: "contain" }} /> : initials(name)}
    </span>
  );
}
