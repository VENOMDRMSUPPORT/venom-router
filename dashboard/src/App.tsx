import AuthGate from "./auth/AuthGate";

// P2a-DS-004 introduced SmokeInventory to prove the real
// @venom/design-system components render across the theme x density
// matrix. P2b-UI-002 fronts the whole app with the owner-auth gate
// (first-run setup / login / session-expiry handling) — SmokeInventory
// now lives behind it, inside AuthenticatedArea, as this task's minimal
// authenticated-area placeholder. The real UI shell (navigation,
// production surfaces) is later work (UI-001+).
export default function App() {
  return <AuthGate />;
}
