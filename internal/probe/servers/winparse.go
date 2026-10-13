package servers

import (
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
)

// Pure parsers for the Windows tools. They have no build tag so tests run
// everywhere.

type listenRow struct{ pid, port int }

func errExit(tool string, code int) error {
	return fmt.Errorf("%s: exit status %d", tool, code)
}

// parseNetstat reads `netstat -ano -p tcp` output and keeps LISTENING rows on
// loopback or wildcard addresses, deduped on (pid, port).
func parseNetstat(out string) []listenRow {
	seen := map[listenRow]bool{}
	var rows []listenRow
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || !strings.EqualFold(f[0], "TCP") || !strings.EqualFold(f[3], "LISTENING") {
			continue
		}
		addr := f[1]
		idx := strings.LastIndex(addr, ":")
		if idx < 0 {
			continue
		}
		switch addr[:idx] {
		case "127.0.0.1", "0.0.0.0", "[::]", "[::1]":
		default:
			continue
		}
		port, err := strconv.Atoi(addr[idx+1:])
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(f[4])
		if err != nil || pid == 0 {
			continue
		}
		r := listenRow{pid, port}
		if !seen[r] {
			seen[r] = true
			rows = append(rows, r)
		}
	}
	return rows
}

// parseTasklist reads `tasklist /FO CSV /NH` output into pid -> process name
// (without the ".exe" suffix, so "node.exe" matches the dev list).
func parseTasklist(out string) map[int]string {
	names := map[int]string{}
	recs, _ := csv.NewReader(strings.NewReader(out)).ReadAll()
	for _, r := range recs {
		if len(r) < 2 {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(r[1]))
		if err != nil {
			continue
		}
		name := r[0]
		if len(name) > 4 && strings.EqualFold(name[len(name)-4:], ".exe") {
			name = name[:len(name)-4]
		}
		names[pid] = strings.ReplaceAll(name, " ", "_")
	}
	return names
}

// lsofLike renders rows as lsof-shaped text (header plus one line per row) so
// parseLsof can read it. Unknown names become "unknown".
func lsofLike(rows []listenRow, names map[int]string) string {
	var b strings.Builder
	b.WriteString("COMMAND PID USER FD TYPE DEVICE SIZE/OFF NODE NAME\n")
	for _, r := range rows {
		name := names[r.pid]
		if name == "" {
			name = "unknown"
		}
		fmt.Fprintf(&b, "%s %d - 0u IPv4 0 0t0 TCP *:%d (LISTEN)\n", name, r.pid, r.port)
	}
	return b.String()
}
