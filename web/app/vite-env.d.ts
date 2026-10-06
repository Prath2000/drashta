/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** True in `npm run dev:mock` (MSW, no Go server). */
  readonly DRASHTA_MOCK: boolean;
}
