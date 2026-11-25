package sqlite

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/bacrehclb/ShadowTalk/internal/history"
	"github.com/gofrs/flock"
)

func prepare(directory string) (_ *flock.Flock, err error) {
	if !localDirectory(directory) {
		return nil, history.ErrUnsafePath
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	info, err := os.Stat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, history.ErrUnsafePath
	}
	for _, name := range []string{fileName, fileName + "-wal", fileName + "-shm", fileName + "-journal", ".messages.lock"} {
		info, err := os.Lstat(filepath.Join(directory, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
			return nil, history.ErrUnsafePath
		}
	}
	lock := flock.New(filepath.Join(directory, ".messages.lock"), flock.SetPermissions(0600))
	locked, err := lock.TryLock()
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	if !locked {
		_ = lock.Close()
		return nil, history.ErrBusy
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, lock.Close())
		}
	}()
	// SQLite inherits mode 0600 for its sidecars. Pre-create the database rather
	// than relying on a process-wide umask, and never truncate an existing file.
	file, err := os.OpenFile(filepath.Join(directory, fileName), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if errors.Is(err, os.ErrExist) {
		return lock, nil
	}
	if err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	return lock, nil
}
