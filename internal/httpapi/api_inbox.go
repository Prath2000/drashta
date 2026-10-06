package httpapi

// GET /api/inbox?filter=open|resolved|all&cursor=&limit=
//
// Reply: {notes, unfiledSnapshots, proposals, feed, next}.
//
//   - notes: notes of every project (hidden ones too) with the title of the item each one is on.
//     Newest first. filter: open (default), resolved or all.
//   - unfiledSnapshots: images in <project>/.drashta/snapshots/inbox/ that no task owns yet.
//   - proposals: "this commit says it fixes T-12, close the task?" (close_task). The page asks the user.
//   - feed: the activity of all projects that are not hidden, newest first.
//   - next: cursor for the next page, or null.
//
// Paging: ?limit= (default 50, at most 200) is the page size of the feed. Notes come complete on the first
// page (up to a ceiling of 500; the page relies on this). One cursor serves both lists: a later page holds
// the next feed events and, only for more than 500 notes, the next notes. unfiledSnapshots and proposals
// are only sent on the first page (no cursor); later pages have empty lists for them.
//
// A feed item of kind "snapshot" carries the verdict word (pending, approved, changes) in context.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Prath2000/drashta/internal/authored"
	"github.com/Prath2000/drashta/internal/pathguard"
	"github.com/Prath2000/drashta/internal/store"
)

// ahInboxNotePage is how many notes one page holds. The page expects the notes complete on the first
// page, so this is a safety ceiling, not a page size to tune. It is a variable only so a test can lower it.
var ahInboxNotePage = 500

const (
	// ahInboxDefaultLimit and ahInboxMaxLimit are for the activity feed (?limit=).
	ahInboxDefaultLimit = 50
	ahInboxMaxLimit     = 200
	ahInboxMaxUnfiled   = 200
	// ahProposalWindow is how far back a closing commit is looked for.
	ahProposalWindow = 90 * 24 * time.Hour
	ahProposalMax    = 50
)

type ahInboxNote struct {
	ID             string   `json:"id"`
	Ref            string   `json:"ref"`
	ProjectID      string   `json:"projectId"`
	Text           string   `json:"text"`
	Author         string   `json:"author"`
	Date           string   `json:"date"`
	Resolved       bool     `json:"resolved"`
	ResolvedAt     string   `json:"resolvedAt,omitempty"`
	Line           *int     `json:"line,omitempty"`
	X              *float64 `json:"x,omitempty"`
	Y              *float64 `json:"y,omitempty"`
	Anchor         string   `json:"anchor,omitempty"`
	ReplyTo        string   `json:"replyTo,omitempty"`
	TargetTitle    string   `json:"targetTitle"`
	TargetSubtitle string   `json:"targetSubtitle,omitempty"`
}

type ahUnfiled struct {
	Name      string `json:"name"`
	ProjectID string `json:"projectId"`
	Path      string `json:"path"` // relative to the project folder; usable with /api/projects/{p}/raw?path=
	Size      int64  `json:"size"` // bytes
	Date      string `json:"date"` // modified time
}

type ahProposal struct {
	Kind      string `json:"kind"`
	TaskRef   string `json:"taskRef"`
	CommitRef string `json:"commitRef"`
}

type ahInbox struct {
	Notes            []ahInboxNote `json:"notes"`
	UnfiledSnapshots []ahUnfiled   `json:"unfiledSnapshots"`
	Proposals        []ahProposal  `json:"proposals"`
	Feed             []ahEvent     `json:"feed"`
	Next             *string       `json:"next"`
}

// ahInboxCursor is the decoded cursor: N notes already sent, and the (ts, id) of the last feed event sent.
type ahInboxCursor struct {
	N int   `json:"n"`
	T int64 `json:"t"`
	I int64 `json:"i"`
}

func ahEncodeCursor(c ahInboxCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func ahDecodeCursor(s string) (ahInboxCursor, bool) {
	var c ahInboxCursor
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || json.Unmarshal(b, &c) != nil || c.N < 0 || c.T < 0 || c.I < 0 {
		return ahInboxCursor{}, false
	}
	return c, true
}

func (h *homeAPI) registerInbox() {
	h.s.Handle(http.MethodGet, "/api/inbox", h.handleInbox, false)
}

func (h *homeAPI) handleInbox(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	filter := "open"
	if q.Has("filter") {
		filter = q.Get("filter")
	}
	switch filter {
	case "open", "resolved", "all":
	default:
		BadRequest(w, "filter must be open, resolved or all")
		return
	}
	limit := ahInboxDefaultLimit
	if q.Has("limit") {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil || n < 1 || n > ahInboxMaxLimit {
			BadRequest(w, "limit must be a number from 1 to "+strconv.Itoa(ahInboxMaxLimit))
			return
		}
		limit = n
	}
	var cur ahInboxCursor
	first := true
	if c := q.Get("cursor"); c != "" {
		var ok bool
		if cur, ok = ahDecodeCursor(c); !ok {
			BadRequest(w, "bad cursor")
			return
		}
		first = false
	}

	out := ahInbox{Notes: []ahInboxNote{}, UnfiledSnapshots: []ahUnfiled{}, Proposals: []ahProposal{}, Feed: []ahEvent{}}

	// notes (one extra row tells us whether there is a next page)
	notes, err := h.d.Store.HomeInboxNotes(ctx, filter, cur.N, ahInboxNotePage+1)
	if err != nil {
		h.fail(w, "inbox notes", err)
		return
	}
	moreNotes := len(notes) > ahInboxNotePage
	if moreNotes {
		notes = notes[:ahInboxNotePage]
	}
	titles := map[string][2]string{}
	for _, n := range notes {
		out.Notes = append(out.Notes, h.inboxNote(ctx, n, titles))
	}

	// feed
	feed, err := h.d.Store.HomeInboxFeed(ctx, cur.T, cur.I, limit+1)
	if err != nil {
		h.fail(w, "inbox feed", err)
		return
	}
	moreFeed := len(feed) > limit
	if moreFeed {
		feed = feed[:limit]
	}
	next := ahInboxCursor{N: cur.N + len(notes), T: cur.T, I: cur.I}
	out.Feed = h.apiEvents(ctx, feed)
	for _, e := range feed {
		next.T, next.I = e.TS.Unix(), e.ID
	}

	if first {
		projects, err := h.d.Store.ProjectList(ctx)
		if err != nil {
			h.fail(w, "inbox projects", err)
			return
		}
		out.UnfiledSnapshots = h.unfiled(ctx, projects)
		props, err := h.d.Store.HomeInboxProposals(ctx, h.now().Add(-ahProposalWindow), ahProposalMax)
		if err != nil {
			h.fail(w, "inbox proposals", err)
			return
		}
		for _, p := range props {
			out.Proposals = append(out.Proposals, ahProposal{
				Kind:      "close_task",
				TaskRef:   p.ProjectID + ":task:" + p.TaskID,
				CommitRef: p.ProjectID + ":commit:" + p.CommitID,
			})
		}
	}

	if moreNotes || moreFeed {
		s := ahEncodeCursor(next)
		out.Next = &s
	}
	WriteJSON(w, http.StatusOK, out)
}

// inboxNote builds one note for the reply. titles caches resolver answers for this request.
func (h *homeAPI) inboxNote(ctx context.Context, n store.Note, titles map[string][2]string) ahInboxNote {
	ref := ahQualify(n.ProjectID, n.Ref)
	o := ahInboxNote{
		ID: n.ID, Ref: ref, ProjectID: n.ProjectID, Text: n.Body, Author: n.Author,
		Date: ahAuthoredTime(n.Created, h.loc), Resolved: n.Resolved,
		Line: n.Line, X: n.X, Y: n.Y, Anchor: n.Anchor, ReplyTo: n.ReplyTo,
	}
	if n.Resolved {
		o.ResolvedAt = ahAuthoredTime(n.ResolvedAt, h.loc)
	}
	o.TargetTitle = ref
	if h.d.Resolver != nil && ref != "" {
		t, ok := titles[ref]
		if !ok {
			title, sub := h.d.Resolver.Title(ctx, ref)
			t = [2]string{title, sub}
			titles[ref] = t
		}
		if t[0] != "" {
			o.TargetTitle = t[0]
		}
		o.TargetSubtitle = t[1]
	}
	return o
}

// ahTaskPrefix finds drop-folder names like "T-12--anything.png".
var ahTaskPrefix = regexp.MustCompile(`^(T-[0-9]+)--`)

// unfiled lists the images in each visible project's snapshots/inbox folder that no task has claimed.
// A name with a task prefix is the indexer's job to file, so it is listed only when that task does not exist.
// Everything goes through pathguard, and only plain files with an image extension count.
func (h *homeAPI) unfiled(ctx context.Context, projects []store.Project) []ahUnfiled {
	exts := map[string]bool{}
	for _, e := range authored.ImageExts {
		exts[e] = true
	}
	type item struct {
		u  ahUnfiled
		mt time.Time
	}
	var items []item
	for _, p := range projects {
		if p.Hidden || p.Path == "" {
			continue
		}
		dir, err := pathguard.Resolve(p.Path, ".drashta/snapshots/inbox")
		if err != nil {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.Type().IsRegular() { // no folders, no symlinks
				continue
			}
			name := e.Name()
			ext := strings.ToLower(strings.TrimPrefix(ahExt(name), "."))
			if !exts[ext] || strings.HasPrefix(name, ".") {
				continue
			}
			if m := ahTaskPrefix.FindStringSubmatch(name); m != nil {
				if ok, err := h.d.Store.HomeHasTask(ctx, p.ID, m[1]); err != nil || ok {
					continue
				}
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			items = append(items, item{
				u:  ahUnfiled{Name: name, ProjectID: p.ID, Path: ".drashta/snapshots/inbox/" + name, Size: info.Size(), Date: ahTime(info.ModTime(), h.loc)},
				mt: info.ModTime(),
			})
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].mt.After(items[j].mt) })
	if len(items) > ahInboxMaxUnfiled {
		items = items[:ahInboxMaxUnfiled]
	}
	out := make([]ahUnfiled, 0, len(items))
	for _, it := range items {
		out = append(out, it.u)
	}
	return out
}

// ahExt is the extension of a file name with its dot ("a.png" gives ".png"). Unlike filepath.Ext it
// gives "" for a name like ".png" (a hidden file), which the caller skips anyway.
func ahExt(name string) string {
	i := strings.LastIndexByte(name, '.')
	if i <= 0 {
		return ""
	}
	return name[i:]
}
