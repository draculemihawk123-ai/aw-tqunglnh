/**
 * Hand-declared wire shapes for the definitions catalog operations —
 * internal/delivery/httpapi/definitions's own response DTOs nest named Go
 * types (definition.Kind, definition.Status, scopeView, DependencyManifest)
 * one level deeper than apicontract's generator expands, so the generated
 * client returns `unknown` for all of them. These interfaces mirror
 * internal/delivery/httpapi/definitions/dto.go's own view types and
 * internal/domain/definition's own Kind/Status enums field-for-field — the
 * same "narrow `unknown` at the call site" convention web/src/api/catalog.ts
 * already established for the sibling catalog package's identical gap.
 */

export const DEFINITION_KINDS = [
  'WORKFLOW', 'BLOCK', 'SKILL', 'LAYER', 'ENGINEERING_PACK',
  'AGENT_PROFILE', 'COMMAND', 'GATE', 'POLICY',
] as const;

export type DefinitionKind = (typeof DEFINITION_KINDS)[number];
export type DefinitionStatus = 'DRAFT' | 'ACTIVE' | 'ARCHIVED';

export interface DefinitionScope {
  global: boolean;
  projectId?: string;
}

export interface DefinitionView {
  id: string;
  kind: DefinitionKind;
  scope: DefinitionScope;
  name: string;
  status: DefinitionStatus;
  version: number;
}

export interface DefinitionListView {
  definitions: DefinitionView[];
}

export interface DependencyPin {
  kind: DefinitionKind;
  definitionId: string;
  versionId: string;
}

export interface DependencyManifest {
  pins?: DependencyPin[];
}

export interface VersionFieldsView {
  id: string;
  definitionId: string;
  kind: DefinitionKind;
  versionNumber: number;
  schemaVersion: number;
  canonicalSource: string;
  sourceHash: string;
  compiledSnapshot: string;
  compiledHash: string;
  dependencies: DependencyManifest;
  publishedBy: string;
  publishedAt: string;
}

export interface VersionListResponse {
  items: VersionFieldsView[];
}
