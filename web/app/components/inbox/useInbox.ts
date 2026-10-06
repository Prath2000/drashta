// Data for the Inbox: GET /inbox?filter=open|resolved|all&cursor= (04 §6).
//
// One "chain" per notes filter. The first page comes from the shared query
// cache (so SSE `invalidate("/inbox")` refreshes it). "Load more" fetches the
// next page with the cursor and keeps it in local state. The cursor is shared
// by the notes list and the activity feed: a page may add notes and feed events,
// and both lists skip what they already have.
import { useCallback, useMemo, useRef, useState } from "react";
import { api, ApiError } from "~/lib/api";
import { useApi } from "~/lib/query";
import type { DayEvent, InboxProposal, InboxResponse, Note } from "~/lib/types";
import { dedupe, eventKey, noteKey } from "./util";

export type NoteFilter = "open" | "resolved" | "all";

export const inboxKey = (filter: NoteFilter): string => "/inbox?filter=" + filter;

export type Unfiled = InboxResponse["unfiledSnapshots"][number];

export interface Chain {
  /** First page answer (undefined while loading). */
  data: InboxResponse | undefined;
  loading: boolean;
  error: unknown;
  notes: Note[];
  feed: DayEvent[];
  unfiled: Unfiled[];
  proposals: InboxProposal[];
  /** Cursor for the next page, or null when everything is loaded. */
  next: string | null;
  loadingMore: boolean;
  moreError: string | null;
  loadMore: () => void;
  /** Refetch the first page. */
  reload: () => void;
  /** Changes `resolved` on the loaded later pages (the first page is refetched instead). */
  patchNote: (key: string, resolved: boolean) => void;
}

const EMPTY_PAGES: InboxResponse[] = [];
const EMPTY_UNFILED: Unfiled[] = [];
const EMPTY_PROPOSALS: InboxProposal[] = [];

export function useInboxChain(filter: NoteFilter, enabled = true): Chain {
  const first = useApi<InboxResponse>(enabled ? inboxKey(filter) : null);
  const [pages, setPages] = useState<InboxResponse[]>(EMPTY_PAGES);
  const [loadingMore, setLoadingMore] = useState(false);
  const [moreError, setMoreError] = useState<string | null>(null);
  const busy = useRef(false);

  const data = first.data;
  const next = pages.length ? pages[pages.length - 1].next ?? null : data?.next ?? null;

  const loadMore = useCallback(() => {
    if (!next || busy.current) return;
    busy.current = true;
    setLoadingMore(true);
    setMoreError(null);
    api
      .inbox(filter, next)
      .then((r) => setPages((p) => [...p, r]))
      .catch((e: unknown) => setMoreError(e instanceof ApiError || e instanceof Error ? e.message : "request failed"))
      .finally(() => {
        busy.current = false;
        setLoadingMore(false);
      });
  }, [filter, next]);

  const patchNote = useCallback((key: string, resolved: boolean) => {
    setPages((all) =>
      all.map((p) => ({
        ...p,
        notes: (p.notes ?? []).map((n) => (noteKey(n) === key ? { ...n, resolved } : n)),
      })),
    );
  }, []);

  const merged = useMemo(() => {
    const all = data ? [data, ...pages] : [];
    return {
      notes: dedupe(
        all.flatMap((p) => p.notes ?? []),
        noteKey,
      ),
      feed: dedupe(
        all.flatMap((p) => p.feed ?? []),
        eventKey,
      ),
    };
  }, [data, pages]);

  return {
    data,
    loading: enabled && !data && !first.error,
    error: first.error,
    notes: merged.notes,
    feed: merged.feed,
    unfiled: data?.unfiledSnapshots ?? EMPTY_UNFILED,
    proposals: data?.proposals ?? EMPTY_PROPOSALS,
    next,
    loadingMore,
    moreError,
    loadMore,
    reload: first.reload,
    patchNote,
  };
}

/** Message text of an error from the API client. */
export function errText(e: unknown): string {
  return e instanceof Error ? e.message : "request failed";
}
