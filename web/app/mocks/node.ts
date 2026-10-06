// MSW in Node, for Vitest. Started in test/setup.ts.
import { setupServer } from "msw/node";
import { handlers } from "./handlers";

export const server = setupServer(...handlers);
