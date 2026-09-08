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

## V5-04 — Persist ContextSnapshot trước dispatch

**Trạng thái:** code DONE, verify local PASS (toàn bộ suite + 1 bug thật tự bắt và tự sửa), chuẩn bị
mở PR.

**Khác biệt với V5-01..V5-03:** đây là task ĐẦU TIÊN trong V5 phải sửa CODE ĐÃ SHIP/ĐÃ TEST của V4
(schedule.go V4-04, finalize.go V4-06, execute.go V4-05, recovery_reaper.go V4-13) thay vì chỉ thêm
infrastructure mới. Trước khi code, đã dừng hỏi user 3 câu hệ trọng (naming collision với
`runtime.ContextSnapshot` cũ, có tái dùng field `ExecutionAttempt.ContextSnapshotID` đang dormant hay
không, đặt dispatch precondition ở đâu) vì fan-out cao và vì phải sửa code người dùng đã review kỹ.

**3 quyết định user chốt tường minh (implement ĐÚNG theo lời user):**
1. **Package mới hoàn toàn `internal/domain/contextsnapshot`, KHÔNG đụng `runtime.ContextSnapshot`/
   bảng `context_snapshots` cũ/call site checkpoint nào.** Bảng mới tên `attempt_context_snapshots`
   (phân biệt rõ khỏi bảng cũ). Package mới KHÔNG import `runtime` (tránh cycle vì `runtime.
   ExecutionAttempt` cần import NGƯỢC LẠI để tham chiếu `contextsnapshot.ID`) — `AttemptID` là type cục
   bộ trong chính package mới, cross-check ở application layer.
2. **Tái dùng field dormant `ExecutionAttempt.ContextSnapshotID`, đổi type thành
   `*contextsnapshot.ID`** (không thêm field thứ hai). Xác nhận TRƯỚC khi sửa: grep toàn repo confirm
   ZERO reference nào tới field NÀY cụ thể (mọi match cũ đều là `Checkpoint.ContextSnapshotID` — field
   khác, struct khác, không đụng). Snapshot/Attempt/EXECUTE_NODE job ghi trong CÙNG một scheduling
   transaction; thứ tự bắt buộc: insert Attempt (mang snapshot ID) TRƯỚC, insert snapshot SAU — vì
   `attempt_context_snapshots.attempt_id` có FK thật vào `execution_attempts(id)`, còn
   `execution_attempts.context_snapshot_id` CỐ TÌNH không có FK (tránh circular FK giữa 2 bảng tham
   chiếu lẫn nhau). Technical retry (finalize.go) VÀ recovery retry (recovery_reaper.go) đều clone
   snapshot từ attempt cũ sang AttemptID mới — không bao giờ share một snapshot row giữa 2 attempt.
3. **Dispatch precondition ở CẢ hai chỗ**: schedule.go bind snapshot atomic lúc tạo Attempt (khớp
   GC-INV-08 "pin trước attempt đầu tiên"); execute.go's `ExecuteNodeHandler` load lại + verify NGAY
   TRƯỚC khi gọi executor (snapshot tồn tại, `snapshot.AttemptID == attempt.ID`, Project/WorkItem khớp
   run, RevisionSet khớp attempt — recompute/tamper-check đã nằm sẵn trong `GetSnapshot`'s own
   `ports.ErrImmutableVersionConflict` path, không lặp lại ở đây) — dùng read-only transaction, ĐÓNG
   trước khi gọi executor (không giữ DB transaction xuyên qua external process call). Verify fail →
   typed error, KHÔNG finalize Attempt (giữ RUNNING, giống hệt cách cancel/lease-loss đã được để lại
   cho V4-13/coordinator khác xử lý) — KHÔNG tự bịa TerminationReason mới, để lại tường minh cho V5-08.

**BUG THẬT tự bắt và tự sửa khi chạy full suite (không phải review, không phải CI — chạy
`go test ./...` local):**
- **Vòng 1**: sau khi code xong, TOÀN BỘ suite pass NGAY LẦN ĐẦU trừ `internal/integration` — 2 test
  của V4-14's own runtime-engine gate FAIL (node bị stuck ở QUEUED, không bao giờ tới RUNNING/SUCCEEDED).
  Root cause: quyết định "chỉ sửa phạm vi được ghi" ban đầu cố tình KHÔNG update SELECT statement nào
  đọc lại `ExecutionAttempt` (chỉ update INSERT) — nhưng chính `verifyContextSnapshot` MỚI VIẾT lại cần
  đọc field đó! `loadExecutionAttemptByID` (dùng chung bởi CẢ `GetExecutionAttempt` LẪN
  `TransitionExecutionAttempt`) chưa bao giờ SELECT cột `context_snapshot_id` — mọi lần load lại đều
  trả về nil dù đã ghi giá trị thật lúc INSERT. Sửa: thêm cột vào SELECT + scan của
  `loadExecutionAttemptByID` (một chỗ sửa, fix cả 2 caller).
- **Vòng 2**: sau fix vòng 1, `TestRuntimeEngineGate_ConcurrentPoolsNoDuplicateExecution` pass nhưng
  `TestRuntimeEngineGate` (test đầy đủ, có fork/join/scope-expansion) vẫn fail — "scope_node" không
  bao giờ được tạo NodeRun. Debug bằng cách tạm thêm `fmt.Printf` vào `verifyContextSnapshot`'s error
  path, chạy 1 lần, thấy: một attempt với ID dạng "h2-586" (rất nhiều attempt number, chứng tỏ retry
  loop RẤT NHANH, không có backoff) liên tục fail vì "no bound context snapshot". Root cause thật: quyết
  định BAN ĐẦU (lúc mới thiết kế, TRƯỚC khi biết rõ blast radius) là hoãn việc clone snapshot ở
  `recovery_reaper.go`'s own retry path sang V5-13 ("fresh context rebuild" đã có sẵn trong scope V5-13).
  Nhưng THỰC TẾ: nếu MỘT attempt bị coi là "orphaned" bởi recovery reaper (do lease hết hạn — có thể xảy
  ra với BẤT KỲ lý do gì, kể cả tạm thời), reaper tạo attempt retry MỚI qua chính code path này — và nếu
  code path đó không bind snapshot, `verifyContextSnapshot` MỚI VIẾT sẽ CHẶN nó MÃI MÃI, tạo ra
  livelock: reaper retry → không snapshot → verify reject → RUNNING mãi → reaper coi là orphan lần nữa
  → retry lần nữa → ... Đây không phải edge case hoãn được — bất kỳ attempt nào "kẹt" vì BẤT KỲ lý do gì
  (kể cả bug ở nơi khác) sẽ kích hoạt reaper, và reaper retry không snapshot sẽ biến một sự cố tạm thời
  thành PERMANENT LIVELOCK. Sửa: áp dụng ĐÚNG pattern clone-on-retry (đã làm ở finalize.go) sang
  `recovery_reaper.go`'s own `retryAttempt` — đảo ngược quyết định "hoãn sang V5-13" ban đầu. Sau fix,
  `TestRuntimeEngineGate` pass NHANH HƠN hẳn (8.26s so với timeout ~13-15s trước đó) — xác nhận đúng root
  cause (loop retry rất nhanh trước đó đã bị dừng).
- **Bài học ghi lại cho task sau**: quyết định "hoãn X sang task khác vì X là edge case" cần re-kiểm
  bằng cách hỏi "nếu để trống, cái gì XẢY RA THẬT khi chạy full pipeline test, không phải suy luận trên
  giấy". `go test ./...` (không chỉ package đang sửa) là bước bắt buộc — nếu chỉ chạy
  `internal/app/runtime` (nơi tôi sửa trực tiếp) sẽ KHÔNG bao giờ phát hiện bug này (chỉ lộ ra ở
  `internal/integration`'s real workerpool.Pool end-to-end test, đúng tinh thần lecture 10 "chỉ full
  pipeline mới tính là verification thật").

**Quyết định tự đưa ra (không nằm trong 3 câu hỏi):**
- **ResourceRefs luôn rỗng ở V5-04** — MessageRefs là dữ liệu THẬT (toàn bộ `ListMessagesForWorkItem`
  của WorkItem, đúng thứ tự Sequence), nhưng gather Skill/Layer/Pack qua `contextassembler.Resolve`
  (V5-03) rõ ràng KHÔNG nằm trong "Thực hiện" line của V5-04 (chỉ nói schema/repository/hash/binding/
  precondition). Việc `contextsnapshot.NewSnapshot` cho phép refs rỗng (quyết định lúc viết domain
  package, xác nhận bằng test `TestNewSnapshot_EmptyRefsAllowed`) hoá ra QUAN TRỌNG hơn dự kiến — chính
  nhờ vậy mà TOÀN BỘ test suite V4 cũ (dùng fake UnitOfWork, WorkItem không có message nào) chạy qua
  được mà không cần sửa fixture nào.
- **`decideRetryOrExhaustion`/`retryAttempt` graceful-skip khi attempt cũ không có snapshot** (thay vì
  fail cứng) — giữ tương thích ngược với MỌI test cũ tự tạo `ExecutionAttempt` trực tiếp qua fake
  (không qua schedule.go) — không có gì để clone thì bỏ qua, đúng trạng thái attempt đó vốn đã có từ
  trước.
- **Không viết integration test riêng cho "restart"/"tamper" ở tầng ExecuteNodeHandler** — "tamper" đã
  có test tường minh ở repository layer (`TestContextSnapshotRepository_TamperDetected_ManifestHashMismatch`,
  sqlite thật); "restart" đã được `TestRuntimeEngineGate`'s own crash-recovery variant (real sqlite +
  real workerpool.Pool, restart thật) cover NGẦM nhưng THẬT (test này giờ pass, chứng minh snapshot sống
  sót qua crash+restart trong pipeline thật). "missing"/"mismatch" viết test tường minh mới ở
  `execute_contextsnapshot_test.go` (fake UnitOfWork, thêm 2 helper method test-only vào
  `fake.ContextSnapshotRepository`: `DeleteSnapshot`/`Overwrite`, mirroring precedent
  `JobsRepository.SetActiveLease`).
- **`fake.NodeExecutor` thêm field `Calls int`** — cần thiết để assert "executor spawn count bằng 0"
  đúng nghĩa đen lời user, trước đó fake này không đếm số lần gọi.

**File thay đổi:**
- MỚI: `internal/domain/contextsnapshot/{contextsnapshot.go,contextsnapshot_test.go}`,
  `internal/app/ports/contextsnapshot.go`, `internal/adapters/sqlite/migrations/0029_attempt_context_snapshots.sql`,
  `internal/adapters/sqlite/contextsnapshot_repository{.go,_test.go}`, `internal/app/ports/fake/contextsnapshot.go`,
  `internal/app/runtime/execute_contextsnapshot_test.go`
- SỬA: `internal/app/ports/unitofwork.go` (+ContextSnapshots()), `internal/adapters/sqlite/unitofwork.go`
  (wire), `internal/app/ports/fake/unitofwork.go` (wire fake), `internal/domain/runtime/runtime.go`
  (đổi type `ExecutionAttempt.ContextSnapshotID`), `internal/adapters/sqlite/schedule_node_run.go`
  (+cột INSERT), `internal/adapters/sqlite/finalize_execution_attempt.go` (+cột SELECT — chính là fix
  bug vòng 1), `internal/adapters/sqlite/db_test.go`/`unitofwork_test.go` (migration count 27→28),
  `internal/app/runtime/schedule.go` (bind snapshot lúc tạo attempt đầu), `internal/app/runtime/finalize.go`
  (clone snapshot lúc technical retry), `internal/app/runtime/execute.go` (dispatch verification — fix
  bug vòng 2 áp dụng ở đây), `internal/app/runtime/recovery_reaper.go` (clone snapshot lúc recovery
  retry — fix bug vòng 2), `internal/app/ports/fake/execution.go` (+field `Calls`).

**Verify (chạy local, Windows):**
```
go build ./...                                   # sạch
go vet ./...                                     # sạch (ngầm qua go test)
go run ./cmd/docs-coverage-check                 # debt = 0
go test -count=1 ./...                           # TOÀN BỘ ~62 package PASS (kể cả internal/integration,
                                                  #   sau 2 vòng tự sửa bug — xem narrative trên)
gofmt -l <19 file .go đổi/mới>                    # rỗng sau gofmt -w
```

**Việc còn lại:** mở PR, chờ CI 6/6, merge. Task kế tiếp: V5-05 (Production ProcessSupervisor
hardening) — độc lập với V5-01..04 (chỉ phụ thuộc V1 config), có thể làm ngay sau khi merge.

## V5-05 — Production ProcessSupervisor hardening

**Trạng thái:** DONE code+local verify, PR [#32](https://github.com/draculemihawk123-ai/agent-workflow/pull/32)
đã mở, chờ CI. Lưu ý: branch tạo TRƯỚC khi PR #31 (V5-04) merge (từ `d66c6b5`, không phải từ
`b9f5987`) — hợp lệ vì V5-05 không phụ thuộc code V5-04 (chỉ phụ thuộc V1 config), nhưng vì cả hai
task cùng append vào file checklist cá nhân này nên khi PR #31 merge xong, nhánh V5-05 phải merge
lại `origin/master` để lấy đúng narrative V5-04 thật (xem conflict resolution: file này TỪNG có một
bản backfill V5-04 tự viết lại từ memory sau compaction, đã BỎ vì bản gốc contemporaneous ở master
đầy đủ và chính xác hơn).

**Bối cảnh trước khi code:** research qua Explore agent xác nhận `internal/adapters/process.Supervisor`
là component PRODUCTION đã live thật (dùng bởi `claude.go`/`codex.go`/`readinesscheck/handler.go`) —
argv-only spawn/timeout/cancel một-process/env allowlist cơ bản, nhưng KHÔNG có: output bound, cwd
validation thật, graceful-then-force cancel, process-tree kill, isolation-enforcement-capability check.
`internal/app/config.Config` mới chỉ có `ProcessOutputLimit`/`ProviderExecutables` (V1-03) — chưa có
`EnvAllowlist`/`NetworkAccess`. `policy.IsolationTier{ENFORCED_ISOLATED, OPERATOR_TRUSTED_LOCAL}` đã
tồn tại (ADR-013), resolve được vào `ResolvedExecutionProfileV1.IsolationTier` ở `schedule.go`, nhưng
CHƯA có gì kiểm tra tier đó có thực sự enforceable hay không.

**2 câu hỏi hỏi user trước khi code, trả lời rất chi tiết cả 2:**
1. **Real enforcement vs. honest fail-closed:** chọn "No real enforcement — ENFORCED_ISOLATED luôn fail
   closed". Trả `ISOLATION_ENFORCEMENT_UNAVAILABLE` TRƯỚC `ProcessSupervisor.Start`, spawn count = 0,
   KHÔNG tự downgrade sang `OPERATOR_TRUSTED_LOCAL`. `OPERATOR_TRUSTED_LOCAL` là tier duy nhất chạy
   được, kèm cwd/env allowlist, timeout/cancel, hậu kiểm `scopeguard.ValidateDiffs` (đã build/wire sẵn
   ở `internal/app/worker/finalizer.go` từ SPK-07, không cần động tới). Windows Job Object hữu ích để
   quản lý/kill process tree nhưng tự nó không chặn network/filesystem nên không đủ để tuyên bố
   `ENFORCED_ISOLATED`.
2. **NetworkAccess default:** chọn luôn là `ALLOWED`. `RuntimeExecutionConfigProvider` phải mô tả
   capability THỰC TẾ platform enforce được, không phải mong muốn operator — khai `NONE` khi child
   process thực sự truy cập được network là false safety claim. KHÔNG thêm boolean operator-configurable
   riêng cho NetworkAccess ở task này; muốn công nhận firewall/sandbox ngoài sau này cần một cơ chế typed
   external-enforcement attestation có provenance/lifecycle rõ ràng, không phải field tự khai.

**Quyết định tự đưa ra (không nằm trong 2 câu hỏi trên):**
- **Port mới `ports.IsolationEnforcementChecker`** (`VerifyEnforceable(ctx, tier) error`), tách hẳn khỏi
  `RuntimeExecutionConfigProvider` (2 trục độc lập: IsolationTier resolve từ Policy pin per-node, không
  phải từ composition-root config). Production impl `process.IsolationChecker` (ENFORCED_ISOLATED luôn
  reject, OPERATOR_TRUSTED_LOCAL luôn accept), fake ở `ports/fake/isolation.go`. Lý do bắt buộc phải có
  port riêng (không thể để V5-08 gọi thẳng `internal/adapters/process`): `TestDomainAppNeverImportAdapters`
  (`internal/archtest/boundary_test.go`) cấm cứng `internal/app/...`/`internal/domain/...` import
  `internal/adapters/...`, kể cả transitively.
- **V5-05 chỉ build primitive + tự chứng minh contract riêng** (spy `ProcessSupervisor` đếm call,
  assert 0 khi ENFORCED_ISOLATED) — KHÔNG wire vào Attempt/BLOCKED state machine thật (đó là V5-08's own
  "isolation profile admission theo ADR-023" theo đúng roadmap; vocabulary `errorcode.
  CodeIsolationEnforcementUnavailable`/`runtime.TerminationReasonIsolationEnforcementUnavailable`/
  `work.BlockerIsolationEnforcementUnavailable` đã tồn tại sẵn từ phase trước, chỉ chưa có caller thật).
- **Cross-platform process-tree kill:** Unix dùng process group (`Setpgid: true`, signal `-pid`);
  Windows dùng Job Object thật (`CreateJobObject` + `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` +
  `AssignProcessToJobObject` qua `OpenProcess` theo pid) + `CREATE_NEW_PROCESS_GROUP` cho
  `GenerateConsoleCtrlEvent(CTRL_BREAK_EVENT, pid)` graceful. File tách theo build tag
  `processtree_windows.go`/`processtree_other.go`, đúng convention `canonical_windows.go`/
  `canonical_other.go` đã có sẵn ở `gitworktree`/`repoprobe`. Đã tự chạy test thật trên máy Windows này
  (không chỉ chờ CI) — `TestSupervisorCancelKillsDescendantProcess` (grandchild qua job object) PASS
  thật.
- **Graceful-then-force:** KHÔNG dùng `exec.CommandContext`'s auto-kill mặc định (chỉ kill 1 process) —
  tự quản lý qua goroutine `Wait()` riêng + `select` đua giữa `waitDone`/`deadline.Done()`/
  `cancelRequested`, gửi graceful signal trước, đợi `GracePeriod` (mặc định 5s, field mới trên
  `ports.ProcessSpec`), hết hạn mới force-kill cả tree.
- **Bounded output:** `ports.ProcessSpec.OutputLimitBytes`/`ports.ProcessResult.OutputTruncated` (field
  mới, additive, mọi struct literal hiện có đều keyed nên không vỡ). Mặc định 10 MiB khi caller để 0 —
  ĐỔI hành vi cũ của `readinesscheck` (trước đây unbounded `bytes.Buffer`) nhưng vô hại thực tế (output
  probe luôn rất nhỏ).
- **cwd validation:** bắt buộc absolute path + tồn tại + là directory, check TRƯỚC `s.reserve`/spawn.
  **Regression tự phát hiện khi chạy full suite:** `internal/spikeacceptance` SPK-11/SPK-12 dùng
  `WorkingDirectory: "."` (placeholder "chỗ nào cũng được" cho fake CLI) — vỡ ngay vì "." không phải
  absolute. Fix: SPK-11 đổi sang `os.TempDir()`, SPK-12 đổi sang `tempDir` (biến `os.MkdirTemp` đã có
  sẵn trong scope) — đúng tinh thần "chỉ cần 1 thư mục thật tồn tại", không đổi ý nghĩa scenario. Đã
  audit toàn bộ codebase (`grep WorkingDirectory:`) để loại trừ chỗ khác bị ảnh hưởng — riêng
  `internal/domain/readiness.CommandSpec.WorkingDirectory` CỐ Ý là relative path (join với worktree
  root thật ở `readinesscheck/handler.go`'s `runCommand`), không đụng tới.
- **`config.Config.EnvAllowlist []string` (field mới):** theo đúng pipeline `Defaults/Overrides/Apply/
  file source/Validate/Dump` sẵn có — file-only (không env/flags form, giống `ProviderExecutables`),
  replace-wholesale (không merge như map) vì allowlist một source sau phải là danh sách đầy đủ, không
  phải "cộng thêm".

**File thay đổi chính:** `internal/app/ports/agent.go` (thêm `GracePeriod`/`OutputLimitBytes` vào
`ProcessSpec`, `OutputTruncated` vào `ProcessResult`), `internal/app/ports/isolation.go` (port mới),
`internal/app/ports/fake/isolation.go` (fake mới), `internal/adapters/process/{isolation,
isolation_test,processtree,processtree_other,processtree_windows,boundedwriter,executionconfig,
executionconfig_test}.go` (mới), `internal/adapters/process/supervisor.go` (viết lại Run/Cancel),
`internal/adapters/process/supervisor_test.go` (thêm 7 test mới), `internal/app/config/{config,
sources,validate,dump}.go` + test tương ứng (EnvAllowlist), `internal/spikeacceptance/
{spk11,spk12}_scenario.go` (fix regression cwd).

**Verify:**
```
go build ./...                                       # sạch
go vet ./...                                          # sạch
go test ./internal/adapters/process/... -v -count=1   # 16 test PASS (kể cả cwd/oversized-output/
                                                       #   descendant-kill/isolation contract)
go test ./internal/adapters/process/... -count=5      # ổn định, không flake (không có cgo trên máy
                                                       #   này nên -race không chạy được local, để CI)
go test ./internal/app/config/... -v -count=1         # PASS (kể cả EnvAllowlist mới)
go test -count=1 ./...                                # toàn bộ PASS (sau khi fix SPK-11/SPK-12)
go run ./cmd/docs-coverage-check                      # debt = 0
```

**BUG THẬT thứ hai, chỉ lộ ra ở CI job "spike acceptance" thật (KHÔNG lộ qua `go test`) — fix
`os.TempDir()`/`tempDir` đầu tiên cho SPK-11/SPK-12 (thay `WorkingDirectory: "."`) tự nó lại SAI:**
- **Triệu chứng:** PR #32 mở, CI fail cả `spike acceptance (ubuntu-latest)` LẪN `spike acceptance
  (windows-latest)` với y hệt lỗi trên cả hai platform: `run scenario SPK-11: spk11: codex start:
  start executable "bin/fake-codex": fork/exec bin/fake-codex: no such file or directory` (Windows:
  "The system cannot find the path specified"). `internal/spikeacceptance`'s own `go test` (chạy local
  ngay trước khi mở PR) PASS sạch — không lộ bug này, vì test đó tự build binary vào đường dẫn TUYỆT
  ĐỐI, không đi qua flow CLI thật `cmd/agentkit-spike acceptance --full` mà CI job "spike acceptance"
  chạy với đường dẫn TƯƠNG ĐỐI (`--fake-codex "bin/fake-codex"`, đúng y hệt cách workflow YAML gọi).
- **Root cause thật (Go/OS semantics, không phải flake):** `ports.ProcessSpec.Executable` tương đối
  (`"bin/fake-codex"`, không qua PATH vì có dấu `/`) được `os/exec` resolve KHÔNG PHẢI so với cwd của
  process gọi, mà so với `cmd.Dir` (chính là `WorkingDirectory` mới) SAU KHI OS đã chdir sang đó —
  chdir xảy ra TRƯỚC khi resolve/exec executable tương đối. `WorkingDirectory: "."` cũ vô tình đúng vì
  "." tương đương "giữ nguyên cwd hiện tại" nên không đổi gì; đổi sang `os.TempDir()`/`tempDir` (một thư
  mục KHÁC) làm executable tương đối "bin/fake-codex" bị tìm trong CHÍNH thư mục temp đó — không tồn
  tại. Root cause của quyết định sai: chỉ nghĩ "cần 1 thư mục tuyệt đối tồn tại thật", không xét
  `Executable` của scenario này CŨNG tương đối và neo vào cùng cwd mà `WorkingDirectory` cũ (".") từng
  bảo toàn.
- **Fix đúng:** dùng `os.Getwd()` (cwd THẬT của process gọi) thay vì một thư mục không liên quan —
  bảo toàn đúng ý nghĩa gốc của "." trong khi vẫn thoả yêu cầu absolute path mới. SPK-11 (`spk11Request`,
  không có error return, theo đúng precedent bỏ qua lỗi `os.Getwd()` đã có sẵn ở
  `internal/adapters/providers/fixtures.go`) và SPK-12 (`runSPK12Scenario`, có error return, xử lý lỗi
  tường minh theo đúng style hàm này). `tempDir` (từ `os.MkdirTemp`) ở SPK-12 vẫn giữ nguyên cho mục
  đích KHÁC (đường dẫn sqlite db) — chỉ đổi 2 chỗ `WorkingDirectory` thôi.
- **Verify thật, không chỉ suy luận:** build cả 5 binary CLI thật (`agentkit-spike`/`fake-claude`/
  `fake-codex`/`spike-helper`/`spike-worker`) vào một thư mục cô lập, chạy ĐÚNG lệnh CI chạy
  (`./bin/agentkit-spike acceptance --full --assessment --fake-claude "bin/fake-claude" --fake-codex
  "bin/fake-codex" ...` từ chính thư mục đó, đường dẫn tương đối y hệt workflow YAML) — SPK-01..12,14
  PASS, SPK-13 FAIL (đúng kỳ vọng: SPK-13 thật cần evidence CẢ HAI platform, job riêng
  "cross-platform semantic diff" mới có; trong "spike acceptance" một platform SPK-13 luôn FAIL vô hại,
  exit code toàn bộ vẫn 0 — xác nhận khớp lần CI xanh trước đó của chính PR này).
- **Bài học ghi thêm:** "chỉ cần absolute + tồn tại" không đủ để chọn giá trị thay thế cho một placeholder
  — phải hiểu ĐẦY ĐỦ TẤT CẢ trường liên quan trong cùng request (ở đây là `Executable` VÀ
  `WorkingDirectory` tương tác qua đúng semantics chdir-trước-resolve của OS), và một lần `go test`
  xanh không chứng minh được nếu chính code path CI thật sự chạy (CLI binary, đường dẫn tương đối) khác
  với code path test framework tự dựng (binary tuyệt đối). Đã tự phát hiện và tự sửa TRƯỚC KHI hỏi
  user, verify lại bằng cách tái tạo chính xác lệnh CI chạy cục bộ — không chỉ tin `go test` xanh lần
  nữa.

**CI PR #32:** vòng đầu `contract (windows-latest)` fail ở stress write-lease diagnostic (V0-11A,
"durable job lease is no longer authoritative") — flake đã biết, tiền lệ V1/V4-08, rerun 1 lần qua.
Nhưng cùng lúc rerun đó lộ ra bug thật ở trên (`spike acceptance` cả 2 platform) vì lần CI ĐẦU TIÊN,
`contract (windows-latest)` fail sớm nên `spike acceptance`/`Linux race and stability` bị skip toàn bộ
— chưa từng chạy thật tới lượt SPK-11. Sau khi sửa bug thật + push lại, chờ CI vòng mới. Job "Linux race
and stability" cũng fail 1 lần ở `TestEndToEnd_PartialFailure_OneReadyOneFailed_SetBlockedRowsKept`
(`internal/app/workspaceprovision`) — flake đã biết, tiền lệ V4-10 (PR #19), không đụng file nào trong
diff (`git diff --stat` xác nhận rỗng), kỳ vọng xanh sau rerun.

**Việc còn lại:** commit, push, mở PR, chờ CI 6/6, merge.

## V5-06 — Claude adapter production contract

**Trạng thái:** code + local verify DONE (toàn bộ suite xanh sau khi tự bắt và tự sửa 3 vấn đề thật —
xem bên dưới), chuẩn bị mở PR. Branch off master sau khi PR #32 (V5-05) merge.

**Nghiên cứu trước khi code** (Explore agent + đọc trực tiếp, kết quả khác với giả định ban đầu ở
nhiều điểm):
- `claude.go`/`codex.go`'s `Capabilities()` là 100% static/hardcoded từ trước tới giờ — không spawn gì
  cả. Chính `cmd/agentkit/adapter.go`'s own doc comment đã tự nhận đây là gap CỐ Ý để lại cho V5-06/07.
- "AdapterBuildVersion registration" (V2-07A/ADR-022) đã build ĐẦY ĐỦ từ trước — domain/app/sqlite/CLI
  4 tầng, ~40 test. "Admission" (so pinned build với thực tế lúc dispatch) KHÔNG tồn tại ở đâu cả —
  chỉ có vocabulary rỗng (`ADAPTER_BUILD_DRIFT` error code/termination reason/blocker type). Roadmap tự
  giao việc đó cho V5-08, KHÔNG phải V5-06. → V5-06 chỉ cần làm `Capabilities()` sống thật, không cần
  xây cơ chế admission.
- HE-05-M01 ("resume MUST không phụ thuộc duy nhất vào session ID") ĐÃ được thoả ở tầng platform từ
  trước: `docs/architecture/03-system-architecture.md` nói thẳng "Alpha orchestrator không gọi
  Resume"; `worker.StartFreshFromLatestCheckpoint` luôn resume qua ContextSnapshot/checkpoint, không
  bao giờ qua session ID; `TestSPK12InvalidProviderSessionNeverBlocksRecovery` đã chứng minh việc này
  end-to-end với adapter thật. → Không cần sửa gì ở `Resume()` cả, chỉ cần giữ nguyên.
- Malformed JSONL fail-closed, Start/Resume/Cancel, canonical event mapping: đã build đầy đủ và có
  test từ trước — không đụng.
- `AgentEvent.ProviderMetadata` chỉ từng được set field `"raw_type"` (breadcrumb chẩn đoán), không có
  consumer nào đọc nó ở `internal/app`/`internal/domain` cả (đúng vì V5-08A — consumer thật đầu tiên —
  chưa build). "Raw metadata không route state" đã đúng NHƯNG chỉ vì chưa có ai đọc, không phải vì có
  chặn tường minh.

**Quyết định tự đưa ra (không có câu hỏi lớn nào cần hỏi user — đây là task khép kín, rủi ro thấp, dễ
đảo ngược, có escape hatch qua config field, khác hẳn tính chất các fork ở V5-03..05):**
- **Cơ chế probe: spawn `<executable> --version` riêng biệt** (không tái dùng protocol stream-json
  đang có) qua package mới `internal/adapters/providers/internal/versionprobe` (dùng chung được cho cả
  V5-07/Codex sau này — roadmap TỰ nói V5-07 "cùng contract/semantics như Claude", không phải suy đoán
  trước). Fail-closed: spawn lỗi/exit khác 0/timeout/output rỗng đều trả error, `Capabilities()` không
  bao giờ fallback về giá trị cũ đoán mò.
- **`claude.Config` thêm `VersionArgs []string`** (mặc định `["--version"]`, override được nếu CLI
  thật dùng flag khác — không có ground-truth về flag thật của Claude CLI trong repo này, nhưng field
  configurable là escape hatch đủ an toàn) và `VersionEnvironment map[string]string` (chỉ cần cho test
  fixture, production để nil).
- **`WorkingDirectory` của probe = `os.Getwd()`** (không phải `os.TempDir()` hay bất kỳ thư mục không
  liên quan nào) — ÁP DỤNG ĐÚNG bài học vừa rút ra ở V5-05 (relative executable path resolve theo
  `cmd.Dir` MỚI, không phải cwd gọi ban đầu).
- **`TestedCLIVersion` đổi nghĩa**: từ "hardcoded constant claim" sang "giá trị probe được ngay bây
  giờ" — xoá hẳn constant `TestedCLIVersion` cũ (grep xác nhận không ai reference ngoài chính file nó
  khai), sửa doc comment ở `ports.AgentCapabilities` cho rõ nghĩa mới. `AdapterVersion`/`ProtocolVersion`
  giữ nguyên là constant tĩnh (mô tả năng lực CODE adapter, không phải năng lực executable đang cấu
  hình).
- **KHÔNG đụng `codex.go`** — giữ nguyên static/hardcoded, để lại đúng cho V5-07 tái dùng
  `versionprobe` (đúng kỷ luật "task nào sở hữu thì task đó sửa" xuyên suốt cả session).
- **Thêm archtest guard mới** (`TestProviderAdaptersNeverImportAppOrchestrationOrPersistence`) — Explore
  agent tự phát hiện đây là gap thật (exit criteria "không import app orchestrator/persistence" trước
  giờ chỉ đúng "tình cờ", chưa có gì enforce). Cho phép `internal/domain/*` (thuần data/logic) và
  `internal/app/ports` + `internal/app/redact` (redact là transitive dependency thật của
  `ports/artifact.go`, phát hiện khi chạy test lần đầu bị false-positive).

**3 vấn đề thật tự phát hiện và tự sửa (không phải review, tự chạy full suite bắt được cả 3):**
1. **Bug ở chính test mới của mình**: `TestClaudeCapabilitiesProbesRealVersion` fail với
   `TestedCLIVersion = "PASS"` thay vì giá trị fake mong đợi. Root cause: probe spawn
   `<test-binary> -test.run=TestProviderHelperProcess -- claude --version` nhưng KHÔNG set env
   `AGENTKIT_PROVIDER_HELPER=1` — `TestProviderHelperProcess` thấy env thiếu nên no-op ngay, để
   `go test` chạy CHÍNH NÓ như một test suite bình thường (chỉ có đúng 1 test, pass ngay), và output
   mặc định không-verbose của `go test` chính là chuỗi "PASS" — bị probe bắt nhầm làm "version quan
   sát được". Fix: thêm `VersionEnvironment map[string]string` vào `claude.Config`, thread qua
   `versionprobe.Probe`'s param mới `env map[string]string`, set đúng trong test fixture.
2. **Regression thật ở `cmd/agentkit/adapter_test.go`** (12 test, KHÔNG phải test mới viết): toàn bộ
   dùng `writeAdapterExecutable` ghi BYTES TUỲ Ý (chuỗi như "binary-v1") vào file đánh dấu executable —
   trước giờ AN TOÀN vì `Capabilities()` chưa từng thật sự chạy nó. Giờ `Capabilities()` chạy thật
   `<file> --version` → lỗi "This version of %1 is not compatible..." (Windows PE loader từ chối file
   không phải PE hợp lệ) trên MỌI test dùng probe qua CLI `adapter probe`/`adapter register`. Fix: thêm
   `TestMain(m *testing.M)` mới cho package (chưa từng có) chặn TRƯỚC khi `go test`'s flag parsing chạy
   — nếu `os.Args` đúng `["<self>", "--version"]` thì in version cố định rồi exit, còn lại chạy suite
   bình thường; `writeAdapterExecutable` đổi sang COPY chính binary test này (`os.Executable()`) rồi
   APPEND marker phân biệt ở cuối (kỹ thuật an toàn, chuẩn — nhiều self-extracting installer làm y hệt:
   loader chỉ đọc tới hết section header đã khai, không quan tâm bytes thừa sau đó). 2 chỗ
   `os.WriteFile(executablePath, []byte("binary-v2..."), ...)` trực tiếp (mô phỏng "executable đổi
   sau probe") cũng phải đổi sang cùng kỹ thuật (`adapterExecutableFixtureBytes`) — nếu không, file
   "swap" thứ hai vẫn là garbage, vẫn fail giống hệt.
3. **False positive ở chính archtest mới viết**: `TestProviderAdaptersNeverImportAppOrchestrationOrPersistence`
   fail ngay lần chạy đầu vì `internal/app/ports/artifact.go` tự nó import `internal/app/redact` (dùng
   cho `Sensitivity`/`Matcher`) — transitive dependency THẬT của chính `internal/app/ports` (package
   DUY NHẤT được phép), không phải vi phạm. Fix: allowlist thêm `internal/app/redact` với comment giải
   thích rõ lý do (utility thuần, không side-effect, không phải orchestration/persistence).

**File thay đổi chính:** `internal/adapters/providers/internal/versionprobe/{versionprobe,
versionprobe_test}.go` (mới, dùng chung tương lai cho V5-07), `internal/adapters/providers/claude/
claude.go` (Capabilities() viết lại, Config thêm 2 field, xoá constant TestedCLIVersion),
`internal/adapters/providers/fixtures.go` (RunFakeProviderCLI thêm nhánh `--version` trước mọi
mode-dispatch, `FakeCLIVersion` helper mới), `internal/adapters/providers/contract_test.go` (2 test
mới + fix `providerCases()`'s claude case thiếu `VersionEnvironment`), `internal/app/ports/agent.go`
(doc comment `TestedCLIVersion` viết lại), `internal/archtest/boundary_test.go` (guard mới),
`cmd/agentkit/adapter.go` (sửa doc comment lỗi thời), `cmd/agentkit/adapter_test.go` (`TestMain` mới +
`writeAdapterExecutable`/`adapterExecutableFixtureBytes` viết lại + 2 call site "swap executable" đổi
theo).

**Verify:**
```
go build ./...                                                    # sạch
go vet ./...                                                      # sạch
go test ./internal/adapters/providers/... -v -count=1             # PASS (kể cả 2 test probe mới)
go test ./internal/adapters/providers/internal/versionprobe/...   # PASS (5 test, package mới)
go test ./cmd/agentkit/... -count=1                                # PASS (12 test cũ + TestMain mới,
                                                                    #   sau khi fix regression #2)
go test ./internal/archtest/... -v -count=1                        # PASS (8 test, kể cả guard mới)
go test -count=1 ./...                                             # toàn bộ ~70 package PASS
go run ./cmd/docs-coverage-check                                  # debt = 0
```

**Việc còn lại:** commit, push, mở PR, chờ CI 6/6, merge. Task kế tiếp: V5-07 (Codex adapter production
contract) — tái dùng nguyên `versionprobe`, áp dụng y hệt pattern cho `codex.go`.

## V5-07 — Codex adapter production contract

**Trạng thái:** code + local verify DONE, chuẩn bị mở PR. Branch off master sau khi PR #33 (V5-06)
merge — LẦN NÀY tạo branch TRƯỚC khi viết code (rút kinh nghiệm từ lỗi quy trình ở V5-06: lúc đó lỡ
commit thẳng vào local master, tự phát hiện và tự sửa trước khi push, nhưng đáng lẽ không nên xảy ra).

**Nghiên cứu trước khi code** (đọc trực tiếp `AK-ARCH-016`/`GC-ACC-12`/`HE-11-M08`, không cần Explore
agent lần này vì V5-06 đã tự nghiên cứu phần lớn context dùng chung): cả 3 nguồn đều xoay quanh MỘT chủ
đề — "cùng workflow/scenario chạy qua Claude/Codex mà scheduler/domain không đổi code, event schema
chuẩn hoá chung." Đây LÀ đúng những gì `contract_test.go`'s `providerCases()` + `assertCanonicalEvents`
đã kiểm chứng từ trước (chạy chung 1 bộ test cho cả 2 provider qua bảng `providerCase`), và
`assertCanonicalEvents` vốn đã CHỈ CHECK "7 kind bắt buộc phải CÓ MẶT" (không check khớp tuyệt đối theo
thứ tự/đủ đúng danh sách) — nghĩa là "normalized differences allowlist" (Codex phát thêm
`STATUS_CHANGED` mà Claude không có) đã được dung nạp SẴN, không cần cơ chế allowlist mới. Không tìm
thấy tài liệu nào mô tả một cơ chế allowlist RIÊNG BIỆT khác — kết luận: V5-07 không cần xây gì mới cho
bullet này.

**Kết luận phạm vi**: V5-07's TOÀN BỘ phần việc thật là ÁP DỤNG Y HỆT pattern V5-06 vừa xây (probe
`--version` qua `versionprobe` dùng chung) sang `codex.go` — không có fork thiết kế mới nào, không cần
hỏi user câu nào (mọi quyết định đã chốt ở V5-06, task này chỉ lặp lại đúng công thức đã duyệt).

**File thay đổi:** `internal/adapters/providers/codex/codex.go` (y hệt cấu trúc sửa ở `claude.go`: xoá
constant `TestedCLIVersion`, thêm `VersionArgs`/`VersionEnvironment` vào `Config`, viết lại
`Capabilities()` gọi `versionprobe.Probe`, thêm `capabilityProbeProcessID()`), `internal/adapters/
providers/contract_test.go` (thêm `VersionEnvironment` vào `providerCases()`'s codex case + 2 test mới
`TestCodexCapabilitiesProbesRealVersion`/`TestCodexCapabilitiesFailsClosedWhenProbeFails`, mirror y hệt
2 test claude ở V5-06), `cmd/agentkit/adapter.go` (sửa doc comment: bỏ câu "codex vẫn hardcoded" vì giờ
không còn đúng nữa). `internal/adapters/providers/internal/versionprobe` KHÔNG đổi gì — dùng nguyên,
đúng như thiết kế "dùng chung cho V5-07" đã ghi ở V5-06.

**Không có bug thật nào tự phát hiện lần này** — khác V5-06 (3 vấn đề thật) và V5-05 (2 bug thật) —
vì `cmd/agentkit/adapter_test.go` chưa từng có fixture nào test Codex qua CLI probe/register (chỉ test
claude), nên không có regression kiểu "writeAdapterExecutable" nào bị lộ ra; và `fixtures.go`'s
`RunFakeProviderCLI`'s nhánh `--version` (viết ở V5-06) đã tổng quát theo `provider string` ngay từ
đầu, không cần sửa gì thêm cho Codex. Đây là tín hiệu tốt cho thấy quyết định "xây `versionprobe` dùng
chung ngay từ V5-06" là đúng.

**Verify:**
```
go build ./...                                          # sạch
go vet ./...                                            # sạch
go test ./internal/adapters/providers/... -v -count=1   # PASS (4 test capabilities mới: claude+codex
                                                         #   × probe-thật + fail-closed)
go test -count=1 ./...                                  # toàn bộ ~70 package PASS
go run ./cmd/docs-coverage-check                        # debt = 0
```

**Việc còn lại:** commit, push, mở PR, chờ CI 6/6, merge. Task kế tiếp: V5-08 (AGENT admission và
execution envelope) — phụ thuộc V5-04, V5-06, V5-07, V3-09, V4-05 (tất cả đã xong) — đây là task sẽ
thật sự wire admission check (isolation, adapter build drift, capability, multi-repo write grant) vào
Attempt/BLOCKED state machine, dùng lại `ports.IsolationEnforcementChecker` (V5-05) và
`ports.AgentExecutor.Capabilities()` (V5-06/07) làm input.

## V5-08 — AGENT admission và execution envelope

**Trạng thái:** code + local verify DONE (toàn bộ suite xanh, kể cả 2 bug thật tự bắt được), chuẩn bị
mở PR. Branch tạo TRƯỚC khi viết code (đúng kỷ luật đã tự sửa ở V5-07). Task lớn và phức tạp nhất từ
đầu V5 tới giờ — 4 câu hỏi lớn hỏi user trước khi code, cả 4 câu đều có correction sâu.

**Nghiên cứu trước khi code** (Explore agent, kết quả khác giả định ban đầu ở NHIỀU điểm quan trọng):
- BLOCKED state (`ExecutionAttemptBlocked`), cả 4 `TerminationReason` VÀ cả 4 `work.BlockerType` cho
  admission blocker group ĐÃ TỒN TẠI SẴN từ phase trước (chỉ thiếu người sinh ra chúng thật) — V5-08
  hoàn toàn KHÔNG cần thêm domain type/migration nào cho việc này.
- `FinalizeExecutionAttempt` KHÔNG dùng lại được cho `QUEUED→BLOCKED` (hardcode
  `ExpectedState: RUNNING`) — cần hàm chị em mới, không sửa hàm cũ.
- `ports.AgentWorkspaceMount`/`WorkspaceAccess` tồn tại từ lâu nhưng CHƯA CÓ NGƯỜI SINH RA nào cả — V5-08
  là producer ĐẦU TIÊN.
- KHÔNG có production `ports.NodeExecutor` nào cả (chỉ có fake) — adapter Claude/Codex thật (V5-06/07)
  hiện chỉ được wire vào `cmd/agentkit adapter probe/register`, chưa bao giờ vào đường dispatch thật.
- ADR-022 tự trả lời rõ: adapter-build drift PHẢI là re-hash/re-probe SỐNG (không phải re-fetch DB —
  Build row content-addressed/bất biến nên re-fetch không bao giờ phát hiện được drift).

**4 câu hỏi hỏi user trước khi code, cả 4 đều có correction/bổ sung sâu so với đề xuất ban đầu của tôi:**
1. **Phạm vi executor bridge:** user chọn "Chỉ xây admission probe" — KHÔNG xây NodeExecutor→AgentExecutor
   bridge thật (để lại cho lúc có event sink/checkpoint/fenced finalize, làm ngay sẽ lấn V5-08A/B).
   **Correction quan trọng:** `AgentCapabilities` hiện tại (protocol/CLI version + capability flags)
   KHÔNG đủ để chứng minh exact BuildID — phải đo lại FULL `CandidateTuple` (content hash + capability
   manifest hash + OS/toolchain sống) rồi so `ID()`, không phải chỉ so `Capabilities()`. Cũng lưu ý:
   `agentregistry.Registry.Resolve()` hiện tại CACHE capability lúc khởi tạo — không dùng được thẳng cho
   probe admission (cần probe SỐNG mỗi lần).
2. **Cấu trúc transaction 2 phase:** user xác nhận đúng phương án đề xuất (fold vào 1 transaction quyết
   định) nhưng bổ sung chi tiết bắt buộc: đổi tên `claimRunning`→`admitOrClaimRunning` trả về
   NOOP|BLOCKED|RUNNING rõ ràng; cancellation/idempotency guard chạy TRƯỚC danh sách admission (không
   tính vào priority); re-verify probe input (adapter build) vẫn khớp pin đã đọc ở preflight; nếu BLOCKED
   phải đồng bộ CẢ Attempt + NodeRun + WorkItemBlocker trong CÙNG transaction, không được để NodeRun mắc
   kẹt QUEUED. Chỉ ra rõ: KHÔNG dùng transaction admission riêng vì tạo race với QUEUED→RUNNING hiện tại
   ở `execute.go` (đã chỉ đúng dòng).
3. **Priority khi nhiều check cùng fail:** giữ nguyên đề xuất (isolation > adapter drift > capability >
   multi-repo-write), nhưng yêu cầu encode ở MỘT CHỖ DUY NHẤT (không rải if), chỉ persist đúng 1 reason/1
   blocker, và có test table-driven cho toàn bộ tổ hợp.
4. **Mở rộng `ResolvedExecutionProfileV1`:** user BÁC BỎ hoàn toàn đề xuất thêm field
   `RequiredCapabilities` vào type đã "khoá" này. **Correction:** tiền đề "chưa pin ở đâu" SAI —
   `RequiredCapabilities` nằm trong TOÀN BỘ `AgentProfileDocument`, mà compiled hash của nó ĐÃ được pin
   trong `Executor.CompiledHash`. Cách đúng: admission tự `LoadVersion` lại đúng version đã pin, verify
   `DefinitionID`/`CompiledHash` khớp, rồi decode `RequiredCapabilities` từ ĐÓ — không denormalize field
   mới vào V1. Muốn denormalize vì hiệu năng sau này thì làm `ResolvedExecutionProfileV2`, không sửa V1.

**2 bug thật tự phát hiện khi chạy full suite thật (KHÔNG lộ qua unit test riêng gói `runtime`, giống
hệt bài học V5-04 — chỉ `internal/integration`'s real sqlite end-to-end mới lộ ra):**
1. **`ports.AgentWorkspaceMount` không marshal được**: `WorkspaceHandle.MarshalText()` tự chặn cứng khi
   handle rỗng ("workspace handle is empty") — ĐÚNG NHƯ THIẾT KẾ (bảo vệ không cho handle rỗng bị âm
   thầm serialize), nhưng va ngay vào quyết định của tôi ở câu hỏi 1 (để `Handle` rỗng vì chưa xây
   bridge thật). Fix: định nghĩa type `persistedEnvelopeMount` riêng (chỉ `RepositoryID`+`Access`) để
   PERSIST, giữ nguyên `buildExecutionEnvelope` trả về đúng `[]ports.AgentWorkspaceMount` thật (để một
   caller trong bộ nhớ dùng thẳng được sau này).
2. **`RecordDecisionArtifact` KHÔNG idempotent** (tự đọc doc comment của chính port: "plain append,
   không update/delete bao giờ") — nhưng tôi lại gọi nó từ `admitOrClaimRunning`, hàm chạy MỘT LẦN MỖI
   ATTEMPT, trong khi envelope key theo NodeRunID (một NodeRun có thể có NHIỀU Attempt qua retry) → lần
   retry thứ 2 insert trùng ID, lỗi "already exists", `Handle()` trả error, job bị retry vô hạn →
   `TestRuntimeEngineGate` treo giống hệt kiểu livelock ở V5-04's Bug #2 (13s thay vì 5.7s bình thường).
   Debug bằng in tạm (xoá ngay sau khi chẩn đoán, đúng kỹ thuật đã dùng ở V5-04). Fix: check-tồn-tại
   trước khi insert (idempotent tại call site, không sửa port).
3. **(Phát hiện phụ, ngoài phạm vi V5-08, đã spawn task riêng)**: `TestAdapterRegister_DriftCreatesNewBuild`
   (V5-06's own test) timeout 5s một lần khi chạy TOÀN BỘ `go test ./...` (nhiều package chạy song song
   gây tranh chấp CPU) — pass sạch khi chạy riêng `cmd/agentkit`. Không phải regression từ V5-08 (không
   đụng file nào của claude.go/codex.go). Đã tạo task riêng để tăng `defaultVersionProbeTimeout`, không
   sửa trong PR này để giữ diff đúng phạm vi.

**File thay đổi chính:** `internal/app/runtime/admission.go` (mới, toàn bộ 4 check + envelope resolver +
priority aggregation), `internal/app/runtime/execute.go` (đổi `claimRunning`→`admitOrClaimRunning`, thêm
`blockAdmission`, `Handle()` gọi Phase 1 trước Phase 2, `ExecuteNodeHandler` thêm 2 dependency mới),
`internal/app/adapterbuild/drift.go` (mới, `VerifyNoDrift` — export `HashExecutableFile` từ
`commands.go`), `internal/app/agentregistry/registry.go` (thêm `Empty()` convenience constructor),
`internal/app/ports/fake/agent.go` (mới, fake `AgentExecutor`), `internal/app/runtime/admission_test.go`
+ `admission_internal_test.go` (mới, 11 test), `internal/app/adapterbuild/drift_test.go` (mới, 5 test).
9 file test cũ (`execute_test.go`, `cancel_run_test.go`, `completion_test.go`, `fork_test.go`,
`join_test.go`, `scope_expansion_test.go`, `finalize_retry_test.go`, `execute_contextsnapshot_test.go`,
`internal/integration/runtimeengine_test.go`) chỉ đổi call site `NewExecuteNodeHandler` (thêm 2 tham số
mới, default an toàn `fake.IsolationEnforcementChecker{}`/`agentregistry.Empty()`) — KHÔNG đổi ý nghĩa
test nào; toàn bộ pass lại ngay vì fixture dùng chung của package này vốn đã set
`IsolationTier: ENFORCED_ISOLATED` + `GrantedCapabilities: [INTEGRATION_MULTI_REPOSITORY_WRITE]` +
không có `RequiredCapabilities`/`AdapterBuildID` nào — cả 4 check đều tự thoả mãn "tình cờ" mà không cần
sửa fixture.

**Verify:**
```
go build ./...                                          # sạch
go vet ./...                                             # sạch
go test ./internal/app/runtime/... -count=1              # PASS (toàn bộ, kể cả 11 test admission mới)
go test ./internal/app/adapterbuild/... -count=1          # PASS (kể cả 5 test VerifyNoDrift mới)
go test ./internal/integration/... -count=1               # PASS, TestRuntimeEngineGate 5.7s (không
                                                           #   livelock, sau khi fix bug #2)
go test ./internal/integration/... ./internal/app/runtime/... -count=3   # ổn định, không flake
go test -count=1 ./...                                    # toàn bộ ~70 package PASS (2 lần liên tiếp)
go run ./cmd/docs-coverage-check                          # debt = 0
```

**Việc còn lại:** commit, push, mở PR, chờ CI 6/6, merge. Task kế tiếp theo dependency graph: V5-08A
(AgentEvent contract, checkpoint batching, diff capture) — phụ thuộc V5-08. **Correction so với dòng ngay
trên (viết trước khi hỏi user):** V5-08A KHÔNG phải nơi xây NodeExecutor→AgentExecutor bridge — xem mục
V5-08A bên dưới, user đã tách rõ: V5-08A chỉ xây AgentEventSink, bridge thật là V5-08B.

## V5-08A — AgentEvent contract, checkpoint batching và diff capture (branch
`feat/v5-08a-agent-event-contract`, xây sau khi merge V5-08 tại `42b462f`)

**Nghiên cứu trước khi code** (Explore agent, nhiều điểm khác giả định ban đầu):
- `internal/app/eventschema` (registry V1-07A) tồn tại nhưng **CHƯA enforce bất cứ event type nào trong
  production** — không riêng AgentEvent: `EnforcingEventsRepository` là decorator rời, chưa từng được
  wire vào bất kỳ `Tx.Events()` thật nào (`runtime.RegisterEventSchemas` cũng chỉ được gọi từ test, tự
  ghi rõ "no composition root exists yet"). Bar "mọi event emit đều qua registry" của V5-08A là bar MỚI
  cho toàn bộ runtime engine, không phải nợ riêng của AgentEvent.
- Bảng `agent_events` tồn tại từ migration V0 spike nhưng **CHƯA CÓ MỘT DÒNG GO CODE NÀO** chạm vào nó —
  không port, không sqlite repository, không gì cả. V5-08A là người viết Go code đầu tiên cho bảng này.
  V4-01 tự ghi rõ trong design doc: "Production AgentEvent contract thuộc V5-08A" — không phải phát hiện
  mới, đúng kế hoạch từ đầu.
- Ngược lại, `checkpoints`/`checkpoint_store.go` đã có implementation ĐẦY ĐỦ, đúng (insert-once, idempotent
  nếu trùng nội dung, `ErrImmutableVersionConflict` nếu trùng key khác nội dung) — KHÔNG cần sửa, chỉ cần
  gọi. Cả 2 adapter thật (claude.go/codex.go) đã emit `AgentEventCheckpointProposed` kèm `Session` từ
  V5-06/07 nhưng chưa ai tiêu thụ event này để tạo Checkpoint thật.
- `internal/app/scopeguard.ValidateDiffs` + `internal/adapters/gitworktree` (diff/CaptureRevision thật) đã
  tồn tại từ trước nhưng chỉ được wire vào đường finalize CŨ (`internal/app/worker.Finalizer`, dùng bởi
  spike acceptance), KHÔNG phải đường V4/V5 thật (`FinalizeExecutionAttempt`/`NodeExecutionResult` không
  có field diff nào cả). Ghép 2 phần cho AGENT node thật là việc mới của V5-08A.
- **Xác nhận trực tiếp từ code** (không chỉ suy luận): `execute.go` HIỆN ĐÃ gọi `h.executor.Execute()` rồi
  đưa thẳng kết quả vào `FinalizeExecutionAttempt` NGAY TRONG `Handle()` — cái thiếu không phải là "gọi
  executor ở đâu" (chỗ đó có sẵn từ V4-05) mà là: `h.executor` hôm nay vẫn là fake `ports.NodeExecutor`
  của V4-05, chưa có implementation thật nào bọc `ports.AgentExecutor.Start/Resume` + sink thật để thay
  vào vị trí đó.
- HE-01-M03 ("assumption surface"/WAITING-NEEDS_INFO) — nguồn thứ 3 của task — **không có bất kỳ
  scaffolding nào trong code**: không `Assumption` type, không `NEEDS_INFO` constant ở đâu cả (chỉ xuất
  hiện trong văn bản harness-engineering). Khác hẳn `RequiredCapabilities` ở V5-08 (tưởng chưa pin nhưng
  hoá ra đã pin gián tiếp) — đây là khoảng trống thật, không phải ngộ nhận.

**2 câu hỏi hỏi user trước khi code, cả 2 đều có correction/xác nhận rõ so với đề xuất ban đầu:**
1. **Ranh giới V5-08A dừng ở đâu** (câu hỏi trọng tâm nhất): tôi đưa ra recommendation "chỉ xây
   AgentEventSink" kèm bằng chứng cụ thể từ `execute.go`/spec text. User chọn đúng recommendation nhưng bổ
   sung chi tiết quan trọng: V5-08A sở hữu normalize+validate qua registry, ordering/dedup/giới hạn
   payload, redaction trước persist, batch checkpoint + checkpoint sequence bất biến, diff capture theo
   EffectiveScope — **KHÔNG wire bridge thật vào `ExecuteNodeHandler`**. Lý do user nêu rõ: nếu thay
   executor thật vào bây giờ, kết quả thật sẽ đi qua `FinalizeExecutionAttempt` khi các fence về diff/
   evidence/generation/lease CHƯA được V5-08B xây — vi phạm "worker chỉ propose outcome", có thể biến exit
   code 0 thành success quá sớm. **Correction về test:** được phép gọi thẳng `Start`/`Resume` để kiểm tra
   adapter–sink contract trong test, nhưng KHÔNG được dùng `Resume` để mô phỏng recovery của platform —
   assertion "Resume call count == 0" của V5-08B (replacement Attempt luôn `Start` từ snapshot) phải giữ
   nguyên, không bị task này làm mờ ranh giới.
2. **HE-01-M03 có phải build item không:** tôi đề xuất "chỉ là rationale", user đồng ý nhưng yêu cầu RÕ
   RÀNG bằng văn bản rằng HE-01-M03 **CHƯA được implement, không được đánh dấu là đã đáp ứng** (không
   được âm thầm coi như "đã xong" chỉ vì registry+redaction đã có). Liệt kê rõ những gì KHÔNG được làm
   trong V5-08A: không thêm `Assumption` vào `AgentDiagnostic`, không suy luận assumption từ text model,
   không tự chuyển NodeRun sang WAITING, không tạo `NEEDS_INFO` tạm ngoài state-reason matrix. User tự
   phác thảo scope tối thiểu cho một task RIÊNG trong tương lai (chưa đặt tên/số trong roadmap chính thức):
   canonical Assumption/Question type, registry event riêng (không nhét raw metadata), redaction+persist,
   materiality rule, transition hợp lệ sang `NodeRun.WAITING`, typed reason `NEEDS_INFO`, command/API giải
   quyết + tiếp tục bằng Attempt mới, test chứng minh provider text không tự route state. **Đã flag qua
   `spawn_task` để không bị quên** (không tự ý sửa design doc thêm task mới ngoài phạm vi được giao).

**File thay đổi chính:**
- `internal/app/ports/agentevent.go` (mới): `AgentEventRecord`, `AgentEventsRepository` (Tx accessor mới,
  theo đúng pattern "populated now" của Artifacts/Messages/ContextSnapshots).
- `internal/app/ports/unitofwork.go`: thêm `Tx.AgentEvents()`.
- `internal/adapters/sqlite/agent_events.go` (mới): `agentEventsRepository.AppendBatch`/`ListByAttempt`,
  bound 256 KiB/event (giống `domain_events`), lỗi duplicate/UNIQUE là lỗi thật (không tự dò-rồi-so-sánh
  như `checkpoint_store.go` — lý do: duplicate ở tầng này là bug thật, không phải race hợp lệ giữa nhiều
  caller độc lập như checkpoint).
- `internal/adapters/sqlite/unitofwork.go`, `internal/app/ports/fake/agentevent.go` (mới),
  `internal/app/ports/fake/unitofwork.go`: wire accessor cho cả sqlite thật lẫn fake.
- `internal/app/agentevents/schema.go` (mới): `Payload` shape, `RegisterEventSchemas` (đăng ký 10
  `AgentEventKind` vào `eventschema.Registry` ở schema version 1) — package doc comment ghi rõ ranh giới
  scope (không bridge, không assumption surface) để không ai đọc nhầm sau này.
- `internal/app/agentevents/sink.go` (mới, ~230 dòng): `Sink` — `Accept`/`Flush` implement
  `ports.AgentEventSink`; ordering/dedup fail-closed trong bộ nhớ (Sink chỉ sống đúng 1 lần Start/Resume
  cho 1 AttemptID, không có kịch bản resume-cross-instance hợp lệ nên KHÔNG cần idempotent-no-op như
  checkpoint); redact qua JSON round-trip trước khi `redact.Matcher.Value` chạy (tránh bug thật tự phát
  hiện: `Value()` phản chiếu (reflect) struct sẽ xoá sạch `time.Time` vì field nội bộ không exported —
  round-trip qua JSON trước khiến nó chỉ thấy string/map/slice, không bao giờ dính struct riêng); checkpoint
  + diff capture chạy khi gặp `AgentEventCheckpointProposed`, diff luôn tính từ baseline lúc Sink khởi tạo
  (không phải từ checkpoint trước) đúng tinh thần ADR-005 "Alpha luôn start fresh".
- `internal/app/agentevents/sink_test.go` (mới, fake-backed, 10 test): ordering/duplicate/oversized/
  registry-gating/batching-bound/redaction/checkpoint, tất cả chạy dưới 1 giây.
- `internal/app/agentevents/sink_sqlite_test.go` (mới, sqlite thật + gitworktree thật, 4 test): checkpoint
  restart (round-trip `LoadLatestCheckpoint`), diff vượt scope (git repo thật, ghi file thật ngoài scope,
  assert `scopeguard.ErrScopeViolation` + KHÔNG có checkpoint nào được lưu + 2 event vẫn được flush đúng
  thứ tự trước khi lỗi), secret fixture search bằng 0 (scan toàn bộ `payload_json` đã persist), và một
  chuỗi event thực tế mô phỏng đúng thứ tự emit của claude.go end-to-end.
- `internal/adapters/sqlite/agent_events_test.go` (mới, 3 test): AppendBatch order/oversized/duplicate ở
  tầng repository thật, độc lập với Sink.

**1 bug thật tự phát hiện khi viết test (không phải khi chạy full suite lần này — full suite pass sạch
ngay từ đầu, không có livelock kiểu V5-04/V5-08's Bug #2):**
- `redact.Matcher` chỉ match CHÍNH XÁC (exact-match, tự ghi rõ trong doc comment, không bao giờ substring/
  regex) — fixture test ban đầu nhúng secret vào giữa chuỗi lớn hơn (`"token=" + secret`) nên KHÔNG bị
  redact, làm `TestSink_Accept_RedactsKnownSecretBeforePersist` fail thật. Không phải bug ở `Sink`/
  `redact.Matcher` — là bug ở chính fixture của tôi, sai với hợp đồng đã biết của `Matcher` (đúng cái
  `internal/app/message`'s `TestAppendMessage_MatcherNeverScansSubstringWithinFreeText` đã cảnh báo sẵn).
  Fix: fixture value phải LÀ chính secret, không phải chuỗi chứa secret.

**Verify:**
```
go build ./...                                            # sạch
go vet ./...                                              # sạch
go run ./cmd/docs-coverage-check                          # debt = 0
go test ./internal/archtest/...                            # PASS (domain/app boundary vẫn giữ)
go test ./internal/app/agentevents/... -v -count=1         # PASS, 14/14 test (10 fake + 4 sqlite/git thật)
go test ./internal/adapters/sqlite/... -run TestAgentEventsRepository -v -count=1   # PASS, 3/3
go test -count=1 ./...                                     # PASS toàn bộ ~70 package (không livelock,
                                                            #   internal/integration 12.7s bình thường)
```
`go test -race` không chạy được trên máy Windows local (`-race requires cgo`, không có mingw) — như mọi
lần trước, để CI's "Linux race and stability" job xác nhận race detector.

**Việc còn lại:** commit, push, mở PR, chờ CI 6/6, merge. Task kế tiếp: V5-08B (Fenced finalize cho AGENT
node — phụ thuộc V5-08A, sẽ là nơi xây NodeExecutor→AgentExecutor bridge thật + fencing mới cho diff
scope/evidence/generation trong `FinalizeExecutionAttempt`). Assumption-surface/NEEDS_INFO feature (nguồn
HE-01-M03) vẫn là nghĩa vụ CHƯA hoàn thành, đã flag task riêng qua `spawn_task`, chưa có số trong roadmap.

## V5-08B0 — Canonical AgentExecutionRequest assembly (branch
`feat/v5-08b0-canonical-agent-execution-request` off `master` tại `7b821fa`)

**Trạng thái:** code DONE, verify local PASS (build/vet/docs-coverage-check/full suite sạch, hai lần liên
tiếp cho `internal/app/runtime`+`internal/integration`), chuẩn bị mở PR. Task này KHÔNG nằm trong roadmap
gốc — user tự tách nó ra làm prerequisite riêng cho V5-08B sau khi review trực tiếp go-core-spec/ADR (xem
`docs/design/07-v5-execution-evidence.md`'s own V5-08B0 entry, thêm cùng đợt trên nhánh
`feat/v5-08b-fenced-finalize-agent`). Scope, 4 quyết định gốc (request assembly/evidence fencing/final
diff/provider loss) và lý do bác bỏ đề xuất A/B/C cũ đã ghi đầy đủ ở mục "Quyết định sau review source of
truth — 2026-09-08" bên trong section V5-08B (nhánh khác) — không lặp lại ở đây, chỉ ghi phần thực thi
thật của riêng V5-08B0.

**Nghiên cứu trước khi code** (đọc trực tiếp source thật, không qua Explore agent packaged report — tự
đọc từng file): phát hiện quan trọng nhất là **"Context route" (ADR-012) đã có sẵn cơ chế thật, chưa từng
được dùng**: `agentprofile.AgentProfileDocument.ContextPolicyRef` (V2-07, doc comment tự trích go-core-
spec §14) pin đúng một PolicyVersion; `policy.PolicyDocument{Category: CONTEXT, Context: *ContextRules}`
(`internal/domain/policy/policy.go`) đã có sẵn `Selector/Order/Budget/ResourceRefs` đúng y hệt câu chữ
ADR-012 "pin selector/order/budget và resource identities" — nhưng KHÔNG AI TỪNG ĐỌC field này
(`schedule.go` chưa từng resolve `ContextPolicyRef`, mọi PolicyRefs generic resolve theo Category chỉ xử
lý ATTEMPT/PERMISSION). Đây chính là mảnh ghép còn thiếu giữa `contextassembler.Resolve` (V5-03, thuật
toán thuần, chưa có caller thật) và snapshot's `ResourceRefs` (V5-04, hardcode nil). Phát hiện khác:
`ports.AgentExecutionRequest` ĐÃ TỒN TẠI (không phải type mới) nhưng shape hoàn toàn khác go-core-spec
§14 — và field `ContextSnapshotID domainruntime.ContextSnapshotID` của nó KHÔNG PHẢI bug: nó đúng cho hệ
thống checkpoint/recovery cũ (`internal/app/worker`, spike acceptance, `checkpoint_store.go` — tất cả
đang dùng field này thật), chỉ là SAI type cho pipeline V5-04 mới — sửa nhầm field này sẽ vỡ toàn bộ
recovery path cũ. Phát hiện thứ ba: KHÔNG có bất kỳ liên kết WorkItem→Component→EngineeringPack nào tồn
tại (`work.WorkItem` không có `ComponentID`) — xác nhận candidate gathering KHÔNG thể đi qua đường
Component pack assignment (V2-era), phải đi qua đường AgentProfile.ContextPolicyRef ở trên.

**Quyết định thiết kế tự đưa ra (không nằm trong "Quyết định sau review source of truth", cần user review
riêng vì đây là chi tiết THỰC THI, không phải quyết định phạm vi):**
1. **Đặt package thực thi trong chính `internal/app/runtime`** (file mới `assemble_execution_request.go`),
   KHÔNG tạo top-level package mới (`internal/app/agentrequest` như tôi từng phác thảo lúc plan) — lý do:
   assembler cần tái dùng `resolvedExecutionProfileView` (unexported), `loadResourceCandidate`/
   `decodeCompiledSkill`/`decodeCompiledLayer` (mới viết cho phần gather ở schedule.go), và các lỗi
   `ErrContextSnapshotUnverified`/`ErrNodeRunMismatch` đã có sẵn — tách package riêng sẽ phải export lại
   toàn bộ hoặc trùng lặp code. Đã sửa doc comment cũ trong `ports/agent.go` (viết trước khi quyết định
   này chốt, nhắc nhầm `internal/app/agentrequest`) cho khớp.
2. **`contextsnapshot.ResourceRef` thêm `OwnerVersionID` với tag `json:",omitempty"`** — không phải chỉ
   thêm field trơn: nếu thiếu `omitempty`, MỌI snapshot cũ (2-field shape) sẽ FAIL tamper-check
   (`ErrImmutableVersionConflict`) ngay lần load đầu tiên sau khi field mới tồn tại, vì
   `computeManifestHash` marshal lại JSON và JSON của field mới (dù rỗng) sẽ khác JSON gốc đã hash lúc
   ghi. Test `TestNewSnapshot_ManifestHash_BackwardCompatibleWithoutOwnerVersionID` tự dựng lại chính xác
   JSON kiểu cũ bằng tay và chứng minh hash trùng khớp — không chỉ tin tưởng suy luận.
3. **`ports.AgentExecutionRequest`/`AgentWorkspaceMount` chỉ ĐƯỢC THÊM field, không sửa/xoá field cũ nào**
   (kể cả field thoạt nhìn "thừa" như `Prompt`/`Model`/`Sandbox`) — "tối thiểu" trong go-core-spec §14 là
   một SÀN, không phải trần; xác nhận bằng `rg` toàn repo trước khi sửa: `ContextSnapshotID` được dùng bởi
   ~15 call site thật (worker/recovery.go, checkpoint_store*, spike acceptance, claude.go/codex.go) — sửa
   type của nó sẽ vỡ compile toàn bộ các nơi đó.
4. **AdapterBuild bắt buộc TẠI THỜI ĐIỂM ASSEMBLY, dù V5-08's own admission vẫn coi `AdapterBuild == nil`
   là "legitimate deferred Alpha state"** — đây là điểm khác biệt CÓ CHỦ ĐÍCH giữa hai lớp: admission
   (V5-08) chỉ kiểm "nếu CÓ pin thì không được drift", còn assembly (V5-08B0) phải sinh ra đúng field
   `AdapterBuildVersion` mà go-core-spec §14 bắt buộc — một request không có build không bao giờ hợp lệ,
   bất kể admission có cho attempt chạy hay không. Test
   `TestAssembleAgentExecutionRequest_NoAdapterBuildPinned_FailsClosed` cố tình dùng đúng path KHÔNG pin
   build (admission vẫn cho RUNNING) để chứng minh assembly vẫn tự fail riêng, không dựa vào admission đã
   chặn hộ. **Không sửa `admission.go`'s own driftSatisfied=true-khi-nil logic** — đó là quyết định của
   V5-08 (đã ship, có lý do riêng: "nothing to verify"), sửa nó là việc của một task khác nếu user muốn,
   không phải hệ quả tất yếu của V5-08B0.
5. **Token/byte budget unit conflation**: `policy.ContextBudget.MaxTokens` (đặt tên "tokens") được đọc
   thẳng làm `contextassembler.Budget.MaxBytes` (byte thật) — không có tokenizer nào tồn tại trong repo
   (đã tự xác nhận qua `contextassembler`'s own doc comment), và V5-03 cố tình chọn byte-based để tránh
   nhầm lẫn đúng vấn đề này. Ghi rõ trong code comment đây là "known, narrow unit conflation," không âm
   thầm giả vờ đã có tokenizer.
6. **"Task contract" trong InstructionArtifact = `WorkItem.Title/Behavior/AcceptanceCriteria/
   VerificationSpec`** (V3-03's own WorkItem contract fields) — suy luận riêng, không có câu chữ nào
   trong 5 dòng quyết định gốc nói rõ "task contract" là gì; chọn 4 field này vì đó chính xác là "cái
   WorkItem hứa" theo doc comment của chính `work.go`.
7. **WorkspaceMount.Handle/WorkingDirectory để trống** (chỉ điền RepositoryID/Access/VCSObjectID/
   WorkspaceGeneration) — tái dùng NGUYÊN VĂN lý do `admission.go`'s `buildExecutionEnvelope` đã dùng:
   resolve `ports.WorkspaceHandle` thật là việc của V5-08B's own execution bridge, không phải assembly.
8. **Resolve-conflict lúc scheduling (contextassembler trả `HardConstraintConflictError`/
   `RequiredContextExceedsBudgetError`) chỉ propagate như lỗi Go thường** (job kỹ thuật retry theo cơ chế
   DurableJob sẵn có), KHÔNG tạo BLOCKED/TerminationReason mới — vì lỗi này xảy ra TRƯỚC khi Attempt tồn
   tại (PENDING→QUEUED transaction chưa insert Attempt nào), nên không có state machine ExecutionAttempt
   nào để chuyển; đúng tinh thần "chỉ sinh reason mới qua ADR", không tự bịa. Đóng gap tường minh cũ mà
   V5-03's checklist để lại ("chưa gán TerminationReason tạm cho context conflict").

**Fixture regression tự phát hiện khi chạy full suite** (không phải bug logic, nhưng chạm ~50 test có
sẵn): `validAgentProfileDocument()` (schedule_test.go, dùng bởi ~17 call site qua `publishAgentProfileVersion`)
đã hardcode `ContextPolicyRef: {VersionID: "context-policy-v1"}` từ trước — vô hại vì trước giờ chưa ai
đọc field này. Ngay khi `gatherContextResourceRefs` bắt đầu resolve nó thật, toàn bộ ~50 test dùng chung
fixture này fail với "definition version not found". Sửa: `publishAgentProfileVersion` tự động publish
kèm một CONTEXT policy version rỗng (`Selector: ["*"]`, `ResourceRefs: nil`) khớp đúng `doc.ContextPolicyRef`
nếu field đó khác rỗng — giữ hành vi quan sát được (ResourceRefs rỗng) y hệt trước khi field này được tiêu
thụ, không đổi ý nghĩa test nào. Tách riêng `publishAgentProfileVersionOnly` (không tự auto-publish) cho
3 test mới cần tự kiểm soát nội dung CONTEXT policy thật (`schedule_contextresourcerefs_test.go`,
`assemble_execution_request_test.go`).

**File thay đổi:**
- `internal/domain/contextsnapshot/contextsnapshot.go` (sửa: `ResourceRef` +`OwnerVersionID`),
  `contextsnapshot_test.go` (+1 test backward-compat hash)
- `internal/app/ports/agent.go` (sửa: `AgentWorkspaceMount` +revision/generation, `AgentExecutionRequest`
  +9 field mới theo go-core-spec §14, `ContextSnapshotPin` type mới — field cũ giữ nguyên)
- `internal/app/runtime/schedule.go` (sửa: `resolveExecutionProfile` trả thêm `contextPolicyRef`;
  `gatherContextResourceRefs`/`loadResourceCandidate`/`decodeCompiledSkill`/`decodeCompiledLayer`/
  `skillProvenance`/`layerProvenance` mới; thay `ResourceRefs: nil` bằng gather thật)
- `internal/app/runtime/assemble_execution_request.go` (mới — `AssembleAgentExecutionRequest`, ~230 dòng)
- `internal/app/runtime/schedule_test.go` (sửa: tách `publishAgentProfileVersionOnly`, auto-publish
  CONTEXT policy trong `publishAgentProfileVersion`)
- `internal/app/runtime/schedule_contextresourcerefs_test.go` (mới, 3 test: gather thật/loại theo
  Selector/tamper fail-closed)
- `internal/app/runtime/assemble_execution_request_test.go` (mới, 3 test: end-to-end thật với sqlite fake
  UnitOfWork + filesystem ArtifactStore thật + AdapterBuild thật/mismatched IDs/thiếu AdapterBuild)
- `docs/design/07-v5-execution-evidence.md`, `baocaov5checklist.md` (đã commit ở nhánh
  `feat/v5-08b-fenced-finalize-agent` từ trước; entry này bổ sung riêng cho nhánh V5-08B0)

**Verify:**
```
go build ./...                                            # sạch
go vet ./...                                               # sạch
go run ./cmd/docs-coverage-check                           # debt = 0
gofmt -l <8 file .go đổi/mới>                              # rỗng
go test ./internal/domain/contextsnapshot/... -v -count=1  # PASS, kể cả test backward-compat mới
go test ./internal/app/runtime/... -v -count=1             # PASS toàn bộ (kể cả 6 test mới: 3 context-
                                                            #   route + 3 assemble-request)
go test ./internal/app/runtime/... ./internal/integration/... -count=2   # ổn định, không flake
go test -count=1 ./...                                     # PASS toàn bộ ~70 package
```
`-race` không chạy được local (không có cgo/mingw trên máy này) — như mọi lần trước, để CI's "Linux race
and stability" job xác nhận.

**Việc còn lại:** commit, push, mở PR, chờ CI 6/6, merge. Sau khi merge, quay lại nhánh
`feat/v5-08b-fenced-finalize-agent` (rebase lên master mới có V5-08B0), code V5-08B thật theo đúng scope
đã chốt ở "Quyết định sau review source of truth — 2026-09-08": nối `AssembleAgentExecutionRequest` +
NodeExecutor→AgentExecutor bridge, `AttemptFinalizationEvidence` 3 pha, `ProcessSupervisor` quiescence
postcondition, provider-loss mapping table.
