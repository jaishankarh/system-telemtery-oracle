package machine

import "os"

// Exists reports whether an absolute system path exists (without following a final symbolic link).
func (m Machine) Exists(p string) bool {
	_, err := os.Lstat(m.Path(p))
	return err == nil
}
