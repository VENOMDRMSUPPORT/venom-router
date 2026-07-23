import * as React from "react";
import { Icon } from "../icons/Icon";

export interface AccordionItem {
  title: React.ReactNode;
  content: React.ReactNode;
}

export interface AccordionProps {
  items?: AccordionItem[];
  /** Indexes (into `items`) open by default. */
  defaultOpen?: number[];
  className?: string;
}

export function Accordion(props: AccordionProps) {
  const { items = [], defaultOpen = [], className = "" } = props;
  const [open, setOpen] = React.useState<number[]>(defaultOpen);
  const toggle = (i: number) => setOpen((o) => (o.includes(i) ? o.filter((x) => x !== i) : [...o, i]));
  return (
    <div className={("vn-accordion " + className).trim()}>
      {items.map((it, i) => {
        const isOpen = open.includes(i);
        return (
          <div className="vn-accordion-item" key={i}>
            <button type="button" className="vn-accordion-trigger" aria-expanded={isOpen} onClick={() => toggle(i)}>
              <span style={{ display: "flex", alignItems: "center", gap: "var(--space-2)" }}>{it.title}</span>
              <Icon name="chevron-right" size={14} className="vn-chevron" />
            </button>
            {isOpen ? <div className="vn-accordion-body">{it.content}</div> : null}
          </div>
        );
      })}
    </div>
  );
}

export interface CollapsibleProps {
  title?: React.ReactNode;
  defaultOpen?: boolean;
  children?: React.ReactNode;
  className?: string;
}

export function Collapsible(props: CollapsibleProps) {
  const { title, defaultOpen = false, children, className = "" } = props;
  const [open, setOpen] = React.useState(defaultOpen);
  return (
    <div className={("vn-accordion " + className).trim()}>
      <div className="vn-accordion-item">
        <button type="button" className="vn-accordion-trigger" aria-expanded={open} onClick={() => setOpen(!open)}>
          <span style={{ display: "flex", alignItems: "center", gap: "var(--space-2)" }}>{title}</span>
          <Icon name="chevron-right" size={14} className="vn-chevron" />
        </button>
        {open ? <div className="vn-accordion-body">{children}</div> : null}
      </div>
    </div>
  );
}
