import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The bundle is compiled straight into the Go embed directory so `go build`
// always ships the frontend that matches the backend on disk.
const backend = "http://127.0.0.1:8787";

export default defineConfig({
  base: "./",
  plugins: [react()],
  build: {
    outDir: "../internal/webui/dist",
    emptyOutDir: true,
    target: "chrome110",
    sourcemap: false,
    assetsDir: "assets",
    chunkSizeWarningLimit: 900,
  },
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      "/ws": { target: backend, ws: true },
      "/api": { target: backend, changeOrigin: true },
    },
  },
});
