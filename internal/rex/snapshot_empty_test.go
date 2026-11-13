package rex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotEmptyServer(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "rex")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf '{}\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	snapshots, err := (Client{Executable: executable}).Snapshot(context.Background(), "")
	if err != nil || len(snapshots) != 0 {
		t.Fatalf("%+v %v", snapshots, err)
	}
}
