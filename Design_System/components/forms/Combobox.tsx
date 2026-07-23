import * as React from "react";
import { Icon } from "../icons/Icon";

export interface ComboboxProps {
  options?: string[];
  value?: string;
  onChange?: (value: string) => void;
  placeholder?: string;
  disabled?: boolean;
  id?: string;
  className?: string;
}

export function Combobox(props: ComboboxProps) {
  const { options = [], value, onChange, placeholder = "Search...", disabled, id, className = "" } = props;
  const [open, setOpen] = React.useState(false);
  const [query, setQuery] = React.useState("");
  const [active, setActive] = React.useState(0);
  const root = React.useRef<HTMLDivElement>(null);
  const listId = (id || "cbx") + "-list";
  const items = options.filter((o) => o.toLowerCase().includes(query.toLowerCase()));
  React.useEffect(() => {
    const onDoc = (e: MouseEvent) => { if (root.current && !root.current.contains(e.target as Node)) setOpen(false); };
    document.addEventListener("mousedown", onDoc);
    return () => document.removeEventListener("mousedown", onDoc);
  }, []);
  const pick = (v: string) => { if (onChange) onChange(v); setQuery(""); setOpen(false); };
  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === "ArrowDown") { e.preventDefault(); setOpen(true); setActive((a) => Math.min(a + 1, items.length - 1)); }
    else if (e.key === "ArrowUp") { e.preventDefault(); setActive((a) => Math.max(a - 1, 0)); }
    else if (e.key === "Enter" && open && items[active]) { e.preventDefault(); pick(items[active]); }
    else if (e.key === "Escape") setOpen(false);
  };
  return (
    <div ref={root} className={("vn-search " + className).trim()} style={{ position: "relative" }}>
      <Icon name="search" size={14} />
      <input className="vn-input" role="combobox" aria-expanded={open} aria-controls={listId} aria-autocomplete="list"
        id={id} placeholder={value || placeholder} value={query} disabled={disabled}
        onFocus={() => setOpen(true)} onKeyDown={onKey}
        onChange={(e: React.ChangeEvent<HTMLInputElement>) => { setQuery(e.target.value); setOpen(true); setActive(0); }} />
      {open && (
        <div className="vn-menu" role="listbox" id={listId} style={{ position: "absolute", top: "calc(100% + 4px)", left: 0, right: 0, maxHeight: 220, overflow: "auto" }}>
          {items.length === 0 && <div className="vn-menu-label vn-caption">No matches</div>}
          {items.map((o, i) => (
            <button key={o} type="button" role="option" aria-selected={o === value}
              className={"vn-menu-item" + (i === active ? " is-active" : "")}
              style={i === active ? { background: "var(--menu-item-hover-bg)" } : undefined}
              onMouseEnter={() => setActive(i)} onClick={() => pick(o)}>
              <span className="vn-truncate">{o}</span>
              {o === value ? <Icon name="check" size={12} style={{ marginLeft: "auto" }} /> : null}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
