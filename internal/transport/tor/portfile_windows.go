package tor

import (
	"errors"
	"os"
	"syscall"
)

// ERROR_SHARING_VIOLATION is not exported by the standard syscall package.
const errorSharingViolation syscall.Errno = 32

func canRetryPortFileRead(err error) bool {
	// A renamed file can be visible before Windows releases the rename handle.
	// process.connect retries within the existing startup deadline. Permission
	// errors and invalid records remain fatal.
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, errorSharingViolation)
}
