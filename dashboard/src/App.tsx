import SmokeInventory from "./SmokeInventory";

// P2a-DS-004 supersedes DS-002's hand-rolled <select>-based switcher with
// the real @venom/design-system ThemeSwitcher/DensityToggle components,
// composed (with a primitive and a domain component) in SmokeInventory.
// The real UI shell (navigation, production surfaces) is later work
// (UI-001+).
export default function App() {
  return <SmokeInventory />;
}
