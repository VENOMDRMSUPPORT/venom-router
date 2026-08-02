import { useEffect, useRef, useState } from "react";
import { Icon } from "@venom/design-system/icons";
import { IconButton } from "@venom/design-system/primitives";
import { NAV, type NavItem } from "./nav";
import "./shell.css";

export interface CommandPaletteProps {
  isOpen: boolean;
  onClose: () => void;
  onSelectNav: (navKey: string) => void;
}

export default function CommandPalette(props: CommandPaletteProps) {
  const { isOpen, onClose, onSelectNav } = props;
  const [query, setQuery] = useState("");
  const [selectedIndex, setSelectedIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);

  const filtered = NAV.filter(
    (item) =>
      item.label.toLowerCase().includes(query.toLowerCase()) ||
      item.description.toLowerCase().includes(query.toLowerCase()) ||
      item.group.toLowerCase().includes(query.toLowerCase())
  );

  useEffect(() => {
    if (isOpen) {
      setQuery("");
      setSelectedIndex(0);
      setTimeout(() => inputRef.current?.focus(), 20);
    }
  }, [isOpen]);

  useEffect(() => {
    setSelectedIndex(0);
  }, [query]);

  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      if (!isOpen) return;

      if (e.key === "Escape") {
        e.preventDefault();
        onClose();
      } else if (e.key === "ArrowDown") {
        e.preventDefault();
        setSelectedIndex((prev) => (filtered.length > 0 ? (prev + 1) % filtered.length : 0));
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        setSelectedIndex((prev) => (filtered.length > 0 ? (prev - 1 + filtered.length) % filtered.length : 0));
      } else if (e.key === "Enter" && filtered.length > 0) {
        e.preventDefault();
        const selected = filtered[selectedIndex];
        if (selected) {
          onSelectNav(selected.key);
          onClose();
        }
      }
    }

    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [isOpen, filtered, selectedIndex, onClose, onSelectNav]);

  if (!isOpen) return null;

  function handleSelect(item: NavItem) {
    onSelectNav(item.key);
    onClose();
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center pt-16 sm:pt-24 p-4 bg-black/60 backdrop-blur-sm animate-in fade-in duration-150"
      onClick={onClose}
    >
      <div
        className="vnd-palette-panel w-full max-w-xl bg-surface-primary border border-border-default rounded-xl shadow-2xl overflow-hidden flex flex-col transition-all"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-label="Search pages or run a command"
      >
        {/* Header Search Input */}
        <div className="flex items-center px-3.5 py-1 border-b border-border-default gap-2">
          <Icon name="search" size={18} className="text-text-muted flex-none" />
          <input
            ref={inputRef}
            type="text"
            value={query}
            placeholder="Search pages or run a command..."
            className="w-full bg-transparent px-2 py-3 text-sm text-text-primary placeholder:text-text-muted border-none outline-none focus:ring-0"
            onChange={(e) => setQuery(e.target.value)}
          />
          <IconButton
            icon="x"
            label="Close search modal"
            size="sm"
            variant="ghost"
            onClick={onClose}
            className="flex-none text-text-muted hover:text-text-primary"
          />
        </div>

        {/* Scrollable Results List */}
        <div className="vnd-palette-results overflow-y-auto p-2 space-y-1">
          {filtered.length > 0 ? (
            <div>
              <div className="px-3 py-2 text-2xs font-semibold text-text-muted uppercase tracking-wider">
                Pages
              </div>
              <div className="space-y-1">
                {filtered.map((item, idx) => {
                  const isSelected = idx === selectedIndex;
                  return (
                    <button
                      key={item.key}
                      type="button"
                      onClick={() => handleSelect(item)}
                      onMouseEnter={() => setSelectedIndex(idx)}
                      className={`w-full flex items-center gap-3.5 px-3.5 py-2.5 rounded-lg text-sm font-medium transition-all text-left ${
                        isSelected
                          ? "bg-accent-default text-text-on-accent font-bold shadow-sm"
                          : "text-text-primary hover:bg-surface-secondary"
                      }`}
                    >
                      <Icon
                        name={item.icon}
                        size={18}
                        className={isSelected ? "text-text-on-accent flex-none" : "text-text-primary flex-none"}
                      />
                      <span className="flex-1 truncate">{item.label}</span>
                      <span
                        className={`text-xs ${
                          isSelected ? "text-text-on-accent opacity-80 font-medium" : "text-text-muted font-normal"
                        }`}
                      >
                        {item.group}
                      </span>
                    </button>
                  );
                })}
              </div>
            </div>
          ) : (
            <div className="p-8 text-center text-sm text-text-muted">
              No matching pages found for <span className="font-semibold text-text-primary">"{query}"</span>
            </div>
          )}
        </div>

        {/* Footer Shortcut Helper */}
        <div className="flex items-center justify-between px-4 py-2.5 border-t border-border-default bg-surface-secondary text-2xs text-text-muted select-none">
          <div className="flex items-center gap-3">
            <span>
              <kbd className="rounded border border-border-default bg-surface-primary px-1 py-0.5 font-mono text-2xs">
                ↑↓
              </kbd>{" "}
              Navigate
            </span>
            <span>
              <kbd className="rounded border border-border-default bg-surface-primary px-1 py-0.5 font-mono text-2xs">
                ↵
              </kbd>{" "}
              Select
            </span>
            <span>
              <kbd className="rounded border border-border-default bg-surface-primary px-1 py-0.5 font-mono text-2xs">
                ESC
              </kbd>{" "}
              Close
            </span>
          </div>
          <span className="font-mono text-2xs text-text-muted">Venom Gateway</span>
        </div>
      </div>
    </div>
  );
}
