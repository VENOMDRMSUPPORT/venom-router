import { useEffect, useState } from "react";
import { Icon } from "@venom/design-system/icons";
import CommandPalette from "./CommandPalette";

export interface SearchBarProps {
  onNavigate?: (navKey: string) => void;
}

export default function SearchBar(props: SearchBarProps) {
  const [isOpen, setIsOpen] = useState(false);

  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setIsOpen((prev) => !prev);
      }
    }
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, []);

  function handleSelectNav(key: string) {
    if (props.onNavigate) {
      props.onNavigate(key);
    }
  }

  return (
    <>
      <button
        type="button"
        onClick={() => setIsOpen(true)}
        aria-label="Open search pages or run a command modal"
        className="flex items-center gap-2 rounded-lg border border-border-default bg-surface-secondary px-2.5 py-1.5 text-xs text-text-muted hover:text-text-primary hover:border-border-strong transition-colors w-28 sm:w-48 md:w-64 max-w-full min-w-0 text-left select-none"
      >
        <Icon name="search" size={14} className="flex-none text-text-muted" />
        <span className="w-full min-w-0 truncate text-xs text-text-muted">
          Search pages or run a command...
        </span>
        <kbd className="hidden sm:inline-block flex-none rounded border border-border-default bg-surface-primary px-1.5 py-0.5 font-mono text-[10px] text-text-muted select-none">
          ⌘K
        </kbd>
      </button>

      <CommandPalette
        isOpen={isOpen}
        onClose={() => setIsOpen(false)}
        onSelectNav={handleSelectNav}
      />
    </>
  );
}
