package containers

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/machine"
)

// The containerd API is gRPC. Two unary calls are all that is needed, so they
// are made with the standard library (HTTP/2 without TLS, 5-byte message
// frames) and the few protobuf fields read by hand; no dependency is pulled in.

const (
	svcNamespaces = "/containerd.services.namespaces.v1.Namespaces/List"
	svcContainers = "/containerd.services.containers.v1.Containers/List"
)

func grpcCall(m machine.Machine, sock, method, namespace string) ([]byte, error) {
	tr := &http.Transport{
		DisableKeepAlives: true,
		Protocols:         new(http.Protocols),
		DialContext:       func(context.Context, string, string) (net.Conn, error) { return m.DialSocket(sock) },
	}
	tr.Protocols.SetUnencryptedHTTP2(true)
	c := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	req, err := http.NewRequest("POST", "http://containerd"+method, bytes.NewReader([]byte{0, 0, 0, 0, 0}))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("Te", "trailers")
	if namespace != "" {
		req.Header.Set("Containerd-Namespace", namespace)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	status := resp.Trailer.Get("Grpc-Status")
	if status == "" {
		status = resp.Header.Get("Grpc-Status")
	}
	if resp.StatusCode != 200 || status != "0" {
		return nil, fmt.Errorf("containerd %s: http %d grpc-status %q", method, resp.StatusCode, status)
	}
	// One response message per List call.
	if len(body) < 5 {
		return nil, nil
	}
	n := binary.BigEndian.Uint32(body[1:5])
	if int(n) > len(body)-5 {
		return nil, errors.New("short grpc message")
	}
	return body[5 : 5+n], nil
}

// containerdList reads running containers from every containerd namespace.
func containerdList(m machine.Machine, sock string) ([]Info, bool) {
	nsResp, err := grpcCall(m, sock, svcNamespaces, "")
	if err != nil {
		return nil, false
	}
	var out []Info
	for _, f := range fields(nsResp) { // repeated Namespace namespaces = 1
		if f.num != 1 {
			continue
		}
		ns := ""
		for _, g := range fields(f.data) {
			if g.num == 1 {
				ns = string(g.data)
			}
		}
		if ns == "" || ns == "moby" { // Docker's own namespace: Docker answers for those
			continue
		}
		cResp, err := grpcCall(m, sock, svcContainers, ns)
		if err != nil {
			continue
		}
		for _, cf := range fields(cResp) { // repeated Container containers = 1
			if cf.num == 1 {
				out = append(out, parseContainer(cf.data))
			}
		}
	}
	return out, true
}

func parseContainer(b []byte) Info {
	in := Info{Runtime: RuntimeContainerd}
	labels := map[string]string{}
	var spec []byte
	for _, f := range fields(b) {
		switch f.num {
		case 1:
			in.ID = string(f.data)
		case 2: // map<string,string> entry
			var k, v string
			for _, e := range fields(f.data) {
				if e.num == 1 {
					k = string(e.data)
				} else if e.num == 2 {
					v = string(e.data)
				}
			}
			labels[k] = v
		case 5: // google.protobuf.Any spec; value is the OCI runtime spec as JSON
			for _, a := range fields(f.data) {
				if a.num == 2 {
					spec = a.data
				}
			}
		}
	}
	in.Name = in.ID
	switch {
	case labels[labelNerdctlName] != "":
		in.Name = labels[labelNerdctlName]
	case labels[labelContainer] != "":
		in.Name = labels[labelContainer]
	}
	in.ComposeProject, in.ComposeService = labels[labelCompose], labels[labelComposeSvc]
	in.StartedBy = startedBy(labels, StartedContainerd)
	if in.StartedBy == StartedContainerd && labels[labelNerdctlName] != "" {
		in.StartedBy = StartedNerdctl
	}
	in.PodName, in.PodNamespace = labels[labelPodName], labels[labelPodNamespace]
	if labels[labelCRIKind] == "sandbox" {
		in.Sandbox = true
		in.Name = "sandbox"
	}
	var oci struct {
		Linux struct {
			Resources struct {
				Memory struct{ Limit *int64 }
			}
		}
	}
	if json.Unmarshal(spec, &oci) == nil {
		if l := oci.Linux.Resources.Memory.Limit; l != nil && *l > 0 {
			v := uint64(*l)
			in.MemoryLimit = &v
		}
	}
	return in
}

type field struct {
	num  int
	data []byte // length-delimited payload; empty for varint/fixed fields
}

// fields splits a protobuf message into its top-level fields. Malformed input
// yields the fields read so far.
func fields(b []byte) []field {
	var out []field
	for len(b) > 0 {
		tag, n := binary.Uvarint(b)
		if n <= 0 {
			return out
		}
		b = b[n:]
		num, wire := int(tag>>3), tag&7
		switch wire {
		case 0:
			_, n = binary.Uvarint(b)
			if n <= 0 {
				return out
			}
			b = b[n:]
		case 1:
			if len(b) < 8 {
				return out
			}
			b = b[8:]
		case 5:
			if len(b) < 4 {
				return out
			}
			b = b[4:]
		case 2:
			l, n := binary.Uvarint(b)
			if n <= 0 || uint64(len(b)-n) < l {
				return out
			}
			out = append(out, field{num, b[n : n+int(l)]})
			b = b[n+int(l):]
		default:
			return out
		}
	}
	return out
}
