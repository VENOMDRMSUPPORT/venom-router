import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Dev mode: npm run dev proxies /api → http://127.0.0.1:8081;
// run venom (or venom serve) alongside for a live dashboard against the real control plane.
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      "/api": { target: "http://127.0.0.1:8081", changeOrigin: true },
    },
  },
});
