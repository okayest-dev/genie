// Package worktree allocates and tears down git worktrees for task isolation
// (og-wm7.3). One claimed task maps to one branch and one worktree, so parallel
// sessions cannot collide: git itself forbids a branch being checked out by two
// worktrees at once.
//
// The manager owns the *mechanics* — path and branch schemes, `git worktree
// add`, resume of a crashed run's tree, and keeping the worktree root out of
// version control. It knows nothing about trackers or the claim lifecycle; the
// governed task tools decide *when* to allocate.
package worktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Policy controls whether the governed task lifecycle allocates a worktree.
const (
	// PolicyOff disables worktree allocation entirely.
	PolicyOff = "off"
	// PolicyOptional allows allocation but does not require it.
	PolicyOptional = "optional"
	// PolicyRequired allocates a worktree for every claimed task.
	PolicyRequired = "required"
)

// Merge modes for resolve-time check-in (consumed by og-wm7.3.3).
const (
	MergeAuto = "auto"
	MergePark = "park"
	MergePR   = "pr"
)

// DefaultPath is the worktree root relative to the repository top level.
const DefaultPath = ".genie/worktrees"

// DefaultBranchPrefix prefixes every task branch.
const DefaultBranchPrefix = "task/"

// Config is the [worktree] config surface (og-wm7.3).
type Config struct {
	// Policy is off | optional | required. Required makes task.claim allocate
	// a worktree and rebind the session cwd.
	Policy string
	// Path is the worktree root, relative to the repo top level (or absolute).
	Path string
	// BranchPrefix is prepended to the tracker id to form the task branch.
	BranchPrefix string
	// Merge is the resolve-time check-in mode: auto | park | pr.
	Merge string
	// BaseBranch is the branch merge=auto merges into (default: repo default).
	BaseBranch string
	// DriftCheck is warn/problem when the base branch has moved (og-wm7.3.3).
	DriftCheck string
}

// DefaultConfig returns the default [worktree] configuration: allocation off,
// the standard path and branch schemes, and parked check-in.
func DefaultConfig() Config {
	return Config{
		Policy:       PolicyOff,
		Path:         DefaultPath,
		BranchPrefix: DefaultBranchPrefix,
		Merge:        MergePark,
		BaseBranch:   "",
		DriftCheck:   "warn",
	}
}

// Validate checks the worktree config values.
func (c *Config) Validate() error {
	switch c.Policy {
	case "", PolicyOff, PolicyOptional, PolicyRequired:
	default:
		return fmt.Errorf("worktree.policy must be one of: off, optional, required")
	}
	switch c.Merge {
	case "", MergeAuto, MergePark, MergePR:
	default:
		return fmt.Errorf("worktree.merge must be one of: auto, park, pr")
	}
	switch c.DriftCheck {
	case "", "warn", "block":
	default:
		return fmt.Errorf("worktree.drift_check must be one of: warn, block")
	}
	if c.Policy == PolicyRequired || c.Policy == PolicyOptional {
		if strings.TrimSpace(c.Path) == "" {
			return fmt.Errorf("worktree.path must not be empty when worktree.policy is %q", c.Policy)
		}
		if strings.TrimSpace(c.BranchPrefix) == "" {
			return fmt.Errorf("worktree.branch_prefix must not be empty when worktree.policy is %q", c.Policy)
		}
	}
	return nil
}

// Runner runs a git command in dir and returns its combined output. It is the
// seam that lets the manager be exercised without a real git binary.
type Runner interface {
	Run(ctx context.Context, dir string, args ...string) (string, error)
}

// execRunner is the production Runner over the git binary.
type execRunner struct{}

func (execRunner) Run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// ManagerInterface is the resolve-time seam the git gate consumes. *Manager
// implements it; the interface keeps the gate evaluator testable without git.
type ManagerInterface interface {
	CheckWorktreeState(ctx context.Context, taskID, registeredWorktree string) (WorktreeState, error)
	CheckIn(ctx context.Context, taskID string) (CheckInResult, error)
	Teardown(ctx context.Context, taskID string) error
}

// WorktreeState is the verified git state of a task's worktree at resolve time.
type WorktreeState struct {
	// IsTaskWorktree is true when the session's registered worktree matches the
	// worktree the manager expects for the task and git agrees it is registered.
	IsTaskWorktree bool
	// TaskBranchExists is true when the task branch exists locally.
	TaskBranchExists bool
	// WorkCommitted is true when the task worktree has no uncommitted changes.
	WorkCommitted bool
	// NoStrayWorktrees is true when only the main checkout and the task worktree
	// are registered with git.
	NoStrayWorktrees bool
	// DiffConfined is true when the task worktree has the task branch checked
	// out, so its commits cannot land on another branch.
	DiffConfined bool
	// CurrentCommit is the commit the task worktree is on.
	CurrentCommit string
	// TaskBranch is the branch the manager expects for the task.
	TaskBranch string
	// WorktreePath is the path the manager expects for the task.
	WorktreePath string
}

// Manager allocates and tears down task worktrees for one repository.
type Manager struct {
	repoRoot string
	cfg      Config
	runner   Runner
}

// Allocation is the result of allocating a task's worktree.
type Allocation struct {
	TaskID    string
	TrackerID string
	Path      string
	Branch    string
	// Resumed is true when an existing branch or worktree from a prior run was
	// reused rather than freshly created.
	Resumed bool
}

// New creates a Manager over repoRoot using the git binary.
func New(repoRoot string, cfg Config) *Manager {
	return newManager(repoRoot, cfg, execRunner{})
}

// newManager is the seam used by tests to inject a fake Runner.
func newManager(repoRoot string, cfg Config, runner Runner) *Manager {
	if cfg.Path == "" {
		cfg.Path = DefaultPath
	}
	if cfg.BranchPrefix == "" {
		cfg.BranchPrefix = DefaultBranchPrefix
	}
	if cfg.Merge == "" {
		cfg.Merge = MergePark
	}
	return &Manager{repoRoot: repoRoot, cfg: cfg, runner: runner}
}

// RepoRoot returns the repository top level the manager operates on.
func (m *Manager) RepoRoot() string { return m.repoRoot }

// Config returns the manager's configuration.
func (m *Manager) Config() Config { return m.cfg }

// Path returns the worktree path for a task: <repo>/<path>/<task-id>.
func (m *Manager) Path(taskID string) string {
	return filepath.Join(m.worktreeRoot(), taskID)
}

// worktreeRoot returns the absolute directory holding the task worktrees.
func (m *Manager) worktreeRoot() string {
	if filepath.IsAbs(m.cfg.Path) {
		return filepath.Clean(m.cfg.Path)
	}
	return filepath.Join(m.repoRoot, m.cfg.Path)
}

// Branch returns the task branch for a tracker id: <branch_prefix><tracker-id>.
// A missing trailing slash on the prefix is normalized in.
func (m *Manager) Branch(trackerID string) string {
	prefix := m.cfg.BranchPrefix
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return prefix + trackerID
}

// DetectRoot returns the repository top level containing dir and whether dir is
// inside a git work tree. A non-repo directory reports (_, false, nil).
func DetectRoot(ctx context.Context, dir string) (string, bool, error) {
	out, err := (execRunner{}).Run(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false, nil
	}
	root := strings.TrimSpace(out)
	if root == "" {
		return "", false, nil
	}
	return root, true, nil
}

// Allocate creates (or resumes) the worktree and branch for a task. An existing
// registered worktree, or an existing branch from a crashed run, is resumed
// rather than recreated. A path occupied by something that is not a registered
// worktree is an error.
func (m *Manager) Allocate(ctx context.Context, taskID, trackerID string) (*Allocation, error) {
	path := m.Path(taskID)
	branch := m.Branch(trackerID)
	alloc := &Allocation{TaskID: taskID, TrackerID: trackerID, Path: path, Branch: branch}

	if m.isRegisteredWorktree(ctx, path) {
		alloc.Resumed = true
		return alloc, nil
	}

	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("worktree path %s already exists but is not a registered git worktree", path)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect worktree path %s: %w", path, err)
	}

	if err := m.ensureExcluded(ctx); err != nil {
		return nil, err
	}

	var args []string
	if m.branchExists(ctx, branch) {
		args = []string{"worktree", "add", path, branch}
		alloc.Resumed = true
	} else {
		args = []string{"worktree", "add", path, "-b", branch}
	}
	if _, err := m.runner.Run(ctx, m.repoRoot, args...); err != nil {
		return nil, fmt.Errorf("allocate worktree for task %s: %w", taskID, err)
	}
	return alloc, nil
}

// Remove tears down a task's worktree, leaving its branch in place.
func (m *Manager) Remove(ctx context.Context, taskID string) error {
	path := m.Path(taskID)
	if _, err := m.runner.Run(ctx, m.repoRoot, "worktree", "remove", "--force", path); err != nil {
		return fmt.Errorf("remove worktree for task %s: %w", taskID, err)
	}
	return nil
}

// CheckWorktreeState verifies the worktree state at task resolve time.
//
// registeredWorktree is the worktree the session recorded at claim time (empty
// when no worktree was allocated). The gate passes only when the session's
// registered worktree is the one the manager expects for taskID, the task
// branch exists, the worktree is clean and checked out on the task branch, and
// no stray worktrees are registered.
func (m *Manager) CheckWorktreeState(ctx context.Context, taskID, registeredWorktree string) (WorktreeState, error) {
	path := m.Path(taskID)
	branch := m.Branch(taskID)
	state := WorktreeState{
		WorktreePath: path,
		TaskBranch:   branch,
	}

	// Current tree is the task's registered worktree: the session's recorded
	// path matches the manager's expected path, and git lists it as a worktree.
	if registeredWorktree != "" &&
		filepath.Clean(registeredWorktree) == filepath.Clean(path) &&
		m.isRegisteredWorktree(ctx, path) {
		state.IsTaskWorktree = true
	}

	state.TaskBranchExists = m.branchExists(ctx, branch)

	if state.IsTaskWorktree {
		if status, err := m.runner.Run(ctx, path, "status", "--porcelain"); err == nil {
			state.WorkCommitted = strings.TrimSpace(status) == ""
		}
		if head, err := m.runner.Run(ctx, path, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
			state.DiffConfined = strings.TrimSpace(head) == branch
		}
		if commit, err := m.runner.Run(ctx, path, "rev-parse", "HEAD"); err == nil {
			state.CurrentCommit = strings.TrimSpace(commit)
		}
	}

	state.NoStrayWorktrees = m.noStrayWorktrees(ctx, path)

	return state, nil
}

// noStrayWorktrees reports whether git knows about any worktree outside the
// main checkout and the manager's configured worktree root. Sibling task
// worktrees from parallel sessions live under that root and are not stray; a
// worktree created anywhere else is.
func (m *Manager) noStrayWorktrees(ctx context.Context, taskPath string) bool {
	out, err := m.runner.Run(ctx, m.repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return false
	}
	root := m.worktreeRoot()
	for _, line := range strings.Split(out, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "worktree ")
		if !ok {
			continue
		}
		clean := filepath.Clean(rest)
		if clean == filepath.Clean(m.repoRoot) || clean == filepath.Clean(taskPath) {
			continue
		}
		if rel, err := filepath.Rel(root, clean); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		return false
	}
	return true
}

// isRegisteredWorktree reports whether path is listed by git as a worktree.
func (m *Manager) isRegisteredWorktree(ctx context.Context, path string) bool {
	out, err := m.runner.Run(ctx, m.repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return false
	}
	want := filepath.Clean(path)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "worktree "); ok {
			if filepath.Clean(rest) == want {
				return true
			}
		}
	}
	return false
}

// branchExists reports whether the local branch already exists.
func (m *Manager) branchExists(ctx context.Context, branch string) bool {
	_, err := m.runner.Run(ctx, m.repoRoot, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// ensureExcluded adds the worktree root to the repository's shared
// info/exclude so allocated trees never show up as untracked content. It is
// idempotent: a root already excluded is left untouched.
func (m *Manager) ensureExcluded(ctx context.Context) error {
	out, err := m.runner.Run(ctx, m.repoRoot, "rev-parse", "--git-common-dir")
	if err != nil {
		return fmt.Errorf("resolve git dir: %w", err)
	}
	gitDir := strings.TrimSpace(out)
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(m.repoRoot, gitDir)
	}
	excludePath := filepath.Join(gitDir, "info", "exclude")
	entry := "/" + strings.Trim(m.cfg.Path, "/") + "/"

	data, err := os.ReadFile(excludePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", excludePath, err)
	}
	if strings.Contains(string(data), entry) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(excludePath), err)
	}
	f, err := os.OpenFile(excludePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", excludePath, err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "\n# task worktrees (og-wm7.3)\n%s\n", entry); err != nil {
		return fmt.Errorf("write %s: %w", excludePath, err)
	}
	return nil
}

// CheckIn performs the resolve-time check-in flow for a task's branch based on
// the configured merge mode (auto | park | pr). It returns the merge result the
// resolution traceability record cites.
func (m *Manager) CheckIn(ctx context.Context, taskID string) (CheckInResult, error) {
	cfg := m.Config()
	result := CheckInResult{TaskID: taskID, MergeMode: cfg.Merge}

	baseBranch, err := m.baseBranch(ctx, cfg.BaseBranch)
	if err != nil {
		return result, err
	}
	result.BaseBranch = baseBranch

	taskBranch := m.Branch(taskID)

	if baseBranch != "" {
		drift, derr := m.checkDrift(ctx, baseBranch, taskBranch)
		if derr == nil && drift {
			switch cfg.DriftCheck {
			case "block":
				return result, fmt.Errorf("base branch %q has moved beyond task branch %q; rebase before merging or set worktree.drift_check=warn", baseBranch, taskBranch)
			case "", "warn":
				result.DriftWarning = fmt.Sprintf("base branch %q has moved; consider rebasing before merging", baseBranch)
			}
		}
	}

	switch cfg.Merge {
	case MergeAuto:
		if err := m.autoMerge(ctx, baseBranch, taskBranch); err != nil {
			return result, err
		}
		result.Merged = true
		result.MergedBranch = taskBranch

	case MergePark:
		// Parked (default): the branch is left for a human or CI to merge.
		result.ParkedBranch = taskBranch

	case MergePR:
		if _, err := m.runner.Run(ctx, m.repoRoot, "push", "origin", taskBranch); err != nil {
			return result, fmt.Errorf("push task branch %s: %w", taskBranch, err)
		}
		result.PushedBranch = taskBranch
	}

	return result, nil
}

// baseBranch resolves the branch merge=auto merges into: the configured branch
// when set, else origin/HEAD, else main/master. An empty return means no base
// branch could be resolved.
func (m *Manager) baseBranch(ctx context.Context, configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	if out, err := m.runner.Run(ctx, m.repoRoot, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"); err == nil {
		if name := strings.TrimPrefix(strings.TrimSpace(out), "refs/remotes/origin/"); name != "" {
			return name, nil
		}
	}
	for _, candidate := range []string{"main", "master"} {
		if m.branchExists(ctx, candidate) {
			return candidate, nil
		}
	}
	return "", nil
}

// autoMerge merges taskBranch into baseBranch in the main checkout. It refuses
// to run when the main checkout is dirty, so a resolve never clobbers the
// user's in-progress work.
func (m *Manager) autoMerge(ctx context.Context, baseBranch, taskBranch string) error {
	if baseBranch == "" {
		return fmt.Errorf("cannot auto-merge: no base branch resolved (set worktree.base_branch)")
	}
	status, err := m.runner.Run(ctx, m.repoRoot, "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("inspect main checkout before merge: %w", err)
	}
	if strings.TrimSpace(status) != "" {
		return fmt.Errorf("cannot auto-merge into %q: main checkout has uncommitted changes; commit them or use worktree.merge=park", baseBranch)
	}
	if _, err := m.runner.Run(ctx, m.repoRoot, "checkout", baseBranch); err != nil {
		return fmt.Errorf("checkout base branch %s: %w", baseBranch, err)
	}
	msg := fmt.Sprintf("Merge task branch %s", taskBranch)
	if _, err := m.runner.Run(ctx, m.repoRoot, "merge", "--ff-only", taskBranch); err != nil {
		if _, err2 := m.runner.Run(ctx, m.repoRoot, "merge", "--no-ff", "-m", msg, taskBranch); err2 != nil {
			return fmt.Errorf("merge %s into %s: %w", taskBranch, baseBranch, err2)
		}
	}
	return nil
}

// CheckInResult holds the result of the check-in operation.
type CheckInResult struct {
	TaskID       string `json:"task_id"`
	MergeMode    string `json:"merge_mode"`
	BaseBranch   string `json:"base_branch,omitempty"`
	Merged       bool   `json:"merged"`
	MergedBranch string `json:"merged_branch,omitempty"`
	ParkedBranch string `json:"parked_branch,omitempty"`
	PushedBranch string `json:"pushed_branch,omitempty"`
	PRUrl        string `json:"pr_url,omitempty"`
	DriftWarning string `json:"drift_warning,omitempty"`
}

// checkDrift reports whether baseBranch has commits the task branch is missing
// (the base moved since the branch was cut).
func (m *Manager) checkDrift(ctx context.Context, baseBranch, taskBranch string) (bool, error) {
	out, err := m.runner.Run(ctx, m.repoRoot, "rev-list", "--left-right", "--count", baseBranch+"..."+taskBranch)
	if err != nil {
		return false, err
	}
	parts := strings.Fields(strings.TrimSpace(out))
	if len(parts) != 2 {
		return false, fmt.Errorf("unexpected rev-list output %q", strings.TrimSpace(out))
	}
	baseAhead, err := strconv.Atoi(parts[0])
	if err != nil {
		return false, err
	}
	return baseAhead > 0, nil
}

// Teardown removes the worktree after check-in, leaving the branch in place.
func (m *Manager) Teardown(ctx context.Context, taskID string) error {
	return m.Remove(ctx, taskID)
}
