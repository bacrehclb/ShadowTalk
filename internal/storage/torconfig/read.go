// Package torconfig reads a user-maintained, versioned connection file. It does
// not write profiles or expose arbitrary Tor directives.
package torconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"

	"github.com/bacrehclb/ShadowTalk/internal/transport/tor"
)

var (
	ErrRead       = errors.New("cannot read Tor configuration")
	ErrUnsafeFile = errors.New("unsafe Tor configuration file")
	ErrInvalid    = errors.New("invalid Tor configuration")
	ErrVersion    = errors.New("unsupported Tor configuration version")
)

const FileName = "tor.json"
const maxSize = 16384

type Settings struct {
	BinaryPath   string
	StartTimeout time.Duration
	Connection   tor.Connection
}

// Read returns os.ErrNotExist only for an absent path. A broken symlink, unsafe
// permissions or malformed file must never silently select a direct connection.
func Read(path string) (Settings, error) {
	var empty Settings
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return empty, os.ErrNotExist
	}
	if err != nil {
		return empty, ErrRead
	}
	if !safeFile(info) {
		return empty, ErrUnsafeFile
	}
	file, err := os.Open(path)
	if err != nil {
		return empty, ErrRead
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !safeFile(opened) || !os.SameFile(info, opened) {
		return empty, ErrUnsafeFile
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil {
		return empty, ErrRead
	}
	if len(data) > maxSize {
		return empty, ErrInvalid
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return empty, ErrRead
	}
	return decode(data, filepath.Dir(absolute))
}

func safeFile(info os.FileInfo) bool {
	return info.Mode().IsRegular() && info.Size() <= maxSize && (runtime.GOOS == "windows" || info.Mode().Perm()&0077 == 0)
}

func decode(data []byte, base string) (Settings, error) {
	s := Settings{BinaryPath: "tor", StartTimeout: 3 * time.Minute}
	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return s, ErrInvalid
	}
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return s, ErrInvalid
		}
		seen[key] = true
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil || bytes.Equal(raw, []byte("null")) {
			return s, ErrInvalid
		}
		switch key {
		case "version":
			var version int
			if json.Unmarshal(raw, &version) != nil {
				return s, ErrInvalid
			}
			if version != 1 {
				return s, ErrVersion
			}
		case "mode":
			if json.Unmarshal(raw, &s.Connection.Mode) != nil || s.Connection.Mode == "" {
				return s, ErrInvalid
			}
		case "bridges":
			if json.Unmarshal(raw, &s.Connection.Bridges) != nil {
				return s, ErrInvalid
			}
		case "tor_binary", "lyrebird_binary":
			var path string
			if json.Unmarshal(raw, &path) != nil || path == "" || strings.ContainsFunc(path, unicode.IsControl) {
				return s, ErrInvalid
			}
			if !filepath.IsAbs(path) {
				path = filepath.Join(base, path)
			}
			if key == "tor_binary" {
				s.BinaryPath = path
			} else {
				s.Connection.LyrebirdBinary = path
			}
		case "startup_timeout":
			var duration string
			if json.Unmarshal(raw, &duration) != nil {
				return s, ErrInvalid
			}
			s.StartTimeout, err = time.ParseDuration(duration)
			if err != nil || s.StartTimeout < time.Second || s.StartTimeout > 15*time.Minute {
				return s, ErrInvalid
			}
		default:
			return s, ErrInvalid
		}
	}
	if _, err := decoder.Token(); err != nil {
		return s, ErrInvalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || !seen["version"] || !seen["mode"] {
		return s, ErrInvalid
	}
	// An omitted executable is selected by application wiring (bundle, then
	// PATH). Explicit relative JSON paths are already absolute, so remain exact.
	if s.Connection.Mode == "obfs4" && !seen["lyrebird_binary"] {
		s.Connection.LyrebirdBinary = "lyrebird"
	}
	if s.Connection.Validate() != nil {
		return s, ErrInvalid
	}
	return s, nil
}
