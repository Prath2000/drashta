import { useEffect, useRef } from "react";
import { bodyFor } from "~/components/viewport/registry";
import { api, ApiError, qs } from "~/lib/api";
import { findProject, useBoot } from "~/lib/boot";
import { KIND_COLOR, STATUS_COLOR } from "~/lib/format";
import { invalidate, useApi } from "~/lib/query";
import { formatRef, parseRef } from "~/lib/refs";
import { useEscapeLayer, useShortcutAction } from "~/lib/shortcuts";
import { histMove, histPush, toast, useStore } from "~/lib/store";
import type { OpenAction, ResolveResponse, TrackerResponse } from "~/lib/types";
import { useItem } from "~/lib/url";

/**
 * The right-hand preview drawer (02-ui-spec §2.4). It opens from `?item=<ref>`.
 * This file is the frame only: header, Open ↗, history, task links and the
 * missing state. The body comes from components/viewport/registry.ts.
 * Esc closes it as the last layer of the global Esc order (lib/shortcuts.ts), and the
 * `o` key runs the Open ↗ button.
 */
export function Viewport() {
  const { item, openItem, closeItem } = useItem();
  const hist = useStore((s) => s.hist);
  const asideRef = useRef<HTMLElement>(null);

  // Esc closes the viewport only after the search panel and the selected day.
  useEscapeLayer("viewport", !!item, closeItem);

  // A ref that arrives by URL (reload, link) still goes into the history.
  useEffect(() => {
    if (item) histPush(item);
  }, [item]);

  // Focus the drawer when a new item opens, so Esc works at once.
  useEffect(() => {
    if (item && asideRef.current && !asideRef.current.contains(document.activeElement)) {
      asideRef.current.focus({ preventScroll: true });
    }
  }, [item]);

  if (!item) return null;

  const move = (d: -1 | 1) => {
    const r = histMove(d);
    if (r) openItem(r, { record: false });
  };
  const pos = hist.list.length > 1 ? hist.idx + 1 + "/" + hist.list.length : "";

  return (
    <aside ref={asideRef} aria-label="Viewport" className="dr-drawer" tabIndex={-1} data-testid="viewport">
      <ViewportInner
        refStr={item}
        pos={pos}
        backDisabled={hist.idx <= 0}
        fwdDisabled={hist.idx >= hist.list.length - 1}
        onBack={() => move(-1)}
        onFwd={() => move(1)}
        onClose={closeItem}
        onOpenItem={openItem}
      />
    </aside>
  );
}

interface InnerProps {
  refStr: string;
  pos: string;
  backDisabled: boolean;
  fwdDisabled: boolean;
  onBack: () => void;
  onFwd: () => void;
  onClose: () => void;
  onOpenItem: (ref: string) => void;
}

function ViewportInner(p: InnerProps) {
  const refObj = parseRef(p.refStr);
  const { data: boot } = useBoot();
  const res = useApi<ResolveResponse>(refObj ? "/resolve" + qs({ ref: p.refStr }) : null);
  const missing = !refObj || (res.error instanceof ApiError && res.error.status === 404);
  const failed = !missing && !!res.error;
  const r = res.data;
  const projectId = r?.projectId ?? refObj?.projectId ?? "";
  const projName = findProject(boot, projectId)?.name ?? projectId;
  const type = r?.type ?? refObj?.type ?? "item";
  const color = missing ? "var(--muted)" : KIND_COLOR[type] ?? "var(--muted)";
  const nativeCmd = r?.nativeActions?.[0]?.command ?? r?.nativeLabel ?? "";

  const openNative = async () => {
    try {
      // Run the action the button is named after (the first native action), not the server default.
      const out = await api.open(p.refStr, r?.nativeActions?.[0]?.action as OpenAction | undefined);
      toast(r?.nativeLabel ? r.nativeLabel + " ↗" : "opened: " + out.ran);
    } catch (e) {
      toast(e instanceof Error ? e.message : "open failed", { error: true });
    }
  };

  // The `o` key runs the same function as the Open ↗ button.
  useShortcutAction("openNative", r && !missing ? openNative : null);

  const Body = r ? bodyFor(r.type) : null;

  return (
    <>
      <div className="dr-drawer-head">
        <div className="dr-row">
          <button type="button" className="dr-icon-btn" aria-label="Back" onClick={p.onBack} disabled={p.backDisabled}>
            ←
          </button>
          <button type="button" className="dr-icon-btn" aria-label="Forward" onClick={p.onFwd} disabled={p.fwdDisabled}>
            →
          </button>
          {p.pos ? <span className="dr-histpos">{p.pos}</span> : null}
          <span className="dr-chip" style={{ borderColor: color, color }}>
            {type}
          </span>
          {r?.light ? <span className="dr-tag">light</span> : null}
          <span className="dr-drawer-proj">{projName}</span>
          <div style={{ flex: 1 }} />
          {r && !missing ? (
            <button type="button" className="dr-open" title={nativeCmd} onClick={openNative}>
              {(r.nativeLabel || "Open") + " ↗"}
            </button>
          ) : null}
          <button type="button" className="dr-icon-btn" aria-label="Close viewport" onClick={p.onClose} style={{ fontSize: 12 }}>
            ✕
          </button>
        </div>
        <div className="dr-drawer-title">
          <span className="ico" style={{ color }}>
            {missing ? "◯" : r?.icon ?? ""}
          </span>
          <h2>{missing ? (refObj?.id.split("/").pop() || "Unknown item") : r?.title ?? (res.loading ? "…" : "")}</h2>
        </div>
        <div className="dr-drawer-sub">
          {missing ? "Not found in the latest scan" : failed ? "could not load this item." : r?.subtitle ?? ""}
        </div>
        {r && !missing ? <TasksRow refStr={p.refStr} resolved={r} onOpenItem={p.onOpenItem} /> : null}
      </div>
      <div className="dr-drawer-body dr-mount">
        {missing ? (
          <Missing refStr={p.refStr} />
        ) : r && Body && refObj ? (
          <Body key={p.refStr} refStr={p.refStr} refObj={refObj} resolved={r} />
        ) : null}
      </div>
    </>
  );
}

function Missing({ refStr }: { refStr: string }) {
  return (
    <div className="dr-missing" data-testid="viewport-missing">
      <svg width="120" height="28" viewBox="0 0 120 28" fill="none" stroke="var(--hairline)" strokeWidth="1.4" aria-hidden="true">
        <path d="M0 14 H120" />
        <path d="M20 10 V18 M40 10 V18 M60 10 V18 M80 10 V18 M100 10 V18" />
      </svg>
      <div className="big">Nothing to show here.</div>
      <div className="why">
        This item isn't in the latest scan — it may have moved, been deleted, or sits in a folder drashta keeps collapsed.
      </div>
      <div className="dr-meta" style={{ fontSize: 10, letterSpacing: "0.08em" }}>
        {refStr}
      </div>
    </div>
  );
}

/** Linked task chips, "not linked", and the `Link to task…` select. */
function TasksRow({ refStr, resolved, onOpenItem }: { refStr: string; resolved: ResolveResponse; onOpenItem: (ref: string) => void }) {
  const pid = resolved.projectId;
  const tracker = useApi<TrackerResponse>(pid ? "/tracker" + qs({ project: pid }) : null);
  const linked = resolved.tasks ?? [];
  const linkedIds = new Set(linked.map((t) => t.id));
  const selfTask = resolved.type === "task" ? parseRef(refStr)?.id : undefined;
  const options = (tracker.data?.tasks ?? []).filter((t) => t.projectId === pid && !linkedIds.has(t.id) && t.id !== selfTask);
  const link = async (taskId: string) => {
    const t = options.find((x) => x.id === taskId);
    try {
      await api.linkTask(pid, taskId, refStr);
      invalidate("/resolve");
      invalidate("/tracker");
      toast("Linked to " + taskId + (t ? " · " + t.title : ""));
    } catch (e) {
      toast(e instanceof Error ? e.message : "link failed", { error: true });
    }
  };
  const short = (s: string, n: number) => (s.length > n ? s.slice(0, n) + "…" : s);
  return (
    <div className="dr-tasks-row">
      <span className="dr-label" style={{ marginRight: 2 }}>
        Tasks
      </span>
      {linked.map((t) => (
        <button
          key={t.id}
          type="button"
          className="dr-task-chip"
          title={t.title}
          onClick={() => onOpenItem(formatRef({ type: "task", projectId: t.projectId, id: t.id }))}
        >
          <span className="dr-dot" style={{ background: STATUS_COLOR[t.status] ?? "var(--muted)" }} />
          {t.id + " · " + short(t.title, 34)}
        </button>
      ))}
      {linked.length === 0 ? <span className="dr-meta">not linked</span> : null}
      {options.length > 0 ? (
        <label style={{ display: "flex", alignItems: "center" }}>
          <span className="sr-only">Link this item to a task</span>
          <select
            className="dr-link-select"
            value=""
            onChange={(e) => {
              if (e.target.value) void link(e.target.value);
            }}
          >
            <option value="">+ Link to task…</option>
            {options.map((t) => (
              <option key={t.id} value={t.id}>
                {t.id + " · " + short(t.title, 42)}
              </option>
            ))}
          </select>
        </label>
      ) : null}
    </div>
  );
}
