// Package dockerclient wraps the Docker Engine API for DockWeave.
// All containers created here carry the dockweave.managed label and are
// hardened per the security model (no caps, no docker.sock, seccomp default).
package dockerclient

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"

	"github.com/ananta888/dockweave/internal/model"
)

// Label keys used to attribute containers to DockWeave identities.
const (
	LabelManaged  = "dockweave.managed"
	LabelAgentID  = "dockweave.agent.id"
	LabelAgentNam = "dockweave.agent.name"
)

// Client wraps the engine client.
type Client struct {
	cli *client.Client
}

// New connects to the Docker Engine (respecting DOCKER_HOST).
func New() (*Client, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &Client{cli: cli}, nil
}

// Close releases resources.
func (c *Client) Close() error { return c.cli.Close() }

// Ping verifies engine connectivity.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.cli.Ping(ctx)
	if err != nil {
		return fmt.Errorf("docker ping: %w", err)
	}
	return nil
}

// ContainerSpec describes a DockWeave agent container.
type ContainerSpec struct {
	Agent        *model.Agent
	AgentDir     string // host path -> /agent
	MemoryDir    string // host path -> /memory
	WorkspaceDir string // host path -> /workspace (bind-propagation rslave)
	KnowledgeDir string // host path -> /knowledge (shared knowledge store)
	NotesDir     string // host path -> /notes (per-agent knowledge folder)
	WorkingDir   string // container working dir (defaults to /workspace)
}

// ContainerName returns the deterministic container name for an agent.
func ContainerName(agentID string) string { return "dockweave-" + agentID }

// EnsureImage pulls the image if not present locally.
func (c *Client) EnsureImage(ctx context.Context, ref string, out io.Writer) error {
	_, _, err := c.cli.ImageInspectWithRaw(ctx, ref)
	if err == nil {
		return nil
	}
	rc, err := c.cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pull image %q: %w", ref, err)
	}
	defer rc.Close()
	if out != nil {
		_, _ = io.Copy(out, rc)
	} else {
		_, _ = io.Copy(io.Discard, rc)
	}
	return nil
}

// Create creates (but does not start) the hardened agent container.
// containerEnv assembles the container environment. Long-term memory is
// the agent's home: tool state (~/.claude, ~/.codex, …) must land under
// /memory where it persists and where the adapter markers live. An
// explicit HOME in the agent env wins.
func containerEnv(agentEnv []string) []string {
	for _, e := range agentEnv {
		if strings.HasPrefix(e, "HOME=") {
			return agentEnv
		}
	}
	return append([]string{"HOME=/memory"}, agentEnv...)
}

func (c *Client) Create(ctx context.Context, spec ContainerSpec) (string, error) {
	a := spec.Agent
	labels := map[string]string{
		LabelManaged:  "true",
		LabelAgentID:  a.ID,
		LabelAgentNam: a.Name,
	}
	cmd := a.Command
	if len(cmd) == 0 {
		cmd = []string{"/bin/sh", "-c", "while :; do sleep 3600; done"}
	}
	// Always run with an explicit identity (review-005): UID 0 is only ever
	// set when the daemon policy explicitly allows root containers.
	user := fmt.Sprintf("%d:%d", a.UID, a.GID)
	workDir := spec.WorkingDir
	if workDir == "" {
		workDir = "/workspace"
	}
	cfg := &container.Config{
		Image:  a.Image,
		Cmd:    cmd,
		Env:    containerEnv(a.Env),
		Labels: labels,
		// Agents work in their assigned projects: /workspace holds the
		// project mounts (/workspace/<name>). Without this the container
		// starts in the image default (/) and the agent cannot see its
		// projects without manual navigation.
		WorkingDir: workDir,
		User:       user,
		// Keep stdin open + tty for attach/exec based interactive use.
		OpenStdin: true,
		Tty:       true,
	}
	mounts := []mount.Mount{
		{
			Type:     mount.TypeBind,
			Source:   spec.AgentDir,
			Target:   "/agent",
			ReadOnly: a.AgentRO,
		},
		{
			Type:   mount.TypeBind,
			Source: spec.MemoryDir,
			Target: "/memory",
		},
		{
			Type:        mount.TypeBind,
			Source:      spec.WorkspaceDir,
			Target:      "/workspace",
			BindOptions: &mount.BindOptions{Propagation: mount.PropagationRSlave},
		},
	}
	host := &container.HostConfig{
		Mounts: mounts,
		// Hardening: drop all capabilities; agent work must not need any.
		CapDrop: []string{"ALL"},
		// Explicitly never privileged; seccomp stays at engine default.
		Privileged:    false,
		SecurityOpt:   []string{"no-new-privileges"},
		NetworkMode:   container.NetworkMode(a.NetworkMode),
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled},
	}
	if a.NetworkMode == "" {
		host.NetworkMode = "bridge"
	}
	if a.CPULimit > 0 {
		host.NanoCPUs = int64(a.CPULimit * 1e9)
	}
	if a.MemoryMB > 0 {
		host.Memory = a.MemoryMB * 1024 * 1024
	}
	resp, err := c.cli.ContainerCreate(ctx, cfg, host, &network.NetworkingConfig{}, nil, ContainerName(a.ID))
	if err != nil {
		return "", fmt.Errorf("create container for agent %q: %w", a.Name, err)
	}
	return resp.ID, nil
}

// Start starts a container.
func (c *Client) Start(ctx context.Context, containerID string) error {
	if err := c.cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start container: %w", err)
	}
	return nil
}

// Stop stops a container with a grace period.
func (c *Client) Stop(ctx context.Context, containerID string, timeoutSec int) error {
	t := timeoutSec
	if err := c.cli.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &t}); err != nil {
		return fmt.Errorf("stop container: %w", err)
	}
	return nil
}

// Remove removes a container (must be stopped or force).
func (c *Client) Remove(ctx context.Context, containerID string, force bool) error {
	if err := c.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: force}); err != nil {
		return fmt.Errorf("remove container: %w", err)
	}
	return nil
}

// Inspect returns container details.
func (c *Client) Inspect(ctx context.Context, containerID string) (*container.InspectResponse, error) {
	info, err := c.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("inspect container: %w", err)
	}
	return &info, nil
}

// FindByAgent returns the container ID currently labeled for agentID, or "".
func (c *Client) FindByAgent(ctx context.Context, agentID string) (string, error) {
	f := filters.NewArgs(
		filters.Arg("label", LabelManaged+"=true"),
		filters.Arg("label", LabelAgentID+"="+agentID),
	)
	list, err := c.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: f})
	if err != nil {
		return "", fmt.Errorf("list containers for agent: %w", err)
	}
	if len(list) == 0 {
		return "", nil
	}
	return list[0].ID, nil
}

// State returns a coarse state string for a container ID ("running", "exited", ...).
func (c *Client) State(ctx context.Context, containerID string) (string, error) {
	info, err := c.Inspect(ctx, containerID)
	if err != nil {
		return "", err
	}
	return info.State.Status, nil
}

// Logs streams container logs. follow=true keeps the stream open.
func (c *Client) Logs(ctx context.Context, containerID string, follow bool, tail string) (io.ReadCloser, error) {
	rc, err := c.cli.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true, ShowStderr: true, Follow: follow, Tail: tail, Timestamps: false,
	})
	if err != nil {
		return nil, fmt.Errorf("container logs: %w", err)
	}
	return rc, nil
}

// StatsSample returns one CPU/memory sample for a container.
func (c *Client) StatsSample(ctx context.Context, containerID string) (cpuPercent float64, memBytes uint64, err error) {
	resp, err := c.cli.ContainerStatsOneShot(ctx, containerID)
	if err != nil {
		return 0, 0, fmt.Errorf("stats: %w", err)
	}
	defer resp.Body.Close()
	var st container.StatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return 0, 0, fmt.Errorf("decode stats: %w", err)
	}
	cpuDelta := float64(st.CPUStats.CPUUsage.TotalUsage - st.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(st.CPUStats.SystemUsage - st.PreCPUStats.SystemUsage)
	if sysDelta > 0 && st.CPUStats.OnlineCPUs > 0 {
		cpuPercent = cpuDelta / sysDelta * float64(st.CPUStats.OnlineCPUs) * 100
	}
	return cpuPercent, st.MemoryStats.Usage, nil
}

// Top lists processes inside a container.
func (c *Client) Top(ctx context.Context, containerID string) ([]string, [][]string, error) {
	resp, err := c.cli.ContainerTop(ctx, containerID, []string{"-eo", "pid,comm,args"})
	if err != nil {
		return nil, nil, fmt.Errorf("container top: %w", err)
	}
	return resp.Titles, resp.Processes, nil
}

// TopPIDs returns the host PIDs of processes inside a container.
func (c *Client) TopPIDs(ctx context.Context, containerID string) ([]int, error) {
	_, procs, err := c.Top(ctx, containerID)
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, p := range procs {
		if len(p) == 0 {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSpace(p[0])); err == nil {
			pids = append(pids, n)
		}
	}
	return pids, nil
}

// Events subscribes to engine events for DockWeave-managed containers.
func (c *Client) Events(ctx context.Context) (<-chan events.Message, <-chan error) {
	f := filters.NewArgs(filters.Arg("label", LabelManaged+"=true"))
	msgs, errs := c.cli.Events(ctx, events.ListOptions{Filters: f})
	return msgs, errs
}

// Exec creates and starts an exec instance, returning a hijacked connection
// and the exec ID (for resize). When tty is true the stream is raw;
// otherwise multiplexed (see Demux).
func (c *Client) Exec(ctx context.Context, containerID string, cmd []string, tty bool) (io.ReadWriteCloser, string, error) {
	execCfg := container.ExecOptions{
		Cmd:          cmd,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          tty,
	}
	created, err := c.cli.ContainerExecCreate(ctx, containerID, execCfg)
	if err != nil {
		return nil, "", fmt.Errorf("exec create: %w", err)
	}
	resp, err := c.cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{Tty: tty})
	if err != nil {
		return nil, "", fmt.Errorf("exec attach: %w", err)
	}
	return resp.Conn, created.ID, nil
}

// ExecResize resizes the TTY of a running exec instance.
func (c *Client) ExecResize(ctx context.Context, execID string, rows, cols int) error {
	opts := container.ResizeOptions{Height: uint(rows), Width: uint(cols)}
	if err := c.cli.ContainerExecResize(ctx, execID, opts); err != nil {
		return fmt.Errorf("exec resize: %w", err)
	}
	return nil
}

// ExecExitCode returns the exit code of a finished exec instance's session
// (best effort; only valid after the stream closed).
func (c *Client) ExecExitCode(ctx context.Context, containerID string, cmd []string) (int, error) {
	execCfg := container.ExecOptions{Cmd: cmd, AttachStdout: true, AttachStderr: true}
	created, err := c.cli.ContainerExecCreate(ctx, containerID, execCfg)
	if err != nil {
		return -1, fmt.Errorf("exec create: %w", err)
	}
	if err := c.cli.ContainerExecStart(ctx, created.ID, container.ExecStartOptions{}); err != nil {
		return -1, fmt.Errorf("exec start: %w", err)
	}
	for {
		info, err := c.cli.ContainerExecInspect(ctx, created.ID)
		if err != nil {
			return -1, fmt.Errorf("exec inspect: %w", err)
		}
		if !info.Running {
			return info.ExitCode, nil
		}
		select {
		case <-ctx.Done():
			return -1, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Attach attaches to the primary container process (TTY).
func (c *Client) Attach(ctx context.Context, containerID string) (io.ReadWriteCloser, error) {
	resp, err := c.cli.ContainerAttach(ctx, containerID, container.AttachOptions{
		Stream: true, Stdin: true, Stdout: true, Stderr: true,
	})
	if err != nil {
		return nil, fmt.Errorf("attach: %w", err)
	}
	return resp.Conn, nil
}

// Resize changes the TTY size of the container's main process.
func (c *Client) Resize(ctx context.Context, containerID string, rows, cols int) error {
	return c.cli.ContainerResize(ctx, containerID, container.ResizeOptions{
		Height: uint(rows), Width: uint(cols),
	})
}

// Demux copies a multiplexed docker stream (8-byte header frames) to stdout/stderr.
func Demux(src io.Reader, stdout, stderr io.Writer) error {
	hdr := make([]byte, 8)
	for {
		if _, err := io.ReadFull(src, hdr); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		n := binary.BigEndian.Uint32(hdr[4:])
		var dst io.Writer
		switch hdr[0] {
		case 2:
			dst = stderr
		default:
			dst = stdout
		}
		if _, err := io.CopyN(dst, src, int64(n)); err != nil {
			return err
		}
	}
}

// Expose nat import usage guard (nat kept for future port mappings).
var _ = nat.Port("")

// ExecOutput runs a command in a container and returns its combined output
// (stdout+stderr, demultiplexed). Non-zero exit codes produce an error that
// contains the output.
func (c *Client) ExecOutput(ctx context.Context, containerID string, cmd []string) (string, error) {
	created, err := c.cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		Cmd: cmd, AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		return "", fmt.Errorf("exec create: %w", err)
	}
	resp, err := c.cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", fmt.Errorf("exec attach: %w", err)
	}
	defer resp.Close()
	var buf bytes.Buffer
	if err := Demux(resp.Reader, &buf, &buf); err != nil {
		return buf.String(), fmt.Errorf("exec stream: %w", err)
	}
	insp, err := c.cli.ContainerExecInspect(ctx, created.ID)
	if err == nil && insp.ExitCode != 0 {
		return buf.String(), fmt.Errorf("exit %d", insp.ExitCode)
	}
	return buf.String(), nil
}

// ErrNotFound wraps engine "no such container" errors.
var ErrNotFound = errors.New("container not found")
