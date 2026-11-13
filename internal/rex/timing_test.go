package rex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rex-statusline/internal/timing"
)

func TestCallTimingIncludesMethodAndFailureWithoutArguments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timing.jsonl")
	logger, err := timing.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	ctx := timing.WithLogger(context.Background(), logger)
	client := Client{Executable: "/nonexistent/rex-statusline-test"}
	for _, method := range []string{"session.list", "session.view", "block.method"} {
		err := client.Call(ctx, "session:s", method, map[string]any{"method": "process", "private": "SECRET_CONTEXT"}, nil)
		if err == nil {
			t.Fatal("missing call failure")
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "SECRET_CONTEXT") {
		t.Fatal("arguments leaked into timings")
	}
	rows := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(rows) != 3 {
		t.Fatalf("timing records %d", len(rows))
	}
	for i, row := range rows {
		var record struct {
			Msg, Method, Session string
			BlockMethod          string `json:"block_method"`
			OK                   bool
			DurationMS           float64 `json:"duration_ms"`
		}
		if err := json.Unmarshal([]byte(row), &record); err != nil {
			t.Fatal(err)
		}
		if record.Msg != "rex_call_done" || record.OK || record.Session != "session:s" || record.DurationMS <= 0 {
			t.Fatal(row)
		}
		if i == 2 && record.BlockMethod != "process" {
			t.Fatal(row)
		}
	}
}
