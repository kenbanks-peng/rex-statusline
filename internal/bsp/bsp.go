// Package bsp stores private, block-scoped status reports.
package bsp

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const (
	Version  = 1
	MaxBytes = 64 << 10
	// TTL bounds idle local reports without requiring a persistent shell daemon.
	TTL       = 24 * time.Hour
	RemoteTTL = 2 * time.Minute
)

var (
	ErrInvalid   = errors.New("invalid BSP report")
	ErrOwnership = errors.New("BSP ownership mismatch")
	ErrConflict  = errors.New("BSP claim superseded")
)

type Context struct {
	Cwd        string            `json:"cwd"`
	Env        map[string]string `json:"env"`
	Jobs       int               `json:"jobs"`
	ExitCode   int               `json:"exit_code"`
	DurationMS int64             `json:"duration_ms"`
}
type Report struct {
	Kind    string  `json:"kind"`
	Context Context `json:"context"`
	Line    string  `json:"line"`
	Source  string  `json:"source"`
	// Epoch is required for remote/container updates, obtained from Claim.
	Epoch string `json:"epoch,omitempty"`
}
type Process struct {
	PIDs     map[int]bool
	ShellPID int
	Remote   bool
}
type Store struct {
	Root      string
	ServerID  string
	SessionID string
}
type record struct {
	Version int       `json:"version"`
	Server  string    `json:"server"`
	Session string    `json:"session"`
	Block   string    `json:"block"`
	Owner   int       `json:"owner"`
	Epoch   string    `json:"epoch"`
	Updated time.Time `json:"updated"`
	Report  Report    `json:"report"`
}

func validID(s string) bool {
	if len(s) == 0 || len(s) > 200 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_:", c)) {
			return false
		}
	}
	return true
}
func alive(pid int) bool {
	return pid > 1 && (unix.Kill(pid, 0) == nil || unix.Kill(pid, 0) == syscall.EPERM)
}
func nonce() (string, error) {
	b := make([]byte, 16)
	_, e := rand.Read(b)
	return hex.EncodeToString(b), e
}
func validReport(r Report) error {
	if r.Source != "local" && r.Source != "remote" && r.Source != "container" {
		return fmt.Errorf("%w: source", ErrInvalid)
	}
	if r.Kind != "context" && r.Kind != "line" {
		return fmt.Errorf("%w: kind", ErrInvalid)
	}
	if !utf8.ValidString(r.Line) || !utf8.ValidString(r.Context.Cwd) || strings.ContainsRune(r.Context.Cwd, 0) {
		return ErrInvalid
	}
	if r.Kind == "context" {
		if r.Source != "local" || !strings.HasPrefix(r.Context.Cwd, "/") || r.Line != "" || r.Context.Jobs < 0 || r.Context.DurationMS < 0 {
			return ErrInvalid
		}
	} else if r.Context.Cwd != "" || len(r.Context.Env) > 0 || r.Context.Jobs != 0 || r.Context.ExitCode != 0 || r.Context.DurationMS != 0 {
		return ErrInvalid
	}
	if len(r.Context.Env) > 64 {
		return ErrInvalid
	}
	for k, v := range r.Context.Env {
		if len(k) == 0 || len(k) > 128 || strings.ContainsAny(k, "=\x00") || strings.ContainsRune(v, 0) || !utf8.ValidString(k) || !utf8.ValidString(v) {
			return ErrInvalid
		}
	}
	return nil
}
func private(fd int, directory bool) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	kind := uint32(unix.S_IFREG)
	if directory {
		kind = unix.S_IFDIR
	}
	if uint32(st.Mode)&unix.S_IFMT != kind || st.Mode&0777 != 0600 && !directory || directory && st.Mode&0777 != 0700 || st.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("%w: unsafe permissions or file type", ErrInvalid)
	}
	return nil
}

// directory opens each owned component with O_NOFOLLOW. Parent directories of
// Root are administrator/user-selected; Root and everything below it is private.
func (s Store) directory(block string, create bool) (int, error) {
	if !strings.HasPrefix(s.Root, "/") || !validID(s.ServerID) || !validID(s.SessionID) || !validID(block) {
		return -1, ErrInvalid
	}
	if create {
		if e := os.MkdirAll(s.Root, 0700); e != nil {
			return -1, e
		}
	}
	fd, e := unix.Open(s.Root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return -1, e
	}
	if e = private(fd, true); e != nil {
		unix.Close(fd)
		return -1, e
	}
	for _, part := range []string{s.ServerID, s.SessionID, block} {
		if create {
			e = unix.Mkdirat(fd, part, 0700)
			if e != nil && e != unix.EEXIST {
				unix.Close(fd)
				return -1, e
			}
		}
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if err != nil {
			return -1, err
		}
		fd = next
		if err = private(fd, true); err != nil {
			unix.Close(fd)
			return -1, err
		}
	}
	return fd, nil
}
func load(fd int) (record, error) {
	var r record
	n, e := unix.Openat(fd, "report.json", unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return r, e
	}
	f := os.NewFile(uintptr(n), "report.json")
	defer f.Close()
	if e = private(n, false); e != nil {
		return r, e
	}
	data, e := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if e != nil {
		return r, e
	}
	if len(data) > MaxBytes || !utf8.Valid(data) {
		return r, ErrInvalid
	}
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if e = d.Decode(&r); e != nil {
		return r, fmt.Errorf("%w: JSON: %v", ErrInvalid, e)
	}
	if d.Decode(new(any)) != io.EOF {
		return r, ErrInvalid
	}
	return r, nil
}
func (s Store) valid(r record, block string) error {
	if r.Version != Version || r.Server != s.ServerID || r.Session != s.SessionID || r.Block != block || r.Owner <= 1 || len(r.Epoch) != 32 || r.Report.Epoch != r.Epoch {
		return ErrInvalid
	}
	if _, e := hex.DecodeString(r.Epoch); e != nil {
		return ErrInvalid
	}
	if e := validReport(r.Report); e != nil {
		return e
	}
	ttl := TTL
	if r.Report.Source != "local" {
		ttl = RemoteTTL
	}
	age := time.Since(r.Updated)
	if r.Updated.IsZero() || age < -time.Minute {
		return ErrInvalid
	}
	if age > ttl || !alive(r.Owner) {
		return fmt.Errorf("%w: expired BSP owner/report", os.ErrNotExist)
	}
	return nil
}
func (s Store) Read(block string, p Process) (Report, error) {
	fd, e := s.directory(block, false)
	if e != nil {
		return Report{}, e
	}
	defer unix.Close(fd)
	r, e := load(fd)
	if e != nil {
		return Report{}, e
	}
	if e = s.valid(r, block); e != nil {
		return Report{}, e
	}
	if !p.PIDs[r.Owner] || r.Report.Source == "local" && (r.Owner != p.ShellPID || p.Remote) || r.Report.Source != "local" && r.Owner == p.ShellPID {
		return Report{}, ErrOwnership
	}
	return r.Report, nil
}
func lock(fd int) (func(), error) {
	n, e := unix.Openat(fd, ".lock", unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return nil, e
	}
	if e = private(n, false); e != nil {
		unix.Close(n)
		return nil, e
	}
	if e = unix.Flock(n, unix.LOCK_EX); e != nil {
		unix.Close(n)
		return nil, e
	}
	return func() { unix.Flock(n, unix.LOCK_UN); unix.Close(n) }, nil
}
func save(fd int, r record) error {
	data, e := json.Marshal(r)
	if e != nil {
		return e
	}
	if len(data) > MaxBytes || !utf8.Valid(data) {
		return ErrInvalid
	}
	id, e := nonce()
	if e != nil {
		return e
	}
	name := ".report-" + id
	n, e := unix.Openat(fd, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return e
	}
	defer unix.Unlinkat(fd, name, 0)
	f := os.NewFile(uintptr(n), name)
	_, e = f.Write(data)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	if e = unix.Renameat(fd, name, fd, "report.json"); e != nil {
		return e
	}
	return unix.Fsync(fd)
}
func (s Store) makeRecord(block string, owner int, source, epoch string, r Report) record {
	r.Source = source
	r.Epoch = epoch
	return record{Version: Version, Server: s.ServerID, Session: s.SessionID, Block: block, Owner: owner, Epoch: epoch, Updated: time.Now().UTC(), Report: r}
}

// Publish commits local context or updates a claimed bridge. Remote/container
// publishers must supply Report.Epoch from Claim; stale writers cannot reclaim.
func (s Store) Publish(block string, owner int, source string, r Report) error {
	r.Source = source
	if owner <= 1 || !alive(owner) {
		return ErrOwnership
	}
	if e := validReport(r); e != nil {
		return e
	}
	fd, e := s.directory(block, true)
	if e != nil {
		return e
	}
	defer unix.Close(fd)
	unlock, e := lock(fd)
	if e != nil {
		return e
	}
	defer unlock()
	previous, pe := load(fd)
	if pe != nil && !errors.Is(pe, os.ErrNotExist) {
		return pe
	}
	epoch := r.Epoch
	if source != "local" {
		if pe != nil || epoch == "" || previous.Epoch != epoch || previous.Owner != owner || previous.Report.Source != source {
			return ErrConflict
		}
	} else {
		if pe == nil && previous.Report.Source != "local" && alive(previous.Owner) {
			return ErrConflict
		}
		epoch, e = nonce()
		if e != nil {
			return e
		}
	}
	return save(fd, s.makeRecord(block, owner, source, epoch, r))
}

// Claim starts a fresh bridge epoch and commits an empty pending line. Only
// launchers call Claim, once per launch; guest updates never call it.
func (s Store) Claim(block string, owner int, source string) (string, error) {
	if owner <= 1 || !alive(owner) || source != "remote" && source != "container" {
		return "", ErrOwnership
	}
	fd, e := s.directory(block, true)
	if e != nil {
		return "", e
	}
	defer unix.Close(fd)
	unlock, e := lock(fd)
	if e != nil {
		return "", e
	}
	defer unlock()
	epoch, e := nonce()
	if e != nil {
		return "", e
	}
	e = save(fd, s.makeRecord(block, owner, source, epoch, Report{Kind: "line"}))
	return epoch, e
}

// Release clears only its own epoch; it never restores an old report.
func (s Store) Release(block string, owner int, epoch string) error {
	fd, e := s.directory(block, false)
	if e != nil {
		return e
	}
	defer unix.Close(fd)
	unlock, e := lock(fd)
	if e != nil {
		return e
	}
	defer unlock()
	r, e := load(fd)
	if e != nil {
		return e
	}
	if r.Owner != owner || r.Epoch != epoch {
		return ErrConflict
	}
	if e = unix.Unlinkat(fd, "report.json", 0); e != nil {
		return e
	}
	return unix.Fsync(fd)
}
