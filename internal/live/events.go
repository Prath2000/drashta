package live

import (
	"encoding/json"
	"time"
)

// Event type names. They are the SSE "event:" field (04 section 6).
const (
	TypeHello    = "hello"
	TypeProject  = "project"
	TypeServers  = "servers"
	TypeTracker  = "tracker"
	TypeActivity = "activity"
	TypeScan     = "scan"
	TypeCounts   = "counts"
	TypeResync   = "resync"
)

// WhatAll is the "what" value of a coalesced project event that merged
// several different parts. The parts are listed in ProjectData.Parts.
const WhatAll = "all"

// Event is one message on the stream. Data is already JSON.
// ID is set by the hub when the event is published. A Hello event is never
// published: each connection gets its own.
type Event struct {
	ID   uint64          `json:"id"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// ProjectData is the payload of a "project" event.
// What is one of files, commits, dirty, sessions, docs.
// When several different parts were coalesced into one event, What is
// WhatAll and Parts lists them.
type ProjectData struct {
	ProjectID string   `json:"projectId"`
	What      string   `json:"what"`
	Parts     []string `json:"parts,omitempty"`
}

// ServersData is the payload of a "servers" event. Active lists live ports.
type ServersData struct {
	ProjectID string `json:"projectId,omitempty"`
	Active    []int  `json:"active"`
}

// TrackerData is the payload of a "tracker" event.
// Kind is one of task, feature, milestone, snapshot, note.
type TrackerData struct {
	ProjectID string `json:"projectId"`
	Kind      string `json:"kind"`
	ID        string `json:"id"`
}

// ActivityData is the payload of an "activity" event. Day is "YYYY-MM-DD".
type ActivityData struct {
	ProjectID string `json:"projectId"`
	Day       string `json:"day"`
}

// ScanData is the payload of a "scan" event. State is "running" or "done".
type ScanData struct {
	ProjectID string    `json:"projectId,omitempty"`
	State     string    `json:"state"`
	At        time.Time `json:"at"`
}

// Counts is the payload of a "counts" event. The hello event also carries it.
type Counts struct {
	LiveServers    int `json:"liveServers"`
	OpenTasks      int `json:"openTasks"`
	AwaitingReview int `json:"awaitingReview"`
	OpenNotes      int `json:"openNotes"`
}

// Hello is the payload of the "hello" event. The server sends it first on
// every connection. Conn is the connection id the page sends back with
// POST /api/presence.
type Hello struct {
	Now    time.Time `json:"now"`
	Counts Counts    `json:"counts"`
	Conn   string    `json:"conn"`
}

// NewEvent marshals data into an event of the given type.
// If data cannot be marshaled the payload is {}.
func NewEvent(typ string, data any) Event {
	b, err := json.Marshal(data)
	if err != nil {
		b = []byte("{}")
	}
	return Event{Type: typ, Data: b}
}

// ProjectEvent builds a "project" event.
func ProjectEvent(projectID, what string) Event {
	return NewEvent(TypeProject, ProjectData{ProjectID: projectID, What: what})
}

// ServersEvent builds a "servers" event. A nil list is sent as [].
func ServersEvent(projectID string, active []int) Event {
	if active == nil {
		active = []int{}
	}
	return NewEvent(TypeServers, ServersData{ProjectID: projectID, Active: active})
}

// TrackerEvent builds a "tracker" event.
func TrackerEvent(projectID, kind, id string) Event {
	return NewEvent(TypeTracker, TrackerData{ProjectID: projectID, Kind: kind, ID: id})
}

// ActivityEvent builds an "activity" event.
func ActivityEvent(projectID, day string) Event {
	return NewEvent(TypeActivity, ActivityData{ProjectID: projectID, Day: day})
}

// ScanEvent builds a "scan" event. state is "running" or "done".
func ScanEvent(projectID, state string, at time.Time) Event {
	return NewEvent(TypeScan, ScanData{ProjectID: projectID, State: state, At: at})
}

// CountsEvent builds a "counts" event.
func CountsEvent(c Counts) Event {
	return NewEvent(TypeCounts, c)
}

// ResyncEvent builds a "resync" event. The page refetches everything visible.
func ResyncEvent() Event {
	return Event{Type: TypeResync, Data: json.RawMessage("{}")}
}

// HelloEvent builds the per-connection "hello" event (no ID).
func HelloEvent(h Hello) Event {
	return NewEvent(TypeHello, h)
}
