package rex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotRejectsMissingView(t *testing.T) {
	for _, reply := range []string{
		`{"sessions":[{"session_id":"s"}],"snapshots":[{"session_id":"s"}]}`,
		`{"sessions":[{"session_id":"s"}]}`,
	} {
		executable := filepath.Join(t.TempDir(), "rex")
		if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf '%s\\n' '"+reply+"'\n"), 0700); err != nil {
			t.Fatal(err)
		}
		if _, err := (Client{Executable: executable}).Snapshot(context.Background(), ""); err == nil {
			t.Fatal("missing view silently accepted")
		}
	}
}
