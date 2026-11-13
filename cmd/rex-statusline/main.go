package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"rex-statusline/internal/app"
	"rex-statusline/internal/bridge"
	"rex-statusline/internal/bsp"
	"rex-statusline/internal/defaults"
	"rex-statusline/internal/display"
	"rex-statusline/internal/manager"
	"rex-statusline/internal/private"
	"rex-statusline/internal/render"
	"rex-statusline/internal/rex"
	"rex-statusline/internal/service"
	"rex-statusline/internal/shellinit"
	"rex-statusline/internal/timing"
)

var version = "dev"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "rex-statusline:", err)
		os.Exit(1)
	}
}
func usage() {
	fmt.Println("Usage: rex-statusline [add|remove|manage|render|publish|claim|release|bridge|display|init|config|version] [options]\n\nversion  Print the build version (also --version).\nconfig   Create default configuration: rex-statusline config init.\ninit     Emit shell integration: rex-statusline init bash|zsh.\nadd      Start bars and enable login startup (launchd/systemd).\nmanage   Run the bar manager.\n         Use --foreground to manage floats in this terminal.\nremove   Stop the background service; leave login startup enabled.\nrender   Render one source block once.\npublish  Publish shell context or a claimed remote/container line.\nclaim    Claim a remote/container bridge; print its epoch.\nrelease  Release a bridge epoch.\nbridge   Forward a launch-private guest report inbox.\n\nUse COMMAND --help for options.")
}
func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}
	command := "manage"
	if len(args) > 0 {
		if args[0] == "--help" || args[0] == "-h" {
			usage()
			return nil
		}
		if args[0] == "--version" {
			command = "version"
			args = args[1:]
		} else if args[0] != "" && args[0][0] != '-' {
			command = args[0]
			args = args[1:]
		}
	}
	if command == "version" {
		f := flag.NewFlagSet("version", flag.ContinueOnError)
		if err := f.Parse(args); err != nil {
			return help(err)
		}
		if f.NArg() != 0 {
			return errors.New("usage: rex-statusline version")
		}
		fmt.Println(version)
		return nil
	}
	if command == "add" {
		command = "manage"
	}
	if command == "config" {
		return configCommand(ctx, args)
	}
	if command == "remove" {
		f := flag.NewFlagSet("remove", flag.ContinueOnError)
		if err := f.Parse(args); err != nil {
			return help(err)
		}
		if f.NArg() != 0 {
			return errors.New("usage: rex-statusline remove")
		}
		return service.Stop(ctx)
	}
	if command == "init" {
		if len(args) != 1 {
			return errors.New("usage: rex-statusline init bash|zsh")
		}
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		script, err := shellinit.Script(args[0], executable)
		if err != nil {
			return err
		}
		fmt.Print(script)
		return nil
	}
	if command == "display" {
		f := flag.NewFlagSet("display", flag.ContinueOnError)
		frame := f.String("frame", "", "private frame file")
		timingPath := f.String("timing-log", "", "append correlated update timings as JSON lines")
		server := f.String("server", "", "Rex host label or server address")
		if err := f.Parse(args); err != nil {
			return help(err)
		}
		if *frame == "" || f.NArg() != 0 {
			return errors.New("display requires --frame PATH")
		}
		logger, err := timing.Open(*timingPath)
		if err != nil {
			return err
		}
		defer logger.Close()
		return display.Run(ctx, *frame, rex.Client{Server: *server, Timeout: 2 * time.Second}, logger)
	}
	if command != "manage" && command != "render" && command != "publish" && command != "claim" && command != "release" && command != "bridge" {
		return fmt.Errorf("unknown command %q", command)
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	server := f.String("server", "", "Rex host label or server address")
	session := f.String("session", "", "Rex session ID (manage defaults to all sessions)")
	block := f.String("block", os.Getenv("REX_BLOCK"), "source block ID")
	config := f.String("config", "", "configuration TOML (default XDG path)")
	once := f.Bool("once", false, "reconcile once and leave floats running")
	foreground := f.Bool("foreground", false, "run the manager in this terminal instead of the background service")
	timingPath := f.String("timing-log", "", "append correlated update timings as JSON lines")
	root := f.String("bsp-root", bspRoot(), "private BSP root")
	owner := f.Int("owner", os.Getppid(), "publisher/bridge PID on the Rex server host")
	source := f.String("source", "local", "publisher source: local, remote, container")
	kind := f.String("kind", "context", "report form: context or line")
	epoch := f.String("epoch", "", "remote/container epoch returned by claim")
	inbox := f.String("inbox", "", "launch-private guest report directory (bridge)")
	cwd := f.String("cwd", "", "shell working directory")
	jobs := f.Int("jobs", 0, "shell job count")
	exitCode := f.Int("exit-code", 0, "last command exit code")
	duration := f.Int64("duration-ms", 0, "last command duration")
	if err := f.Parse(args); err != nil {
		return help(err)
	}
	if f.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if command == "manage" && !*once && !*foreground {
		serviceArgs := append([]string{"manage"}, args...)
		serviceArgs = append(serviceArgs, "--foreground")
		for _, option := range []struct{ name, value string }{{"--config", *config}, {"--bsp-root", *root}, {"--timing-log", *timingPath}} {
			if option.value == "" {
				continue
			}
			absolute, err := filepath.Abs(option.value)
			if err != nil {
				return err
			}
			serviceArgs = append(serviceArgs, option.name, absolute)
		}
		if err := service.Ensure(ctx, serviceArgs); err != nil {
			return err
		}
		return nil
	}
	client := rex.Client{Server: *server}
	id, err := client.Identity(ctx)
	if err != nil {
		return err
	}
	if id.Principal.Kind != "peer" || id.Principal.Transport != "unix" {
		return errors.New("run rex-statusline on the Rex server host through its local Unix socket; remote runtime files and PIDs cannot be used safely")
	}
	if command != "manage" && *session == "" {
		*session = os.Getenv("REX_SESSION")
	}
	if command != "manage" && (*session == "" || *block == "") {
		return errors.New("--session and --block (or REX_SESSION/REX_BLOCK) are required")
	}
	if command == "bridge" {
		if !filepath.IsAbs(*inbox) {
			return errors.New("bridge requires --inbox ABSOLUTE_DIRECTORY")
		}
		var view rex.View
		if err := client.Call(ctx, *session, "session.view", map[string]any{}, &view); err != nil {
			return err
		}
		view.SessionID = *session
		sources, _ := manager.Sources(view)
		src, ok := sources[*block]
		if !ok || src.Block.HostID != id.HostID {
			return bsp.ErrOwnership
		}
		validate := func(check context.Context) error {
			var process rex.Process
			if err := client.Call(check, *session, "block.method", map[string]any{"block_id": *block, "method": "process", "args": map[string]any{}}, &process); err != nil {
				return err
			}
			p := app.ProcessContext(process, false)
			if !p.PIDs[*owner] || p.ShellPID == *owner {
				return bsp.ErrOwnership
			}
			return nil
		}
		b := bridge.Bridge{Store: bsp.Store{Root: *root, ServerID: id.InstanceID, SessionID: *session}, Block: *block, Owner: *owner, Source: *source, Inbox: *inbox, Validate: validate, Notify: func() { notify(*session, *block) }, Log: func(err error) { fmt.Fprintln(os.Stderr, "rex-statusline bridge:", err) }}
		return b.Run(ctx)
	}
	if command == "publish" || command == "claim" || command == "release" {
		return publication(ctx, client, id, *root, *session, *block, *owner, *source, *kind, *epoch, *cwd, *jobs, *exitCode, *duration, command)
	}
	cfg, err := render.Load(*config)
	if err != nil {
		return err
	}
	renderer, err := render.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer renderer.Close()
	resolver := app.Resolver{API: client, Identity: id, Root: *root, Renderer: renderer}
	if command == "render" {
		var view rex.View
		if err = client.Call(ctx, *session, "session.view", map[string]any{}, &view); err != nil {
			return err
		}
		view.SessionID = *session
		sources, _ := manager.Sources(view)
		s, exists := sources[*block]
		if !exists {
			return errors.New("source terminal not found")
		}
		if err = client.Call(ctx, *session, "block.method", map[string]any{"block_id": *block, "method": "size", "args": map[string]any{}}, &s.Size); err != nil {
			return err
		}
		line, err := resolver.Render(ctx, s)
		if err == nil {
			fmt.Println(line)
		}
		return err
	}
	directory, err := private.Runtime(id.InstanceID)
	if err != nil {
		return err
	}
	lock, err := private.Lock(directory)
	if err != nil {
		return err
	}
	defer lock.Close()
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}
	statePath, err := manager.StatePath()
	if err != nil {
		return err
	}
	logger, err := timing.Open(*timingPath)
	if err != nil {
		return err
	}
	defer logger.Close()
	if logger != nil {
		fmt.Fprintln(os.Stderr, "rex-statusline timing log:", logger.Path)
	}
	m := &manager.Manager{Timing: logger, Placement: renderer.Placement, HorizontalPadding: rex.TerminalPaddingX, Padding: rex.TerminalPadding, StatePath: statePath, API: client, DisplayServer: *server, Executable: executable, Directory: directory, Session: *session, Render: resolver.Render, Prepare: resolver.Prepare,
		Log: func(err error) { fmt.Fprintln(os.Stderr, "rex-statusline:", err) }}
	if *once {
		return m.Reconcile(ctx)
	}
	watchCtx, cancelWatch := context.WithCancel(ctx)
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		renderer.Watch(watchCtx, *config, func(err error) {
			fmt.Fprintln(os.Stderr, "rex-statusline config:", err)
		})
	}()
	defer func() {
		cancelWatch()
		<-watchDone
	}()
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		m.Cleanup(cleanup)
	}()
	eventCtx, cancelEvents := context.WithCancel(ctx)
	defer cancelEvents()
	notifications := make(chan rex.Event, 1)
	eventsDone := make(chan error, 1)
	go func() { eventsDone <- client.Events(eventCtx, notifications) }()
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	managerDone := make(chan error, 1)
	go func() { managerDone <- m.Run(runCtx, notifications) }()
	select {
	case err := <-managerDone:
		return err
	case err := <-eventsDone:
		cancelRun()
		<-managerDone
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("Rex event connection lost; restart the manager: %w", err)
	}
}
func help(err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}
func bspRoot() string {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "bsp")
}
func publication(ctx context.Context, client rex.Client, id rex.Identity, root, session, block string, owner int, source, kind, epoch, cwd string, jobs, exitCode int, duration int64, command string) error {
	var view rex.View
	if err := client.Call(ctx, session, "session.view", map[string]any{}, &view); err != nil {
		return err
	}
	view.SessionID = session
	sources, _ := manager.Sources(view)
	s, ok := sources[block]
	if !ok {
		return errors.New("publisher source terminal not found")
	}
	resolver := app.Resolver{API: client, Identity: id}
	_, p, err := resolver.Process(ctx, s)
	if err != nil {
		return err
	}
	if s.Block.HostID != id.HostID || !p.PIDs[owner] {
		return bsp.ErrOwnership
	}
	store := bsp.Store{Root: root, ServerID: id.InstanceID, SessionID: session}
	if command == "release" {
		err := store.Release(block, owner, epoch)
		if err == nil {
			notify(session, block)
		}
		return err
	}
	if command == "claim" {
		if owner == p.ShellPID {
			return errors.New("bridge must be owned by a launcher process, not the parent interactive shell")
		}
		value, err := store.Claim(block, owner, source)
		if err == nil {
			fmt.Println(value)
			notify(session, block)
		}
		return err
	}
	if source == "local" && (p.Remote || owner != p.ShellPID) {
		return bsp.ErrOwnership
	}
	if source != "local" && owner == p.ShellPID {
		return bsp.ErrOwnership
	}
	report := bsp.Report{Kind: kind, Source: source, Epoch: epoch}
	if kind == "line" {
		data, err := io.ReadAll(io.LimitReader(os.Stdin, bsp.MaxBytes+1))
		if err != nil {
			return err
		}
		if len(data) > bsp.MaxBytes {
			return errors.New("published line too large")
		}
		report.Line = string(data)
	} else {
		if cwd == "" {
			cwd, err = os.Getwd()
			if err != nil {
				return err
			}
		}
		env := map[string]string{}
		for _, name := range []string{"PATH", "HOME", "LANG", "LC_ALL", "VIRTUAL_ENV", "FNOX_SHELL", "POSH_HOST_LABEL", "SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY", "PYENV_VERSION", "MISE_ENV", "GOPATH", "GOROOT"} {
			if value, exists := os.LookupEnv(name); exists {
				env[name] = value
			}
		}
		report.Context = bsp.Context{Cwd: cwd, Env: env, Jobs: jobs, ExitCode: exitCode, DurationMS: duration}
	}
	if err := store.Publish(block, owner, source, report); err != nil {
		return err
	}
	notify(session, block)
	return nil
}
func notify(session, block string) {
	// Keep stdout machine-readable, including when a launcher captures its epoch.
	if os.Getenv("REX_BLOCK") != block || os.Getenv("REX_SESSION") != session {
		return
	}
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return
	}
	defer tty.Close()
	fmt.Fprint(tty, "\x1b]7501;state=idle:id=rex-bsp:app=rex-statusline\x1b\\")
}

func configCommand(ctx context.Context, args []string) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Println("Usage: rex-statusline config init\n\nCreate config.toml and oh-my-posh.toml with defaults at the XDG path; confirm before replacing existing files.")
		return nil
	}
	if len(args) == 0 || args[0] != "init" {
		return errors.New("usage: rex-statusline config init")
	}
	f := flag.NewFlagSet("config init", flag.ContinueOnError)
	if err := f.Parse(args[1:]); err != nil {
		return help(err)
	}
	if f.NArg() != 0 {
		return errors.New("usage: rex-statusline config init")
	}
	return initConfig(ctx, os.Stdin, os.Stdout)
}

func initConfig(ctx context.Context, input io.Reader, output io.Writer) error {
	path, err := render.DefaultConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	files := []struct {
		path string
		data []byte
	}{
		{path, defaults.Config},
		{filepath.Join(filepath.Dir(path), "oh-my-posh.toml"), defaults.Theme},
	}
	existing := make(map[string]bool)
	selected := make(map[string]bool)
	reader := bufio.NewReader(input)
	interactive := false
	var terminalState *term.State
	var terminalFD int
	defer func() {
		if terminalState != nil {
			term.Restore(terminalFD, terminalState)
		}
	}()
	for _, entry := range files {
		info, err := os.Lstat(entry.path)
		if errors.Is(err, os.ErrNotExist) {
			selected[entry.path] = true
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("not a regular configuration file: %s", entry.path)
		}
		existing[entry.path] = true
		if file, ok := input.(*os.File); ok && terminalState == nil && term.IsTerminal(int(file.Fd())) {
			terminalFD = int(file.Fd())
			terminalState, err = term.MakeRaw(terminalFD)
			if err != nil {
				return err
			}
			interactive = true
		}
		fmt.Fprintf(output, "Replace %s with defaults? [y/N] (Esc/Ctrl-C cancels) ", entry.path)
		confirmed, err := confirmReplacement(ctx, reader, output, interactive)
		if err != nil {
			return err
		}
		selected[entry.path] = confirmed
	}
	if terminalState != nil {
		if err := term.Restore(terminalFD, terminalState); err != nil {
			return err
		}
		terminalState = nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Roll back newly created files on failure.
	var created []string
	ok := false
	defer func() {
		if !ok {
			for _, path := range created {
				os.Remove(path)
			}
		}
	}()
	for _, entry := range files {
		if !selected[entry.path] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
		if existing[entry.path] {
			flags = os.O_WRONLY | os.O_TRUNC
		}
		file, err := os.OpenFile(entry.path, flags, 0644)
		if err != nil {
			return err
		}
		if !existing[entry.path] {
			created = append(created, entry.path)
		}
		_, writeErr := file.Write(entry.data)
		if err := errors.Join(writeErr, file.Close()); err != nil {
			return err
		}
	}
	ok = true
	for _, entry := range files {
		if selected[entry.path] {
			fmt.Fprintln(output, entry.path)
		}
	}
	return nil
}

func readConfirmation(reader *bufio.Reader, output io.Writer, interactive bool) (string, error) {
	var answer []byte
	for {
		key, err := reader.ReadByte()
		if errors.Is(err, io.EOF) {
			return string(answer), nil
		}
		if err != nil {
			return "", err
		}
		switch key {
		case 0x1b, 0x03:
			if interactive {
				fmt.Fprint(output, "\r\n")
			}
			return "", context.Canceled
		case '\n', '\r':
			if key == '\r' && !interactive {
				continue
			}
			if interactive {
				fmt.Fprint(output, "\r\n")
			}
			return string(answer), nil
		case 0x04:
			return string(answer), nil
		case 0x7f, '\b':
			if len(answer) > 0 {
				answer = answer[:len(answer)-1]
				if interactive {
					fmt.Fprint(output, "\b \b")
				}
			}
		default:
			if key >= 0x20 && len(answer) < 1024 {
				answer = append(answer, key)
				if interactive {
					fmt.Fprintf(output, "%c", key)
				}
			}
		}
	}
}

func confirmReplacement(ctx context.Context, reader *bufio.Reader, output io.Writer, interactive bool) (bool, error) {
	type response struct {
		answer string
		err    error
	}
	result := make(chan response, 1)
	go func() {
		answer, err := readConfirmation(reader, output, interactive)
		result <- response{answer: strings.TrimSpace(answer), err: err}
	}()
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case reply := <-result:
		return strings.EqualFold(reply.answer, "y") || strings.EqualFold(reply.answer, "yes"), reply.err
	}
}
