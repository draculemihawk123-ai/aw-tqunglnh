# Roadmap phát triển Agent Kit Alpha

> Trạng thái: BASELINE ĐÃ QUYẾT ĐỊNH — product owner đã ủy quyền chốt.
>
> Cập nhật: 2026-08-31.
>
> Phạm vi: toàn bộ công việc từ trạng thái Go spike hiện tại đến Alpha usable; không thiết kế hoặc
> triển khai Beta.

## 1. Authority và cách đọc

Thứ tự authority:

1. ADR-001 đến ADR-025 đã accepted trong `docs/architecture/02-architecture-decisions.md`.
2. Go core spec trong `docs/architecture/04-go-core-spec.md`.
3. Thiết kế hệ thống Alpha trong `docs/design/01-system-design.md`.
4. File version trong thư mục này.
5. Code và test hiện có.

Nếu hai nguồn cùng cấp mâu thuẫn, dừng task và hỏi product owner. Không chọn bên thắng bằng suy đoán.
`00-luong.md` chỉ là luồng ví dụ; nó không định nghĩa state, persistence hay workflow cố định cho
Agent Kit.

Các quyết định đã khóa cho Alpha: compiled snapshot và AdapterBuildVersion được pin; scope expansion
dùng amendment + activation mới; execution khai đúng isolation profile; mỗi Attempt mặc định chỉ ghi
một repository; checker read-only; ReleaseSet/local commit có trong Alpha nhưng remote Git mutation
không có; projection dùng JournalPosition; local API có Host/Origin/session-token protection; retention
theo class; UI có source/diff/log read-only và không có interactive terminal.

Bổ sung sau review thiết kế ngày 2026-08-31 (ADR-020…025): Attempt có terminal `BLOCKED` tách khỏi
`FAILED`; cancel đi qua `CANCELLING` và quiesce thật; CompletionPolicy có bốn outcome với transition cố
định; AdapterBuildVersion là operational registry có surface probe/register riêng; isolation không
auto-downgrade; acceptance criteria được phân loại phase trước V1.

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
7. Khôi phục sau process crash từ SQLite, ContextSnapshot và checkpoint bằng Attempt mới + `Start`;
   không gọi provider-native `Resume`.
8. Chỉ hoàn thành WorkflowRun và WorkItem khi Completion Policy, evidence, approval và exact
   RevisionSet/ReleaseSet đạt.
9. Dùng local web UI cho Kanban, task detail, graph/timeline, workspace, evidence, chat và definition
   management.
10. Chạy cùng semantic suite trên Windows và Linux; đóng gói được bản Alpha có hướng dẫn vận hành.
11. Tạo/seal/abandon ReleaseSet và local commit qua typed operation; từ chối push/PR/merge/force-push.
12. Vận hành local API loopback an toàn và hiển thị trung thực execution isolation profile.
13. Probe và đăng ký AdapterBuildVersion khi provider CLI thay đổi, rồi republish để pin build mới.
14. Hủy một run đang chạy qua `CANCELLING` với quiesce thật, không để lại outcome bị gán nhãn sai.

Alpha không cần visual graph editor. Người dùng author workflow dưới dạng YAML/JSON qua file, CLI,
HTTP API hoặc text editor trong UI; tất cả đi qua cùng compiler/publish contract.

## 3. Nguyên tắc chia task

- Một session chỉ thực hiện đúng một Task ID.
- Một Task ID có thể tiếp tục qua nhiều session; mỗi session handoff cùng Task ID cho tới khi đạt
  `Hoàn thành khi`, không tự đánh dấu DONE vì hết phiên.
- Mục tiêu thời gian thông thường là 30–60 phút. Đây là planning heuristic, **không** phải completion
  criterion: một task được coi là đúng kích thước khi nó tương ứng với một contract suite hoặc một
  failure-boundary suite verify được độc lập.
- Task phải tạo một thay đổi có thể verify độc lập. Không chia chỉ để tạo file/field nếu phần đó chưa
  có contract test hoặc behavior quan sát được.
- Task sau không bắt đầu nếu dependency hoặc exit gate chưa đạt.
- Không gộp task vì “đang tiện sửa cùng file”.
- Phát hiện ngoài scope được ghi thành proposal cho task/version phù hợp, không sửa kèm.

Mọi task kế thừa **phạm vi mặc định**: chỉ được sửa code/test/migration/tài liệu trực tiếp cần cho
`Mục tiêu`, các bước `Thực hiện` và version hiện tại; mọi capability/version khác là ngoài phạm vi.
Task có side effect phá hủy, security boundary, schema migration hoặc sửa xuyên package phải ghi thêm
`Phạm vi`/`Không làm` tường minh. Quy tắc này áp dụng cho các task đang không lặp lại field `Phạm vi`.

Mỗi task trong các file version có tối thiểu:

- `Mục tiêu`;
- `Phụ thuộc`;
- `Thực hiện`;
- `Verify` bằng command/test/evidence cụ thể;
- `Hoàn thành khi`;
- `Nguồn` liệt kê criterion mà task này chịu trách nhiệm, theo grammar `SourceRef` ổn định để checker
  phân giải được:

| Prefix | Trỏ tới |
|---|---|
| `ADR-NNN` | Một architecture decision |
| `AK-ARCH-NNN[X]` | Một architecture acceptance criterion |
| `HE-NN-{M,S}NN` | Một harness-engineering criterion |
| `GC-INV-NN` | Một invariant trong Go core spec §5, ID gán tại chỗ |
| `GC-ACC-NN` | Một mục acceptance gate trong Go core spec §22, ID gán tại chỗ |
| `GC-DS-NN` | Một downstream Alpha target trong Go core spec §22.1, ID gán tại chỗ |
| `ROADMAP-§<S>` | Một quy tắc process của roadmap; `<S>` là số mục kèm hậu tố chữ tùy chọn, ví dụ `§3`, `§5B` |

Không phải mọi `SourceRef` đều là acceptance criterion — `ADR` và `ROADMAP` là nguồn quyết định, còn
`AK-ARCH`/`HE`/`GC-*` mới là criterion mang nhãn phase. Checker phải phân biệt hai loại này thay vì coi
tất cả là criterion, và phải **tokenize toàn bộ** trường `Nguồn` rồi reject nếu bất kỳ token nào sai —
kiểm "có ít nhất một prefix hợp lệ" là không đủ và đã từng cho lọt lỗi thật.

Quy tắc `Nguồn` áp cho V1…V8. Task V0 được miễn trường này vì chúng đã mang traceability riêng bằng SPK
ID và phải chạy xong trước khi V1-00A tồn tại; đây là miễn **hình thức**, không phải miễn coverage:
V1-00B phải gộp mapping SPK → criterion vào cùng một index, nên V0 vẫn nằm trong coverage checker. Từ V1
trở đi, task mới không được merge nếu thiếu `Nguồn`.

Coverage map là artifact machine-checkable: mọi criterion `ALPHA_MUST` phải được ít nhất một Task ID
nhận trong trường `Nguồn`, và CI fail khi có criterion `ALPHA_MUST` không có owner. V8-11 chỉ tổng hợp
evidence cuối, không khám phá ownership lần đầu.

Các trường rủi ro, fixture, migration, handoff hoặc tài liệu chỉ xuất hiện khi có giá trị cho task.
Task tổng hợp verdict được phép chạy khi execution gate trước đó fail, miễn các task đánh giá phụ thuộc
đã tạo đủ artifact/trạng thái. “Task đánh giá đã chạy xong” khác với “gate đã PASS”; verdict chuẩn là
`GO|REWORK|STOP|CHƯA ĐỦ EVIDENCE` ở V0 và `ALPHA_READY|REWORK|STOP|CHƯA ĐỦ EVIDENCE` ở V8.

## 4. Chuỗi version bắt buộc

| Version | File | Kết quả có thể quan sát | Gate sang version sau |
|---|---|---|---|
| V0 — Spike verdict | `02-v0-spike-verdict.md` | SPK-01…14 có evidence Windows/Linux và verdict | Chỉ `GO` mới mở V1 |
| V1 — Alpha foundation | `03-v1-alpha-foundation.md` | Binary local khởi động, config/migration/ports ổn định | Foundation contract pass |
| V2 — Definition plane | `04-v2-definition-plane.md` | Author/validate/publish mọi definition cần thiết | Publish/immutability suite pass |
| V3 — Project & workspace | `05-v3-project-workspace.md` | Project đa repo, WorkItem family và WorkspaceSet vận hành | Workspace/scope/recovery suite pass |
| V4 — Runtime engine | `06-v4-runtime-engine.md` | Workflow graph chạy bền vững qua node/retry/rework/fork/join | Deterministic orchestration suite pass |
| V5 — Execution & evidence | `07-v5-execution-evidence.md` | Agent/command/gate/context/evidence/ReleaseSet chạy end-to-end | Evidence-bound completion suite pass |
| V6 — API & projections | `08-v6-api-projections.md` | Local HTTP API/SSE và read models đầy đủ | API contract + rebuild projection pass |
| V7 — Alpha UI | `09-v7-alpha-ui.md` | Người dùng vận hành toàn bộ core từ local web UI | UI journeys + accessibility smoke pass |
| V8 — Alpha hardening | `10-v8-alpha-hardening.md` | Recovery/security/packaging/docs đạt release gate | Alpha verdict |

Không chạy song song hai version có dependency nối tiếp. Bên trong một version, mặc định vẫn làm theo
thứ tự Task ID. Hai task chỉ được chạy song song khi file version ghi rõ `Có thể song song` và chúng
không sửa chung contract/schema/package.

Parallel group được đánh dấu **sau khi** dependency contract của version đó đã freeze, không đánh dấu
trước. Cơ chế này phải thực sự được dùng ở nơi an toàn thay vì tồn tại trên giấy: mặc định tuần tự chỉ
áp cho task dùng chung schema/contract.

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

## 5A. Phân loại phase của acceptance criteria

Theo ADR-024, mọi criterion được gán nhãn phase **trước** khi V1 bắt đầu, không phải khi tổng hợp
verdict. Nhãn hợp lệ: `ALPHA_MUST`, `BETA_ADAPTER_GATE`, `BETA_PARITY_GATE`, `CROSS_PHASE_GUARD` và
`NOT_APPLICABLE` kèm authority reason.

Bảng phân loại chuẩn nằm tại `docs/architecture/03-system-architecture.md` §17. Mặc định của AK-ARCH là
`ALPHA_MUST`; ngoại lệ đã chốt là AK-ARCH-019 (`BETA_ADAPTER_GATE`), AK-ARCH-026 (`BETA_PARITY_GATE`)
và AK-ARCH-028 (`CROSS_PHASE_GUARD`).

HE criteria và Go core MUST được phân loại theo cùng cơ chế trong **V1-00A…V1-00C**, là **pre-V1 gate**
chứ không phải task triển khai V1 thông thường:

- V1-00A gán stable ID và nhãn phase; V1-00B backfill `Nguồn` và map SPK; V1-00C viết checker.
- Chúng chạy ngay sau verdict `GO` của V0 và trước mọi task code Alpha.
- V1-01 phụ thuộc V1-00C; không task nào khác ngoài chính ba task này được bắt đầu trước khi V1-00C pass.
- Chỉ được tuyên bố bộ thiết kế “execution-ready” sau khi coverage checker chạy thành công **với debt
  bằng 0** — không tuyên bố dựa trên việc tài liệu đã được cập nhật, và không hard-code con số debt vào
  tài liệu vì nó lệch ngay khi task đầu tiên nhận `Nguồn`.

V8 không được phân loại lại criteria và không được dùng `deferred` cho một `ALPHA_MUST`.

## 5B. Trạng thái của spike sau V0

Quyết định: bộ SPK tiếp tục là **live regression gate** sau V0, không phải evidence đóng băng.

- Evidence bundle sinh ra ở V0 là lịch sử bất biến và không được sửa lại.
- Harness/source SPK tiếp tục build và chạy qua toàn bộ refactor V1.
- V1-12 phải chạy lại full offline SPK-01…14 và coi failure là blocker của V1.
- Sau V1, mỗi SPK được map sang một successor Alpha contract/fault test trong coverage map.
- `agentkit-spike` chỉ được retire bằng một task/decision tường minh, sau khi successor coverage đã
  pass — không retire ngầm vì code cũ vướng refactor.

## 6. Gate chung cho mọi version

Một version chỉ được đóng khi:

1. Tất cả task implementation bắt buộc đạt `Hoàn thành khi`; task assessment/verdict phải chạy và
   ghi kết quả dù gate fail.
2. `go test ./...` và `go vet ./...` pass trên platform đang làm việc.
3. Gate platform/CI riêng của version pass nếu được yêu cầu.
4. Migration cũ mở được và migration mới idempotent nếu schema thay đổi.
5. Không vi phạm dependency `domain <- app <- adapters/delivery <- cmd`.
6. Evidence/log/error mới không chứa secret fixture.
7. Tài liệu và fixture liên quan được cập nhật trong đúng task.
8. Không có acceptance criterion bắt buộc bị đổi thành “deferred” chỉ để đóng version.
9. Mọi criterion `ALPHA_MUST` thuộc phạm vi version đã có owner trong trường `Nguồn` của một Task ID.
10. Full offline SPK-01…14 vẫn pass (từ V1 trở đi), cho tới khi spike được retire tường minh.

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
- Interactive browser terminal hoặc terminal session capability.
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
