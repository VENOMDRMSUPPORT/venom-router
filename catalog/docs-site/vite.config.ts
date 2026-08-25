import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig(({ mode }) => ({
  root: 'docs-site',
  plugins: [react()],
  base: process.env.DOCS_BASE_PATH || '/',
  build: {
    outDir: '../dist-docs',
    emptyOutDir: true,
    sourcemap: mode !== 'production',
  },
  resolve: {
    alias: { '@docs': '/docs-site/src' },
  },
}));
