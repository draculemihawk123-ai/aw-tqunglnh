# V6-00 — UX artifact framework-neutral (13-screen inventory)

> Task ID: **V6-00**. Nguồn: ADR-010, ADR-018, ADR-028. Phụ thuộc: V5 (đã đóng).
>
> Trích nguyên văn spec task từ `docs/design/08-v6-api-projections.md` §"V6-00 — UX artifact
> framework-neutral" (đã đọc trước khi viết tài liệu này, không suy diễn):
>
> - Mục tiêu: đặc tả hành vi cho 13 screen trước khi freeze API; không chọn UI framework.
> - Phụ thuộc: V5.
> - Phạm vi: wireframe và screen/action/query/state inventory trong `docs/design/`.
> - Không làm: không viết production UI, endpoint hay domain command.
> - Thực hiện: mô tả loading/empty/stale/error/blocked, keyboard/accessibility và Graph/Timeline
>   fork/join/rework/checkpoint. Mỗi action/query ghi proposed operationId, `aw` leaf, public
>   operation hoặc một gap có owner Task ID cụ thể; không tự tạo generic status setter.
> - Verify: checker bảo đảm đủ 13 screen, mọi ô có mapping hoặc owner, không có gap chưa gán và
>   không trùng authority. Zero gap chỉ là precondition của V6-12, không phải precondition để
>   V6-00 hoàn thành.
> - Hoàn thành khi: inventory đủ để endpoint task biết dữ liệu/interaction cần làm mà không hỏi
>   lại product decision; mọi gap đều có Task ID chịu trách nhiệm.
>
> Tài liệu này KHÔNG chọn UI framework (ADR-010 vẫn hoãn quyết định đó tới V7-01) và KHÔNG viết
> production UI/endpoint/domain command (đúng "Không làm" ở trên). Nó chỉ là artifact hành vi:
> screen, state, action/query và mapping/gap để các task V6-0x (endpoint) và V7-0x (UI) không
> phải tự quyết product decision khi implement.

## 0. 13 screen đến từ đâu

`docs/design/09-v7-alpha-ui.md` (V7 — Local web UI Alpha) là phase UI thật sự tiêu thụ artifact
này. Loại bỏ các task hạ tầng không phải screen (V7-01 chọn framework, V7-02 workspace/API
client, V7-03 design tokens, V7-04 app shell/SSE, V7-17 full-journey gate), phần còn lại
V7-05…V7-16 cộng V7-13A là đúng 13 task, mỗi task tương ứng một screen — khớp chính xác con số "13
screen" mà V6-00 tự đặt ra. Đây không phải suy diễn: đó là toàn bộ danh sách screen thật mà
`09-v7-alpha-ui.md` liệt kê, không thêm/bớt.

| # | Screen | Task V7 sở hữu UI | Nhóm task V6 sở hữu backend |
|---|---|---|---|
| 1 | Doctor (first-run/ongoing health) | V7-05 | V6-10A, V6-10I, V6-10J |
| 2 | Project / Repository / Component | V7-06 | V6-03, V6-03A |
| 3 | Definition catalog & version detail | V7-07 | V6-05 |
| 4 | Definition editor (author/validate/publish) | V7-08 | V6-05 |
| 5 | Kanban board | V7-09 | V6-10, V6-04A, V6-08A |
| 6 | Create WorkItem (root/child) | V7-10 | V6-04 |
| 7 | Task/WorkItem detail & actions | V7-11 | V6-04, V6-06, V6-06A, V6-06D |
| 8 | Run graph & timeline (+ diagnostics/recovery) | V7-12 | V6-06B, V6-06C, V6-06D |
| 9 | Workspace / source-diff-log viewer | V7-13 | V6-10B, V6-10C, V6-10D |
| 10 | ReleaseSet & local commit | V7-13A | V6-10E, V6-10F |
| 11 | Evidence / artifact viewer | V7-14 | V6-07B |
| 12 | Task chat | V7-15 | V6-07, V6-07A |
| 13 | Settings & run diagnostics | V7-16 | V6-10G, V6-10H, V6-06C |

Không có screen thứ 14. `09-v7-alpha-ui.md` không đặt tên một screen "Projection/rebuild status"
riêng — xem §5 "Cross-cutting concerns" để biết vì sao đó là một quyết định đã có (không phải một
gap bị bỏ sót).

## 1. Cách đọc tài liệu

Mỗi screen có bốn phần cố định: **Purpose**, **States** (loading/empty/stale/error/blocked —
"N/A" là một giá trị hợp lệ khi trạng thái đó không áp dụng, không phải ô bỏ trống), **Keyboard &
accessibility**, và bảng **Actions/Queries**.

Cột bảng Actions/Queries:

- **Kind** — `query` (đọc, không CommandEnvelope) hoặc `command` (mutation, luôn qua
  `CommandEnvelope` theo contract chung §1 của `08-v6-api-projections.md`).
- **Proposed operationId** — tên đề xuất cho OpenAPI operationId; KHÔNG phải giá trị đã freeze.
  V6-12 là nơi duy nhất compose/freeze operationId thật; task này chỉ đề xuất để endpoint task có
  điểm khởi đầu, không phải tự nghĩ tên khi bắt tay code.
- **Proposed `aw` leaf** — hình dạng lệnh CLI đề xuất theo grammar `aw <resource> <action>`
  (ADR-028); V6-15B…V6-15O có quyền điều chỉnh chữ, miễn giữ đúng **invocation shape** (resource +
  scope discriminator) đã ghi ở đây.
- **Public application command/query** — tên type/command thật. Khi type đã tồn tại thật trong
  code (`internal/app/...`), ghi rõ package + tên type đã grep được, đánh dấu **[ĐÃ CÓ]**; khi
  chưa tồn tại (task backend chưa chạy), ghi tên đề xuất — ưu tiên tuyệt đối tên đã bị khóa cứng
  trong chính `08-v6-api-projections.md` hoặc ADR-028 (ví dụ `MarkWorkItemReady`, `GetSafeSettings`)
  thay vì tự bịa, đánh dấu **[CHƯA CÓ]**.
- **Owner Task ID** — Task ID trong `08-v6-api-projections.md` chịu trách nhiệm xây/expose đúng
  action/query đó. Cột này không bao giờ để trống; đây chính là cơ chế "gap có owner" mà spec yêu
  cầu — mọi action/query trong tài liệu này đều map được vào một Task ID đã tồn tại, không có action
  nào cần một Task ID mới bịa ra (xem §6 để xác nhận zero unassigned gap).

Khi hai screen cùng gọi một authority thật (ví dụ `CancelRun` xuất hiện cả ở Task detail và ở
Graph/Timeline), bảng ghi rõ dòng "authority dùng chung với Screen #N — không phải hai owner khác
nhau" ngay dưới hàng đó, để checker không hiểu nhầm là trùng/xung đột authority (spec cấm "trùng
authority" nghĩa là hai owner tranh nhau MỘT mutation, không cấm một mutation có hai bề mặt UI).

**Không có action nào trong tài liệu này là generic status setter.** Toàn bộ transition trạng thái
WorkItem/Run đi qua command hẹp có tên riêng (`MarkWorkItemReady`, `StartWorkflowRun`, `CancelRun`,
`CancelWorkItem`, `ResolveWorkItemBlocker`, `ResolveApproval`, các lệnh scope-expansion, `PASS/
REWORK/BLOCK/FAIL` của CompletionPolicy) — không có `set-status`/`TransitionWorkItemStatus`/
`UpdateRunState` nào được đề xuất, kể cả dưới tên khác.

## 2. Screen 1 — Doctor (first-run / ongoing installation health)

**Purpose.** Cho operator biết DB/workspace/artifact/Git/provider/adapter nào ready hay blocked
ngay từ lần chạy đầu, và cung cấp action đăng ký adapter build mới mà không cần mở terminal
(ADR-022, V7-05).

**States.**
- Loading: skeleton card cho từng component (DB, workspace root, artifact root, Git, provider,
  adapter build); không render icon trạng thái tạm thời (không đoán ACTIVE trong lúc chờ).
- Empty: N/A cho bản thân Doctor report (luôn có ít nhất trạng thái của các component cố định);
  riêng danh sách adapter build có empty state "Chưa có adapter build nào được đăng ký" với action
  probe nổi bật.
- Stale: Doctor là authoritative query chạy lại mỗi lần mở màn hình, không phải projection nên
  không có khái niệm generation lag; UI hiển thị "Last checked <timestamp>" và nút "Recheck" gọi
  lại đúng query đó thay vì cache client-side.
- Error: lỗi mạng/authorization hiển thị banner toàn màn hình riêng biệt với remediation card của
  từng component (không trộn hai loại lỗi).
- Blocked: mỗi component `BLOCKED` có card riêng với lý do typed + action remediation cụ thể (ví
  dụ card Repository BLOCKED chỉ là deep-link sang Screen 2, không tự làm retry tại đây — xem ghi
  chú "không duplicate Doctor" ở Screen 2).

**Keyboard & accessibility.** Danh sách card dùng roving tabindex; mỗi remediation action là một
`<button>`/`<a>` thật, tới được bằng Tab, không chỉ click vùng card; trạng thái truyền bằng
icon + text (không chỉ màu, theo token V7-03); sau khi "Recheck" trả về, một live region
`aria-live="polite"` công bố kết quả mới.

**Actions/Queries.**

| # | Kind | UI action/query | Proposed operationId | Proposed `aw` leaf | Public application command/query | Owner Task ID |
|---|---|---|---|---|---|---|
| 1 | query | Tải Doctor report | `getInstallationDoctorReport` | `aw doctor` | `GetInstallationDoctorReport` [CHƯA CÓ] — kế thừa shape từ `internal/app/doctor.Report`/`doctor.CheckResult` đã có (V0), bọc lại thành installation-scoped query | V6-10A |
| 2 | query | Danh sách adapter build đã đăng ký | `listAdapterBuilds` | `aw adapter list` | `ListAdapterBuilds` [CHƯA CÓ] | V6-10J (đọc dữ liệu do V6-10I hardening tạo) |
| 3 | command | Probe adapter build mới | `probeAdapterBuild` | `aw adapter probe` | `ProbeAdapterBuild` [CHƯA CÓ] — tên khóa cứng ở ADR-028 §30, hiện có tiền thân nội bộ `adapterbuild.ProbeRequest`/`ProbeResult` [ĐÃ CÓ, chưa installation CommandEnvelope] | V6-10I (hardening) → V6-10J (route) |
| 4 | command | Đăng ký (confirm) adapter build sau probe | `registerAdapterBuild` | `aw adapter register` | `RegisterAdapterBuild` [CHƯA CÓ] — tên khóa cứng ở ADR-028 §30, tiền thân `adapterbuild.RegisterRequest`/`RegisterResult` [ĐÃ CÓ, chưa installation CommandEnvelope] | V6-10I (hardening) → V6-10J (route) |
| 5 | — | Link "Fix repository" từ card BLOCKED | *(không phải action riêng)* | *(điều hướng sang Screen 2)* | dùng lại `RetryRepositoryProbe` của Screen 2 hàng 8 | V6-03A — **authority dùng chung với Screen 2, Doctor không tự làm retry** (đúng "Không làm" của V6-03A: "không duplicate Doctor") |

## 3. Screen 2 — Project / Repository / Component management

**Purpose.** Tạo Project, đăng ký/probe repository, xem component đã discover và gán Engineering
Pack đúng version, không suy repo từ path/name (V7-06, ADR-019).

**States.**
- Loading: skeleton list Project + skeleton table Repository.
- Empty: "Chưa có Project nào" với CTA tạo Project đầu tiên; "Chưa có repository nào" riêng trong
  từng Project.
- Stale: N/A cho danh sách/detail Project và Repository (authoritative, không phải projection theo
  đúng Phạm vi V6-03/V6-03A); trạng thái onboarding `REGISTERING`/`PROBING` là async polling
  state, không phải staleness.
- Error: lỗi validate khi tạo/đăng ký hiển thị inline theo field; lỗi mạng có banner + retry.
- Blocked: card Repository `BLOCKED` có action "Retry probe" và link "Xem lịch sử probe".

**Keyboard & accessibility.** Bảng repository có hàng focusable; form đăng ký dùng
label/error association chuẩn; trạng thái async polling công bố qua `aria-live="polite"` khi
chuyển `REGISTERING → PROBING → ACTIVE/BLOCKED`.

**Actions/Queries.**

| # | Kind | UI action/query | Proposed operationId | Proposed `aw` leaf | Public application command/query | Owner Task ID |
|---|---|---|---|---|---|---|
| 1 | query | Danh sách Project | `listProjects` | `aw project list` | `ListProjects` [CHƯA CÓ] — tên khóa cứng ở Mục tiêu V6-03 | V6-03 |
| 2 | command | Tạo Project | `createProject` | `aw project create` | `CreateProject` [ĐÃ CÓ: `internal/app/ports/catalog.go:CreateProjectRequest`, chưa có CommandEnvelope/HTTP/CLI] | V6-03 |
| 3 | query | Chi tiết Project | `getProject` | `aw project show` | `GetProject` [CHƯA CÓ] — tên khóa cứng ở Mục tiêu V6-03 | V6-03 |
| 4 | command | Đăng ký repository | `registerRepository` | `aw repository register` | `RegisterRepository` [ĐÃ CÓ: `internal/app/ports/catalog.go:RegisterRepositoryRequest`] | V6-03A |
| 5 | query | Danh sách repository của Project | `listRepositories` | `aw repository list` | `ListRepositories` [CHƯA CÓ] | V6-03A |
| 6 | query | Chi tiết repository (incl. onboarding state) | `getRepository` | `aw repository show` | `GetRepository` [CHƯA CÓ] | V6-03A |
| 7 | query | Lịch sử probe của repository | `listRepositoryProbeHistory` | `aw repository probe-history` | dựa trên `RecordRepositoryProbeAttemptRequest`/`RepositoryProbeAttempt` [ĐÃ CÓ ở `ports/catalog.go`, chưa có query công khai] | V6-03A |
| 8 | command | Retry probe (chỉ khi `BLOCKED`) | `retryRepositoryProbe` | `aw repository retry-probe` | `RetryRepositoryProbe` [ĐÃ CÓ: `internal/app/ports/catalog.go:RetryRepositoryProbeRequest`] — leaf canonical là `retry-probe` theo đúng chữ trong V6-03A | V6-03A |
| 9 | query | Danh sách component đã discover | `listComponents` | `aw component list` | `ListComponents` [CHƯA CÓ] — chỉ đọc, **không có** `CreateComponent` public dù type nội bộ `ports.CreateComponentRequest` đã tồn tại (ADR-028 cấm expose helper này) | V6-03A |
| 10 | command | Gán Engineering Pack version cho component | `assignComponentPack` | `aw pack-assignment assign` | `AssignComponentPack` [ĐÃ CÓ: `internal/app/ports/catalog.go:AssignComponentPackRequest`, chưa CommandEnvelope/HTTP/CLI] | V6-03A |
| 11 | query | Danh sách pack assignment | `listPackAssignments` | `aw pack-assignment list` | `ListPackAssignments` [CHƯA CÓ] | V6-03A |

## 4. Screen 3 — Definition catalog & version detail

**Purpose.** Duyệt mọi loại definition (global và project-scoped), version, dependency/hash/
compatibility (V7-07, AK-ARCH-001).

**States.**
- Loading: skeleton grid/list catalog.
- Empty: "Chưa có definition loại X" theo từng filter kind/scope.
- Stale: N/A (authoritative theo Phạm vi V6-05, không phải projection).
- Error: chẩn đoán validate có location (line/column khi có) hiển thị inline, không phải toast rời.
- Blocked: N/A — definition không có khái niệm blocked.

**Keyboard & accessibility.** Bộ lọc kind/scope là combobox/listbox chuẩn có điều hướng bàn phím;
bộ chọn version là listbox điều hướng bằng phím mũi tên; diff view có ký hiệu +/- dạng text đi kèm
màu (không chỉ màu).

**Actions/Queries.**

| # | Kind | UI action/query | Proposed operationId | Proposed `aw` leaf | Public application command/query | Owner Task ID |
|---|---|---|---|---|---|---|
| 1 | query | Danh sách definition (global + `/projects/{id}/definitions`) | `listDefinitions` | `aw definition list` | `ListDefinitions` [CHƯA CÓ] | V6-05 |
| 2 | query | Chi tiết một definition (item) | `getDefinition` | `aw definition show` | `GetDefinition` [CHƯA CÓ] | V6-05 |
| 3 | query | Danh sách version của một definition | `listDefinitionVersions` | `aw definition versions` | `ListDefinitionVersions` [CHƯA CÓ] | V6-05 |
| 4 | query | Chi tiết một version (hash/pins) | `getDefinitionVersion` | `aw definition version show` | `GetDefinitionVersion` [CHƯA CÓ] | V6-05 |
| 5 | query | Diff hai version cùng scope | `diffDefinitionVersions` | `aw definition version diff` | `DiffDefinitionVersions` [CHƯA CÓ] — hai operand phải cùng scope theo đúng Verify V6-05 | V6-05 |

## 5. Screen 4 — Definition editor (author / validate / publish)

**Purpose.** Tác giả YAML/JSON, xem diagnostics/diff, publish có confirm — không có visual graph
editor (V7-08).

**States.**
- Loading: skeleton editor shell khi mở definition có sẵn để sửa.
- Empty: editor trống với scaffold template khi tạo definition mới.
- Stale: draft là **format-preserving, local-only** (V7-08 nêu rõ) — không có khái niệm stale phía
  server cho draft; nếu published version đổi trong lúc đang soạn (người khác publish trước), hiển
  thị banner conflict dựa trên `ExpectedVersion`/`If-Match` khi submit publish.
- Error: danh sách diagnostics có location, đồng bộ với gutter marker trong editor.
- Blocked: N/A.

**Keyboard & accessibility.** Phím tắt editor tài liệu rõ ràng (Ctrl/Cmd+S = validate, KHÔNG tự
publish); dialog confirm publish là modal có focus trap, Escape hủy; mỗi dòng diagnostics điều
hướng được bằng bàn phím và nhảy con trỏ tới đúng vị trí.

**Actions/Queries.**

| # | Kind | UI action/query | Proposed operationId | Proposed `aw` leaf | Public application command/query | Owner Task ID |
|---|---|---|---|---|---|---|
| 1 | *(client-local)* | Lưu draft tạm | *(không có endpoint)* | *(không áp dụng)* | Không cần authority server — draft "local-only" theo đúng chữ V7-08; không phải gap, là quyết định thiết kế đã có sẵn trong chính task V7-08 | *(không cần owner — không phải public operation)* |
| 2 | command | Validate draft | `validateDefinitionDraft` | `aw definition validate` | `ValidateDefinitionDraft` [CHƯA CÓ] | V6-05 |
| 3 | command | Tạo definition (persist draft đầu tiên) | `createDefinition` | `aw definition create` | `CreateDefinition` [ĐÃ CÓ: `internal/app/definitions/commands.go:CreateDefinitionRequest`, chưa CommandEnvelope/HTTP/CLI] — route quyết định scope theo ADR-028 §30 (global vs `/projects/{id}/...`), không nhận scope từ payload | V6-05 |
| 4 | command | Publish version | `publishDefinitionVersion` | `aw definition publish` | `PublishDefinitionVersion` [ĐÃ CÓ: `internal/app/definitions/commands.go:PublishDefinitionVersionRequest`] — trả SourceHash + CompiledSnapshotHash + pin chính xác | V6-05 |
| 5 | query | Tải lại definition/version hiện tại khi conflict | *(dùng lại Screen 3 hàng 2/4)* | *(dùng lại)* | `GetDefinition`/`GetDefinitionVersion` — **authority dùng chung với Screen 3** | V6-05 |

## 6. Screen 5 — Kanban board

**Purpose.** Xem WorkItem theo cột trạng thái, filter theo repository/component, multi-repo badge,
blocker, freshness; drag/drop chỉ là shortcut cho named valid action đã có (V7-09, ADR-028).

**States.**
- Loading: skeleton cột + skeleton card.
- Empty: "Chưa có WorkItem nào" ở cấp board khi Project rỗng; "Không có item" ở cấp từng cột khi cột
  đó rỗng nhưng board có dữ liệu — hai empty state khác nhau, không dùng chung một thông điệp.
- Stale: badge `DEGRADED`/`STALE` theo generation/freshness của chính V6-08A/V6-10; khi stale, các
  action lạc quan (optimistic drag) bị vô hiệu hóa cho tới khi projection fresh trở lại.
- Error: banner fetch lỗi + retry ở cấp board; toast lỗi riêng ở cấp card khi một action thất bại
  (ví dụ conflict version).
- Blocked: badge trên card, không phải trạng thái của cột; cột `BLOCKED` chỉ hiển thị card có badge
  đó, không có hành vi cột riêng.

**Keyboard & accessibility.** Board thao tác được đầy đủ KHÔNG cần kéo-thả: mỗi card có menu hành
động (Enter/Space mở) liệt kê đúng những named valid action mà server trả về; kéo-thả chỉ là lối tắt
trình bày cho cùng action đó, có rollback UI khi server reject — không tồn tại lối nào khác ngoài
action đã đăng ký (đáp ứng WCAG operable, đồng thời đúng ADR-028 "Kanban drag/drop chỉ là presentation
của một named valid action").

**Actions/Queries.**

| # | Kind | UI action/query | Proposed operationId | Proposed `aw` leaf | Public application command/query | Owner Task ID |
|---|---|---|---|---|---|---|
| 1 | query | Danh sách card Kanban (filter/paged) | `listKanbanCards` | `aw work-item list` | `ListKanbanCards` [CHƯA CÓ] — projected read model, không phải authoritative detail (phân biệt rõ với Screen 7) | V6-10 |
| 2 | *(bundled)* | Freshness/generation của projection | *(nằm trong response #1, không tách operationId riêng)* | *(cùng lệnh trên)* | field `Freshness` theo shared contract V6-02A | V6-10 |
| 3 | command | Mark ready (`BACKLOG → READY`, kể cả khi kéo card sang cột Ready) | `markWorkItemReady` | `aw work-item mark-ready` | `MarkWorkItemReady` [CHƯA CÓ] — tên khóa cứng ở ADR-028 §30, KHÔNG nhận `targetStatus` | V6-04A |
| 4 | command | Start run (`READY → ACTIVE`, kể cả khi kéo card sang cột Active) | `startWorkflowRun` | `aw run start` | `StartWorkflowRun` [ĐÃ CÓ: `internal/app/runtime/commands.go:StartWorkflowRunRequest`] — **authority dùng chung với Screen 7 hàng 3**; nếu chưa có valid action hợp lệ (thiếu manifest pin…) client từ chối drop, không tự dispatch | V6-06 |
| 5 | command | Cancel run từ menu card | *(dùng lại Screen 7 hàng 4)* | *(dùng lại)* | `CancelRun` — **authority dùng chung với Screen 7**, không định nghĩa lại ở đây | V6-06 |
| 6 | — | Cột `BLOCKED`/`DONE`/`CANCELLED` | *(không có action ghi trực tiếp)* | *(không áp dụng)* | Các cột này chỉ là kết quả hiển thị của blocker/CompletionPolicy/cancellation authority (V6-06D, orchestrator, V6-06) — board KHÔNG BAO GIỜ tự ghi các trạng thái này, đúng "Không làm generic status/family/workspace setter" | V6-06D / orchestrator (đã có chủ, không cần thêm) |

## 7. Screen 6 — Create WorkItem (root/child)

**Purpose.** Nhập đủ WHAT/DONE/scope/out-of-scope/workflow version cho root hoặc child WorkItem
(V7-10, HE-01-M02).

**States.**
- Loading: skeleton form trong lúc tải picker repository/definition version.
- Empty: N/A cho bản thân form; nếu Project chưa có repository/definition nào, hiển thị hướng dẫn
  điều hướng sang Screen 2/3 thay vì form rỗng gây hiểu lầm.
- Stale: N/A — create luôn là command mới, không có state cũ để stale.
- Error: lỗi diagnostics theo field, ánh xạ trực tiếp từ response contract của server (WHAT/DONE/
  scope) — không tự suy lỗi phía client.
- Blocked: N/A.

**Keyboard & accessibility.** Toàn bộ form điều hướng được bằng bàn phím; selector repo/path là
combobox có filter gõ phím; nhóm checkbox chọn subset (child) dùng `fieldset`/`legend` chuẩn.

**Actions/Queries.**

| # | Kind | UI action/query | Proposed operationId | Proposed `aw` leaf | Public application command/query | Owner Task ID |
|---|---|---|---|---|---|---|
| 1 | command | Tạo root WorkItem | `createRootWorkItem` | `aw work-item create` | `CreateRootWorkItem` [ĐÃ CÓ: `internal/app/work/commands.go:CreateRootWorkItemRequest`] — atomic WorkItem+TaskFamily+WorkspaceSet+scope+provision job theo ADR-019 | V6-04 |
| 2 | command | Tạo child WorkItem (subset) | `createChildWorkItem` | `aw work-item create-child` | `CreateChildWorkItem` [ĐÃ CÓ: `internal/app/work/commands.go:CreateChildWorkItemRequest`] | V6-04 |
| 3 | query | Xem trước readiness diagnostics trước submit | `getWorkItemReadiness` | `aw work-item readiness` | `GetWorkItemReadiness` [CHƯA CÓ] | V6-04 |
| 4 | query | Picker repository/definition | *(dùng lại Screen 2 hàng 5, Screen 3 hàng 1)* | *(dùng lại)* | `ListRepositories`, `ListDefinitions` — **authority dùng chung**, không định nghĩa lại | V6-03A / V6-05 |

## 8. Screen 7 — Task/WorkItem detail & actions

**Purpose.** Hiển thị intent, state, family/run, blocker, và toàn bộ action con người hợp lệ tiếp
theo (approve/cancel/scope) — tách biệt ba hành động cancel/withdraw/resolve thành ba nút riêng,
không gộp (V7-11, ADR-020).

**States.**
- Loading: skeleton header + tab.
- Empty: "Chưa có child" khi root chưa có descendant — trạng thái hợp lệ, không phải lỗi.
- Stale: detail này là **authoritative**, không phải projection (V6-04 nêu rõ: "Authoritative
  detail phân biệt với projected card/detail của V6-10") — "stale" ở đây chỉ xảy ra dưới dạng
  conflict 409/`If-Match` mismatch khi dispatch action, không phải freshness badge nền.
- Error: dialog conflict typed khi version lệch, có nút "Tải lại".
- Blocked: block riêng hiển thị **summary + deep link** tới Screen 8 (Graph/Timeline) — action retry
  KHÔNG nằm ở đây (đúng chữ V7-11: "action retry thuộc V7-12 nơi có đủ ngữ cảnh node/attempt").

**Keyboard & accessibility.** Tab dùng ARIA tabs pattern (mũi tên trái/phải chuyển tab); mọi action
phá hủy/có ảnh hưởng lớn (cancel run/cancel task) mở dialog confirm có focus trap, focus mặc định ở
nút an toàn ("Hủy thao tác"), KHÔNG mặc định ở nút "Xác nhận"; nút action có nhãn text rõ ràng (không
chỉ icon) để phân biệt `CancelRun` / `CancelWorkItem` / `ResolveWorkItemBlocker`.

**Actions/Queries.**

| # | Kind | UI action/query | Proposed operationId | Proposed `aw` leaf | Public application command/query | Owner Task ID |
|---|---|---|---|---|---|---|
| 1 | query | Chi tiết WorkItem authoritative | `getWorkItem` | `aw work-item show` | `GetWorkItem` [CHƯA CÓ] | V6-04 |
| 2 | query | Family/children | `listWorkItemFamily` | `aw work-item show` (embed) | `ListWorkItemFamily` [CHƯA CÓ] — có thể là field lồng trong `GetWorkItem`, không bắt buộc operationId riêng | V6-04 |
| 3 | command | Start run | `startWorkflowRun` | `aw run start` | `StartWorkflowRun` [ĐÃ CÓ] — **authority dùng chung với Screen 5 hàng 4** | V6-06 |
| 4 | command | Cancel run | `cancelRun` | `aw run cancel` | `CancelRun` [ĐÃ CÓ: `internal/app/runtime/cancel_run.go:CancelRunRequest`] — trả `CANCELLING`, hiển thị như tiến trình thật, không báo đã hủy xong ngay | V6-06 |
| 5 | command | Cancel WorkItem (hủy hẳn task) | `cancelWorkItem` | `aw work-item cancel` | `CancelWorkItem` [ĐÃ CÓ: `internal/app/runtime/cancel_work_item.go:CancelWorkItemRequest`] | V6-06D |
| 6 | — | Resolve blocker (chỉ summary+link ở đây) | *(không dispatch trực tiếp tại Screen 7)* | *(điều hướng sang Screen 8)* | `ResolveWorkItemBlocker` — **authority dùng chung với Screen 8**, invocation thật nằm ở Screen 8 vì cần ngữ cảnh node/attempt | V6-06D |
| 7 | command | Yêu cầu mở rộng scope | `requestScopeExpansion` | `aw scope-expansion request` | `RequestScopeExpansion` [ĐÃ CÓ: `internal/app/work/scope_expansion.go:RequestScopeExpansionRequest`] | V6-04 |
| 8 | command | Duyệt scope expansion | `approveScopeExpansion` | `aw scope-expansion approve` | `ApproveScopeExpansion` [ĐÃ CÓ: `internal/app/work/scope_expansion.go:ApproveScopeExpansionRequest`] | V6-04 |
| 9 | command | Từ chối scope expansion | `rejectScopeExpansion` | `aw scope-expansion reject` | `RejectScopeExpansion` [ĐÃ CÓ: `internal/app/work/scope_expansion.go:RejectScopeExpansionRequest`] | V6-04 |
| 10 | command | Rút yêu cầu scope expansion (chỉ khi pending) | `withdrawScopeExpansion` | `aw scope-expansion withdraw` | `WithdrawScopeExpansion` [ĐÃ CÓ: `internal/app/work/scope_expansion.go:WithdrawScopeExpansionRequest`] — sở hữu bởi chính V6-04 theo đúng chữ Thực hiện | V6-04 |
| 11 | command | Duyệt/từ chối approval (WAIT decision) | `resolveApproval` | `aw approval approve` / `aw approval reject` | `ResolveApproval` [ĐÃ CÓ: `internal/app/runtime/approval.go:ResolveApprovalRequest`, field `Outcome` là vocabulary do node khai báo — không phải generic status] — UI/CLI trình bày thành hai nút/leaf riêng cho cùng một command với `Outcome` khác nhau, không phải hai authority | V6-06A |
| 12 | command | Gửi tín hiệu WAIT typed | `submitWaitSignal` | `aw wait signal` | `SubmitWaitSignal` [CHƯA CÓ — tiền thân nội bộ: `ports.WaitRepository.RecordWaitSignal` + `runtimedomain.NewWaitSignal` đã có ở `internal/app/runtime/wait.go`, chưa có public command bọc ngoài] | V6-06A |
| 13 | *(bundled)* | Next valid actions/freshness | *(nằm trong response #1)* | *(cùng lệnh trên)* | field `ValidAction`/`Freshness` theo shared contract V6-02A — advisory, server luôn revalidate khi dispatch | V6-04 (data) / V6-02A (contract shape) |

## 9. Screen 8 — Run graph & timeline (+ diagnostics/recovery)

**Purpose.** Overlay definition đã pin lên NodeRun/Attempt/route/retry/checkpoint; chẩn đoán queue/
job/lease/fence/provider/workspace; thực hiện action retry-blocked có đủ ngữ cảnh node/attempt
(V7-12, ADR-018, ADR-022).

**States.**
- Loading: skeleton canvas graph + placeholder danh sách accessible.
- Empty: "Đang chuẩn bị run" khi run vừa start, chưa có node nào activate.
- Stale: cursor quá cũ trả typed resync (V6-06B "stable cursors") — banner resync, tạm dừng live
  update cho tới khi người dùng chấp nhận resync.
- Error: banner fetch lỗi; panel chi tiết lỗi riêng cho từng attempt `FAILED`/`TIMED_OUT`.
- Blocked: node/activation `BLOCKED` có visual state riêng biệt, hiển thị đúng `TerminationReason`
  typed, kèm action retry (khi hợp lệ) hoặc đề xuất `CancelRun` thay thế khi adapter drift không
  khôi phục được pin (đúng chữ V7-12: "trình bày CancelRun là valid action thay thế thay vì mời
  retry vô ích").

**Fork/join/rework/checkpoint — visual state bắt buộc theo đúng spec V6-06B:**
- **Fork.** Node có nhiều hơn một activation edge đi ra render thành nhánh rẽ, mỗi nhánh gắn nhãn
  route/condition đã chọn; các nhánh chạy song song hiển thị thành lane song song trên timeline
  (không chồng lên nhau thành một dòng).
- **Join.** Node chờ nhiều nhánh cha có visual state riêng "đang chờ join" cho tới khi mọi nhánh bắt
  buộc đạt trạng thái terminal/đã chọn route; khi join xong, các cạnh hội tụ về mặt hình ảnh và
  timeline đánh dấu đúng sự kiện join (`JOIN_DECIDED`).
- **Rework.** Cạnh rework (vòng lặp lại một node trước đó) render bằng kiểu nét đứt/back-edge khác
  hẳn luồng thuận; mỗi lần lặp rework tăng một bộ đếm iteration hiển thị trên node đích; timeline
  liệt kê MỖI lần activation rework là một entry correlated riêng, không gộp chung với lần đầu.
- **Checkpoint.** Node/attempt có checkpoint durable hiển thị icon marker trên timeline; hover/chọn
  cho thấy vị trí checkpoint và khả năng resume — chỉ đọc ở screen này (phục vụ nhận biết, không
  phải action phục hồi tại đây).

**Keyboard & accessibility.** Graph renderer BẮT BUỘC có accessible list fallback — danh sách
node/edge/attempt tuyến tính, điều hướng được bằng bàn phím, tương đương thông tin với đồ họa, bật
tắt bằng một toggle; timeline là danh sách roving tabindex; correlation/causation ID hiển thị dạng
text (không chỉ qua tooltip hover) để screen reader/bàn phím đọc được.

**Actions/Queries.**

| # | Kind | UI action/query | Proposed operationId | Proposed `aw` leaf | Public application command/query | Owner Task ID |
|---|---|---|---|---|---|---|
| 1 | query | Chi tiết Run (manifest revision, pin) | `getRun` | `aw run show` | `GetRun` [CHƯA CÓ] | V6-06B |
| 2 | query | Graph (node/edge/activation) | `getRunGraph` | `aw run graph` | `GetRunGraph` [CHƯA CÓ] | V6-06B |
| 3 | query | Timeline (attempt/route/retry/checkpoint, correlation/causation) | `getRunTimeline` | `aw run timeline` | `GetRunTimeline` [CHƯA CÓ] | V6-06B |
| 4 | query | Diagnostics (queue/job/lease/fence/provider/workspace + advisory valid action) | `getRunDiagnostics` | `aw run diagnostics` | `GetRunDiagnostics` [CHƯA CÓ] | V6-06C |
| 5 | command | Retry blocked activation | `retryBlockedActivation` | `aw node-run retry-blocked` | `RetryBlockedActivation` [ĐÃ CÓ: `internal/app/runtime/retry_blocked_activation.go:RetryBlockedActivationRequest`] — retry thất bại KHÔNG được sinh thêm blocked activation mới trong view (Verify V7-12) | V6-06D |
| 6 | command | Cancel run (fallback khi adapter drift không phục hồi được) | *(dùng lại Screen 7 hàng 4)* | *(dùng lại)* | `CancelRun` — **authority dùng chung với Screen 7**, không định nghĩa lại | V6-06 |
| 7 | command | Cancel WorkItem / Resolve blocker (thực thi thật, có ngữ cảnh node/attempt) | *(dùng lại Screen 7 hàng 5/6)* | *(dùng lại)* | `CancelWorkItem` / `ResolveWorkItemBlocker` — **authority dùng chung với Screen 7**; hai bề mặt UI (summary-link ở Screen 7, nút thao tác thật ở Screen 8) chia sẻ ĐÚNG MỘT command mỗi loại, không phải hai owner | V6-06D |

## 10. Screen 9 — Workspace / source-diff-log viewer

**Purpose.** Tab theo từng repository, xem source/diff/log read-only, trạng thái revision/scope/
lease/quarantine, không có browser terminal trong Alpha (V7-13, ADR-018).

**States.**
- Loading: skeleton per-repo tab.
- Empty: repository chưa có commit / diff không có thay đổi — "Không có khác biệt" là empty state
  riêng, không phải lỗi.
- Stale: revision bị pin tại thời điểm request; banner "Revision có thể đã cũ, tải lại" gắn với
  lease/generation từ V6-10B.
- Error: lỗi typed bounded/truncation (file nhị phân, quá lớn) hiển thị thông báo rõ ràng, không
  phải generic error.
- Blocked: trạng thái quarantine của workspace hiển thị theo từng repo kèm action reconcile.

**Keyboard & accessibility.** Tab repo dùng ARIA tabs; diff viewer có ký hiệu +/- dạng text đi kèm
màu (không chỉ màu); danh sách log điều hướng và phân trang được bằng bàn phím.

**Actions/Queries.**

| # | Kind | UI action/query | Proposed operationId | Proposed `aw` leaf | Public application command/query | Owner Task ID |
|---|---|---|---|---|---|---|
| 1 | query | Trạng thái workspace/repository-workspace | `getWorkspaceState` | `aw workspace-set show` | `GetWorkspaceState` [CHƯA CÓ] | V6-10B |
| 2 | command | Yêu cầu release WorkspaceSet | `requestWorkspaceSetRelease` | `aw workspace-set release` | `RequestWorkspaceSetRelease` [ĐÃ CÓ: `internal/app/workspacerelease/commands.go:RequestWorkspaceSetReleaseRequest`] — dispatch bất đồng bộ, confirm phải nêu rõ đây là intent, không phải thao tác tức thời | V6-10B |
| 3 | command | Yêu cầu reconcile repository-workspace | `requestWorkspaceReconciliation` | `aw repository-workspace reconcile` | `RequestWorkspaceReconciliation` [ĐÃ CÓ: `internal/app/workspacereconcile/commands.go:RequestWorkspaceReconciliationRequest`] | V6-10B |
| 4 | query | Nội dung source file | `getSource` | `aw repository-workspace source` | `GetSource` [CHƯA CÓ] — tên khóa cứng ở Mục tiêu V6-10C | V6-10C / V6-10D (route) |
| 5 | query | Diff | `getDiff` | `aw repository-workspace diff` | `GetDiff` [CHƯA CÓ] — tên khóa cứng ở Mục tiêu V6-10C | V6-10C / V6-10D |
| 6 | query | Repository log | `getRepositoryLog` | `aw repository-workspace log` | `GetRepositoryLog` [CHƯA CÓ] — tên khóa cứng ở Mục tiêu V6-10C | V6-10C / V6-10D |

## 11. Screen 10 — ReleaseSet & local commit

**Purpose.** Tạo/seal/abandon ReleaseSet, local commit có confirm rõ ràng, verdict từng repository,
không có lối ra remote (push/PR/merge/force-push) (V7-13A, ADR-014).

**States.**
- Loading: skeleton list/detail.
- Empty: "Chưa có ReleaseSet nào" với CTA tạo mới.
- Stale: local commit là operation bất đồng bộ — trạng thái "Đang xử lý" (PENDING/RUNNING) hiển thị
  riêng biệt với error, không coi là stale kiểu projection.
- Error: verdict từng repository (partial release) hiển thị tách biệt với lỗi toàn operation.
- Blocked: N/A — mismatch/drift hiển thị như conflict typed, không phải trạng thái blocked riêng.

**Keyboard & accessibility.** Dialog confirm seal/abandon/local-commit yêu cầu xác nhận rõ ràng
(đúng chữ V7-13A "confirmed local commit"), tới được bằng bàn phím; bảng verdict từng repo điều
hướng được bằng bàn phím. Ghi chú UI contract: **không có** control push/PR/merge/force-push nào
tồn tại trong DOM của screen này — không phải ẩn, mà không render, nên không có gì để focus tới.

**Actions/Queries.**

| # | Kind | UI action/query | Proposed operationId | Proposed `aw` leaf | Public application command/query | Owner Task ID |
|---|---|---|---|---|---|---|
| 1 | query | Danh sách ReleaseSet | `listReleaseSets` | `aw release-set list` | `ListReleaseSets` [CHƯA CÓ] | V6-10E / V6-10F |
| 2 | command | Tạo ReleaseSet | `createReleaseSet` | `aw release-set create` | `CreateReleaseSet` [ĐÃ CÓ: `internal/app/work/release_set.go:CreateReleaseSetRequest`] | V6-10E / V6-10F |
| 3 | query | Chi tiết ReleaseSet | `getReleaseSet` | `aw release-set show` | `GetReleaseSet` [CHƯA CÓ] | V6-10E / V6-10F |
| 4 | command | Seal ReleaseSet | `sealReleaseSet` | `aw release-set seal` | `SealReleaseSet` [ĐÃ CÓ: `internal/app/work/release_set.go:SealReleaseSetRequest`] | V6-10E / V6-10F |
| 5 | command | Abandon ReleaseSet | `abandonReleaseSet` | `aw release-set abandon` | `AbandonReleaseSet` [ĐÃ CÓ: `internal/app/work/release_set.go:AbandonReleaseSetRequest`] | V6-10E / V6-10F |
| 6 | command | Yêu cầu local commit cho một entry | `requestReleaseSetLocalCommit` | `aw release-set local-commit` | `RequestReleaseSetLocalCommit` [CHƯA CÓ] — tên khóa cứng ở Phạm vi V6-10E, job-backed, crash-safe | V6-10E / V6-10F |
| 7 | query | Trạng thái operation local-commit | `getLocalCommitOperationStatus` | `aw release-set local-commit status` | `GetLocalCommitOperationStatus` [CHƯA CÓ] | V6-10F |
| 8 | command | Release WorkspaceSet từ màn hình này | *(dùng lại Screen 9 hàng 2)* | *(dùng lại)* | `RequestWorkspaceSetRelease` — **authority dùng chung với Screen 9**, không định nghĩa lại | V6-10B |

## 12. Screen 11 — Evidence / artifact viewer

**Purpose.** Verdict cấp criteria, RevisionSet chính xác, xem/tải output với hash/tamper state
(V7-14, AK-ARCH-021).

**States.**
- Loading: skeleton list/preview.
- Empty: "Chưa có evidence cho criterion này."
- Stale: N/A — evidence immutable sau khi tạo.
- Error: lỗi tamper/hash-mismatch typed tách biệt với lỗi fetch chung; thông báo typed riêng khi
  evidence đã hết hạn retention.
- Blocked: N/A.

**Keyboard & accessibility.** Panel preview đóng được bằng bàn phím (Escape); nút download là
link/button thật (không phải giả); chỉ báo truncation công bố qua `aria-live` khi nội dung vượt
giới hạn; PASS/FAIL/ERROR/N/A truyền bằng icon + text.

**Actions/Queries.**

| # | Kind | UI action/query | Proposed operationId | Proposed `aw` leaf | Public application command/query | Owner Task ID |
|---|---|---|---|---|---|---|
| 1 | query | Danh sách evidence theo WorkItem/Run/criterion | `listEvidence` | `aw evidence list` | `ListEvidence` [CHƯA CÓ] | V6-07B |
| 2 | query | Chi tiết/verify metadata evidence (online) | `getEvidence` | `aw evidence show` | `GetEvidence` [CHƯA CÓ] — khác với `aw evidence verify` (offline, `CLI_LOCAL` theo ADR-028, dùng injected verifier không qua HTTP); hai leaf khác nhau cho hai nhu cầu khác nhau, không phải trùng authority | V6-07B |
| 3 | query | Danh sách artifact | `listArtifacts` | `aw artifact list` | `ListArtifacts` [CHƯA CÓ] | V6-07B |
| 4 | query | Nội dung artifact (stream/tải) | `getArtifactContent` | `aw artifact get --output <path\|->` | `GetArtifactContent` [CHƯA CÓ] — tên khóa cứng ở Phạm vi V6-07B | V6-07B |
| 5 | query | Chi tiết ContextSnapshot | `getContextSnapshot` | `aw context-snapshot show` | `GetContextSnapshot` [CHƯA CÓ] | V6-07B |

## 13. Screen 12 — Task chat

**Purpose.** UI hội thoại canonical, không lẫn approve/cancel/scope với free text (V7-15,
HE-05-M07).

**States.**
- Loading: skeleton danh sách message.
- Empty: "Chưa có tin nhắn nào" mời bắt đầu hội thoại.
- Stale: banner reconnect/backoff SSE khi mất kết nối — đây là mối quan tâm của app shell (V7-04),
  dùng chung, không định nghĩa lại ở screen này.
- Error: tin nhắn gửi lỗi hiển thị inline kèm nút gửi lại (dùng lại đúng idempotency key, không tạo
  message mới).
- Blocked: N/A — chat không bao giờ bị block; approval/WAIT là control typed riêng (Screen 7), không
  thuộc authority của screen này.

**Keyboard & accessibility.** Danh sách message là log region `aria-live="polite"` cho tin nhắn
mới; ô soạn là textarea chuẩn với nút gửi hiển thị (không chỉ Enter-to-send); tải file lên có file
picker truy cập được bằng bàn phím, tiến trình công bố qua `aria-live`.

**Actions/Queries.**

| # | Kind | UI action/query | Proposed operationId | Proposed `aw` leaf | Public application command/query | Owner Task ID |
|---|---|---|---|---|---|---|
| 1 | query | Danh sách message (paged) | `listMessages` | `aw message list` | `ListMessages` [CHƯA CÓ] | V6-07 |
| 2 | command | Gửi message (text) | `appendMessage` | `aw message append` | `AppendMessage` [ĐÃ CÓ: `internal/app/message/commands.go:AppendMessageRequest`, `internal/app/ports/message.go:AppendMessageRequest`] | V6-07 |
| 3 | command | Upload attachment | `appendConversationAttachment` | `aw attachment upload` | `AppendConversationAttachment` [CHƯA CÓ] — tên khóa cứng ở Phạm vi V6-07A | V6-07A |
| 4 | *(bundled)* | Context-used metadata theo message | *(nằm trong response #1/detail)* | *(cùng lệnh trên)* | field ContextSnapshot/message reference — **authority dùng chung với Screen 11 hàng 5** cho chi tiết đầy đủ | V6-07 (field) / V6-07B (detail) |

Ghi chú rõ ràng: nút approve/reject/scope/WAIT **không** xuất hiện ở screen này — chúng thuộc Screen
7, đúng yêu cầu "không map message text thành control."

## 14. Screen 13 — Settings & run diagnostics

**Purpose.** Xem/sửa safe settings đã allowlist, hướng dẫn restart-required, và bối cảnh chẩn đoán
job/lease/recovery cấp installation (V7-16, ADR-016, ADR-017).

**States.**
- Loading: skeleton form.
- Empty: N/A — settings luôn có desired document.
- Stale: banner conflict `If-Match`/version khi một phiên khác đã update trước.
- Error: lỗi strict-unknown-field / invalid-value hiển thị inline theo từng field allowlist.
- Blocked: N/A.

**Keyboard & accessibility.** Field theo label/error association chuẩn; chỉ báo "restart required"
là text+icon, công bố qua `aria-live` sau khi save; field bị `maskedByStartupSource` hiển thị dạng
disabled kèm giải thích rõ ràng (không ẩn âm thầm).

**Actions/Queries.**

| # | Kind | UI action/query | Proposed operationId | Proposed `aw` leaf | Public application command/query | Owner Task ID |
|---|---|---|---|---|---|---|
| 1 | query | Lấy safe settings (desired/effective/version/masking) | `getSafeSettings` | `aw settings show` | `GetSafeSettings` [CHƯA CÓ] — tên khóa cứng ở Phạm vi V6-10G | V6-10G / V6-10H (route) |
| 2 | command | Cập nhật safe settings (`If-Match`) | `updateSafeSettings` | `aw settings update` | `UpdateSafeSettings` [CHƯA CÓ] — tên khóa cứng ở Phạm vi V6-10G | V6-10G / V6-10H |
| 3 | query | Run diagnostics (drill-down theo Run cụ thể) | *(dùng lại Screen 8 hàng 4)* | *(dùng lại)* | `GetRunDiagnostics` — **authority dùng chung với Screen 8**; screen này chỉ cung cấp bối cảnh cài đặt/provider/retention xung quanh, không định nghĩa một query aggregate riêng (xem §6.2 để biết vì sao không có query "danh sách toàn bộ Run cần chú ý" độc lập) | V6-06C |

## 15. Cross-cutting concerns (không thuộc riêng một screen nào)

Các mục dưới đây phục vụ nhiều/mọi screen, sở hữu bởi V7-04 (app shell) ở phía UI — không tính vào
13 screen vì V7-04 tự nó không phải một "screen" (nó là shell/routing/SSE, đúng như V6-00 loại các
task V7-01…V7-04 ra khỏi danh sách screen ở §0).

| Concern | Proposed operationId | Proposed `aw` leaf | Public application command/query | Owner Task ID |
|---|---|---|---|---|
| Health liveness | `getHealthLive` | *(không có `aw` command riêng — CLI dùng `aw health live`, xem V6-15C)* | health check nội bộ (không phải application command/query per se) | V6-01 |
| Health readiness | `getHealthReady` | `aw health ready` | health check nội bộ | V6-01 |
| SSE project event stream (invalidation/runtime summary, dùng cho reconnect/badge stale trên Kanban, Doctor, Graph) | `watchProjectEvents` | `aw events watch` | `WatchProjectEvents` [CHƯA CÓ] — tên khóa cứng ở Phạm vi V6-11 | V6-11 |
| Bootstrap per-start token (không phải action người dùng thấy) | *(không áp dụng — bootstrap HTML no-store/CSP)* | *(không áp dụng)* | `LocalPrincipalSnapshot` binding | V6-01A |
| Projection rebuild (request/status) | `requestProjectionRebuild` / `getProjectionRebuildStatus` | `aw projection rebuild` / `aw projection rebuild-status` | `RequestProjectionRebuild` [CHƯA CÓ] / rebuild operation status | V6-09 / V6-09A / V6-09B |

**Ghi chú quan trọng về "Projection/rebuild status" không phải screen riêng:** `08-v6-api-
projections.md` có định nghĩa đầy đủ backend cho rebuild (V6-09, V6-09A, V6-09B) và CLI (V6-15N),
nhưng `09-v7-alpha-ui.md` (nguồn thật của 13 screen) không đặt tên một screen UI riêng cho nó — từ
"rebuild"/"projection" trong toàn bộ file V7 chỉ xuất hiện ở mức "stale/degraded projection
indicator" (V7-04, shell) và "projection resync" (V7-17, test full-journey), không có Mục tiêu/
Thực hiện riêng nào mô tả một màn hình rebuild. Đây KHÔNG phải một gap bị bỏ sót của V6-00 (V6-00
mô tả đúng những gì V7 đã quyết định làm UI); nếu một phiên bản V7 sau này quyết định thêm màn hình
thao tác rebuild, authority đã sẵn có ở V6-09/V6-09A/V6-09B — không cần Task ID mới.

## 16. Gap register và tự-kiểm theo Verify bar

### 16.1 Danh sách gap (action/query chưa có public operation, đã có owner)

Toàn bộ ô "CHƯA CÓ" ở các bảng trên là action/query chưa được implement — vì toàn bộ V6-01…V6-15P
chưa bắt đầu (chỉ V6-00A đã đóng). Mỗi ô như vậy đã có **đúng một** Owner Task ID trỏ vào chính
`08-v6-api-projections.md`; không có ô nào bị bỏ trống. Đây chính là nghĩa "gap có owner" mà spec
yêu cầu — task backend tương ứng sẽ quyết định request/response DTO chính xác khi implement, nhưng
KHÔNG cần hỏi lại product decision về: action đó có tồn tại không, nó thuộc scope nào (installation/
project), và nó gọi đúng authority nào.

### 16.2 Hai điểm mơ hồ đã tự giải quyết (ghi lại lý do, không im lặng bỏ qua)

1. **Component pack assignment naming** — `AssignComponentPack` đã tồn tại thật trong
   `internal/app/ports/catalog.go`, nên đây là một mapping cụ thể (Screen 2 hàng 10), không phải
   gap; V6-03A chỉ cần thêm CommandEnvelope/HTTP/CLI, không cần quyết định lại tên hay authority.
2. **"Run diagnostics" ở Screen 13 (Settings) có cần một query aggregate "danh sách mọi Run cần chú
   ý" riêng không?** — Rà toàn bộ `08-v6-api-projections.md` không tìm thấy task nào định nghĩa một
   query cross-run như vậy; `V6-06C` chỉ định nghĩa đúng `GET /runs/{id}/diagnostics` (scope một
   Run). Quyết định: KHÔNG bịa một Task ID mới cho nhu cầu giả định này, vì đường vận hành thật đã
   đủ — Kanban (Screen 5) đã hiển thị badge blocker/degraded per-card, Task detail (Screen 7) đã có
   deep-link, và Graph (Screen 8) đã có `GetRunDiagnostics` đầy đủ ngữ cảnh; Settings (Screen 13)
   chỉ cần TÁI SỬ DỤNG đúng authority đó khi người dùng drill-down từ một Run cụ thể. Nếu sau này
   product thực sự muốn một dashboard "mọi Run cần chú ý" cấp installation, đó là quyết định sản
   phẩm mới ngoài phạm vi 13 screen hiện tại của V7 — không phải điều V6-00 được phép tự quyết
   (đúng "Không làm: không viết... domain command" của chính task này).

### 16.3 Tự-kiểm so với Verify bar của chính V6-00

- **"đủ 13 screen"** — §0 liệt kê đúng 13 screen, khớp 1:1 với V7-05…V7-16 + V7-13A; không thêm,
  không bớt.
- **"mọi ô có mapping hoặc owner"** — mọi hàng trong 13 bảng Actions/Queries (§2–§14) có đủ bốn cột
  operationId/aw leaf/public operation/Owner Task ID; không có ô nào để trống.
- **"không có gap chưa gán"** — mọi action "[CHƯA CÓ]" đều có đúng một Owner Task ID trỏ vào một
  Task ID đã tồn tại trong `08-v6-api-projections.md` (xem §16.1); không có action nào chờ một
  quyết định sản phẩm chưa ai chịu trách nhiệm.
- **"không trùng authority"** — mọi trường hợp một command được gọi từ ≥2 screen (`StartWorkflowRun`,
  `CancelRun`, `CancelWorkItem`, `ResolveWorkItemBlocker`, `RequestWorkspaceSetRelease`,
  `GetRunDiagnostics`, ContextSnapshot metadata) đều được đánh dấu rõ "authority dùng chung với
  Screen #N", cùng một Owner Task ID ở cả hai nơi — không có hai Task ID nào cùng tuyên bố sở hữu
  MỘT mutation.
- **"không tự tạo generic status setter"** — không có hàng nào tên `set-status`/
  `TransitionWorkItemStatus`/`UpdateRunState` hay tương đương; mọi transition dùng command hẹp đã
  liệt kê ở §1 cuối cùng.
- **Zero gap KHÔNG phải precondition để hoàn thành V6-00** (đúng chữ Verify) — tài liệu này cố tình
  giữ nguyên các ô "[CHƯA CÓ]" thay vì giả vờ chúng đã tồn tại; điều bắt buộc chỉ là mỗi ô như vậy
  có owner, không phải mọi ô phải đã implement xong (đó là việc của V6-12 làm gate sau).

## 17. Kết luận — sẵn sàng cho endpoint task

Task V6-0x (V6-03…V6-11) và task CLI V6-15C…V6-15N có thể bắt đầu implement mà không cần hỏi lại
product decision về: screen nào tồn tại, mỗi action/query trên đó tên gì, gọi authority nào, thuộc
scope installation hay project, và action nào KHÔNG được tạo (generic setter, `CreateComponent`
public, remote Git). Những gì còn lại là quyết định kỹ thuật thuộc về chính task backend đó (DTO
field chính xác, pagination shape, error code cụ thể) — đúng ranh giới "không viết production UI,
endpoint hay domain command" của V6-00.
