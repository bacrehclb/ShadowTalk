package main

import (
	"fmt"
	"io"
	"runtime"
)

// GoReleaser sets these through linker flags. Source builds remain identifiable.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func printVersion(out io.Writer) error {
	_, err := fmt.Fprintf(out, "shadowtalk %s (commit %s, built %s, %s/%s)\n",
		version, commit, date, runtime.GOOS, runtime.GOARCH)
	return err
}
