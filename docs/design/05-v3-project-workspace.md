# V3 — Project, WorkItem và WorkspaceSet

> Entry: V2 gate pass.
>
> Exit: Project đa repository, component catalog, root/child WorkItem, scope expansion và Git
> WorkspaceSet lifecycle vận hành bền vững trước khi chạy workflow.

## V3-01 — Project/Repository/Component persistence

- **Mục tiêu:** catalog authoritative có repository identity độc lập locator/path.
- **Phụ thuộc:** V2.
- **Thực hiện:** migration + repository contracts + commands create/register/discover/update status;
  validate repository thuộc project và component path.
- **Verify:** CRUD/CAS/idempotency/cross-project tests.
- **Hoàn thành khi:** list/filter không suy identity từ slug/cwd/remote.

## V3-02 — Repository onboarding probe

- **Mục tiêu:** đăng ký repo chỉ thành công khi Git/base ref/toolchain metadata có evidence.
- **Phụ thuộc:** V3-01.
- **Thực hiện:** read-only durable onboarding job, canonical path safety, base commit, dirty/baseline
  result, component discovery proposal.
- **Verify:** temp valid/invalid/bare/dirty repos; Windows/Linux path cases.
- **Hoàn thành khi:** environment failure khác validation/business failure.

## V3-03 — WorkItem contract và readiness gate

- **Mục tiêu:** root task có behavior, acceptance, verification, out-of-scope và workflow version.
- **Phụ thuộc:** V3-01, V2-12.
- **Thực hiện:** schema migration/domain/command; BACKLOG→READY completeness validation; audit transition.
- **Verify:** missing field, invalid workflow/project/scope và CAS tests.
- **Hoàn thành khi:** task thiếu executable acceptance không thể READY nếu chưa có approval ngoại lệ.

## V3-04 — Root TaskFamily và initial scope transaction

- **Mục tiêu:** tạo root WorkItem + TaskFamily + WorkspaceSet intent atomically.
- **Phụ thuộc:** V3-03.
- **Thực hiện:** normalized READ/WRITE/path scope, add-only version 1, same-project validation, provision jobs.
- **Verify:** rollback failure, duplicate command và multi-repo fixture.
- **Hoàn thành khi:** không có root task orphan family/workspace hoặc job intent thiếu.

## V3-05 — Child WorkItem subset scope

- **Mục tiêu:** child reuse family/workspace và không thể mở quyền.
- **Phụ thuộc:** V3-04.
- **Thực hiện:** parent/family inheritance, subset algorithm access/path, parent relation/join metadata.
- **Verify:** child same/different repo, path subset, READ→WRITE escalation rejection.
- **Hoàn thành khi:** tạo child không enqueue provision workspace mới.

## V3-06 — Persist WorkspaceSet/RepositoryWorkspace lifecycle

- **Mục tiêu:** tích hợp Git worktree adapter với state/generation/revision authority.
- **Phụ thuộc:** V3-04.
- **Thực hiện:** provision handler per scoped repo, state aggregation, opaque handle, failure partial state,
  base RevisionSet after all required ready.
- **Verify:** multi-repo provision/restart/partial failure tests.
- **Hoàn thành khi:** family chỉ ready khi mọi required repository ready.

## V3-07 — Initialization/readiness evidence

- **Mục tiêu:** writer không chạy trước clean known base và baseline check.
- **Phụ thuộc:** V3-02, V3-06.
- **Thực hiện:** readiness profile, canonical setup/verification recipes, pre-change baseline evidence,
  typed environment blocker.
- **Verify:** baseline green/red/command-error fixtures.
- **Hoàn thành khi:** baseline debt được pin, không giả PASS.

## V3-08 — Scope expansion request/approval

- **Mục tiêu:** add-only scope operation tường minh theo ADR-002.
- **Phụ thuộc:** V3-05, V3-06.
- **Thực hiện:** request object, block target node/work item, approve/reject commands, increment scope version,
  provision added repo và new RevisionSet.
- **Verify:** duplicate approval, remove request, cross-project, concurrent scope update tests.
- **Hoàn thành khi:** provider/prompt không thể tự mở scope.

## V3-09 — WriteLease service production hóa

- **Mục tiêu:** application dùng durable JobLease + WriteLease tách authority.
- **Phụ thuộc:** V3-06.
- **Thực hiện:** batch order by repository ID, all-or-none, heartbeat/release/fence, generation validation;
  Alpha exclusive per RepositoryWorkspace.
- **Verify:** 100 race iterations, TTL/takeover/stale job/generation tests.
- **Hoàn thành khi:** không có hai valid writer cho cùng workspace generation.

## V3-10 — Quarantine/reconcile/recreate commands

- **Mục tiêu:** vận hành recovery workspace mà không destructive reset mù.
- **Phụ thuộc:** V3-09.
- **Thực hiện:** inspect process/revision/diff, accept/block/recreate decision, generation increment,
  retain evidence; cleanup chỉ owned path.
- **Verify:** kill writer, stale token, dirty unknown, idempotent reconcile tests.
- **Hoàn thành khi:** quarantined workspace không cấp writer hoặc release.

## V3-11 — Release WorkspaceSet gate

- **Mục tiêu:** release chỉ khi family terminal, no active attempt/job/lease và clean evidence đạt.
- **Phụ thuộc:** V3-07, V3-10.
- **Thực hiện:** durable release jobs, per-repo partial result, idempotent Git release, retain metadata/artifacts.
- **Verify:** active lease/dirty/quarantined/partial release/restart tests.
- **Hoàn thành khi:** cleanup không đụng base repo hoặc family khác.

## V3-12 — V3 multi-repository acceptance gate

- **Mục tiêu:** khóa Project→Family→WorkspaceSet journey.
- **Phụ thuộc:** V3-01…V3-11.
- **Thực hiện:** project hai repo, two root families, children, expansion, parallel leases, restart/release.
- **Verify:** full test/vet + Windows/Linux workspace semantic suite.
- **Hoàn thành khi:** exact RevisionSet/evidence trace đủ và mọi invariant architecture 01 pass.
