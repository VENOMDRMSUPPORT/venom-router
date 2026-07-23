import * as React from "react";

export interface SkeletonProps {
  width?: string | number;
  height?: string | number;
  className?: string;
  style?: React.CSSProperties;
}

export function Skeleton(props: SkeletonProps) {
  const { width = "100%", height = 14, className = "", style } = props;
  return <span className={("vn-skeleton " + className).trim()} style={{ display: "inline-block", width, height, ...style }} aria-hidden="true"></span>;
}
