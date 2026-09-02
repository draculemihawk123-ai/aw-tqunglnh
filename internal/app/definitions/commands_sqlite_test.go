package definitions_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// TestPublishDefinitionVersion_ConcurrentPublish_NoDuplicateVersionOrHash
// proves, through the Command-envelope application layer, the same
// concurrency property internal/adapters/sqlite's own
// TestPublishSharedDefinitionVersion_ConcurrentPublish_NoDuplicateVersionOrHash
// already proves at the bare repository layer: N goroutines racing
// PublishDefinitionVersion with distinct content each get a distinct
// version number/hash, no version row is lost or duplicated, and no
// domain event collides. This needs real sqlite (not the in-memory fake,
// whose WithSerializedWrite unlocks before running fn and so cannot
// exercise genuine concurrent-writer contention — it would either race on
// the shared map or reject a genuine concurrent caller with
// fake.ErrNestedTransaction) to be a meaningful proof of the property.
func TestPublishDefinitionVersion_ConcurrentPublish_NoDuplicateVersionOrHash(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "agentkit-definitions-command-concurrent.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	uow := sqlite.NewUnitOfWork(store)

	createCmd := ports.Command{
		ID: "cmd-create", IdempotencyKey: "idem-create", Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.InstallationScope(),
		RequestedAt: time.Now().UTC(), Type: "CreateDefinition", RequestHash: "hash-create",
	}
	if _, err := definitions.CreateDefinition(ctx, uow, createCmd, definitions.CreateDefinitionRequest{
		DefinitionID: "def-race", Kind: definition.KindBlock, Scope: definition.GlobalScope(), Name: "race target",
	}); err != nil {
		t.Fatalf("CreateDefinition: %v", err)
	}

	const writers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]definition.VersionFields, writers)
	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			// Each writer uses its own IdempotencyKey/VersionID/content —
			// a real caller would generate fresh ones per candidate, same
			// as internal/adapters/sqlite's own concurrent test does at
			// the repository layer. Distinct content is what actually
			// exercises "no duplicate version number/hash" — this is not
			// N retries of the same command, but N genuinely different
			// commands racing the same Definition.
			cmd := ports.Command{
				ID: fmt.Sprintf("cmd-publish-%d", i), IdempotencyKey: fmt.Sprintf("idem-publish-%d", i),
				Actor: "actor-1", CorrelationID: "corr-1", Scope: ports.InstallationScope(),
				RequestedAt: time.Now().UTC(), Type: "PublishDefinitionVersion",
				RequestHash: fmt.Sprintf("hash-publish-%d", i),
			}
			versionID := fmt.Sprintf("ver-race-%d", i)
			content := fmt.Sprintf("distinct-content-%d", i)
			results[i], errs[i] = definitions.PublishDefinitionVersion(ctx, uow, cmd, definitions.PublishDefinitionVersionRequest{
				DefinitionID: "def-race", Kind: definition.KindBlock,
				Compile: func() (definition.VersionFields, error) {
					return definition.NewVersionFields(definition.NewVersionFieldsRequest{
						ID: versionID, DefinitionID: "def-race", Kind: definition.KindBlock,
						VersionNumber: 1, SchemaVersion: 1,
						CanonicalSource: content, SourceHash: "sha256:source-" + versionID,
						CompiledSnapshot: content, CompiledHash: "sha256:compiled-" + content,
						PublishedBy: "operator-1", PublishedAt: time.Now().UTC(),
					})
				},
			})
		}()
	}
	close(start)
	wg.Wait()

	seenVersionNumbers := map[uint64]bool{}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
		if seenVersionNumbers[results[i].VersionNumber()] {
			t.Fatalf("writer %d got duplicate version number %d", i, results[i].VersionNumber())
		}
		seenVersionNumbers[results[i].VersionNumber()] = true
	}
	if len(seenVersionNumbers) != writers {
		t.Fatalf("got %d distinct version numbers, want %d", len(seenVersionNumbers), writers)
	}

	versions, err := definitions.ListVersions(ctx, uow, definition.KindBlock, "def-race")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != writers {
		t.Fatalf("version rows = %d, want %d (one per distinct content, no duplicates/loss)", len(versions), writers)
	}
}

// TestPublishDefinitionVersion_ConcurrentPublish_SameContent_DedupesToOneVersion
// is the same-content counterpart: N goroutines racing to publish
// byte-identical compiled content under distinct commands must all agree
// on exactly one version row (AK-ARCH-005B dedup by DefinitionID +
// CompiledSnapshotHash), never one row per writer.
func TestPublishDefinitionVersion_ConcurrentPublish_SameContent_DedupesToOneVersion(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "agentkit-definitions-command-concurrent-dedup.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	uow := sqlite.NewUnitOfWork(store)

	createCmd := ports.Command{
		ID: "cmd-create", IdempotencyKey: "idem-create", Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.InstallationScope(),
		RequestedAt: time.Now().UTC(), Type: "CreateDefinition", RequestHash: "hash-create",
	}
	if _, err := definitions.CreateDefinition(ctx, uow, createCmd, definitions.CreateDefinitionRequest{
		DefinitionID: "def-dedup-race", Kind: definition.KindBlock, Scope: definition.GlobalScope(), Name: "dedup race target",
	}); err != nil {
		t.Fatalf("CreateDefinition: %v", err)
	}

	const writers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]definition.VersionFields, writers)
	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			cmd := ports.Command{
				ID: fmt.Sprintf("cmd-publish-%d", i), IdempotencyKey: fmt.Sprintf("idem-publish-%d", i),
				Actor: "actor-1", CorrelationID: "corr-1", Scope: ports.InstallationScope(),
				RequestedAt: time.Now().UTC(), Type: "PublishDefinitionVersion",
				RequestHash: fmt.Sprintf("hash-publish-%d", i),
			}
			versionID := fmt.Sprintf("ver-dedup-race-%d", i) // distinct candidate identity
			const content = "identical-content"              // identical compiled content
			results[i], errs[i] = definitions.PublishDefinitionVersion(ctx, uow, cmd, definitions.PublishDefinitionVersionRequest{
				DefinitionID: "def-dedup-race", Kind: definition.KindBlock,
				Compile: func() (definition.VersionFields, error) {
					return definition.NewVersionFields(definition.NewVersionFieldsRequest{
						ID: versionID, DefinitionID: "def-dedup-race", Kind: definition.KindBlock,
						VersionNumber: 1, SchemaVersion: 1,
						CanonicalSource: content, SourceHash: "sha256:source-" + versionID,
						CompiledSnapshot: content, CompiledHash: "sha256:compiled-identical",
						PublishedBy: "operator-1", PublishedAt: time.Now().UTC(),
					})
				},
			})
		}()
	}
	close(start)
	wg.Wait()

	firstID := ""
	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
		if firstID == "" {
			firstID = results[i].ID()
		} else if results[i].ID() != firstID {
			t.Fatalf("writer %d resolved to version id %q, want every writer to agree on the same id %q", i, results[i].ID(), firstID)
		}
	}

	versions, err := definitions.ListVersions(ctx, uow, definition.KindBlock, "def-dedup-race")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("version rows = %d, want 1 (identical content across every writer must never duplicate)", len(versions))
	}
}
