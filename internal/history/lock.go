package history

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/jaishankarh/system-telemtery-oracle/internal/machine"
)

// LockFile sits next to the ring in the state dir. The running instance holds
// an exclusive flock on it for as long as it owns History, so a second
// instance cannot write the same ring or announce itself over the first.
const LockFile = "history.lock"

// ErrLocked is returned by Open when another instance already owns History.
var ErrLocked = errors.New("another sys-telemetry-app instance is recording History in this state dir")

// lockState takes the exclusive, non-blocking lock. The kernel drops it when
// the process dies, so a crash never leaves a stale lock.
func lockState(stateDir string) (*os.File, error) {
	if err := machine.EnsurePrivateDir(stateDir); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(stateDir, LockFile), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w (%s)", ErrLocked, stateDir)
		}
		return nil, err
	}
	return f, nil
}
