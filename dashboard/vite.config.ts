import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// This config executes under Node, but the project deliberately carries no
// @types/node dependency — declare the one Node global it reads.
declare const process: { env: Record<string, string | undefined> };

// Dev mode: npm run dev proxies /api → the control plane (127.0.0.1:8081)
// where the single production database (venom.db) lives.
const apiTarget = process.env.VENOM_DEV_API_TARGET ?? "http://127.0.0.1:8081";

export default defineConfig({
  plugins: [react()],
  server: {
    // Pin the dev frontend to the ONE canonical dev port so it is identical
    // however it is launched (`npm run dev`, `task dev`, or the tray) — never
    // Vite's default 5173. strictPort fails loudly instead of silently drifting
    // to another port if 8088 is taken.
    host: "127.0.0.1",
    port: 8088,
    strictPort: true,
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
    watch: {
      // chokidar native events are unreliable when files are edited by
      // external tools (e.g. Claude Code, scripts). Polling guarantees
      // HMR fires regardless of how a file was written.
      usePolling: true,
    },
  },
});
