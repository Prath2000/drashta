import { reactRouter } from "@react-router/dev/vite";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";

// `npm run dev` proxies /api to the Go server on 4455 (SSE passes through unbuffered).
// `npm run dev:mock` (mode "mock") runs with MSW and no Go server.
export default defineConfig(({ mode }) => ({
  plugins: [reactRouter()],
  resolve: {
    alias: { "~": fileURLToPath(new URL("./app", import.meta.url)) },
  },
  publicDir: mode === "mock" ? "mock-public" : "public",
  define: {
    "import.meta.env.DRASHTA_MOCK": JSON.stringify(mode === "mock"),
  },
  server: {
    proxy:
      mode === "mock"
        ? undefined
        : {
            "/api": { target: "http://localhost:4455", changeOrigin: true },
          },
  },
}));
