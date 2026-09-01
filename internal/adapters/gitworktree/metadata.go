package gitworktree

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
)

const metadataSchemaVersion = 1

type workspaceMetadata struct {
	SchemaVersion   int    `json:"schemaVersion"`
	Handle          string `json:"handle"`
	RepositoryID    string `json:"repositoryId"`
	LocalRepository string `json:"localRepository"`
	BaseRevision    string `json:"baseRevision"`
	FamilyID        string `json:"familyId"`
	WorkspaceSetID  string `json:"workspaceSetId"`
	Generation      uint64 `json:"generation"`
	BranchRef       string `json:"branchRef"`
}

type releaseTombstone struct {
	SchemaVersion int    `json:"schemaVersion"`
	Handle        string `json:"handle"`
}

func (p *Provider) metadataPath(handle ports.WorkspaceHandle) (string, error) {
	if err := validateHandle(handle); err != nil {
		return "", err
	}
	if err := p.validateManagedDirectory(p.metadataRoot); err != nil {
		return "", err
	}
	path := filepath.Join(p.metadataRoot, handle.String()+".json")
	if err := ensureLexicallyWithin(p.metadataRoot, path); err != nil {
		return "", err
	}
	return path, nil
}

func (p *Provider) tombstonePath(handle ports.WorkspaceHandle) (string, error) {
	if err := validateHandle(handle); err != nil {
		return "", err
	}
	if err := p.validateManagedDirectory(p.metadataRoot); err != nil {
		return "", err
	}
	path := filepath.Join(p.metadataRoot, handle.String()+".released.json")
	if err := ensureLexicallyWithin(p.metadataRoot, path); err != nil {
		return "", err
	}
	return path, nil
}

func (p *Provider) readMetadata(handle ports.WorkspaceHandle) (workspaceMetadata, error) {
	path, err := p.metadataPath(handle)
	if err != nil {
		return workspaceMetadata{}, err
	}
	file, err := openRegularFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return workspaceMetadata{}, ErrWorkspaceNotFound
	}
	if err != nil {
		return workspaceMetadata{}, fmt.Errorf("open workspace metadata: %w", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(io.LimitReader(file, 64*1024))
	decoder.DisallowUnknownFields()
	var metadata workspaceMetadata
	if err := decoder.Decode(&metadata); err != nil {
		return workspaceMetadata{}, fmt.Errorf("decode workspace metadata: %w", err)
	}
	if metadata.SchemaVersion != metadataSchemaVersion || metadata.Handle != handle.String() {
		return workspaceMetadata{}, fmt.Errorf("%w: metadata identity does not match handle", ErrInvalidHandle)
	}
	if metadata.RepositoryID == "" || metadata.LocalRepository == "" || metadata.BaseRevision == "" ||
		metadata.FamilyID == "" || metadata.WorkspaceSetID == "" || metadata.Generation == 0 || metadata.BranchRef == "" {
		return workspaceMetadata{}, fmt.Errorf("%w: incomplete workspace metadata", ErrInvalidHandle)
	}
	if !validObjectID(metadata.BaseRevision) || metadata.BranchRef != deriveBranchRef(
		work.TaskFamilyID(metadata.FamilyID),
		project.RepositoryID(metadata.RepositoryID),
		metadata.Generation,
	) {
		return workspaceMetadata{}, fmt.Errorf("%w: invalid workspace metadata", ErrInvalidHandle)
	}
	return metadata, nil
}

func (p *Provider) writeMetadata(metadata workspaceMetadata) error {
	handle, err := ports.NewWorkspaceHandle(metadata.Handle)
	if err != nil {
		return err
	}
	path, err := p.metadataPath(handle)
	if err != nil {
		return err
	}
	return writeExclusiveJSON(path, metadata)
}

func (p *Provider) isReleased(handle ports.WorkspaceHandle) (bool, error) {
	path, err := p.tombstonePath(handle)
	if err != nil {
		return false, err
	}
	file, err := openRegularFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open workspace release tombstone: %w", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(io.LimitReader(file, 16*1024))
	decoder.DisallowUnknownFields()
	var tombstone releaseTombstone
	if err := decoder.Decode(&tombstone); err != nil {
		return false, fmt.Errorf("decode workspace release tombstone: %w", err)
	}
	if tombstone.SchemaVersion != metadataSchemaVersion || tombstone.Handle != handle.String() {
		return false, fmt.Errorf("%w: release tombstone identity does not match handle", ErrInvalidHandle)
	}
	return true, nil
}

func (p *Provider) writeReleaseTombstone(handle ports.WorkspaceHandle) error {
	path, err := p.tombstonePath(handle)
	if err != nil {
		return err
	}
	err = writeExclusiveJSON(path, releaseTombstone{
		SchemaVersion: metadataSchemaVersion,
		Handle:        handle.String(),
	})
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	return err
}

func writeExclusiveJSON(path string, value any) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	removeIncomplete := true
	defer func() {
		_ = file.Close()
		if removeIncomplete {
			_ = os.Remove(path)
		}
	}()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	removeIncomplete = false
	return nil
}

func openRegularFile(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, ErrUnsafePath
	}
	return os.Open(path)
}
