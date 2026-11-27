package tor

import (
	"reflect"
	"testing"
)

func TestBundledLibraryPathDoesNotLeakInheritedSearchDirectories(t *testing.T) {
	inherited := []string{"HOME=/home/test", "LD_LIBRARY_PATH=.:/other", "LANG=C", "LD_LIBRARY_PATH=/again"}
	want := []string{"HOME=/home/test", "LANG=C", "LD_LIBRARY_PATH=/app with spaces/tor"}
	if got := libraryEnvironment(inherited, "/app with spaces/tor"); !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	if inherited[1] != "LD_LIBRARY_PATH=.:/other" {
		t.Fatal("mutated parent environment")
	}
	if _, err := (Options{DataDir: "/data", LibraryDir: "relative"}).defaults(); err != ErrInvalidOptions {
		t.Fatal(err)
	}
}
