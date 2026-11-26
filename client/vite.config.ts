import { fileURLToPath, URL } from "node:url";

import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";
import { webNotices } from "./scripts/web-notices";

export default defineConfig({
  publicDir: false,
  plugins: [react(), webNotices()],
  resolve: {
    dedupe: [
      "react",
      "react-dom",
      "@base-ui/react",
      "@tanstack/react-query",
      "framer-motion",
      "@iquji-community/web-component",
      "@iquji-community/web-icons",
      "@iquji-community/web-theme",
    ],
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  server: {
    host: "127.0.0.1",
    port: 5174,
    strictPort: true,
    proxy: {
      "/api": {
        target: process.env.IQUOPS_API_PROXY_TARGET ?? "http://127.0.0.1:3001",
        changeOrigin: false,
        ws: true,
      },
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  test: {
    include: ["src/**/*.test.{ts,tsx}"],
    environment: "node",
    server: { deps: { inline: ["zod"] } },
  },
});
