package rex

import (
	"context"
	"os"
	"testing"
)

// Exercise the CLI without the session variables inherited by interactive tests.
func TestLiveSnapshotMatchesSessionViews(t *testing.T) {
	if os.Getenv("REX_STATUSLINE_LIVE_TEST") != "1" {
		t.Skip("requires a running local Rex server")
	}
	for _, name := range []string{"REX_SESSION", "REX_BLOCK", "REX_CLIENT"} {
		t.Setenv(name, "")
	}
	ctx := context.Background()
	client := Client{}
	var listed struct {
		Sessions []Session `json:"sessions"`
	}
	if err := client.Call(ctx, "", "session.list", map[string]any{}, &listed); err != nil {
		t.Fatal(err)
	}
	scopes := []string{""}
	if len(listed.Sessions) > 0 {
		scopes = append(scopes, listed.Sessions[0].ID)
	}
	for _, scope := range scopes {
		snapshots, err := client.Snapshot(ctx, scope)
		if err != nil {
			t.Fatal(err)
		}
		want := len(listed.Sessions)
		if scope != "" {
			want = 1
		}
		if len(snapshots) != want {
			t.Fatalf("scope %q: snapshots=%d sessions=%d", scope, len(snapshots), want)
		}
		for _, snapshot := range snapshots {
			var view View
			if err := client.Call(ctx, snapshot.SessionID, "session.view", map[string]any{}, &view); err != nil {
				t.Fatal(err)
			}
			if snapshot.Error != "" || snapshot.View.SessionID != view.SessionID || snapshot.View.ActiveWindowID != view.ActiveWindowID || len(snapshot.View.Windows) != len(view.Windows) {
				t.Fatalf("headless snapshot differs from direct session.view for %s: error=%q windows=%d want=%d", snapshot.SessionID, snapshot.Error, len(snapshot.View.Windows), len(view.Windows))
			}
		}
	}
}
