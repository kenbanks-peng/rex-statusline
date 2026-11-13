package rex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotUsesOneCLIAndPassesSessionAsData(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "rex")
	record := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + record + "\nprintf '%s\\n' '{\"snapshots\":[{\"session_id\":\"s\",\"view\":{\"revision\":7}},{\"session_id\":\"gone\",\"error\":\"missing\"}]}'\n"
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	session := `session:"; error("injection") --`
	got, err := (Client{Executable: executable}).Snapshot(context.Background(), session)
	if err != nil || len(got) != 2 || got[0].View.Revision != 7 || got[1].Error != "missing" {
		t.Fatalf("%+v %v", got, err)
	}
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	args := string(raw)
	encoded, _ := json.Marshal(map[string]string{"session_id": session})
	if !strings.Contains(args, "do\n-e\n"+snapshotScript+"\n--args\n"+string(encoded)+"\n--json") {
		t.Fatalf("incorrect arguments: %s", args)
	}
}

func TestSnapshotCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Client{}).Snapshot(ctx, ""); err == nil {
		t.Fatal("ignored cancellation")
	}
}
