# Agent Kit — Start here / handoff hiện tại

> Đọc tài liệu này đầu tiên khi bắt đầu một session mới. Nó là điểm vào cho mục tiêu, các quyết định
> đã chốt, trạng thái thực thi và ranh giới công việc hiện tại.
>
> Cập nhật: 2026-09-05 (mục 20-22, ADR-026, ADR-027). Nếu tài liệu này khác ADR, ADR là authority về
> quyết định kiến trúc; cần sửa tài liệu này trong cùng thay đổi, không tự suy diễn.

## 1. Mục tiêu sản phẩm

Agent Kit là harness/runtime để tự động hóa quy trình phát triển phần mềm với AI. Hệ thống không
hard-code một quy trình duy nhất: người vận hành định nghĩa các **Block** (khối công việc) và ghép
chúng thành workflow graph versioned. Một task đi qua các block, tạo state, context, evidence và
quyết định chuyển tiếp có thể truy vết được.

Nó phải phục vụ được project lớn, đa repository: backend microservice, frontend/micro-frontend,
mobile, Docker/Kubernetes và automation test có thể cùng thuộc một Project. Layer/Skill cung cấp
tri thức và convention theo technology stack; runtime giữ scope, worktree, verification và evidence.

Đây **không** là BPMN, không giả lập import/export BPMN và không trao quyền thực thi chỉ bằng prompt.

## 2. Yêu cầu cốt lõi đã chốt

1. `Project → Repository[]` là domain chuẩn từ đầu; UI có thể focus một repository tại một thời điểm
   nhưng không giới hạn task/runtime vào một repository.
2. Root `WorkItem` tạo `TaskFamily → WorkspaceSet`; child kế thừa family/workspace. Hai root family
   không dùng chung mutable worktree; sibling chỉ song song khi scope/lease cho phép.
3. Workflow, Block, Skill, Layer, policy và adapter đều có version publish bất biến. Một run pin đúng
   version/hash đã bắt đầu, không đọc definition hiện tại trên disk để đổi hành vi.
4. Context là dữ liệu do platform sở hữu. Sau crash/retry, execution mới luôn được `Start` từ
   `ContextSnapshot`; `ProviderSessionRef` chỉ để correlation/debug, không là điều kiện recovery.
5. Claude CLI, Codex CLI và CLI tương lai đi qua process/protocol adapter. Domain/application không
   có nhánh xử lý riêng theo provider.
6. Skill/Layer/Engineering Pack chỉ chứa instruction và resource. Command, gate, scaffold hay script
   thực thi phải là executable definition/executor versioned, được policy cấp quyền tường minh.
7. Alpha là single-user local, lưu SQLite và artifact local disk, không mã hóa at-rest; TTL 7 ngày chỉ
   áp cho raw output/evidence tạm theo retention class, secret vẫn phải redact trước persist. Beta là
   deployment tập trung riêng với PostgreSQL, login/RBAC,
   tổ chức/team và server worker. Alpha và beta **không đồng bộ dữ liệu** với nhau.
8. UI cuối cùng cần Kanban, task detail/timeline, workflow/block đang chạy hoặc bị block, quản lý
   Skill/Layer/Agent và chat theo task. UI chỉ gọi application API, không spawn Git/CLI hay ghi state
   runtime trực tiếp.
9. Scope expansion không sửa manifest/NodeRun cũ: approval tạo `RunManifestAmendment` và activation
   mới. END chỉ tạo completion candidate; CompletionPolicy mới chuyển Run/WorkItem thành công.
10. Alpha phân biệt isolation được enforce với executable local được operator tin cậy; không gọi
    cwd/diff guard là sandbox. Multi-repository write chỉ dành cho integration capability.
11. Alpha có ReleaseSet local và local commit theo policy; không có push/PR/merge executor. Projection
    dùng JournalPosition durable và recovery lease chạy định kỳ, không chỉ khi startup.
12. TTL 7 ngày áp cho evidence/raw payload mặc định. Canonical conversation/context phục vụ recovery
    không bị blanket TTL và hold được tôn trọng. UI Alpha có source/diff/log read-only và không có
    interactive browser terminal.
13. `ExecutionAttempt` có terminal `BLOCKED` tách khỏi `FAILED`; scope expansion dùng
    `TerminationReason=SCOPE_EXPANSION_REQUIRED` và không tiêu thụ retry budget.
14. Cancel run đi qua `CANCELLING` và quiesce thật; outcome chưa xác định là `INDETERMINATE` +
    `QUARANTINED`, không phải `CANCELLED`. Cancel không tự cleanup workspace hay abandon ReleaseSet.
    `CancelRun` chỉ no-op khi **Run** đã terminal; Attempt terminal hay END không làm cancel no-op.
    `CancelRun`, `CancelWorkItem` và `ResolveWorkItemBlocker` là ba command khác nhau.
15. CompletionPolicy có đúng bốn outcome `PASS|REWORK|BLOCK|FAIL` với transition cố định; `REWORK` thiếu
    rework edge đã publish phải thành `BLOCK`.
16. `AdapterBuildVersion` là operational registry, không phải DefinitionKind; có surface probe/register
    riêng và run đang chạy không bao giờ bị tự động repin.
17. Isolation profile đã pin không auto-downgrade. Mọi admission check fail-closed cho Attempt kết thúc
    `QUEUED → BLOCKED` với reason typed; `RetryBlockedActivation` tạo activation mới sau khi revalidate
    pin, không hồi sinh Attempt cũ.
18. Acceptance criteria được phân loại phase trước V1 bằng pre-V1 gate `V1-00A…V1-00C`; V8 không phân loại lại.
19. Command envelope dùng `CommandScope = INSTALLATION | PROJECT(ProjectID)`; adapter registry, Doctor
    và safe settings là installation-scoped.
20. Node type `ROUTER` chỉ được khai báo đúng một outcome cho Alpha (ADR-026); publish một `ROUTER`
    nhiều outcome là lỗi validation, không phải hành vi runtime âm thầm deadlock. Multi-outcome rule
    thật hoãn tới ADR/authoring schema riêng.
21. `ExecutionProfileHash` không bao giờ nhận một hash trần từ caller (ADR-027): scheduling transaction
    chỉ nhận `RuntimeExecutionConfigSnapshotV1` đã resolve qua `ports.RuntimeExecutionConfigProvider`
    rồi tự canonicalize/tính hash — nhận thẳng hash từ caller sẽ đảo ngược authority.
22. V4-04's scheduling resolver (`ScheduleExecutableNodeRun`) fail-closed khi một node executable
    (AGENT/COMMAND/MACHINE_GATE) không pin được đúng một Policy category ATTEMPT (nguồn của
    `TimeoutSeconds`) và đúng một Policy category PERMISSION (nguồn của `IsolationTier`) —
    `ErrAttemptPolicyRequired`/`ErrPermissionPolicyRequired`. Một node thiếu policy này không bao giờ
    được lên lịch: `ResolvedExecutionProfileV1` (GC-INV-08) đòi hỏi timeout dương và isolation tier
    tường minh, không có default ngầm.

## 3. Glossary chuẩn

| Term | Nghĩa chuẩn |
|---|---|
| Block | Định nghĩa một loại công việc/node có input, policy, executor reference và outcome hợp lệ. |
| Workflow | Graph các Block versioned; không phải state machine hard-code trong UI/domain. |
| WorkItem | Task nghiệp vụ/Kanban card. |
| TaskFamily | Boundary ownership của root WorkItem và toàn bộ descendant. |
| WorkspaceSet | Tập RepositoryWorkspace/worktree của một TaskFamily. |
| Layer | Instruction/resource về convention cho một stack, ví dụ Java/Spring Boot, Go, Angular, React, PHP, Docker/K8s. |
| Skill | Instruction/resource chuyên cho một loại công việc, ví dụ implement, review, test hay migration. |
| Engineering Pack | Gói versioned để resolve một tập Layer/Skill/resource tương thích; không phải executable authority và không thay thế Layer. |
| Executor | Capability thực thi đã đăng ký/versioned, ví dụ agent CLI adapter, command/gate/scaffold runner. |
| ContextSnapshot | Snapshot canonical của message/resource/revision để bắt đầu execution mới. |
| CompletionDecision | Quyết định typed của CompletionPolicy: `PASS`, `REWORK`, `BLOCK` hoặc `FAIL`. |
| CancellationIntent | Durable intent đưa Run vào `CANCELLING`; Run chỉ `CANCELLED` sau khi quiesce. |
| Phase label | Nhãn scope của một acceptance criterion: `ALPHA_MUST`, `BETA_ADAPTER_GATE`, `BETA_PARITY_GATE`, `CROSS_PHASE_GUARD` hoặc `NOT_APPLICABLE`. |
| TerminationReason | Enum runtime giải thích vì sao một Attempt kết thúc; khác `AppError.Code`. |
| SourceRef | Tham chiếu chuẩn trong trường `Nguồn`: `ADR`, `AK-ARCH`, `HE`, `GC-INV`, `GC-ACC`, `ROADMAP`. |
| Evidence | Artifact immutable, redacted, hash-verified chứng minh outcome/gate. |
| RunManifestAmendment | Revision append-only mở rộng scope đã được duyệt mà không sửa ExecutionManifest cũ. |
| ReleaseSet | Kết quả release tương quan nhiều repository; mỗi repository có revision/gate/result riêng. |
| AdapterBuildVersion | Identity bất biến của adapter/protocol/build/capability thực thi. |
| JournalPosition | Vị trí delivery monotonic của event, tách khỏi sequence trong từng aggregate. |

## 4. Trạng thái lộ trình hiện tại

| Mục | Trạng thái | Quy tắc |
|---|---|---|
| 1. Domain Project/Repository/TaskFamily/WorkspaceSet | Hoàn tất tài liệu baseline | Không đổi semantics nếu không có ADR mới. |
| 2. Quyết định kiến trúc | ACCEPTED | ADR-001…025 là baseline sau review thiết kế ngày 2026-08-31. |
| 3. Go core architecture/spec | Hoàn tất specification baseline | Code phải bám spec hoặc tạo ADR superseding. |
| 4. Go spike | **GO** (2026-09-01, V0-14) | SPK-01…SPK-14 đều pass thật trên Windows và Linux (CI), evidence verify được, 10/10 suite runs không flaky đúng semantics, `-race` pass trên CI. Chi tiết: [spike report](spikes/02-go-core-spike-report.md). |
| 5. Alpha UI/runtime | **ĐƯỢC PHÉP BẮT ĐẦU** | Verdict `GO` đã ghi; bắt đầu theo đúng dependency/gate trong `docs/design/00-roadmap.md`, mỗi session một Task ID. |
| 6. Thiết kế chi tiết version/subtask | **BASELINE ĐÃ QUYẾT ĐỊNH** | Roadmap Alpha và task theo session nằm tại `docs/design/`; đã cập nhật theo ADR-020…025. V1 được phép thực thi từ verdict `GO` (mục 4), vẫn phải tuân dependency/gate trong roadmap. |

Spike đã đạt gate: SPK-01…SPK-14 đều có evidence PASS thật (13/14 trực tiếp trên mỗi platform, SPK-13
qua job `semantic-diff` cross-platform riêng — không thể/không được kết luận từ một platform đơn lẻ).
Race detector sạch, 10 lần chạy full suite liên tiếp không flaky, mỗi lần có bằng chứng tường minh
(không suy từ exit code). Xem [spike report](spikes/02-go-core-spike-report.md) mục 3 để có đầy đủ CI
run ID/evidence ID.

## 5. Đọc theo thứ tự này

1. Tài liệu này.
2. [Kinh nghiệm từ claude-workflow](danh-gia-claude-workflow.md).
3. [Tổng quan harness engineering](harness-engineering/00-tong-quan.md) và lecture liên quan.
4. [Mô hình Project–Repository–WorkspaceSet](architecture/01-project-repository-workspace-model.md).
5. [Architecture decisions](architecture/02-architecture-decisions.md), gồm ADR-001…025.
6. [System architecture](architecture/03-system-architecture.md) và [Go core spec](architecture/04-go-core-spec.md).
7. [Roadmap Alpha](design/00-roadmap.md) và [thiết kế hệ thống Alpha](design/01-system-design.md).
8. [Go spike plan](spikes/01-go-core-spike-plan.md), rồi [spike report](spikes/02-go-core-spike-report.md).
9. Khi thực thi, đọc đúng file version trong `docs/design/` chứa Task ID được giao.

Khi bắt đầu code, đọc spike report trước để biết gate nào thiếu; không suy trạng thái từ tên test hay
code hiện có. Khi cần quyết định mới, kiểm ADR trước; thay đổi semantics phải bổ sung ADR superseding.

## 6. Cách chạy spike hiện có

Từ repository root, dùng Go toolchain đã cài. `acceptance --offline` (baseline `go test` wrapper) và
`acceptance --full` (registry thật, 14 scenario, dùng để đóng gate) là hai lệnh khác nhau — `--full`
mới là lệnh tạo evidence cho verdict `GO`:

```text
go vet ./...
go test -count=1 ./...
go build -o bin/agentkit-spike ./cmd/agentkit-spike
go build -o bin/fake-claude ./cmd/fake-claude
go build -o bin/fake-codex ./cmd/fake-codex
go build -o bin/spike-helper ./cmd/spike-helper
go build -o bin/spike-worker ./cmd/spike-worker
./bin/agentkit-spike acceptance --full --assessment \
  --evidence-dir docs/spikes/evidence \
  --fake-claude bin/fake-claude --fake-codex bin/fake-codex \
  --spike-helper bin/spike-helper --spike-worker bin/spike-worker
./bin/agentkit-spike evidence verify --evidence-dir docs/spikes/evidence --suite <suite-id>-<spkId>
```

`--assessment` thoát mã 0 khi harness/evidence hoàn chỉnh dù một SPK cụ thể (SPK-13, khi chạy đơn
platform) báo `false` — dùng cho CI thường trực, không tự nhận `GO`. `--require-all-pass` là gate đóng
cuối, thoát khác 0 nếu bất kỳ SPK nào false. SPK-13 chỉ có kết luận thật (authoritative) qua
`agentkit-spike semantic-diff --left --right --out --evidence-dir`, so hai manifest từ hai platform
thật — xem CI job `cross-platform semantic diff` trong
[spike-gate.yml](../.github/workflows/spike-gate.yml).

Evidence generated nằm dưới `docs/spikes/evidence/`, bị Git ignore và raw spike payload có retention
mặc định 7 ngày. CI (`.github/workflows/spike-gate.yml`) chạy đủ: `contract` (vet+test hai OS),
`spike acceptance` (registry thật hai OS), `Linux race and stability` (`-race` + 10 lần full suite,
V0-12), `cross-platform semantic diff` (SPK-13, V0-11), `Boundary/dependency report` (V0-13).

## 7. Definition of done cho mục 4

Chỉ ghi `GO` khi SPK-01…SPK-14 đều pass offline trên Windows và Linux, có evidence verify được,
không flaky qua 10 suite runs và `-race` chạy trên CI hỗ trợ. Nếu chưa đạt:

- **REWORK:** invariant/primitive bị chứng minh sai hoặc flaky; sửa hẹp, lưu finding, chạy lại SPK
  ảnh hưởng và regression. Không hạ gate.
- **CHƯA ĐỦ EVIDENCE:** Linux/race/acceptance chưa chạy; không kết luận kiến trúc sai nhưng cũng không
  làm mục 5.
- **STOP:** evidence chứng minh một lựa chọn lõi không thể giữ invariant; ghi phương án thay thế bằng
  ADR, không suy từ cảm nhận hay số dòng code.

**Đã ghi `GO` ngày 2026-09-01 (V0-14).** Toàn bộ bốn tiêu chí trên đạt bằng evidence thật, dẫn chi tiết
tại [spike report](spikes/02-go-core-spike-report.md) mục 3 (CI run ID, artifact name, evidence suite
ID cụ thể). Một gap thật từng phát hiện ở chính bài kiểm 10-run (không kiểm `result.Passed`, chỉ kiểm
có evidence) đã được sửa và chạy lại nguyên chuỗi 10 lần từ đầu trước khi verdict này được ghi — xem
spike report mục 6, finding #10.

## 8. Quy tắc cập nhật tài liệu

- Quyết định semantics: cập nhật ADR và tài liệu này.
- Thay đổi tiến độ/evidence/gate: cập nhật spike report và tài liệu này nếu milestone đổi.
- Thêm thuật ngữ: cập nhật glossary ở đây trước khi dùng lẫn lộn ở docs khác.
- Không commit/push nếu user chưa yêu cầu. Thiết kế mục 6 đã được user cho phép; execution vẫn phải
  tuân dependency/gate trong `docs/design/00-roadmap.md` và mỗi session chỉ làm đúng một Task ID.
