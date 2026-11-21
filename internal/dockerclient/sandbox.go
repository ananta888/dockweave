package dockerclient

import (
	"context"
	"fmt"
	"os"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
)

// LabelGate marks ephemeral containers created for gate execution.
const LabelGate = "dockweave.gate"

// SandboxSpec describes a short-lived, hardened container that is not an agent.
// It has no broker/docker socket, no network by default and a read-only root
// filesystem with a writable /tmp tmpfs.
type SandboxSpec struct {
	Image     string
	Cmd       []string
	WorkDir   string
	Binds     []mount.Mount
	Env       []string
	MemoryMB  int64
	NanoCPUs  int64
	PidsLimit int64
	Labels    map[string]string
	// NetworkMode defaults to "none".
	NetworkMode string
	// User defaults to the current process uid:gid so files created in the
	// workspace copy stay removable by the daemon.
	User string
}

// CreateSandbox creates (does not start) a hardened ephemeral container.
func (c *Client) CreateSandbox(ctx context.Context, spec SandboxSpec) (string, error) {
	labels := map[string]string{LabelManaged: "true", LabelGate: "true"}
	for k, v := range spec.Labels {
		labels[k] = v
	}
	env := append([]string{"HOME=/tmp", "TMPDIR=/tmp", "CI=1", "NO_COLOR=1"}, spec.Env...)
	user := spec.User
	if user == "" {
		user = fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
	}
	cfg := &container.Config{
		Image:      spec.Image,
		User:       user,
		Cmd:        spec.Cmd,
		WorkingDir: spec.WorkDir,
		Env:        env,
		Labels:     labels,
		// TTY keeps the log stream un-multiplexed (no 8-byte Docker headers).
		Tty:       true,
		OpenStdin: false,
	}
	networkMode := spec.NetworkMode
	if networkMode == "" {
		networkMode = "none"
	}
	pids := spec.PidsLimit
	if pids <= 0 {
		pids = 256
	}
	host := &container.HostConfig{
		Mounts:         spec.Binds,
		NetworkMode:    container.NetworkMode(networkMode),
		CapDrop:        []string{"ALL"},
		Privileged:     false,
		SecurityOpt:    []string{"no-new-privileges"},
		ReadonlyRootfs: true,
		Tmpfs: map[string]string{
			"/tmp":     "rw,noexec,nosuid,size=512m",
			"/var/tmp": "rw,noexec,nosuid,size=64m",
		},
		Resources: container.Resources{
			Memory:    spec.MemoryMB * 1024 * 1024,
			NanoCPUs:  spec.NanoCPUs,
			PidsLimit: &pids,
		},
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled},
	}
	if host.Resources.Memory < 0 {
		host.Resources.Memory = 0
	}
	resp, err := c.cli.ContainerCreate(ctx, cfg, host, &network.NetworkingConfig{}, nil, "")
	if err != nil {
		return "", fmt.Errorf("create sandbox container: %w", err)
	}
	return resp.ID, nil
}

// WaitExit blocks until the container exits and returns its exit code.
func (c *Client) WaitExit(ctx context.Context, containerID string) (int64, error) {
	statusCh, errCh := c.cli.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return -1, fmt.Errorf("wait container: %w", err)
		}
		return -1, nil
	case st := <-statusCh:
		return st.StatusCode, nil
	case <-ctx.Done():
		return -1, ctx.Err()
	}
}

// ImageDigest returns the pinned digest of an image (RepoDigest preferred,
// image ID as a fallback for locally built images).
func (c *Client) ImageDigest(ctx context.Context, ref string) (string, error) {
	info, err := c.cli.ImageInspect(ctx, ref)
	if err != nil {
		return "", fmt.Errorf("inspect image %q: %w", ref, err)
	}
	if len(info.RepoDigests) > 0 {
		return info.RepoDigests[0], nil
	}
	return info.ID, nil
}
