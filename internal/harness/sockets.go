package harness

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// sockets serves fake container-runtime sockets. Each is a real unix socket
// in a short temp dir, reached through machine.Machine.Dial under its
// virtual path (for example /var/run/docker.sock). A runtime nobody adds a
// container to has no socket, which is how "no runtime installed" looks.
type sockets struct {
	t   *testing.T
	mu  sync.Mutex
	dir string
	at  map[string]string // virtual path to real socket path
}

func newSockets(t *testing.T) *sockets { return &sockets{t: t, at: map[string]string{}} }

func (s *sockets) dial(virtual string) (net.Conn, error) {
	s.mu.Lock()
	real, ok := s.at[virtual]
	s.mu.Unlock()
	if !ok {
		return nil, &net.OpError{Op: "dial", Net: "unix", Err: os.ErrNotExist}
	}
	return net.Dial("unix", real)
}

func (s *sockets) serve(virtual string, srv *http.Server) {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir == "" {
		d, err := os.MkdirTemp("", "so")
		if err != nil {
			s.t.Fatal(err)
		}
		s.dir = d
		s.t.Cleanup(func() { os.RemoveAll(d) })
	}
	real := filepath.Join(s.dir, fmt.Sprintf("s%d", len(s.at)))
	ln, err := net.Listen("unix", real)
	if err != nil {
		s.t.Fatal(err)
	}
	s.at[virtual] = real
	go srv.Serve(ln)
	s.t.Cleanup(func() { srv.Close() })
}

// DockerContainer is a container on the fake Docker daemon.
type DockerContainer struct {
	ID          string
	Name        string
	MemoryLimit uint64 // bytes; 0 means no limit
	Labels      map[string]string
}

// FakeDocker is a fake Docker Engine API on /var/run/docker.sock. The socket
// appears when the first container is added.
type FakeDocker struct {
	s          *sockets
	mu         sync.Mutex
	containers []DockerContainer
	started    bool
}

// Add starts the fake daemon (first call) and adds a running container.
func (d *FakeDocker) Add(c DockerContainer) {
	d.mu.Lock()
	d.containers = append(d.containers, c)
	start := !d.started
	d.started = true
	d.mu.Unlock()
	if start {
		mux := http.NewServeMux()
		mux.HandleFunc("/containers/json", func(w http.ResponseWriter, r *http.Request) {
			d.mu.Lock()
			defer d.mu.Unlock()
			var out []map[string]any
			for _, c := range d.containers {
				out = append(out, map[string]any{"Id": c.ID, "Names": []string{"/" + c.Name}, "Labels": c.Labels})
			}
			json.NewEncoder(w).Encode(out)
		})
		mux.HandleFunc("/containers/", func(w http.ResponseWriter, r *http.Request) {
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/containers/"), "/json")
			d.mu.Lock()
			defer d.mu.Unlock()
			for _, c := range d.containers {
				if c.ID == id {
					json.NewEncoder(w).Encode(map[string]any{"Id": c.ID, "Name": "/" + c.Name,
						"HostConfig": map[string]any{"Memory": c.MemoryLimit},
						"Config":     map[string]any{"Labels": c.Labels}})
					return
				}
			}
			http.NotFound(w, r)
		})
		d.s.serve("/var/run/docker.sock", &http.Server{Handler: mux})
	}
}

// ContainerdContainer is a container in the fake containerd.
type ContainerdContainer struct {
	ID          string
	Namespace   string // containerd namespace, "k8s.io" for Kubernetes, "default" otherwise
	Labels      map[string]string
	MemoryLimit uint64 // bytes; 0 means no limit
}

// FakeContainerd is a fake containerd gRPC API on /run/containerd/containerd.sock
// (set Path before the first Add for another socket such as k3s's). It
// answers Namespaces/List and Containers/List with real protobuf and gRPC
// framing over HTTP/2.
type FakeContainerd struct {
	s          *sockets
	Path       string
	mu         sync.Mutex
	containers []ContainerdContainer
	started    bool
}

// Add starts the fake daemon (first call) and adds a container.
func (c *FakeContainerd) Add(x ContainerdContainer) {
	c.mu.Lock()
	c.containers = append(c.containers, x)
	start := !c.started
	c.started = true
	c.mu.Unlock()
	if !start {
		return
	}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status")
		c.mu.Lock()
		defer c.mu.Unlock()
		var msg []byte
		switch r.URL.Path {
		case "/containerd.services.namespaces.v1.Namespaces/List":
			seen := map[string]bool{}
			for _, x := range c.containers {
				if !seen[x.Namespace] {
					seen[x.Namespace] = true
					msg = append(msg, pbBytes(1, pbBytes(1, []byte(x.Namespace)))...)
				}
			}
		case "/containerd.services.containers.v1.Containers/List":
			ns := r.Header.Get("Containerd-Namespace")
			for _, x := range c.containers {
				if x.Namespace == ns {
					msg = append(msg, pbBytes(1, encodeContainer(x))...)
				}
			}
		default:
			w.Header().Set("Grpc-Status", "12")
			return
		}
		frame := make([]byte, 5, 5+len(msg))
		binary.BigEndian.PutUint32(frame[1:], uint32(len(msg)))
		w.Write(append(frame, msg...))
		w.Header().Set("Grpc-Status", "0")
	})
	p := new(http.Protocols)
	p.SetUnencryptedHTTP2(true)
	path := c.Path
	if path == "" {
		path = "/run/containerd/containerd.sock"
	}
	c.s.serve(path, &http.Server{Handler: h, Protocols: p})
}

func encodeContainer(x ContainerdContainer) []byte {
	b := pbBytes(1, []byte(x.ID))
	for k, v := range x.Labels {
		b = append(b, pbBytes(2, append(pbBytes(1, []byte(k)), pbBytes(2, []byte(v))...))...)
	}
	spec := map[string]any{"linux": map[string]any{"resources": map[string]any{}}}
	if x.MemoryLimit > 0 {
		spec["linux"].(map[string]any)["resources"] = map[string]any{"memory": map[string]any{"limit": x.MemoryLimit}}
	}
	js, _ := json.Marshal(spec)
	any := append(pbBytes(1, []byte("types.containerd.io/opencontainers/runtime-spec/1/Spec")), pbBytes(2, js)...)
	return append(b, pbBytes(5, any)...)
}

func pbBytes(num int, data []byte) []byte {
	b := binary.AppendUvarint(nil, uint64(num)<<3|2)
	b = binary.AppendUvarint(b, uint64(len(data)))
	return append(b, data...)
}

// AddProcess writes a recorded process into the fake /proc: stat, status,
// cmdline, cgroup, cwd, smaps_rollup and io. cgroup is the cgroup v2 path.
func (m *Machine) AddProcess(pid, ppid int, comm, cmdline, cgroup string, pssKB int) {
	m.t.Helper()
	d := fmt.Sprintf("/proc/%d/", pid)
	m.WriteFile(d+"stat", fmt.Sprintf("%d (%s) S %d %d %d 0 -1 4194304 100 0 0 0 100 50 0 0 20 0 1 0 %d 4595712 3000 18446744073709551615 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0\n", pid, comm, ppid, pid, pid, 2000+pid))
	m.WriteFile(d+"status", fmt.Sprintf("Name:\t%s\nTgid:\t%d\nPid:\t%d\nUid:\t0\t0\t0\t0\nVmRSS:\t%d kB\nVmSwap:\t0 kB\nThreads:\t1\n", comm, pid, pid, pssKB))
	m.WriteFile(d+"cmdline", strings.ReplaceAll(cmdline, " ", "\x00")+"\x00")
	m.WriteFile(d+"cgroup", "0::"+cgroup+"\n")
	m.WriteFile(d+"smaps_rollup", fmt.Sprintf("00400000-7ffc18f63000 ---p 00000000 00:00 0   [rollup]\nRss:  %d kB\nPss:  %d kB\nSwap:  0 kB\n", pssKB, pssKB))
	m.WriteFile(d+"io", "read_bytes: 0\nwrite_bytes: 0\n")
	if err := os.Symlink("/", filepath.Join(m.Root, d, "cwd")); err != nil && !os.IsExist(err) {
		m.t.Fatal(err)
	}
}
