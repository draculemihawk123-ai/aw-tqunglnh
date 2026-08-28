# Roadmap phát triển Agent Kit Alpha

> Trạng thái: DRAFT — chờ product owner review.
>
> Cập nhật: 2026-08-28.
>
> Phạm vi: toàn bộ công việc từ trạng thái Go spike hiện tại đến Alpha usable; không thiết kế hoặc
> triển khai Beta.

## 1. Authority và cách đọc

Thứ tự authority:

1. ADR đã accepted trong `docs/architecture/02-architecture-decisions.md`.
2. Go core spec trong `docs/architecture/04-go-core-spec.md`.
3. Thiết kế hệ thống Alpha trong `docs/design/01-system-design.md`.
4. File version trong thư mục này.
5. Code và test hiện có.

Nếu hai nguồn cùng cấp mâu thuẫn, dừng task và hỏi product owner. Không chọn bên thắng bằng suy đoán.
`00-luong.md` chỉ là luồng ví dụ; nó không định nghĩa state, persistence hay workflow cố định cho
Agent Kit.

Đọc theo thứ tự:

1. `docs/00-start-here.md`.
2. File này.
3. `docs/design/01-system-design.md`.
4. File version chứa task được giao.
5. Các source/spec được task đó liệt kê.

## 2. Định nghĩa Alpha hoàn thành

Alpha là ứng dụng local single-user có thể dùng để:

1. Đăng ký Project có một hoặc nhiều Git Repository local.
2. Author, validate và publish workflow declarative thành version bất biến.
3. Quản lý version của Block, Skill, Layer, Engineering Pack, Agent Profile, command, gate và policy
   cần cho workflow.
4. Tạo root/child WorkItem với repository/path scope rõ; root có WorkspaceSet riêng và child reuse
   workspace của TaskFamily.
5. Chạy workflow bằng durable job/embedded worker qua Claude CLI hoặc Codex CLI adapter trung lập.
6. Chạy command và machine gate đã publish, có permission, timeout và evidence contract.
7. Resume sau process crash từ SQLite, ContextSnapshot và checkpoint; không phụ thuộc provider
   session.
8. Chỉ hoàn thành WorkItem khi Completion Policy, evidence, approval và exact RevisionSet đạt.
9. Dùng local web UI cho Kanban, task detail, graph/timeline, workspace, evidence, chat và definition
   management.
10. Chạy cùng semantic suite trên Windows và Linux; đóng gói được bản Alpha có hướng dẫn vận hành.

Alpha không cần visual graph editor. Người dùng author workflow dưới dạng YAML/JSON qua file, CLI,
HTTP API hoặc text editor trong UI; tất cả đi qua cùng compiler/publish contract.

## 3. Nguyên tắc chia task

- Một session chỉ thực hiện đúng một Task ID.
- Mục tiêu thời gian thông thường là 30–60 phút; hard limit là 120 phút.
- Task phải tạo một thay đổi có thể verify độc lập. Không chia chỉ để tạo file/field nếu phần đó chưa
  có contract test hoặc behavior quan sát được.
- Nếu task vượt 120 phút, session chỉ được phép dừng và đề xuất cách chia; không tự làm một phần rồi
  gọi là hoàn thành.
- Task sau không bắt đầu nếu dependency hoặc exit gate chưa đạt.
- Không gộp task vì “đang tiện sửa cùng file”.
- Phát hiện ngoài scope được ghi thành proposal cho task/version phù hợp, không sửa kèm.

Mỗi task trong các file version có tối thiểu:

- `Mục tiêu`;
- `Phụ thuộc`;
- `Phạm vi` và phần không được làm;
- `Thực hiện`;
- `Verify` bằng command/test/evidence cụ thể;
- `Hoàn thành khi`.

Các trường rủi ro, fixture, migration, handoff hoặc tài liệu chỉ xuất hiện khi có giá trị cho task.

## 4. Chuỗi version bắt buộc

| Version | File | Kết quả có thể quan sát | Gate sang version sau |
|---|---|---|---|
| V0 — Spike verdict | `02-v0-spike-verdict.md` | SPK-01…14 có evidence Windows/Linux và verdict | Chỉ `GO` mới mở V1 |
| V1 — Alpha foundation | `03-v1-alpha-foundation.md` | Binary local khởi động, config/migration/ports ổn định | Foundation contract pass |
| V2 — Definition plane | `04-v2-definition-plane.md` | Author/validate/publish mọi definition cần thiết | Publish/immutability suite pass |
| V3 — Project & workspace | `05-v3-project-workspace.md` | Project đa repo, WorkItem family và WorkspaceSet vận hành | Workspace/scope/recovery suite pass |
| V4 — Runtime engine | `06-v4-runtime-engine.md` | Workflow graph chạy bền vững qua node/retry/rework/fork/join | Deterministic orchestration suite pass |
| V5 — Execution & evidence | `07-v5-execution-evidence.md` | Agent/command/gate/context/evidence chạy end-to-end | Evidence-bound completion suite pass |
| V6 — API & projections | `08-v6-api-projections.md` | Local HTTP API/SSE và read models đầy đủ | API contract + rebuild projection pass |
| V7 — Alpha UI | `09-v7-alpha-ui.md` | Người dùng vận hành toàn bộ core từ local web UI | UI journeys + accessibility smoke pass |
| V8 — Alpha hardening | `10-v8-alpha-hardening.md` | Recovery/security/packaging/docs đạt release gate | Alpha verdict |

Không chạy song song hai version có dependency nối tiếp. Bên trong một version, mặc định vẫn làm theo
thứ tự Task ID. Hai task chỉ được chạy song song khi file version ghi rõ `Có thể song song` và chúng
không sửa chung contract/schema/package.

## 5. Critical path

```text
V0 GO
  -> V1 application/persistence foundation
    -> V2 immutable definitions
      -> V3 Project/TaskFamily/WorkspaceSet
        -> V4 durable workflow engine
          -> V5 real execution + evidence authority
            -> V6 stable API + projections
              -> V7 usable UI
                -> V8 release hardening
```

Lý do thứ tự:

- V0 chứng minh primitive; không đầu tư Alpha nếu primitive sai.
- Definition phải publish trước khi runtime có object bất biến để pin.
- Project/workspace phải tồn tại trước mutating execution.
- Runtime engine được kiểm bằng fake executor trước khi tích hợp CLI thật, giảm số failure boundary
  trong một task.
- API chỉ mở sau khi application command/query semantics ổn định.
- UI phụ thuộc API, không gọi trực tiếp SQLite/Git/CLI.
- Packaging chỉ có ý nghĩa sau khi full local journey chạy được.

## 6. Gate chung cho mọi version

Một version chỉ được đóng khi:

1. Tất cả task bắt buộc trong file version đạt `Hoàn thành khi`.
2. `go test ./...` và `go vet ./...` pass trên platform đang làm việc.
3. Gate platform/CI riêng của version pass nếu được yêu cầu.
4. Migration cũ mở được và migration mới idempotent nếu schema thay đổi.
5. Không vi phạm dependency `domain <- app <- adapters/delivery <- cmd`.
6. Evidence/log/error mới không chứa secret fixture.
7. Tài liệu và fixture liên quan được cập nhật trong đúng task.
8. Không có acceptance criterion bắt buộc bị đổi thành “deferred” chỉ để đóng version.

Nếu local environment không chạy được một gate bắt buộc, kết quả là `CHƯA ĐỦ EVIDENCE`, không phải
PASS. Gate đó phải chạy ở CI hoặc môi trường hỗ trợ trước khi đóng version.

## 7. Chính sách schema và compatibility

- SQLite là persistence duy nhất của Alpha.
- Application/domain chỉ phụ thuộc repository/UoW/query ports; không nhận `*sql.Tx`, SQL row hoặc
  SQLite-specific error.
- Mỗi thay đổi schema là migration mới có checksum; không sửa migration đã phát hành.
- Thiết kế giữ ID do application tạo, UTC timestamp, enum codec, JSON codec, expected version và
  typed error ổn định để Beta có thể thêm adapter khác mà không đổi semantics.
- Không viết migration, query hay contract test PostgreSQL trong roadmap Alpha.

## 8. Chính sách UI framework

Không chọn framework trước verdict V0 `GO`. Task đầu V7 tạo decision record dựa trên:

- tốc độ build Alpha;
- component/accessibility library;
- TypeScript/tooling và khả năng bảo trì;
- graph/timeline rendering;
- bundle/build integration với Go binary;
- testability.

`01-system-design.md` chỉ khóa screen, interaction, API và state ownership; không khóa React/Angular.

## 9. Ngoài phạm vi toàn roadmap

- PostgreSQL, object storage và server worker pool.
- Login, organization, team, RBAC và multi-tenancy.
- Đồng bộ Alpha–Beta.
- Kubernetes hoặc distributed scheduler production-grade.
- Visual workflow graph editor.
- Runtime migration giữa hai WorkflowVersion.
- Path-scoped concurrent writers trên cùng RepositoryWorkspace.
- Go dynamic plugins.
- Auto push, pull request, merge hoặc force-push.
- SERVICE_CALL, SUBFLOW và dynamic child-workflow semantics nếu chưa có ADR riêng.

Các boundary cho những capability này được giữ qua port/version/identity, nhưng không tạo task triển
khai giả trong Alpha.

## 10. Cách thực thi một session

1. Chọn đúng một Task ID chưa bị dependency chặn.
2. Đọc `docs/00-start-here.md`, file này, system design, file version và source được task chỉ định.
3. Nói lại mục tiêu, liệt kê câu hỏi hệ trọng. Có câu hỏi chưa trả lời thì dừng trước khi sửa.
4. Kiểm baseline của phạm vi task.
5. Chỉ sửa phạm vi được ghi.
6. Chạy toàn bộ lệnh `Verify` của task và lưu evidence khi task yêu cầu.
7. Đối chiếu `Hoàn thành khi`; thiếu một mục thì không báo DONE.
8. Handoff gồm file đổi, output verify thật, blocker và Task ID kế tiếp có thể bắt đầu.

Roadmap không giữ trạng thái task song song với runtime database. Trong giai đoạn build chính Agent
Kit, trạng thái thực thi session được bàn giao bằng output/evidence và Git diff; sau khi WorkItem
runtime usable, chính Agent Kit sẽ là authority cho execution state.
