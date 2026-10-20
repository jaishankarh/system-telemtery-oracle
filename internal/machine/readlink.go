package machine

import "os"

// Readlink returns the target of a symbolic link at an absolute system path
// (for example /proc/<pid>/cwd), unresolved and unprefixed by Root.
func (m Machine) Readlink(p string) (string, error) { return os.Readlink(m.Path(p)) }
