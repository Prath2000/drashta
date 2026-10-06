import type { Config } from "@react-router/dev/config";

// SPA mode: no server rendering. The Go binary serves build/client.
export default {
  appDirectory: "app",
  ssr: false,
} satisfies Config;
