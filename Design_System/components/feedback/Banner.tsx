import * as React from "react";
import { Icon } from "../icons/Icon";

export type BannerTone = "info" | "warning" | "critical";

const ICONS: Record<BannerTone, string> = { info: "info", warning: "triangle-alert", critical: "circle-x" };

export interface BannerProps {
  tone?: BannerTone;
  children?: React.ReactNode;
  actions?: React.ReactNode;
  className?: string;
}

export function Banner(props: BannerProps) {
  const { tone = "warning", children, actions, className = "" } = props;
  return (
    <div className={("vn-banner vn-banner--" + tone + " " + className).trim()} role="status">
      <Icon name={ICONS[tone]} size={15} />
      <span style={{ flex: 1 }}>{children}</span>
      {actions ? <span className="vn-banner-actions">{actions}</span> : null}
    </div>
  );
}
