// MSW in the browser, for `npm run dev:mock` (no Go server needed).
// Screen handlers go first so they override the shared defaults (MSW uses the first match).
// (The inbox registers its own handlers from routes/inbox.tsx.)
import { setupWorker } from "msw/browser";
import { handlers } from "./handlers";
import { trackerHandlers } from "./tracker";
import { viewportHandlers } from "./viewport";

export const worker = setupWorker(...trackerHandlers, ...viewportHandlers, ...handlers);
