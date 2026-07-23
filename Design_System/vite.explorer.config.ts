import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import fs from "node:fs";
import path from "node:path";

/**
 * Explorer build — the canonical, Vite-compiled component explorer (replaces the old
 * browser-Babel fallback in storybook/card-runtime.js). Root stays the package root so
 * every card's relative `../styles.css` / `../../src/index` import and every hub iframe's
 * relative `src="../foundations/…"` resolves identically in dev and in the built output.
 * Every specimen HTML (storybook hub, foundations, component cards, state matrices, the
 * ui_kit console) is a real Rollup entry: a missing import or broken export in any one of
 * them fails `npm run build:explorer` outright — there is no per-card try/catch fallback.
 */
function findHtmlEntries(): Record<string, string> {
  const entries: Record<string, string> = {};
  const add = (rel: string) => {
    const key = rel.replace(/[\\/]/g, "_").replace(/\.html$/, "");
    entries[key] = path.resolve(__dirname, rel);
  };

  add("storybook/index.html");
  add("ui_kits/venom-console/index.html");

  for (const f of fs.readdirSync(path.resolve(__dirname, "foundations"))) {
    if (f.endsWith(".html")) add("foundations/" + f);
  }
  for (const f of fs.readdirSync(path.resolve(__dirname, "states"))) {
    if (f.endsWith(".html")) add("states/" + f);
  }
  for (const dir of fs.readdirSync(path.resolve(__dirname, "components"))) {
    const full = path.resolve(__dirname, "components", dir);
    if (!fs.statSync(full).isDirectory()) continue;
    for (const f of fs.readdirSync(full)) {
      if (f.endsWith(".card.html")) add("components/" + dir + "/" + f);
    }
  }
  return entries;
}

export default defineConfig({
  root: __dirname,
  base: "./",
  plugins: [react()],
  build: {
    outDir: "dist-explorer",
    emptyOutDir: true,
    rollupOptions: {
      input: findHtmlEntries(),
    },
  },
  server: {
    host: "127.0.0.1",
    port: 4173,
    strictPort: false,
    fs: {
      allow: [__dirname],
    },
  },
  preview: {
    host: "127.0.0.1",
    port: 4173,
    strictPort: false,
  },
});
