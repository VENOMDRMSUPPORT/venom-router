import * as React from "react";

export type TabOption = string | { value: string; label: React.ReactNode; count?: number };

export interface TabsProps {
  tabs?: TabOption[];
  value?: string;
  onChange?: (value: string) => void;
  label?: string;
  className?: string;
}

function tabValue(t: TabOption): string {
  return typeof t === "string" ? t : t.value;
}

export function Tabs(props: TabsProps) {
  const { tabs = [], value, onChange, label, className = "" } = props;
  const refs = React.useRef<Array<HTMLButtonElement | null>>([]);
  const idx = tabs.findIndex((t) => tabValue(t) === value);
  const onKey = (e: React.KeyboardEvent) => {
    let next = idx;
    if (e.key === "ArrowRight") next = (idx + 1) % tabs.length;
    else if (e.key === "ArrowLeft") next = (idx - 1 + tabs.length) % tabs.length;
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = tabs.length - 1;
    else return;
    e.preventDefault();
    const v = tabValue(tabs[next]);
    if (onChange) onChange(v);
    if (refs.current[next]) refs.current[next]!.focus();
  };
  return (
    <div role="tablist" aria-label={label} className={("vn-tabs " + className).trim()} onKeyDown={onKey}>
      {tabs.map((t, i) => {
        const v = tabValue(t);
        const lab = typeof t === "string" ? t : t.label;
        const count = typeof t === "string" ? undefined : t.count;
        const selected = v === value;
        return (
          <button key={v} type="button" role="tab" ref={(el) => { refs.current[i] = el; }}
            aria-selected={selected} tabIndex={selected ? 0 : -1}
            className="vn-tab" onClick={() => onChange && onChange(v)}>
            {lab}{count != null ? <span className="vn-count">{count}</span> : null}
          </button>
        );
      })}
    </div>
  );
}
