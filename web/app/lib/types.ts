// Hand-written types for every JSON response of the drashta API
// (docs/spec/04-data-model.md §6). All times are ISO strings. Day keys are "YYYY-MM-DD".
//
// When the Go side adds a field, add it here as optional first.

export type ProjectId = string;
export type IsoTime = string;
export type DayKey = string;

export type TaskStatus = "backlog" | "todo" | "in_progress" | "review" | "done";
export type Verdict = "pending" | "approved" | "changes";
export type Vcs = "git" | "fossil";
export type RefType =
  | "file"
  | "image"
  | "commit"
  | "session"
  | "server"
  | "snapshot"
  | "task"
  | "note"
  | "feature"
  | "milestone"
  | "project"
  | "doc";

/** Object form of a ref (04 §2.4). The string form is `[<project>:]<type>:<id>[#frag]`. */
export interface RefObj {
  type: RefType;
  projectId: ProjectId;
  id: string;
  frag?: string;
}

/** API error body: `{error:{code,message}}`. */
export interface ApiErrorBody {
  error: { code: string; message: string };
}

// ---------- counts ----------
export interface Counts {
  liveServers: number;
  openTasks: number;
  awaitingReview: number;
  openNotes: number;
}

// ---------- /boot ----------
export interface ProjectSummary {
  id: ProjectId;
  name: string;
  group: string;
  path: string;
  vcs: Vcs[] | Vcs | null;
  branch: string | null;
  lastChangeAt: IsoTime | null;
  hidden: boolean;
  surveyed: boolean;
  /** Uncommitted paths (the `±n` on a lane). Optional until the API sends it. */
  dirtyCount?: number;
  /** Files indexed in the project. 0 means an empty directory (Home shows "empty directory"). */
  fileCount?: number;
  description?: string;
  stack?: string[];
  remote?: string | null;
}

export interface Boot {
  now: IsoTime;
  statuses: TaskStatus[];
  profiles: string[];
  author: string;
  counts: Counts;
  projects: ProjectSummary[];
  /** Last finished scan time, if the server knows it. */
  lastScanAt?: IsoTime | null;
  /** Only when there are no projects: why, and what to change in config.toml. */
  notice?: string;
}

export interface Session {
  token: string;
}

// ---------- /home ----------
export interface DayCell {
  files: number;
  commits: number;
  linesPlus: number;
  linesMinus: number;
  sessions: number;
  snapshots: number;
  notes: number;
  serverMinutes: number;
}

export interface Lane {
  projectId: ProjectId;
  days: DayCell[];
  live: { servers: number[]; sessions: string[] };
  lastChangeAt: IsoTime | null;
}

export interface Home {
  range: 7 | 30;
  days: DayKey[];
  lanes: Lane[];
}

// ---------- /day ----------
export type EventKind = "commit" | "session" | "snapshot" | "note" | "task" | "file" | "server";

export interface DayEvent {
  ts: IsoTime;
  kind: EventKind;
  ref: string;
  label: string;
  projectId: ProjectId;
  context?: string;
}

export interface DayTotals {
  files: number;
  commits: number;
  linesPlus: number;
  linesMinus: number;
  sessions: number;
  snapshots: number;
  notes: number;
}

export interface DayResponse {
  date: DayKey;
  events: DayEvent[];
  totals: DayTotals;
  /** Paths touched that day (top 20), plus how many more. */
  files?: string[];
  moreFiles?: number;
}

// ---------- notes ----------
export interface Note {
  id: string;
  ref: string;
  projectId: ProjectId;
  text: string;
  author: string;
  date: IsoTime;
  resolved: boolean;
  line?: number;
  x?: number;
  y?: number;
  anchor?: string;
  replyTo?: string;
  /** Title of the item the note is on (inbox only). */
  targetTitle?: string;
}

export interface NotesResponse {
  notes: Note[];
}

// ---------- /inbox ----------
export interface InboxProposal {
  kind: "close_task";
  taskRef: string;
  commitRef: string;
}

export interface InboxResponse {
  notes: Note[];
  unfiledSnapshots: { name: string; projectId: ProjectId; path: string; date: IsoTime }[];
  proposals: InboxProposal[];
  feed: DayEvent[];
  next: string | null;
}

// ---------- project ----------
export interface DirtyFile {
  path: string;
  state: string;
}

export interface Server {
  key: string;
  port: number;
  pid: number;
  name: string;
  cmd: string;
  cwd: string;
  projectId: ProjectId | null;
  kind: "dev" | "helper" | string;
  title: string;
  firstSeen?: IsoTime;
  lastSeen?: IsoTime;
}

export interface Milestone {
  id: string;
  projectId: ProjectId;
  title: string;
  due: DayKey | null;
  featureIds: string[];
  rev?: string;
}

export interface Feature {
  id: string;
  projectId: ProjectId;
  title: string;
  summary?: string;
  milestoneId?: string | null;
  rev?: string;
}

export interface ProjectDetail {
  project: ProjectSummary;
  overview: {
    readme: { path: string; text: string } | null;
    counts: { files: number; commits: number; sessions: number; openTasks: number; openNotes: number };
    dirty: DirtyFile[];
    servers: Server[];
    milestones: Milestone[];
  };
}

export interface ActivityDay {
  day: DayKey;
  files: number;
  commits: number;
  linesPlus: number;
  linesMinus: number;
  sessions: number;
  snapshots: number;
  notes: number;
  serverMinutes?: number;
}

export interface ActivityResponse {
  days: ActivityDay[];
}

export interface TreeEntry {
  path: string;
  dir: boolean;
  size: number | null;
  mtime: IsoTime | null;
  hasChildren: boolean;
  /** Skipped folders (node_modules, .git, dist, target, venv) are listed but collapsed. */
  collapsed?: boolean;
  dirty?: string;
}

export interface TreeResponse {
  dir: string;
  entries: TreeEntry[];
}

export interface FileResponse {
  path: string;
  size: number;
  mtime: IsoTime;
  kind: string;
  isDoc: boolean;
  lines: number;
  from: number;
  to: number;
  text: string;
  truncated: boolean;
  notes: Note[];
}

export interface Commit {
  id: string;
  vcs: Vcs;
  author: string;
  ts: IsoTime;
  msg: string;
  plus: number | null;
  minus: number | null;
  nfiles: number;
  tasks: string[];
}

export interface CommitsResponse {
  commits: Commit[];
  next: string | null;
}

export interface CommitDetailResponse {
  commit: Commit;
  body: string;
  files: { path: string; plus: number | null; minus: number | null }[];
  patch: string;
}

export interface DirtyResponse {
  files: DirtyFile[];
  diff: string;
}

export interface SessionSummary {
  id: string;
  profile: string;
  start: IsoTime;
  end: IsoTime | null;
  title: string;
  turns: number;
  kb: number;
  tools: Record<string, number>;
  edited: string[];
  tasks: string[];
  /** Why the session counts for this project (cwd, subfolder, mentioned). */
  reason: string;
  cwd?: string;
}

export interface SessionsResponse {
  sessions: SessionSummary[];
  next: string | null;
}

export type TurnRole = "user" | "claude" | "tool";

export interface Turn {
  i: number;
  role: TurnRole;
  ts: IsoTime;
  text: string;
  tool?: string;
}

export interface SessionDetailResponse {
  session: SessionSummary;
  turns: Turn[];
  next: string | null;
}

export interface Doc {
  path: string;
  title: string;
  mtime: IsoTime;
  lines: number;
}

export interface DocsResponse {
  docs: Doc[];
}

export interface GithubResponse {
  prs: { number: number; title: string; state: string; url: string; author?: string; updatedAt?: IsoTime }[];
  issues: { number: number; title: string; state: string; url: string; updatedAt?: IsoTime }[];
  runs: { id: number; name: string; status: string; conclusion: string | null; url: string; createdAt?: IsoTime }[];
  fetchedAt: IsoTime;
  /** Set when gh is missing, not logged in, or the remote is not GitHub. A normal 200 response. */
  unavailable?: { reason: string };
}

// ---------- servers ----------
export interface ServersResponse {
  active: Server[];
  recent: Server[];
}

/**
 * GET /servers/embed?port=N: can the live server be shown in an iframe?
 * 404 when the port is not live. `http` false = not a web page at all (a helper
 * that does not speak HTTP). `http` true and `frameable` false = the app sends
 * X-Frame-Options / CSP frame-ancestors, so only the last capture can be shown;
 * `reason` says why. `title` is the page title ("" when unknown).
 */
export interface EmbedInfo {
  http: boolean;
  frameable: boolean;
  reason: string;
  title: string;
}

export interface OkResponse {
  ok: boolean;
}

/** Plash desktop modes (contrib/desktop/README.md). "blank" is the normal macOS wallpaper. */
export type DesktopMode = "browse" | "wallpaper" | "blank";

/** GET and POST /api/desktop. `mode` is "" when unknown. */
export interface DesktopResponse {
  available: boolean;
  mode: DesktopMode | "";
  modes: DesktopMode[];
}

export interface RestartResponse {
  ok: boolean;
  log: string;
}

// ---------- tracker ----------
export interface Task {
  id: string;
  projectId: ProjectId;
  featureId: string | null;
  milestoneId?: string | null;
  title: string;
  status: TaskStatus;
  assignee: string;
  created: IsoTime;
  updated?: IsoTime;
  body?: string;
  rev?: string;
  latestSnapshot?: { version: number; verdict: Verdict } | null;
  linkCount?: number;
  openNotes?: number;
}

export interface TrackerResponse {
  milestones: Milestone[];
  features: Feature[];
  tasks: Task[];
}

export interface TaskLink {
  ref: string;
  source: "manual" | "auto" | string;
  reason?: string;
  resolved: { title: string; subtitle: string; date: IsoTime | null } | null;
}

export interface Snapshot {
  version: number;
  title: string;
  url: string;
  captured: IsoTime;
  author: string;
  commit: string | null;
  verdict: Verdict;
  reviewNote: string;
  notes: Note[];
  path?: string;
  taskId?: string;
  projectId?: ProjectId;
}

export interface TaskLogEntry {
  ts: IsoTime;
  author: string;
  text: string;
}

export interface TaskDetailResponse {
  task: Task;
  rev: string;
  links: TaskLink[];
  snapshots: Snapshot[];
  notes: Note[];
  log: TaskLogEntry[];
}

export interface TaskWriteResponse {
  task: Task;
  rev: string;
}

export interface LinksResponse {
  links: TaskLink[];
}

export interface SnapshotsResponse {
  snapshots: Snapshot[];
}

export interface SnapshotResponse {
  snapshot: Snapshot;
}

export interface VerdictResponse {
  snapshot: Snapshot;
  task: Task;
}

// ---------- notes, refs, search, actions ----------
export interface NoteResponse {
  note: Note;
}

export interface NativeAction {
  action: string;
  label: string;
  command: string;
}

export interface ResolveResponse {
  ref: string;
  type: RefType;
  projectId: ProjectId;
  title: string;
  subtitle: string;
  icon: string;
  date: IsoTime | null;
  obj: unknown;
  nativeLabel: string;
  nativeActions: NativeAction[];
  notes: Note[];
  tasks: Task[];
  /** True when only a light summary exists for this item. */
  light?: boolean;
}

export type SearchKind =
  | "project"
  | "file"
  | "doc"
  | "commit"
  | "session"
  | "server"
  | "task"
  | "snapshot"
  | "note";

export interface SearchHit {
  kind: SearchKind;
  ref: string;
  projectId: ProjectId;
  title: string;
  snippet: string;
  ts: IsoTime | null;
}

export interface SearchResponse {
  hits: SearchHit[];
}

export interface RefreshResponse {
  started: boolean;
}

export type OpenAction = "default" | "editor" | "finder" | "terminal" | "safari" | "resume" | "app";

export interface OpenResponse {
  ok: boolean;
  ran: string;
}

export interface Settings {
  hiddenFolders: string[];
  author: string;
  editorCmd: string;
}

// ---------- live updates ----------
/** `/api/state?since=` answer: missed events, or a full resync. */
export interface StateResponse {
  resync: boolean;
  head?: string | number;
  events?: LiveEvent[];
}

export interface LiveEvent {
  id?: string;
  type: string;
  data: unknown;
}

export interface ScanEventData {
  projectId?: ProjectId;
  scope?: string;
  state: "running" | "done" | "idle";
  at?: IsoTime;
}

export interface HelloEventData {
  now: IsoTime;
  counts: Counts;
  /** Connection id; send it back in POST /api/presence. */
  conn?: string;
}

/** `project` event. `what:"all"` (with `parts`) means several parts changed: refetch the whole project. */
export interface ProjectEventData {
  projectId: ProjectId;
  what: "files" | "commits" | "dirty" | "sessions" | "docs" | "all" | string;
  parts?: string[];
}
