import * as React from "react";
import { Icon } from "../icons/Icon";

export interface SectionDeckItem {
  key: string;
  label: string;
  icon: string;
}

export interface SectionDeckSection {
  key: string;
  label: string;
  icon: string;
  items: readonly SectionDeckItem[];
}

export interface SectionDeckProps {
  sections: readonly SectionDeckSection[];
  activeKey: string;
  onNavigate: (key: string) => void;
  label?: string;
  className?: string;
}

export function SectionDeck(props: SectionDeckProps) {
  const { sections, activeKey, onNavigate, label = "Sections", className = "" } = props;
  const [openSection, setOpenSection] = React.useState<string | null>(null);
  const rootRef = React.useRef<HTMLElement>(null);
  const triggerRefs = React.useRef<Record<string, HTMLButtonElement | null>>({});
  const itemRefs = React.useRef<Array<HTMLButtonElement | null>>([]);
  const trayId = React.useId();
  const activeSection = sections.find((section) => section.items.some((item) => item.key === activeKey));
  const open = sections.find((section) => section.key === openSection);

  const close = React.useCallback((restoreFocus = false) => {
    const key = openSection;
    setOpenSection(null);
    if (restoreFocus && key) requestAnimationFrame(() => triggerRefs.current[key]?.focus());
  }, [openSection]);

  React.useEffect(() => {
    if (!openSection) return;
    const onPointerDown = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) close(false);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        close(true);
      }
    };
    document.addEventListener("pointerdown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);
    const raf = requestAnimationFrame(() => {
      const activeIndex = open?.items.findIndex((item) => item.key === activeKey) ?? -1;
      itemRefs.current[Math.max(activeIndex, 0)]?.focus();
    });
    return () => {
      cancelAnimationFrame(raf);
      document.removeEventListener("pointerdown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [activeKey, close, open, openSection]);

  function activateSection(section: SectionDeckSection) {
    if (section.items.length === 1) {
      onNavigate(section.items[0].key);
      close(false);
      return;
    }
    setOpenSection((current) => current === section.key ? null : section.key);
  }

  function navigate(key: string) {
    onNavigate(key);
    close(false);
  }

  return (
    <nav ref={rootRef} className={("vn-section-deck " + className).trim()} aria-label={label}>
      {open ? (
        <div id={trayId} className="vn-section-deck-tray" aria-label={`${open.label} pages`}>
          <div className="vn-section-deck-tray-head">
            <span className="vn-overline">{open.label}</span>
            <span className="vn-caption">Choose a destination</span>
          </div>
          <div className="vn-section-deck-items">
            {open.items.map((item, index) => (
              <button
                key={item.key}
                ref={(node) => { itemRefs.current[index] = node; }}
                type="button"
                className="vn-section-deck-item"
                aria-current={activeKey === item.key ? "page" : undefined}
                onClick={() => navigate(item.key)}
              >
                <span className="vn-section-deck-item-icon"><Icon name={item.icon} size={16} /></span>
                <span>{item.label}</span>
                <Icon name="chevron-right" size={14} className="vn-section-deck-item-chevron" />
              </button>
            ))}
          </div>
        </div>
      ) : null}

      <div className="vn-section-deck-bar">
        {sections.map((section) => {
          const isActive = activeSection?.key === section.key;
          const isOpen = openSection === section.key;
          return (
            <button
              key={section.key}
              ref={(node) => { triggerRefs.current[section.key] = node; }}
              type="button"
              className="vn-section-deck-trigger"
              aria-current={isActive ? "page" : undefined}
              aria-expanded={section.items.length > 1 ? isOpen : undefined}
              aria-controls={section.items.length > 1 && isOpen ? trayId : undefined}
              onClick={() => activateSection(section)}
            >
              <span className="vn-section-deck-trigger-icon"><Icon name={section.icon} size={18} /></span>
              <span>{section.label}</span>
            </button>
          );
        })}
      </div>
    </nav>
  );
}
