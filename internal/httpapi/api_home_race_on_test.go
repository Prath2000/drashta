//go:build race

package httpapi

// The race detector makes code several times slower, so timing tests use a looser limit.
const ahRaceEnabled = true
