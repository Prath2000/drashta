// Package live is the in-process event bus behind GET /api/events.
//
// The indexer, the servers loop and the tracker call Hub.Publish. The HTTP
// layer (internal/httpapi) turns the events into Server-Sent Events.
// Rules come from 03-architecture.md section 6 and 04-data-model.md section 6.
//
// What the hub does:
//   - gives every event a growing id (the SSE "id:" field);
//   - keeps the last 1000 events so a page can reconnect with Last-Event-ID;
//   - merges "project" events: at most one per project per second;
//   - remembers, for each open stream, whether the page says it is visible.
//
// Ids start at the start time in microseconds, so an id from before a server
// restart is never confused with a new one. A page with such an id gets a
// resync, not a wrong replay.
//
// # Exported API
//
// Hub:
//   - NewHub(HubOptions) *Hub                 make a hub. Options: Now, RingSize, Window, ConnBuffer, StartSeq, Tick.
//   - (*Hub).Publish(Event) uint64            send an event. Returns the id, or 0 if a project event was held back.
//   - (*Hub).Flush() int                      send held project events whose window is over.
//   - (*Hub).Run(ctx, tick <-chan time.Time)  call Flush on each tick (tests pass a fake tick).
//   - (*Hub).Start(ctx)                       Run with a real 250 ms ticker, in a goroutine.
//   - (*Hub).Head() uint64                    id of the newest event.
//   - (*Hub).Since(lastID) (events, resync, head)  missed events, or resync=true. lastID 0 means "know nothing".
//   - (*Hub).Connect(lastID) *Conn            open a stream (used by the SSE handler).
//   - (*Hub).Subscribe(lastID) (replay, ch, resync, cancel)  the same, as four values.
//   - (*Hub).SetVisible(connID, bool) bool    set one stream's visible flag by id.
//   - (*Hub).SetAllVisible(bool) int          set every stream's flag.
//   - (*Hub).AnyVisible() bool                true if at least one open stream is visible. Drives the lsof cadence.
//   - (*Hub).Conns() int                      number of open streams.
//   - (*Hub).Changed() <-chan struct{}        signal when a stream opens, closes or changes its flag.
//   - (*Hub).Close()                          close all streams.
//
// Conn (one open stream): fields ID, Replay, Resync, Events, Head; methods Close, SetVisible, Visible.
// A new Conn counts as visible. If a stream falls 256 events behind, the hub
// closes its Events channel; the page reconnects and catches up.
//
// Event: struct {ID uint64; Type string; Data json.RawMessage}.
//
// Event builders (they return an Event with no id yet):
//   - ProjectEvent(projectID, what)           what: files | commits | dirty | sessions | docs
//   - ServersEvent(projectID, active []int)   projectID may be ""
//   - TrackerEvent(projectID, kind, id)       kind: task | feature | milestone | snapshot | note
//   - ActivityEvent(projectID, day)           day: "YYYY-MM-DD"
//   - ScanEvent(projectID, state, at)         state: running | done; projectID may be ""
//   - CountsEvent(Counts)                     {liveServers, openTasks, awaitingReview, openNotes}
//   - ResyncEvent()                           tells the page to refetch everything visible
//   - HelloEvent(Hello), NewEvent(type, data) used by the HTTP layer and for custom events
//
// Payload types: ProjectData, ServersData, TrackerData, ActivityData,
// ScanData, Counts, Hello. Type names: TypeHello, TypeProject, TypeServers,
// TypeTracker, TypeActivity, TypeScan, TypeCounts, TypeResync.
//
// Coalescing: when several "project" events for one project arrive inside the
// window, the page gets one event. If they all had the same "what", it is kept.
// If they differed, the event has what = "all" (WhatAll) and a "parts" list.
// That is the only addition to the payloads in 04 section 6. A page that does
// not know "all" should refetch the whole project summary.
package live
