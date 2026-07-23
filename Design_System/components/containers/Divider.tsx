import * as React from "react";

export interface DividerProps {
  vertical?: boolean;
  className?: string;
}

export function Divider(props: DividerProps) {
  const { vertical = false, className = "" } = props;
  return vertical ? <span className={("vn-divider--v " + className).trim()} aria-hidden="true"></span> : <hr className={("vn-divider " + className).trim()} />;
}
