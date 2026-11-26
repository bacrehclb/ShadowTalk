package torbundle

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fixture"), 0755); err != nil {
		t.Fatal(err)
	}
}

func TestLocateRelocatedPackages(t *testing.T) {
	for _, test := range []struct{ name, executable, bundle, goos, arch string }{
		{"portable", "shadowtalk", "bin/tor-runtime", "linux", "amd64"},
		{"windows", "shadowtalk.exe", "bin/tor-runtime", "windows", "amd64"},
		{"app", "Moved App.app/Contents/MacOS/shadowtalk", "Moved App.app/Contents/Resources/tor-runtime", "darwin", "arm64"},
		{"deb", "usr/bin/shadowtalk", "usr/lib/shadowtalk/tor-runtime", "linux", "arm64"},
		{"pkg", "usr/local/bin/shadowtalk", "usr/local/lib/shadowtalk/tor-runtime", "darwin", "amd64"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "папка с пробелами")
			executable := filepath.Join(root, filepath.FromSlash(test.executable))
			writeExecutable(t, executable)
			base := filepath.Join(root, filepath.FromSlash(test.bundle), test.goos+"-"+test.arch, "tor")
			suffix := ""
			if test.goos == "windows" {
				suffix = ".exe"
			}
			want := Paths{filepath.Join(base, "tor"+suffix), filepath.Join(base, "pluggable_transports", "lyrebird"+suffix)}
			writeExecutable(t, want.Tor)
			writeExecutable(t, want.Lyrebird)
			got, err := Locate(executable, test.goos, test.arch)
			// macOS resolves /var to /private/var; compare resolved paths.
			want.Tor, _ = filepath.EvalSymlinks(want.Tor)
			want.Lyrebird, _ = filepath.EvalSymlinks(want.Lyrebird)
			if err != nil || got != want {
				t.Fatal(got, want, err)
			}
			if err := os.Remove(want.Lyrebird); err != nil {
				t.Fatal(err)
			}
			if _, err := Locate(executable, test.goos, test.arch); !errors.Is(err, ErrIncomplete) {
				t.Fatal("damaged bundle fell back", err)
			}
		})
	}
}

func TestLocateSourceBuildAndExecutableSymlink(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "app", "shadowtalk")
	writeExecutable(t, executable)
	got, err := Locate(executable, "linux", "amd64")
	if err != nil || got != (Paths{"tor", "lyrebird"}) {
		t.Fatal(got, err)
	}
	if runtime.GOOS == "windows" {
		return
	} // Creating symlinks may require privileges.
	base := filepath.Join(root, "app", "tor-runtime", "linux-amd64", "tor")
	writeExecutable(t, filepath.Join(base, "tor"))
	writeExecutable(t, filepath.Join(base, "pluggable_transports", "lyrebird"))
	link := filepath.Join(root, "shadowtalk-link")
	if err := os.Symlink(executable, link); err != nil {
		t.Fatal(err)
	}
	got, err = Locate(link, "linux", "amd64")
	want, _ := filepath.EvalSymlinks(filepath.Join(base, "tor"))
	if err != nil || got.Tor != want {
		t.Fatal(got, err)
	}
	if err := os.Chmod(want, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Locate(link, "linux", "amd64"); !errors.Is(err, ErrIncomplete) {
		t.Fatal("non-executable Tor accepted", err)
	}
}
