package rex

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"rex-statusline/internal/timing"
)

type SessionSnapshot struct {
	SessionID string `json:"session_id"`
	View      View   `json:"view"`
	Error     string `json:"error"`
}

// Reuse the supported Lua CLI connection for enumeration and the first view.
// Pass identifiers as data, never executable Lua.
const snapshotScript = `
local sessions
if rex.args.session_id ~= "" then
 sessions = {{session_id=rex.args.session_id}}
else
 local listed, err = rex.call("session.list")
 if not listed then error(tostring(err)) end
 sessions = listed.sessions or {}
end
local snapshots = {}
if #sessions == 0 then return {} end
local session = sessions[1]
local attached, err = rex.call("session.attach", {session_id=session.session_id})
if attached and attached.view then
 table.insert(snapshots, {session_id=session.session_id, view=attached.view})
else
 table.insert(snapshots, {session_id=session.session_id, error=tostring(err or "missing attached session view")})
end
return {sessions=sessions, snapshots=snapshots}
`

func (c Client) Snapshot(ctx context.Context, session string) (snapshots []SessionSnapshot, err error) {
	began := time.Now()
	defer func() { timing.Record(ctx, "rex_snapshot_done", began, "ok", err == nil) }()
	args, err := json.Marshal(map[string]string{"session_id": session})
	if err != nil {
		return nil, err
	}
	var result struct {
		Sessions  []Session         `json:"sessions"`
		Snapshots []SessionSnapshot `json:"snapshots"`
	}
	err = c.run(ctx, []string{"do", "-e", snapshotScript, "--args", string(args), "--json"}, &result)
	if err != nil {
		return nil, err
	}
	if len(result.Sessions) > 0 && len(result.Snapshots) != 1 {
		return nil, fmt.Errorf("Rex snapshot omitted its session view")
	}
	for _, snapshot := range result.Snapshots {
		if snapshot.Error == "" && snapshot.View.SessionID == "" && snapshot.View.Revision == 0 {
			return nil, fmt.Errorf("Rex snapshot returned an empty view for %s", snapshot.SessionID)
		}
	}
	if len(result.Sessions) <= 1 {
		return result.Snapshots, nil
	}
	// Only the first view shares enumeration's process. Other sessions retain
	// independent deadlines, response limits and bounded parallel discovery.
	snapshots = make([]SessionSnapshot, len(result.Sessions))
	copy(snapshots, result.Snapshots)
	jobs := make(chan int, len(result.Sessions)-1)
	for i := 1; i < len(result.Sessions); i++ {
		jobs <- i
	}
	close(jobs)
	var workers sync.WaitGroup
	for range min(4, len(result.Sessions)-1) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				snapshots[i].SessionID = result.Sessions[i].ID
				callErr := ctx.Err()
				if callErr == nil {
					callErr = c.Call(ctx, snapshots[i].SessionID, "session.view", map[string]any{}, &snapshots[i].View)
				}
				if callErr != nil {
					snapshots[i].Error = callErr.Error()
				}
			}
		}()
	}
	workers.Wait()
	return snapshots, nil
}
