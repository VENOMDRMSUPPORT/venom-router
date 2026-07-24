import "@venom/design-system/styles.css";
// P2a-DS-003: Tailwind utilities last, so they can override DS component
// classes when composed on the same element (07 §2.3).
import "./tailwind.css";

import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { initializeThemeRuntime } from "./theme-runtime";

// Apply the package defaults (venom-dark / comfortable) before first
// paint. Real (server-persisted) theme/density restore lands in P2b.
initializeThemeRuntime();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
