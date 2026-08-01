import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { resolve } from "node:path";

/**
 * Library build — compiles the package's public entry points (src/*.ts) into
 * dist/*.{mjs,cjs}. Type declarations are generated separately by
 * `tsc -p tsconfig.build.json` (see package.json `build:types`).
 */
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "dist",
    emptyOutDir: false,
    lib: {
      entry: {
        index: resolve(__dirname, "src/index.ts"),
        tokens: resolve(__dirname, "src/tokens.ts"),
        themes: resolve(__dirname, "src/themes.ts"),
        density: resolve(__dirname, "src/density.ts"),
        customizer: resolve(__dirname, "src/customizer.ts"),
        icons: resolve(__dirname, "src/icons.ts"),
        primitives: resolve(__dirname, "src/primitives.ts"),
        domain: resolve(__dirname, "src/domain.ts"),
        tailwind: resolve(__dirname, "src/tailwind.ts"),
      },
      formats: ["es", "cjs"],
      fileName: (format, entryName) => `${entryName}.${format === "es" ? "mjs" : "cjs"}`,
    },
    rollupOptions: {
      external: ["react", "react-dom", "react/jsx-runtime"],
    },
    sourcemap: true,
  },
});
