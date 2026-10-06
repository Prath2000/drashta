# ADR 0006: Server-Sent Events with resync, no polling

- Status: Accepted for the push model (decision D5). The choice of SSE and its details come from `docs/spec/03-architecture.md` section 6 and go with the spec approval.
- Date: 2026-10-01

## Context

- D5: the page updates itself, with no reload timer and no constant animation.
- As the wallpaper, the page lives in a WKWebView behind all windows. WebKit may throttle timers or suspend it. A suspended page can lose its socket without noticing.
- Polling would cost CPU and battery all day and would still be late.

## Decision

- One stream per tab: `GET /api/events` (`text/event-stream`). A `: ping` comment every 25 s. The first event is `hello {now, counts}`.
- Events are small hints (`project`, `activity`, `servers`, `tracker`, `scan`, `counts`). The page refetches what it shows.
- The hub numbers events and keeps the last 1,000 in a ring buffer. A reconnect with `Last-Event-ID` replays the gap. If the id fell out of the ring, the hub sends `resync`.
- On `visibilitychange` to visible, the page calls `GET /api/state?since=<id>`. It gets the missed events or `{resync:true}`.
- The page tells the server when it is visible (`POST /api/presence`). lsof runs every 5 s with a visible tab and every 60 s otherwise.
- The server sends at most one `project` event per project per second.
- The page has no timers that its correctness depends on.

## Consequences

- Good: near-zero idle cost. Changes show in under 2 s.
- Good: SSE needs no extra library, passes through the Vite proxy, and `EventSource` reconnects by itself.
- Cost: a silent socket death while suspended. The visibility catch-up covers it. A 24 h soak test checks sleep and long covered periods (risks R2, R3).
- Cost: hints cause extra fetches. The 1 s coalescing keeps this small.
