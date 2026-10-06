# ADR 0005: One Go binary serves an embedded React SPA on 127.0.0.1:4455

- Status: Accepted (decisions S1 and S2).
- Date: 2026-10-01

## Context

- drashta must start at login, need no Node at run time, and give Plash a plain URL.
- acme already works this way (Go server, launchd plist, port 4444). That pattern is proven on this Mac.
- The UI needs a real component model: a seismograph, a viewport with many body types, a tracker board.

## Decision

- Go backend, React Router v7 in SPA mode, built by Vite.
- The build output (`web/build/client`) is embedded with `//go:embed all:build/client`. Unknown non-`/api` GETs return `index.html`. Hashed assets are cached as immutable. `index.html` is `no-cache`.
- One process listens on `127.0.0.1:4455`. Never `0.0.0.0`.
- Dev mode: `drashta -dev` plus Vite on 5173, which proxies `/api` (including SSE). `-dev` only adds the Vite Origin to the allowed list.
- The CSRF token is injected into `index.html` as a meta tag, and read from `GET /api/session` in dev.
- A launchd plist (`in.drashta`, `KeepAlive`, `RunAtLoad`) starts it at login.

## Consequences

- Good: one file to build, install and restart. No CORS. No Node at run time.
- Good: the page and the API share one origin, so the guard is simple.
- Cost: building needs Node and Go. A UI change needs a rebuilt binary (dev mode avoids this).
- Cost: if another program holds port 4455, drashta logs the reason and exits 1.
- Cost: loopback only, so there is no remote access by design.
