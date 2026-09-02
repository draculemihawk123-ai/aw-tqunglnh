package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
)

// adapterBuildRepository implements ports.AdapterBuildRepository
// (docs/design/04-v2-definition-plane.md V2-07A) against the
// adapter_build_versions / adapter_build_signing_keys tables
// (0005_adapter_build_versions.sql).
type adapterBuildRepository struct{ tx *sql.Tx }

var _ ports.AdapterBuildRepository = adapterBuildRepository{}

func (r adapterBuildRepository) LoadOrCreateSigningKey(ctx context.Context) ([]byte, error) {
	key, err := r.loadSigningKey(ctx)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, ports.ErrNoSigningKey) {
		return nil, err
	}
	newKey := make([]byte, 32)
	if _, err := rand.Read(newKey); err != nil {
		return nil, fmt.Errorf("sqlite: generate adapter build signing key: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := r.tx.ExecContext(ctx,
		`INSERT INTO adapter_build_signing_keys (id, secret_key_hex, created_at) VALUES (1, ?, ?)`,
		hex.EncodeToString(newKey), now,
	); err != nil {
		return nil, MapSQLiteError(fmt.Errorf("insert adapter build signing key: %w", err))
	}
	return newKey, nil
}

func (r adapterBuildRepository) LoadSigningKey(ctx context.Context) ([]byte, error) {
	return r.loadSigningKey(ctx)
}

func (r adapterBuildRepository) loadSigningKey(ctx context.Context) ([]byte, error) {
	var hexKey string
	err := r.tx.QueryRowContext(ctx, `SELECT secret_key_hex FROM adapter_build_signing_keys WHERE id = 1`).Scan(&hexKey)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ports.ErrNoSigningKey
	}
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("load adapter build signing key: %w", err))
	}
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("sqlite: decode stored signing key: %w", err)
	}
	return key, nil
}

func (r adapterBuildRepository) RotateSigningKey(ctx context.Context) ([]byte, error) {
	newKey := make([]byte, 32)
	if _, err := rand.Read(newKey); err != nil {
		return nil, fmt.Errorf("sqlite: generate adapter build signing key: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := r.tx.ExecContext(ctx,
		`INSERT INTO adapter_build_signing_keys (id, secret_key_hex, created_at) VALUES (1, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET secret_key_hex = excluded.secret_key_hex, created_at = excluded.created_at`,
		hex.EncodeToString(newKey), now,
	); err != nil {
		return nil, MapSQLiteError(fmt.Errorf("rotate adapter build signing key: %w", err))
	}
	return newKey, nil
}

func (r adapterBuildRepository) InsertIfAbsent(ctx context.Context, build adapterbuild.Build) (adapterbuild.Build, bool, error) {
	existing, err := r.get(ctx, build.ID())
	if err == nil {
		return existing, true, nil
	}
	if !errors.Is(err, ports.ErrAdapterBuildNotFound) {
		return adapterbuild.Build{}, false, err
	}

	manifestJSON, _, err := adapterbuild.HashCapabilityManifest(build.CapabilityManifest())
	if err != nil {
		return adapterbuild.Build{}, false, err
	}
	tuple := build.Tuple()
	if _, err := r.tx.ExecContext(ctx, `
INSERT INTO adapter_build_versions (
	id, provider_key, executable_path, executable_content_hash, protocol_version,
	capability_manifest, capability_manifest_hash, os, toolchain, config_identity,
	registered_by, registered_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		build.ID(), tuple.ProviderKey, tuple.ExecutablePath, tuple.ExecutableContentHash, tuple.ProtocolVersion,
		manifestJSON, tuple.CapabilityManifestHash, tuple.OS, tuple.Toolchain, tuple.ConfigIdentity,
		build.RegisteredBy(), build.RegisteredAt().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return adapterbuild.Build{}, false, MapSQLiteError(fmt.Errorf("insert adapter build version: %w", err))
	}
	return build, false, nil
}

func (r adapterBuildRepository) Get(ctx context.Context, id string) (adapterbuild.Build, error) {
	return r.get(ctx, id)
}

func (r adapterBuildRepository) get(ctx context.Context, id string) (adapterbuild.Build, error) {
	var providerKey, executablePath, executableContentHash, protocolVersion string
	var capabilityManifestHash, os, toolchain, configIdentity string
	var manifestJSON, registeredBy, registeredAtText string
	err := r.tx.QueryRowContext(ctx, `
SELECT provider_key, executable_path, executable_content_hash, protocol_version,
       capability_manifest, capability_manifest_hash, os, toolchain, config_identity,
       registered_by, registered_at
FROM adapter_build_versions WHERE id = ?`, id,
	).Scan(&providerKey, &executablePath, &executableContentHash, &protocolVersion,
		&manifestJSON, &capabilityManifestHash, &os, &toolchain, &configIdentity,
		&registeredBy, &registeredAtText)
	if errors.Is(err, sql.ErrNoRows) {
		return adapterbuild.Build{}, ports.ErrAdapterBuildNotFound
	}
	if err != nil {
		return adapterbuild.Build{}, MapSQLiteError(fmt.Errorf("load adapter build version: %w", err))
	}
	return rebuildAdapterBuild(providerKey, executablePath, executableContentHash, protocolVersion,
		manifestJSON, capabilityManifestHash, os, toolchain, configIdentity, registeredBy, registeredAtText)
}

func (r adapterBuildRepository) List(ctx context.Context) ([]adapterbuild.Build, error) {
	rows, err := r.tx.QueryContext(ctx, `
SELECT provider_key, executable_path, executable_content_hash, protocol_version,
       capability_manifest, capability_manifest_hash, os, toolchain, config_identity,
       registered_by, registered_at
FROM adapter_build_versions ORDER BY registered_at`)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list adapter build versions: %w", err))
	}
	defer rows.Close()

	var builds []adapterbuild.Build
	for rows.Next() {
		var providerKey, executablePath, executableContentHash, protocolVersion string
		var capabilityManifestHash, os, toolchain, configIdentity string
		var manifestJSON, registeredBy, registeredAtText string
		if err := rows.Scan(&providerKey, &executablePath, &executableContentHash, &protocolVersion,
			&manifestJSON, &capabilityManifestHash, &os, &toolchain, &configIdentity,
			&registeredBy, &registeredAtText); err != nil {
			return nil, MapSQLiteError(fmt.Errorf("scan adapter build version: %w", err))
		}
		build, err := rebuildAdapterBuild(providerKey, executablePath, executableContentHash, protocolVersion,
			manifestJSON, capabilityManifestHash, os, toolchain, configIdentity, registeredBy, registeredAtText)
		if err != nil {
			return nil, err
		}
		builds = append(builds, build)
	}
	if err := rows.Err(); err != nil {
		return nil, MapSQLiteError(fmt.Errorf("iterate adapter build versions: %w", err))
	}
	return builds, nil
}

func decodeCapabilityManifest(canonicalJSON string) (adapterbuild.CapabilityManifest, error) {
	var manifest adapterbuild.CapabilityManifest
	if err := json.Unmarshal([]byte(canonicalJSON), &manifest); err != nil {
		return adapterbuild.CapabilityManifest{}, fmt.Errorf("sqlite: decode stored capability manifest: %w", err)
	}
	return manifest, nil
}

func rebuildAdapterBuild(
	providerKey, executablePath, executableContentHash, protocolVersion string,
	manifestJSON, capabilityManifestHash, os, toolchain, configIdentity string,
	registeredBy, registeredAtText string,
) (adapterbuild.Build, error) {
	manifest, err := decodeCapabilityManifest(manifestJSON)
	if err != nil {
		return adapterbuild.Build{}, err
	}
	registeredAt, err := parseDBTime(registeredAtText)
	if err != nil {
		return adapterbuild.Build{}, err
	}
	return adapterbuild.NewBuild(adapterbuild.NewBuildRequest{
		Tuple: adapterbuild.CandidateTuple{
			ProviderKey:            providerKey,
			ExecutablePath:         executablePath,
			ExecutableContentHash:  executableContentHash,
			ProtocolVersion:        protocolVersion,
			CapabilityManifestHash: capabilityManifestHash,
			OS:                     os,
			Toolchain:              toolchain,
			ConfigIdentity:         configIdentity,
		},
		CapabilityManifest: manifest,
		RegisteredBy:       registeredBy,
		RegisteredAt:       registeredAt,
	})
}
