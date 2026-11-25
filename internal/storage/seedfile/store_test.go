package seedfile

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/bacrehclb/ShadowTalk/internal/identity"
)

func TestCreateLoadDecryptAndRestoreIdentity(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "-профиль с пробелами")
	original, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	seed, err := original.Seed()
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, identity.SeedEncryptionKeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	encrypted, err := identity.EncryptSeed(seed, key)
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Clone(encrypted)
	if err := New(directory).Create("key_A-1", encrypted); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encrypted, want) {
		t.Fatal("Create modified caller-owned ciphertext")
	}
	encrypted[0] ^= 1
	if err := New(directory).Create("replacement", encrypted); !errors.Is(err, ErrExists) {
		t.Fatalf("got %v, want ErrExists", err)
	}
	id, loaded, err := New(directory).Load()
	if err != nil || id != "key_A-1" || !bytes.Equal(loaded, want) {
		t.Fatalf("record was not preserved across store instances: %v", err)
	}
	decrypted, err := identity.DecryptSeed(loaded, key)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := identity.RestoreKeyPair(decrypted)
	if err != nil || !bytes.Equal(restored.PublicKey(), original.PublicKey()) {
		t.Fatal("saved seed did not restore the original identity")
	}
	loaded[0] ^= 1
	_, loaded, err = New(directory).Load()
	if err != nil || !bytes.Equal(loaded, want) {
		t.Fatal("changing a loaded slice changed the saved record")
	}
	data, err := os.ReadFile(filepath.Join(directory, fileName))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 3 || string(fields["version"]) != "1" || string(fields["key_id"]) != `"key_A-1"` || string(fields["encrypted_seed"]) != `"`+base64.StdEncoding.EncodeToString(want)+`"` {
		t.Fatal("unexpected on-disk format")
	}
	for _, secret := range [][]byte{seed, key} {
		if bytes.Contains(data, secret) || bytes.Contains(data, []byte(base64.StdEncoding.EncodeToString(secret))) {
			t.Fatal("file contains plaintext secret material")
		}
	}
	if runtime.GOOS != "windows" {
		for path, wantMode := range map[string]fs.FileMode{directory: 0o700, filepath.Join(directory, fileName): 0o600} {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != wantMode {
				t.Fatalf("incorrect permissions for %s: %v", path, err)
			}
		}
	}
	assertOnlySeedFile(t, directory)
}

func TestMissingAndInvalidInputDoNotCreateFiles(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "missing")
	store := New(directory)
	if id, data, err := store.Load(); id != "" || data != nil || !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound and no data", err)
	}
	for _, id := range []string{"", "../key", "key with spaces", strings.Repeat("a", 129)} {
		if err := store.Create(id, make([]byte, encryptedSeedSize)); !errors.Is(err, identity.ErrInvalidEncryptionKeyID) {
			t.Fatalf("got %v, want ErrInvalidEncryptionKeyID", err)
		}
	}
	for _, size := range []int{0, 32, encryptedSeedSize - 1, encryptedSeedSize + 1} {
		if err := store.Create("key-1", make([]byte, size)); !errors.Is(err, ErrInvalidRecord) {
			t.Fatalf("size %d: got %v, want ErrInvalidRecord", size, err)
		}
	}
	if _, err := os.Stat(directory); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("read or rejected create changed the filesystem: %v", err)
	}
}

func TestMalformedRecordsArePreserved(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString(make([]byte, encryptedSeedSize))
	valid := fmt.Sprintf(`{"version":1,"key_id":"key-1","encrypted_seed":%q}`, encoded)
	for _, tt := range []struct {
		name string
		data string
		want error
	}{
		{"empty", "", ErrInvalidRecord},
		{"truncated", `{"version":1,`, ErrInvalidRecord},
		{"null", "null", ErrInvalidRecord},
		{"missing version", strings.Replace(valid, `"version":1,`, "", 1), ErrInvalidRecord},
		{"invalid version", strings.Replace(valid, `"version":1`, `"version":0`, 1), ErrInvalidRecord},
		{"future version", strings.Replace(valid, `"version":1`, `"version":2`, 1), ErrUnsupportedVersion},
		{"missing ID", strings.Replace(valid, `"key_id":"key-1",`, "", 1), ErrInvalidRecord},
		{"invalid ID", strings.Replace(valid, "key-1", "../key", 1), ErrInvalidRecord},
		{"missing ciphertext", `{"version":1,"key_id":"key-1"}`, ErrInvalidRecord},
		{"null ciphertext", strings.Replace(valid, `"`+encoded+`"`, "null", 1), ErrInvalidRecord},
		{"array ciphertext", strings.Replace(valid, `"`+encoded+`"`, "[]", 1), ErrInvalidRecord},
		{"invalid base64", strings.Replace(valid, encoded, strings.Repeat("!", len(encoded)), 1), ErrInvalidRecord},
		{"short ciphertext", strings.Replace(valid, encoded, base64.StdEncoding.EncodeToString(make([]byte, encryptedSeedSize-1)), 1), ErrInvalidRecord},
		{"long ciphertext", strings.Replace(valid, encoded, base64.StdEncoding.EncodeToString(make([]byte, encryptedSeedSize+1)), 1), ErrInvalidRecord},
		{"line break in base64", strings.Replace(valid, encoded, encoded[:len(encoded)-1]+`\n`, 1), ErrInvalidRecord},
		{"unknown field", strings.TrimSuffix(valid, "}") + `,"DO_NOT_LOG":"secret"}`, ErrInvalidRecord},
		{"extra record", valid + " {}", ErrInvalidRecord},
		{"trailing junk", valid + " junk", ErrInvalidRecord},
		{"oversized", valid + strings.Repeat(" ", maxFileSize-len(valid)+1), ErrInvalidRecord},
	} {
		t.Run(tt.name, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, fileName)
			if err := os.WriteFile(path, []byte(tt.data), 0o600); err != nil {
				t.Fatal(err)
			}
			store := New(directory)
			id, data, err := store.Load()
			if id != "" || data != nil || !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v and no data", err, tt.want)
			}
			if strings.Contains(err.Error(), "DO_NOT_LOG") {
				t.Fatal("error contains input data")
			}
			if err := store.Create("replacement", make([]byte, encryptedSeedSize)); !errors.Is(err, ErrExists) {
				t.Fatalf("got %v, want ErrExists", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != tt.data {
				t.Fatal("malformed record was changed")
			}
			assertOnlySeedFile(t, directory)
		})
	}
}

func TestConcurrentCreatePublishesOneCompleteRecord(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "profile")
	type result struct {
		id   string
		data []byte
		err  error
	}
	const count = 8
	results := make(chan result, count)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := range count {
		workers.Go(func() {
			<-start
			id := fmt.Sprintf("key-%d", i)
			data := bytes.Repeat([]byte{byte(i)}, encryptedSeedSize)
			results <- result{id, data, New(directory).Create(id, data)}
		})
	}
	close(start)
	workers.Wait()
	close(results)
	var winner result
	successes := 0
	for result := range results {
		if result.err == nil {
			winner = result
			successes++
		} else if !errors.Is(result.err, ErrExists) {
			t.Errorf("unexpected create error: %v", result.err)
		}
	}
	if successes != 1 {
		t.Fatalf("got %d successful creators, want 1", successes)
	}
	id, data, err := New(directory).Load()
	if err != nil || id != winner.id || !bytes.Equal(data, winner.data) {
		t.Fatal("file does not contain the complete winning record")
	}
	assertOnlySeedFile(t, directory)
}

func TestExistingDirectoryIsRejectedAndPreserved(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, fileName)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	store := New(directory)
	if id, data, err := store.Load(); id != "" || data != nil || !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("got %v, want ErrInvalidRecord and no data", err)
	}
	if err := store.Create("key-1", make([]byte, encryptedSeedSize)); err == nil {
		t.Fatal("Create accepted an occupied path")
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		t.Fatal("existing directory changed")
	}
	assertOnlySeedFile(t, directory)
}

func TestLoadPermissionFailureIsNotMissingRecord(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires Unix permissions without root privileges")
	}
	directory := t.TempDir()
	path := filepath.Join(directory, fileName)
	if err := os.WriteFile(path, []byte("unreadable"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	id, data, err := New(directory).Load()
	if id != "" || data != nil || !errors.Is(err, fs.ErrPermission) || errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want a permission error and no data", err)
	}
}

func assertOnlySeedFile(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != fileName {
		t.Fatal("unexpected directory contents or leftover temporary files")
	}
}
