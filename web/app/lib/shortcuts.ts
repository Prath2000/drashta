// Keyboard shortcuts (02-ui-spec section 2.6).
//
// ONE global `keydown` listener lives in `useShortcuts()`. It is added once when
// the shell mounts and removed when it unmounts. Everything else only registers
// small things here, and the listener reads them when a key is pressed:
//
//   - Esc layers: useEscapeLayer("app" | "search" | "day" | "viewport", active, close).
//     One Esc closes ONE layer, in this order: the expanded server app ("app"),
//     then search, then the selected day, then the viewport.
//   - Actions: useShortcutAction("focusSearch" | "openNative", fn). The search box
//     and the viewport hand over what `/` and `o` should run.
//
// Rules the listener follows:
//   - Nothing happens in readout mode (the Plash wallpaper has no keyboard).
//   - Nothing happens while Ctrl, Meta or Alt is held.
//   - While a text field, textarea, select or contenteditable has focus, only Esc works.
//   - A key that something else already handled (defaultPrevented) is left alone.
//   - preventDefault is called only for a key that really did something.
//   - The arrow keys are NOT handled here. Home's lanes have their own roving
//     tabindex and arrow handling (Lane.tsx).
import { useEffect, useRef } from "react";
import { useLocation } from "react-router";
import { api, ApiError } from "./api";
import { findProject, useBoot } from "./boot";
import { getState, histMove, scanFailed, scanStarted, toast } from "./store";
import { projectFromPath, useGo, useItem } from "./url";

/** How long the second key of `g x` may take, in milliseconds. */
export const CHORD_MS = 1000;

/** What one Esc can close, in the order it is tried. */
export const ESC_ORDER = ["app", "search", "day", "viewport"] as const;
export type EscapeLayer = (typeof ESC_ORDER)[number];

/** Things other components run for a key. */
export type ShortcutAction = "focusSearch" | "openNative";

type Entry = { run: () => void };

const layers: Record<EscapeLayer, Entry[]> = { app: [], search: [], day: [], viewport: [] };
const actions: Record<ShortcutAction, Entry[]> = { focusSearch: [], openNative: [] };

function add(list: Entry[], entry: Entry): () => void {
  list.push(entry);
  return () => {
    const i = list.indexOf(entry);
    if (i >= 0) list.splice(i, 1);
  };
}

/**
 * Says "this layer is open, and Esc may close it" while `active` is true.
 * `close` may change on every render. If two components use the same layer, the
 * one that registered last is closed first.
 */
export function useEscapeLayer(layer: EscapeLayer, active: boolean, close: () => void): void {
  const latest = useRef(close);
  latest.current = close;
  useEffect(() => {
    if (!active) return;
    return add(layers[layer], { run: () => latest.current() });
  }, [layer, active]);
}

/** Hands a function to the shortcut for `name`. Pass null while the action is not available. */
export function useShortcutAction(name: ShortcutAction, fn: (() => void) | null): void {
  const latest = useRef(fn);
  latest.current = fn;
  const available = fn !== null;
  useEffect(() => {
    if (!available) return;
    return add(actions[name], { run: () => latest.current?.() });
  }, [name, available]);
}

function topOf(list: Entry[]): Entry | undefined {
  return list[list.length - 1];
}

/** Closes the first open layer. Returns true when one was closed. */
export function runEscape(): boolean {
  for (const layer of ESC_ORDER) {
    const top = topOf(layers[layer]);
    if (top) {
      top.run();
      return true;
    }
  }
  return false;
}

/** Runs an action if one is registered. Returns true when it ran. */
function runAction(name: ShortcutAction): boolean {
  const top = topOf(actions[name]);
  if (!top) return false;
  top.run();
  return true;
}

// ---------- key helpers ----------
const NON_TEXT_INPUT = new Set(["button", "checkbox", "radio", "range", "submit", "reset", "image", "file", "color"]);
const EDITABLE = '[contenteditable=""],[contenteditable="true"],[contenteditable="plaintext-only"],[role="textbox"]';
const MODIFIER_KEYS = new Set(["Shift", "Control", "Alt", "Meta", "AltGraph", "CapsLock", "Fn", "OS"]);

/** True when typing here would put characters in something (so shortcuts must stay out of the way). */
export function isTypingTarget(t: EventTarget | null): boolean {
  if (!t || typeof (t as Element).tagName !== "string") return false;
  const el = t as HTMLElement;
  const tag = el.tagName;
  if (tag === "TEXTAREA" || tag === "SELECT") return true;
  if (tag === "INPUT") return !NON_TEXT_INPUT.has((el as HTMLInputElement).type);
  if (el.isContentEditable) return true;
  return typeof el.closest === "function" && el.closest(EDITABLE) !== null;
}

/** The second key of a `g` chord and where it goes. */
const CHORD_KEYS = { h: "home", p: "project", t: "tracker", i: "inbox" } as const;

/**
 * Asks the server to rescan: one project, or all of them when no id is given.
 * The same steps as the refresh buttons. Does nothing while a scan already runs.
 */
export async function refreshScan(projectId?: string): Promise<void> {
  if (getState().scan.running) return;
  scanStarted(true);
  try {
    await api.refresh(projectId);
    // progress and the end arrive over SSE (`scan` events)
  } catch (e) {
    scanFailed();
    toast(e instanceof ApiError ? e.message : "refresh failed", { error: true });
  }
}

/**
 * Installs the one global keydown listener. Call it once, in the shell.
 * It must run inside the router (it reads the URL and navigates).
 */
export function useShortcuts(): void {
  const { item, openItem } = useItem();
  const go = useGo();
  const { pathname } = useLocation();
  const { data: boot } = useBoot();

  // The listener is added once; it reads the newest values through this ref.
  const latest = useRef({ item, openItem, go, pathname, boot });
  latest.current = { item, openItem, go, pathname, boot };

  useEffect(() => {
    let chord = false;
    let timer: ReturnType<typeof setTimeout> | null = null;
    const endChord = () => {
      chord = false;
      if (timer) clearTimeout(timer);
      timer = null;
    };

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.repeat || e.isComposing) return;
      if (e.ctrlKey || e.metaKey || e.altKey) return;
      if (getState().mode === "readout") return;
      const key = e.key;
      if (MODIFIER_KEYS.has(key)) return;

      if (key === "Escape") {
        endChord();
        if (runEscape()) e.preventDefault();
        return;
      }
      if (isTypingTarget(e.target)) {
        endChord();
        return;
      }

      const pending = chord;
      endChord();
      const now = latest.current;

      if (pending && Object.prototype.hasOwnProperty.call(CHORD_KEYS, key)) {
        e.preventDefault();
        switch (CHORD_KEYS[key as keyof typeof CHORD_KEYS]) {
          case "home":
            now.go.home();
            break;
          case "project": {
            // the last project you viewed, else the picker (same as the rail)
            const p = findProject(now.boot, getState().lastProjectId);
            if (p) now.go.project(p.id);
            else now.go.picker();
            break;
          }
          case "tracker":
            now.go.tracker();
            break;
          case "inbox":
            now.go.inbox();
            break;
        }
        return;
      }
      // A stray key after `g` only cancels the chord. It still does its own job below.

      switch (key) {
        case "g":
          e.preventDefault();
          chord = true;
          timer = setTimeout(endChord, CHORD_MS);
          return;
        case "/":
          if (runAction("focusSearch")) e.preventDefault();
          return;
        case "o":
          if (runAction("openNative")) e.preventDefault();
          return;
        case "[":
        case "]": {
          if (!now.item) return;
          const ref = histMove(key === "[" ? -1 : 1);
          if (ref) {
            e.preventDefault();
            now.openItem(ref, { record: false });
          }
          return;
        }
        case "r": {
          e.preventDefault();
          const pp = projectFromPath(now.pathname);
          void refreshScan(pp ? pp.project : undefined);
          return;
        }
      }
    };

    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      endChord();
    };
  }, []);
}
