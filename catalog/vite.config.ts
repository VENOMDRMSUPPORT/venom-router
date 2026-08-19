import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Vercel-style catalog SPA
export default defineConfig({
  plugins: [react()],
  server: {
    // PORT lets a second instance run beside the live one instead of fighting it.
    port: Number(process.env.PORT ?? 5173),
    open: false,
    strictPort: false,
    proxy: {
      // The catalog service is loopback-only and lives on its own port; the SPA
      // talks to it through this proxy so the browser sees one origin.
      // Overridable so a verification instance on another port can be inspected
      // without stopping the live one.
      '/v1': { target: process.env.CATALOG_API ?? 'http://127.0.0.1:8791', changeOrigin: false },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: true,
  },
});
