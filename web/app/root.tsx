import { useEffect } from "react";
import { Links, Meta, Outlet, Scripts, ScrollRestoration, isRouteErrorResponse } from "react-router";
import type { Route } from "./+types/root";
import { Shell } from "./components/shell/Shell";
import { startEvents } from "./lib/events";
import { startModeTracking } from "./lib/store";
import "./styles/app.css";

/** The Go server replaces this value in index.html with the real CSRF token (03 §8). */
export const TOKEN_PLACEHOLDER = "__DRASHTA_TOKEN__";

// Sets the `desktop` class before the first paint when the URL has ?desktop=1
// (Plash readout marker, D3), so the readout never flashes the browsing layout.
const modeBoot = `(function(){try{if(new URLSearchParams(location.search).get('desktop')==='1')document.documentElement.classList.add('desktop')}catch(e){}})();`;

export function Layout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" suppressHydrationWarning>
      <head>
        <meta charSet="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        <meta name="drashta-token" content={TOKEN_PLACEHOLDER} />
        <title>द्रष्टा</title>
        <script dangerouslySetInnerHTML={{ __html: modeBoot }} />
        <Meta />
        <Links />
      </head>
      <body>
        {children}
        <ScrollRestoration />
        <Scripts />
      </body>
    </html>
  );
}

export default function App() {
  useEffect(() => {
    const stopMode = startModeTracking();
    const ev = startEvents();
    return () => {
      ev.stop();
      stopMode();
    };
  }, []);
  return (
    <Shell>
      <Outlet />
    </Shell>
  );
}

export function HydrateFallback() {
  return <div className="dr-root" />;
}

export function ErrorBoundary({ error }: Route.ErrorBoundaryProps) {
  let message = "something went wrong.";
  let details = "";
  if (isRouteErrorResponse(error)) {
    message = error.status === 404 ? "nothing at this address." : error.statusText || message;
  } else if (error instanceof Error) {
    details = error.message;
  }
  return (
    <div className="dr-placeholder">
      <div className="dr-cartouche">
        <span className="dr-head" lang="sa">द्रष्टा</span>
        <span className="dr-empty">{message}</span>
        {details ? <span className="dr-meta">{details}</span> : null}
      </div>
    </div>
  );
}
