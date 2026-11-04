package gitworktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// V12-33 (ADR-065): the worktree's aw-private directory (.aw/), ignored by git through the repository's info/exclude.

const (
	awExcludeLine         = "/" + ports.AwPrivateDirectory + "/"
	maxAwDocuments        = 200
	maxAwDocumentBytes    = 256 << 10
	awFeaturesDirectory   = ports.AwPrivateDirectory + "/features"
	repoFeaturesDirectory = "docs/features"
)

// ensureAwPrivateExcluded adds /.aw/ to the info/exclude git uses for this worktree (shared by the repository's worktrees), once.
func (p *Provider) ensureAwPrivateExcluded(ctx context.Context, workspacePath string) error {
	out, err := p.runGit(ctx, workspacePath, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	path := strings.TrimSpace(string(out))
	if !filepath.IsAbs(path) {
		path = filepath.Join(workspacePath, path)
	}
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(existing), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == awExcludeLine {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	prefix := ""
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		prefix = "\n"
	}
	_, err = fmt.Fprintf(f, "%s# aw: per-task documents kept out of git (ADR-065)\n%s\n", prefix, awExcludeLine)
	return err
}

func (p *Provider) awWorkspace(ctx context.Context, handle ports.WorkspaceHandle) (string, error) {
	dir, err := p.WorkingDirectory(ctx, handle)
	if err != nil {
		return "", err
	}
	return dir, p.ensureAwPrivateExcluded(ctx, dir)
}

// awFiles lists the regular files under .aw/features/ (no symbolic link is followed), relative to the worktree with "/".
func awFiles(workspacePath string) ([]string, error) {
	root := filepath.Join(workspacePath, filepath.FromSlash(awFeaturesDirectory))
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if d.Type().IsRegular() {
			rel, err := filepath.Rel(workspacePath, path)
			if err != nil {
				return err
			}
			out = append(out, filepath.ToSlash(rel))
			if len(out) > maxAwDocuments {
				return fmt.Errorf("gitworktree: more than %d files under %s", maxAwDocuments, awFeaturesDirectory)
			}
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// ListAwDocuments implements ports.AwDocumentStore.
func (p *Provider) ListAwDocuments(ctx context.Context, handle ports.WorkspaceHandle) ([]ports.AwDocument, error) {
	dir, err := p.awWorkspace(ctx, handle)
	if err != nil {
		return nil, err
	}
	files, err := awFiles(dir)
	if err != nil {
		return nil, err
	}
	out := make([]ports.AwDocument, 0, len(files))
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return nil, err
		}
		doc := ports.AwDocument{Path: rel, Size: int64(len(data))}
		if len(data) > maxAwDocumentBytes {
			data, doc.Truncated = data[:maxAwDocumentBytes], true
		}
		doc.Content = string(data)
		out = append(out, doc)
	}
	return out, nil
}

// ExportAwDocuments implements ports.AwDocumentStore: .aw/features/<rest> -> docs/features/<rest>, left uncommitted.
func (p *Provider) ExportAwDocuments(ctx context.Context, handle ports.WorkspaceHandle, overwrite bool) (ports.AwDocumentsExport, error) {
	result := ports.AwDocumentsExport{Exported: []string{}, Unchanged: []string{}, Conflicts: []string{}}
	dir, err := p.awWorkspace(ctx, handle)
	if err != nil {
		return result, err
	}
	files, err := awFiles(dir)
	if err != nil {
		return result, err
	}
	type copyJob struct {
		target string
		data   []byte
	}
	var jobs []copyJob
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return result, err
		}
		target := repoFeaturesDirectory + strings.TrimPrefix(rel, awFeaturesDirectory)
		targetPath := filepath.Join(dir, filepath.FromSlash(target))
		if info, err := os.Lstat(targetPath); err == nil {
			if !info.Mode().IsRegular() {
				result.Conflicts = append(result.Conflicts, target)
				continue
			}
			existing, err := os.ReadFile(targetPath)
			if err != nil {
				return result, err
			}
			if bytes.Equal(existing, data) {
				result.Unchanged = append(result.Unchanged, target)
				continue
			}
			result.Conflicts = append(result.Conflicts, target)
			if !overwrite {
				continue
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return result, err
		}
		jobs = append(jobs, copyJob{target: target, data: data})
	}
	if len(result.Conflicts) > 0 && !overwrite {
		return result, nil
	}
	for _, j := range jobs {
		path := filepath.Join(dir, filepath.FromSlash(j.target))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return result, err
		}
		if err := os.WriteFile(path, j.data, 0o644); err != nil {
			return result, err
		}
		result.Exported = append(result.Exported, j.target)
	}
	return result, nil
}

var _ ports.AwDocumentStore = (*Provider)(nil)
