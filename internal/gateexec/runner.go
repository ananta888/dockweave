// Package gateexec runs workspace gates in a short-lived hardened container
// instead of executing agent-controlled commands on the host.
//
// The workspace is copied to a private directory under BaseDir, the copy is
// mounted into a container with no network, no capabilities and a read-only
// root filesystem, and the configured gate commands run there. Protected files
// are hashed before and after every gate so a run that edits its own checks is
// rejected. After the run a diff between the original and the copy feeds the
// optional scope/dependency/secret policies.
package gateexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types/mount"

	"github.com/ananta888/dockweave/internal/credentialbroker"
	"github.com/ananta888/dockweave/internal/dockerclient"
	"github.com/ananta888/dockweave/internal/gatesync"
	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/persistence"
)

// UsageChecker is satisfied by brokerclient.Client.
type UsageChecker interface {
	Usage(ctx context.Context, grantID string) (*credentialbroker.Usage, error)
}

// Runner executes gate configs in sandbox containers.
type Runner struct {
	Docker       *dockerclient.Client
	Store        *persistence.Store
	Broker       UsageChecker
	BaseDir      string
	DefaultImage string
	AllowedRoots []string
	Logger       *slog.Logger
}

// Request is one gate execution.
type Request struct {
	Workspace string
	Image     string
	Actor     string
	Timeout   time.Duration
}

const (
	defaultImage   = "node:22-bookworm"
	defaultTimeout = 15 * time.Minute
	phaseOrder     = "pre_run,after_run,post_run"
)

// Run executes the workspace gates and records the result. Setup problems
// (missing workspace, unreadable config) return an error; a failing gate is a
// normal result with Status failed.
func (r *Runner) Run(ctx context.Context, req Request) (*model.GateRun, error) {
	workspace, err := r.validateWorkspace(req.Workspace)
	if err != nil {
		return nil, err
	}
	if req.Timeout <= 0 {
		req.Timeout = defaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	run := &model.GateRun{
		ID:        newID("gate"),
		Workspace: workspace,
		Actor:     req.Actor,
		StartedAt: time.Now().UTC(),
		Status:    model.GateError,
	}

	cfg, cfgRaw, err := readConfig(workspace)
	if err != nil {
		run.Reason = err.Error()
		run.FinishedAt = time.Now().UTC()
		r.record(ctx, run)
		return run, nil
	}
	run.GatesHash = hashString(cfgRaw)
	if len(cfg.Gates) == 0 {
		run.Status = model.GateSkipped
		run.Reason = "no after_run gates configured"
		run.FinishedAt = time.Now().UTC()
		r.record(ctx, run)
		return run, nil
	}
	run.Gates = len(cfg.Gates)

	image := firstNonEmpty(req.Image, cfg.RunnerImage, r.DefaultImage, os.Getenv("DOCKWEAVE_GATE_IMAGE"), defaultImage)
	run.Image = image
	if err := r.Docker.EnsureImage(runCtx, image, io.Discard); err != nil {
		run.Reason = "gate runner image unavailable: " + err.Error()
		run.FinishedAt = time.Now().UTC()
		r.record(ctx, run)
		return run, nil
	}
	if digest, err := r.Docker.ImageDigest(runCtx, image); err == nil {
		run.ImageDigest = digest
	}

	if blocked, warning := r.checkBudget(runCtx, cfg.Budget); blocked {
		run.Status = model.GateFailed
		run.Reason = warning
		run.Warnings = append(run.Warnings, warning)
		run.FinishedAt = time.Now().UTC()
		r.record(ctx, run)
		return run, nil
	}

	base := r.BaseDir
	if base == "" {
		base = filepath.Join(os.TempDir(), "dockweave-gates")
	}
	if err := os.MkdirAll(base, 0o750); err != nil {
		run.Reason = "create gate base dir: " + err.Error()
		run.FinishedAt = time.Now().UTC()
		r.record(ctx, run)
		return run, nil
	}
	tmp, err := os.MkdirTemp(base, "run-")
	if err != nil {
		run.Reason = "create gate workdir: " + err.Error()
		run.FinishedAt = time.Now().UTC()
		r.record(ctx, run)
		return run, nil
	}
	defer os.RemoveAll(tmp)
	work := filepath.Join(tmp, "work")
	if err := copyTree(runCtx, workspace, work); err != nil {
		run.Reason = err.Error()
		run.FinishedAt = time.Now().UTC()
		r.record(ctx, run)
		return run, nil
	}

	protected := mergeStrings(defaultProtected, cfg.Protected)
	before, _ := hashPaths(work, protected)

	order := strings.Split(phaseOrder, ",")
	byPhase := map[string][]gatesync.Gate{}
	for _, g := range cfg.Gates {
		when := strings.TrimSpace(g.When)
		if when == "" {
			when = "after_run"
		}
		known := false
		for _, ph := range order {
			if when == ph {
				known = true
				break
			}
		}
		if !known {
			run.Status = model.GateError
			run.Reason = fmt.Sprintf("gate %q has unknown phase %q", g.ID, when)
			run.FinishedAt = time.Now().UTC()
			r.record(ctx, run)
			return run, nil
		}
		byPhase[when] = append(byPhase[when], g)
	}

	tamper := false
gateLoop:
	for _, phase := range order {
		for _, gate := range byPhase[phase] {
			result := r.runGate(runCtx, work, image, run.ID, gate)
			run.Results = append(run.Results, result)
			if result.Status == "passed" {
				run.Passed++
			}
			after, _ := hashPaths(work, protected)
			if changed := changedHashes(before, after); len(changed) > 0 {
				tamper = true
				run.Tamper = true
				run.Warnings = append(run.Warnings, "protected files changed during gate "+gate.ID+": "+strings.Join(changed, ", "))
				break gateLoop
			}
			before = after
		}
	}
	run.DurationMS = time.Since(run.StartedAt).Milliseconds()

	// Post-run policies compare the original workspace with the sandbox copy.
	originalHashes, _ := walkHashes(workspace, defaultIgnore)
	workHashes, _ := walkHashes(work, defaultIgnore)
	changed := diffTrees(originalHashes, workHashes)
	if len(changed) > 0 || cfg.AllowPaths != nil || cfg.DenyNewDependencies || cfg.SecretScan {
		run.Diff = &model.GateDiff{Changed: changed}
		if cfg.AllowPaths != nil {
			run.Diff.OutOfScope = outOfScope(changed, cfg.AllowPaths)
		}
		if cfg.DenyNewDependencies {
			beforePkg, _ := os.ReadFile(filepath.Join(workspace, "package.json"))
			afterPkg, _ := os.ReadFile(filepath.Join(work, "package.json"))
			run.Diff.NewDependencies = newDependencies(beforePkg, afterPkg)
		}
		if cfg.SecretScan {
			run.Diff.Secrets = scanSecrets(work, changed)
		}
	}

	if blocked, warning := r.checkBudget(runCtx, cfg.Budget); blocked {
		run.Warnings = append(run.Warnings, warning)
		run.Reason = warning
	}
	run.Status = model.GatePassed
	if tamper {
		run.Status = model.GateFailed
		run.Reason = "protected gate files changed during the run"
	}
	if run.Diff != nil {
		switch {
		case len(run.Diff.OutOfScope) > 0:
			run.Status = model.GateFailed
			run.Reason = "changes outside the allowed paths: " + strings.Join(run.Diff.OutOfScope, ", ")
		case len(run.Diff.NewDependencies) > 0:
			run.Status = model.GateFailed
			run.Reason = "new dependencies without approval: " + strings.Join(run.Diff.NewDependencies, ", ")
		case len(run.Diff.Secrets) > 0:
			run.Status = model.GateFailed
			run.Reason = "possible secret in changed files: " + strings.Join(run.Diff.Secrets, ", ")
		}
	}
	if run.Reason != "" && run.Status == model.GatePassed {
		run.Status = model.GateFailed
	}
	for _, res := range run.Results {
		if res.Status != "passed" {
			run.Status = model.GateFailed
			if run.Reason == "" {
				run.Reason = "gate " + res.ID + " failed"
			}
			break
		}
	}
	run.FinishedAt = time.Now().UTC()
	r.record(ctx, run)
	return run, nil
}

func (r *Runner) runGate(ctx context.Context, work, image, runID string, gate gatesync.Gate) model.GateResult {
	res := model.GateResult{ID: gate.ID, Command: gate.Command, Cwd: gate.Cwd, Exit: -1}
	started := time.Now()
	timeout := time.Duration(gate.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	gateCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cwd := strings.TrimSpace(gate.Cwd)
	if cwd == "" {
		cwd = "."
	}
	if strings.HasPrefix(cwd, "..") || filepath.IsAbs(cwd) {
		res.Status, res.Reason = "error", "gate cwd must stay inside the workspace"
		res.DurationMS = time.Since(started).Milliseconds()
		return res
	}
	workDir := filepath.ToSlash(filepath.Join("/work", cwd))
	containerID, err := r.Docker.CreateSandbox(gateCtx, dockerclient.SandboxSpec{
		Image:   image,
		Cmd:     []string{"/bin/sh", "-c", gate.Command},
		WorkDir: workDir,
		Binds: []mount.Mount{{
			Type:   mount.TypeBind,
			Source: work,
			Target: "/work",
		}},
		MemoryMB:  2048,
		NanoCPUs:  int64(2e9),
		PidsLimit: 512,
		Labels:    map[string]string{"dockweave.gate.run": runID, "dockweave.gate.id": gate.ID},
	})
	if err != nil {
		res.Status, res.Reason = "error", err.Error()
		res.DurationMS = time.Since(started).Milliseconds()
		return res
	}
	defer func() { _ = r.Docker.Remove(context.Background(), containerID, true) }()

	if err := r.Docker.Start(gateCtx, containerID); err != nil {
		res.Status, res.Reason = "error", "start gate container: "+err.Error()
		res.DurationMS = time.Since(started).Milliseconds()
		return res
	}
	exit, waitErr := r.Docker.WaitExit(gateCtx, containerID)
	if waitErr != nil {
		if errors.Is(gateCtx.Err(), context.DeadlineExceeded) {
			_ = r.Docker.Stop(context.Background(), containerID, 0)
			res.Status, res.Reason = "error", fmt.Sprintf("gate timed out after %ds", gate.TimeoutSec)
		} else {
			res.Status, res.Reason = "error", "wait for gate: "+waitErr.Error()
		}
		res.DurationMS = time.Since(started).Milliseconds()
		return res
	}
	res.Exit = int(exit)
	if rc, err := r.Docker.Logs(context.Background(), containerID, false, "400"); err == nil {
		if raw, err := io.ReadAll(rc); err == nil {
			res.Output = tailLines(string(raw), 40)
		}
		_ = rc.Close()
	}
	if exit == 0 {
		res.Status = "passed"
	} else {
		res.Status = "failed"
	}
	res.DurationMS = time.Since(started).Milliseconds()
	return res
}

func (r *Runner) checkBudget(ctx context.Context, budget *gatesync.Budget) (bool, string) {
	if budget == nil || budget.GrantID == "" || r.Broker == nil {
		return false, ""
	}
	usage, err := r.Broker.Usage(ctx, budget.GrantID)
	if err != nil {
		return false, "budget check unavailable: " + err.Error()
	}
	if budget.MaxRequestsDay > 0 && usage.RequestsDay >= budget.MaxRequestsDay {
		return true, fmt.Sprintf("budget exhausted: %d/%d requests today for grant %s", usage.RequestsDay, budget.MaxRequestsDay, budget.GrantID)
	}
	if budget.MaxTokensDay > 0 && usage.TokensDay >= budget.MaxTokensDay {
		return true, fmt.Sprintf("budget exhausted: %d/%d tokens today for grant %s", usage.TokensDay, budget.MaxTokensDay, budget.GrantID)
	}
	if budget.MaxCostDayUSD > 0 && usage.CostDayUSD >= budget.MaxCostDayUSD {
		return true, fmt.Sprintf("budget exhausted: %.2f/%.2f USD today for grant %s", usage.CostDayUSD, budget.MaxCostDayUSD, budget.GrantID)
	}
	return false, ""
}

func (r *Runner) record(ctx context.Context, run *model.GateRun) {
	if r.Store != nil {
		if err := r.Store.SaveGateRun(context.Background(), run); err != nil && r.Logger != nil {
			r.Logger.Warn("save gate run failed", "error", err)
		}
		if run.Status != model.GateSkipped {
			status := model.EvidenceUnknown
			if run.Status == model.GatePassed {
				status = model.EvidencePassed
			} else if run.Status == model.GateFailed {
				status = model.EvidenceFailed
			}
			_, _ = r.Store.AddEvidence(context.Background(), &model.Evidence{
				ID:             newID("ev-gate"),
				Kind:           model.EvidenceGate,
				Status:         status,
				Ref:            run.ID,
				Verified:       run.Status == model.GatePassed && !run.Tamper,
				IdempotencyKey: "gate-run:" + run.ID,
				CreatedAt:      run.FinishedAt,
				Payload: map[string]string{
					"workspace":    run.Workspace,
					"image":        run.Image,
					"image_digest": run.ImageDigest,
					"gates_hash":   run.GatesHash,
					"tamper":       fmt.Sprint(run.Tamper),
					"reason":       run.Reason,
				},
			})
		}
		e := &model.Event{
			ID:    newID("evt-gate"),
			Type:  model.EventGateRun,
			Actor: firstNonEmpty(run.Actor, "daemon"),
			Message: fmt.Sprintf("gate run %s workspace=%s: %s (%d/%d, tamper=%t, image=%s)",
				run.ID, run.Workspace, run.Status, run.Passed, run.Gates, run.Tamper, run.ImageDigest),
			Timestamp: run.FinishedAt,
		}
		if err := r.Store.AppendEvent(context.Background(), e); err != nil && r.Logger != nil {
			r.Logger.Warn("append gate audit event failed", "error", err)
		}
	}
	if r.Logger != nil {
		r.Logger.Info("gate run finished", "workspace", run.Workspace, "status", run.Status,
			"gates", run.Gates, "passed", run.Passed, "tamper", run.Tamper, "duration_ms", run.DurationMS)
	}
}

func (r *Runner) validateWorkspace(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("workspace is required")
	}
	if !filepath.IsAbs(raw) {
		return "", errors.New("workspace must be an absolute path")
	}
	canon, err := filepath.EvalSymlinks(raw)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	info, err := os.Stat(canon)
	if err != nil {
		return "", fmt.Errorf("stat workspace: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("workspace is not a directory")
	}
	if len(r.AllowedRoots) > 0 {
		ok := false
		for _, root := range r.AllowedRoots {
			cr, err := filepath.EvalSymlinks(root)
			if err != nil {
				continue
			}
			rel, err := filepath.Rel(cr, canon)
			if err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))) {
				ok = true
				break
			}
		}
		if !ok {
			return "", fmt.Errorf("workspace %q is outside the allowed roots", canon)
		}
	}
	return canon, nil
}

func readConfig(workspace string) (gatesync.Config, []byte, error) {
	path := filepath.Join(workspace, ".paperclip", "gates.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// No gate config means "nothing configured", which is a skip, not a
			// failure. This matches the previous gates.py semantics.
			return gatesync.Config{}, nil, nil
		}
		return gatesync.Config{}, nil, fmt.Errorf("read gate config: %w", err)
	}
	var cfg gatesync.Config
	if err := jsonUnmarshal(raw, &cfg); err != nil {
		return gatesync.Config{}, raw, fmt.Errorf("parse gate config: %w", err)
	}
	return cfg, raw, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func mergeStrings(a, b []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(a)+len(b))
	for _, list := range [][]string{a, b} {
		for _, v := range list {
			v = strings.TrimSpace(v)
			if v == "" || seen[v] {
				continue
			}
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
