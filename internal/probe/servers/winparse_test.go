package servers

import "testing"

const netstatFixture = `
Active Connections

  Proto  Local Address          Foreign Address        State           PID
  TCP    0.0.0.0:135            0.0.0.0:0              LISTENING       1140
  TCP    127.0.0.1:5173         0.0.0.0:0              LISTENING       4242
  TCP    [::1]:5173             [::]:0                 LISTENING       4242
  TCP    [::]:3000              [::]:0                 LISTENING       777
  TCP    192.168.1.5:8080       0.0.0.0:0              LISTENING       900
  TCP    127.0.0.1:5173         127.0.0.1:50000        ESTABLISHED     4242
`

const tasklistFixture = `"System Idle Process","0","Services","0","8 K"
"node.exe","4242","Console","1","80,000 K"
"Code Helper.exe","777","Console","1","1 K"
`

func TestParseNetstat(t *testing.T) {
	rows := parseNetstat(netstatFixture)
	want := []listenRow{{1140, 135}, {4242, 5173}, {777, 3000}}
	if len(rows) != len(want) {
		t.Fatalf("got %v", rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Fatalf("row %d: %v want %v", i, rows[i], want[i])
		}
	}
}

func TestParseTasklistAndLsofLike(t *testing.T) {
	names := parseTasklist(tasklistFixture)
	if names[4242] != "node" || names[777] != "Code_Helper" {
		t.Fatalf("names %v", names)
	}
	got := parseLsof(lsofLike(parseNetstat(netstatFixture), names))
	if len(got) != 3 || got[1].Name != "node" || got[1].Port != 5173 || got[1].PID != 4242 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Name != "unknown" || KindOf(got[1].Name) != KindDev {
		t.Fatalf("got %+v", got)
	}
}
