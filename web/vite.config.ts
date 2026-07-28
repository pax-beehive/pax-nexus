import { defineConfig } from "vitest/config";
import { loadEnv } from "vite";
import react from "@vitejs/plugin-react";

// The backend is mapped to host port 58080 by compose.yaml; override with
// VITE_API_ORIGIN when pointing at a different environment. In production the
// portal is served same-origin with the API, so no proxy is needed.
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, ".", "");
  const apiOrigin = env.VITE_API_ORIGIN ?? "http://localhost:58080";
  return {
    plugins: [react()],
    server: {
      port: 5173,
      proxy: {
        "/v1": {
          target: apiOrigin,
          changeOrigin: false,
        },
      },
    },
    test: {
      environment: "node",
      include: ["tests/**/*.test.ts", "tests/**/*.dom.test.tsx"],
      // Lib tests stay on the node environment; page-level DOM smoke tests
      // (tests/**/*.dom.test.tsx) run in jsdom.
      environmentMatchGlobs: [["tests/**/*.dom.test.tsx", "jsdom"]],
    },
  };
});
