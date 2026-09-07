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

## V5-03 — Resource registry và ContextAssembler

**Trạng thái:** code DONE, verify local PASS, chuẩn bị mở PR.

**Khác biệt với V5-01/V5-02:** đây là task đầu tiên trong V5 đòi hỏi PHÁT MINH thuật toán thật (chưa
có precedent gần), không phải "nối dây" infrastructure theo pattern có sẵn. Trước khi code, đã dừng
hỏi user 3 câu hệ trọng (đúng tinh thần roadmap §10 bước 3 — "liệt kê câu hỏi hệ trọng, dừng trước khi
sửa nếu chưa trả lời") vì fan-out cao (V5-04/V5-08/V5-12 sẽ dùng lại contract này) và vì HE-04 (lecture
nguồn của task) chính là về nguy cơ "last-wins" instruction conflict — một failure mode an toàn thật,
không phải chi tiết vặt.

**Research xác nhận trước khi hỏi:** HE-04-M05/HE-04-S03 (`docs/harness-engineering/04-lec-04-progressive-disclosure.md`)
+ HE-13-M08. V2-06 (`docs/design/04-v2-definition-plane.md:62-70`) đã xây `definition.PriorityClass`
(HARD_CONSTRAINT/REQUIRED_PROCEDURE/GUIDANCE/REFERENCE), `Selector{ComponentTags,PathTags,TaskKinds,
BlockKinds,RiskClasses}` trên Skill/Layer, và `engineeringpack.CheckResourceConflicts` (fail-closed
same-key/different-hash/có HARD_CONSTRAINT — NHƯNG chỉ trong phạm vi MỘT pack's resolved manifest).
V2-06 **cố tình để ngỏ** một câu hỏi ngay trong doc comment của `skill.Selector`: "AND-across-dimensions
selector language is a context-assembler concern for a later task" — đây chính là V5-03. Phát hiện quan
trọng khác: `internal/domain/runtime.ContextSnapshot` ĐÃ TỒN TẠI (spike-era, shape khác hẳn go-core-spec
§4.6, gắn với `WorkflowPersistence` cũ) — nguy cơ trùng tên/shape thật, đã hỏi user và xác nhận V5-03
không đụng vào nó.

**3 quyết định user chốt tường minh (implement ĐÚNG theo lời user, không tự diễn giải lại):**

1. **Pure resolver, không persist gì, không đụng `runtime.ContextSnapshot`.** Trả về value object
   in-memory `ContextResolution`-shaped (đặt tên thật: `Resolution`) gồm: danh sách resource đã chọn
   theo thứ tự xác định, exact identity/provenance, lý do chọn/loại theo từng resource, kết quả tính
   budget, và phiên bản thuật toán/cost model. V5-04 sở hữu việc biến kết quả này thành snapshot durable
   (schema, canonical hash, binding Attempt, reload/integrity).
2. **Conflict trả typed error, không tự chuyển trạng thái runtime.** `HardConstraintConflictError{
   Conflicts []ConstraintConflict}` — mỗi conflict có constraint key/scope (`ResourceKey`), TOÀN BỘ
   resource va chạm, content hash, provenance; list sort ổn định; trả HẾT mọi conflict phát hiện được
   (không dừng ở conflict đầu tiên, không phụ thuộc thứ tự map iteration — implement bằng cách gom
   theo `ResourceKey` vào map trước, rồi sort kết quả cuối cùng, không bao giờ trả trực tiếp thứ tự từ
   map). Không gán TerminationReason tạm — ghi rõ trong doc comment đây là gap tường minh cho task nối
   dây admission (V5-08+) tự quyết định, đúng lời user "Hiện state–reason matrix chưa có reason rõ ràng
   cho context conflict, nên phần wiring sau cần bổ sung quyết định đó một cách tường minh."
3. **Byte-based budget, fail-closed với hard constraint, đơn vị KHÔNG BAO GIỜ gọi "token".**
   `Budget{MaxBytes, ReservedBytes}`; `available() = MaxBytes - ReservedBytes` (0 nếu Reserved ≥ Max,
   không âm). Cost tính trên UTF-8 byte length của `Candidate.Payload` thật (canonical payload sẽ
   render) qua interface `CostEstimator{Name() string; Cost(Candidate) uint64}` — implementation mặc
   định `ByteCostEstimator` đặt tên model `"UTF8_BYTES_V1"` (hằng số `CostModelUTF8BytesV1`), ghi vào
   `Resolution.CostModel` mọi lần — đổi sang tokenizer thật sau này chỉ cần đổi implementation, không đổi
   API `Resolve`. Thứ tự: resolve applicability + conflict TRƯỚC, budget SAU. Toàn bộ HARD_CONSTRAINT
   applicable là non-droppable; nếu tổng cost của riêng chúng vượt `available()` → trả
   `RequiredContextExceedsBudgetError` (không drop/truncate/vượt âm thầm). Phần còn lại sort theo
   Priority (REQUIRED_PROCEDURE→GUIDANCE→REFERENCE) rồi tie-break xác định
   (ResourceKey→OwnerVersionID→ContentHash), greedy include — **quan trọng: greedy KHÔNG dừng ở candidate
   đầu tiên không vừa** (một candidate nhỏ hơn ở sau vẫn được xét) — resource bị loại vì budget nhận
   `ReasonBudgetExceeded`.

**Quyết định tự đưa ra (KHÔNG nằm trong 3 câu hỏi, cần user review riêng):**
- **AND-across-dimensions, OR-within-one-dimension** cho `Selector.MatchesContext` (dimension nào
  Selector khai báo thì PHẢI khớp, dimension bỏ trống không hạn chế gì; trong 1 dimension chỉ cần giao
  nhau). Đây CHÍNH LÀ câu hỏi V2-06 cố tình để ngỏ cho task này — chọn AND (khắt khe hơn OR) vì lý do an
  toàn: đúng tinh thần "failure mode" đầu tiên HE-04 liệt kê là "route không điều kiện nên mọi resource
  đều nạp." Ghi rõ trong doc comment `MatchesContext` để dễ đảo ngược nếu user muốn OR.
- **Dedupe CHỈ theo full Identity tuple** (`OwnerVersionID+ResourceKey+ContentHash` giống hệt nhau mới
  coi là 1 candidate) — KHÔNG dedupe theo "cùng key+hash khác owner" (đó là phạm vi HE-04-M07 "single
  canonical rule", không nằm trong Nguồn của V5-03: HE-04-M05/HE-04-S03/HE-13-M08). Có test riêng
  (`TestResolve_NoConflict_SameContentHash_DifferentOwner`) chứng minh case này KHÔNG bị coi là conflict
  VÀ KHÔNG bị dedupe (cả hai đều được select, cost tính riêng từng cái) — nếu user muốn dedupe case này
  sau, đây là chỗ cần sửa.
- **`ResolutionContext.TaskKind/BlockKind/RiskClass` là string tự do, không phải enum đóng** — khớp
  đúng lý do `work.go`'s own comment tại `WorkItem.RiskLevel` ("no citation... enumerates a closed
  set... real resolver là task I/O-capable sau") — V5-03 không tự đặt ra vocabulary.
- **Package location: `internal/domain/contextassembler`**, KHÔNG import `internal/domain/skill`/`layer`
  trực tiếp — tự định nghĩa `Selector`/`Provenance` riêng (gần như trùng field với skill/layer) để giữ
  package này không phụ thuộc vào loại authoring cụ thể; caller (V5-04, khi thật sự gather candidate từ
  Definitions/Messages) tự map `skill.Resource`/`layer.Resource`/`message.Message` sang
  `contextassembler.Candidate`.
- **KHÔNG viết application-layer orchestration** (gather candidate thật từ `LoadVersion`/
  `ListMessagesForWorkItem`/`GetEffectiveComponentPackAssignment`) trong V5-03 — đọc kỹ lại thấy việc
  "gather" thuộc tự nhiên về V5-04 (task đó mới thật sự cần candidate thật để tính hash/persist), còn
  V5-03 chỉ cần thuật toán thuần nhận input đã có sẵn (test tự construct `Candidate` bằng tay, không cần
  sqlite/fake nào).

**File thay đổi:**
- `internal/domain/contextassembler/contextassembler.go` + `contextassembler_test.go` (mới) — package
  hoàn toàn mới, KHÔNG động tới ports/sqlite/fake nào (pure domain, không I/O).

**Verify (chạy local, Windows):**
```
go build ./...                                   # sạch
go vet ./internal/domain/contextassembler/...    # sạch
go test ./internal/domain/contextassembler/... -v -count=1   # 21 test PASS ngay lần đầu
go run ./cmd/docs-coverage-check                 # debt = 0
go test -count=1 ./...                           # toàn bộ ~56 package PASS
gofmt -l <2 file .go mới>                        # rỗng
```

**Việc còn lại:** mở PR, chờ CI 6/6, merge. Task kế tiếp: V5-04 (Persist ContextSnapshot trước
dispatch), phụ thuộc V5-03 + V4-04 — đây là task sẽ thật sự gather candidate + gọi `Resolve` + persist,
và cũng là nơi quyết định cách reconcile với `runtime.ContextSnapshot` cũ.
