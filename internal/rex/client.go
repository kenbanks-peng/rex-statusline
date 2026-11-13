package rex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"rex-statusline/internal/timing"
)

const Terminal = "com.superlogical.terminal"
const StatusPrefix = "rex-statusline:"

type Rect [4]float64
type Node struct {
	BlockID string                         `json:"block_id"`
	Rect    Rect                           `json:"rect"`
	Split   *struct{ Before, After *Node } `json:"split"`
}
type Block struct {
	ID      string `json:"block_id"`
	Creator string `json:"creator_name"`
	Label   string `json:"label"`
	HostID  string `json:"host_id"`
}
type Layer struct {
	ID     string  `json:"layer_id"`
	Kind   string  `json:"kind"`
	State  string  `json:"state"`
	Bounds Rect    `json:"bounds"`
	Layout *Node   `json:"layout"`
	Blocks []Block `json:"blocks"`
}
type Window struct {
	ID      string  `json:"window_id"`
	Focused string  `json:"focused_block_id"`
	Zoomed  string  `json:"zoomed_block_id"`
	Layers  []Layer `json:"layers"`
}
type View struct {
	SessionID      string   `json:"session_id"`
	ActiveWindowID string   `json:"active_window_id"`
	Revision       uint64   `json:"revision"`
	Windows        []Window `json:"windows"`
}
type Identity struct {
	Principal struct {
		Kind      string `json:"kind"`
		Transport string `json:"transport"`
	} `json:"principal"`
	InstanceID string `json:"instance_id"`
	HostID     string `json:"host_id"`
	SessionID  string `json:"session_id"`
}
type ProcessEntry struct {
	UserID *int   `json:"user_id"`
	PID    int    `json:"pid"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	Script string `json:"script"`
	Cwd    string `json:"cwd"`
}
type Process struct {
	Foreground ProcessEntry   `json:"foreground"`
	Ancestors  []ProcessEntry `json:"ancestors"`
	Child      ProcessEntry   `json:"child"`
}
type Size struct {
	Columns      int `json:"columns"`
	Rows         int `json:"rows"`
	CellHeightPX int `json:"cell_height_px"`
	CellWidthPX  int `json:"cell_width_px"`
}
type Created struct {
	LayerID  string   `json:"layer_id"`
	BlockIDs []string `json:"block_ids"`
}
type Session struct {
	ID string `json:"session_id"`
}
type API interface {
	Call(context.Context, string, string, any, any) error
}
type Client struct {
	Executable, Server string
	Timeout            time.Duration
}

func (c Client) args(args ...string) []string {
	base := []string{"--autostart=false"}
	if c.Server != "" {
		base = append(base, "-S", c.Server)
	}
	return append(base, args...)
}

type limitedBuffer struct {
	bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		return 0, fmt.Errorf("Rex response exceeds %d bytes", b.max)
	}
	return b.Buffer.Write(p)
}
func (c Client) run(ctx context.Context, args []string, out any) error {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	executable := c.Executable
	if executable == "" {
		executable = "rex"
	}
	cmd := exec.CommandContext(ctx, executable, c.args(args...)...)
	var stdout limitedBuffer
	stdout.max = 8 << 20
	var stderr limitedBuffer
	stderr.max = 32 << 10
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("rex %s: %w: %s", strings.Join(args[:min(len(args), 3)], " "), err, strings.TrimSpace(stderr.String()))
	}
	if out != nil {
		if err := json.Unmarshal(stdout.Bytes(), out); err != nil {
			return fmt.Errorf("rex response: %w", err)
		}
	}
	return nil
}
func (c Client) Identity(ctx context.Context) (Identity, error) {
	var id Identity
	err := c.run(ctx, []string{"whoami", "--json"}, &id)
	if err == nil && (id.InstanceID == "" || id.HostID == "") {
		err = fmt.Errorf("Rex returned no server identity")
	}
	return id, err
}
func (c Client) Call(ctx context.Context, session, method string, args any, out any) (err error) {
	began := time.Now()
	defer func() {
		fields := []any{"method", method, "ok", err == nil}
		if timing.From(ctx).Session == "" {
			fields = append(fields, "session", session)
		}
		if method == "block.method" {
			if values, ok := args.(map[string]any); ok {
				fields = append(fields, "block_method", values["method"])
			}
		}
		// Do not record arguments, responses, process paths, or rendered content.
		timing.Record(ctx, "rex_call_done", began, fields...)
	}()
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	argv := []string{"api", "call"}
	if session != "" {
		argv = append(argv, "-s", session)
	}
	argv = append(argv, method, string(raw))
	return c.run(ctx, argv, out)
}

// Events uses the installed CLI's supported transport rather than assuming an
// undocumented socket wire protocol. Only relevant events wake reconciliation.
func (c Client) Events(ctx context.Context, notify chan Event) error {
	executable := c.Executable
	if executable == "" {
		executable = "rex"
	}
	cmd := exec.CommandContext(ctx, executable, c.args("events", "--all", "--json")...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		event, relevant := parseEvent(scanner.Bytes())
		if relevant {
			enqueueEvent(ctx, notify, event)
		}
	}
	scanErr := scanner.Err()
	if scanErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if scanErr != nil {
		return scanErr
	}
	if waitErr != nil {
		return waitErr
	}
	return io.EOF
}

func Leaves(node *Node) map[string]Rect {
	out := map[string]Rect{}
	var visit func(*Node)
	visit = func(n *Node) {
		if n == nil {
			return
		}
		if n.BlockID != "" {
			out[n.BlockID] = n.Rect
		}
		if n.Split != nil {
			visit(n.Split.Before)
			visit(n.Split.After)
		}
	}
	visit(node)
	return out
}
func IsStatus(b Block) bool {
	return b.Label == "rex-status" || strings.HasPrefix(b.Label, StatusPrefix)
}
