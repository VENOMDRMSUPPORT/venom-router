import { designSystemCheck } from "./design-system-check";

// Placeholder surface for P2a-DS-001 (workspace + package wiring only).
// Global styles, theme/density application, and the real UI shell land in DS-002/DS-003/UI-001.
export default function App() {
  return (
    <p>
      @venom/design-system resolved — default theme: {designSystemCheck.defaultTheme} (
      {designSystemCheck.themes.length} themes registered)
    </p>
  );
}
