package secretstore_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/secretkey"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite/sqlitetest"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/secretstore"
)

type envResolver map[string]string

func (e envResolver) Resolve(_ context.Context, name string) (string, error) {
	if v, ok := e[name]; ok {
		return v, nil
	}
	return "", errors.New("not set")
}

func command(key, typ string) ports.Command {
	return ports.Command{ID: "cmd-" + key, IdempotencyKey: key, Actor: "operator-1", CorrelationID: "c", Scope: ports.ProjectScope("p1"),
		RequestedAt: time.Now().UTC(), Type: typ, RequestHash: "h-" + key}
}

func fixture(t *testing.T) (ports.UnitOfWork, secretstore.Deps) {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, sqlitetest.MigratedPath(t, "secrets.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(ctx, ports.CreateProjectRequest{ID: "p1", Name: "p1"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, repo := range []string{"api", "web"} {
		cmd := command("reg-"+repo, "RegisterRepository")
		if _, err := catalog.RegisterRepository(ctx, uow, ids, cmd, catalog.RegisterRepositoryRequest{
			RepositoryID: repo, ProjectID: "p1", Name: repo, RemoteLocator: "/fixture/" + repo, DefaultRef: "main",
		}); err != nil {
			t.Fatalf("RegisterRepository: %v", err)
		}
	}
	return uow, secretstore.Deps{UnitOfWork: uow, Keys: secretkey.NewFileProvider(t.TempDir())}
}

func TestSecretStore_StoresEncryptedAndResolvesPerRepository(t *testing.T) {
	uow, deps := fixture(t)
	ctx := context.Background()
	if _, err := secretstore.Set(ctx, deps, command("s1", "SetRepositorySecrets"), "p1", "api", map[string]string{"CHOCHUYENNHA_DB_PASSWORD": "hunter2"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// The database holds no clear value.
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		rec, err := tx.Secrets().GetRepositorySecret(ctx, "api", "CHOCHUYENNHA_DB_PASSWORD")
		if err == nil && bytes.Contains(rec.Ciphertext, []byte("hunter2")) {
			t.Fatal("the stored value is not encrypted")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	list, err := secretstore.List(ctx, deps, "p1", "api")
	if err != nil || len(list.Secrets) != 1 || list.Secrets[0].Name != "CHOCHUYENNHA_DB_PASSWORD" || list.Protection != "file" {
		t.Fatalf("List = %+v, %v", list, err)
	}

	resolver := secretstore.Resolver{Deps: deps, Fallback: envResolver{"OTHER": "from-env", "CHOCHUYENNHA_DB_PASSWORD": "env-value"}}
	if v, err := resolver.ResolveScoped(ctx, []string{"api"}, "CHOCHUYENNHA_DB_PASSWORD"); err != nil || v != "hunter2" {
		t.Fatalf("ResolveScoped(api) = %q, %v; want the store's value", v, err)
	}
	if v, err := resolver.ResolveScoped(ctx, []string{"web"}, "CHOCHUYENNHA_DB_PASSWORD"); err != nil || v != "env-value" {
		t.Fatalf("ResolveScoped(web) = %q, %v; want the environment (another repository's secret is not shared)", v, err)
	}
	if v, err := resolver.ResolveScoped(ctx, []string{"api"}, "OTHER"); err != nil || v != "from-env" {
		t.Fatalf("fallback = %q, %v", v, err)
	}

	// A row moved to another name does not decrypt (the name is part of the additional data).
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		rec, err := tx.Secrets().GetRepositorySecret(ctx, "api", "CHOCHUYENNHA_DB_PASSWORD")
		if err != nil {
			return err
		}
		rec.Name = "STOLEN"
		_, err = tx.Secrets().PutRepositorySecret(ctx, rec, 0)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveScoped(ctx, []string{"api"}, "STOLEN"); err == nil {
		t.Fatal("a ciphertext under another name must not decrypt")
	}

	if _, err := secretstore.Set(ctx, deps, command("s2", "SetRepositorySecrets"), "p1", "api", map[string]string{"1BAD": "x"}); !errors.Is(err, secretstore.ErrInvalidName) {
		t.Fatalf("bad name: %v", err)
	}
	if _, err := secretstore.Delete(ctx, deps, command("d1", "DeleteRepositorySecret"), "p1", "api", "CHOCHUYENNHA_DB_PASSWORD"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := secretstore.List(ctx, deps, "p2", "api"); err == nil {
		t.Fatal("another project's repository must be hidden")
	}
}

func TestParseDotEnv(t *testing.T) {
	got, err := secretstore.ParseDotEnv("# comment\nexport A=1\nB = \"two words\\nline\"\nC='x # not a comment'\nD=plain # comment\nEMPTY=\n")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"A": "1", "B": "two words\nline", "C": "x # not a comment", "D": "plain"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s = %q, want %q", k, got[k], v)
		}
	}
	if _, err := secretstore.ParseDotEnv("GOOD=1\nnot a line with secret-value\n"); err == nil || strings.Contains(err.Error(), "secret-value") || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("err = %v; want the line number, never its content", err)
	}
}
