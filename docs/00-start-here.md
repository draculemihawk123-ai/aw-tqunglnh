# Agent Kit — Start here / handoff hiện tại

> Đọc tài liệu này đầu tiên khi bắt đầu một session mới. Nó là điểm vào cho mục tiêu, các quyết định
> đã chốt, trạng thái thực thi và ranh giới công việc hiện tại.
>
> Cập nhật: 2026-08-28. Nếu tài liệu này khác ADR, ADR là authority về quyết định kiến trúc; cần sửa
> tài liệu này trong cùng thay đổi, không tự suy diễn.

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
   `ContextSnapshot`; `ProviderSessionRef` chỉ để correlation/debug, không là điều kiện resume.
5. Claude CLI, Codex CLI và CLI tương lai đi qua process/protocol adapter. Domain/application không
   có nhánh xử lý riêng theo provider.
6. Skill/Layer/Engineering Pack chỉ chứa instruction và resource. Command, gate, scaffold hay script
   thực thi phải là executable definition/executor versioned, được policy cấp quyền tường minh.
7. Alpha là single-user local, lưu SQLite và evidence local disk 7 ngày, không mã hóa at-rest; secret
   vẫn phải redact trước persist. Beta là deployment tập trung riêng với PostgreSQL, login/RBAC,
   tổ chức/team và server worker. Alpha và beta **không đồng bộ dữ liệu** với nhau.
8. UI cuối cùng cần Kanban, task detail/timeline, workflow/block đang chạy hoặc bị block, quản lý
   Skill/Layer/Agent và chat theo task. UI chỉ gọi application API, không spawn Git/CLI hay ghi state
   runtime trực tiếp.

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
| Evidence | Artifact immutable, redacted, hash-verified chứng minh outcome/gate. |

## 4. Trạng thái lộ trình hiện tại

| Mục | Trạng thái | Quy tắc |
|---|---|---|
| 1. Domain Project/Repository/TaskFamily/WorkspaceSet | Hoàn tất tài liệu baseline | Không đổi semantics nếu không có ADR mới. |
| 2. Quyết định kiến trúc | ACCEPTED | ADR-001…010 là baseline. |
| 3. Go core architecture/spec | Hoàn tất specification baseline | Code phải bám spec hoặc tạo ADR superseding. |
| 4. Go spike | **IN PROGRESS** | Phải đạt SPK-01…SPK-14 trước alpha. |
| 5. Alpha UI/runtime | **CHƯA ĐƯỢC BẮT ĐẦU** | Chỉ bắt đầu sau verdict `GO` của spike. |
| 6. Thiết kế chi tiết version/subtask | **DRAFT CHỜ REVIEW** | Roadmap Alpha và task theo session nằm tại `docs/design/`; chưa thực thi V1 trước verdict `GO` của spike. |

Spike hiện đã có primitive và baseline evidence Windows, nhưng **chưa đạt gate**: còn acceptance
end-to-end từng SPK, một số fault path, Linux semantic suite và `go test -race` trên CI. Không được
diễn đạt local unit pass là `GO` cho alpha.

## 5. Đọc theo thứ tự này

1. Tài liệu này.
2. [Kinh nghiệm từ claude-workflow](danh-gia-claude-workflow.md).
3. [Tổng quan harness engineering](harness-engineering/00-tong-quan.md) và lecture liên quan.
4. [Mô hình Project–Repository–WorkspaceSet](architecture/01-project-repository-workspace-model.md).
5. [Architecture decisions](architecture/02-architecture-decisions.md).
6. [System architecture](architecture/03-system-architecture.md) và [Go core spec](architecture/04-go-core-spec.md).
7. [Roadmap Alpha](design/00-roadmap.md) và [thiết kế hệ thống Alpha](design/01-system-design.md).
8. [Go spike plan](spikes/01-go-core-spike-plan.md), rồi [spike report](spikes/02-go-core-spike-report.md).
9. Khi thực thi, đọc đúng file version trong `docs/design/` chứa Task ID được giao.

Khi bắt đầu code, đọc spike report trước để biết gate nào thiếu; không suy trạng thái từ tên test hay
code hiện có. Khi cần quyết định mới, kiểm ADR trước; thay đổi semantics phải bổ sung ADR superseding.

## 6. Cách chạy spike hiện có

Từ repository root, dùng Go toolchain đã cài:

```text
go test ./...
go vet ./...
go run ./cmd/agentkit-spike acceptance --offline --evidence-dir docs/spikes/evidence
go run ./cmd/agentkit-spike evidence verify --evidence-dir docs/spikes/evidence --suite <suite-id>
```

`acceptance --offline` hiện tạo evidence cho baseline test suite, **không** tự động biến mọi SPK thành
PASS. Evidence generated nằm dưới `docs/spikes/evidence/`, bị Git ignore và retention là 7 ngày.
Linux/race là CI gate bắt buộc, không được bỏ qua chỉ vì local Go bundle không hỗ trợ race detector.

## 7. Definition of done cho mục 4

Chỉ ghi `GO` khi SPK-01…SPK-14 đều pass offline trên Windows và Linux, có evidence verify được,
không flaky qua 10 suite runs và `-race` chạy trên CI hỗ trợ. Nếu chưa đạt:

- **REWORK:** invariant/primitive bị chứng minh sai hoặc flaky; sửa hẹp, lưu finding, chạy lại SPK
  ảnh hưởng và regression. Không hạ gate.
- **CHƯA ĐỦ EVIDENCE:** Linux/race/acceptance chưa chạy; không kết luận kiến trúc sai nhưng cũng không
  làm mục 5.
- **STOP:** evidence chứng minh một lựa chọn lõi không thể giữ invariant; ghi phương án thay thế bằng
  ADR, không suy từ cảm nhận hay số dòng code.

## 8. Quy tắc cập nhật tài liệu

- Quyết định semantics: cập nhật ADR và tài liệu này.
- Thay đổi tiến độ/evidence/gate: cập nhật spike report và tài liệu này nếu milestone đổi.
- Thêm thuật ngữ: cập nhật glossary ở đây trước khi dùng lẫn lộn ở docs khác.
- Không commit/push nếu user chưa yêu cầu. Thiết kế mục 6 đã được user cho phép; execution vẫn phải
  tuân dependency/gate trong `docs/design/00-roadmap.md` và mỗi session chỉ làm đúng một Task ID.
