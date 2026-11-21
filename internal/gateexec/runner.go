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
	// FingerprintLimits bounds the workspace fingerprint (zero = defaults).
	FingerprintLimits FingerprintLimits
}

// Request is one gate execution.
type Request struct {
	Workspace string
	IssueID   string
	Image     string
	Actor     string
	Timeout   time.Duration
	// UpstreamAgentID is the agent whose work triggered the run (optional).
	UpstreamAgentID string
	// Phases restricts the run to these phases (pre_run, after_run,
	// post_run). Empty runs every phase in order.
	Phases []string
}

const (
	defaultImage   = "node:22-bookworm"
	defaultTimeout = 15 * time.Minute
	phaseOrder     = "pre_run,after_run,post_run"
)

// Run executes the workspace gates and records the result. Setup problems
// (missing workspace, unreadable config) return an error; a failing gate is a
// normal result with Status failed.
func (r *Runner) Run(ctx context.Context, req Request) (result *model.GateRun, retErr error) {
	workspace, err := r.ValidateWorkspace(req.Workspace)
	if err != nil {
		return nil, err
	}
	phases, err := normalizePhases(req.Phases)
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
		IssueID:   strings.TrimSpace(req.IssueID),
		Actor:     req.Actor,
		StartedAt: time.Now().UTC(),
		Status:    model.GateError,

		UpstreamAgentID: strings.TrimSpace(req.UpstreamAgentID),
		Phases:          phases,
	}

	if r.Store != nil {
		run.Reason = "gate run incomplete"
		if err := r.Store.SaveGateRun(runCtx, run); err != nil {
			return nil, fmt.Errorf("record gate start: %w", err)
		}
		run.Reason = ""
	}
	defer func() {
		run.FinishedAt = time.Now().UTC()
		run.DurationMS = time.Since(run.StartedAt).Milliseconds()
		if err := r.record(ctx, run); err != nil {
			result, retErr = run, err
		}
	}()
	base := r.BaseDir
	if base == "" {
		base = filepath.Join(os.TempDir(), "dockweave-gates")
	}
	if err := os.MkdirAll(base, 0o750); err != nil {
		run.Reason = "create gate base dir: " + err.Error()
		run.FinishedAt = time.Now().UTC()
		return run, nil
	}
	tmp, err := os.MkdirTemp(base, "run-")
	if err != nil {
		run.Reason = "create gate workdir: " + err.Error()
		run.FinishedAt = time.Now().UTC()
		return run, nil
	}
	defer os.RemoveAll(tmp)
	work := filepath.Join(tmp, "work")
	sourceBefore, err := Fingerprint(runCtx, workspace, r.FingerprintLimits)
	if err != nil {
		run.Reason = "source fingerprint: " + err.Error()
		return run, nil
	}
	if err := copyTree(runCtx, workspace, work); err != nil {
		run.Reason = err.Error()
		run.FinishedAt = time.Now().UTC()
		return run, nil
	}

	snapshotFP, err := Fingerprint(runCtx, work, r.FingerprintLimits)
	if err != nil {
		run.Reason = "snapshot fingerprint: " + err.Error()
		return run, nil
	}
	sourceAfter, err := Fingerprint(runCtx, workspace, r.FingerprintLimits)
	if err != nil || sourceBefore != snapshotFP || sourceAfter != snapshotFP {
		run.Reason = "workspace changed while creating gate snapshot"
		if err != nil {
			run.Reason += ": " + err.Error()
		}
		return run, nil
	}
	run.WorkspaceFingerprint = snapshotFP
	cfg, cfgRaw, err := readConfigContext(runCtx, work)
	if err != nil {
		run.Reason = err.Error()
		run.FinishedAt = time.Now().UTC()
		return run, nil
	}
	run.GatesHash = hashString(cfgRaw)
	if len(cfg.Gates) == 0 {
		run.Status = model.GateSkipped
		run.Reason = "no after_run gates configured"
		run.FinishedAt = time.Now().UTC()
		return run, nil
	}
	run.Gates = len(cfg.Gates)

	image := firstNonEmpty(req.Image, cfg.RunnerImage, r.DefaultImage, os.Getenv("DOCKWEAVE_GATE_IMAGE"), defaultImage)
	run.Image = image
	if err := r.Docker.EnsureImage(runCtx, image, io.Discard); err != nil {
		run.Reason = "gate runner image unavailable: " + err.Error()
		run.FinishedAt = time.Now().UTC()
		return run, nil
	}
	if digest, err := r.Docker.ImageDigest(runCtx, image); err == nil {
		run.ImageDigest = digest
	}

	if b := r.checkBudget(runCtx, cfg.Budget); b != nil {
		run.Budget = b
		if b.Exceeded {
			run.Status = model.GateFailed
			run.Reason = b.Reason
			run.Warnings = append(run.Warnings, b.Reason)
			run.FinishedAt = time.Now().UTC()
			return run, nil
		}
	}
	// The daily budget cost feeds the `budget` variable of gate expressions.
	// It is only usable when a real usage snapshot exists (fail-closed).
	budgetEnv := budgetExprEnv(run.Budget)

	protected := mergeStrings(defaultProtected, cfg.Protected)
	before, err := hashSelected(runCtx, work, nil, protected)
	if err != nil {
		run.Reason = "protected baseline: " + err.Error()
		return run, nil
	}
	originalHashes, err := hashSelected(runCtx, work, defaultIgnore, nil)
	if err != nil {
		run.Reason = "policy baseline: " + err.Error()
		return run, nil
	}
	manifests, err := readManifestBaseline(runCtx, work, cfgRaw, cfg.DenyNewDependencies)
	if err != nil {
		run.Reason = "dependency baseline: " + err.Error()
		return run, nil
	}
	if cfg.DenyNewDependencies {
		run.Warnings = append(run.Warnings, "dependency manifest scope: "+strings.Join(manifests.scope, ", "))
	}

	order := strings.Split(phaseOrder, ",")
	selected := order
	if len(phases) > 0 {
		selected = phases
	}
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
			return run, nil
		}
		byPhase[when] = append(byPhase[when], g)
	}
	scheduled := 0
	for _, ph := range selected {
		scheduled += len(byPhase[ph])
	}
	if scheduled == 0 {
		run.Status = model.GateSkipped
		run.Reason = "no gates configured for phases " + strings.Join(selected, ",")
		run.FinishedAt = time.Now().UTC()
		return run, nil
	}
	run.Gates = scheduled

	tamper := false
gateLoop:
	for _, phase := range selected {
		for _, gate := range byPhase[phase] {
			result := r.runGate(runCtx, work, image, run.ID, gate, budgetEnv)
			run.Results = append(run.Results, result)
			if result.Status == "passed" {
				run.Passed++
			}
			after, err := hashSelected(runCtx, work, nil, protected)
			if err != nil {
				run.Reason = "protected verification: " + err.Error()
				break gateLoop
			}
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

	// Post-run policies compare with the immutable pre-execution baseline.
	workHashes, err := hashSelected(runCtx, work, defaultIgnore, nil)
	if err != nil {
		run.Reason = "policy verification: " + err.Error()
		return run, nil
	}
	changed := diffTrees(originalHashes, workHashes)
	if len(changed) > 0 || cfg.AllowPaths != nil || cfg.DenyNewDependencies || cfg.SecretScan {
		run.Diff = &model.GateDiff{Changed: changed}
		if cfg.AllowPaths != nil {
			run.Diff.OutOfScope = outOfScope(changed, cfg.AllowPaths)
		}
		if cfg.DenyNewDependencies {
			run.Diff.NewDependencies, err = manifests.compare(runCtx, work)
			if err != nil {
				run.Reason = "dependency verification: " + err.Error()
				return run, nil
			}
		}
		if cfg.SecretScan {
			run.Diff.Secrets, err = scanSecretsContext(runCtx, work, changed)
			if err != nil {
				run.Reason = "secret verification: " + err.Error()
				return run, nil
			}
		}
	}

	if b := r.checkBudget(runCtx, cfg.Budget); b != nil {
		run.Budget = b
		if b.Exceeded {
			run.Warnings = append(run.Warnings, b.Reason)
			run.Reason = b.Reason
		}
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
	return run, nil
}

func (r *Runner) runGate(ctx context.Context, work, image, runID string, gate gatesync.Gate, env ExprEnv) model.GateResult {
	res := model.GateResult{ID: gate.ID, Command: gate.Command, Cwd: gate.Cwd, Exit: -1}
	started := time.Now()
	if gate.TimeoutSec < 0 || gate.TimeoutSec > 86400 {
		res.Status, res.Reason = "error", "gate timeout must be between 0 and 86400 seconds"
		return res
	}
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
	env.Exit = int(exit)
	res.Status, res.Reason = gateVerdict(gate, env)
	res.DurationMS = time.Since(started).Milliseconds()
	return res
}

func (r *Runner) checkBudget(ctx context.Context, budget *gatesync.Budget) *model.GateBudget {
	if budget == nil || budget.GrantID == "" {
		return nil
	}
	out := &model.GateBudget{
		GrantID:        budget.GrantID,
		MaxRequestsDay: budget.MaxRequestsDay,
		MaxTokensDay:   budget.MaxTokensDay,
		MaxCostDayUSD:  budget.MaxCostDayUSD,
	}
	if r.Broker == nil {
		out.Unavailable = true
		out.Reason = "budget check unavailable: broker not configured"
		if !budgetWarnOnly(budget) {
			out.Exceeded = true
		}
		return out
	}
	usage, err := r.Broker.Usage(ctx, budget.GrantID)
	if err != nil {
		out.Unavailable = true
		out.Reason = "budget check unavailable: " + err.Error()
		if !budgetWarnOnly(budget) {
			out.Exceeded = true
		}
		return out
	}
	out.RequestsDay = usage.RequestsDay
	out.TokensDay = usage.TokensDay
	out.CostDayUSD = usage.CostDayUSD
	switch {
	case budget.MaxRequestsDay > 0 && usage.RequestsDay >= budget.MaxRequestsDay:
		out.Exceeded = true
		out.Reason = fmt.Sprintf("budget exhausted: %d/%d requests today for grant %s", usage.RequestsDay, budget.MaxRequestsDay, budget.GrantID)
	case budget.MaxTokensDay > 0 && usage.TokensDay >= budget.MaxTokensDay:
		out.Exceeded = true
		out.Reason = fmt.Sprintf("budget exhausted: %d/%d tokens today for grant %s", usage.TokensDay, budget.MaxTokensDay, budget.GrantID)
	case budget.MaxCostDayUSD > 0 && usage.CostDayUSD >= budget.MaxCostDayUSD:
		out.Exceeded = true
		out.Reason = fmt.Sprintf("budget exhausted: %.2f/%.2f USD today for grant %s", usage.CostDayUSD, budget.MaxCostDayUSD, budget.GrantID)
	}
	return out
}

// budgetExprEnv builds the expression environment from the run's budget
// snapshot. Without a configured budget, or when the broker could not be
// queried, the `budget` variable is marked unavailable so that an expression
// referencing it fails instead of silently comparing against 0.
func budgetExprEnv(b *model.GateBudget) ExprEnv {
	switch {
	case b == nil:
		return ExprEnv{BudgetUnavailable: "no budget configured for this workspace"}
	case b.Unavailable:
		return ExprEnv{BudgetUnavailable: b.Reason}
	default:
		return ExprEnv{Budget: b.CostDayUSD, BudgetAvailable: true}
	}
}

// gateVerdict derives a gate result from the command exit code and the
// optional deterministic expression contract (GP-005). Without an expression
// the exit code decides (0 = passed). With an expression, its result replaces
// the exit-code verdict, so `command != 0` can express an expected failure;
// on_true/on_false become the reason. An invalid expression, or one that
// needs an unavailable budget, is an error (fail-closed).
func gateVerdict(gate gatesync.Gate, env ExprEnv) (status, reason string) {
	expr := strings.TrimSpace(gate.Expression)
	if expr == "" {
		if env.Exit == 0 {
			return "passed", ""
		}
		return "failed", ""
	}
	_, _, reasonTrue, reasonFalse := gatesync.ResolveRouting(gate.OnTrue, gate.OnFalse, gate.ReasonTrue, gate.ReasonFalse)
	ok, err := Evaluate(expr, env)
	switch {
	case err != nil:
		return "error", "gate expression: " + err.Error()
	case ok:
		return "passed", reasonTrue
	default:
		if reasonFalse != "" {
			return "failed", reasonFalse
		}
		return "failed", "gate expression evaluated to false"
	}
}

// normalizePhases validates an explicit phase selection and returns it in
// execution order without duplicates. Empty input selects all phases (nil).
func normalizePhases(in []string) ([]string, error) {
	want := map[string]bool{}
	for _, p := range in {
		for _, part := range strings.Split(p, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if !strings.Contains(","+phaseOrder+",", ","+part+",") {
				return nil, fmt.Errorf("invalid gate phase %q (want pre_run, after_run or post_run)", part)
			}
			want[part] = true
		}
	}
	if len(want) == 0 {
		return nil, nil
	}
	var out []string
	for _, ph := range strings.Split(phaseOrder, ",") {
		if want[ph] {
			out = append(out, ph)
		}
	}
	return out, nil
}

// budgetWarnOnly reports whether a configured budget should degrade to a
// warning instead of failing the run when the broker is unreachable. The
// default is fail-closed.
func budgetWarnOnly(budget *gatesync.Budget) bool {
	return budget != nil && strings.EqualFold(strings.TrimSpace(budget.OnUnavailable), "warn")
}

func (r *Runner) record(ctx context.Context, run *model.GateRun) error {
	if r.Store != nil {
		var evidence *model.Evidence
		if run.Status != model.GateSkipped {
			status := model.EvidenceUnknown
			if run.Status == model.GatePassed {
				status = model.EvidencePassed
			} else if run.Status == model.GateFailed {
				status = model.EvidenceFailed
			}
			evidence = &model.Evidence{
				ID: newID("ev-gate"), Kind: model.EvidenceGate, Status: status, Ref: run.ID,
				Verified:       run.Status == model.GatePassed && !run.Tamper && run.WorkspaceFingerprint != "",
				IdempotencyKey: "gate-run:" + run.ID, CreatedAt: run.FinishedAt,
				Payload: map[string]string{"workspace": run.Workspace, "image": run.Image, "image_digest": run.ImageDigest, "gates_hash": run.GatesHash, "tamper": fmt.Sprint(run.Tamper), "reason": run.Reason},
			}
		}
		event := &model.Event{ID: newID("evt-gate"), Type: model.EventGateRun, Actor: firstNonEmpty(run.Actor, "daemon"),
			Message: fmt.Sprintf("gate run %s workspace=%s: %s (%d/%d, tamper=%t, image=%s)", run.ID, run.Workspace, run.Status, run.Passed, run.Gates, run.Tamper, run.ImageDigest), Timestamp: run.FinishedAt}
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := r.Store.CompleteGateRun(finishCtx, run, evidence, event); err != nil {
			return fmt.Errorf("record gate completion: %w", err)
		}
	}
	if r.Logger != nil {
		r.Logger.Info("gate run finished", "workspace", run.Workspace, "status", run.Status, "gates", run.Gates, "passed", run.Passed, "tamper", run.Tamper, "duration_ms", run.DurationMS)
	}
	return nil
}

// ValidateWorkspace canonicalises a workspace path (EvalSymlinks) and checks
// it against AllowedRoots. The pipeline API uses it for edge workspaces.
func (r *Runner) ValidateWorkspace(raw string) (string, error) {
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
	return readConfigContext(context.Background(), workspace)
}
func readConfigContext(ctx context.Context, workspace string) (gatesync.Config, []byte, error) {
	root, err := openRoot(workspace)
	if err != nil {
		return gatesync.Config{}, nil, err
	}
	defer root.close()
	raw, err := root.readFile(ctx, ".paperclip/gates.json", 4<<20)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
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
