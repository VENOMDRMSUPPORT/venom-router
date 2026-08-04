import AuthGate from "./auth/AuthGate";

// The whole app is fronted by the owner-auth gate (P2b-UI-002: first-run
// setup / login / session-expiry handling); AuthGate mounts the real UI shell
// (AppShell) once a session exists.
//
// SmokeInventory (P2a-DS-004) is NO LONGER mounted here — it was the interim
// authenticated-area placeholder until the shell landed. It is retained
// deliberately as a TEST-ONLY surface: SmokeInventory.test.tsx renders it to
// prove the real @venom/design-system components still render across the full
// theme x density matrix, which no production surface exercises exhaustively.
// Its only importer is that test, and that is intentional.
export default function App() {
  return <AuthGate />;
}
