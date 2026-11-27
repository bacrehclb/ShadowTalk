package torconfig

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/gofrs/flock"
)

var (
	ErrWrite   = errors.New("cannot save Tor configuration")
	ErrChanged = errors.New("Tor configuration changed; reload before saving")
	ErrBusy    = errors.New("Tor configuration is being edited")
)

func Defaults() Settings {
	return Settings{BinaryPath: "tor", StartTimeout: 3 * time.Minute}
}

// Write replaces a validated configuration after checking the editor's original
// snapshot. A nil previous means the file did not exist. It never repairs or
// overwrites malformed files. Temporary files and the lock are owner-only.
func Write(path string, previous *Settings, next Settings) (err error) {
	data, err := encode(next)
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return ErrWrite
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() {
		return ErrUnsafeFile
	}
	lockPath := filepath.Join(directory, ".tor-config.lock")
	if info, err := os.Lstat(lockPath); err == nil {
		if !safeFile(info) {
			return ErrUnsafeFile
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrWrite
	}
	lock := flock.New(lockPath, flock.SetPermissions(0600))
	defer func() {
		if lock.Close() != nil {
			err = errors.Join(err, ErrWrite)
		}
	}()
	locked, err := lock.TryLock()
	if err != nil {
		return ErrWrite
	}
	if !locked {
		return ErrBusy
	}
	current, readErr := Read(path)
	switch {
	case errors.Is(readErr, os.ErrNotExist):
		if previous != nil {
			return ErrChanged
		}
	case readErr != nil:
		return readErr
	case previous == nil || !reflect.DeepEqual(current, *previous):
		return ErrChanged
	}
	file, err := os.CreateTemp(directory, ".tor-config-*.tmp")
	if err != nil {
		return ErrWrite
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return ErrWrite
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return ErrWrite
	}
	if err := file.Close(); err != nil {
		return ErrWrite
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return ErrWrite
	}
	return nil
}

func encode(s Settings) ([]byte, error) {
	mode := s.Connection.Mode
	if mode == "" {
		mode = "direct"
	}
	torBinary, lyrebird := s.BinaryPath, s.Connection.LyrebirdBinary
	// These sentinel values mean automatic discovery, not relative file paths.
	if torBinary == "tor" {
		torBinary = ""
	}
	if lyrebird == "lyrebird" {
		lyrebird = ""
	}
	if s.BinaryPath == "" || (torBinary != "" && !filepath.IsAbs(torBinary)) ||
		(lyrebird != "" && !filepath.IsAbs(lyrebird)) {
		return nil, ErrInvalid
	}
	record := struct {
		Version  int      `json:"version"`
		Mode     string   `json:"mode"`
		Timeout  string   `json:"startup_timeout"`
		Tor      string   `json:"tor_binary,omitempty"`
		Lyrebird string   `json:"lyrebird_binary,omitempty"`
		Bridges  []string `json:"bridges,omitempty"`
	}{1, mode, s.StartTimeout.String(), torBinary, lyrebird, s.Connection.Bridges}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil || len(data)+1 > maxSize {
		return nil, ErrInvalid
	}
	if _, err := decode(data, ""); err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
