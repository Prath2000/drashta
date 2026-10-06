import { useCallback, useMemo, useState } from "react";
import { api, get } from "~/lib/api";
import { useBoot } from "~/lib/boot";
import { invalidate, setCached, useApi } from "~/lib/query";
import { toast, useStore } from "~/lib/store";
import type { DayEvent, InboxResponse, Note, TrackerResponse } from "~/lib/types";
import { useGo, useItem } from "~/lib/url";
import "./inbox.css";
import { ActivityFeed, type KindFilter } from "./ActivityFeed";
import { NeedsYou, type DirtyProject } from "./NeedsYou";
import { NotesPanel } from "./NotesPanel";
import { errText, inboxKey, useInboxChain, type NoteFilter } from "./useInbox";
import { asOfLabel, dedupe, fullRef, noteKey, noteOpenRef } from "./util";

/** A page of notes this long may have a next page. */
const NOTES_PAGE_HINT = 50;

const byDateDesc = (a: Note, b: Note) => Date.parse(b.date) - Date.parse(a.date) || (noteKey(a) < noteKey(b) ? 1 : -1);
const byTsDesc = (a: DayEvent, b: DayEvent) => Date.parse(b.ts) - Date.parse(a.ts);

/**
 * The Inbox screen (02-ui-spec §7, prototype Inbox.dc.html).
 * Left: Notes with Open / Resolved / All. Right: "Needs you" and the Activity feed.
 */
export function InboxScreen() {
  const [tab, setTab] = useState<NoteFilter>("open");
  const [kind, setKind] = useState<KindFilter>("all");
  const [allWanted, setAllWanted] = useState(false);
  // Optimistic resolve / reopen: note key -> the resolved flag we asked for.
  const [pending, setPending] = useState<Record<string, boolean>>({});

  const boot = useBoot();
  const counts = useStore((s) => s.counts);
  const go = useGo();
  const { openItem } = useItem();
  const tracker = useApi<TrackerResponse>("/tracker");

  const openChain = useInboxChain("open");
  const resolvedChain = useInboxChain("resolved");
  const allChain = useInboxChain("all", allWanted);
  const chains = { open: openChain, resolved: resolvedChain, all: allChain };

  // The feed also holds commits and sessions. events.ts refetches /inbox on
  // every project, activity, session and tracker event, so nothing more is
  // needed here (a second refetch on each new /boot fetched /inbox twice).

  // ---------- projects, time ----------
  const projects = boot.data?.projects;
  const { hidden, names } = useMemo(() => {
    const h = new Set<string>();
    const n = new Map<string, string>();
    for (const p of projects ?? []) {
      n.set(p.id, p.name);
      if (p.hidden) h.add(p.id);
    }
    return { hidden: h, names: n };
  }, [projects]);
  const projectName = useCallback((id: string) => names.get(id) ?? id, [names]);
  const nowMs = useMemo(() => {
    const t = boot.data ? Date.parse(boot.data.now) : NaN;
    return Number.isNaN(t) ? Date.now() : t;
  }, [boot.data]);

  // ---------- notes ----------
  const allNotes = useMemo(() => {
    const merged = dedupe([...openChain.notes, ...resolvedChain.notes, ...allChain.notes], noteKey);
    return merged
      .map((n) => {
        const want = pending[noteKey(n)];
        return want === undefined || want === n.resolved ? n : { ...n, resolved: want };
      })
      .sort(byDateDesc);
  }, [openChain.notes, resolvedChain.notes, allChain.notes, pending]);
  const nOpen = useMemo(() => allNotes.filter((n) => !n.resolved).length, [allNotes]);
  const nResolved = allNotes.length - nOpen;
  const shownNotes = useMemo(() => allNotes.filter((n) => (tab === "all" ? true : tab === "open" ? !n.resolved : n.resolved)), [allNotes, tab]);

  // The cursor is shared with the feed, and the server sends the notes complete
  // unless there are many. A chain "may have more notes" only when it has a next
  // cursor and a full page of notes. Only then is the live counter worth more
  // than what we loaded, and only then does "Load more" show under the notes.
  const mayHaveMore = (c: { next: string | null; notes: Note[] }) => !!c.next && c.notes.length >= NOTES_PAGE_HINT;
  const openNumber = mayHaveMore(openChain) ? Math.max(counts.openNotes, nOpen) : nOpen;
  const noteCounts: Record<NoteFilter, string | number | null> = {
    open: openChain.data ? openNumber : counts.openNotes,
    resolved: resolvedChain.data ? nResolved : null,
    all: openChain.data && resolvedChain.data ? openNumber + nResolved : null,
  };
  const tabChain = chains[tab];
  const notesHasMore = mayHaveMore(tabChain);

  const pick = (t: NoteFilter) => {
    if (t === "all") setAllWanted(true);
    setTab(t);
  };

  const toggle = useCallback(
    async (n: Note) => {
      const k = noteKey(n);
      const want = !n.resolved;
      setPending((p) => ({ ...p, [k]: want }));
      const drop = () =>
        setPending((p) => {
          const { [k]: _gone, ...rest } = p;
          return rest;
        });
      try {
        await api.updateNote(n.projectId, n.id, { resolved: want });
      } catch (e) {
        drop();
        toast("Could not " + (want ? "resolve " : "reopen ") + n.id + ": " + errText(e), { error: true });
        return;
      }
      toast(n.id + (want ? " resolved" : " reopened"));
      openChain.patchNote(k, want);
      resolvedChain.patchNote(k, want);
      allChain.patchNote(k, want);
      // Put the server's first pages in the cache, then drop the optimistic flag.
      const keys = [inboxKey("open"), inboxKey("resolved"), ...(allWanted ? [inboxKey("all")] : [])];
      await Promise.all(
        keys.map((key) =>
          get<InboxResponse>(key).then(
            (d) => setCached(key, d),
            () => undefined,
          ),
        ),
      );
      drop();
      invalidate("/notes");
      invalidate("/resolve");
      invalidate("/boot");
      invalidate("/projects/" + encodeURIComponent(n.projectId));
    },
    [openChain, resolvedChain, allChain, allWanted],
  );

  // ---------- needs you ----------
  const dirty: DirtyProject[] = useMemo(
    () =>
      (projects ?? [])
        .filter((p) => !p.hidden && (p.dirtyCount ?? 0) > 0)
        .map((p) => ({ id: p.id, name: p.name, count: p.dirtyCount ?? 0 })),
    [projects],
  );
  const tasks = tracker.data?.tasks;
  const tasksInReview = useMemo(() => (tasks ?? []).filter((t) => t.status === "review" && !hidden.has(t.projectId)).length, [tasks, hidden]);

  // ---------- activity ----------
  const feed = useMemo(() => openChain.feed.filter((e) => !hidden.has(e.projectId)).sort(byTsDesc), [openChain.feed, hidden]);
  const kindCounts = useMemo(() => {
    const c = { all: feed.length, commit: 0, session: 0, snapshot: 0, task: 0, note: 0 } as Record<KindFilter, number>;
    for (const e of feed) if (e.kind in c) c[e.kind as KindFilter]++;
    return c;
  }, [feed]);
  const shownFeed = useMemo(() => (kind === "all" ? feed : feed.filter((e) => e.kind === kind)), [feed, kind]);

  const openRef = (ref: string) => openItem(fullRef(ref, ""));

  return (
    <div className="ix-root" data-testid="inbox-root">
      <div className="ix-header">
        <div className="ix-cartouche">
          <div>
            <h1 className="ix-title">Inbox</h1>
          </div>
        </div>
        <span className="ix-sub">everything addressed to you</span>
        <div className="ix-spacer" />
        <span className="ix-asof">{asOfLabel(nowMs)}</span>
      </div>
      <div className="ix-main">
        <NotesPanel
          tab={tab}
          onTab={pick}
          notes={shownNotes}
          counts={noteCounts}
          loading={tabChain.loading}
          error={tabChain.error}
          onRetry={tabChain.reload}
          hasMore={notesHasMore}
          loadingMore={tabChain.loadingMore}
          moreError={tabChain.moreError}
          onMore={tabChain.loadMore}
          projectName={projectName}
          nowMs={nowMs}
          onOpen={(n) => openItem(noteOpenRef(n))}
          onToggle={toggle}
        />
        <div className="ix-right">
          <NeedsYou
            awaitingReview={counts.awaitingReview}
            tasksInReview={tasksInReview}
            openNotes={openChain.data ? openNumber : counts.openNotes}
            dirty={dirty}
            unfiled={openChain.unfiled}
            proposals={openChain.proposals}
            tasks={tasks ?? []}
            tasksReady={!!tasks}
            projectName={projectName}
            onSnapshots={() => go.tracker("review")}
            onTasks={() => go.tracker("review")}
            onNotes={() => setTab("open")}
            onDirty={(id) => go.project(id, "commits")}
            onOpenRef={openRef}
          />
          <ActivityFeed
            kind={kind}
            onKind={setKind}
            events={shownFeed}
            counts={kindCounts}
            partial={!!openChain.next}
            loading={openChain.loading}
            error={openChain.error}
            onRetry={openChain.reload}
            hasMore={!!openChain.next}
            loadingMore={openChain.loadingMore}
            moreError={openChain.moreError}
            onMore={openChain.loadMore}
            nowMs={nowMs}
            projectName={projectName}
            onOpen={(e) => (e.ref ? openItem(fullRef(e.ref, e.projectId)) : undefined)}
          />
        </div>
      </div>
    </div>
  );
}
