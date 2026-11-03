package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"sort"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/secretstore"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// V12-29 (ADR-061): `aw repository secret list|set|delete`. A value is read from stdin or a file, never from argv (a process's
// argv is visible to every user of the machine); the command hash covers names only, like the HTTP routes'.

func init() {
	for _, d := range []cli.Descriptor{
		{Path: []string{"repository", "secret", "list"}, Scope: cli.ScopeProject, AppOperation: "ListRepositorySecrets", HTTPOperationID: "listRepositorySecrets"},
		{Path: []string{"repository", "secret", "set"}, Scope: cli.ScopeProject, AppOperation: "SetRepositorySecrets", HTTPOperationID: "setRepositorySecrets"},
		{Path: []string{"repository", "secret", "delete"}, Scope: cli.ScopeProject, AppOperation: "DeleteRepositorySecret", HTTPOperationID: "deleteRepositorySecret"},
	} {
		cli.MustRegister(d)
	}
}

// SecretDependencies is what the secret leaves need.
type SecretDependencies struct {
	Dependencies
	Keys ports.SecretKeyProvider
}

func secretLeaf(fs *flag.FlagSet, args []string, usage string) (projectID, repositoryID string, err error) {
	project := cli.BindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return "", "", err
	}
	if len(fs.Args()) != 1 || strings.TrimSpace(*project) == "" {
		return "", "", usageErrorf("usage: aw repository secret %s", usage)
	}
	return *project, fs.Args()[0], nil
}

// RunRepositorySecretList implements `aw repository secret list --project-id <p> <repositoryId>`: names only.
func RunRepositorySecretList(ctx context.Context, deps SecretDependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository secret list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID, repositoryID, err := secretLeaf(fs, args, "list --project-id <projectId> <repositoryId>")
	if err != nil {
		return err
	}
	list, err := secretstore.List(ctx, secretstore.Deps{UnitOfWork: deps.UoW, Keys: deps.Keys}, projectID, repositoryID)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, list)
}

func secretMutation(ctx context.Context, deps SecretDependencies, stdout io.Writer, principalConfigPath, idempotencyKey, commandType, projectID, repositoryID string,
	names []string, run func(cmd ports.Command, d secretstore.Deps) (any, error)) error {
	sort.Strings(names)
	payload, err := json.Marshal(struct {
		RepositoryID string   `json:"repositoryId"`
		Names        []string `json:"names"`
	}{repositoryID, names})
	if err != nil {
		return err
	}
	return projectMutation(ctx, deps.Dependencies, stdout, projectID, principalConfigPath, idempotencyKey, commandType, 0, payload,
		func(ctx context.Context, cmd ports.Command) (any, error) {
			return run(cmd, secretstore.Deps{UnitOfWork: deps.UoW, Keys: deps.Keys})
		})
}

// RunRepositorySecretSet implements `aw repository secret set --project-id <p> --name <NAME> <repositoryId>` with the value on stdin (or
// --file), or `aw repository secret set --project-id <p> --env-file <.env> <repositoryId>` to import every NAME=value of a .env file
// once (only the names are printed; the file is not kept).
func RunRepositorySecretSet(ctx context.Context, deps SecretDependencies, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository secret set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	name := fs.String("name", "", "the secret's name (an environment variable name); its value is read from stdin or --file")
	file := cli.BindFileFlag(fs)
	envFile := fs.String("env-file", "", "import every NAME=value of this .env file instead of one --name")
	projectID, repositoryID, err := secretLeaf(fs, args, "set --project-id <projectId> (--name <NAME> | --env-file <.env>) <repositoryId>")
	if err != nil {
		return err
	}
	if (*name == "") == (*envFile == "") {
		return usageErrorf("give either --name (the value on stdin or --file) or --env-file")
	}
	values := map[string]string{}
	if *envFile != "" {
		raw, err := cli.ReadBoundedInput(stdin, *envFile, 512<<10)
		if err != nil {
			return cli.UsageError{Err: err}
		}
		if values, err = secretstore.ParseDotEnv(string(raw)); err != nil {
			return cli.UsageError{Err: err}
		}
		if len(values) == 0 {
			return usageErrorf("the .env file has no NAME=value line")
		}
	} else {
		if err := secretstore.ValidateName(*name); err != nil {
			return cli.UsageError{Err: err}
		}
		raw, err := cli.ReadBoundedInput(stdin, *file, secretstore.MaxValueBytes)
		if err != nil {
			return cli.UsageError{Err: err}
		}
		value := strings.TrimRight(string(raw), "\r\n")
		if value == "" {
			return usageErrorf("the secret value is empty (pass it on stdin or with --file)")
		}
		values[*name] = value
	}
	names := make([]string, 0, len(values))
	for k := range values {
		names = append(names, k)
	}
	return secretMutation(ctx, deps, stdout, *principalConfigPath, *idempotencyKey, "SetRepositorySecrets", projectID, repositoryID, names,
		func(cmd ports.Command, d secretstore.Deps) (any, error) {
			return secretstore.Set(ctx, d, cmd, projectID, repositoryID, values)
		})
}

// RunRepositorySecretDelete implements `aw repository secret delete --project-id <p> --name <NAME> <repositoryId>`.
func RunRepositorySecretDelete(ctx context.Context, deps SecretDependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository secret delete", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	name := fs.String("name", "", "the secret's name")
	projectID, repositoryID, err := secretLeaf(fs, args, "delete --project-id <projectId> --name <NAME> <repositoryId>")
	if err != nil {
		return err
	}
	if *name == "" {
		return errors.New("cli: repository secret delete: --name is required")
	}
	return secretMutation(ctx, deps, stdout, *principalConfigPath, *idempotencyKey, "DeleteRepositorySecret", projectID, repositoryID, []string{*name},
		func(cmd ports.Command, d secretstore.Deps) (any, error) {
			return secretstore.Delete(ctx, d, cmd, projectID, repositoryID, *name)
		})
}
