//go:build !windows

package tor

import (
	"errors"
	"os"
)

func canRetryPortFileRead(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
