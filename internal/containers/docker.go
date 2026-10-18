package containers

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/machine"
)

const maxContainers = 500

func dockerGet(ctx context.Context, m machine.Machine, sock, path string, v any) error {
	c := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext:       func(context.Context, string, string) (net.Conn, error) { return m.DialSocket(sock) },
		},
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "http://docker"+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("docker %s: status %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// dockerList reads running containers from the Docker Engine API.
func dockerList(ctx context.Context, m machine.Machine, sock string) ([]Info, bool) {
	var list []struct {
		ID string `json:"Id"`
	}
	if err := dockerGet(ctx, m, sock, "/containers/json", &list); err != nil {
		return nil, false
	}
	var out []Info
	for i, c := range list {
		if i >= maxContainers || ctx.Err() != nil {
			break
		}
		var d struct {
			ID         string `json:"Id"`
			Name       string
			HostConfig struct{ Memory int64 }
			Config     struct{ Labels map[string]string }
		}
		if err := dockerGet(ctx, m, sock, "/containers/"+c.ID+"/json", &d); err != nil {
			continue // vanished between list and inspect
		}
		in := Info{ID: d.ID, Name: strings.TrimPrefix(d.Name, "/"), Runtime: RuntimeDocker,
			StartedBy: startedBy(d.Config.Labels, StartedDockerRun)}
		in.ComposeProject, in.ComposeService = d.Config.Labels[labelCompose], d.Config.Labels[labelComposeSvc]
		if d.HostConfig.Memory > 0 {
			v := uint64(d.HostConfig.Memory)
			in.MemoryLimit = &v
		}
		if pod := d.Config.Labels[labelPodName]; pod != "" {
			in.PodName, in.PodNamespace = pod, d.Config.Labels[labelPodNamespace]
			if n := d.Config.Labels[labelContainer]; n != "" {
				in.Name = n
			}
		}
		out = append(out, in)
	}
	return out, true
}
