//go:build race

package store

// The race detector makes code several times slower, so timing tests use a looser limit.
const raceEnabled = true
