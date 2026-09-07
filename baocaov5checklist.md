# V5 checklist — Execution, context, verification và evidence

> File cá nhân, KHÔNG commit (không gitignore nhưng theo đúng precedent
> baocaov0-4checklist.md — `git status` xác nhận các file đó vẫn untracked
> qua nhiều phase). Ghi narrative đầy đủ cho mỗi task: quyết định, lý do,
> câu hỏi tự phát hiện, verify output thật.

## V5-01 — Artifact metadata và retention migration

**Trạng thái:** DONE. PR [#28](https://github.com/draculemihawk123-ai/agent-workflow/pull/28),
CI 6/6 xanh (contract ×2, spike acceptance ×2, Linux race/stability, semantic-diff), user tự
merge (không phải Claude) lúc 2026-09-07T01:21:21Z, merge commit `666312a`. Local branch đã xóa,
master đã fast-forward.

User đã trả lời AskUserQuestion "merge and continue to V5-02" ngay khi PR mở (trước khi CI xong) —
continuous-authorization cho cả phase V5, xem memory `agent-kit-v5-continuous-execution`. Từ đây
tiếp tục tuần tự sang V5-02 không dừng hỏi giữa các task, vẫn giữ nguyên kỷ luật per-task (branch/
PR/CI/merge riêng, verify thật, log narrative vào file này).

**Bối cảnh trước khi code:** V5 chưa có task nào chạy trước đây
(`gh pr list` xác nhận PR #27 = V4-14 là PR gần nhất; không PR nào tên
"v5-*"). V5-01 không có caller thật nào tồn tại — V5-02..V5-15 (Message,
checkpoint diff, gate evidence, ReleaseSet, ...) đều CHƯA build — nên đây là
lần đầu tiên `ports.ArtifactStore` (V1-08) được wire vào một caller sản
xuất thật.

**Nghiên cứu trước khi viết code** (đọc trực tiếp, không suy đoán):
- go-core-spec §4.6 cho sẵn struct mục tiêu: `ArtifactRef { ID, ProjectID,
  Kind, URI, ContentHash, Size, MediaType, Sensitivity, CreatedAt }` — đây
  là DB-row shape khác hẳn `ports.ArtifactRef` (handle content-addressed
  V1 trả về từ `Put`). Quyết định: bỏ `Kind`/đổi `URI`→`Locator` (chưa có
  caller cần phân loại theo Kind — roadmap §3 "không tạo field chưa có
  contract test").
- ADR-017 (`docs/architecture/02-architecture-decisions.md` mục 19) khóa
  sẵn: TTL 7 ngày mặc định cho evidence/raw output; canonical
  Message/context/resource giữ dưới nhãn `CANONICAL_CONTEXT` (literal đã
  chốt) + retention hold; metadata/hash giữ để audit dù payload hết hạn;
  sweeper chỉ xóa artifact đúng class, không hold, đã qua integrity check.
  → `RetentionClass` chỉ có đúng 2 giá trị: `RAW_OUTPUT_TEMP` (7 ngày) và
  `CANONICAL_CONTEXT` (không TTL) — không thêm class thứ ba cho "audit
  metadata" vì đọc kỹ ADR-017 thì đó là thuộc tính của MỌI row (dòng DB
  không bao giờ tự xóa trong phạm vi V5-01/V5-14; chỉ sweeper mới xóa
  BODY/artifact, chưa bao giờ xóa row).
- go-core-spec §11.1 (Application transaction): "Không gọi Git, provider,
  process, filesystem artifact store hoặc network trong transaction." — xác
  nhận CỨNG: `ArtifactStore.Put`/`Verify` KHÔNG BAO GIỜ được gọi bên trong
  `WithSerializedWrite`. §11.2 (Worker transaction): "Artifact MAY được lưu
  với nhãn untrusted/orphan để điều tra" — xác nhận "orphan" là một STATE
  VALUE ghi được trên chính row, không phải một khái niệm suy ra bằng cách
  so sánh filesystem với DB.
- `internal/app/ports/unitofwork.go`'s `Tx` interface: pattern
  "AdapterBuilds/Readiness/Wait/Approvals populated now" — mỗi field mới
  nhận real interface từ đầu khi task sở hữu trọn concern. Áp dụng y hệt
  cho `Artifacts() ArtifactRepository`.

**Quyết định thiết kế (tự quyết, có lý do, note lại để user review):**
1. **Không có "owner"/aggregate reference trên chính Artifact row** — khớp
   đúng go-core-spec §4.6: `ArtifactRef` không có field owner nào; owner
   luôn nằm ở CHIỀU NGƯỢC LẠI (`Evidence.ArtifactRefs[]`,
   `ConversationMessage.ContentArtifactID`). V5-01 chỉ dựng row + repository
   cho MỌI task V5 sau này compose vào transaction riêng của chúng
   (Message V5-02, diff capture V5-08A, gate evidence V5-10, …).
2. **`PrepareAttachment` (internal/app/artifact) KHÔNG tự mở transaction**
   — chỉ làm nửa "phải chạy ngoài Tx" (Put + Verify + build domain
   struct), trả về `artifact.Artifact` sẵn sàng cho caller tự
   `tx.Artifacts().InsertArtifact` bên trong Tx CỦA CHÍNH caller đó. Lý do:
   một future caller thật (V5-02 chẳng hạn) cần insert CẢ message row VÀ
   artifact row trong CÙNG một transaction — nếu `PrepareAttachment` tự mở
   `WithSerializedWrite` của riêng nó thì không compose được (không được
   nest transaction, `unitofwork.go` cấm rõ). Đây là lý do V5-01 không có
   một "command" top-level kiểu `CreateRootWorkItem` — nó là building
   block, không phải public command.
3. **ID luôn mint mới qua `idsource`, KHÔNG content-addressed** — khác
   `AdapterBuildVersion` (ADR-022's exception rõ ràng cho operational
   registry). Cùng nội dung bytes có thể backing nhiều Artifact row độc
   lập (khác Project, khác retention/hold) — `ArtifactStore.Put` tự dedupe
   bytes trên đĩa rồi, không cần DB tầng trên dedupe theo hash lần nữa.
4. **`AttachState{Orphan, Attached}`** — Orphan là state hợp lệ MỌI row có
   thể mang (không phải "trạng thái tạm trước khi có row"), transition
   Orphan→Attached là fenced CAS (`TransitionArtifactAttachState`). V5-01
   tự test cả hai chiều bằng insert trực tiếp (không cần caller thật) —
   đúng tinh thần "test cái trạng thái, không chờ có caller mới test".
5. **`Hold`** là field độc lập, CAS riêng (`SetArtifactHold`), không gắn
   với AttachState/RetentionClass — khớp ADR-017 "Hold hợp lệ chặn sweeper"
   (governance override, không phải một class).
6. **Redact.Sensitivity chưa từng persist ở đâu trong repo** (grep xác
   nhận) → tự viết `sensitivityToDB`/`sensitivityFromDB` (map sang TEXT
   'PUBLIC'/'SENSITIVE'/'SECRET'), không có convention cũ để theo.
7. **Migration số 0027** — 0013 là gap lịch sử vĩnh viễn (đã note trong
   `docs/design/06-v4-runtime-engine.md` dòng ~720); file mới luôn lấy số
   cao nhất hiện có (0026) + 1, KHÔNG lấp gap 0013. Migration count
   assertion cứng (`db_test.go`×2, `unitofwork_test.go`×1) phải bump
   25→26 — đúng bài học V4-13 để lại (đếm theo SỐ FILE, không theo SỐ
   HIỆU cao nhất).
8. **`fake.Tx` (internal/app/ports/fake) cũng cần `ArtifactRepository`
   fake** — phát hiện khi build: `fake.Tx` implement `ports.Tx` nên thêm
   method vào interface là breaking change nếu không thêm fake tương ứng.
   Viết fake y hệt pattern `CatalogRepository`/`WorkRepository` (map
   in-memory, `cloneWith(catalog)` để check ProjectID tồn tại).

**Phạm vi KHÔNG làm (để lại cho task sau, đúng roadmap §3):**
- Không có `Kind` enum trên Artifact — chưa có caller cần phân loại.
- Không xóa artifact nào cả — retention sweeper thật là V5-14.
- Không có `Delete`/enumerate trên `ports.ArtifactStore` (V1 port giữ
  nguyên, không đổi).
- Không có Evidence domain type — đó là V5-10's job (go-core-spec §4.6 có
  sketch nhưng V5-01 chỉ làm nửa ArtifactRef).

**File thay đổi:**
- `internal/domain/artifact/artifact.go` + `artifact_test.go` (mới)
- `internal/app/ports/artifactrecord.go` (mới)
- `internal/app/ports/unitofwork.go` (sửa: thêm `Tx.Artifacts()`)
- `internal/app/ports/fake/unitofwork.go` (sửa: thêm fake `ArtifactRepository`)
- `internal/adapters/sqlite/migrations/0027_artifacts.sql` (mới)
- `internal/adapters/sqlite/artifact_repository.go` + `artifact_repository_test.go` (mới)
- `internal/adapters/sqlite/unitofwork.go` (sửa: wire accessor)
- `internal/adapters/sqlite/db_test.go`, `unitofwork_test.go` (sửa: migration count 25→26)
- `internal/app/artifact/attach.go` + `attach_test.go` (mới — PrepareAttachment)
- `internal/integration/artifact_attach_test.go` (mới — restart + tamper end-to-end)

**Verify (chạy local, Windows):**
```
go build ./...                                   # sạch
go vet ./...                                     # sạch
go run ./cmd/docs-coverage-check                 # debt = 0
go test -count=1 ./...                           # toàn bộ package PASS (~50 packages)
gofmt -l <13 file .go đổi/mới>                    # rỗng sau gofmt -w (CRLF do git-on-Windows, đúng precedent)
```
`-race` không chạy được local (CGO_ENABLED=0 trên máy này, "-race requires
cgo") — dựa vào CI job `linux-race-and-stability` như mọi task trước.

**Trạng thái ngoài lề phát hiện, KHÔNG sửa (ngoài phạm vi V5-01):**
`docs/00-start-here.md`, `docs/architecture/02-architecture-decisions.md`
(ADR-028), `docs/design/08-v6-api-projections.md` và 8 file doc khác đang
có diff uncommitted TỪ TRƯỚC session này (nội dung hợp lệ, khớp
"Cập nhật: 2026-09-06" đã ghi sẵn trong chính các file — có vẻ là công việc
tài liệu ADR-028 hoàn tất nhưng chưa commit). Không commit kèm vì không
thuộc phạm vi V5-01 và user chưa yêu cầu commit — để nguyên, đã báo cho
user trong response.

**Việc còn lại:** mở PR trên `taQuangLing/agent-workflow` (branch mới), chờ
CI 6/6 xanh, merge. Sau khi merge, task kế tiếp có thể bắt đầu là V5-02
(Conversation và Message authority), phụ thuộc V5-01.

## V5-02 — Conversation và Message authority

**Trạng thái:** code DONE, verify local PASS, chuẩn bị mở PR.

**Nghiên cứu trước khi code:** HE-02-M05 (`docs/harness-engineering/02-lec-02-nam-phan-he-harness.md:25`)
+ HE-05-M07 (`docs/harness-engineering/05-lec-05-lien-tuc-qua-session.md:28`) — dữ liệu sống qua
session MUST platform-owned, raw transcript MUST có redaction/size/retention policy. go-core-spec
§4.6 sketch `ConversationMessage{ID,ProjectID,WorkItemID,AttemptID?,Actor,Role,ContentArtifactID,
CreatedAt}` — xác nhận KHÔNG có aggregate "Conversation" riêng ở bất kỳ đâu trong toàn bộ doc set
(grep toàn repo) — "conversation" chỉ là mọi Message cùng WorkItemID, sắp theo Sequence.

**Quyết định thiết kế:**
1. **`internal/domain/message` là package MỚI, import cả `work` VÀ `runtime`** — xác nhận trước
   bằng cách grep: `runtime` import `work` (không ngược lại, tránh cycle) nên package thứ ba mới này
   import cả hai là an toàn (không ai import `message` lại nên không tạo cycle mới).
2. **`Role` enum (`USER/ASSISTANT/SYSTEM/TOOL`) là suy đoán riêng, KHÔNG có precedent nào trong toàn
   bộ doc set** (grep xác nhận zero). Ghi rõ trong doc comment để user review dễ sửa nếu muốn giá trị
   khác.
3. **Sequence do REPOSITORY tự resolve (MAX(sequence)+1 scoped work_item_id, trong cùng
   transaction), không phải do domain constructor hay caller tự cấp** — copy đúng pattern
   `node_dispatch.go`/`attempt_store.go` đã dùng cho `domain_events.sequence`. Domain constructor
   `NewMessage` chỉ validate Sequence > 0, không tự allocate.
4. **Domain event của message KHÔNG dùng `AggregateType="WorkItem"`** — tự phát hiện lúc code: nếu
   dùng WorkItem aggregate (đã có event stream từ `CreateRootWorkItem` và các command khác), hard-code
   `Sequence` sẽ VA CHẠM UNIQUE(aggregate_type, aggregate_id, sequence) hoặc cần thêm một MAX+1 query
   NỮA cho domain_events (khác hẳn messages.sequence). Sửa: dùng `AggregateType="Message",
   AggregateID=messageID, Sequence=1` — an toàn vì Message là aggregate MỚI HOÀN TOÀN, bất biến, chỉ
   có đúng 1 event từ lúc sinh ra tới mãi mãi (giống lý do `CreateRootWorkItem` hard-code Sequence=1
   cho WorkItem mới tạo).
5. **Cross-project reference check bị THIẾU lúc viết bản đầu, tự bắt được khi viết test**: ban đầu
   `appendMessageTx` chỉ check `work_items.id` tồn tại, không check `work_items.project_id` khớp
   `req.ProjectID` — một caller có thể tạo message claim sai Project trong khi WorkItem thuộc Project
   khác. Sửa cả sqlite VÀ fake: luôn đọc `project_id` thật từ WorkItem's own stored row, trả
   `ports.ErrCrossProjectReference` nếu lệch — đúng discipline `CreateComponent`/`AssignComponentPack`
   đã dùng ("resolve từ row đã lưu, không tin caller"). Thêm test
   `TestMessageRepository_AppendMessage_CrossProjectReference_Rejected`.
6. **`redact.Matcher` KHÔNG quét substring trong free text — CHỈ exact-string-equality** (xác nhận từ
   `redact.go`'s doc comment + `baocaov1checklist.md`'s note tự sửa lỗi V1-09 y hệt). Test
   `TestAppendMessage_MatcherNeverScansSubstringWithinFreeText` cố tình ghi lại giới hạn này (secret
   nhúng giữa câu KHÔNG bị redact — đúng theo thiết kế, không phải bug) để không ai "sửa lại" nhầm sau
   này. Redaction thật của "secret tests" dựa vào 2 cơ chế: `Sensitivity` (structural, luôn áp dụng)
   + `Matcher` (exact-match, optional).
7. **Sensitivity validate TRƯỚC redact/Put, không phải sau** — tự phát hiện: nếu Sensitivity ngoài
   phạm vi hợp lệ lọt qua `Tagged()` (fallback "unredacted passthrough" ở nhánh `default`), bytes
   CHƯA redact đã ghi xuống đĩa qua `Put` trước khi transaction (nơi `artifact.NewArtifact` mới thật
   sự validate Sensitivity) fail và rollback — ArtifactStore không có `Delete`, nên bytes rò rỉ ở lại
   vĩnh viễn dù toàn bộ operation coi như thất bại. Thêm check tường minh trước bước redact.
8. **`PrepareAttachment` gọi TRƯỚC receipt-check** (không tối ưu retry để tránh Put lại) — chấp nhận
   lãng phí I/O nhỏ khi replay (Put vốn idempotent/content-addressed, không sai, chỉ hơi tốn), đổi
   lấy code đơn giản hơn nhiều so với thêm một read-transaction riêng chỉ để pre-check receipt trước
   Put. Ghi lại làm judgment call, không phải thiếu sót.
9. **`AppendMessage`/`ListMessages` không lồng trong `AppendMessageRequest.Sequence`** — repository
   API cố tình KHÔNG có field Sequence trong request (khác hẳn field đó nằm sẵn trong domain struct)
   để không có cách nào caller tự gán sai.
10. **Không viết `commands_sqlite_test.go` riêng cho `internal/app/message`** (khác
    `internal/app/work` có cả `commands_test.go` lẫn `commands_sqlite_test.go`) — quyết định có ý
    thức: sqlite-layer đã test đủ CAS/FK/idempotency/ordering ở `message_repository_test.go`; app-layer
    fake-based test đã cover đủ 4 yêu cầu Verify (ordering/idempotency/attempt linkage — qua
    repository test/secret) mà không cần lặp lại qua sqlite thật lần nữa.
11. **Bỏ qua "attempt linkage" ở app-layer test** (chỉ test ở sqlite-layer qua
    `SeedFixtureExecutionAttempt`) — dựng một ExecutionAttempt thật qua fake cần cả chuỗi
    WorkflowRun→NodeRun→ExecutionAttempt (không có shortcut như sqlite's fixture helper), trong khi
    `AppendMessage`'s own xử lý AttemptID chỉ là forward-through, không có logic ứng dụng nào thêm cần
    verify riêng ở tầng app.

**File thay đổi:**
- `internal/domain/message/message.go` + `message_test.go` (mới)
- `internal/app/ports/message.go` (mới)
- `internal/app/ports/unitofwork.go` (sửa: thêm `Tx.Messages()`)
- `internal/app/ports/fake/message.go` (mới), `fake/unitofwork.go` (sửa: wire fake)
- `internal/adapters/sqlite/migrations/0028_messages.sql` (mới)
- `internal/adapters/sqlite/message_repository.go` + `message_repository_test.go` (mới)
- `internal/adapters/sqlite/unitofwork.go` (sửa: wire accessor)
- `internal/adapters/sqlite/db_test.go`, `unitofwork_test.go` (sửa: migration count 26→27)
- `internal/app/message/commands.go` + `commands_test.go` (mới — AppendMessage/ListMessages)

**Verify (chạy local, Windows):**
```
go build ./...                                   # sạch
go vet ./...                                     # sạch
go run ./cmd/docs-coverage-check                 # debt = 0
go test -count=1 ./...                           # toàn bộ ~55 package PASS
gofmt -l <12 file .go đổi/mới>                    # rỗng sau gofmt -w (CRLF do git-on-Windows)
```

**Việc còn lại:** mở PR, chờ CI 6/6, merge. Task kế tiếp: V5-03 (Resource registry và
ContextAssembler), phụ thuộc V2-06 + V5-02.
