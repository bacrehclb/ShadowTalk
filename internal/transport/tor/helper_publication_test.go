package tor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

func TestFakeControlPortPublicationIsComplete(t *testing.T) {
	const address = "127.0.0.1:9051"
	const record = "PORT=" + address + "\n"
	directory := t.TempDir()

	// Observe the same absent-to-published transition as process.connect.
	// Repeating with fresh paths exercises the reader/writer scheduling window.
	for attempt := range 100 {
		path := filepath.Join(directory, strconv.Itoa(attempt)+".port")
		ready := make(chan struct{})
		published := make(chan struct{})
		observed := make(chan error, 1)
		go func() {
			close(ready)
			for {
				data, err := os.ReadFile(path)
				if err == nil && string(data) != record {
					observed <- fmt.Errorf("reader observed an incomplete port record: %q", data)
					return
				}
				if err != nil && !canRetryPortFileRead(err) {
					observed <- err
					return
				}
				select {
				case <-published:
					observed <- nil
					return
				default:
					runtime.Gosched()
				}
			}
		}()
		<-ready
		writeErr := publishFakeControlPort(path, address)
		close(published)
		readErr := <-observed
		if writeErr != nil || readErr != nil {
			t.Fatalf("publication %d: write: %v; read: %v", attempt, writeErr, readErr)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != record {
			t.Fatalf("publication %d: final record: %q, %v", attempt, data, err)
		}
		if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("publication %d: temporary port file remains: %v", attempt, err)
		}
	}
}
