// Package bridge forwards a launch-private guest inbox without exposing host
// block identity or claim tokens to the guest.
package bridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
	"rex-statusline/internal/bsp"
)

type Message struct {
	Version int       `json:"version"`
	Kind    string    `json:"kind"`
	Line    string    `json:"line"`
	Updated time.Time `json:"updated"`
}
type Bridge struct {
	Store    bsp.Store
	Block    string
	Owner    int
	Source   string
	Inbox    string
	Validate func(context.Context) error
	Notify   func()
	Log      func(error)
}

func Parse(data []byte, now time.Time) (Message, error) {
	var m Message
	if len(data) > bsp.MaxBytes || !utf8.Valid(data) {
		return m, bsp.ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return m, fmt.Errorf("guest report: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return m, bsp.ErrInvalid
	}
	age := now.Sub(m.Updated)
	if m.Version != 1 || m.Kind != "line" || m.Updated.IsZero() || age < -time.Minute || age > bsp.RemoteTTL {
		return m, bsp.ErrInvalid
	}
	return m, nil
}
func read(fd int) ([]byte, error) {
	n, err := unix.Openat(fd, "report.json", unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(n), "report.json")
	defer file.Close()
	var st unix.Stat_t
	if err = unix.Fstat(n, &st); err != nil {
		return nil, err
	}
	if uint32(st.Mode)&unix.S_IFMT != unix.S_IFREG || st.Size > bsp.MaxBytes {
		return nil, bsp.ErrInvalid
	}
	return io.ReadAll(io.LimitReader(file, bsp.MaxBytes+1))
}
func (b Bridge) Run(ctx context.Context) error {
	if b.Source != "remote" && b.Source != "container" {
		return bsp.ErrInvalid
	}
	if b.Validate != nil {
		if err := b.Validate(ctx); err != nil {
			return err
		}
	}
	fd, err := unix.Open(b.Inbox, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open guest inbox: %w", err)
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		return err
	}
	if st.Mode&0777 != 0700 || int(st.Uid) != os.Geteuid() {
		return errors.New("guest inbox must be owned by this user with mode 0700")
	}
	epoch, err := b.Store.Claim(b.Block, b.Owner, b.Source)
	if err != nil {
		return err
	}
	notify := func() {
		if b.Notify != nil {
			b.Notify()
		}
	}
	log := func(err error) {
		if err != nil && b.Log != nil {
			b.Log(err)
		}
	}
	notify()
	defer func() {
		err := b.Store.Release(b.Block, b.Owner, epoch)
		if err == nil {
			notify()
		} else if !errors.Is(err, bsp.ErrConflict) && !errors.Is(err, os.ErrNotExist) {
			log(err)
		}
	}()
	var previous [32]byte
	havePrevious := false
	previousError := ""
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	validate := time.NewTicker(2 * time.Second)
	defer validate.Stop()
	for {
		data, err := read(fd)
		if err == nil {
			previousError = ""
			hash := sha256.Sum256(data)
			if !havePrevious || hash != previous {
				previous = hash
				havePrevious = true
				message, err := Parse(data, time.Now())
				if err == nil {
					// New guest timestamp/content only. A silent guest never gets a heartbeat
					// merely because the host's polling loop is still alive.
					err = b.Store.Publish(b.Block, b.Owner, b.Source, bsp.Report{Kind: "line", Line: message.Line, Epoch: epoch})
					if err == nil {
						notify()
					}
				}
				log(err)
				if errors.Is(err, bsp.ErrConflict) {
					return err
				}
			}
		} else if !errors.Is(err, os.ErrNotExist) && err.Error() != previousError {
			previousError = err.Error()
			log(err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		case <-validate.C:
			if b.Validate != nil {
				if err = b.Validate(ctx); err != nil {
					return err
				}
			}
		}
	}
}
