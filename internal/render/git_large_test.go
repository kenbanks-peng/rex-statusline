package render

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitLargeWorktreeKeepsBranchAndDirtyState(t *testing.T) {
	f := newGitFixture(t)
	for i := range 900 {
		path := filepath.Join(f.dir, fmt.Sprintf("%05d-%080d", i, i))
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	values := map[string]string{}
	gitSegments(context.Background(), &Renderer{}, f.dir, f.env, values)
	if !strings.Contains(values["GITSTATUS"], "main") {
		t.Fatalf("large worktree lost branch: %v", values)
	}
	if values["GITSTATUS_CHANGED"] != "1" {
		t.Fatalf("large worktree lost dirty state: %v", values)
	}
}
