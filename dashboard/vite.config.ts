import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// This config executes under Node, but the project deliberately carries no
// @types/node dependency — declare the one Node global it reads.
declare const process: { env: Record<string, string | undefined> };

// Dev mode: npm run dev proxies /api → the control plane. The target defaults
// to the standalone bind (127.0.0.1:8081); the tray's Development section
// overrides it via VENOM_DEV_API_TARGET to point at the supervised dev
// backend (127.0.0.1:8082) so dev traffic never touches production state.
const apiTarget = process.env.VENOM_DEV_API_TARGET ?? "http://127.0.0.1:8081";

export default defineConfig({
  plugins: [react()],
  server: {
    // @venom/design-system is a file:../Design_System dependency, so its
    // icon CSS (icons.css) references SVGs that live OUTSIDE this package —
    // vite dev rewrites those urls to /@fs/ absolute-path requests. The
    // default fs.allow list is the workspace root, and this repo has no npm
    // workspace at the top level, so vite only allowed dashboard/ and
    // answered every /@fs/ icon request with 403. Allow the repo root
    // ("../" from this config's root) so the design-system assets are
    // servable in dev. Production is unaffected either way: `vite build`
    // bundles the assets into dist/.
    fs: { allow: [".."] },
    proxy: {
      "/api": { target: apiTarget, changeOrigin: true },
    },
  },
});
