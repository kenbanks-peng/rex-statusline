package rex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotIsolatesRemainingSessionFailures(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "rex")
	script := `#!/bin/sh
if [ "$2" = do ]; then
 printf '%s\n' '{"sessions":[{"session_id":"a"},{"session_id":"b"},{"session_id":"c"}],"snapshots":[{"session_id":"a","view":{"revision":1}}]}'
elif [ "$5" = b ]; then
 printf '%s\n' '{"revision":2}'
else
 echo unavailable >&2
 exit 1
fi
`
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	got, err := (Client{Executable: executable}).Snapshot(context.Background(), "")
	if err != nil || len(got) != 3 {
		t.Fatalf("%+v %v", got, err)
	}
	if got[0].SessionID != "a" || got[0].View.Revision != 1 || got[1].SessionID != "b" || got[1].View.Revision != 2 || got[2].SessionID != "c" || got[2].Error == "" {
		t.Fatalf("%+v", got)
	}
}
