import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// In dev, `npm run dev` serves the app on :5173 and forwards /api to a
// running `goharness web` on :8787. Keep the Host header (changeOrigin off)
// so the server's same-origin check sees the page's own host.
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      "/api": { target: "http://127.0.0.1:8787", changeOrigin: false },
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
});
