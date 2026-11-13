package private

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"syscall"

	"golang.org/x/sys/unix"
)

var identifier = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

func Runtime(server string) (string, error) {
	if !identifier.MatchString(server) {
		return "", fmt.Errorf("invalid server identity")
	}
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		cache = filepath.Join(home, ".cache")
	}
	if !filepath.IsAbs(cache) {
		return "", fmt.Errorf("XDG_CACHE_HOME must be absolute")
	}
	if err := os.MkdirAll(cache, 0700); err != nil {
		return "", err
	}
	path := cache
	for _, name := range []string{"rex-statusline", "runtime", server} {
		path = filepath.Join(path, name)
		if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
			return "", err
		}
		stat, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		owner, ok := stat.Sys().(*syscall.Stat_t)
		if !stat.IsDir() || stat.Mode()&os.ModeSymlink != 0 || !ok || int(owner.Uid) != os.Geteuid() {
			return "", fmt.Errorf("unsafe runtime directory: %s", path)
		}
		if err = os.Chmod(path, 0700); err != nil {
			return "", err
		}
	}
	return path, nil
}
func Lock(directory string) (*os.File, error) {
	path := filepath.Join(directory, "manager.lock")
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || int(st.Uid) != os.Geteuid() {
		f.Close()
		return nil, fmt.Errorf("unsafe manager lock")
	}
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another rex-statusline manager is running for this server: %w", err)
	}
	return f, nil
}
