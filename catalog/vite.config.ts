import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Vercel-style catalog SPA
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    open: true,
    proxy: {
      // The catalog service is loopback-only and lives on its own port; the SPA
      // talks to it through this proxy so the browser sees one origin.
      '/v1': { target: 'http://127.0.0.1:8791', changeOrigin: false },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: true,
  },
});
