import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";

// Dev server proxies /api (REST and WebSocket) to the running
// kamado-api container on localhost:8080. In production the Go
// server serves the built static files alongside /api itself, so
// there is nothing to proxy.
export default defineConfig({
  plugins: [svelte()],
  server: {
    port: 5173,
    proxy: {
      "/api": {
        target: "http://localhost:8080",
        ws: true,
        changeOrigin: false,
      },
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    target: "es2022",
  },
});
