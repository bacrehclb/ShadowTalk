// Package torbundle locates the Tor Expert Bundle shipped with ShadowTalk.
// It never downloads, executes, or installs programs and never changes profiles.
package torbundle

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

var ErrIncomplete = errors.New("bundled Tor runtime is missing or incomplete")

type Paths struct {
	Tor      string
	Lyrebird string
}

// Locate follows the application executable, not cwd or the profile directory.
// An absent bundle allows source builds to use PATH. A damaged installed bundle
// is an error: silently switching to an unrelated system Tor would hide damage.
func Locate(executable, targetOS, arch string) (Paths, error) {
	executable, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return Paths{}, ErrIncomplete
	}
	dir := filepath.Dir(executable)
	roots := []string{filepath.Join(dir, "tor-runtime"), filepath.Join(dir, "bin", "tor-runtime")}
	if targetOS == "darwin" && filepath.Base(dir) == "MacOS" && filepath.Base(filepath.Dir(dir)) == "Contents" {
		roots = append(roots, filepath.Join(dir, "..", "Resources", "tor-runtime"))
	}
	if targetOS != "windows" && filepath.Base(dir) == "bin" {
		roots = append(roots, filepath.Join(dir, "..", "lib", "shadowtalk", "tor-runtime"))
	}
	for _, root := range roots {
		info, err := os.Lstat(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.IsDir() {
			return Paths{}, ErrIncomplete
		}
		suffix := ""
		if targetOS == "windows" {
			suffix = ".exe"
		}
		base := filepath.Join(root, targetOS+"-"+arch, "tor")
		paths := Paths{Tor: filepath.Join(base, "tor"+suffix), Lyrebird: filepath.Join(base, "pluggable_transports", "lyrebird"+suffix)}
		for _, path := range []string{paths.Tor, paths.Lyrebird} {
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && targetOS != "windows" && info.Mode().Perm()&0111 == 0) {
				return Paths{}, ErrIncomplete
			}
		}
		return paths, nil
	}
	return Paths{Tor: "tor", Lyrebird: "lyrebird"}, nil
}
