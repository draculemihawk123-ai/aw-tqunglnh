package catalog_test

// V11-12 (ADR-043): correcting, retiring and removing a repository, against a real sqlite store.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

type lifecycleFixture struct {
	t     *testing.T
	ctx   context.Context
	store *sqlite.Store
	uow   ports.UnitOfWork
	ids   idsource.Source
	n     int
}

func newLifecycleFixture(t *testing.T) *lifecycleFixture {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "agentkit-repository-lifecycle.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	f := &lifecycleFixture{t: t, ctx: ctx, store: store, uow: sqlite.NewUnitOfWork(store), ids: idsource.Random{}}
	seedProjectSQLite(t, f.uow, "p1")
	return f
}

func (f *lifecycleFixture) exec(query string) error {
	return f.store.RunSerializedWrite(f.ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(f.ctx, query)
		return err
	})
}

func (f *lifecycleFixture) cmd(typ, key string, version uint64) ports.Command {
	f.n++
	return ports.Command{
		ID: fmt.Sprintf("cmd-%s-%d", typ, f.n), IdempotencyKey: key, Actor: "actor-1", CorrelationID: "corr-1",
		Scope: ports.ProjectScope("p1"), RequestedAt: time.Now().UTC(), Type: typ, RequestHash: "hash-" + typ + "-" + key, ExpectedVersion: version,
	}
}

func (f *lifecycleFixture) register(id, name string) catalog.RegisterRepositoryResult {
	f.t.Helper()
	result, err := catalog.RegisterRepository(f.ctx, f.uow, f.ids, f.cmd("RegisterRepository", "reg-"+id, 0), catalog.RegisterRepositoryRequest{
		RepositoryID: id, ProjectID: "p1", Name: name, RemoteLocator: "D:/wrong/" + id, DefaultRef: "main",
	})
	if err != nil {
		f.t.Fatalf("RegisterRepository(%s): %v", id, err)
	}
	return result
}

// block plays the probe: REGISTERING -> PROBING -> BLOCKED with a failed attempt that carries the kind and hint.
func (f *lifecycleFixture) block(id string) project.Repository {
	f.t.Helper()
	return f.finishProbe(id, project.RepositoryBlocked)
}

func (f *lifecycleFixture) activate(id string) project.Repository {
	f.t.Helper()
	return f.finishProbe(id, project.RepositoryActive)
}

func (f *lifecycleFixture) finishProbe(id string, outcome project.RepositoryStatus) project.Repository {
	f.t.Helper()
	var repo project.Repository
	jobID := f.unusedProbeJob(id) // read before the write transaction opens: a read inside it would wait for itself
	err := f.uow.WithSerializedWrite(f.ctx, func(tx ports.Tx) error {
		current, err := tx.Catalog().GetRepository(f.ctx, id)
		if err != nil {
			return err
		}
		if current.Status == project.RepositoryRegistering {
			if current, err = tx.Catalog().TransitionRepositoryStatus(f.ctx, ports.TransitionRepositoryStatusRequest{
				RepositoryID: id, ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: current.Version, NextStatus: project.RepositoryProbing,
			}); err != nil {
				return err
			}
		}
		var code *string
		attempt := ports.RecordRepositoryProbeAttemptRequest{
			ID: f.ids.NewID(), ProjectID: "p1", RepositoryID: id, JobID: jobID, State: ports.RepositoryProbeAttemptSucceeded, Result: &outcome,
		}
		if outcome == project.RepositoryBlocked {
			c, msg, kind, hint := "INVALID_ARGUMENT", "The folder D:/wrong is not a Git repository yet.", "NOT_A_GIT_REPOSITORY", "Run git init."
			code = &c
			attempt.ErrorCode, attempt.ErrorMessage, attempt.FailureKind, attempt.ErrorHint = &c, &msg, &kind, &hint
		} else {
			base := "abc"
			attempt.BaseCommit = &base
		}
		repo, err = tx.Catalog().TransitionRepositoryStatus(f.ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: id, ExpectedStatus: project.RepositoryProbing, ExpectedVersion: current.Version, NextStatus: outcome, LastProbeErrorCode: code,
		})
		if err != nil {
			return err
		}
		_, err = tx.Catalog().RecordRepositoryProbeAttempt(f.ctx, attempt)
		return err
	})
	if err != nil {
		f.t.Fatalf("finish probe of %s: %v", id, err)
	}
	return repo
}

// unusedProbeJob returns a probe job of the repository that has no attempt row yet (attempt.job_id is a foreign key to durable_jobs).
func (f *lifecycleFixture) unusedProbeJob(repositoryID string) string {
	f.t.Helper()
	jobs, err := f.store.DebugListJobsByKind(f.ctx, catalog.RepositoryProbeJobKind)
	if err != nil {
		f.t.Fatalf("DebugListJobsByKind: %v", err)
	}
	used := map[string]bool{}
	attempts, _ := catalog.ListRepositoryProbeAttempts(f.ctx, f.uow, repositoryID)
	for _, a := range attempts {
		used[a.JobID] = true
	}
	for _, job := range jobs {
		if job.AggregateID == repositoryID && !used[job.ID] {
			return job.ID
		}
	}
	f.t.Fatalf("no unused probe job for %s", repositoryID)
	return ""
}

func (f *lifecycleFixture) get(id string) project.Repository {
	f.t.Helper()
	repo, err := catalog.GetRepository(f.ctx, f.uow, id)
	if err != nil {
		f.t.Fatalf("GetRepository(%s): %v", id, err)
	}
	return repo
}

func TestProbeAttempt_KeepsKindDetailAndHint(t *testing.T) {
	f := newLifecycleFixture(t)
	f.register("r1", "svc")
	f.block("r1")
	attempts, err := catalog.ListRepositoryProbeAttempts(f.ctx, f.uow, "r1")
	if err != nil || len(attempts) != 1 {
		t.Fatalf("attempts = %v, %v", attempts, err)
	}
	a := attempts[0]
	if a.FailureKind == nil || *a.FailureKind != "NOT_A_GIT_REPOSITORY" || a.ErrorHint == nil || *a.ErrorHint != "Run git init." {
		t.Fatalf("attempt = %+v; want the kind and hint kept", a)
	}
}

func TestUpdateRepository_CorrectsABlockedRegistrationAndProbesAgain(t *testing.T) {
	f := newLifecycleFixture(t)
	f.register("r1", "svc")
	blocked := f.block("r1")

	result, err := catalog.UpdateRepository(f.ctx, f.uow, f.ids, f.cmd("UpdateRepository", "upd-1", blocked.Version), catalog.UpdateRepositoryRequest{
		RepositoryID: "r1", ProjectID: "p1", RemoteLocator: "D:/right/svc", DefaultRef: "develop",
	})
	if err != nil {
		t.Fatalf("UpdateRepository: %v", err)
	}
	repo := f.get("r1")
	if repo.Status != project.RepositoryProbing || repo.RemoteLocator != "D:/right/svc" || repo.DefaultRef != "develop" || repo.Name != "svc" ||
		repo.LastProbeErrorCode != nil || repo.Version != blocked.Version+1 || result.Version != repo.Version {
		t.Fatalf("repository after update = %+v, result %+v", repo, result)
	}
	count, err := f.store.CountDurableJobsByIdempotencyKey(f.ctx, "upd-1-probe")
	if err != nil || count != 1 || result.ProbeJobID == "" {
		t.Fatalf("probe jobs = %d, %v (job %q); want exactly one", count, err, result.ProbeJobID)
	}

	// replay: same key and hash, same answer, nothing more written
	again, err := catalog.UpdateRepository(f.ctx, f.uow, f.ids, f.cmd("UpdateRepository", "upd-1", blocked.Version), catalog.UpdateRepositoryRequest{
		RepositoryID: "r1", ProjectID: "p1", RemoteLocator: "D:/right/svc", DefaultRef: "develop",
	})
	if err != nil || again != result {
		t.Fatalf("replay = %+v, %v; want %+v", again, err, result)
	}
	if count, _ := f.store.CountDurableJobsByIdempotencyKey(f.ctx, "upd-1-probe"); count != 1 {
		t.Fatalf("replay enqueued another probe: %d", count)
	}
}

func TestUpdateRepository_RefusesWhatIsNotBlockedAndStaleVersions(t *testing.T) {
	f := newLifecycleFixture(t)
	f.register("r1", "svc")
	active := f.activate("r1")
	_, err := catalog.UpdateRepository(f.ctx, f.uow, f.ids, f.cmd("UpdateRepository", "upd-active", active.Version), catalog.UpdateRepositoryRequest{
		RepositoryID: "r1", ProjectID: "p1", RemoteLocator: "D:/other", DefaultRef: "main",
	})
	if catalog.RepositoryRefusalCode(err) != catalog.RefusalNotBlocked {
		t.Fatalf("update of an ACTIVE repository = %v; want %s", err, catalog.RefusalNotBlocked)
	}

	f.register("r2", "svc2")
	blocked := f.block("r2")
	_, err = catalog.UpdateRepository(f.ctx, f.uow, f.ids, f.cmd("UpdateRepository", "upd-stale", blocked.Version-1), catalog.UpdateRepositoryRequest{
		RepositoryID: "r2", ProjectID: "p1", RemoteLocator: "D:/other", DefaultRef: "main",
	})
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("stale version = %v; want ErrOptimisticConflict", err)
	}
	if got := f.get("r2"); got.RemoteLocator != "D:/wrong/r2" || got.Status != project.RepositoryBlocked {
		t.Fatalf("a refused update changed the repository: %+v", got)
	}
}

func TestUpdateRepository_NameTakenByAnotherLiveRepositoryIsAConflict(t *testing.T) {
	f := newLifecycleFixture(t)
	f.register("r1", "svc")
	f.register("r2", "other")
	blocked := f.block("r2")
	_, err := catalog.UpdateRepository(f.ctx, f.uow, f.ids, f.cmd("UpdateRepository", "upd-name", blocked.Version), catalog.UpdateRepositoryRequest{
		RepositoryID: "r2", ProjectID: "p1", Name: "svc", RemoteLocator: "D:/x", DefaultRef: "main",
	})
	if !errors.Is(err, ports.ErrPersistenceAlreadyExists) {
		t.Fatalf("rename onto a live name = %v; want ErrPersistenceAlreadyExists", err)
	}
}

func TestDisableRepository_BlockedGoesAndFreesItsName(t *testing.T) {
	f := newLifecycleFixture(t)
	f.register("r1", "svc")
	blocked := f.block("r1")
	result, err := catalog.DisableRepository(f.ctx, f.uow, f.cmd("DisableRepository", "dis-1", blocked.Version), catalog.DisableRepositoryRequest{
		RepositoryID: "r1", ProjectID: "p1", Reason: "registered the wrong folder",
	})
	if err != nil || result.Status != "DISABLED" || result.PreviousStatus != "BLOCKED" {
		t.Fatalf("DisableRepository = %+v, %v", result, err)
	}
	// the same name can be registered again; the disabled one keeps its id and history
	f.register("r1-again", "svc")
	if got := f.get("r1"); got.Status != project.RepositoryDisabled {
		t.Fatalf("r1 = %+v", got)
	}
	attempts, _ := catalog.ListRepositoryProbeAttempts(f.ctx, f.uow, "r1")
	if len(attempts) != 1 {
		t.Fatalf("a disabled repository keeps its history, got %d attempts", len(attempts))
	}
	// but two live repositories still cannot share a name
	_, err = catalog.RegisterRepository(f.ctx, f.uow, f.ids, f.cmd("RegisterRepository", "reg-dup", 0), catalog.RegisterRepositoryRequest{
		RepositoryID: "r1-third", ProjectID: "p1", Name: "svc", RemoteLocator: "D:/x", DefaultRef: "main",
	})
	if !errors.Is(err, ports.ErrPersistenceAlreadyExists) {
		t.Fatalf("a second live repository named svc = %v; want ErrPersistenceAlreadyExists", err)
	}
	// disabling again is a typed refusal
	_, err = catalog.DisableRepository(f.ctx, f.uow, f.cmd("DisableRepository", "dis-2", result.Version), catalog.DisableRepositoryRequest{RepositoryID: "r1", ProjectID: "p1"})
	if catalog.RepositoryRefusalCode(err) != catalog.RefusalAlreadyDisabled {
		t.Fatalf("second disable = %v; want %s", err, catalog.RefusalAlreadyDisabled)
	}
}

func TestDisableRepository_ActiveGoesAndInUseIsRefusedWithTheHolders(t *testing.T) {
	f := newLifecycleFixture(t)
	f.register("r1", "svc")
	active := f.activate("r1")
	result, err := catalog.DisableRepository(f.ctx, f.uow, f.cmd("DisableRepository", "dis-active", active.Version), catalog.DisableRepositoryRequest{RepositoryID: "r1", ProjectID: "p1"})
	if err != nil || result.PreviousStatus != "ACTIVE" {
		t.Fatalf("ACTIVE -> DISABLED = %+v, %v", result, err)
	}

	// a repository an unreleased workspace still uses cannot be disabled
	g := newLifecycleFixture(t)
	if err := sqlite.SeedFixtureOwners(g.ctx, g.store, "p2", "f2", "wi-2"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := sqlite.SeedFixtureRepositoryWorkspace(g.ctx, g.store, "p2", "f2", "ws-set-2", "repo-used", "rw-2"); err != nil {
		t.Fatalf("SeedFixtureRepositoryWorkspace: %v", err)
	}
	cmd := g.cmd("DisableRepository", "dis-used", 1)
	cmd.Scope = ports.ProjectScope("p2")
	_, err = catalog.DisableRepository(g.ctx, g.uow, cmd, catalog.DisableRepositoryRequest{RepositoryID: "repo-used", ProjectID: "p2"})
	if catalog.RepositoryRefusalCode(err) != catalog.RefusalInUse {
		t.Fatalf("disable of a repository with a live workspace = %v; want %s", err, catalog.RefusalInUse)
	}
	items := catalog.RepositoryRefusalItems(err)
	if len(items) == 0 {
		t.Fatalf("the refusal must name what holds the repository, got %v", err)
	}
}

func TestDeleteRepository_RemovesANeverActiveRepositoryAndRetiresItsId(t *testing.T) {
	f := newLifecycleFixture(t)
	reg := f.register("r1", "svc")
	blocked := f.block("r1")

	result, err := catalog.DeleteRepository(f.ctx, f.uow, f.cmd("DeleteRepository", "del-1", blocked.Version), catalog.DeleteRepositoryRequest{
		RepositoryID: "r1", ProjectID: "p1", Reason: "wrong folder",
	})
	if err != nil || result.Name != "svc" {
		t.Fatalf("DeleteRepository = %+v, %v", result, err)
	}
	if _, err := catalog.GetRepository(f.ctx, f.uow, "r1"); !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("GetRepository after delete = %v; want not found", err)
	}
	if attempts, _ := catalog.ListRepositoryProbeAttempts(f.ctx, f.uow, "r1"); len(attempts) != 0 {
		t.Fatalf("probe history survived the delete: %d", len(attempts))
	}

	// the name is free again; the id is not
	f.register("r1-new", "svc")
	_, err = catalog.RegisterRepository(f.ctx, f.uow, f.ids, f.cmd("RegisterRepository", "reg-reuse", 0), catalog.RegisterRepositoryRequest{
		RepositoryID: "r1", ProjectID: "p1", Name: "fresh", RemoteLocator: "D:/x", DefaultRef: "main",
	})
	if !errors.Is(err, ports.ErrRepositoryDeleted) {
		t.Fatalf("registering a deleted id = %v; want ErrRepositoryDeleted", err)
	}

	// replaying the original registration must not claim the deleted repository exists
	_, err = catalog.RegisterRepository(f.ctx, f.uow, f.ids, f.cmd("RegisterRepository", "reg-r1", 0), catalog.RegisterRepositoryRequest{
		RepositoryID: "r1", ProjectID: "p1", Name: "svc", RemoteLocator: "D:/wrong/r1", DefaultRef: "main",
	})
	if catalog.RepositoryRefusalCode(err) != catalog.RefusalDeleted {
		t.Fatalf("replayed registration after delete = %v (receipt result %+v); want %s", err, reg, catalog.RefusalDeleted)
	}

	// replaying the delete itself answers with the original result
	again, err := catalog.DeleteRepository(f.ctx, f.uow, f.cmd("DeleteRepository", "del-1", blocked.Version), catalog.DeleteRepositoryRequest{
		RepositoryID: "r1", ProjectID: "p1", Reason: "wrong folder",
	})
	if err != nil || again != result {
		t.Fatalf("replayed delete = %+v, %v", again, err)
	}
}

func TestDeleteRepository_CancelsItsWaitingProbeJobAndLeavesTheEventBehind(t *testing.T) {
	f := newLifecycleFixture(t)
	f.register("r1", "svc") // REGISTERING, probe job AVAILABLE
	repo := f.get("r1")
	if _, err := catalog.DeleteRepository(f.ctx, f.uow, f.cmd("DeleteRepository", "del-reg", repo.Version), catalog.DeleteRepositoryRequest{
		RepositoryID: "r1", ProjectID: "p1", Reason: "typo",
	}); err != nil {
		t.Fatalf("DeleteRepository of a REGISTERING repository: %v", err)
	}
	jobs, err := f.store.DebugListJobsByKind(f.ctx, catalog.RepositoryProbeJobKind)
	if err != nil || len(jobs) != 1 || jobs[0].State != "CANCELLED" {
		t.Fatalf("probe jobs = %+v, %v; want the one job CANCELLED", jobs, err)
	}
	events, err := f.store.ListDomainEventsForProject(f.ctx, "p1")
	if err != nil {
		t.Fatalf("ListDomainEventsForProject: %v", err)
	}
	found := false
	for _, event := range events {
		if event.EventType == catalog.RepositoryDeletedEventType && event.SchemaVersion == catalog.RepositoryDeletedSchemaVersion {
			found = true
		}
	}
	if !found {
		t.Fatalf("no %s event among %d events", catalog.RepositoryDeletedEventType, len(events))
	}
}

func TestDeleteRepository_RefusesWhatWasEverActiveOrIsReferenced(t *testing.T) {
	f := newLifecycleFixture(t)
	f.register("r1", "svc")
	active := f.activate("r1")
	_, err := catalog.DeleteRepository(f.ctx, f.uow, f.cmd("DeleteRepository", "del-active", active.Version), catalog.DeleteRepositoryRequest{RepositoryID: "r1", ProjectID: "p1", Reason: "x"})
	if catalog.RepositoryRefusalCode(err) != catalog.RefusalWasActive {
		t.Fatalf("delete of an ACTIVE repository = %v; want %s", err, catalog.RefusalWasActive)
	}
	// ACTIVE once, then disabled: still not deletable
	disabled, err := catalog.DisableRepository(f.ctx, f.uow, f.cmd("DisableRepository", "dis-x", active.Version), catalog.DisableRepositoryRequest{RepositoryID: "r1", ProjectID: "p1"})
	if err != nil {
		t.Fatalf("DisableRepository: %v", err)
	}
	_, err = catalog.DeleteRepository(f.ctx, f.uow, f.cmd("DeleteRepository", "del-disabled", disabled.Version), catalog.DeleteRepositoryRequest{RepositoryID: "r1", ProjectID: "p1", Reason: "x"})
	if catalog.RepositoryRefusalCode(err) != catalog.RefusalWasActive {
		t.Fatalf("delete of a DISABLED repository = %v; want %s", err, catalog.RefusalWasActive)
	}

	// a BLOCKED repository that something references: refused, and it lists what
	if err := sqlite.SeedFixtureOwners(f.ctx, f.store, "p2", "f2", "wi-2"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := sqlite.SeedFixtureRepositoryWorkspace(f.ctx, f.store, "p2", "f2", "ws-set-2", "repo-ref", "rw-2"); err != nil {
		t.Fatalf("SeedFixtureRepositoryWorkspace: %v", err)
	}
	if err := f.exec(`UPDATE repositories SET status = 'BLOCKED' WHERE id = 'repo-ref'`); err != nil {
		t.Fatalf("make blocked: %v", err)
	}
	cmd := f.cmd("DeleteRepository", "del-ref", 1)
	cmd.Scope = ports.ProjectScope("p2")
	_, err = catalog.DeleteRepository(f.ctx, f.uow, cmd, catalog.DeleteRepositoryRequest{RepositoryID: "repo-ref", ProjectID: "p2", Reason: "x"})
	if catalog.RepositoryRefusalCode(err) != catalog.RefusalReferenced {
		t.Fatalf("delete of a referenced repository = %v; want %s", err, catalog.RefusalReferenced)
	}
	if items := catalog.RepositoryRefusalItems(err); len(items) == 0 || items[0] != "repository_workspaces: 1 row(s)" {
		t.Fatalf("items = %v; want the referencing table listed", items)
	}
	if _, err := catalog.GetRepository(f.ctx, f.uow, "repo-ref"); err != nil {
		t.Fatalf("a refused delete removed the repository: %v", err)
	}
}

func TestDeleteRepository_RequiresAReason(t *testing.T) {
	f := newLifecycleFixture(t)
	f.register("r1", "svc")
	repo := f.get("r1")
	if _, err := catalog.DeleteRepository(f.ctx, f.uow, f.cmd("DeleteRepository", "del-noreason", repo.Version), catalog.DeleteRepositoryRequest{RepositoryID: "r1", ProjectID: "p1"}); err == nil {
		t.Fatal("DeleteRepository without a reason succeeded, want an error")
	}
}
