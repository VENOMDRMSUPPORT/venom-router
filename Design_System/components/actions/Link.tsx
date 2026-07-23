import * as React from "react";
import { Icon } from "../icons/Icon";

export interface LinkProps extends React.AnchorHTMLAttributes<HTMLAnchorElement> {
  /** Opens in a new tab with `rel="noreferrer"` and shows the external-link glyph. */
  external?: boolean;
}

export function Link(props: LinkProps) {
  const { href = "#", external = false, children, className = "", ...rest } = props;
  return (
    <a href={href} className={className} {...(external ? { target: "_blank", rel: "noreferrer" } : {})} {...rest}>
      {children}
      {external ? <Icon name="external-link" size={12} style={{ marginLeft: 3, verticalAlign: "-1px" }} /> : null}
    </a>
  );
}
