package machine

import (
	"errors"
	"net"
	"time"
)

// DialSocket connects to a unix socket at an absolute system path through the
// Machine (container runtimes listen on /var/run/docker.sock and
// containerd.sock). It fails when the socket does not exist, which is how a
// missing runtime shows up.
func (m Machine) DialSocket(path string) (net.Conn, error) {
	if m.Dial == nil {
		return nil, errors.New("no socket dialer")
	}
	return m.Dial(path)
}

func realDial(path string) (net.Conn, error) { return net.DialTimeout("unix", path, 2*time.Second) }
