import { useEffect, useState } from "react";
import { InboxScreen } from "~/components/inbox/InboxScreen";

// In `npm run dev:mock` the inbox needs a few extra mock endpoints (paging, unfiled
// snapshots, proposals). They live in mocks/inbox.ts and are added to the
// browser worker here, so no shared mock file changes.
// `=== true` on purpose: a real build turns it into `false === true` and drops the
// dynamic imports; Vitest makes it the string "false", which must not count as on.
let mockReady: Promise<void> | null = null;
function ensureMocks(): Promise<void> {
  if (import.meta.env.DRASHTA_MOCK === true) {
    mockReady ??= Promise.all([import("~/mocks/browser"), import("~/mocks/inbox")]).then(([b, m]) => m.registerInboxMocks(b.worker));
    return mockReady;
  }
  return Promise.resolve();
}

export default function InboxRoute() {
  const [ready, setReady] = useState(import.meta.env.DRASHTA_MOCK !== true);
  useEffect(() => {
    if (ready) return;
    let live = true;
    void ensureMocks().then(() => live && setReady(true));
    return () => {
      live = false;
    };
  }, [ready]);
  return ready ? <InboxScreen /> : null;
}
