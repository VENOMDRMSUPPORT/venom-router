import * as React from "react";

export interface KbdProps {
  children?: React.ReactNode;
  className?: string;
}

export function Kbd(props: KbdProps) { return <kbd className={("vn-kbd " + (props.className || "")).trim()}>{props.children}</kbd>; }
