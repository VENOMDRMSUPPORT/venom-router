import * as React from "react";
import { Icon } from "../icons/Icon";

export interface CopyButtonProps {
  /** The text copied to the clipboard. */
  value: string;
  /** Accessible label (and tooltip) — becomes "Copied" for 1.5s after a successful copy. */
  label?: string;
  size?: "sm" | "md" | "lg";
  className?: string;
  onCopied?: () => void;
}

export function CopyButton(props: CopyButtonProps) {
  const { value, label = "Copy", size = "sm", className = "", onCopied } = props;
  const [copied, setCopied] = React.useState(false);
  const timer = React.useRef<ReturnType<typeof setTimeout> | null>(null);
  React.useEffect(() => () => { if (timer.current) clearTimeout(timer.current); }, []);
  const copy = async () => {
    try { await navigator.clipboard.writeText(value); } catch (e) { /* no-op: clipboard denied */ }
    setCopied(true); if (onCopied) onCopied();
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => setCopied(false), 1500);
  };
  return (
    <button type="button" className={("vn-btn vn-btn--icon vn-btn--ghost vn-btn--" + size + " vn-copy " + className).trim()}
      aria-label={copied ? "Copied" : label} title={copied ? "Copied" : label} onClick={copy} aria-live="polite">
      <Icon name={copied ? "check" : "copy"} size={12} />
    </button>
  );
}
