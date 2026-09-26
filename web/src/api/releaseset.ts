/**
 * Hand-declared wire shapes for the V6-10F ReleaseSet routes —
 * `internal/delivery/httpapi/releaseset`'s own DTOs (`CreateReleaseSetRequest.repositories`,
 * `GetReleaseSetResponse.entries`, `ListReleaseSetsForFamilyResponse.items`)
 * nest one level deeper than apicontract's shallow generator expands, so
 * the generated client returns `unknown[]` for them. Field-for-field
 * mirror of `internal/app/work/release_set.go`'s and
 * `release_set_queries.go`'s own types — the same "narrow `unknown` at the
 * call site" convention every sibling API-gap file this session already
 * established.
 */

/** gate.Verdict's own closed 5-value set — the only values the server ever accepts. */
export type Verdict = 'PASS' | 'FAIL' | 'ERROR' | 'NOT_RUN' | 'NOT_APPLICABLE';

export const VERDICT_OPTIONS: Verdict[] = ['PASS', 'FAIL', 'ERROR', 'NOT_RUN', 'NOT_APPLICABLE'];

export interface RepositoryReleaseRequest {
  repositoryId: string;
  baseVcsObjectId: string;
  resultVcsObjectId: string;
  verdict: Verdict;
}

export interface RepositoryReleaseDetail {
  repositoryId: string;
  baseVcsObjectId: string;
  resultVcsObjectId: string;
  verdict: Verdict | string;
}

export type ReleaseSetState = 'CREATED' | 'SEALED' | 'ABANDONED';

export type ReleaseSetLocalCommitState = 'REQUESTED' | 'COMMITTED' | 'FAILED';

export interface ReleaseSetDetail {
  releaseSetId: string;
  projectId: string;
  familyId: string;
  state: ReleaseSetState | string;
  contentHash: string;
  version: number;
  entries: RepositoryReleaseDetail[];
}
