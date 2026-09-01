package gitworktree

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

const handlePrefix = "ws_"

type Config struct {
	Root          string
	GitExecutable string
}

type Provider struct {
	root          string
	worktreesRoot string
	metadataRoot  string
	gitExecutable string
}

var _ ports.WorkspaceProvider = (*Provider)(nil)

func New(config Config) (*Provider, error) {
	if strings.TrimSpace(config.Root) == "" {
		return nil, fmt.Errorf("%w: root is required", ErrInvalidConfig)
	}
	root, err := filepath.Abs(config.Root)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve root: %v", ErrInvalidConfig, err)
	}
	root = filepath.Clean(root)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("%w: create root: %v", ErrInvalidConfig, err)
	}
	root, err = canonicalExistingPath(root)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve root symlinks: %v", ErrInvalidConfig, err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("%w: canonicalize root: %v", ErrInvalidConfig, err)
	}

	gitExecutable := strings.TrimSpace(config.GitExecutable)
	if gitExecutable == "" {
		gitExecutable = "git"
	}
	gitExecutable, err = exec.LookPath(gitExecutable)
	if err != nil {
		return nil, fmt.Errorf("%w: find git executable: %v", ErrInvalidConfig, err)
	}

	provider := &Provider{
		root:          root,
		worktreesRoot: filepath.Join(root, "worktrees"),
		metadataRoot:  filepath.Join(root, "metadata"),
		gitExecutable: gitExecutable,
	}
	for _, directory := range []string{provider.worktreesRoot, provider.metadataRoot} {
		if err := ensureLexicallyWithin(provider.root, directory); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
		}
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, fmt.Errorf("%w: create provider directory: %v", ErrInvalidConfig, err)
		}
	}
	return provider, nil
}

func (p *Provider) Provision(ctx context.Context, spec ports.ProvisionSpec) (ports.WorkspaceHandle, error) {
	if err := validateProvisionSpec(spec); err != nil {
		return ports.WorkspaceHandle{}, err
	}

	repositoryPath, err := p.validateLocalRepository(ctx, spec.LocalRepository)
	if err != nil {
		return ports.WorkspaceHandle{}, err
	}
	baseRevision, err := p.resolveCommit(ctx, repositoryPath, spec.BaseRef)
	if err != nil {
		return ports.WorkspaceHandle{}, err
	}
	handle, err := deriveHandle(spec.FamilyID, spec.RepositoryID, spec.Generation)
	if err != nil {
		return ports.WorkspaceHandle{}, err
	}
	branchRef := deriveBranchRef(spec.FamilyID, spec.RepositoryID, spec.Generation)
	metadata := workspaceMetadata{
		SchemaVersion:   metadataSchemaVersion,
		Handle:          handle.String(),
		RepositoryID:    string(spec.RepositoryID),
		LocalRepository: repositoryPath,
		BaseRevision:    baseRevision,
		FamilyID:        string(spec.FamilyID),
		WorkspaceSetID:  string(spec.WorkspaceSetID),
		Generation:      spec.Generation,
		BranchRef:       branchRef,
	}

	released, err := p.isReleased(handle)
	if err != nil {
		return ports.WorkspaceHandle{}, err
	}
	if released {
		return ports.WorkspaceHandle{}, ErrWorkspaceReleased
	}
	existing, err := p.readMetadata(handle)
	switch {
	case err == nil:
		if !metadataEqual(existing, metadata) {
			return ports.WorkspaceHandle{}, ErrProvisionConflict
		}
		if _, err := p.inspectActive(ctx, handle, existing); err != nil {
			return ports.WorkspaceHandle{}, err
		}
		return handle, nil
	case !errors.Is(err, ErrWorkspaceNotFound):
		return ports.WorkspaceHandle{}, err
	}

	workspacePath, err := p.workspacePath(handle)
	if err != nil {
		return ports.WorkspaceHandle{}, err
	}
	if _, err := os.Lstat(workspacePath); err == nil {
		return ports.WorkspaceHandle{}, fmt.Errorf("%w: target already exists", ErrProvisionConflict)
	} else if !errors.Is(err, os.ErrNotExist) {
		return ports.WorkspaceHandle{}, fmt.Errorf("inspect workspace target: %w", err)
	}

	if _, err := p.runGit(ctx, repositoryPath, "worktree", "add", "-b", branchRef, workspacePath, baseRevision); err != nil {
		return ports.WorkspaceHandle{}, err
	}
	if err := p.writeMetadata(metadata); err != nil {
		_, _ = p.runGit(context.Background(), repositoryPath, "worktree", "remove", workspacePath)
		return ports.WorkspaceHandle{}, fmt.Errorf("persist workspace metadata: %w", err)
	}
	if _, err := p.inspectActive(ctx, handle, metadata); err != nil {
		return ports.WorkspaceHandle{}, err
	}
	return handle, nil
}

func (p *Provider) Inspect(ctx context.Context, handle ports.WorkspaceHandle) (ports.WorkspaceInspection, error) {
	if err := validateHandle(handle); err != nil {
		return ports.WorkspaceInspection{}, err
	}
	metadata, err := p.readMetadata(handle)
	if err != nil {
		return ports.WorkspaceInspection{}, err
	}
	released, err := p.isReleased(handle)
	if err != nil {
		return ports.WorkspaceInspection{}, err
	}
	if released {
		return releasedInspection(handle, metadata), nil
	}
	return p.inspectActive(ctx, handle, metadata)
}

// WorkingDirectory resolves a WorkspaceHandle this Provider issued to a real
// filesystem path suitable as a child process's working directory. This is
// the one sanctioned bridge from an opaque handle to a spawnable cwd — for
// starting the real AgentExecutor/helper process a workspace exists to
// support — not a general path leak: the handle is still fully validated
// (existence, identity, containment) exactly as Inspect does, and nothing
// about the handle's own representation changes. Callers still cannot
// derive or guess a workspace's path any other way.
func (p *Provider) WorkingDirectory(ctx context.Context, handle ports.WorkspaceHandle) (string, error) {
	if _, err := p.Inspect(ctx, handle); err != nil {
		return "", err
	}
	return p.workspacePath(handle)
}

func (p *Provider) CaptureRevision(ctx context.Context, handle ports.WorkspaceHandle) (workspace.Revision, error) {
	inspection, err := p.Inspect(ctx, handle)
	if err != nil {
		return workspace.Revision{}, err
	}
	if inspection.State == ports.WorkspaceReleased {
		return workspace.Revision{}, ErrWorkspaceReleased
	}
	return inspection.CurrentRevision, nil
}

func (p *Provider) Diff(
	ctx context.Context,
	handle ports.WorkspaceHandle,
	base workspace.Revision,
) (ports.WorkspaceDiff, error) {
	inspection, err := p.Inspect(ctx, handle)
	if err != nil {
		return ports.WorkspaceDiff{}, err
	}
	if inspection.State == ports.WorkspaceReleased {
		return ports.WorkspaceDiff{}, ErrWorkspaceReleased
	}
	if base.RepositoryID != inspection.RepositoryID ||
		base.WorkspaceGeneration != inspection.Generation ||
		!validObjectID(base.VCSObjectID) {
		return ports.WorkspaceDiff{}, fmt.Errorf("%w: base revision does not belong to workspace", ErrInvalidSpec)
	}

	workspacePath, err := p.workspacePath(handle)
	if err != nil {
		return ports.WorkspaceDiff{}, err
	}
	if _, err := p.runGit(ctx, workspacePath, "cat-file", "-e", base.VCSObjectID+"^{commit}"); err != nil {
		return ports.WorkspaceDiff{}, fmt.Errorf("%w: base revision is not a commit in workspace: %v", ErrInvalidSpec, err)
	}
	status, err := p.runGit(ctx, workspacePath, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return ports.WorkspaceDiff{}, err
	}
	files, err := parsePorcelainV1Z(status)
	if err != nil {
		return ports.WorkspaceDiff{}, err
	}
	patch, err := p.runGit(ctx, workspacePath, "diff", "--binary", "--no-ext-diff", "--full-index", base.VCSObjectID, "--")
	if err != nil {
		return ports.WorkspaceDiff{}, err
	}
	return ports.WorkspaceDiff{
		RepositoryID:    inspection.RepositoryID,
		BaseRevision:    base,
		CurrentRevision: inspection.CurrentRevision,
		Files:           files,
		Patch:           patch,
	}, nil
}

func (p *Provider) Release(ctx context.Context, handle ports.WorkspaceHandle) error {
	if err := validateHandle(handle); err != nil {
		return err
	}
	metadata, err := p.readMetadata(handle)
	if err != nil {
		return err
	}
	released, err := p.isReleased(handle)
	if err != nil {
		return err
	}
	if released {
		return nil
	}
	inspection, err := p.inspectActive(ctx, handle, metadata)
	if err != nil {
		if errors.Is(err, ErrWorkspaceNotFound) {
			return p.writeReleaseTombstone(handle)
		}
		return err
	}
	if inspection.Dirty {
		return ErrWorkspaceDirty
	}
	repositoryPath, err := p.validateLocalRepository(ctx, metadata.LocalRepository)
	if err != nil {
		return err
	}
	if !samePath(repositoryPath, metadata.LocalRepository) {
		return ErrProvisionConflict
	}
	workspacePath, err := p.workspacePath(handle)
	if err != nil {
		return err
	}
	if _, err := p.runGit(ctx, repositoryPath, "worktree", "remove", workspacePath); err != nil {
		return err
	}
	if _, err := os.Lstat(workspacePath); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return fmt.Errorf("%w: git left workspace path behind", ErrUnsafePath)
		}
		return fmt.Errorf("verify released workspace path: %w", err)
	}
	if err := p.writeReleaseTombstone(handle); err != nil {
		return fmt.Errorf("persist workspace release tombstone: %w", err)
	}
	return nil
}

func (p *Provider) inspectActive(
	ctx context.Context,
	handle ports.WorkspaceHandle,
	metadata workspaceMetadata,
) (ports.WorkspaceInspection, error) {
	workspacePath, err := p.workspacePath(handle)
	if err != nil {
		return ports.WorkspaceInspection{}, err
	}
	if err := p.validateExistingWorkspace(ctx, workspacePath); err != nil {
		return ports.WorkspaceInspection{}, err
	}
	repositoryPath, err := p.validateLocalRepository(ctx, metadata.LocalRepository)
	if err != nil {
		return ports.WorkspaceInspection{}, err
	}
	if !samePath(repositoryPath, metadata.LocalRepository) {
		return ports.WorkspaceInspection{}, ErrProvisionConflict
	}
	sourceCommonDirectory, err := p.gitCommonDirectory(ctx, repositoryPath)
	if err != nil {
		return ports.WorkspaceInspection{}, err
	}
	workspaceCommonDirectory, err := p.gitCommonDirectory(ctx, workspacePath)
	if err != nil {
		return ports.WorkspaceInspection{}, err
	}
	if !samePath(sourceCommonDirectory, workspaceCommonDirectory) {
		return ports.WorkspaceInspection{}, fmt.Errorf("%w: worktree belongs to another Git repository", ErrProvisionConflict)
	}
	head, err := p.resolveCommit(ctx, workspacePath, "HEAD")
	if err != nil {
		return ports.WorkspaceInspection{}, err
	}
	status, err := p.runGit(ctx, workspacePath, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return ports.WorkspaceInspection{}, err
	}
	branch, err := p.symbolicBranch(ctx, workspacePath)
	if err != nil {
		return ports.WorkspaceInspection{}, err
	}
	if branch != metadata.BranchRef {
		return ports.WorkspaceInspection{}, fmt.Errorf("%w: expected branch %q, got %q", ErrProvisionConflict, metadata.BranchRef, branch)
	}

	repositoryID := project.RepositoryID(metadata.RepositoryID)
	generation := metadata.Generation
	return ports.WorkspaceInspection{
		Handle:         handle,
		RepositoryID:   repositoryID,
		FamilyID:       work.TaskFamilyID(metadata.FamilyID),
		WorkspaceSetID: workspace.WorkspaceSetID(metadata.WorkspaceSetID),
		Generation:     generation,
		BranchRef:      branch,
		BaseRevision: workspace.Revision{
			RepositoryID:        repositoryID,
			VCSObjectID:         metadata.BaseRevision,
			WorkspaceGeneration: generation,
		},
		CurrentRevision: workspace.Revision{
			RepositoryID:        repositoryID,
			VCSObjectID:         head,
			WorkspaceGeneration: generation,
		},
		State: ports.WorkspaceReady,
		Dirty: len(status) != 0,
	}, nil
}

func releasedInspection(handle ports.WorkspaceHandle, metadata workspaceMetadata) ports.WorkspaceInspection {
	repositoryID := project.RepositoryID(metadata.RepositoryID)
	return ports.WorkspaceInspection{
		Handle:         handle,
		RepositoryID:   repositoryID,
		FamilyID:       work.TaskFamilyID(metadata.FamilyID),
		WorkspaceSetID: workspace.WorkspaceSetID(metadata.WorkspaceSetID),
		Generation:     metadata.Generation,
		BranchRef:      metadata.BranchRef,
		BaseRevision: workspace.Revision{
			RepositoryID:        repositoryID,
			VCSObjectID:         metadata.BaseRevision,
			WorkspaceGeneration: metadata.Generation,
		},
		State: ports.WorkspaceReleased,
	}
}

func validateProvisionSpec(spec ports.ProvisionSpec) error {
	if spec.RepositoryID == "" || spec.FamilyID == "" || spec.WorkspaceSetID == "" {
		return fmt.Errorf("%w: repository, family and workspace set ids are required", ErrInvalidSpec)
	}
	if strings.TrimSpace(spec.LocalRepository) == "" || strings.TrimSpace(spec.BaseRef) == "" {
		return fmt.Errorf("%w: local repository and base ref are required", ErrInvalidSpec)
	}
	if spec.Generation == 0 {
		return fmt.Errorf("%w: generation must be greater than zero", ErrInvalidSpec)
	}
	if !validRevisionInput(spec.BaseRef) {
		return fmt.Errorf("%w: base ref contains unsupported characters", ErrInvalidSpec)
	}
	return nil
}

func deriveHandle(
	familyID work.TaskFamilyID,
	repositoryID project.RepositoryID,
	generation uint64,
) (ports.WorkspaceHandle, error) {
	digest := identityDigest(
		"agentkit-workspace-v1",
		string(familyID),
		string(repositoryID),
		strconv.FormatUint(generation, 10),
	)
	return ports.NewWorkspaceHandle(handlePrefix + hex.EncodeToString(digest))
}

func deriveBranchRef(familyID work.TaskFamilyID, repositoryID project.RepositoryID, generation uint64) string {
	digest := identityDigest(
		"agentkit-branch-v1",
		string(familyID),
		string(repositoryID),
		strconv.FormatUint(generation, 10),
	)
	// A 128-bit suffix keeps refs below legacy Windows path limits while making
	// family/repository/generation collisions impractical.
	return "agentkit/w-" + hex.EncodeToString(digest[:16])
}

func identityDigest(parts ...string) []byte {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(strconv.Itoa(len(part))))
		_, _ = hash.Write([]byte{':'})
		_, _ = hash.Write([]byte(part))
	}
	return hash.Sum(nil)
}

func validateHandle(handle ports.WorkspaceHandle) error {
	token := handle.String()
	if len(token) != len(handlePrefix)+sha256.Size*2 || !strings.HasPrefix(token, handlePrefix) {
		return ErrInvalidHandle
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(token, handlePrefix)); err != nil {
		return ErrInvalidHandle
	}
	return nil
}

func (p *Provider) workspacePath(handle ports.WorkspaceHandle) (string, error) {
	if err := validateHandle(handle); err != nil {
		return "", err
	}
	if err := p.validateManagedDirectory(p.worktreesRoot); err != nil {
		return "", err
	}
	path := filepath.Join(p.worktreesRoot, handle.String())
	if err := ensureLexicallyWithin(p.worktreesRoot, path); err != nil {
		return "", err
	}
	return path, nil
}

func (p *Provider) validateManagedDirectory(directory string) error {
	rootInfo, err := os.Lstat(p.root)
	if err != nil || rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return ErrUnsafePath
	}
	directoryInfo, err := os.Lstat(directory)
	if err != nil || directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() {
		return ErrUnsafePath
	}
	canonicalRoot, err := canonicalExistingDirectory(p.root)
	if err != nil || !samePath(canonicalRoot, p.root) {
		return ErrUnsafePath
	}
	canonicalDirectory, err := canonicalExistingDirectory(directory)
	if err != nil || !samePath(canonicalDirectory, directory) || !isWithin(canonicalRoot, canonicalDirectory) {
		return ErrUnsafePath
	}
	return nil
}

func (p *Provider) validateLocalRepository(ctx context.Context, rawPath string) (string, error) {
	canonical, err := canonicalExistingDirectory(rawPath)
	if err != nil {
		return "", fmt.Errorf("%w: local repository: %v", ErrInvalidSpec, err)
	}
	if isWithin(p.root, canonical) || isWithin(canonical, p.root) {
		return "", fmt.Errorf("%w: source repository and workspace root must not contain each other", ErrUnsafePath)
	}
	topLevelOutput, err := p.runGit(ctx, canonical, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("%w: local path is not a Git worktree: %v", ErrInvalidSpec, err)
	}
	topLevel, err := canonicalExistingDirectory(strings.TrimSpace(string(topLevelOutput)))
	if err != nil {
		return "", fmt.Errorf("%w: resolve Git top level: %v", ErrInvalidSpec, err)
	}
	if !samePath(canonical, topLevel) {
		return "", fmt.Errorf("%w: local repository must identify its Git top-level directory", ErrInvalidSpec)
	}
	return canonical, nil
}

func (p *Provider) validateExistingWorkspace(ctx context.Context, workspacePath string) error {
	if err := ensureLexicallyWithin(p.worktreesRoot, workspacePath); err != nil {
		return err
	}
	info, err := os.Lstat(workspacePath)
	if errors.Is(err, os.ErrNotExist) {
		return ErrWorkspaceNotFound
	}
	if err != nil {
		return fmt.Errorf("inspect workspace path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return ErrUnsafePath
	}
	canonical, err := canonicalExistingDirectory(workspacePath)
	if err != nil {
		return fmt.Errorf("%w: resolve workspace: %v", ErrUnsafePath, err)
	}
	if !isWithin(p.worktreesRoot, canonical) {
		return ErrUnsafePath
	}
	topLevelOutput, err := p.runGit(ctx, canonical, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	topLevel, err := canonicalExistingDirectory(strings.TrimSpace(string(topLevelOutput)))
	if err != nil || !samePath(canonical, topLevel) {
		return ErrUnsafePath
	}
	return nil
}

func (p *Provider) resolveCommit(ctx context.Context, repositoryPath string, revision string) (string, error) {
	if !validRevisionInput(revision) {
		return "", fmt.Errorf("%w: invalid Git revision", ErrInvalidSpec)
	}
	output, err := p.runGit(ctx, repositoryPath, "rev-parse", "--verify", "--end-of-options", revision+"^{commit}")
	if err != nil {
		return "", err
	}
	objectID := strings.TrimSpace(string(output))
	if !validObjectID(objectID) {
		return "", fmt.Errorf("%w: Git returned invalid object id", ErrGit)
	}
	return objectID, nil
}

func (p *Provider) symbolicBranch(ctx context.Context, repositoryPath string) (string, error) {
	output, exitCode, err := p.runGitWithExitCode(ctx, repositoryPath, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	if exitCode != 0 {
		return "", fmt.Errorf("%w: workspace HEAD is detached", ErrProvisionConflict)
	}
	return strings.TrimSpace(string(output)), nil
}

func (p *Provider) gitCommonDirectory(ctx context.Context, repositoryPath string) (string, error) {
	output, err := p.runGit(ctx, repositoryPath, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	commonDirectory := strings.TrimSpace(string(output))
	if !filepath.IsAbs(commonDirectory) {
		commonDirectory = filepath.Join(repositoryPath, commonDirectory)
	}
	canonical, err := canonicalExistingDirectory(commonDirectory)
	if err != nil {
		return "", fmt.Errorf("%w: resolve Git common directory: %v", ErrGit, err)
	}
	return canonical, nil
}

func (p *Provider) runGit(ctx context.Context, directory string, arguments ...string) ([]byte, error) {
	output, exitCode, err := p.runGitWithExitCode(ctx, directory, arguments...)
	if err != nil {
		return nil, err
	}
	if exitCode != 0 {
		return nil, fmt.Errorf("%w: git %s exited with code %d", ErrGit, arguments[0], exitCode)
	}
	return output, nil
}

func (p *Provider) runGitWithExitCode(
	ctx context.Context,
	directory string,
	arguments ...string,
) ([]byte, int, error) {
	commandArguments := append([]string{"-C", directory}, arguments...)
	command := exec.CommandContext(ctx, p.gitExecutable, commandArguments...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err == nil {
		return stdout.Bytes(), 0, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		message := strings.TrimSpace(stderr.String())
		if len(message) > 4096 {
			message = message[:4096]
		}
		if message != "" {
			return stdout.Bytes(), exitError.ExitCode(), fmt.Errorf("%w: %s", ErrGit, message)
		}
		return stdout.Bytes(), exitError.ExitCode(), nil
	}
	return nil, -1, fmt.Errorf("%w: execute git: %v", ErrGit, err)
}

func canonicalExistingDirectory(rawPath string) (string, error) {
	if strings.TrimSpace(rawPath) == "" || strings.IndexByte(rawPath, 0) >= 0 {
		return "", errors.New("path is empty or contains NUL")
	}
	absolute, err := filepath.Abs(rawPath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("path is not a directory")
	}
	canonical, err := canonicalExistingPath(absolute)
	if err != nil {
		return "", err
	}
	return filepath.Clean(canonical), nil
}

func ensureLexicallyWithin(root string, candidate string) error {
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return ErrUnsafePath
	}
	return nil
}

func isWithin(root string, candidate string) bool {
	return ensureLexicallyWithin(root, candidate) == nil
}

func samePath(left string, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func validRevisionInput(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 1024 || !utf8.ValidString(value) {
		return false
	}
	return !strings.ContainsAny(value, "\x00\r\n")
}

func validObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

func metadataEqual(left workspaceMetadata, right workspaceMetadata) bool {
	return left == right
}
