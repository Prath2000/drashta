// Writes for the Tracker: patch a task (optimistic, with If-Match and the 409
// path), set a verdict, create a task. All of them confirm with a toast after
// a user action and refetch what they touched.
import { useCallback, useRef } from "react";
import { api, ApiError } from "~/lib/api";
import { invalidate, setCached, useApi } from "~/lib/query";
import { toast } from "~/lib/store";
import type { Snapshot, Task, TaskDetailResponse, TaskStatus, TrackerResponse, Verdict, VerdictResponse } from "~/lib/types";
import { STATUS_WORD, tkey } from "./model";

export const TRACKER_KEY = "/tracker";

const enc = encodeURIComponent;

/** Cache key of GET /projects/{p}/tasks/{id}. */
export const taskDetailKey = (p: string, id: string) => "/projects/" + enc(p) + "/tasks/" + enc(id);

/** Cache key of GET /projects/{p}/tasks/{id}/snapshots. */
export const snapshotsKey = (p: string, id: string) => taskDetailKey(p, id) + "/snapshots";

/** Fields a task edit can change (04 §6 PATCH). */
export interface TaskFields {
  status?: TaskStatus;
  title?: string;
  featureId?: string | null;
  assignee?: string;
  body?: string;
}

export interface PatchOptions {
  /** The open task detail, so its cache is updated at once too. */
  detail?: { key: string; data: TaskDetailResponse };
  /** The rev to send. Pass the one from the task detail answer, it is the freshest. */
  rev?: string;
}

/** Refetch everything a task change can touch. The SSE `tracker` event does the same for other tabs. */
export function refreshTask(projectId: string, taskId: string): void {
  invalidate(TRACKER_KEY);
  invalidate(taskDetailKey(projectId, taskId));
  invalidate("/resolve");
  invalidate("/notes");
  invalidate("/inbox");
}

function message(e: unknown, fallback: string): string {
  return e instanceof Error && e.message ? e.message : fallback;
}

// The last rev the server gave us for a task, so a second quick edit does not
// send an old rev and hit a pointless 409.
const lastRev = new Map<string, { base: string | undefined; next: string }>();
// Edits to one task go one after the other.
const queues = new Map<string, Promise<unknown>>();

/** Test helper: forget remembered revs and queues. */
export function resetActionState(): void {
  lastRev.clear();
  queues.clear();
}

function mergeTask(list: Task[], t: Pick<Task, "projectId" | "id">, patch: Partial<Task>): Task[] {
  return list.map((x) => (x.projectId === t.projectId && x.id === t.id ? { ...x, ...patch } : x));
}

/**
 * Returns `patchTask(task, fields, opts)`. It updates the caches at once
 * (optimistic), sends `PATCH` with `If-Match`, then refetches. On a 409 it drops
 * the local change, reloads and toasts "T-12 changed on disk, reloaded".
 * Resolves true when the server accepted the change.
 */
export function useTaskPatch() {
  const tr = useApi<TrackerResponse>(TRACKER_KEY);
  const trRef = useRef(tr.data);
  trRef.current = tr.data;

  return useCallback((task: Task, fields: TaskFields, opts: PatchOptions = {}): Promise<boolean> => {
    const k = tkey(task.projectId, task.id);
    // 1. optimistic
    const cur = trRef.current;
    if (cur) setCached<TrackerResponse>(TRACKER_KEY, { ...cur, tasks: mergeTask(cur.tasks, task, fields) });
    if (opts.detail) {
      setCached<TaskDetailResponse>(opts.detail.key, {
        ...opts.detail.data,
        task: { ...opts.detail.data.task, ...fields },
      });
    }
    // 2. send, after any earlier edit of the same task
    const send = async (): Promise<boolean> => {
      try {
        let rev = opts.rev ?? task.rev;
        const rec = lastRev.get(k);
        if (rec && (rev === undefined || rev === rec.base)) rev = rec.next;
        if (!rev) rev = (await api.task(task.projectId, task.id)).rev;
        const res = await api.updateTask(task.projectId, task.id, rev, fields);
        lastRev.set(k, { base: rev, next: res.rev });
        // Keep the new rev in the caches until the refetch lands.
        const now = trRef.current;
        if (now) setCached<TrackerResponse>(TRACKER_KEY, { ...now, tasks: mergeTask(now.tasks, task, { ...fields, rev: res.rev }) });
        refreshTask(task.projectId, task.id);
        toast(fields.status && Object.keys(fields).length === 1 ? task.id + " → " + STATUS_WORD[fields.status] : task.id + " saved");
        return true;
      } catch (e) {
        lastRev.delete(k);
        refreshTask(task.projectId, task.id);
        if (e instanceof ApiError && e.status === 409) toast(task.id + " changed on disk, reloaded");
        else toast(message(e, "could not save " + task.id), { error: true });
        return false;
      }
    };
    const job = (queues.get(k) ?? Promise.resolve()).catch(() => undefined).then(send);
    queues.set(k, job);
    return job;
  }, []);
}

const VERDICT_WORD: Record<Verdict, string> = {
  approved: "approved",
  changes: "changes requested",
  pending: "reset to pending",
};

/**
 * Returns `setVerdict(task, snapshot, verdict, note)`. Approving the latest
 * snapshot never moves the task (A4): the screens show a "Move to Done" chip.
 * Resolves with the server answer, or null after an error toast.
 */
export function useVerdict() {
  const tr = useApi<TrackerResponse>(TRACKER_KEY);
  const trRef = useRef(tr.data);
  trRef.current = tr.data;

  return useCallback(async (task: Task, snap: Pick<Snapshot, "version" | "title">, verdict: Verdict, note?: string): Promise<VerdictResponse | null> => {
    try {
      const res = await api.setVerdict(task.projectId, task.id, snap.version, verdict, note);
      const cur = trRef.current;
      if (cur) {
        const patch: Partial<Task> = { ...res.task };
        if (task.latestSnapshot?.version === snap.version) patch.latestSnapshot = { version: snap.version, verdict: res.snapshot.verdict ?? verdict };
        setCached<TrackerResponse>(TRACKER_KEY, { ...cur, tasks: mergeTask(cur.tasks, task, patch) });
      }
      refreshTask(task.projectId, task.id);
      toast((snap.title || task.title) + " v" + snap.version + " · " + VERDICT_WORD[verdict]);
      return res;
    } catch (e) {
      toast(message(e, "could not set the verdict"), { error: true });
      return null;
    }
  }, []);
}
