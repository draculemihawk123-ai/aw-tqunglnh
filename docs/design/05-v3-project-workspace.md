# V3 — Project, WorkItem và WorkspaceSet

> Entry: V2 gate pass.
>
> Exit: Project đa repository, component catalog, root/child WorkItem, scope expansion và Git
> WorkspaceSet lifecycle vận hành bền vững trước khi chạy workflow.

> Phạm vi task mặc định: kế thừa mục 3 của `00-roadmap.md`.

## V3-01 — Project/Repository/Component persistence

- **Mục tiêu:** catalog authoritative có repository identity độc lập locator/path.
- **Phụ thuộc:** V2.
- **Thực hiện:** migration + repository contracts; `RegisterRepository` atomically tạo record
  `REGISTERING` và probe job/outbox; status machine `REGISTERING→PROBING→ACTIVE|BLOCKED`, còn
  `DISABLED` chỉ là operator action sau khi từng ACTIVE; validate project/component path; typed
  ComponentPackAssignment pin exact PackVersion/effective time/actor.
- **Verify:** CRUD/CAS/idempotency/cross-project và pack-assignment version/provenance tests.
- **Hoàn thành khi:** list/filter không suy identity từ slug/cwd/remote.
- **Nguồn:** ROADMAP-§2, GC-INV-02, HE-03-M04, HE-06-M05.

## V3-02 — Repository onboarding probe

- **Mục tiêu:** onboarding bất đồng bộ chỉ chuyển repository sang ACTIVE khi Git/base ref/toolchain
  metadata có evidence; failure giữ record BLOCKED để retry.
- **Phụ thuộc:** V3-01.
- **Thực hiện:** handler chuyển `REGISTERING→PROBING`, chạy read-only durable probe, canonical path
  safety, base commit, dirty/baseline result, component discovery proposal; typed retry từ `BLOCKED`.
- **Verify:** temp valid/invalid/bare/dirty repos; Windows/Linux path cases.
- **Hoàn thành khi:** environment failure khác validation/business failure.
- **Nguồn:** HE-06-M03, HE-06-M07, HE-06-M02.

## V3-03 — WorkItem contract và readiness gate

- **Mục tiêu:** định nghĩa schema/validator cho root task có schema version, behavior, acceptance,
  verification, risk, out-of-scope và workflow version mà chưa tạo public root command riêng lẻ.
- **Phụ thuộc:** V3-01, V2-12.
- **Thực hiện:** schema migration/domain/contract validator; BACKLOG→READY completeness validation.
  Validator MAY chạy trước V3-04 nhưng không persist WorkItem hoặc transition độc lập.
- **Verify:** missing field, invalid workflow/project/scope và CAS tests.
- **Hoàn thành khi:** task thiếu executable acceptance không thể READY nếu chưa có approval ngoại lệ.
- **Nguồn:** HE-01-M02, HE-07-M02, HE-07-M01, HE-08-M01, HE-08-M06, HE-10-M01, HE-11-M05.

## V3-04 — Root TaskFamily và initial scope transaction

- **Mục tiêu:** public root-create duy nhất tạo WorkItem + TaskFamily + WorkspaceSet intent + initial
  scope + provision jobs atomically.
- **Phụ thuộc:** V3-03.
- **Thực hiện:** normalized READ/WRITE/path scope, add-only version 1, same-project/ACTIVE-repository
  validation, provision jobs/outbox/event/receipt trong transaction.
- **Verify:** rollback failure, duplicate command và multi-repo fixture.
- **Hoàn thành khi:** không có root task orphan family/workspace hoặc job intent thiếu.
- **Nguồn:** AK-ARCH-011, GC-INV-01.

## V3-05 — Child WorkItem subset scope

- **Mục tiêu:** child reuse family/workspace và không thể mở quyền.
- **Phụ thuộc:** V3-04.
- **Thực hiện:** parent/family inheritance, subset algorithm access/path, parent relation/join metadata.
- **Verify:** child same/different repo, path subset, READ→WRITE escalation rejection.
- **Hoàn thành khi:** tạo child không enqueue provision workspace mới.
- **Nguồn:** AK-ARCH-012, GC-INV-05, HE-07-M05, HE-07-M07, HE-08-M07.

## V3-06 — Persist WorkspaceSet/RepositoryWorkspace lifecycle

- **Mục tiêu:** tích hợp Git worktree adapter với state/generation/revision authority.
- **Phụ thuộc:** V3-04.
- **Thực hiện:** provision handler per scoped repo, state aggregation, opaque handle, failure partial state,
  base RevisionSet after all required ready.
- **Verify:** multi-repo provision/restart/partial failure tests.
- **Hoàn thành khi:** family chỉ ready khi mọi required repository ready.
- **Nguồn:** GC-INV-03, GC-INV-04.

## V3-07 — Initialization/readiness evidence

- **Mục tiêu:** writer không chạy trước clean known base và baseline check.
- **Phụ thuộc:** V3-02, V3-06.
- **Thực hiện:** readiness profile, canonical setup/verification recipes, pre-change baseline evidence,
  typed environment blocker.
- **Verify:** baseline green/red/command-error fixtures.
- **Hoàn thành khi:** baseline debt được pin, không giả PASS.
- **Nguồn:** HE-06-M01, HE-06-M06, HE-12-M03.

## V3-08 — Scope expansion request/approval

- **Mục tiêu:** add-only family grant operation tường minh; chưa tự thay scope NodeRun/Attempt vì runtime
  activation thuộc V4.
- **Phụ thuộc:** V3-05, V3-06.
- **Thực hiện:** request object, block WorkItem/run reference nếu có, approve/reject commands, increment
  family scope version, provision added repo và new RevisionSet; emit approved-scope event để V4 append
  RunManifestAmendment và reactivate.
- **Verify:** duplicate approval, remove request, cross-project, concurrent scope update tests.
- **Hoàn thành khi:** provider/prompt không thể tự mở scope.
- **Nguồn:** AK-ARCH-015A.

## V3-09 — WriteLease service production hóa

- **Mục tiêu:** application dùng durable JobLease + WriteLease tách authority.
- **Phụ thuộc:** V3-06.
- **Thực hiện:** batch order by repository ID, all-or-none, heartbeat/release/fence, generation validation;
  Alpha exclusive per RepositoryWorkspace.
- **Verify:** 100 race iterations, TTL/takeover/stale job/generation tests.
- **Hoàn thành khi:** không có hai valid writer cho cùng workspace generation.
- **Nguồn:** AK-ARCH-013, AK-ARCH-009, GC-INV-19, HE-07-M03, HE-13-M07.

## V3-10 — Quarantine/reconcile/recreate commands

- **Mục tiêu:** vận hành recovery workspace mà không destructive reset mù.
- **Phụ thuộc:** V3-09.
- **Thực hiện:** inspect process/revision/diff, accept/block/recreate decision, generation increment,
  retain evidence; cleanup chỉ owned path. Tách public `RequestWorkspaceReconciliation` (kiểm điều kiện,
  ghi intent, enqueue job `CONTROL`) khỏi internal `ExecuteWorkspaceReconciliation` (thực thi
  filesystem/Git); API chỉ được gọi command public.
- **Verify:** kill writer, stale token, dirty unknown, idempotent reconcile tests.
- **Hoàn thành khi:** quarantined workspace không cấp writer hoặc release.
- **Nguồn:** GC-ACC-05.

## V3-11 — Workspace release request và eligibility primitive

- **Mục tiêu:** cung cấp primitive release idempotent chỉ khi không còn active attempt/job/lease,
  quarantine và caller đưa authorization `ReleaseSet sealed|abandoned` hợp lệ.
- **Phụ thuộc:** V3-07, V3-10.
- **Phạm vi:** V3-11 sở hữu public `RequestWorkspaceSetRelease` — kiểm eligibility, ghi intent, enqueue
  job `CONTROL`. Internal `ExecuteWorkspaceSetRelease` (thực thi filesystem/Git) thuộc **V5-14**; route
  thuộc **V6-10B**, valid action thuộc **V7-13A**. API không được expose command internal.
- **Thực hiện:** durable release jobs, per-repo partial result, idempotent Git release, retain metadata/
  artifacts; V3 test bằng fake eligibility authority, không implement CompletionPolicy/ReleaseSet thật.
- **Verify:** active lease/dirty/quarantined/partial release/restart tests; architecture test khẳng định
  request command không tự chạy filesystem/Git.
- **Hoàn thành khi:** cleanup không đụng base repo/family khác và không tự quyết family đã hoàn tất.
- **Nguồn:** AK-ARCH-015C, GC-INV-26, HE-07-M08, HE-12-M01, HE-12-M07.

## V3-12 — V3 multi-repository acceptance gate

- **Mục tiêu:** khóa Project→Family→WorkspaceSet journey.
- **Phụ thuộc:** V3-01…V3-11.
- **Thực hiện:** project hai repo, two root families, children, expansion, parallel leases, restart/release.
- **Verify:** full test/vet + Windows/Linux workspace semantic suite.
- **Hoàn thành khi:** exact RevisionSet/provision evidence trace đủ và mọi invariant thuộc phạm vi V3
  pass; completion/evidence/ReleaseSet authority được để cho V5.
- **Nguồn:** GC-ACC-06, GC-ACC-07, GC-ACC-08.
