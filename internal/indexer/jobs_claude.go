package indexer

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Prath2000/drashta/internal/live"
	"github.com/Prath2000/drashta/internal/probe/claude"
	"github.com/Prath2000/drashta/internal/store"
)

// sessMem is what the indexer remembers about one session family between parses,
// so an append parse can continue from the stored counts.
type sessMem struct {
	sess claude.Session
	text string // redacted user texts for search
}

// maxSessionDays caps how many days back one session can touch on the seismograph.
const maxSessionDays = 62

// jobClaudeAll parses every transcript, newest session first, so recent sessions
// appear in seconds. Rows for files and sessions that are gone are removed.
func (i *Indexer) jobClaudeAll(ctx context.Context) {
	files, err := i.pr.ClaudeDiscover(i.c.Cfg.ClaudeProfiles)
	complete := err == nil
	if err != nil {
		i.logOnce("", "claude", err)
	}
	states, err := i.c.DB.IdxClaudeFiles(ctx)
	if err != nil {
		i.logOnce("", "claude", err)
		return
	}
	fams := claude.Group(files)
	present := map[string]bool{}
	parents := map[string]bool{}
	touched := map[string]bool{}
	for _, f := range files {
		present[f.Path] = true
	}
	for _, fam := range fams {
		if ctx.Err() != nil {
			return
		}
		parents[fam.ParentPath] = true
		if p := i.parseFamily(ctx, fam, states); p != "" {
			touched[p] = true
		}
	}
	for _, p := range sortedPaths(touched) {
		i.autolink(ctx, p)
	}
	if !complete || ctx.Err() != nil {
		return
	}
	// Clean up what is gone from disk.
	var goneFiles []string
	for p := range states {
		if !present[p] {
			goneFiles = append(goneFiles, p)
		}
	}
	sessPaths, err := i.c.DB.IdxSessionPaths(ctx)
	if err != nil {
		i.logOnce("", "claude", err)
		return
	}
	var goneSess []string
	for sid, p := range sessPaths {
		if !parents[p] {
			goneSess = append(goneSess, sid)
		}
	}
	if len(goneFiles) == 0 && len(goneSess) == 0 {
		return
	}
	sort.Strings(goneSess)
	if err := i.c.DB.Write(ctx, func(tx *sql.Tx) error {
		for _, p := range goneFiles {
			if err := store.IdxDeleteClaudeFileTx(ctx, tx, p); err != nil {
				return err
			}
		}
		for _, sid := range goneSess {
			if err := store.IdxDeleteSessionTx(ctx, tx, sid); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		i.logOnce("", "claude", err)
		return
	}
	i.mu.Lock()
	for _, p := range goneFiles {
		delete(i.sessMem, p)
	}
	i.mu.Unlock()
}

// jobClaude parses one session family. sub is "<main .jsonl path>\x00<profile name>".
func (i *Indexer) jobClaude(ctx context.Context, sub string) {
	parent, profile, _ := strings.Cut(sub, "\x00")
	fam, ok := familyOf(parent, profile)
	if !ok {
		// The main file and every subagent file are gone: drop the session.
		id := strings.TrimSuffix(filepath.Base(parent), ".jsonl")
		row, _, found, err := i.c.DB.IdxSession(ctx, id)
		if err != nil {
			i.logOnce("", "claude", err)
			return
		}
		_ = i.c.DB.Write(ctx, func(tx *sql.Tx) error {
			if err := store.IdxDeleteClaudeFileTx(ctx, tx, parent); err != nil {
				return err
			}
			if found && row.JSONLPath == parent {
				return store.IdxDeleteSessionTx(ctx, tx, id)
			}
			return nil
		})
		i.mu.Lock()
		delete(i.sessMem, parent)
		i.mu.Unlock()
		if found && row.JSONLPath == parent && row.ProjectID != "" {
			i.publish(live.ProjectEvent(row.ProjectID, "sessions"))
		}
		return
	}
	states := map[string]store.ClaudeFile{}
	for _, f := range fam.Files {
		if cf, ok, err := i.c.DB.GetClaudeFile(ctx, f.Path); err == nil && ok {
			states[f.Path] = cf
		}
	}
	if p := i.parseFamily(ctx, fam, states); p != "" {
		i.autolink(ctx, p)
	}
}

// familyOf builds the family of one session from disk: the main file plus subagent files.
func familyOf(parent, profile string) (claude.Family, bool) {
	var files []claude.File
	if f, err := claude.Stat(parent, profile); err == nil {
		files = append(files, f)
	}
	id := strings.TrimSuffix(filepath.Base(parent), ".jsonl")
	subDir := filepath.Join(filepath.Dir(parent), id, "subagents")
	if ents, err := os.ReadDir(subDir); err == nil {
		for _, e := range ents {
			if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			if f, err := claude.Stat(filepath.Join(subDir, e.Name()), profile); err == nil {
				files = append(files, f)
			}
		}
	}
	if len(files) == 0 {
		return claude.Family{}, false
	}
	fams := claude.Group(files)
	if len(fams) == 0 {
		return claude.Family{}, false
	}
	return fams[0], true
}

// parseFamily parses one session (incrementally when possible) and stores it.
// stored holds the claude_file rows (may hold more paths than the family).
// It returns the project of a session that changed ("" when nothing changed), so the
// caller can refresh that project's auto links once.
func (i *Indexer) parseFamily(ctx context.Context, fam claude.Family, stored map[string]store.ClaudeFile) string {
	states := map[string]claude.State{}
	for _, f := range fam.Files {
		if cf, ok := stored[f.Path]; ok {
			states[f.Path] = claude.State{Size: cf.Size, MTime: cf.MTime, Offset: cf.Offset, Inode: cf.Inode}
		}
	}
	i.mu.Lock()
	mem := i.sessMem[fam.ParentPath]
	scanner := i.scanner
	i.mu.Unlock()

	var prev *claude.Session
	prevText := ""
	if mem != nil {
		s := mem.sess
		prev, prevText = &s, mem.text
	} else if len(states) > 0 {
		// After a restart: continue from the stored row of this file.
		row, mentions, ok, err := i.c.DB.IdxSession(ctx, fam.ID)
		if err == nil && ok && row.JSONLPath == fam.ParentPath {
			prev = sessionFromRow(row, mentions)
		}
	}
	var prevEnd, prevStartT = timeOr(prev, false), timeOr(prev, true)
	prevProject := ""
	if prev != nil {
		prevProject = prev.ProjectID
	}

	res, err := scanner.ParseFamily(ctx, fam, prev, states)
	if err != nil {
		if ctx.Err() == nil {
			i.logOnce("", "claude", err)
		}
		return ""
	}
	if res.Skipped > 0 || res.Unknown > 0 {
		i.log.Debug("claude parse", "session", fam.ID, "skippedLines", res.Skipped, "unknownRecords", res.Unknown)
	}
	if !res.Changed && prev != nil {
		return ""
	}
	s := res.Session
	text := prevText
	if res.Rebuilt || text == "" {
		text = strings.Join(i.pr.ClaudeUsers(ctx, fam.ParentPath), "\n")
	}

	// The same session id can live under two profiles. Keep the row with more turns;
	// on a tie the newer file, then the smaller path. The rule is a strict order, so the
	// same two files always give the same winner and sweeps never flip between them (M2).
	winner := true
	if row, _, ok, err := i.c.DB.IdxSession(ctx, s.ID); err == nil && ok && row.JSONLPath != fam.ParentPath &&
		sessionBeats(row.Turns, row.JSONLMTime, row.JSONLPath, s.Turns, s.MTime, fam.ParentPath) {
		winner = false
	}

	touched := map[string]map[string]bool{} // project -> days
	addDays := func(project string, days []string) {
		if project == "" {
			return
		}
		if touched[project] == nil {
			touched[project] = map[string]bool{}
		}
		for _, d := range days {
			touched[project][d] = true
		}
	}
	if winner {
		if res.Rebuilt || prev == nil || prevProject != s.ProjectID {
			addDays(s.ProjectID, i.daysBetween(s.Start, s.End, maxSessionDays))
			if prev != nil {
				addDays(prevProject, i.daysBetween(prevStartT, prevEnd, maxSessionDays))
			}
		} else {
			addDays(s.ProjectID, i.daysBetween(prevEnd, s.End, maxSessionDays))
		}
	}

	err = i.c.DB.Write(ctx, func(tx *sql.Tx) error {
		for path, st := range res.States {
			if err := store.PutClaudeFileTx(ctx, tx, store.ClaudeFile{Path: path, Size: st.Size, MTime: st.MTime,
				Offset: st.Offset, Inode: st.Inode}); err != nil {
				return err
			}
		}
		if !winner {
			return nil
		}
		if err := store.UpsertSessionsTx(ctx, tx, []store.Session{sessionRow(s, text)}); err != nil {
			return err
		}
		links := []store.SessionProject{}
		if s.ProjectID != "" {
			links = append(links, store.SessionProject{ProjectID: s.ProjectID, Reason: "cwd"})
		}
		ments := make([]string, 0, len(s.Mentions))
		for p := range s.Mentions {
			if p != s.ProjectID && p != "" {
				ments = append(ments, p)
			}
		}
		sort.Strings(ments)
		for _, p := range ments {
			links = append(links, store.SessionProject{ProjectID: p, Reason: "mention"})
		}
		if err := store.SetSessionProjectsTx(ctx, tx, s.ID, links); err != nil {
			return err
		}
		// Day-readout events: one session_start and one (moving) session_end.
		ref := "session:" + s.ID
		if prevProject != "" && prevProject != s.ProjectID {
			if err := store.IdxDeleteEventsTx(ctx, tx, prevProject, "", ref); err != nil {
				return err
			}
		}
		if s.ProjectID != "" {
			title := s.Title
			if title == "" {
				title = "claude session"
			}
			meta := metaJSON(map[string]any{"profile": s.Profile, "turns": s.Turns})
			for _, k := range []string{"session_start", "session_end"} {
				if err := store.IdxDeleteEventsTx(ctx, tx, s.ProjectID, k, ref); err != nil {
					return err
				}
			}
			if !s.Start.IsZero() {
				if _, err := store.AddEventTx(ctx, tx, store.Event{ProjectID: s.ProjectID, TS: s.Start,
					Day: i.dayOf(s.Start), Kind: "session_start", Ref: ref, Label: title, Meta: meta}); err != nil {
					return err
				}
			}
			if !s.End.IsZero() && !s.End.Equal(s.Start) {
				if _, err := store.AddEventTx(ctx, tx, store.Event{ProjectID: s.ProjectID, TS: s.End,
					Day: i.dayOf(s.End), Kind: "session_end", Ref: ref, Label: title, Meta: meta}); err != nil {
					return err
				}
			}
		}
		for project, days := range touched {
			for d := range days {
				if err := store.RecomputeDailyTx(ctx, tx, project, d, i.loc); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		if ctx.Err() == nil {
			i.logOnce(s.ProjectID, "claude", err)
		}
		return ""
	}

	i.mu.Lock()
	i.sessMem[fam.ParentPath] = &sessMem{sess: s, text: text}
	newer := false
	if ps := i.projs[s.ProjectID]; ps != nil && winner && s.End.After(ps.lastSession) {
		newer = s.End.After(ps.lastChange())
		ps.lastSession = s.End
	}
	i.mu.Unlock()
	if newer { // the project's "last change" moved: keep the home lane order right
		if row, ok := i.projectRow(s.ProjectID); ok {
			if err := i.c.DB.UpsertProject(ctx, row); err != nil {
				i.logOnce(s.ProjectID, "claude", err)
			}
		}
	}

	if !winner {
		return ""
	}
	if s.ProjectID != "" {
		i.publish(live.ProjectEvent(s.ProjectID, "sessions"))
	}
	if prevProject != "" && prevProject != s.ProjectID {
		i.publish(live.ProjectEvent(prevProject, "sessions"))
	}
	for project, days := range touched {
		i.publishDays(project, days)
	}
	return s.ProjectID
}

func timeOr(s *claude.Session, start bool) time.Time {
	if s == nil {
		return time.Time{}
	}
	if start {
		return s.Start
	}
	return s.End
}

func sessionRow(s claude.Session, text string) store.Session {
	return store.Session{
		ID: s.ID, Profile: s.Profile, JSONLPath: s.JSONLPath, CWD: s.CWD, ProjectID: s.ProjectID,
		Start: s.Start, End: s.End, Title: s.Title, Turns: s.Turns, KB: s.KB, Tools: s.Tools, Edited: s.Edited,
		JSONLSize: s.Size, JSONLMTime: s.MTime, Text: text,
	}
}

// sessionFromRow rebuilds a parser session from the stored row. Mention counts are not
// stored, so each mentioned project counts 1 (only the set matters to the index).
func sessionFromRow(r store.Session, mentions []string) *claude.Session {
	s := &claude.Session{
		ID: r.ID, Profile: r.Profile, JSONLPath: r.JSONLPath, CWD: r.CWD, ProjectID: r.ProjectID,
		Start: r.Start, End: r.End, Title: r.Title, Turns: r.Turns, KB: r.KB,
		Tools: map[string]int{}, Edited: append([]string(nil), r.Edited...), Size: r.JSONLSize, MTime: r.JSONLMTime,
		Mentions: map[string]int{},
	}
	for k, v := range r.Tools {
		s.Tools[k] = v
	}
	for _, m := range mentions {
		s.Mentions[m] = 1
	}
	return s
}

func metaJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("{}")
	}
	return b
}

// sessionBeats reports whether the stored copy a of a session wins over the copy b that
// was just parsed: more turns wins, then the newer .jsonl mtime, then the smaller path.
func sessionBeats(aTurns int, aMTime time.Time, aPath string, bTurns int, bMTime time.Time, bPath string) bool {
	if aTurns != bTurns {
		return aTurns > bTurns
	}
	// The store keeps whole seconds, so compare whole seconds.
	if a, b := aMTime.Unix(), bMTime.Unix(); a != b {
		return a > b
	}
	return aPath < bPath
}
