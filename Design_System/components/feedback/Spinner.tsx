import * as React from "react";

export interface SpinnerProps {
  size?: "sm" | "md" | "lg";
  label?: string;
  className?: string;
}

export function Spinner(props: SpinnerProps) {
  const { size = "md", label = "Loading", className = "" } = props;
  return <span className={("vn-spinner " + (size === "lg" ? "vn-spinner--lg " : "") + className).trim()} role="status" aria-label={label}></span>;
}
