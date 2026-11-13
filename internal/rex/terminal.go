package rex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"rex-statusline/internal/timing"
)

type TerminalContext struct {
	Size    Size    `json:"size"`
	Process Process `json:"process"`
}

const terminalScript = `
local attached, err = rex.call("session.attach", {session_id=rex.args.session_id})
if not attached then error(tostring(err)) end
local function call(method)
 local value, err = rex.call("block.method", {session_id=rex.args.session_id, block_id=rex.args.block_id, method=method, args={}})
 if not value then error(tostring(err)) end
 return value
end
local result = {size=call("size")}
if rex.args.process then result.process = call("process") end
return result
`

// Terminal reads fresh dimensions and ownership over one supported CLI connection.
func (c Client) Terminal(ctx context.Context, session, block string, process bool) (result TerminalContext, err error) {
	began := time.Now()
	defer func() { timing.Record(ctx, "terminal_context_done", began, "ok", err == nil) }()
	args, err := json.Marshal(map[string]any{"session_id": session, "block_id": block, "process": process})
	if err != nil {
		return result, err
	}
	var response struct {
		Size    *Size    `json:"size"`
		Process *Process `json:"process"`
	}
	err = c.run(ctx, []string{"do", "-e", terminalScript, "--args", string(args), "--json"}, &response)
	if err != nil {
		return result, err
	}
	if response.Size == nil || (process && response.Process == nil) {
		return result, fmt.Errorf("Rex terminal context omitted requested fields")
	}
	result.Size = *response.Size
	if response.Process != nil {
		result.Process = *response.Process
	}
	return result, nil
}

// ReadTerminal also supports API implementations without a combined transport.
func ReadTerminal(ctx context.Context, api API, session, block string, process bool) (TerminalContext, error) {
	if reader, ok := api.(interface {
		Terminal(context.Context, string, string, bool) (TerminalContext, error)
	}); ok {
		return reader.Terminal(ctx, session, block, process)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var result TerminalContext
	done := make(chan error, 1)
	if process {
		go func() {
			began := time.Now()
			err := api.Call(ctx, session, "block.method", map[string]any{"block_id": block, "method": "process", "args": map[string]any{}}, &result.Process)
			timing.Record(ctx, "process_done", began, "ok", err == nil)
			if err != nil {
				cancel()
			}
			done <- err
		}()
	}
	began := time.Now()
	err := api.Call(ctx, session, "block.method", map[string]any{"block_id": block, "method": "size", "args": map[string]any{}}, &result.Size)
	timing.Record(ctx, "size_done", began, "ok", err == nil)
	if err != nil || result.Size.Columns <= 0 || result.Size.Rows <= 0 {
		cancel()
	}
	if process {
		err = errors.Join(err, <-done)
	}
	return result, err
}
