// Venom Router Vite configuration.
//
// TanStack Start (SSR React) + Tailwind v4, assembled from the official plugins
// directly. There is no shared meta-config wrapper: every plugin below is listed
// explicitly so the build pipeline is auditable.
//
// Plugin responsibilities:
//   - tanstackStart()  — SSR, file-based routing, route generation
//   - viteReact()      — React Fast Refresh + JSX
//   - tailwindcss()    — Tailwind v4 PostCSS/Vite pipeline
//   - tsConfigPaths()  — resolves the "@" path alias from tsconfig.json
//   - import.meta.env.VITE_* is injected manually (loadEnv) below.
import { defineConfig, loadEnv } from "vite";
import { tanstackStart } from "@tanstack/react-start/plugin/vite";
import viteReact from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import tsConfigPaths from "vite-tsconfig-paths";

export default defineConfig(({ mode }) => {
  // Inject VITE_* env vars as import.meta.env.<NAME> in the client bundle.
  const envDefine: Record<string, string> = {};
  for (const [key, value] of Object.entries(loadEnv(mode, process.cwd(), "VITE_"))) {
    envDefine[`import.meta.env.${key}`] = JSON.stringify(value);
  }

  return {
    define: envDefine,
    // Match the build CSS pipeline in dev so the preview is honest about
    // Lightning CSS transforms (e.g. vendor prefix collapsing).
    css: { transformer: "lightningcss" },
    resolve: {
      alias: {
        "@": `${process.cwd()}/src`,
      },
      // Prevent duplicate copies of React/TanStack from breaking hooks/context.
      dedupe: [
        "react",
        "react-dom",
        "react/jsx-runtime",
        "react/jsx-dev-runtime",
        "@tanstack/react-query",
        "@tanstack/query-core",
      ],
    },
    optimizeDeps: {
      include: [
        "react",
        "react-dom",
        "react-dom/client",
        "react/jsx-runtime",
        "react/jsx-dev-runtime",
      ],
      ignoreOutdatedRequests: true,
    },
    plugins: [
      tailwindcss(),
      tsConfigPaths({ projects: ["./tsconfig.json"] }),
      tanstackStart({
        // Redirect TanStack Start's bundled server entry to src/server.ts (our SSR wrapper).
        // nitro/vite builds from this entry.
        server: { entry: "server" },
        importProtection: {
          behavior: "error",
          client: {
            files: ["**/server/**"],
            specifiers: ["server-only"],
          },
        },
      }),
      viteReact(),
    ],
    server: { port: 8084, strictPort: true },
    preview: { port: 8084, strictPort: true },
  };
});
