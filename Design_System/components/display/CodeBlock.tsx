import * as React from "react";
import { CopyButton } from "../actions/CopyButton";

export interface CodeBlockProps {
  code: string;
  label?: string;
  copyable?: boolean;
  className?: string;
}

export function CodeBlock(props: CodeBlockProps) {
  const { code, label, copyable = true, className = "" } = props;
  return (
    <pre className={("vn-codeblock vn-scroll " + className).trim()} aria-label={label} tabIndex={0}>
      {copyable ? <span className="vn-copy"><CopyButton value={code} label="Copy code" /></span> : null}
      <code>{code}</code>
    </pre>
  );
}

export interface CodeProps {
  children?: React.ReactNode;
  className?: string;
}

/** Code — inline code span. */
export function Code(props: CodeProps) { return <code className={("vn-code-inline " + (props.className || "")).trim()}>{props.children}</code>; }
