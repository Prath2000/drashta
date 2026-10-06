import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";

// Vitest runs without the React Router vite plugin (it is for the app build only).
export default defineConfig({
  resolve: {
    alias: { "~": fileURLToPath(new URL("./app", import.meta.url)) },
  },
  define: {
    "import.meta.env.DRASHTA_MOCK": "false",
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./test/setup.ts"],
    include: ["app/**/*.test.{ts,tsx}", "test/**/*.test.{ts,tsx}"],
    css: false,
    environmentOptions: { jsdom: { url: "http://localhost:5173/" } },
  },
});
