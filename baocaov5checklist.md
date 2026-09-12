# V5 checklist — Execution, context, verification và evidence

> Nhật ký kiểm chứng V5 được theo dõi trong repository. Ghi narrative đầy đủ cho mỗi task: quyết định,
> lý do, câu hỏi tự phát hiện và verify output thật. Từ mục rà soát V5-09 trở đi, trạng thái luôn phân
> biệt rõ “đã có nền”, “đủ dữ kiện để triển khai” và “đã triển khai”; ba khái niệm này không thay thế nhau.

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

## V5-02 remediation — post-merge audit fixes (2026-09-09)

**Bối cảnh:** user yêu cầu đi lại từng task V5 đã merge, đọc phần "Kết luận và đánh giá sau merge" (viết
trực tiếp bởi user, audit 2026-09-08, hiện sống trên nhánh `feat/v5-08b-fenced-finalize-agent`'s own
`baocaov5checklist.md` — chưa merge vào `master` nên không xuất hiện ở phần trên của chính file này), tự
sửa; nếu block vòng tròn thì hỏi lại. V5-02's audit kết luận **"CHƯA ĐẠT đầy đủ"** với 3 finding. Task này
sửa 2/3 (rõ ràng, an toàn); finding thứ 3 (redaction substring) bị **block thật** — xem cuối mục.

**Finding 1 — Attempt linkage không cross-check WorkItem/Project:** `message_repository.go`'s cũ chỉ
chạy `SELECT 1 FROM execution_attempts WHERE id = ?` (bare existence). Sửa: JOIN
`execution_attempts→node_runs→workflow_runs` để lấy `work_item_id`/`project_id` THẬT của Attempt, so với
`req.WorkItemID`/`req.ProjectID`, trả `ports.ErrCrossWorkItemReference` (sentinel mới, `ports/message.go`
— cố tình KHÔNG dùng lại `ErrCrossProjectReference` vì mismatch có thể xảy ra ngay trong CÙNG project,
chỉ khác WorkItem) nếu lệch. Sửa cả `internal/app/ports/fake/message.go` (trace qua
`m.runtime.nodeRuns`/`m.runtime.workflowRuns`). Test mới: 2 test sqlite
(`TestMessageRepository_AppendMessage_AttemptBelongsToDifferentWorkItem_Rejected`,
`...DifferentProject_Rejected`, thêm helper `seedFixtureSecondWorkItem` vì `SeedFixtureOwners` không gọi
lại được cho project đã tồn tại) + 2 test fake (`internal/app/message/attempt_linkage_test.go`, thêm
helper `seedFakeExecutionAttempt` dựng WorkflowRun→NodeRun→ExecutionAttempt thật qua
`tx.Runtime().CreateWorkflowRun/CreateNodeRun/CreateExecutionAttempt` trực tiếp — không có shortcut như
sqlite's fixture helper, đúng ghi chú cũ đã để lại).

**Finding 2 — `PrepareAttachment` chạy trước receipt-check:** tách logic receipt-check thành
`loadOrValidateReceiptTx` (Tx-scoped, dùng chung bởi cả hai chỗ) + `loadOrValidateReceipt` (read-only
pre-check, gọi TRƯỚC `PrepareAttachment`/`Put`). Một replay hoặc conflict giờ được phát hiện trước khi
Put chạy; transaction serialized-write vẫn re-check y hệt để đóng race TOCTOU giữa pre-check và
transaction (đúng pattern hai pha admission.go đã dùng). Test mới
(`internal/app/message/receipt_precheck_test.go`): `spyArtifactStore` đếm `Put` calls, chứng minh
`PutCalls` giữ nguyên ở 1 sau một replay VÀ sau một conflict (không tăng thêm) — đúng yêu cầu "Evidence
nghiệm thu bắt buộc" của audit.

**Finding 3 — secret nhúng trong free text KHÔNG bị redact — BỊ BLOCK, cần user quyết định:**
`redact.Matcher` chỉ match CHÍNH XÁC (exact-string-equality), một quyết định đã tài liệu hoá tường minh
từ V1-09 và được `TestAppendMessage_MatcherNeverScansSubstringWithinFreeText` cố tình ghi lại làm hành vi
ĐÚNG (không phải bug). Audit 2026-09-08 giờ nói ngược lại: "known secret nhúng trong free text có thể đi
vào canonical conversation" là một khoảng hở thật theo AK-ARCH-024 ("Secret fixture bị redact khỏi
event, log tìm kiếm, conversation và retained artifact theo policy"), yêu cầu fixture `"token=<secret>"`
tìm kiếm ra 0 kết quả. Tự nghiên cứu thêm: **AK-ARCH-024 được cite bởi CẢ HAI** V1-03 (`docs/design/
03-v1-alpha-foundation.md`, redactor gốc — chính là cái đã build, exact-match) **VÀ** V8-04E "Security
aggregate gate" (`docs/design/10-v8-alpha-hardening.md`, "Hoàn thành khi: không có sink nào bỏ qua
redactor dùng chung", cùng đúng fixture "secret fixture search bằng 0"). Đây là dấu hiệu rõ AK-ARCH-024
là một invariant TỔNG HỢP được hiện thực hoá DẦN qua nhiều task/phase — V8-04E mới là "aggregate gate"
chính thức xác nhận invariant này giữ TOÀN CỤC, không phải V5-02.

Đây là **block vòng tròn thật, không tự sửa**: nếu đổi `redact.Matcher` sang substring-match để thoả audit
finding 3, sẽ:
1. Đảo ngược một quyết định đã tài liệu hoá tường minh (`TestAppendMessage_MatcherNeverScansSubstringWithinFreeText`)
   mà chính user đã duyệt qua nhiều phase trước.
2. Ảnh hưởng dây chuyền: `agentevents.Sink` (V5-08A) có audit finding **giống hệt** ("fixture secret
   đứng riêng và nhúng trong text đều có search count bằng 0") — sửa Matcher một lần sẽ tác động CẢ HAI
   task, không chỉ V5-02.
3. Có đánh đổi thật (false positive khi secret ngắn/phổ biến xuất hiện tình cờ trong văn bản hợp lệ;
   chi phí quét substring trên corpus lớn) mà không có hướng dẫn rõ ràng chọn bên nào.
4. Roadmap đã có sẵn V8-04E làm "aggregate gate" chính thức cho đúng invariant này — chưa rõ ý user là
   fix ngay ở V5-02/V5-08A hay để nguyên chờ V8-04E.

**Câu hỏi cho user (đã hỏi trong chat, chờ trả lời trước khi động vào `redact.Matcher`):** có nên đổi
`redact.Matcher` sang substring-match ngay bây giờ (ảnh hưởng cả V5-02 và V5-08A), hay giữ nguyên exact-
match và để V8-04E làm aggregate gate như roadmap đã định?

**File thay đổi:**
- `internal/app/ports/message.go` (sentinel `ErrCrossWorkItemReference` mới)
- `internal/adapters/sqlite/message_repository.go`, `message_repository_test.go` (JOIN cross-check + 2
  test + helper `seedFixtureSecondWorkItem`)
- `internal/app/ports/fake/message.go` (JOIN cross-check qua map)
- `internal/app/message/commands.go` (`loadOrValidateReceipt`/`loadOrValidateReceiptTx`)
- `internal/app/message/attempt_linkage_test.go` (mới, 2 test fake + helper `seedFakeExecutionAttempt`)
- `internal/app/message/receipt_precheck_test.go` (mới, 2 test spy Put-count)

**Verify:**
```
go build ./...                                          # sạch
go vet ./...                                            # sạch
go run ./cmd/docs-coverage-check                        # debt = 0
gofmt -l <7 file .go đổi/mới>                            # rỗng sau gofmt -w
go test ./internal/app/message/... ./internal/adapters/sqlite/... -count=3   # ổn định, không flake
go test -count=1 ./...                                  # PASS toàn bộ ~70 package
```

**Việc còn lại:** commit, push, mở PR, chờ CI 6/6, merge. Finding 3 (redact.Matcher substring) treo lại
chờ user trả lời — KHÔNG tự sửa. Task kế tiếp trong remediation pass: V5-03 (MỘT PHẦN — resolver không
có caller thật, đã ĐÓNG MỘT PHẦN bởi V5-08B0's own gatherContextResourceRefs, cần re-assess lại xem còn
thiếu gì sau khi V5-08B0 đã merge).

**Quyết định user cho Finding 3 (2026-09-09, ngay trong chat, trước khi PR #38 merge):** "Giữ nguyên, chờ
V8-04E" — KHÔNG đổi `redact.Matcher` sang substring-match bây giờ. V8-04E (roadmap V8) vẫn là nơi chính
thức xác nhận AK-ARCH-024 toàn cục ("không sink nào bỏ qua redactor dùng chung"). V5-02 và V5-08A giữ
nguyên kết luận hiện tại của chúng cho finding này — không coi đây là gap cần đóng ở V5 phase. Không sửa
code gì thêm cho finding 3.

## V5-03 remediation — post-merge audit fixes (2026-09-09)

**Bối cảnh:** tiếp tục vòng đi lại từng task V5 theo yêu cầu user. Audit V5-03 (viết 2026-09-08, sống
trên nhánh `feat/v5-08b-fenced-finalize-agent`) kết luận **"MỘT PHẦN"**: thuật toán `contextassembler.Resolve`
đạt, nhưng KHÔNG có caller production nào và kết quả không đi vào ContextSnapshot. V5-08B0 (đã merge)
ĐÃ ĐÓNG phần lớn gap này (schedule.go giờ gather candidate thật + gọi Resolve + persist ResourceRefs với
đủ OwnerVersionID) — nhưng audit's own "Kết quả cần đạt" còn một phần V5-08B0 CHƯA làm: "persist selected
resource identity đầy đủ **cùng provenance/reason** có thể audit" — V5-08B0 chỉ lưu identity
(OwnerVersionID+ResourceKey+ContentHash) vào Snapshot, KHÔNG lưu lý do chọn/loại (`SelectionReason`) hay
danh sách bị loại (`Excluded`) ở đâu cả — biến mất ngay khi `ScheduleExecutableNodeRun` return.

**Fix:** `gatherContextResourceRefs` đổi return type từ `[]contextsnapshot.ResourceRef` sang
`contextassembler.Resolution` đầy đủ (Selected + Excluded + budget spent + cost model). Caller
(`ScheduleExecutableNodeRun`) tự map `.Selected` thành ResourceRefs cho Snapshot (như cũ), CỘNG THÊM
persist TOÀN BỘ Resolution (kể cả Excluded/reason) thành một DecisionArtifact mới
`"<nodeRunId>-context-resolution-v1"` — đúng pattern `"<nodeRunId>-execution-profile-v1"`/
`"<nodeRunId>-execution-envelope-v1"` đã có sẵn trong chính file này. Chỉ persist khi
`contextPolicyRef.VersionID != ""` (có context route thật để audit) — COMMAND/MACHINE_GATE node hoặc
AGENT profile không khai ContextPolicyRef thì không có gì để ghi.

**Test coverage bổ sung** (audit's own "Evidence nghiệm thu bắt buộc" liệt kê rõ Skill/Layer/hard-conflict/
provenance-reason — V5-08B0 trước đó CHỈ test qua Skill, chưa test Layer hay hard-conflict với definition
thật):
- `TestScheduleExecutableNodeRun_GathersRealResourceRefsFromLayer` — mirror test Skill hiện có nhưng qua
  Layer (thêm helper `oneResourceLayerDocument`/`publishLayerVersion`/`layerResourceContentHash`) — trước
  giờ path `decodeCompiledLayer`/`layer.ResourceIdentities` trong schedule.go mới chỉ được BUILD, chưa
  từng chạy qua test thật nào.
- `TestScheduleExecutableNodeRun_ContextRoute_HardConstraintConflict_FailsClosed` — 2 Skill version thật,
  cùng ResourceKey khác ContentHash, cả hai HARD_CONSTRAINT, gather qua đúng đường ScheduleExecutableNodeRun
  thật (không phải unit test thuần của contextassembler.Resolve) — chứng minh scheduling fail closed thật
  khi có conflict thật, không chỉ thuật toán tự nó phát hiện được.
- `TestScheduleExecutableNodeRun_PersistsContextResolutionDecisionWithReasons` — chứng minh
  DecisionArtifact mới lưu đúng: 1 resource SELECTED (global, luôn áp dụng) + 1 resource NOT_APPLICABLE
  (Selector không khớp WorkItem) — cả hai xuất hiện đúng reason trong JSON đã persist, không chỉ trong bộ
  nhớ.

**Phạm vi KHÔNG làm thêm (để tránh lấn task khác):**
- Không test "Pack" (EngineeringPack) riêng — thiết kế thật (khám phá ở V5-08B0) đi qua
  `AgentProfileDocument.ContextPolicyRef → policy.CategoryContext.ResourceRefs` trực tiếp, KHÔNG qua
  EngineeringPack's own dependency graph (`engineeringpack.ResolvePackGraph`) — audit's gợi ý "Pack" viết
  trước khi cơ chế thật này được khám phá, không phải một đường thật cần test.
- Không thêm test riêng cho "manifest giống nhau khi đảo input order/restart" với ResourceRefs thật —
  `contextsnapshot`'s own order-sensitive hash test (V5-04) và tamper-detection test (sqlite layer) đã
  cover nguyên lý này ở tầng snapshot; không lặp lại with real Skill/Layer content vì không có gì khác về
  nguyên lý.
- Không thêm test "reserved budget được giữ" riêng với gathering thật — `contextassembler`'s own unit
  test (V5-03 gốc) đã cover budget reservation logic; real-gathering test ở đây chỉ cần chứng minh
  candidate/payload thật đi vào đúng, không cần lặp lại budget-reservation math.

**File thay đổi:**
- `internal/app/runtime/schedule.go` (sửa: `gatherContextResourceRefs` trả `contextassembler.Resolution`
  thay vì `[]contextsnapshot.ResourceRef`; `recordContextResolutionDecision` mới)
- `internal/app/runtime/schedule_contextresourcerefs_test.go` (thêm 3 test + helper Layer)

**Verify:**
```
go build ./...                                          # sạch
go vet ./...                                            # sạch
go run ./cmd/docs-coverage-check                        # debt = 0
gofmt -l <2 file .go đổi>                                # rỗng sau gofmt -w
go test ./internal/app/runtime/... -count=2              # ổn định, không flake
go test -count=1 ./...                                  # PASS toàn bộ ~70 package
```

**Việc còn lại:** commit, push, mở PR, chờ CI 6/6, merge. Task kế tiếp trong remediation pass: V5-04
(CHƯA ĐẠT — ResourceRefs=nil hardcode đã ĐÓNG bởi V5-08B0, nhưng audit còn nêu vấn đề khác: attempt bị
mắc kẹt RUNNING mãi mãi khi verifyContextSnapshot fail sau CAS — cần re-assess xem còn gì phải sửa).

## V5-04 remediation — post-merge audit fixes (2026-09-09)

**Bối cảnh:** tiếp tục vòng đi lại từng task V5. Audit V5-04 kết luận **"CHƯA ĐẠT acceptance đầy đủ"**
với finding nghiêm trọng nhất trong toàn bộ audit: `verifyContextSnapshot` (execute.go, dispatch-time
precondition) chỉ kiểm binding/RevisionSet, KHÔNG BAO GIỜ dereference MessageRefs/ResourceRefs; và khi
verify fail, code CŨ chỉ `return err`, để Attempt mắc kẹt `RUNNING` VĨNH VIỄN — một livelock thật (không
có đường nào trong toàn bộ codebase từng re-drive một Attempt RUNNING mà job của nó cứ fail đúng
precondition này ở mọi lần redelivery).

**Fix 1 — đóng livelock:** `Handle()`'s own verify-failure branch đổi từ `return err` (để RUNNING mãi)
sang gọi `FinalizeExecutionAttempt` với `NextState: FAILED, TerminationReason: EXECUTION_FAILED,
FailureCode: errorcode.CodeExecutionFailed` — ĐÚNG (state, reason) pair mà nhánh "bare executor error"
ngay bên dưới ĐàN dùng, không cần TerminationReason mới (không đụng ma trận state–reason đã khoá của
ADR-020). `Handle()` trả `finalizeErr` (nil khi finalize thành công) thay vì `err` gốc — đúng pattern MỌI
nhánh finalize khác trong function này đã dùng (job coi là "đã xong việc" khi Attempt đã terminal, không
retry job vô ích).

**Fix 2 — verifyContextSnapshot dereference thật:** thêm 2 vòng lặp mới sau check RevisionSet — mỗi
`MessageRef` phải resolve về `Message` thật CÙNG WorkItem/Project, có `ContentArtifactID` trỏ tới Artifact
`ATTACHED`; mỗi `ResourceRef` re-verify qua `loadResourceCandidate` (schedule.go, V5-08B0 — TÁI DÙNG
nguyên hàm, không viết logic thứ hai) — cùng một đường verify `AssembleAgentExecutionRequest` (V5-08B0)
đã dùng, tránh hai nơi kiểm tra khác nhau có thể lệch nhau theo thời gian.

**2 test cũ SAI kỳ vọng, đã sửa:** cả 2 test hiện có (`TestExecuteNodeHandler_MissingContextSnapshot_...`,
`TestExecuteNodeHandler_ContextSnapshotBoundToDifferentAttempt_...`) TỪNG assert `attempt.State ==
RUNNING` như là hành vi ĐÚNG (đúng y hệt bug vừa sửa) — đổi assertion sang `Handle()` trả `nil` +
`attempt.State == FAILED` + `TerminationReason == EXECUTION_FAILED`.

**Test mới** (đúng gap audit chỉ ra: dereferencing trước đây KHÔNG BAO GIỜ được test):
- `TestExecuteNodeHandler_ContextSnapshot_MessageRefInvalid_FinalizesFailed` — snapshot bị overwrite với
  MessageRef trỏ tới Message không tồn tại.
- `TestExecuteNodeHandler_ContextSnapshot_ResourceRefInvalid_FinalizesFailed` — tương tự với ResourceRef
  trỏ tới OwnerVersionID không tồn tại.

**Phạm vi KHÔNG làm thêm (ghi rõ, không lặng lẽ bỏ qua):** audit's "Evidence nghiệm thu bắt buộc" còn liệt
kê thêm cross-project Message ref, artifact tamper (AttachState≠ATTACHED), và RevisionSet mismatch — CHƯA
viết test riêng cho 3 case này ở tầng dispatch-precondition (dù logic verify MỚI đã bao phủ đủ cả 3 —
cross-project check dùng đúng `msg.WorkItemID/ProjectID` so sánh, AttachState check dùng đúng
`art.AttachState != Attached`, RevisionSet check đã có từ trước task này). Quyết định dừng ở 2 test trên vì
đã đủ chứng minh CẢ HAI vòng lặp mới (Message VÀ Resource) hoạt động và livelock đã đóng — 3 case còn lại
là biến thể của CÙNG logic, không phải đường code mới. Nếu user muốn coverage đầy đủ hơn, có thể mở task
riêng.

**File thay đổi:**
- `internal/app/runtime/execute.go` (sửa: `verifyContextSnapshot` thêm dereference MessageRefs/
  ResourceRefs; `Handle()`'s verify-failure branch finalize FAILED thay vì để RUNNING)
- `internal/app/runtime/execute_contextsnapshot_test.go` (sửa 2 test cũ + thêm 2 test mới)

**Verify:**
```
go build ./...                                          # sạch
go vet ./...                                            # sạch
go run ./cmd/docs-coverage-check                        # debt = 0
gofmt -l <2 file .go đổi>                                # rỗng sau gofmt -w
go test ./internal/app/runtime/... ./internal/integration/... -count=2   # ổn định, không flake
go test -count=1 ./...                                  # PASS toàn bộ ~70 package
```

**Việc còn lại:** commit, push, mở PR, chờ CI 6/6, merge. Task kế tiếp trong remediation pass: V5-05
(MỘT PHẦN — process-tree quiescence trên normal-exit path — đã được user chốt là scope của V5-08B chính
thức, KHÔNG phải remediation riêng ở đây; re-assess xem còn phần nào của V5-05's own audit KHÔNG thuộc
V5-08B cần sửa ngay).

## V5-05 remediation — re-assessed, no separate action (2026-09-09)

Audit V5-05 kết luận "MỘT PHẦN": process-tree/quiescence guarantee (đặc biệt nhánh normal-exit) chưa đủ
để nghiệm thu cho mutating execution. Re-đọc kỹ: finding này TRÙNG KHỚP HOÀN TOÀN với "Quyết định sau
review source of truth — 2026-09-08" mục 3 (đã chốt trên nhánh `feat/v5-08b-fenced-finalize-agent`
TRƯỚC cả vòng remediation này) — user đã tự quyết định `ProcessSupervisor.Run` cần postcondition
quiescence mới, và explicit giao việc này cho V5-08B ("normal-completion path cần guarantee này ngay
TRONG V5-08B", không phải một fix riêng). KHÔNG sửa gì thêm ở đây — chờ V5-08B tự triển khai đúng theo
quyết định đã chốt.

## V5-06 remediation — re-assessed, no separate action (2026-09-09)

Audit V5-06 kết luận "ĐẠT CÓ ĐIỀU KIỆN ở adapter boundary" — toàn bộ "Kết quả cần đạt"/"Evidence nghiệm
thu bắt buộc" đòi hỏi một AGENT Attempt THẬT chạy qua request assembly → Claude adapter → durable
canonical events/checkpoint — tức là cần NodeExecutor→AgentExecutor bridge thật, đúng scope V5-08B
(chưa build). Không có phần nào của audit V5-06 tách rời được khỏi việc chờ bridge đó. KHÔNG sửa gì thêm
ở đây.

## V5-07 remediation — post-merge audit fix (2026-09-09)

**Bối cảnh:** audit V5-07 có HAI phần: (1) adapter-boundary conditional — giống hệt V5-06, chờ V5-08B,
không sửa riêng; (2) **semantic-diff verification CHƯA ĐẠT** — SPK-11's own `missingEventKinds` chỉ kiểm
"7 kind bắt buộc có mặt", không so ordering/payload, không có allow-list tường minh cho khác biệt giữa
provider. Phần (2) độc lập với V5-08B, sửa được ngay.

**Fix (vòng 1, SAI, tự bắt và tự sửa):** thử strict total-order comparison (`equalEventKindSequences`,
sequence phải khớp tuyệt đối sau khi trừ allow-list). Chạy `go test ./internal/spikeacceptance/...`
NGAY LẬP TỨC bắt được: `TestDefaultScenariosFormAValidRegistryAndCleanRun` fail thật — Codex và Claude
CÓ THỨ TỰ KHÁC NHAU thật giữa TOOL_CALL_STARTED/FINISHED và ASSISTANT_MESSAGE. Đọc trực tiếp
`internal/adapters/providers/fixtures.go`'s `writeProviderSuccess` (JSONL thật của cả 2 fake CLI) để xác
nhận NGUYÊN NHÂN: đây KHÔNG phải bug — Codex's protocol thật báo tool call THÀNH HAI SỰ KIỆN vận hành
(`item.started`/`item.completed`) TRƯỚC một item tóm tắt riêng (`agent_message`); Claude's protocol thật
GỘP text và tool_use vào MỘT message assistant duy nhất, nên normalizer của claude.go phát ASSISTANT_MESSAGE
trước cặp TOOL_CALL_STARTED/FINISHED mà tool_result phía sau mới suy ra được. Đây là khác biệt THẬT, hợp
lệ giữa hai provider — không phải lỗi cần sửa fake CLI.

**Fix (vòng 2, đúng):** đổi sang so sánh MULTISET (cùng kind, cùng SỐ LƯỢNG mỗi kind, không đòi thứ tự
tổng thể giống hệt) + 2 invariant cấu trúc thật sự đúng cho MỌI provider: (a) EXECUTION_STARTED luôn đầu
tiên, EXECUTION_FINISHED luôn cuối cùng; (b) mỗi TOOL_CALL_STARTED luôn đứng trước chính TOOL_CALL_FINISHED
của nó. Đây là contract ĐÚNG hơn "identical total order" — bắt được thật sự mọi khác biệt audit lo ngại
(event dư ngoài allow-list, đếm sai số lượng, thiếu event) mà KHÔNG false-positive trên khác biệt hợp lệ
giữa provider.

**Test:** `spk11_scenario_test.go` (mới) — `TestNormalizeEventKinds_StripsOnlyAllowlistedProviderExtras`,
`TestEqualEventKindMultisets_DetectsRealDivergence` (5 sub-test: identical/reorder-vẫn-khớp/extra-event/
dropped-event/same-length-khác-count đều đúng kỳ vọng), `TestFirstLastEventKindIndex`.

**Phát hiện phụ, KHÔNG sửa ở đây, đã spawn_task riêng:** khi thử tái tạo CI's "spike acceptance" job cục
bộ (build 5 binary vào 1 thư mục cô lập, chạy CLI thật với đường dẫn tương đối y hệt CI) để double-check
trước khi push, SPK-03 fail với đúng lỗi class y hệt bug SPK-11/12 mà V5-05 đã tự sửa
("executable file not found" — relative spike-worker path resolve sai) — SPK-03's own scenario code
CHƯA từng nhận fix tương tự. Không thuộc phạm vi V5-07, đã spawn_task để không quên.

**File thay đổi:**
- `internal/spikeacceptance/spk11_scenario.go` (sửa: `missingEventKinds` giữ nguyên; thêm
  `spk11NormalizedDifferencesAllowlist`/`normalizeEventKinds`/`equalEventKindMultisets`/
  `firstEventKind`/`lastEventKind`/`eventKindIndex`; 3 assertion mới trong `runSPK11Scenario`)
- `internal/spikeacceptance/spk11_scenario_test.go` (mới)

**Verify:**
```
go build ./...                                          # sạch
go vet ./...                                            # sạch
go run ./cmd/docs-coverage-check                        # debt = 0
gofmt -l <2 file .go đổi/mới>                            # rỗng sau gofmt -w
go test ./internal/spikeacceptance/... -v -count=1       # PASS toàn bộ, kể cả
                                                          #   TestDefaultScenariosFormAValidRegistryAndCleanRun
                                                          #   (13 SPK Passed:true, SPK-13 PENDING_PEER_PLATFORM)
go test ./internal/spikeacceptance/... -count=2          # ổn định, không flake
go test -count=1 ./...                                  # PASS toàn bộ ~70 package
```
Không tái tạo được đầy đủ CI's "spike acceptance" job cục bộ (SPK-03's own pre-existing, unrelated bug
chặn `--full` chạy hết) — dựa vào CI job thật để xác nhận cuối cùng, đúng bài học V5-05 để lại.

**Việc còn lại:** commit, push, mở PR, chờ CI 6/6 (đặc biệt "spike acceptance" cả 2 platform — job DUY
NHẤT thật sự chạy `runSPK11Scenario`), merge. Task kế tiếp trong remediation pass: V5-08 (CHƯA ĐẠT —
AdapterBuild nil bypass driftSatisfied + envelope thiếu hầu hết field §14).

## Fix ngoài phạm vi V5 — race thật trong `internal/app/workspaceprovision` (2026-09-09)

**Bối cảnh:** trong lúc chờ CI của PR V5-08 (remediation), CI's own "Linux race and stability" job
(`go test -race -count=1 ./...`) bắt được một race THẬT, hoàn toàn không liên quan V5-08: hai test
(`TestEndToEnd_MultiRepoProvision_BothReachReadyWithBaseRevisionSet`,
`TestEndToEnd_PartialFailure_OneReadyOneFailed_SetBlockedRowsKept`) dùng chung một
`idsource.Sequential` cho `workspaceprovision.Handler` chạy dưới `provisionPoolConfig`'s own
`Concurrency: 2` — hai repository trong cùng WorkspaceSet được xử lý bởi hai goroutine THẬT đồng thời,
cả hai cùng gọi `ids.NewID()` trên MỘT instance chưa từng có khoá. `idsource.Sequential`'s own doc
comment ghi rõ: "Not safe for concurrent use — it is a single-threaded test helper, not a production
allocator." Đây là bug thật trong fixture của 2 test đó, không phải flake giả — CI's own workflow
comment cho job này cũng ghi rõ "một failure ở đây là tín hiệu thật để root-cause, không phải lý do để
rerun tới khi xanh", nên không rerun mù mà sửa gốc.

**Quyết định user (2026-09-09):** dù ngoài phạm vi V5-08/V5-08A đang làm, sửa ngay bằng một PR nhỏ tách
riêng (đúng doctrine "mỗi fix một PR"), vì bug này chặn CI của TẤT CẢ PR đang mở (race không xác định về
thời điểm, có thể tái phát ở bất kỳ PR nào chạy job "Linux race and stability" tiếp theo).

**Fix:** cả 2 test đổi ID source của riêng `handler := workspaceprovision.New(uow, ...)` (đối tượng được
gọi từ nhiều goroutine qua pool) sang `idsource.Random{}` (an toàn concurrent, đúng production allocator)
— giữ nguyên `ids` (Sequential) cho các lệnh seed đồng bộ trước khi pool chạy (`mustSeedActiveRepositorySQLite`,
`mustCreateRootWorkItemSQLite`), vì các lệnh đó không bao giờ chạy đồng thời với gì khác. Không đổi gì ở
`idsource.Sequential` package tự nó (contract "single-threaded" của nó là đúng, đã ghi rõ từ đầu) — bug
nằm ở phía gọi dùng sai, không phải ở chính type.

Đã kiểm tra `TestEndToEnd_Restart_RemainingJobCompletesAndSetReachesReady` (dùng `idsource.NewSequential`
tương tự) KHÔNG có race này: registry của nó cố tình chỉ để job ĐẦU TIÊN claim thật sự gọi
`realHandler.Handle` (dùng `ids`); job thứ hai luôn rơi vào nhánh "stuck" mô phỏng crash, không bao giờ
gọi `ids.NewID()` — nên không có 2 lời gọi đồng thời thật, không cần sửa.

**File thay đổi:**
- `internal/app/workspaceprovision/handler_sqlite_test.go` (2 test đổi ID source của handler sang
  `idsource.Random{}`)

**Verify:**
```
go build ./...                                          # sạch
go vet ./...                                            # sạch
go run ./cmd/docs-coverage-check                        # debt = 0
gofmt -l <1 file .go đổi>                                # rỗng sau gofmt -w
go test ./internal/app/workspaceprovision/... -v -count=1              # PASS 14/14
go test ./internal/app/workspaceprovision/... -run 'TestEndToEnd_MultiRepoProvision|TestEndToEnd_PartialFailure' -count=10  # ổn định
go test -count=1 ./...                                  # PASS toàn bộ ~70 package
```
`-race` không tự verify lại được cục bộ (máy Windows này không có cgo) — dựa vào CI's own race job để
xác nhận cuối cùng.

**Việc còn lại:** commit, push, mở PR, chờ CI 6/6 (đặc biệt "Linux race and stability" — chính job đã bắt
bug này), merge. Sau khi merge, quay lại PR V5-08 (đang chờ CI) — rebase nếu cần rồi tiếp tục theo đúng
thứ tự tuyến tính đã thống nhất.

## V5-08 remediation — post-merge audit fixes (2026-09-09)

**Bối cảnh:** tiếp tục vòng đi lại từng task V5. Audit V5-08 kết luận **"CHƯA ĐẠT"** với hai finding: (1)
`runAdmissionProbePhase` (admission.go) coi `profile.AdapterBuild == nil` là "không có gì để kiểm tra,
pass" VÔ ĐIỀU KIỆN — trực tiếp mâu thuẫn GC-INV-23 ("Attempt pin immutable AdapterBuildVersion; version
khác bị từ chối trước dispatch"), vốn coi việc pin build là BẮT BUỘC chứ không phải optional; (2)
`recordExecutionEnvelope` còn thiếu hầu hết field go-core-spec §14 đòi hỏi (chỉ có RepositoryID/Access,
thiếu ProviderKey/InstructionArtifact/EffectiveScope/ExecutionProfileHash/... đầy đủ). Task này sửa (1);
(2) để nguyên, xem cuối mục vì sao.

**Fix 1 — đóng AdapterBuild nil-bypass (GC-INV-23):** `runAdmissionProbePhase`'s nhánh
`profile.AdapterBuild == nil` đổi từ set `driftSatisfied = true` vô điều kiện sang kiểm tra
`profile.Executor.Kind`: COMMAND/MACHINE_GATE (không có khái niệm AdapterBuildVersion) vẫn pass-through
như cũ; AGENT node giờ nhận `driftErr` thật ("AGENT node declares no AdapterBuildID — GC-INV-23 requires
an Attempt to pin an immutable AdapterBuildVersion") với `driftSatisfied` để nguyên `false` (zero value) —
`evaluateAdmission` tự resolve thành ADAPTER_BUILD_DRIFT, đúng ma trận state-reason đã khoá của ADR-020
(QUEUED→BLOCKED).

**Ripple lớn nhất phiên này — gần như TOÀN BỘ fixture của `internal/app/runtime` chưa từng pin build
thật:** fix trên khiến MỌI test AGENT trong package (execute/finalize/fork/join/scope-expansion/cancel/
completion — ~30 test) vốn dựa vào hành vi "nil bypass = pass" cũ giờ fail ADAPTER_BUILD_DRIFT. Sửa theo
lớp, không sửa từng test riêng lẻ:
- `admission_test.go`: 7 call site cũ + test mới `TestAdmission_NoAdapterBuildPinned_BlocksBeforeSpawn`
  (executor.Calls=0 — "chặn trước dispatch" thật).
- `shared_admission_test.go` (mới): một `sync.Once`-backed singleton
  (`sharedTestAdapterBuild`/`sharedTestAgentRegistry`/`registerSharedTestAdapterBuild`) — build một
  executable tạm thật ĐÚNG MỘT LẦN cho cả package test binary (qua `os.MkdirTemp`, KHÔNG `t.TempDir()` —
  file phải sống sót qua nhiều test function), thay vì luồn thêm một return value mới qua ~30 call site
  của `scheduledExecutionFixture`/`scheduledExecutionFixtureWithAttemptPolicy` trong 8 file test.
- `execute_test.go`/`finalize_retry_test.go`: 2 fixture trung tâm tự pin + register build bên trong,
  không đổi signature của chính chúng.
- `join_test.go`: `joinPolicyDocument` thêm tham số `t *testing.T` (mint `AdapterBuildID` thật cho mỗi
  branch's own AgentNodeConfig) — 8 call site (7 trong `join_test.go`, 1 trong `join_sqlite_test.go`) cập
  nhật theo.
- `fork_test.go`: `forkExecutableDocument` tương tự, thêm tham số `t`.
- `cancel_run_test.go`/`completion_test.go`/`execute_contextsnapshot_test.go`/`scope_expansion_test.go`/
  `fork_sqlite_test.go`/`join_sqlite_test.go`: thay `agentregistry.Empty()` → `sharedTestAgentRegistry(t)`.

**Fix 2 (KHÔNG làm ở đây — không phải block vòng tròn, chỉ là ngoài phạm vi đã chốt trước):**
`recordExecutionEnvelope`'s field-completeness so với go-core-spec §14 — giống hệt lý do V5-05/V5-06 đã
ghi: `AssembleAgentExecutionRequest` (V5-08B0) đã build ĐÚNG một envelope §14 đầy đủ (ProviderKey,
InstructionArtifact, ContextSnapshot, EffectiveScope, ExecutionProfileHash, IsolationProfile,
AllowedCapabilities, ...) nhưng cố tình CHƯA nối vào `ExecuteNodeHandler.Handle` (V5-08B0's own "Phụ
thuộc" note: "deliberately deferred to V5-08B"). Hợp nhất `recordExecutionEnvelope` cũ vào envelope §14
mới đó là chính xác việc V5-08B tự làm, không phải một fix riêng ở remediation pass này — sửa 2 lần cùng
một chỗ (một lần vá field thiếu, một lần thay hẳn bằng V5-08B0) là lãng phí và có nguy cơ tạo hai đường
envelope khác nhau tạm thời.

**Circular block thật, đã hỏi user trước khi sửa — golden-trace `internal/integration`'s own
`TestRuntimeEngineGate`:** sau khi Fix 1 làm AGENT node pin build bắt buộc, `internal/integration`'s own
runtime-engine gate (V4-14, chưa từng pin build trước đây) cũng cần một AdapterBuild thật — nhưng build
đó cần một file thực thi thật trên đĩa để `HashExecutableFile`/`VerifyNoDrift` hoạt động, và
`CandidateTuple` của nó nhúng `runtime.GOOS`/`runtime.Version()` thật. Verify thực nghiệm: 2 lần
`go test` riêng biệt (cùng máy) cho ra 2 giá trị `executionProfileHash` KHÁC NHAU trong golden trace (vì
build gắn với đường dẫn file tạm ngẫu nhiên mỗi lần chạy process) — và CI's own "contract" job chạy
CHÍNH test này trên CẢ `windows-latest` LẪN `ubuntu-latest`, nên một so sánh byte-exact trên field này
KHÔNG THỂ pass trên cả hai platform cùng lúc. Đây là block vòng tròn thật (đóng gap GC-INV-23 cho gate
này phá vỡ cơ chế golden byte-exact đã được user xác nhận trước đó) — đã hỏi user (AskUserQuestion) thay
vì tự chọn.

**Quyết định user (2026-09-09):** field-aware canonicalization, KHÔNG xoá field, KHÔNG alias mọi chuỗi
`sha256:` (phạm vi rộng có thể che regression ở contentHash/revisionHash/policyHash — user tự nêu rõ,
không chọn phương án alias rộng). Cụ thể: `executionProfileHash` được alias THEO TÊN FIELD thành
`<execution-profile-hash-N>` (cùng giá trị → cùng alias, khác giá trị → khác alias — golden vẫn phát
hiện được sai lệch same/different giữa các node), validate đúng format `sha256:<64 hex>` trước khi alias
(panic nếu không đúng — fail loudly, không âm thầm bỏ qua), mọi field hash khác giữ nguyên byte-exact.
Giữ AdapterBuild THẬT trong gate (không né tránh việc pin thật). GC-INV-23 được assert TRỰC TIẾP (không
chỉ dựa vào golden) trong test mới `TestRuntimeEngineGate_AdapterBuildPinning`.

**Fix:**
- `canonicalizeRuntimeEngineTrace` (runtimeengine_test.go): thêm nhánh field-name-keyed cho
  `"executionProfileHash"` (alias qua `aliasForExecutionProfileHash`, map riêng — không dùng chung
  namespace với `aliasForGeneratedID`'s own UUID/t-N/h-N), validate qua `executionProfileHashPattern`
  (`^sha256:[0-9a-f]{64}$`), panic khi sai shape. Doc comment cập nhật giải thích rõ đây là ngoại lệ
  platform-derived thứ hai, tương tự ngoại lệ `jobId` đã có.
- `runtimeEngineDocument`/`publishREWorkflowVersion`: AGENT node ("implement", "scope_node") giờ pin
  `AdapterBuildID` thật qua singleton package-wide mới `runtimeEngineAdapterBuild`/
  `runtimeEngineAgentRegistry`/`registerRuntimeEngineAdapterBuild` (cùng pattern
  `shared_admission_test.go` đã dùng cho `internal/app/runtime`).
- `TestRuntimeEngineGate_AdapterBuildPinning` (mới): 2 phần — (1) dispatch sạch, đọc lại DecisionArtifact
  `"<nodeRunId>-execution-profile-v1"` thật, xác nhận `AdapterBuild.BuildID` đúng build đã đăng ký; (2)
  drift thật (tamper byte thực thi sau khi đăng ký) chặn Attempt MỚI trước khi executor được gọi
  (`StartedAt == nil`), đúng `TerminationReason=ADAPTER_BUILD_DRIFT` + blocker `OPEN` tương ứng — mirror
  `internal/app/runtime`'s own `TestAdmission_AdapterBuildDrift_BlocksBeforeSpawn` nhưng qua full stack
  thật (sqlite + pool + ExecuteNodeHandler thật), không phải fake.
- `TestCanonicalizeRuntimeEngineTrace_ExecutionProfileHashFieldAware`,
  `TestCanonicalizeRuntimeEngineTrace_MalformedExecutionProfileHash_PanicsFailClosed` (mới): chứng minh
  đúng thiết kế user chốt — same/different value → same/different alias, field hash KHÔNG liên quan
  (vd `contentHash`) giữ nguyên byte-exact, shape sai → panic.

**3 vòng tự bắt và tự sửa flake của chính test mới (không phải bug runtime, lỗi thiết kế fixture):**
(a) `startPool()` ban đầu tạo `idsource.NewSequential("h")` MỚI mỗi lần gọi lại — khi Part 2 khởi động
pool mới trên CÙNG store đã có data từ Part 1, id "h-1" reset lại và collide với row Part 1 đã ghi thật;
(b) dù đã sửa (a) và dừng hẳn pool Part 1 trước khi tamper file, vẫn còn ~1/25 lần fail cục bộ "execution
attempt not found" — sửa bằng cách tách Part 1/Part 2 thành hai `*sqlite.Store` HOÀN TOÀN độc lập
(`runAdapterBuildPinningScenario` factor ra) — 30 lần liên tiếp cục bộ đều xanh, PUSH LÊN CI.
(c) **CI's own `go test -race -count=1 ./...` (job "Linux race and stability") bắt được lỗi THẬT thứ ba,
sâu hơn cả (a)/(b), mà 30 lần chạy cục bộ KHÔNG `-race` không bao giờ lộ ra** (Windows dev machine không
có cgo, không chạy được `-race` cục bộ): `runAdapterBuildPinningScenario` khởi động NGUYÊN registry đầy
đủ (`registerRuntimeEngineHandlers` — gồm cả `Scheduler`'s own `AdvanceRunJobKind` handler) NGAY TRƯỚC
khi tự gọi trực tiếp `StartWorkflowRun`/`AdvanceRun` ở foreground — pool's own background Scheduler đua
tranh advance ĐÚNG NodeRun mà code foreground cũng đang advance; bên thua (đôi khi chính là lệnh gọi
foreground) nhận lại một hop rỗng (`NextNodeKey=""`) từ đường idempotent-replay. Đây CHÍNH XÁC là rủi ro
`TestRuntimeEngineGate_ConcurrentPoolsNoDuplicateExecution`'s own doc comment đã cảnh báo từ trước (dùng
provisioning-only pool cho `createREWorkItem`, hủy nó, tự lái foreground, CHỈ start full pool SAU KHI
`ScheduleExecutableNodeRun` đã tạo job EXECUTE_NODE thật) — bài test mới của tôi không theo đúng pattern
đó. Sửa: tái cấu trúc `runAdapterBuildPinningScenario` đúng y hệt pattern đã có sẵn (provisioning-only
pool trước, hủy, lái foreground trực tiếp, full pool CHỈ start sau `ScheduleExecutableNodeRun`) — loại
bỏ hẳn race bằng kiến trúc, không phải vá triệu chứng. Verify: 50 lần liên tiếp cục bộ + suite tích hợp
+ suite toàn repo đều xanh; `-race` tự nó không verify lại được cục bộ (không cgo trên máy Windows này),
dựa vào CI's own race job để xác nhận cuối cùng.

**File thay đổi:**
- `internal/app/runtime/admission.go` (Fix 1: AdapterBuild nil-bypass cho AGENT)
- `internal/app/runtime/admission_test.go` (7 call site cập nhật + test mới)
- `internal/app/runtime/shared_admission_test.go` (mới, singleton build+registry)
- `internal/app/runtime/execute_test.go`, `finalize_retry_test.go` (2 fixture trung tâm tự pin)
- `internal/app/runtime/join_test.go`, `join_sqlite_test.go`, `fork_test.go`, `fork_sqlite_test.go`,
  `cancel_run_test.go`, `completion_test.go`, `execute_contextsnapshot_test.go`,
  `scope_expansion_test.go` (propagate build pin / thay `agentregistry.Empty()`)
- `internal/integration/runtimeengine_test.go` (canonicalization field-aware fix + AdapterBuild thật cho
  gate + 3 test mới + tách Part 1/Part 2 thành store độc lập)
- `internal/integration/testdata/golden/v4-runtime-clean.json`,
  `v4-runtime-crash-recovery.json` (regenerate — chỉ `executionProfileHash` đổi dạng alias, xác nhận
  byte-exact giữa 2 process run riêng biệt trước khi commit)

**Verify:**
```
go build ./...                                           # sạch
go vet ./...                                             # sạch
go run ./cmd/docs-coverage-check                         # debt = 0
gofmt -l <14 file .go đổi/mới>                            # rỗng sau gofmt -w
go test ./internal/app/runtime/... -count=1              # PASS toàn bộ (~30 test trước đó fail đều xanh)
go test ./internal/integration/... -run TestRuntimeEngineGate_AdapterBuildPinning -count=30  # ổn định
go test ./internal/app/runtime/... ./internal/integration/... -count=2                        # ổn định
go test -count=1 ./...                                   # PASS toàn bộ ~70 package
AGENTKIT_REGENERATE_RUNTIME_ENGINE_GOLDEN=1 go test ./internal/integration/... -run TestRuntimeEngineGate$ -count=1  # x2 lần, diff = rỗng (byte-exact giữa 2 process run)
```

**Việc còn lại:** commit, push, mở PR, chờ CI 6/6 (đặc biệt cả hai platform contract job — chính golden
này giờ mới thật sự portable), merge. Task kế tiếp trong remediation pass: V5-08A (task cuối cùng trước
V5-08B chính nó) — audit CHƯA ĐẠT với: Sink/CheckpointStore thiếu JobLease/WriteLease fencing,
`flushLocked` xoá buffer trước khi commit (rủi ro durability), `validateOrderingLocked` không kiểm
`event.AttemptID`, `AgentEvent` thiếu `SchemaVersion`/`ArtifactRefs` (SQLite luôn ghi
`artifact_refs_json='[]'`), checkpoint dùng `SharedStateHash` giả thay vì thật, `ArtifactReferences=nil`.

## V5-08A remediation — post-merge audit fixes (2026-09-09)

**Bối cảnh:** tiếp tục vòng đi lại từng task V5. Audit V5-08A kết luận **"CHƯA ĐẠT production
AgentEvent/checkpoint authority"** với 6 finding trong `agentevents.Sink`/`CheckpointStore`: (1) không
JobLease/WriteLease fencing trên write của Sink; (2) `NewSink` không kiểm mọi repository trong
`EffectiveScope` đều có Mount; (3) `flushLocked` xoá buffer TRƯỚC KHI commit thành công (mất event vĩnh
viễn nếu commit fail thật); (4) `validateOrderingLocked` không kiểm `event.AttemptID` dù
`ports.AgentEvent` đã có field này; (5) `AgentEventRecord` không có field `ArtifactRefs` (SQLite hardcode
`artifact_refs_json='[]'`); (6) `Checkpoint.SharedStateHash` là hash tổng hợp (synthetic), không phải
`WorkflowRun.SharedState` thật, `ArtifactReferences` luôn `nil`.

**Quyết định user về phạm vi (2026-09-09):** việc này lớn hơn hẳn mọi remediation trước đó trong phiên
này (V5-02..V5-08 mỗi task chỉ một chủ đề). Đã hỏi user cách chia phạm vi — chọn: sửa 3 finding cơ học
NGAY (không có bề mặt thiết kế mới, đóng được bằng fake test hôm nay), hoãn JobLease/WriteLease fencing
+ checkpoint completeness (SharedStateHash/ArtifactRefs) sang V5-08B — task đó đã sở hữu quyết định thiết
kế "evidence fencing" của chính nó (Câu hỏi 2 của V5-08B, Đề xuất A: "checkpoint CHÍNH LÀ evidence trail
durable") và sẽ định nghĩa chính xác shape field evidence Checkpoint cần — xây shape đó bây giờ có nguy
cơ phải sửa lại một lần nữa khi biết được contract tiêu thụ thật của V5-08B.

**Fix 1 — `validateOrderingLocked` kiểm `event.AttemptID`:** thêm check đầu tiên trong hàm, trả
`ErrWrongAttempt` (sentinel mới) nếu `event.AttemptID` khác `s.attemptID`. **Xác nhận trực tiếp từ code
(không phải giả định):** cả `claude.go` lẫn `codex.go`'s own `normalizer.emit` ĐÃ stamp
`event.AttemptID = n.attemptID` tập trung tại một chỗ TRƯỚC KHI gọi `Sink.Accept` — 2 adapter thật hoàn
toàn không bị ảnh hưởng bởi fix này, chỉ có fixture của chính các test cũ (chưa từng set AttemptID trên
event literal) cần cập nhật.

**Fix 2 — `flushLocked` giữ buffer cho tới khi commit thành công:** đổi thứ tự — gọi
`WithSerializedWrite` TRƯỚC, chỉ `s.buffer = nil` SAU KHI transaction trả về không lỗi. Vì
`AppendBatch` chạy trong đúng MỘT transaction (all-or-nothing rollback), giữ lại batch để retry sau một
lần fail không có rủi ro duplicate-insert nào — transaction fail thật nghĩa là KHÔNG có gì được ghi.

**Fix 3 — `NewSink` kiểm Mount-coverage:** mọi repository trong `cfg.EffectiveScope` giờ bắt buộc phải có
Mount tương ứng trong `cfg.Mounts`, nếu không trả `ErrMissingMount` (sentinel mới) ngay tại constructor,
trước khi Sink được dùng.

**Test:**
- Cập nhật ~15 event literal cũ trong `sink_test.go`/`sink_sqlite_test.go` (chưa từng set `AttemptID`)
  để set đúng AttemptID của chính Sink mỗi test — không có test nào đổi hành vi, chỉ đóng gap fixture.
- `TestSink_Accept_RejectsEventForWrongAttempt` (mới) — event AttemptID khác Sink's own AttemptID bị
  từ chối với `ErrWrongAttempt`.
- `TestSink_Flush_RetainsBufferOnCommitFailure` (mới) — `failingUnitOfWork` (wrapper cục bộ quanh
  `ports.UnitOfWork` thật, inject lỗi vào `WithSerializedWrite` đúng 1 lần) chứng minh: Flush đầu tiên
  fail → 0 row (chưa persist gì), event vẫn còn trong buffer → Flush retry sau đó persist đúng 1 row —
  không mất event.
- `TestNewSink_RejectsEffectiveScopeRepositoryWithoutMount` (mới) — `EffectiveScope` có 1 repository
  thật (`work.NewRepositoryScope`), không có Mount tương ứng → `NewSink` trả `ErrMissingMount` ngay.

**File thay đổi:**
- `internal/app/agentevents/sink.go` (3 sentinel/fix trên)
- `internal/app/agentevents/sink_test.go` (fixture AttemptID + 3 test mới)
- `internal/app/agentevents/sink_sqlite_test.go` (fixture AttemptID cho 3 test còn thiếu)

**Verify:**
```
go build ./...                                            # sạch
go vet ./...                                              # sạch
go run ./cmd/docs-coverage-check                          # debt = 0
gofmt -l <3 file .go đổi>                                  # rỗng sau gofmt -w
go test ./internal/app/agentevents/... -v -count=1        # PASS 17/17 (14 cũ + 3 mới)
go test ./internal/adapters/providers/... -count=1        # PASS (2 adapter thật không bị ảnh hưởng)
go test ./internal/app/agentevents/... -count=3           # ổn định, không flake
go test -count=1 ./...                                    # PASS toàn bộ ~70 package
```

**Ghi chú hạ tầng (không liên quan code):** đúng lúc task này, GitHub Actions của
`draculemihawk123-ai/agent-workflow` bắt đầu chặn job CI thật ("recent account payments have failed or
your spending limit needs to be increased" — billing, không phải lỗi code) — dần lan từ 2 job phụ
(spike-acceptance, Linux race/stability) tới cả job `contract` chính. Theo quyết định của user, repo gốc
chuyển sang `zlinh4605/agent-workflow` (2 tài khoản `draculemihawk123-ai`/`taQuangLing` được invite làm
collaborator) — `master` cùng 2 branch remediation đang mở (V5-07, V5-08) đã được push sang, PR mở lại
tương ứng trên repo mới (không mang theo được lịch sử PR/comment cũ, chỉ nội dung git).

**Việc còn lại:** commit, push (lên repo mới `zlinh4605/agent-workflow`), mở PR, chờ CI 6/6, merge — có
thể sẽ conflict tại đúng đuôi `baocaov5checklist.md` với PR V5-07/V5-08 (chưa merge tại thời điểm branch
này được tạo), xử lý bằng cách nối các mục theo đúng thứ tự (V5-05 → V5-06 → V5-07 → V5-08 → V5-08A) như
mọi lần trước trong phiên này. Task kế tiếp trong remediation pass: JobLease/WriteLease fencing +
checkpoint completeness (SharedStateHash/ArtifactRefs thật) — hoãn cho V5-08B tự làm cùng lúc với
"evidence fencing" design của chính nó, theo đúng quyết định user ở trên.

## V5-08B — Fenced finalize cho AGENT node (branch `feat/v5-08b-implementation`)

**Ghi chú nguồn:** nghiên cứu và quyết định thiết kế đầy đủ của task này (mục "Quyết định sau review
source of truth — 2026-09-08" bên dưới) ban đầu được viết trên nhánh `feat/v5-08b-fenced-finalize-agent`
(commit tài liệu `c14c7ff`, trước khi repo migrate sang `zlinh4605/agent-workflow`) nhưng CHƯA TỪNG merge
vào `master` — nhánh đó dừng lại ngay sau V5-08A (`7b821fa`), không có bất kỳ code hay remediation nào
sau đó (V5-02..V5-08A remediation, V5-08B0, fix race `workspaceprovision` đều KHÔNG có trên nhánh đó).
Nhánh triển khai thật (`feat/v5-08b-implementation`) được tạo mới từ `master` hiện tại (đã có đủ toàn bộ
remediation + V5-08B0 merge), nên toàn bộ nội dung nghiên cứu/quyết định dưới đây được chép nguyên văn
sang đây trước khi tiếp tục code — nếu không chép lại, quyết định gốc sẽ chỉ còn sống trên một nhánh cục
bộ không bao giờ merge, và PR thật của V5-08B sẽ không có nguồn nào giải thích các lựa chọn thiết kế của
chính nó.

**Nghiên cứu trước khi code** (Explore agent) phát hiện task này lớn/mơ hồ hơn nhiều so với 5 dòng
"Thực hiện" của chính nó — tóm tắt những điểm quan trọng nhất:
- `FinalizeExecutionAttempt` (`internal/app/runtime/finalize.go`) hiện đã fence JobLease
  (`ValidateActiveJob`), WriteLease (`ValidateWriteLeaseFencing`, bao gồm cả generation — SQL đã
  join `write_leases`↔`repository_workspaces` và so `generation`, ĐÃ hoạt động, không cần code mới),
  và attempt version (CAS `ExpectedVersion`). **Chưa có** field/check nào cho diff-scope hay evidence.
  Allow-list cho `SelectedOutcome` (GC-INV-11) đã được enforce ở tầng dưới (`advanceRunTx`), không phải
  trong `FinalizeExecutionAttempt` — bridge chỉ cần không tự ý bypass đường đó.
- `ports.NodeExecutor.Execute(ctx, NodeExecutionRequest) (NodeExecutionResult, error)` — 1 lệnh gọi
  blocking, không có field Prompt/session ref nào cả. `NodeExecutionRequest` chỉ có
  AttemptID/NodeRunID/RunID/ExecutorKind/ExecutionProfileHash.
- **Provider key thật để chọn claude/codex nằm ở `ResolvedExecutionProfileV1.ProviderKey` (field cấp
  cao, không phải `Executor.Kind` — cái đó chỉ là AGENT/COMMAND/MACHINE_GATE)** — đã pin sẵn, chỉ cần
  decode thêm vào `resolvedExecutionProfileView` (1 dòng), không phải xây mới.
- WorkingDirectory/WorkspaceMounts thật: `RepositoryWorkspace.Locator` CHÍNH LÀ chuỗi token của
  `WorkspaceHandle` — `ports.NewWorkspaceHandle(rw.Locator)` dựng lại được ngay, không cần re-provision.
  `gitworktree.Provider.WorkingDirectory` đã là "sanctioned bridge" từ handle sang cwd thật. Nhưng
  **generation bị hardcode = 1 ở MỌI production writer thật** — chưa ai từng test một generation lệch
  thật.
- **Lỗ hổng lớn nhất, không nằm trong đề bài:** không có function nào chuyển `contextsnapshot.Snapshot`
  (V5-04 — chỉ có MessageRefs/ResourceRefs/ContentHash, không text inline) thành request thực thi thật.
  `renderCanonicalSnapshot` duy nhất đang có nhắm sai type (`runtime.ContextSnapshot` — type thoái hóa,
  chính package doc của `contextsnapshot` tự ghi "confirmed unused by the real scheduling/dispatch
  path"). MessageRefs có thể resolve qua Message→ContentArtifactID→ArtifactStore; ResourceRefs hiện còn
  thiếu `OwnerVersionID` so với identity bắt buộc của ADR-012. `contextassembler` chỉ là pure resolver,
  không sở hữu content store: resource content thật phải được load từ exact published DefinitionVersion,
  tìm theo ResourceKey rồi tính lại ContentHash. Ngoài ra `ports.AgentExecutionRequest` hiện thiếu phần
  lớn minimum contract ở go-core-spec §14, nên một `PromptRenderer` đơn lẻ không đóng được lỗ hổng này.
  (Lỗ hổng này đã được đóng riêng bởi V5-08B0, merge trước khi nhánh này được tạo.)
- "Evidence" trong finalize chưa có contract production. Checkpoint V5-08A hiện chưa gắn diff/output
  artifact và một checkpoint bất kỳ giữa chừng không đủ chứng minh terminal proposal. Artifact lifecycle
  đã có primitive `ORPHAN → ATTACHED`; V5-08B phải compose primitive đó với completion checkpoint và
  fenced finalize thay vì xem "checkpoint count ≥ 1" là evidence policy.
- "Provider loss" không có `TerminationReason` riêng, nhưng đã có `CodeProviderUnavailable`; ADR-020
  cố ý tách `TerminationReason` khỏi `AppError.Code` và khóa state–reason matrix. Vì vậy thiếu một reason
  mới không đồng nghĩa thiếu vocabulary để phân loại provider loss.
  `TerminationReasonScopeViolation` đã reserved từ ADR-020 nhưng CHƯA CÓ producer nào — rất có thể
  V5-08B là nơi đầu tiên sinh ra nó (lỗi `scopeguard.ErrScopeViolation` đã được `agentevents.Sink` phát
  hiện giữa chừng ở mỗi checkpoint từ V5-08A, bubble ngược qua `Start`/`Resume` của cả claude.go lẫn
  codex.go — chỉ cần map đúng reason ở finalize, không phải phát hiện lại từ đầu).
- **Không có composition root thật nào** (`cmd/agentkit serve`/`worker` vẫn là stub trống, không nơi
  nào wire `ExecuteNodeHandler`+`workerpool.Pool`+adapter thật với nhau ngoài test) — khớp đúng pattern
  mọi task V4/V5 trước giờ (library code + test, không đụng `cmd/`), nên đây là giả định làm việc, không
  phải câu hỏi cần hỏi lại.

**Bốn câu hỏi lịch sử đã hỏi user qua AskUserQuestion:** user chủ động dismiss cả 4 (không chọn option
nào) và yêu cầu ghi lại vào file. Các option dưới đây được giữ nguyên để audit, kể cả nhãn "khuyến nghị";
chúng là tham khảo tại thời điểm đó, không phải quyết định triển khai. Quyết định sau khi review kỹ nằm
ở mục kế tiếp.

1. **Phạm vi dựng prompt thật cho V5-08B đến đâu?** (câu hỏi quan trọng nhất, ảnh hưởng lớn nhất tới
   kích thước task)
   - *Đề xuất A (khuyến nghị):* Renderer tối thiểu — chỉ resolve MessageRefs→Message→ContentArtifactID→
     byte thật qua ArtifactStore, nối theo thứ tự ref. ResourceRefs bị chặn rõ ràng (lỗi nếu snapshot có
     ResourceRefs, không âm thầm bỏ qua) — đủ để bridge gọi executor thật và test end-to-end cho trường
     hợp phổ biến, không nuốt luôn bài toán tích hợp contextassembler.
   - *Đề xuất B:* Renderer đầy đủ — resolve cả ResourceRefs qua contextassembler's own content store,
     một renderer production-grade hoàn chỉnh ngay trong task này.
   - *Đề xuất C:* Ngoài phạm vi — chỉ định nghĩa interface `PromptRenderer`, dùng fake trong test của
     chính V5-08B; một task riêng sau này mới xây renderer thật.

2. **Định nghĩa "evidence fencing" trong finalize?**
   - *Đề xuất A (khuyến nghị):* Yêu cầu tối thiểu 1 Checkpoint thật tồn tại cho Attempt này
     (`checkpoints.LoadLatestCheckpoint`) trước khi chấp nhận SUCCEEDED — checkpoint chính là evidence
     trail durable (Revisions/SharedStateHash đã pin theo GC-INV-20). Không cần xây Artifact lifecycle
     mới nào.
   - *Đề xuất B:* Xây đường Orphan→Attached thật cho Artifact liên quan (diff/output) — đúng như doc
     comment V5-01 gợi ý, nhưng phức tạp hơn đáng kể.
   - *Đề xuất C:* Bỏ qua evidence fencing ở V5-08B, để lại hoàn toàn cho task sau (V5-10/V5-11).

3. **Finalize có nên tự đo lại diff (real I/O, ngoài transaction) một lần nữa trước khi commit
   SUCCEEDED, hay chỉ dựa vào lỗi `scopeguard.ErrScopeViolation` đã bubble sẵn từ `agentevents.Sink`'s
   own mid-run checkpoint check?**
   - *Đề xuất A (khuyến nghị):* Đo lại thật — gọi `WorkspaceProvider.Diff` cho mỗi mount ngay trước khi
     mở finalize transaction (two-phase pattern giống hệt admission probe: I/O thật ngoài transaction,
     quyết định trong transaction) — đóng khoảng hở giữa checkpoint cuối cùng và lúc process thật sự
     thoát, provider có thể ghi thêm ngoài scope trong khoảng đó mà không ai bắt nếu chỉ dựa vào Sink.
   - *Đề xuất B:* Chỉ dựa vào lỗi bubble từ Sink, không đo thêm — đơn giản hơn nhưng để hở khoảng thời
     gian đó.

4. **"Provider loss" (nêu trong Verify của V5-08B) cần `TerminationReason` mới hay tái dùng
   `EXECUTION_FAILED`?**
   - *Đề xuất A (khuyến nghị):* `TerminationReason` mới (ví dụ `PROVIDER_LOST`) cho trường hợp process/
     kết nối executor chết/mất xác nhận được — khác với FAILED thông thường provider tự report, khớp
     tinh thần GC-INV-18 ("exit code 0 không tự tạo success").
   - *Đề xuất B:* Tái dùng `EXECUTION_FAILED`/`CodeExecutionFailed` fallback hiện có, không thêm reason
     mới.

**Trạng thái tại thời điểm commit `c14c7ff`:** KHÔNG có code nào được viết cho V5-08B; commit chỉ ghi
nghiên cứu và bốn câu hỏi trên. Trạng thái chờ trả lời này đã được thay thế bởi quyết định ngày
2026-09-08 dưới đây.

### Quyết định sau review source of truth — 2026-09-08

Không chọn nguyên xi option A/B/C nào ở trên. Quyết định dựa trên ADR đã accepted, go-core-spec,
roadmap V5 và code production hiện tại; nhãn "khuyến nghị" cũ không có authority cao hơn các nguồn đó.

#### 1. Request/prompt thật: tạo prerequisite "Canonical AgentExecutionRequest assembly"

Tách một prerequisite độc lập, tên làm việc `V5-08B0 — Canonical AgentExecutionRequest assembly`, rồi
để V5-08B phụ thuộc vào nó. Đây không phải chia task theo file: nó là một contract/schema boundary độc
lập và có thể verify riêng. Option A không đủ vì chỉ render MessageRefs, từ chối ResourceRefs và không
tạo toàn bộ request; option B mô tả sai `contextassembler` như content store; option C dùng fake không
đạt E2E của V5-08B. **(Đã DONE — merge trước khi nhánh `feat/v5-08b-implementation` được tạo, xem mục
V5-08B0 phía trên.)**

Phạm vi bắt buộc của prerequisite:

1. Version ContextSnapshot mới phải pin ResourceRef bằng đủ bộ
   `OwnerVersionID + ResourceKey + ContentHash`. Giữ read compatibility cho snapshot cũ; snapshot cũ
   có ResourceRefs thiếu owner không được dispatch âm thầm.
2. Scheduler phải gather candidate từ exact definition/policy pins và chạy `contextassembler.Resolve`
   trước khi persist snapshot; không tiếp tục hardcode `ResourceRefs: nil`. Thứ tự resource đã chọn là
   thứ tự canonical cần giữ nguyên.
3. Request assembler resolve MessageRef qua Message→Artifact row→ArtifactStore; resolve ResourceRef bằng
   cách load exact published DefinitionVersion, tìm đúng key, tính lại identity/hash và fail closed khi
   thiếu hoặc mismatch.
4. Materialize task contract + messages + resources thành deterministic `InstructionArtifact`, gọi
   ArtifactStore `Put` và `Verify` ngoài DB transaction, rồi pin artifact/hash vào execution request.
5. Nâng `ports.AgentExecutionRequest` tới minimum contract của go-core-spec §14: AttemptID, ProviderKey,
   AdapterBuildVersion, InstructionArtifact, ContextSnapshot, mounts có revision/generation/access,
   EffectiveScope, ExecutionProfileHash, IsolationProfile, AllowedCapabilities, Timeout,
   CancellationToken, optional RecoveryCheckpoint và IdempotencyKey. Provider adapter không được tự
   suy quyền/mount từ prompt.
6. Trước spawn phải revalidate snapshot/manifest/profile/fences; bất kỳ ref, hash, version hoặc pin nào
   không hợp lệ đều fail với process spawn count bằng 0.

Verify độc lập: cùng snapshot tạo cùng instruction hash/request; thay đổi thứ tự resource đổi manifest;
message/resource/artifact bị tamper hoặc thiếu owner bị từ chối; mọi field security/scope của request
đến adapter đúng exact pin.

#### 2. Evidence fencing: terminal evidence bundle, không phải "có một checkpoint"

V5-08B định nghĩa `AttemptFinalizationEvidence` typed, tối thiểu gồm terminal event sequence,
completion checkpoint ID, exact final RevisionSet, diff-manifest artifact cho từng repository,
output artifact refs nếu có, typed proposed outcome và schema version.

Protocol ba pha:

1. Sau execution và ngoài DB transaction: redact, `Put` và `Verify` diff/output artifact.
2. Trong transaction chuẩn bị ngắn: insert metadata của chúng ở `ORPHAN`; crash/reject sau bước này để
   lại orphan có thể audit/cleanup, không để artifact chưa được chấp nhận thành evidence.
3. Trong **một** finalize transaction: revalidate JobLease, mọi WriteLease, workspace generation,
   Attempt expected version; kiểm terminal event/checkpoint cùng Attempt/ContextSnapshot, exact final
   RevisionSet, diff scope, output schema và outcome allow-list; chuyển artifact `ORPHAN → ATTACHED`;
   persist completion checkpoint + Attempt/Node transition + canonical event + downstream job/job
   completion atomically. Stale fence làm transaction không commit và artifact vẫn ORPHAN.

`AgentEvent` phải có `SchemaVersion` và `ArtifactRefs`; `AgentExecutionResult` phải có artifact refs và
typed proposed outcome. Completion checkpoint phải dùng shared-state hash canonical thật, không dùng
surrogate ghép từ AttemptID/event sequence/revision. Có thể không có provider output artifact nếu output
policy cho phép, nhưng diff manifest cuối là bắt buộc cho mỗi mount. Đây là evidence về execution/finalize;
criteria-level Evidence thuộc V5-10 và quyết định WorkItem `DONE` theo required evidence thuộc V5-11.

#### 3. Final diff: chỉ đo sau khi process tree đã quiesce

Đo diff cuối là bắt buộc nhưng tự nó chưa đóng TOCTOU. `ProcessSupervisor.Run` phải có postcondition:
chỉ return kết quả xác định khi toàn bộ process tree không còn process có thể ghi. Nhánh parent exit bình
thường cũng phải cleanup/confirm descendants, không chỉ cancellation/timeout. Tree binding không được
"best effort" đối với mutating attempt: nếu không chứng minh quiescence thì không được commit success;
Attempt thành `INDETERMINATE / RECONCILIATION_REQUIRED`, error code `INDETERMINATE`, workspace bị
quarantine.

Thứ tự bắt buộc:

`process tree quiesced → flush AgentEvent sink → final Diff/CaptureRevision → Put/Verify artifacts →`
`finalize transaction re-check fences và commit exact evidence`.

Windows dùng Job Object; Unix dùng process group/isolation primitive và phải kiểm tra group đã hết. V5-08C
sau này tái sử dụng cùng primitive cho cancellation, nhưng normal-completion path cần guarantee này ngay
trong V5-08B. Test bắt buộc có child process tiếp tục ghi sau khi parent exit: supervisor không được trả
success trong lúc child còn sống, và final diff phải bắt được trailing out-of-scope write.

#### 4. Provider loss: không thêm `PROVIDER_LOST` vào TerminationReason

ADR-020 khóa state–reason matrix và tách TerminationReason khỏi AppError.Code. Không sửa enum cục bộ chỉ
để phản ánh một transport diagnostic; nếu sản phẩm thật sự cần reason mới thì phải có ADR superseding.
Mapping production được chốt như sau:

| Tình huống | Attempt state / TerminationReason | ErrorCode |
|---|---|---|
| Provider unavailable trước side effect, hoặc process đã quiesce và outcome failure xác định | `FAILED / EXECUTION_FAILED` | `PROVIDER_UNAVAILABLE` |
| Provider báo execution/task failure xác định | `FAILED / EXECUTION_FAILED` | `EXECUTION_FAILED` |
| Mất kết nối/xác nhận và không chứng minh được kết quả mutating process | `INDETERMINATE / RECONCILIATION_REQUIRED` | `INDETERMINATE` |
| Mất lease ở read-only attempt | `LOST / LEASE_LOST` | `LEASE_LOST` |
| Mất lease ở mutating attempt | `INDETERMINATE / OWNERSHIP_LOST_MUTATING` | `INDETERMINATE` |
| ProviderSessionRef cũ mất/không hợp lệ | Tạo Attempt mới và gọi `Start` từ canonical snapshot | Không gọi `Resume` |

Raw provider reason (`protocol_error`, `process_error`, v.v.) chỉ được lưu làm diagnostic/evidence;
provider-neutral classifier phải sinh typed transport result/execution result trước khi application map
sang state, TerminationReason và ErrorCode. Retry chỉ dựa vào ErrorCode/pinned AttemptPolicy;
`INDETERMINATE` không bao giờ technical-retry tự động.

#### 5. Quản trị checklist

File này là audit/handoff tracked, không phải authority song song với DB/Git. Bảng "Trạng thái hiện tại"
ở đầu file phải được cập nhật khi chuyển task; các status/remaining cũ giữ nguyên nhưng được hiểu là
historical snapshot. Mỗi quyết định mới phải ghi nguồn, scope/non-goals, verify thật và commit/PR/CI tương
ứng; không dùng nhãn "khuyến nghị" trong câu hỏi cũ như authorization để code.

### Thứ tự triển khai đã chốt

1. Cập nhật roadmap/scope với prerequisite `V5-08B0` và dependency của V5-08B. **(DONE.)**
2. Hoàn thành request assembly/snapshot V2 và các contract test fail-closed trước spawn. **(DONE — qua
   V5-08B0.)**
3. Harden normal-exit process-tree quiescence trên Windows/Linux. **(DONE — xem bên dưới.)**
4. Nâng AgentEvent/AgentExecutionResult và xây terminal evidence staging. **(ĐANG LÀM — xem bên dưới.)**
5. Nối NodeExecutor→AgentExecutor và mở rộng fenced finalize. **(CHƯA LÀM.)**
6. Chạy E2E: success; provider-declared failure; exit code 0 thiếu/invalid outcome; missing/tampered
   evidence; stale JobLease/WriteLease/generation/attempt version; trailing out-of-scope write;
   provider unavailable xác định; provider loss không xác định; replacement Attempt có `Start` count
   đúng và `Resume` count bằng 0. **(CHƯA LÀM.)**

### Triển khai thật — Bước 3: harden ProcessSupervisor quiescence postcondition (2026-09-09)

**Thay đổi:** `processTree` interface (`internal/adapters/process/processtree.go`) thêm
`quiesced(cmd *exec.Cmd) (bool, error)`. Unix (`processtree_other.go`):
`syscall.Kill(-cmd.Process.Pid, 0)`, `ESRCH` → quiesced=true. Windows (`processtree_windows.go`):
`windows.QueryInformationJobObject` với information class `JobObjectBasicAccountingInformation` — struct
Win32 tương ứng (`JOBOBJECT_BASIC_ACCOUNTING_INFORMATION`) KHÔNG có sẵn dạng typed trong
`golang.org/x/sys/windows` (khác `JOBOBJECT_EXTENDED_LIMIT_INFORMATION`, có sẵn) nên phải tự định nghĩa
đúng layout ABI; `ActiveProcesses == 0` → quiesced=true. `Supervisor.Run` gọi `confirmTreeQuiesced`
(poll bounded, 20ms/lần, tối đa 2s) ngay sau khi set `FinishedAt`/`ExitCode`/`OutputTruncated`, TRƯỚC
nhánh rẽ theo `terminationCause` — chạy trên **mọi** exit path, không riêng cancel/timeout, đúng yêu cầu
quyết định #3 ở trên ("nhánh parent exit bình thường cũng phải cleanup/confirm descendants"). Kết quả:
field mới `ports.ProcessResult.TreeQuiesced bool`.

**Lỗi tự phát hiện khi viết test:** thiết kế test ĐẦU TIÊN cố quan sát process con orphan SAU KHI `Run()`
return (marker file có tiếp tục được ghi hay không) — fail nhất quán ở đúng mốc ~2.1-2.3s cả 3 lần chạy
lại. Root cause: trên Windows, `defer tree.close()` (chạy khi `Run()` return) gọi
`windows.CloseHandle(job)` trên một Job Object có `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` — flag này giết
NGAY LẬP TỨC mọi process còn lại trong job tại thời điểm handle đóng, nghĩa là process con orphan ĐÃ CHẾT
trước khi test code chạy tiếp, bất kể `TreeQuiesced` bên trong báo đúng gì. Sửa: chứng minh liveness bằng
TIMING nội bộ của `Run()` thay vì quan sát process sau khi return — assert `elapsed >=
quiescencePollBound - 100ms` (tức `Run` phải đợi gần hết trọn 2s poll bound, chứng minh process con thật
sự còn sống trong suốt lúc poll) + xác nhận marker file tồn tại — không bao giờ cần quan sát process sau
khi `Run` return nữa. Test mới: `TestSupervisorNormalExit_TreeQuiescedFalseWhileDescendantStillRuns`
(case "orphan" mới trong `TestProcessHelper` — con exit ngay không đợi cháu, khác case "descendant" có
sẵn). Assertion `TreeQuiesced` cũng thêm vào `TestSupervisorCancelKillsDescendantProcess` hiện có.

**File thay đổi:** `internal/adapters/process/processtree.go`, `processtree_other.go`,
`processtree_windows.go`, `supervisor.go`, `supervisor_test.go`, `internal/app/ports/agent.go`
(`ProcessResult.TreeQuiesced`).

**Verify:**
```
go build ./...                       # sạch (Windows)
GOOS=linux go build ./...            # sạch (cross-compile)
go vet ./...                         # sạch
go test ./internal/adapters/process/... -v -count=1   # PASS, kể cả test quiescence mới
go test -count=1 ./...               # PASS toàn bộ
```
`-race` không chạy được local — để CI's "Linux race and stability" job xác nhận, như mọi lần trước.
Commit: `3a86477` — "feat(v5-08b): harden ProcessSupervisor's quiescence postcondition".

### Triển khai thật — Bước 4 (đang làm): nâng AgentEvent, JobLease/WriteLease fencing cho Sink (2026-09-09)

**4a — `AgentEventRecord.ArtifactRefs`:** quyết định #2 ở trên yêu cầu `AgentEvent` phải có
`ArtifactRefs` (đã có `SchemaVersion` từ V5-08A). Thêm field `[]string` vào `ports.AgentEventRecord`.
SQLite adapter (`internal/adapters/sqlite/agent_events.go`) trước đây HARDCODE
`artifact_refs_json='[]'` trong `AppendBatch` bất kể caller truyền gì — giờ marshal thật
(`nil` → `[]` để round-trip ổn định, không lỗi khi đọc lại), `ListByAttempt` unmarshal thật thay vì bỏ
qua cột. `fake.AgentEventsRepository` không cần sửa (lưu/trả nguyên struct value, field mới tự đi kèm).
Test mới: `TestAgentEventsRepository_AppendBatch_RoundTripsArtifactRefs` (2 chiều: có ref và không ref,
chạy trên sqlite thật).

**4b — JobLease/WriteLease fencing cho `agentevents.Sink`:** đóng finding #1 hoãn từ V5-08A remediation
("Sink ghi event/checkpoint mà không kiểm JobLease/WriteLease còn hiệu lực — worker đã mất quyền vẫn ghi
như còn quyền"), đúng tinh thần quyết định #2 ở trên ("evidence fencing" phải là fencing thật, không chỉ
tồn tại). `Config` thêm `JobLease ports.JobLease` (bắt buộc — `NewSink` fail closed nếu `JobID` rỗng) và
`WriteLeases []ports.WriteLeaseGrant` (tùy chọn, mặc định rỗng). `Sink` lưu cả hai. Method mới
`validateFencingLocked` TÁI DÙNG đúng 2 lời gọi `finalize.go`'s own fencing pattern đã dùng
(`tx.Jobs().ValidateActiveJob` rồi loop `tx.Runtime().ValidateWriteLeaseFencing` cho từng grant), không
phát minh cơ chế mới. `flushLocked` gọi nó TRƯỚC `AppendBatch`, trong CÙNG transaction — atomic với chính
write. `captureCheckpointLocked` gọi nó qua một `WithReadOnly` RIÊNG ngay trước `StoreCheckpoint` — ghi rõ
trong code comment đây KHÔNG atomic với chính write (`StoreCheckpoint` là API autocommit cũ, chưa
`ports.Tx`-composable) — chấp nhận một khoảng TOCTOU hẹp như cải thiện chặt hơn hẳn so với không kiểm gì,
thay vì mở rộng phạm vi việc này để đưa checkpoint storage lên `ports.Tx` (ngoài phạm vi sub-task này).

**Test fixture ripple:** `NewSink` bắt buộc `JobLease` phá vỡ toàn bộ 17 test hiện có (cả
`sink_test.go` fake-backed lẫn `sink_sqlite_test.go` real-sqlite-backed) — dự kiến, không phải bug mới.
Thêm `testJobLease` (`sink_test.go`, fake: `EnqueueJob` qua `WithSerializedWrite` + `SetActiveLease` trực
tiếp trên `uow.Snapshot`) và `testSQLiteJobLease` (`sink_sqlite_test.go`, real: `store.EnqueueJob` +
`store.ClaimJob`) — cùng attempt fixture `"attempt-1"`/`sqliteFixtureAttemptID` mọi test đã dùng sẵn.
SQLite adapter đòi `MaxClaims > 0` (fake thì không enforce) — set `MaxClaims: 1`.

**Test fencing thật sự reject (không chỉ accept):** mọi test ở trên chỉ set lease HỢP LỆ rồi Sink hoạt
động bình thường — không chứng minh `validateFencingLocked` THẬT SỰ chặn khi lease đã mất hiệu lực. Thêm
`TestSink_Flush_RejectsWhenJobLeaseFenced` (`sink_test.go`): sau khi Sink được tạo với lease hợp lệ, mô
phỏng một worker khác giành lại đúng job đó (`SetActiveLease` lại với Owner/Token mới, đúng cách một
`ClaimJob` thật bump token) rồi gọi `Flush` — assert `errors.Is(err, ports.ErrJobLeaseLost)` và 0 row
được ghi. **Không** thêm test WriteLease-fencing-reject: `fake.RuntimeRepository.ValidateWriteLeaseFencing`
là stub luôn-pass có chủ đích từ V4-05 ("deep WriteLease fencing edge cases are SQLite-only... a fake
test that wants a write-lease rejection path is testing the wrong layer" — doc comment gốc), và hiện
CHƯA có sink test nào truyền `WriteLeases` (rỗng ở cả 4 test sqlite) — dựng fixture `write_leases`/
`repository_workspaces` thật cho một reject-test riêng ở đây sẽ lấn sang phạm vi Bước 6 (E2E), nơi
"stale JobLease/WriteLease/generation/attempt version" đã được liệt kê rõ là một trong các kịch bản bắt
buộc phải test — để lại đúng chỗ đó, không tự ý thu hẹp phạm vi Bước 6 bằng cách làm trước một phần ở đây.

**File thay đổi:** `internal/app/ports/agentevent.go` (+`ArtifactRefs`), `internal/adapters/sqlite/
agent_events.go` + `agent_events_test.go` (ArtifactRefs thật), `internal/app/agentevents/sink.go`
(+`JobLease`/`WriteLeases`, `validateFencingLocked`, wiring vào `flushLocked`/`captureCheckpointLocked`),
`internal/app/agentevents/sink_test.go` + `sink_sqlite_test.go` (fixture ripple + 1 test reject mới).

**Verify:**
```
go build ./...                                            # sạch
go vet ./...                                               # sạch
go run ./cmd/docs-coverage-check                           # debt = 0
gofmt -l <6 file .go đổi>                                  # 4 flagged là CRLF-only (xác nhận: gofmt
                                                            #   và bản gốc trùng md5 sau khi tr -d '\r'
                                                            #   cả hai phía) — không phải lỗi format thật
go test ./internal/app/agentevents/... -v -count=1        # PASS 18/18 (14 cũ + ArtifactRefs +
                                                            #   RejectsWhenJobLeaseFenced +
                                                            #   2 test đã có tên đổi ripple)
go test -count=1 ./...                                     # PASS toàn bộ ~70 package
```

**Việc còn lại (Bước 4) tại thời điểm đó:** đã hoàn thành đầy đủ bên dưới — xem các mục kế tiếp.

### Triển khai thật — Quyết định bổ sung: giao thức "terminal outcome marker" cho AGENT node (2026-09-09)

**Bối cảnh:** quyết định #2 gốc yêu cầu `AgentExecutionResult` phải có "typed proposed outcome", nhưng
đọc trực tiếp `claude.go`/`codex.go` xác nhận: KHÔNG hề có cơ chế nào để provider tự báo cáo nó chọn
outcome nào trong số các outcome node đã khai báo. Ba vòng hỏi lại user (AskUserQuestion) để chốt đúng vì
đây là quyết định sản phẩm thật, không thể tự suy đoán:

1. **Vòng 1 — "AGENT phải khai đúng 1 outcome"**: đề xuất ban đầu của tôi, User BÁC BỎ ngay vì phá
   `TestValidateDocumentAcceptsBoundedCycleWithEscalationExit` ([compiler_test.go:245](internal/domain/workflow/compiler_test.go:245)) — một AGENT node với `CyclePolicy` hợp lệ khai 2 outcome ("retry" tự-loop +
   "done" là `EscalationOutcome`). Đọc `advance.go:582` xác nhận runtime tự set
   `skippedNodeRun.SelectedOutcome = downstreamNode.CyclePolicy.EscalationOutcome` trên một NodeRun bị
   SKIP — agent không hề được gọi thực thi ở vòng escalation, nên không cần "derive proposal" cho outcome
   đó.
2. **Vòng 2 — loại `EscalationOutcome` khỏi tập agent-selectable, còn lại đúng 1 → bridge tự suy ra**:
   User chỉ ra bằng chứng KHÁC còn mạnh hơn — fixture `boundedCycleDocument` ([cycle_test.go:39](internal/app/runtime/cycle_test.go:39)) có node "checker" khai BA outcome
   `pass/rework/escalate` (escalate là EscalationOutcome); sau khi loại escalate, "checker" vẫn còn ĐÚNG
   HAI outcome agent-selectable thật (`pass`/`rework`) — một use case multi-outcome AGENT có thật, không
   phải giả thuyết. Kết luận: multi-outcome AGENT KHÔNG được cấm; cần một giao thức thật để agent tự báo
   cáo outcome đã chọn khi có >1 lựa chọn; phần protocol này phải làm TRONG V5-08B/B0, không hoãn sang
   V5-10/11 (agent tự báo cáo là core evidence, không phải criteria-level evidence).
3. **Vòng 3 — chốt cơ chế encoding**: hai phương án — (a) marker cấu trúc ở cuối assistant message cuối
   cùng, parse bằng text matching thuần; (b) tool-call riêng, cần agent gọi 1 tool không đăng ký (RỦI RO:
   chưa xác minh Claude Code CLI/Codex CLI cho phép việc này mà không lỗi/treo trong chế độ stream-json
   hiện dùng). User chọn (a), format cụ thể:
   `<agentkit-outcome>{"schemaVersion":1,"outcome":"pass"}</agentkit-outcome>`

**Quy tắc đã chốt với user (áp dụng nguyên văn):**
- Marker là nội dung cuối cùng (ngoài whitespace) của assistant message CUỐI, xuất hiện đúng 1 lần trong
  toàn bộ execution.
- Claude/Codex chỉ PARSE marker nếu có mặt — lưu candidate ngay khi thấy, CHỈ xác nhận khi nhận terminal
  event (result/turn.completed), vì lúc đọc text chưa biết đó có phải message cuối hay không.
- Strip marker khỏi `ASSISTANT_MESSAGE` event trước khi persist — marker không bao giờ chạm tới durable
  storage.
- Thiếu, trùng, malformed, hoặc outcome ngoài `AllowedOutcomes` → protocol error, không tạo success, không
  fallback `AllowedOutcomes[0]`.
- Với ĐÚNG 1 agent-selectable outcome: marker KHÔNG bắt buộc — bridge tự derive
  (`AgentOutcomeDerivedSingleAllowed`). Với >1: marker bắt buộc; adapter chỉ parse, KHÔNG tự quyết "thiếu
  marker có phải lỗi không" (đó là việc của bridge, vì chỉ bridge biết `AllowedOutcomes` có thật sự là một
  lựa chọn hay không).
- `CyclePolicy.EscalationOutcome` không bao giờ nằm trong `AllowedOutcomes`; runtime vẫn tự đặt nó trên
  NodeRun SKIPPED như hiện tại, không đổi.

**Triển khai:**
- `internal/domain/workflow/validation.go`: sửa lại rule AGENT (thay literal "đúng 1 outcome" bằng "ít
  nhất 1 outcome sau khi loại EscalationOutcome") + test mới `agent with only its own escalation outcome`
  ([compiler_test.go](internal/domain/workflow/compiler_test.go)).
- `internal/app/ports/agent.go`: `AgentExecutionRequest` +`AllowedOutcomes []string`; `AgentExecutionResult`
  +`TreeQuiesced`/+`ArtifactRefs` (shape only, chưa adapter nào sinh ra)/+`ProposedOutcome
  *AgentProposedOutcome`; type mới `AgentProposedOutcome{Value, Source, SchemaVersion}`,
  `AgentOutcomeSource` (`REPORTED_BY_PROVIDER`, `DERIVED_SINGLE_ALLOWED`).
- `claude.go`/`codex.go`: `extractOutcomeMarker`/`trackOutcomeMarker`/`resolveProposedOutcome` (logic giống
  hệt, duplicate có chủ đích — 2 package độc lập, không base chung); hook vào `emit()` (một điểm chung cho
  mọi `ASSISTANT_MESSAGE`, dùng chung cho cả `consumeAssistant` của Claude lẫn `consumeItem` agent_message
  của Codex); `finalStatus` đổi signature thêm `allowedOutcomes []string`, trả thêm `*AgentProposedOutcome`.
  `result.TreeQuiesced` cũng gán từ `process.TreeQuiesced` (Bước 4 còn thiếu, gộp vào đây).
- `internal/adapters/providers/fixtures.go`: 3 mode fixture mới (`outcome-success`, `outcome-malformed`,
  `outcome-duplicate`) + `OutcomeMarker()` helper — dùng chung giữa test binary và `cmd/fake-claude`/
  `cmd/fake-codex`.
- `internal/adapters/providers/contract_test.go`: `TestAgentExecutorTerminalOutcomeMarker` — 5 kịch bản ×
  2 provider (valid/reported, absent/nil, out-of-range/rejected, malformed/rejected, duplicate/rejected).
- `internal/app/runtime/assemble_execution_request.go`: `agentSelectableOutcomes(node)` (loại
  EscalationOutcome); load `WorkflowVersion.Document()`, `findNode` theo `nodeRun.NodeKey`, fail closed nếu
  0 agent-selectable outcome — đây chính là "runtime defense cho WorkflowVersion cũ" user yêu cầu (chạy
  MỖI LẦN `AssembleAgentExecutionRequest` chạy, kể cả revalidate ngay trước spawn, nên một version cũ đã
  publish trước khi có rule compile-time vẫn KHÔNG BAO GIỜ spawn được với 0 outcome hợp lệ).

**Verify:**
```
go build ./...                                            # sạch
go vet ./...                                               # sạch
go test ./internal/domain/workflow/... -v -count=1        # PASS, kể cả test escalation-only mới
go test ./internal/adapters/providers/... -v -count=1      # PASS, 10 test outcome-marker mới (5×2)
go test ./internal/app/runtime/... -count=1                # PASS
go test -count=1 ./...                                     # PASS toàn bộ ~70 package
```

### Triển khai thật — Bước 4 (tiếp) + Bước 5: Tx-composable checkpoint, bridge, finalize evidence (2026-09-09)

**`ports.CheckpointsRepository`** (`internal/app/ports/checkpoint.go`, mới): accessor Tx-composable thứ 2
cho Checkpoint, chỉ dùng cho completion checkpoint của finalize — checkpoint giữa chừng của
`agentevents.Sink` giữ nguyên `CheckpointStore` autocommit cũ, không đổi. `InsertCheckpoint(ctx,
checkpoint) error` — implement thật ở `internal/adapters/sqlite/checkpoint_store.go` (INSERT thuần, UNIQUE
conflict là bug thật, không dedup như `StoreCheckpoint`) và fake ở `internal/app/ports/fake/checkpoint.go`
(cùng discipline). Test Tx-composable riêng
(`TestCheckpointsRepository_InsertCheckpoint_CommitsWithinCallerTransaction`, sqlite thật): chứng minh cả
commit lẫn rollback (transaction fail → 0 row) và UNIQUE conflict thật bị từ chối.

**`AgentNodeExecutor`** (`internal/app/runtime/agent_node_executor.go` +
`agent_node_executor_resources.go`, mới) — production `ports.NodeExecutor`, bridge NodeExecutor→
AgentExecutor mà `execute.go` gọi "V4-05's fake executor tương lai":
1. Gọi `AssembleAgentExecutionRequest` (V5-08B0) lấy request thật.
2. `resolveExecutionResources`: Phase 1 (read-only tx) load `WorkflowRun.FamilyID` →
   `WorkspaceSet` → mỗi mount's `RepositoryWorkspace.Locator` thật; Phase 2 (I/O thật, ngoài tx):
   `ports.NewWorkspaceHandle(locator)` + `workspaces.WorkingDirectory` — đúng chỗ
   `AssembleAgentExecutionRequest`/`admission.go`'s `buildExecutionEnvelope` đã cố tình để trống, đúng như
   comment cũ đã ghi "V5's own real executor is the first caller". Với mount WRITE: gọi thật
   `writeLeases.AcquireWriteLeases` (execute.go's own doc comment gọi đích danh bridge này là "the first
   caller with a genuine reason to... call AcquireWriteLeases before Execute") — TTL = Attempt's own
   Timeout + 2 phút buffer (không có heartbeat mid-flight vì `AgentExecutor.Start` block đồng bộ, không có
   hook để heartbeat giữa chừng).
3. Resolve `ports.AgentExecutor` thật qua `agentregistry.Registry.Resolve(request.ProviderKey, ...)`.
4. Dựng `agentevents.Sink` thật, fenced bằng `req.JobLease`/`resolved.writeLeaseGrants` thật (không phải
   `nil`/rỗng như trước khi có bridge).
5. LUÔN gọi `Start`, KHÔNG BAO GIỜ `Resume` (ADR-005 "Alpha luôn khởi động agent mới từ ContextSnapshot" —
   mọi Attempt, kể cả retry V4-06, luôn là Attempt MỚI gọi Start; không có caller thật nào cho `Resume`
   trong Alpha — khớp đúng mapping #4 dòng cuối "ProviderSessionRef cũ mất → Start, không Resume").
6. `sink.Flush(ctx)` bắt buộc ngay sau `Start` trả về (Sink's own doc "caller's own responsibility to call
   exactly once... regardless of its own error").
7. `classify`: áp bảng mapping provider-loss ĐÃ CHỐT ở quyết định #4 gốc, rút gọn còn 2 trường hợp thật sự
   "finalizable" (SUCCEEDED, FAILED định) — LOST/INDETERMINATE (dòng 3-5 bảng gốc: mất lease giữa chừng,
   quiescence không xác nhận trên mutating attempt) đều map vào MỘT sentinel mới `ErrIndeterminateExecution`
   thay vì cố propose một NextState — vì `isFinalizableExecutionAttemptState` (finalize.go) LOẠI TRỪ hẳn
   LOST/INDETERMINATE (không có lease sống nào để fence cho transition đó), nên bridge không được phép tự
   "phát minh" một finalize call cho 2 state này; `execute.go`'s `Handle` nhận diện sentinel này giống hệt
   nhánh "outer-cancellation" có sẵn — bỏ qua finalize, để Attempt RUNNING cho crash-recovery path (V4-13,
   `internal/app/worker/interruption.go`) xử lý.
8. Trên success: `buildEvidence` — đo diff MỖI mount SAU KHI xác nhận quiesce (dùng `TreeQuiesced` đã có từ
   Bước 3), gọi `scopeguard.ValidateDiffs` NGAY (cùng discipline `agentevents.Sink`'s own
   `captureCheckpointLocked` áp cho checkpoint giữa chừng — nhưng đây là lần đo CUỐI, sau quiesce, mà
   `Sink` không bao giờ thấy) trước khi Put/Verify bất kỳ artifact nào; vi phạm scope → propose
   FAILED/`TerminationReasonScopeViolation`/`CodeScopeViolation` — ADR-020 reserve reason này từ trước
   nhưng CHƯA CÓ producer thật nào; bridge V5-08B là producer đầu tiên. Put/Verify từng diff làm artifact
   riêng, insert ORPHAN trong MỘT transaction ngắn (protocol phase 2 của quyết định #2 gốc). Mint
   `CompletionCheckpointID` (bridge tự chọn ID, finalize dùng chính ID này khi build Checkpoint thật —
   không phải finalize tự sinh).
9. `resolveSelectedOutcome`: nếu adapter đã parse marker → dùng luôn; nếu không và `AllowedOutcomes` có
   đúng 1 phần tử → tự derive `DERIVED_SINGLE_ALLOWED`; nếu >1 mà không có marker → FAILED/
   `TerminationReasonOutcomeRejected`/`CodeValidationFailed` (tái dùng đúng pattern execute.go đã có cho
   scope-expansion proposal sai hình dạng, không phát minh reason mới).

**`FinalizeExecutionAttempt` mở rộng** (`internal/app/runtime/finalize.go`): field mới
`Evidence *ports.AttemptFinalizationEvidence` trên request — `nil` với MỌI caller cũ (fake NodeExecutor
V4-05, test hiện có) → hành vi cũ giữ nguyên 100%, xác nhận bằng cách chạy lại toàn bộ suite `internal/app/
runtime` không đổi kết quả. Khi `Evidence != nil` VÀ `NextState == SUCCEEDED`: hàm mới
`attachFinalizationEvidenceTx` chạy TRONG transaction fenced sẵn có (Bước 1/3 đã fence JobLease/WriteLease
từ trước khi tới đây), theo đúng phase 3 của protocol quyết định #2 gốc — validate
`ProposedOutcome.Value == req.SelectedOutcome`; xác nhận hàng `agent_events` với đúng
`TerminalEventSequence` tồn tại thật; xác nhận tập diff-manifest artifact ĐẦY ĐỦ so với
`NodeRun.EffectiveScope` (đúng số lượng, đúng repository set — nội dung diff đã được `scopeguard.ValidateDiffs`
kiểm ở bridge rồi, TRƯỚC transaction, vì kiểm lại từ artifact bytes trong transaction sẽ vi phạm "không
gọi ArtifactStore trong transaction"); chuyển từng artifact ORPHAN→ATTACHED (đúng hướng V5-01 vốn đã dùng,
KHÔNG PHẢI hướng ngược lại — sửa lại doc comment cũ trên `TransitionArtifactAttachState`, vốn đoán sai
hướng); build + insert completion Checkpoint thật qua `tx.Checkpoints().InsertCheckpoint` — dùng
`canonicalStateHash(run.SharedState)` thật (không phải surrogate của Sink), `Sequence` =
`CanonicalEventSequence` = `evidence.TerminalEventSequence` (an toàn không đụng `checkpointSeq` riêng của
Sink — không có cách nào an toàn đọc `LoadLatestCheckpoint` giữa chừng transaction vì đó là API autocommit
riêng, một connection khác, có nguy cơ deadlock SQLite nếu gọi khi transaction hiện tại đang giữ write
lock).

**File thay đổi:** `internal/app/ports/checkpoint.go` (mới), `internal/app/ports/fake/checkpoint.go`
(mới), `internal/adapters/sqlite/checkpoint_store.go` + `checkpoint_store_test.go`,
`internal/adapters/sqlite/unitofwork.go`, `internal/app/ports/fake/unitofwork.go`,
`internal/app/runtime/agent_node_executor.go` (mới), `agent_node_executor_resources.go` (mới),
`agent_node_executor_test.go` (mới), `internal/app/runtime/execute.go` (JobLease vào
`NodeExecutionRequest`, nhận diện `ErrIndeterminateExecution`, `Evidence` vào FinalizeExecutionAttemptRequest),
`internal/app/runtime/finalize.go` (`Evidence` field + `attachFinalizationEvidenceTx`),
`internal/app/ports/execution.go` (`NodeExecutionResult.Evidence`, type mới `AttemptFinalizationEvidence`/
`DiffManifestArtifactRef`), `internal/app/ports/workspace.go` (`WorkspaceProvider.WorkingDirectory` — method
mới, cần sửa mọi fake implement interface này: `internal/app/agentevents/sink_test.go`,
`internal/app/runtime/commands_test.go`, `internal/app/runtime/scope_expansion_test.go`,
`internal/app/work/scope_expansion_workspaceprovision_test.go`,
`internal/app/workspaceprovision/handler_test.go`, `internal/app/workspacereconcile/handler_test.go`,
`internal/integration/runtimeengine_test.go`), `internal/app/ports/artifactrecord.go` (sửa doc comment
`TransitionArtifactAttachState`).

**Bước 6 (E2E) — làm được bao nhiêu trong phạm vi PR này:** 7 test end-to-end mới trong
`agent_node_executor_test.go`, dùng fake UOW + fake `WorkspaceProvider`/`WriteLeaseManager`/`AgentExecutor`
cục bộ (không cần git thật hay subprocess CLI thật — `gitworktree` package và
`internal/adapters/providers`'s own contract_test.go đã tự chứng minh riêng phần Diff/WorkingDirectory và
provider-adapter translation rồi, cùng discipline "không cần toàn bộ subprocess machinery cho một concern
một lớp bên dưới" mà `contract_test.go` đã có sẵn):
- `TestAgentNodeExecutor_Success_BuildsEvidenceAndFinalizesEndToEnd` — golden path đầy đủ: assembly →
  resolve mount/write-lease thật → Start → evidence → ORPHAN → finalize → ATTACHED → NodeRun advance.
- `TestAgentNodeExecutor_LeaseLostMidExecution_ReturnsIndeterminate` — JobLease bị cướp giữa chừng.
- `TestAgentNodeExecutor_UnconfirmedQuiescenceOnMutatingAttempt_ReturnsIndeterminate` — `TreeQuiesced=false`
  trên mutating attempt.
- `TestAgentNodeExecutor_OutOfScopeDiff_RejectsAsScopeViolation` — diff cuối vi phạm scope.
- `TestAgentNodeExecutor_ProviderDeclaredFailure_ReturnsExecutionFailed` — mapping row 2.
- `TestAgentNodeExecutor_ProviderUnavailableBareError_ReturnsProviderUnavailable` — mapping row 1.
- `TestFinalizeExecutionAttempt_TamperedEvidence_RejectsBeforeCommitting` — evidence giả (sai
  TerminalEventSequence) bị từ chối, xác nhận transaction rollback toàn bộ (Attempt version không đổi,
  artifact vẫn ORPHAN).

**Chưa làm / cố ý để lại (ghi rõ, không giấu):**
- Test multi-outcome THẬT ở tầng bridge (>1 `AllowedOutcomes`, marker bắt buộc) — đã có ở tầng adapter
  (`contract_test.go`'s 10 test outcome-marker) nhưng `assembleRequestFixture` dùng cố định
  `agentExecutableDocument` (1 outcome); dựng fixture multi-outcome riêng cho bridge cần sửa/nhân bản
  fixture chain — hoãn, không chặn vì logic `resolveSelectedOutcome` đã test đủ qua đường single-outcome +
  qua unit-level của chính adapter.
- Test WriteLease-cụ-thể bị stale/generation lệch ở finalize — `FinalizeExecutionAttempt`'s own
  `ValidateWriteLeaseFencing` fencing đã có test tổng quát từ V4-05, không lặp lại riêng cho AGENT path.
- KHÔNG có composition root (`cmd/agentkit serve/worker` vẫn stub) nối `AgentNodeExecutor` thật vào
  `ExecuteNodeHandler` trong production — đúng pattern MỌI task V4/V5 trước giờ (library code + test,
  không đụng `cmd/`), không phải thiếu sót của riêng task này.

**Verify:**
```
go build ./...                                            # sạch
go vet ./...                                               # sạch
go run ./cmd/docs-coverage-check                           # debt = 0
go test ./internal/app/runtime/... -v -count=1             # PASS toàn bộ, kể cả 7 test E2E mới
go test -count=1 ./...                                     # PASS toàn bộ ~70 package (2 lần liên tiếp,
                                                            #   1 lần gặp đúng flake đã biết trước
                                                            #   TestProjectWorkspaceGate — pass khi chạy
                                                            #   lại riêng lẻ, cùng lớp timing-flake đã
                                                            #   ghi nhận trước đó trong phiên này)
```

**Việc còn lại:** commit (đã xong theo từng phần: `806f416` outcome marker, `56b49a0` AllowedOutcomes +
Tx checkpoint, `fe85099` bridge, `8138166` finalize evidence + E2E, `e11c354` E2E bổ sung — tổng cộng 6
commit trên nhánh kể từ `3a86477` Bước 3), push nhánh `feat/v5-08b-implementation` lên
`zlinh4605/agent-workflow`, mở MỘT PR duy nhất cho toàn bộ V5-08B (đúng lựa chọn user), chờ CI 6/6, merge.

## V5-08C — Cancellation execution path (branch `feat/v5-08c-cancellation-execution-path`)

**Bối cảnh / nghiên cứu trước khi code:** `cancel_run_coordinator.go`'s own doc comment đã tự thừa nhận
lỗ hổng chính task này phải đóng: một `ExecutionAttempt` thật đang RUNNING (process/provider thật đang
chạy) bị `CancelRunCoordinatorHandler` **bỏ qua nguyên văn**, y hệt BLOCKED — "Alpha has no process-kill;
it runs to its own natural conclusion". V4-12B (worker-side re-check trước khi gọi executor,
`h.runIsCancelling` ở `execute.go`) và V4-13 (crash-recovery/interruption sau khi executor đã dừng vì lý
do khác) đều đã có sẵn — mảnh còn thiếu duy nhất là: làm sao một `RunCancellationIntent` được ghi NHẬN
GIỮA lúc executor đang chạy thật sự dừng được process đó, và Attempt được phân loại đúng sau khi nó dừng.

Đọc `internal/adapters/process/supervisor.go`'s own `Supervisor.Run` phát hiện: `ctx` truyền vào đã được
dùng để dựng `deadline := context.WithTimeout(ctx, spec.Timeout)` — hủy `ctx` (không chỉ hết timeout)
CŨNG khiến `deadline.Done()` fire (child kế thừa cancellation của parent theo đúng ngữ nghĩa
`context` package), và `setCancellationResult` đã set đúng `result.Cancelled = true` (không phải
`TimedOut`) khi `terminationCause` là `context.Canceled`. **Kết luận: propagation ctx-cancel xuống tận
process thật đã hoạt động sẵn — không cần thêm plumbing gì ở tầng `ProcessSupervisor`.** Việc còn lại
hoàn toàn nằm ở tầng app: (1) khi nào hủy ctx, và (2) diễn giải kết quả `AgentExecutionCancelled` mà
`claude.go`/`codex.go`'s own `finalStatus` đã trả về sẵn (check đầu tiên, trước cả terminalSeen/protocol)
thành gì cho Attempt.

**Quyết định (không hỏi lại, theo đúng "tự giác tiếp tục"):**
1. **Không phát minh cơ chế live-signal mới.** `workerpool.Pool.runJob` có `jobCtx`/`cancelJob` riêng cho
   từng job nhưng không có registry cho goroutine khác với tới hủy đúng 1 job cụ thể — đúng với triết lý
   "trust durable state, never a live signal" toàn bộ codebase đã theo từ đầu. Thay vào đó: một poller
   trong chính `ExecuteNodeHandler.Handle` (`execute.go`) tự đọc lại `WorkflowRun.State` mỗi
   `cancellationPollInterval` (500ms) TRONG LÚC executor đang chạy — poll đọc, không phải push.
2. **`execCtx` là con của `attemptCtx`, không phải chính `attemptCtx`.** Nếu poller hủy thẳng
   `attemptCtx`, nhánh cũ "`attemptCtx.Err() != nil` → để RUNNING" sẽ tự động nuốt luôn case mới này mà
   không bao giờ phân loại được gì khác — phải tách hẳn một context con để giữ nguyên khả năng phân biệt
   "deadline của chính mình" / "ctx ngoài bị hủy vì lý do khác" / "poller vừa phát hiện cancel thật".
3. **Bridge (`AgentNodeExecutor`) tự re-derive lại nguyên nhân dừng, không tin bất kỳ ai gọi nó nói gì.**
   `classify` (agent_node_executor.go) thêm nhánh `agentResult.Status == AgentExecutionCancelled` →
   `classifyCancellation` (file mới `agent_node_executor_cancellation.go`) đọc lại
   `WorkflowRun.State` một lần nữa (transaction read-only riêng, KHÔNG dùng ctx đã bị hủy) — nếu Run
   không hề có `RunCancellationIntent` (`Cancelling`/`Cancelled`), đây là nguyên nhân mơ hồ (ví dụ
   `workerpool` tự shutdown) → `ErrIndeterminateExecution`, không bao giờ đoán CANCELLED cho một cause
   không xác nhận được.
4. **Read-only attempt vs mutating attempt tách hai nhánh khác nhau** — khớp yêu cầu gốc của task ("mutating
   attempt không chứng minh được kết quả thành INDETERMINATE với workspace QUARANTINED"):
   - Không có write mount nào (`resolved.hasWriteMount == false`): không side effect nào từng có thể xảy
     ra → trả thẳng `NodeExecutionResult{State: Cancelled, TerminationReason: RunCancelled}` (err=nil) —
     đi qua `FinalizeExecutionAttempt` bình thường, tới `decideCancelledOutcomeTx` (V4-12B, đã có sẵn từ
     trước, đã tự làm đúng NodeRun RUNNING→CANCELLED + branch-token + run-terminality reconciliation).
   - Có write mount: KHÔNG được tin process đã dừng sạch chỉ vì `TreeQuiesced` — phải tái xác nhận từng
     repository workspace bằng CHÍNH các primitive V4-13 đã có sẵn và đã được `RecoveryReaperHandler`
     dùng y hệt (`worker.ReconcileMutatingAttempt`, `worker.InterruptionRecoveryStore.TerminateInterruptedAttempt`,
     `worker.WorkspaceReconciler.QuarantineRepositoryWorkspace`) — KHÔNG dùng
     `worker.ReconcileInterruptedAttempt` (wrapper tiện lợi giả định đúng 1 repo workspace; bridge này có
     thể có nhiều write mount cùng lúc) mà gọi trực tiếp các primitive cấp thấp hơn, y hệt cách
     `RecoveryReaperHandler` đã làm — không phát minh đường thứ hai đi tới cùng một kết quả durable.
     Attempt → INDETERMINATE (`OWNERSHIP_LOST_MUTATING`) LUÔN LUÔN trước (không có nhánh "sạch thì
     CANCELLED" — mutating attempt bị cắt ngang không bao giờ là CANCELLED sạch, kể cả khi revision
     cuối cùng trùng khớp — chỉ là INDETERMINATE-không-quarantine so với
     INDETERMINATE-có-quarantine); WriteLease chỉ release SAU KHI toàn bộ reconciliation (terminate +
     quarantine nếu có) xong — release trước sẽ mở cửa sổ cho attempt khác giành write lease vào một
     workspace chưa xác định xong tính toàn vẹn. Hàm này trả `ErrAttemptAlreadyTerminated` (sentinel mới)
     để báo `execute.go` rằng Attempt đã bị đưa tới trạng thái terminal RỒI, không cần
     `FinalizeExecutionAttempt` nữa (CAS đó chắc chắn fail vì `ExpectedVersion` không còn khớp RUNNING).
5. **`execute.go`'s own switch thiếu hẳn case `Cancelled`** — bug phát hiện khi đọc lại code trước khi
   viết poller: `nextState` mặc định cứng ở `Failed`, và nhánh `default` chỉ copy `TerminationReason`
   sang mà KHÔNG BAO GIỜ ghi đè `nextState` — nếu không sửa, nhánh read-only-attempt ở quyết định #4 phía
   trên (execErr == nil, State == Cancelled) sẽ lặng lẽ bị finalize thành FAILED. Thêm hẳn
   `case runtimedomain.ExecutionAttemptCancelled` copy đúng `TerminationReason` từ `execResult`.
6. **Check `attemptCtx.Err()` cũ (2 chỗ) đổi thành `execCtxErr`** (snapshot CHỤP TRƯỚC khi gọi
   `cancelExec()` dọn goroutine poller — nếu chụp sau, `execCtx.Err()` LUÔN non-nil vì chính
   `cancelExec()` vừa gọi, làm sai lệch mọi executor trả lỗi bare không liên quan gì tới cancel; bug này
   tự phát hiện qua test `TestExecuteNodeHandler_ExecutorReturnsError_FinalizesFailed` đỏ ngay lần chạy
   đầu, sửa bằng cách chụp `execCtxErr := execCtx.Err()` NGAY sau khi `Execute` return, trước
   `cancelExec()`). Dùng `execCtx` (con) thay vì `attemptCtx` (cha) đúng ý nghĩa: một executor CHUNG
   CHUNG (không biết gì về phân loại V5-08C, ví dụ mọi fake test cũ) mà bị poller hủy execCtx thì PHẢI
   rơi vào đúng nhánh an toàn "để RUNNING" y hệt outer-cancel trước đây — chỉ `AgentNodeExecutor` mới
   biết tự giải quyết dứt điểm (qua `ErrAttemptAlreadyTerminated` hoặc `State: Cancelled` thật) và thoát
   khỏi nhánh này TRƯỚC khi tới check `execCtxErr`.

**Thực hiện:**
- `agent_node_executor.go`: import `internal/app/worker`; struct thêm `interruptions
  worker.InterruptionRecoveryStore`, `reconciler worker.WorkspaceReconciler`; constructor thêm 2 tham số;
  `classify` thêm nhánh `AgentExecutionCancelled` gọi `classifyCancellation`.
- `agent_node_executor_resources.go`: `resolvedExecutionResources` thêm `writeMounts
  []mutatingMountInfo` (repositoryWorkspaceID/handle/pinnedRevision/workspaceVersion — đủ dữ liệu để
  reconcile mà không cần round-trip `AttemptHeldAnyWriteLease`/`LoadRepositoryWorkspaceRevision` như
  crash-recovery path phải làm); `gatheredMount` thêm `workspaceVersion`; Phase 1 gathering ghi lại
  `rw.Version`; Phase 2 loop append `writeMounts` khi `access == WorkspaceReadWrite`.
- `agent_node_executor_cancellation.go` (**file mới**): `ErrAttemptAlreadyTerminated`,
  `cancellationPollInterval = 500ms`, `classifyCancellation`, `handleMutatingCancellation`,
  `loadAttemptVersion` — đúng nội dung quyết định #3/#4 ở trên.
- `execute.go`: package doc + inline comment cập nhật; `execCtx, cancelExec :=
  context.WithCancel(attemptCtx)` bọc quanh lệnh gọi executor; goroutine `pollForCancellation` (hàm mới,
  đặt cạnh `runIsCancelling` sẵn có, TÁI DÙNG chính helper đó cho việc đọc — không viết lại logic đọc
  `WorkflowRun.State` lần hai) dùng `pollCtx` là ctx NGOÀI (`ctx`, không phải `execCtx`) vì bản thân vòng
  poll phải sống sót qua chính tín hiệu nó tạo ra; `cancelExec()` + `<-pollDone` ngay sau khi `Execute`
  return để không leak goroutine; `execCtxErr` chụp trước dọn dẹp (quyết định #6); nhận diện
  `ErrAttemptAlreadyTerminated` đầu tiên trong nhánh `execErr != nil` → `return nil`; switch thêm case
  `Cancelled` (quyết định #5).

**File thay đổi:** `internal/app/runtime/agent_node_executor.go`, `agent_node_executor_resources.go`,
`agent_node_executor_cancellation.go` (mới), `agent_node_executor_test.go`, `execute.go`, `execute_test.go`.

**Test:**
- 3 test mới ở tầng bridge (`agent_node_executor_test.go`), gọi thẳng `executor.Execute` (giống style 7
  test E2E của V5-08B — không qua `ExecuteNodeHandler`):
  - `TestAgentNodeExecutor_CancelledWithoutDurableIntent_ReturnsIndeterminateExecution` — không có
    `CancelRun` nào từng gọi → `ErrIndeterminateExecution`, không termination/quarantine nào.
  - `TestAgentNodeExecutor_MutatingCancellation_CleanRevision_TerminatesIndeterminateWithoutQuarantine` —
    gọi `runtime.CancelRun` thật, revision cuối trùng pinned → đúng 1 termination
    (INDETERMINATE/OWNERSHIP_LOST_MUTATING), 0 quarantine.
  - `TestAgentNodeExecutor_MutatingCancellation_MutatedRevision_TerminatesIndeterminateAndQuarantines` —
    revision cuối KHÁC pinned → đúng 1 termination, đúng 1 quarantine với
    `Reason == string(worker.ReconciliationMutationObserved)`.
- 2 test mới ở tầng handler (`execute_test.go`), lần đầu tiên chứng minh chính CƠ CHẾ POLLER (không
  test nào trước đây từng dựng `WorkflowRun.Cancelling` GIỮA LÚC executor đang block) — cả hai chạy
  `handler.Handle` trong goroutine, sleep 50ms rồi gọi `runtime.CancelRun` thật (không đụng ctx trực
  tiếp, khác hẳn `TestExecuteNodeHandler_CancelledContextDoesNotFinalize` cũ):
  - `TestExecuteNodeHandler_PollerDetectsDurableCancellation_UnrecognizedExecutorLeavesRunning` — dùng
    `fake.NodeExecutor{Block: ...}` (không biết gì về V5-08C) → Handle return trong ~1 tick (đo elapsed,
    assert < 3s, so với AttemptPolicy timeout 600s cố tình để rất dài) trả `context.Canceled`, Attempt
    vẫn RUNNING (nhánh an toàn quyết định #6).
  - `TestExecuteNodeHandler_PollerDetectsDurableCancellation_DefinitiveResultFinalizesCancelled` — dùng
    executor cục bộ mới `cancellationAwareExecutor` (mô phỏng đúng những gì `classifyCancellation`'s own
    read-only-attempt branch trả) → Handle return nil (finalize thành công), Attempt CANCELLED/
    RUN_CANCELLED, và phát hiện thêm: NodeRun tự động CANCELLED + Run tự đóng CANCELLED luôn (qua
    `decideCancelledOutcomeTx`/`reconcileRunTerminalityTx` — logic V4-12B có sẵn từ trước, task này chỉ
    cần mở đúng đường tới nó qua switch case mới).
  - Cả hai test stress `-count=5` liên tục pass, thời gian ổn định ~0.50-0.51s mỗi lần (đúng 1 tick
    `cancellationPollInterval`), không flake.

**Lỗi tự phát hiện và sửa trong lúc code (ghi lại đầy đủ, không giấu):**
- `bridgeFakeWriteLeaseManager.ReleaseWriteLeases` hard-code lỗi "must not be called" (chưa từng có test
  nào gọi thật trước V5-08C) — 2 test mutating-cancellation mới gọi lần đầu tiên → đổi fake từ value-type
  luôn lỗi sang pointer-type ghi lại `released [][]ports.WriteLeaseGrant`.
- `TestExecuteNodeHandler_ExecutorReturnsError_FinalizesFailed` đỏ ngay bản đầu tiên vì gọi
  `cancelExec()` TRƯỚC khi đọc `execCtx.Err()` — mọi lỗi bare không liên quan cancel đều bị hiểu nhầm
  thành cancel (đã sửa, xem quyết định #6).

**Chưa làm / cố ý để lại:**
- Không có test end-to-end kết hợp CẢ `ExecuteNodeHandler` (poller thật) LẪN `AgentNodeExecutor` (bridge
  thật) trong cùng một lời gọi — 2 nhóm test tách riêng (bridge-level dùng `bridgeFixture`/gọi thẳng
  `Execute`, handler-level dùng `cancellationAwareExecutor` mô phỏng lại đúng hợp đồng
  `classifyCancellation` trả về). Rủi ro khoảng trống nối 2 lớp gần như bằng 0 vì: (1) việc `execCtx` bị
  hủy thật sự truyền xuống context nhận được ở `AgentNodeExecutor.Execute` chỉ là ngữ nghĩa chuẩn của
  `context` package, không phải code tự viết; (2) `AgentExecutionCancelled` status bridge nhận vào đã có
  đường đi riêng được test kỹ; ghép 2 lớp thật cần dựng thêm `agentregistry`/`ports.AgentExecutor` giả
  lập process thật — hoãn, không chặn V5-08C, có thể làm ở V5-15 (execution/evidence acceptance gate,
  task tổng kết cuối V5) nếu cần.
- Không đổi `cmd/agentkit serve/worker` để nối `AgentNodeExecutor` thật vào `ExecuteNodeHandler` trong
  production — đúng pattern mọi task V4/V5 trước giờ (library + test, không đụng `cmd/`).
- `AttemptHeldAnyWriteLease`/`LoadRepositoryWorkspaceRevision` của 2 interface `worker.InterruptionRecoveryStore`/
  `WorkspaceReconciler` không bao giờ được bridge gọi (đã có sẵn đủ dữ liệu từ `resolved.writeMounts`,
  không cần round-trip crash-recovery path phải làm) — 2 fake test tương ứng cố tình lỗi "must not be
  called" để tự khẳng định điều này, không phải thiếu sót.

**Verify:**
```
go build ./...                                            # sạch
go vet ./...                                               # sạch
go run ./cmd/docs-coverage-check                           # debt = 0
gofmt -l <file thay đổi>                                   # chỉ báo CRLF (đã biết, benign — finalize.go
                                                            #   không hề đụng tới cũng bị báo y hệt)
go test -count=1 ./internal/app/runtime/... -v             # PASS toàn bộ, kể cả 5 test mới
go test -count=1 ./...                                     # PASS toàn bộ ~70 package
go test -count=1 ./internal/app/runtime/... (x3 lần)       # PASS ổn định, không flake
go test -count=5 ./internal/app/runtime/... -run PollerDetects -v
                                                            # PASS 5/5, ~0.50-0.51s mỗi lần, không flake
```

**Việc còn lại:** commit, push nhánh `feat/v5-08c-cancellation-execution-path`, mở PR, chờ CI 6/6, merge.

**Kết quả:** PR #6, merge commit `4afd51d`. CI lần đầu đỏ ở `contract (windows-latest)` —
`TestProjectWorkspaceGate` (`internal/integration`), đúng flake timing đã biết từ trước (ghi nhận trong
chính V5-08B's own checklist entry phía trên), không liên quan gì tới `internal/app/runtime` (package duy
nhất task này đụng tới) — xác nhận bằng 3 lần chạy lại cục bộ đều pass, sau đó `gh run rerun --failed`
trả về 6/6 xanh. Merge xong, xoá nhánh local + remote.

## V5-08D — RetryBlockedActivation handler (branch `feat/v5-08d-retry-blocked-activation`)

**Bối cảnh / nghiên cứu trước khi code:** `workdomain.BlockerType`'s own doc comment
(`internal/domain/work/blocker.go`) đã tự đặt tên chính xác nhiệm vụ này từ trước: bốn admission blocker
(`ISOLATION_ENFORCEMENT_UNAVAILABLE`, `ADAPTER_BUILD_DRIFT`, `CAPABILITY_REQUIREMENT_UNSATISFIED`,
`WRITE_CAPABILITY_OR_GRANT_MISSING`) "can never be waived... the only exits are fixing the underlying
condition (admission reasons — **a future RetryBlockedActivation**)". `admission.go` (V5-08, đã merge)
đã có sẵn producer thật cho cả bốn (`blockAdmission`, `execute.go`) — CAS đồng thời NodeRun và Attempt
QUEUED→BLOCKED, mở `WorkItemBlocker` với ID xác định `<attemptID>-admission-blocker` — nhưng KHÔNG có
đường thoát nào: `evaluateAdmission`'s own 4 check chỉ chạy MỘT LẦN, tại thời điểm admission ban đầu.

Phát hiện quan trọng nhất (đọc code, không đoán): `ResolveWorkItemBlocker` (V4-12C, command công khai duy
nhất khác có thẩm quyền đóng blocker) có precondition riêng "no non-terminal Run for this WorkItem"
(`ErrWorkItemHasNonTerminalRun`) — nhưng một NodeRun bị admission-block thì Run CHỦ của nó **không bao
giờ** rời RUNNING/WAITING (không transition nào trong toàn bộ codebase từng tạo ra
`runtime.WorkflowRunBlocked` — bốn admission reason chỉ CAS NodeRun/Attempt, chưa từng đụng tới Run).
Nghĩa là `ResolveWorkItemBlocker` KHÔNG BAO GIỜ dùng được cho một retry thật (Run vẫn đang sống) — nó chỉ
tồn tại để đóng sổ một blocker mà Run của nó đã terminal theo cách khác. V5-08D cần một command HOÀN TOÀN
KHÁC, dành riêng cho trường hợp Run vẫn còn sống.

Tìm được precedent gần như giống hệt: `reactivateBlockedNodeRunTx` (`scope_expansion.go`, V4-12A) — cơ chế
đóng SCOPE_EXPANSION_REQUIRED blocker, MỘT lần approve xong thì "mint a new NodeRunID, bump
ActivationSequence, copy NodeKey/Iteration/BranchTokenID, hand off to the EXISTING
ScheduleExecutableNodeRun pipeline (via a plain ScheduleNodeRunJobKind job) rather than re-deriving
EffectiveScope/ManifestRevision/ExecutionProfileHash a second way here" — đúng NGUYÊN VĂN hình dạng task
này cần, chỉ khác blocker group. Còn tìm thấy một test PLACEHOLDER đã được viết sẵn từ trước
(`TestRetryBlockedActivation_CreatesNewAttemptNeverRevivesOld`, `admission_test.go`, comment tự ghi "the
full RetryBlockedActivation command is V5-08D's scope") — tự tay dựng NodeRun mới + gọi thẳng
`ScheduleExecutableNodeRun` để CHỨNG MINH shape khả thi trước khi command thật tồn tại; task này thay hẳn
đoạn hand-rolled đó bằng lời gọi handler thật.

**Quyết định (không hỏi lại):**
1. **KHÔNG dùng `ResolveWorkItemBlocker`** — lý do đã nêu ở trên (precondition không bao giờ thoả cho một
   Run còn sống). `RetryBlockedActivationHandler.Retry` (file mới `retry_blocked_activation.go`) là command
   RIÊNG, có thẩm quyền đóng blocker admission theo cách `reactivateBlockedNodeRunTx` đã đóng blocker
   scope-expansion — `unlockedStatus = ACTIVE` (Run vẫn tiếp tục), không phải `READY`.
2. **Tái cấu trúc thay vì viết lại:** `runAdmissionProbePhase`/`loadExecutionProfile` (cả hai vốn là method
   của `*ExecuteNodeHandler`, `admission.go`/`execute.go`) đổi thành free function nhận tham số trực tiếp
   (`uow`, `isolation`, `agents`, `nodeRunID`) — method cũ trên `*ExecuteNodeHandler` giữ nguyên, chỉ còn là
   wrapper mỏng gọi hàm mới, KHÔNG đổi call site nào khác. Lý do: revalidate của retry PHẢI chạy đúng cùng
   một logic admission gốc đã chạy — chép lại một bản thứ hai là đúng thứ "one real resolver, every caller
   reuses it" mà `reactivateBlockedNodeRunTx`'s own doc comment đã cảnh báo tránh.
3. **"Không repin Run"** (yêu cầu khoá cứng của task, tự suy ra từ code chứ không hỏi): `loadExecutionProfile`
   chỉ đọc lại ĐÚNG DecisionArtifact bất biến `"<nodeRunId>-execution-profile-v1"` schedule.go đã ghi một
   lần duy nhất — nghĩa là retry LUÔN re-probe đúng AdapterBuildID gốc, không có cách nào tự thay bằng build
   mới hơn. Test riêng chứng minh: đăng ký thêm một build thứ hai hoàn toàn hợp lệ (không drift) sau khi đã
   bị block vì drift — retry vẫn thất bại với `ADAPTER_BUILD_DRIFT`, build mới không hề được xét tới.
4. **Idempotency gate: State của WorkItemBlocker, không phải State của NodeRun.** Phát hiện lúc viết test
   idempotent (gọi Retry() lần 2 sau khi lần 1 đã thành công): không có transition nào trong toàn bộ
   codebase từng đưa một NodeRun đã BLOCKED trở lại một State khác — bản gốc bị block ở lại BLOCKED MÃI MÃI
   như một historical record (giống hệt cách Attempt bị block cũng ở lại BLOCKED mãi mãi), chỉ một NodeRun
   MỚI với ActivationSequence cao hơn được tạo ra. Vậy check `nodeRun.State != BLOCKED` không bao giờ đúng
   nghĩa "đã retry rồi" — tín hiệu thật là blocker của chính Attempt đó không còn OPEN nữa (đã RESOLVED bởi
   lần retry trước). Sửa lại: `isAdmissionBlockerReason` check trước (loại các lý do khác như
   SCOPE_EXPANSION_REQUIRED — không có thẩm quyền), rồi mới tới `blocker.State != OPEN` → `AlreadyRetried`
   (no-op, không lỗi), rồi mới tới cancel-fence Run.
5. **"Thất bại giữ nguyên blocker hiện tại và không tạo thêm blocked activation":** nhánh
   `decision.reason != ""` trong transaction cuối cùng KHÔNG GHI GÌ CẢ — chỉ trả về
   `RetryBlockedActivationResult{FailureReason, FailureDetail}` (business outcome, không phải Go error,
   cùng vocabulary với `admissionDecision` gốc) rồi return nil (transaction rỗng, an toàn). Test lặp 5 lần
   xác nhận: đúng 1 blocker OPEN, số NodeRun không đổi so với trước khi bắt đầu retry.
6. **Cancel fence + "expected version":** thiết kế hai-pha giống hệt `admitOrClaimRunning` (`admission.go`,
   V5-08) — preflight đọc-only + Phase 1 I/O thật ngoài transaction, rồi Phase 2 một transaction
   `WithSerializedWrite` duy nhất đọc lại MỌI thứ tươi (đóng đúng race TOCTOU `admitOrClaimRunning` đã tự
   đóng giữa probe và commit) trước khi quyết định. Không có field `ExpectedVersion` nào lộ ra ngoài request
   — CAS được xử lý nội bộ hoàn toàn bằng cách đọc lại tươi trong transaction cuối, đúng cách
   `reactivateBlockedNodeRunTx`/`admitOrClaimRunning` đã làm, không phải một field caller tự cung cấp.
7. **NodeRun mới không sao chép EffectiveScope/RunManifestAmendment:** khác `reactivateBlockedNodeRunTx`
   (task đó THÊM scope mới), admission retry không hề đổi scope — `createRetriedNodeRunActivationTx`
   truyền `nil` cho EffectiveScope giống hệt cách `reactivateBlockedNodeRunTx` cũng làm, vì
   `ScheduleExecutableNodeRun` tự resolve lại từ snapshot hiện tại của WorkItem bất kể NewNodeRun được
   dựng với gì.

**Thực hiện:** `admission.go` (`runAdmissionProbePhase` → free function + wrapper method,
`isAdmissionBlockerReason` helper mới), `execute.go` (`loadExecutionProfile` → free function + wrapper
method — không đổi hành vi, chỉ đổi chữ ký nội bộ), `blocker.go` (cập nhật 2 doc comment để phản ánh người
gọi thứ ba của `closeWorkItemBlockerTx`/`openWorkItemBlockerTx`), `retry_blocked_activation.go` (file mới —
`RetryBlockedActivationHandler`/`Retry`, `loadForRetry`/`loadForRetryTx`, `createRetriedNodeRunActivationTx`,
3 sentinel error mới), `admission_test.go` (helper mới `claimableScheduleNodeRunJob`; nâng cấp
`TestRetryBlockedActivation_CreatesNewAttemptNeverRevivesOld` từ hand-rolled placeholder thành lời gọi
handler thật + verify blocker RESOLVED/WorkItem ACTIVE + idempotent lần gọi thứ hai; 3 test mới).

**Test (4 test mới + 1 nâng cấp, tất cả trong `admission_test.go`):**
- `TestRetryBlockedActivation_CreatesNewAttemptNeverRevivesOld` (nâng cấp) — golden path đầy đủ: block vì
  isolation → Retry thật → xử lý job SCHEDULE_NODE_RUN thật qua `NodeSchedulingHandler` → Attempt mới thật
  → Handle thành công → Attempt gốc vẫn BLOCKED nguyên vẹn → blocker RESOLVED, WorkItem ACTIVE → gọi Retry
  lần 2 trả về AlreadyRetried (không lỗi, không retry lần hai).
- `TestRetryBlockedActivation_RevalidationStillFails_NoRepeatedBlockedActivation` — lặp 5 lần Retry trên
  một nguyên nhân vẫn còn sai → đúng 1 blocker OPEN, số NodeRun không đổi so với trước khi bắt đầu.
- `TestRetryBlockedActivation_RunNotRetryable_Rejected` — `CancelRun` thật rồi Retry → `ErrRunNotRetryable`.
- `TestRetryBlockedActivation_AdapterDrift_NeverRepinsToNewerBuild` — đăng ký build thứ hai hợp lệ sau khi
  đã block vì drift → Retry vẫn thất bại với `ADAPTER_BUILD_DRIFT` (build gốc, không phải build mới).

**Chưa làm / cố ý để lại:**
- Không test riêng "NodeRun BLOCKED vì SCOPE_EXPANSION_REQUIRED bị RetryBlockedActivation từ chối" — logic
  (`isAdmissionBlockerReason`, so khớp với 4-phần tử `admissionPriority` có sẵn) đơn giản/rủi ro thấp, và
  dựng lại toàn bộ fixture scope-expansion (`TestFinalizeExecutionAttempt_Blocked_CreatesOriginAndRequestsScopeExpansion`'s
  own machinery) chỉ để test một nhánh không nằm trong "Verify" gốc của task — không chặn.
- Không đổi `cmd/agentkit` để nối `RetryBlockedActivationHandler` thật vào bất kỳ route/CLI nào — đúng
  pattern mọi task V4/V5 trước giờ (library + test, route thuộc V6, action UI thuộc V7-12, đúng "Phạm vi"
  chính task này tự ghi).

**Verify:**
```
go build ./...                                            # sạch
go vet ./...                                               # sạch
go run ./cmd/docs-coverage-check                           # debt = 0
gofmt -l <file thay đổi>                                   # chỉ báo CRLF trên file cũ (đã biết, benign);
                                                            #   file mới retry_blocked_activation.go sạch
go test -count=1 ./internal/app/runtime/... -v             # PASS toàn bộ, kể cả 4 test mới + 1 nâng cấp
go test -count=1 ./...                                     # PASS toàn bộ ~70 package
go test -count=1 ./internal/app/runtime/...                # lặp lại, ổn định không flake
go test -count=3 ./internal/app/runtime/... -run "TestRetryBlockedActivation|TestAdmission"
                                                            # PASS ổn định, không flake
```

**Việc còn lại:** commit, push nhánh `feat/v5-08d-retry-blocked-activation`, mở PR, chờ CI 6/6, merge.

**Kết quả:** PR #7, merge commit `6ff10ec`. CI xanh 6/6 ngay lần chạy đầu tiên (không cần rerun như
V5-08C). Merge xong, xoá nhánh local + remote.

## V5-09 — Command executor và COMMAND handler (branch `feat/v5-09-command-executor`)

**Bối cảnh / nghiên cứu trước khi code:** dùng một Explore agent để scope trước (không viết code) trong
lúc chờ CI của V5-08D — phát hiện quan trọng nhất: `internal/domain/command.CommandDocument` (V2-05, đã
xong từ lâu) đã có sẵn ĐẦY ĐỦ mọi field cần: `Argv []ArgvElement` (LITERAL/PLACEHOLDER, không bao giờ là
chuỗi shell), `PlaceholderAllowlist`, `CwdRepositoryTarget`, `EnvAllowlist`, `NetworkAccess`, `SecretRefs`
(chỉ tên, không giá trị), `TimeoutSeconds`, `Output{CaptureStdout, CaptureStderr, MaxOutputBytes}` —
nhưng KHÔNG có bất kỳ đường dispatch nào cho COMMAND-kind NodeRun trong production code: `schedule.go`'s
own `resolveExecutionProfile` chỉ pin identity (`Executor.Kind/DefinitionID/VersionID/CompiledHash`) cho
nhánh COMMAND, chưa từng decode `CommandDocument` (khác AGENT — decode `AgentProfileDocument` ngay tại
chỗ); `ExecuteNodeHandler` hoàn toàn generic, không switch theo `ExecutorKind` ở đâu cả. `ports.ProcessSupervisor`
(V5-05, `internal/adapters/process.Supervisor`) — chính primitive claude.go/codex.go đã dùng để spawn
provider — hoàn toàn tái dùng được để spawn COMMAND trực tiếp, KHÔNG cần qua `ports.AgentExecutor`.
**Secret-ref resolution KHÔNG tồn tại ở đâu cả** — chỉ có field khai tên (`SecretRefs []string`), chưa có
port/adapter nào resolve giá trị thật — đây là gap thật, phải xây từ đầu.

Đọc sâu thêm `attachFinalizationEvidenceTx` (finalize.go) phát hiện: hàm này BẮT BUỘC một row `agent_events`
thật khớp `TerminalEventSequence` cho MỌI attempt có Evidence — nhưng `ports.AgentEventKind`'s own
`EXECUTION_STARTED`/`EXECUTION_FINISHED` hoàn toàn generic (không mang ý nghĩa chat-specific gì), nên một
COMMAND execution hoàn toàn có thể emit đúng 2 event này qua CHÍNH `agentevents.Sink` AGENT đã dùng — đây
là insight quan trọng nhất: COMMAND KHÔNG cần một evidence pipeline riêng, chỉ cần "đóng vai" một
AgentExecutor cực đơn giản (2 event, không streaming JSONL) để tái dùng TOÀN BỘ evidence/checkpoint/
lease-fencing machinery AGENT đã có. Cũng xác nhận `admission.go`'s own 4 check (isolation/adapter-build/
capability/multi-repo-write) đã hoàn toàn kind-agnostic từ trước (COMMAND tự động qua được, không cần
sửa), `scopeguard.ValidateDiffs` cũng generic 100%, và `resolveSelectedOutcome(proposed, allowedOutcomes)`
(agent_node_executor.go) đã là free function sẵn — gọi với `proposed=nil` cho COMMAND (không có marker
protocol) tái dùng y hệt logic single-outcome-derivation AGENT đã có.

**Quyết định (không hỏi lại, tất cả rút ra trực tiếp từ code hiện có, không đoán):**
1. **Không mở rộng `ResolvedExecutionProfileV1`** với field riêng cho COMMAND (argv/cwd/env/secret) —
   `admission.go`'s own `checkCapabilityRequirement` đã tự nêu rõ nguyên tắc: "never denormalizing the
   requirement onto ResolvedExecutionProfileV1 itself... a future ResolvedExecutionProfileV2 could
   denormalize this for performance, not V5-08". `decodeCompiledCommand` (mirror `decodeCompiledAgentProfile`,
   `schedule.go`) được gọi LẠI TỪ ĐẦU tại execution time (`gatherCommandExecutionInputs`), load fresh qua
   `Executor.VersionID`/`DefinitionID` — không bao giờ tin một bản sao cũ.
2. **Tái cấu trúc thêm một lần nữa (lần thứ 3 trong V5, tiếp nối V5-08D):** `resolveExecutionResources`,
   `buildEvidence`, `terminalEventSequence` (agent_node_executor_resources.go), `classifyCancellation`,
   `handleMutatingCancellation`, `loadAttemptVersion` (agent_node_executor_cancellation.go) đều đổi từ
   method trên `*AgentNodeExecutor` thành free function nhận tham số trực tiếp — method cũ giữ nguyên,
   chỉ còn là wrapper mỏng, không đổi hành vi, không đổi call site nào khác. Đây CHÍNH XÁC là yêu cầu khoá
   cứng của task: "COMMAND node dùng lại chính đường này" (V5-08C) và "cancel giữa một mutating command
   dùng đúng đường V5-08C, không có đường terminate riêng" (V5-09 tự ghi) — không có cách nào tái dùng
   thật nếu không tách các hàm này ra khỏi `*AgentNodeExecutor` trước.
3. **Secret resolution: `ports.SecretResolver` mới, adapter thật đọc từ chính OS environment variable của
   worker host** (`internal/adapters/secretenv.Resolver`) — cùng tinh thần "trust the local machine" mà
   ADR-016 (local HTTP trust boundary) đã xác lập ở nơi khác trong chính codebase này; không có secret
   store thật nào tồn tại (Vault hay tương đương) để tích hợp ở scope Alpha này. Interface hẹp
   (`Resolve(ctx, name) (string, error)`) nên sau này thay bằng implementation thật không cần đổi gì ở
   phía gọi.
4. **Secret luôn resolve vào `ProcessSpec.Environment` (map), KHÔNG BAO GIỜ vào Argv** — lý do bảo mật cụ
   thể (không phải "không có lựa chọn tốt hơn" mà là lựa chọn ĐÚNG duy nhất): argv của một process hiển
   thị được cho process/user khác trên cùng host qua `ps`/process listing; env thông qua
   `ProcessSpec.Environment` (giá trị tường minh) tách biệt hoàn toàn khỏi `InheritedEnvironment` (chỉ
   tên, kế thừa từ EnvAllowlist) — đúng khớp model 2-field `ports.ProcessSpec` đã có sẵn, không cần thêm
   gì.
5. **Placeholder vocabulary: tên PLACEHOLDER phải trùng với một RepositoryID thật trong EffectiveScope của
   chính NodeRun đó, resolve thành `WorkingDirectory` của mount tương ứng.** `PlaceholderAllowlist` chỉ
   được validate là một closed set THUẦN TÊN ở publish-time (`command.ValidateDocument`) — không hề kiểm
   tra tên đó có khớp EffectiveScope thật hay không (EffectiveScope thay đổi theo từng WorkItem/Run, publish-time
   không biết được) — nên `resolveCommandInvocation` (runtime, execution-time) fail closed nếu một tên
   PLACEHOLDER không khớp mount nào thật, và fail closed y hệt nếu `CwdRepositoryTarget` không khớp — cả
   hai đều: KHÔNG BAO GIỜ spawn process (`supervisor.Calls == 0`, test xác nhận trực tiếp), đúng
   "Hoàn thành khi: resource script không chạy nếu thiếu exact CommandVersion/policy grant" của chính task.
6. **Timeout: bound chặt hơn thắng.** `ProcessSpec.Timeout = min(AttemptPolicy.TimeoutSeconds,
   CommandDocument.TimeoutSeconds)` — ctx deadline ngoài (execute.go, từ AttemptPolicy) vẫn luôn cắt đúng
   hạn nếu CommandDocument khai dài hơn, nhưng một CommandDocument tự khai timeout NGẮN hơn là một ràng
   buộc thật riêng của chính command đó, không được để AttemptPolicy nuốt mất.
7. **Output artifact: chèn thẳng ATTACHED, không qua đường ORPHAN→ATTACHED của diff-manifest.**
   `attachFinalizationEvidenceTx` không hề policing `Evidence.OutputArtifactRefs` (chỉ gộp vào danh sách
   artifact reference của Checkpoint) — không có bước promote nào sẽ từng chạm tới nó, nên chèn ORPHAN sẽ
   kẹt vĩnh viễn. Chỉ tạo artifact khi `Output.CaptureStdout || CaptureStderr` đúng như author khai — không
   bao giờ persist cả 2 stream nếu author chỉ cho phép 1.
8. **NetworkAccess: chỉ khai báo/audit cho Alpha, không có sandbox OS thật.** Khớp đúng tinh thần honesty
   sẵn có của `internal/adapters/process.IsolationChecker` ("Alpha has no real OS-level filesystem/network
   sandbox") — không giả vờ enforce cái không làm được thật. Ghi rõ đây là gap cố ý, không giấu.
9. **OS/toolchain compatibility của CommandDocument KHÔNG được check ở runtime task này** — không nằm
   trong "Thực hiện" gốc của task, để lại cho author tự đảm bảo qua file extension/nội dung script (phạm
   vi rõ ràng, không mở rộng).

**Thực hiện:** `internal/app/ports/secret.go` (mới — `SecretResolver` interface), `internal/adapters/secretenv/resolver.go`
(mới — adapter thật đọc OS env), `internal/app/ports/fake/secret.go` (mới — fake test double),
`internal/app/ports/fake/process_supervisor.go` (mới — fake `ports.ProcessSupervisor`, ghi lại mọi
`ProcessSpec` nhận được + đọc thật nội dung file tại `spec.Executable` để xác nhận bước materialize chạy
thật), `internal/app/runtime/schedule.go` (`decodeCompiledCommand` mới), `internal/app/runtime/agent_node_executor.go`
+ `agent_node_executor_resources.go` + `agent_node_executor_cancellation.go` (tái cấu trúc free-function,
quyết định #2), `internal/app/runtime/command_node_executor.go` (file mới — `CommandNodeExecutor`,
`gatherCommandExecutionInputs`, `resolveCommandInvocation`, `materializeExecutable`,
`persistCommandOutputArtifact`, `classify`).

**Test (10 test mới, `command_node_executor_test.go`):**
- `TestCommandNodeExecutor_Success_ResolvesArgvCwdAndFinalizesEndToEnd` — golden path đầy đủ: argv LITERAL
  + PLACEHOLDER (bao gồm một giá trị chứa `&&`/`|` để chứng minh không hề bị shell diễn giải — "injection"
  verify point) + cwd đúng mount → SUCCEEDED/outcome "done", Evidence có đúng 1 output artifact ref.
- `TestCommandNodeExecutor_EnvAllowlist_PassedAsInheritedEnvironment` — EnvAllowlist đúng thành
  `InheritedEnvironment` (so sánh theo set, vì `command.Compile` tự sort field này — order không mang
  nghĩa).
- `TestCommandNodeExecutor_SecretRef_ResolvedIntoEnvironmentNeverArgv` — secret resolve đúng vào
  `Environment`, KHÔNG xuất hiện ở bất kỳ argv element nào.
- `TestCommandNodeExecutor_SecretUnresolvable_FailsClosedWithoutSpawning` — secret không resolve được →
  FAILED/VALIDATION_FAILED, `supervisor.Calls == 0`.
- `TestCommandNodeExecutor_NonzeroExit_FinalizesFailed` — exit 7 → FAILED/EXECUTION_FAILED.
- `TestCommandNodeExecutor_ProcessOwnTimeout_FinalizesFailed` — `TimedOut=true` → FAILED/CodeTimeout.
- `TestCommandNodeExecutor_CommandOwnTimeoutTighterThanAttemptPolicy_Honored` — CommandDocument khai 5s,
  AttemptPolicy 600s → `ProcessSpec.Timeout == 5s` (quyết định #6, xác nhận trực tiếp qua giá trị thật đã
  truyền cho supervisor).
- `TestCommandNodeExecutor_UnknownArgvPlaceholder_FailsClosedWithoutSpawning` /
  `TestCommandNodeExecutor_CwdRepositoryTargetNotInScope_FailsClosedWithoutSpawning` — quyết định #5, cả
  hai xác nhận `supervisor.Calls == 0`.
- `TestCommandNodeExecutor_ProcessCancelled_ReusesV508CMutatingPath` — `CancelRun` thật rồi
  `Cancelled=true` → `ErrAttemptAlreadyTerminated`, đúng 1 termination INDETERMINATE, 0 quarantine (revision
  sạch) — chứng minh ĐÚNG dây nối tới free function V5-08C, không lặp lại toàn bộ ma trận đã test kỹ ở
  `agent_node_executor_test.go`.

**Lỗi tự phát hiện và sửa trong lúc code (ghi lại đầy đủ):**
- Test golden-path đầu tiên đỏ ngay: `bridgeFakeWorkspaceProvider` fixture của tôi quên set field `diff`
  (`ports.WorkspaceDiff{}` rỗng) → `buildEvidence`'s own `workspace.NewRevisionSet` từ chối vì
  RepositoryID/VCSObjectID rỗng — sửa bằng `defaultInScopeDiff()` (helper có sẵn từ agent_node_executor_test.go).
- Test `EnvAllowlist` đỏ vì assert theo ĐÚNG THỨ TỰ `["PATH","HOME"]` — `command.Compile`'s own
  `documentSetPaths` đánh dấu `envAllowlist` là set (order không mang nghĩa) nên bị sort lại thành
  `["HOME","PATH"]` — sửa assertion thành so sánh set, không so sánh order.
- `gofmt` báo lệch alignment thật (không phải CRLF benign) trên `command_node_executor.go` — 1 dòng struct
  literal field bị lệch cột do sửa tay — `gofmt -w` tự sửa, build/test lại xác nhận không đổi hành vi.

**Chưa làm / cố ý để lại (ghi rõ, không giấu):**
- Không có test end-to-end thật kết hợp `ExecuteNodeHandler` (poller/admission thật) + `CommandNodeExecutor`
  trong cùng một lời gọi — cùng lý do V5-08C đã ghi: rủi ro khoảng trống gần như bằng 0 (ctx-cancel-propagation
  là ngữ nghĩa chuẩn `context` package; admission đã kind-agnostic và test riêng đầy đủ).
- Không check `CommandDocument.Compatibility.OS` so với host thật lúc chạy (quyết định #9) — không nằm
  trong "Thực hiện" gốc.
- Không đổi `cmd/agentkit` để nối `CommandNodeExecutor` thật vào bất kỳ route/CLI nào — đúng pattern mọi
  task V4/V5 trước giờ.
- Không test riêng NetworkAccess=ALLOWED vs NONE thật (quyết định #8 — declared-only, không sandbox thật,
  nên không có hành vi runtime nào khác nhau để test).

**Verify:**
```
go build ./...                                            # sạch
go vet ./...                                               # sạch
go run ./cmd/docs-coverage-check                           # debt = 0
gofmt -l <file mới/thay đổi>                                # sạch hết (1 lỗi thật đã tự sửa, xem trên)
go test -count=1 ./internal/app/runtime/... -v             # PASS toàn bộ, kể cả 9 test mới
go test -count=1 ./...                                     # PASS toàn bộ ~70 package
go test -count=1 ./internal/app/runtime/...                # lặp lại, ổn định không flake
go test -count=3 ./internal/app/runtime/... -run "TestCommandNodeExecutor"
                                                            # PASS ổn định, không flake
```

**Việc còn lại:** commit, push nhánh `feat/v5-09-command-executor`, mở PR, chờ CI 6/6, merge.

**Kết quả sau merge:** PR #8 đã merge vào master tại `2269cd8` lúc 2026-09-10T06:21:52+07:00.
Không ghi claim CI ở đây vì lần rà soát này chỉ xác minh commit graph.

**Rà soát sau merge:** test mang tên `FinalizesEndToEnd` mới gọi `CommandNodeExecutor.Execute` và kiểm
proposal Evidence/ProcessSpec, chưa đi qua ExecuteNodeHandler/finalizer. Admission pipeline dùng chung,
nhưng capability/AdapterBuild có nhánh riêng cho AGENT; với COMMAND, multi-write mới là check authority
thực chất. Việc insert output thẳng ATTACHED là hành vi code đã merge, chưa phải lifecycle acceptance
đã đóng; phần review tổng hợp bên dưới ghi các gap còn lại.

## V5-10 — Gate runner và criteria-level Evidence (branch `feat/v5-10-gate-runner`, stacked trên `feat/v5-09-command-executor`)

**Bối cảnh:** người dùng chủ động bảo "triển khai luôn được không" ngay khi V5-09's own PR #8 còn đang chờ
CI — nhánh này được tạo TRÊN `feat/v5-09-command-executor` (chưa merge vào master) vì V5-10 phụ thuộc trực
tiếp vào code V5-09 vừa viết (`resolveExecutionResources`, `buildEvidence`, `resolveArgvAndSecrets`,
`materializeExecutable`, `classifyCancellation` — tất cả free function V5-09 đã tách ra). Sẽ rebase nhánh
này lên `master` ngay khi PR #8 merge xong.

Dùng một Explore agent scope trước (research-only) trong lúc chờ CI của V5-09, phát hiện quan trọng nhất:
`internal/domain/gate.GateDocument{CommandRef, Criteria []Criterion{Name, EvidenceKey}, PolicyRefs}` đã có
sẵn từ lâu (V2-05), `Verdict` enum đã đóng đúng 5 giá trị PASS/FAIL/ERROR/NOT_RUN/NOT_APPLICABLE — nhưng
package tự ghi rõ "never evaluates anything itself... the runtime's job" — nghĩa là TOÀN BỘ logic map
exit-code/output → verdict từng criterion KHÔNG tồn tại ở đâu cả, phải tự thiết kế (không có ADR nào chỉ
định cơ chế cụ thể — AK-ARCH-015's own toàn bộ nội dung chỉ là "Gate đa repository pin và hiển thị exact
RevisionSet", không nói gì về criterion-mapping). Cũng phát hiện: `ReleaseSet` KHÔNG tồn tại ở đâu cả
(chỉ có placeholder port `ports.IsReleaseAuthorized` tự ghi rõ "chưa implement thật"); V5-10A (task tạo ra
ReleaseSet thật) tự ghi phụ thuộc NGƯỢC vào V5-10, không phải chiều kia — nghĩa là V5-10's own "Thực hiện"
line tự nhắc tới ReleaseSet là văn bản hướng-tới-tương-lai, chưa phải yêu cầu thật lúc implement.

**Xác nhận với user (2026-09-10, verbatim, trước khi code):** "Đúng, cứ triển khai như vậy. V5-10 dùng
exact RevisionSet làm input bắt buộc. Không tạo ReleaseSet giả hoặc dependency ngược. Khi V5-10A cung cấp
ReleaseSet, bổ sung nó vào provenance/input của gate mà giữ nguyên GateResult, Evidence và verdict
semantics." — khoá cứng: dùng RevisionSet thật (đã có sẵn, heavily used qua AGENT/COMMAND) làm input duy
nhất; `GateResult`/`GateCriterionResult` (type export mới, tên user tự đặt) được thiết kế để một field
provenance ReleaseSet sau này CHỈ CẦN thêm vào, không bao giờ đổi nghĩa OverallVerdict/Criteria hiện có.

**Quyết định (không hỏi lại, trừ quyết định đã xác nhận ở trên):**
1. **Tái cấu trúc lần thứ 3 (tiếp nối V5-08D, V5-09):** tách `resolveCommandInvocation`
   (command_node_executor.go) thành `mountsByRepositoryID` + `resolveArgvAndSecrets` (argv/secret, không
   còn tự resolve cwd) — Command's own cwd vẫn resolve qua CwdRepositoryTarget như cũ (method wrapper giữ
   hành vi), nhưng Gate giờ gọi thẳng `resolveArgvAndSecrets` với cwd HOÀN TOÀN riêng (xem quyết định #3).
2. **Giao thức criterion-verdict tự định nghĩa (vì domain schema cố tình không định nghĩa):** underlying
   Command's own stdout PHẢI là một JSON object duy nhất, key = từng Criterion's own EvidenceKey, value =
   `{"verdict": "...", "detail": "...", "reason": "..."}`. Một exit code khác 0, timeout, hoặc bare spawn
   error khiến TẤT CẢ criteria → ERROR ngay (không đọc stdout nữa) — đúng "Hoàn thành khi: error/missing
   evidence không thể PASS": một evaluator tự thân không chạy sạch thì không output nào của nó còn đáng
   tin. Một EvidenceKey không được evaluator nhắc tới → NOT_RUN (khác ERROR — "chưa từng chạy" khác
   "chạy nhưng dữ liệu hỏng"). NOT_APPLICABLE thiếu `reason` → ép về ERROR (đúng convention toàn repo
   "NOT_APPLICABLE cần policy và reason"). Một verdict string lạ (không phải 1 trong 5 giá trị) → ERROR
   (tamper/malformed). `OverallVerdict` = verdict có độ nghiêm trọng cao nhất trong toàn bộ criteria
   (ERROR > FAIL > NOT_RUN > PASS/NOT_APPLICABLE) — chỉ SUCCEEDED khi OverallVerdict == PASS.
3. **Gate luôn read-only — nhưng qua DOWNGRADE mount, không REJECT khi thấy WRITE grant.** Lúc đầu định
   viết "fail closed nếu EffectiveScope có bất kỳ repo WRITE nào" — SAI, tự phát hiện trước khi viết test:
   EffectiveScope là khái niệm CẢ WorkItem (không phải theo từng node/NodeRun) — một workflow thật hoàn
   toàn có thể có node AGENT/COMMAND ghi VÀ node MACHINE_GATE verify cùng chia sẻ một EffectiveScope —
   reject thẳng sẽ khiến GẦN NHƯ MỌI Gate thật không chạy được. Sửa: `forceReadOnlyMounts`
   (gatherGateExecutionInputs's own final step) ép MỌI mount về `ports.WorkspaceReadOnly` trước khi
   `resolveExecutionResources` từng thấy nó — Gate không bao giờ xin write lease, `hasWriteMount` luôn
   false, `classifyCancellation`'s own nhánh mutating (INDETERMINATE + reconciliation) không bao giờ chạm
   tới được cho Gate — chỉ nhánh read-only đơn giản (CANCELLED) mới có thể chạy.
4. **"Scratch output nằm ngoài source workspace":** cwd của evaluator LUÔN là một temp dir rỗng mới tạo
   (`scratchDirectory`, `os.MkdirTemp`), KHÔNG BAO GIỜ một repo mount thật — khác hẳn Command (cwd =
   CwdRepositoryTarget's own mount). Repo vẫn truy cập được qua PLACEHOLDER argv (đọc, không phải cwd).
5. **Revision freshness:** trước khi spawn, so `mount.VCSObjectID` (đã pin từ ContextSnapshot) với
   `workspaces.CaptureRevision` SỐNG THẬT (tái dùng đúng primitive V5-08C's own handleMutatingCancellation
   đã dùng) — lệch → FAILED/VALIDATION_FAILED, KHÔNG spawn (không có Evidence, không side effect nào từng
   xảy ra).
6. **Evidence: GateResult luôn được persist làm artifact ATTACHED trực tiếp** (mirror
   `persistCommandOutputArtifact`'s own pattern — finalize.go không hề policing OutputArtifactRefs) — kể
   cả khi verdict KHÔNG PASS (một GateResult FAIL/ERROR chính là provenance-bearing record "MACHINE_GATE
   tạo verdict authoritative với provenance" yêu cầu, không chỉ khi PASS).

**Thực hiện:** `internal/app/runtime/command_node_executor.go` (tách `resolveArgvAndSecrets`/
`mountsByRepositoryID`, không đổi hành vi Command), `internal/app/runtime/schedule.go`
(`decodeCompiledGate` mới), `internal/app/runtime/gate_node_executor.go` (file mới — `GateNodeExecutor`,
`GateResult`/`GateCriterionResult` (export mới), `gatherGateExecutionInputs`, `forceReadOnlyMounts`,
`staleMountRevision`, `scratchDirectory`, `deriveGateResult`, `persistGateResultArtifact`, `classify`).

**Test (10 test mới, `gate_node_executor_test.go`):**
- `TestGateNodeExecutor_AllCriteriaPass_FinalizesSucceeded` — golden path, xác nhận cwd KHÔNG phải mount
  thật (quyết định #4) + đúng 1 output artifact ref.
- `TestGateNodeExecutor_OneCriterionFails_FinalizesFailed`, `TestGateNodeExecutor_NonzeroExit_AllCriteriaError`,
  `TestGateNodeExecutor_MissingOutput_AllCriteriaError`, `TestGateNodeExecutor_MissingCriterionKey_ResolvesNotRun`,
  `TestGateNodeExecutor_NotApplicableWithoutReason_FinalizesFailed`,
  `TestGateNodeExecutor_NotApplicableWithReason_CountsAsPass`, `TestGateNodeExecutor_UnrecognizedVerdict_FinalizesFailed`
  — từng nhánh của quyết định #2.
- `TestGateNodeExecutor_StaleRevision_FailsClosedWithoutSpawning` — quyết định #5, `supervisor.Calls == 0`.
- `TestGateNodeExecutor_ProcessCancelled_ReusesV508CReadOnlyPath` — xác nhận CANCELLED/RUN_CANCELLED (nhánh
  đơn giản, không INDETERMINATE), `interruptions`/`reconciler` không hề bị chạm — chứng minh trực tiếp
  quyết định #3's own hệ quả cấu trúc.

**Lỗi tự phát hiện và sửa TRƯỚC KHI viết test (tránh vòng lặp sửa-test tốn công):** thiết kế ban đầu
"reject Gate nếu EffectiveScope có WRITE" — tự nhận ra sai ngay khi chuẩn bị dựng fixture test (mọi
`scheduleFixture` có sẵn đều seed repo-1 WRITE qua `readyFixture`'s own `CreateRootWorkItem`, nghĩa là
MỌI test sẽ tự reject chính nó) — sửa toàn bộ sang downgrade-to-read-only (quyết định #3 ở trên) trước khi
viết dòng test đầu tiên, không phải sau khi test đỏ.

**Chưa làm / cố ý để lại:**
- Chưa rebase nhánh này lên `master` — chờ PR #8 (V5-09) merge xong.
- Không tích hợp ReleaseSet — đúng quyết định đã xác nhận với user, để lại nguyên vẹn cho V5-10A.
- Không đổi `cmd/agentkit` để nối `GateNodeExecutor` thật vào route/CLI — đúng pattern mọi task V4/V5.
- Không check `command.Compatibility.OS`/`NetworkAccess` thật lúc runtime — kế thừa nguyên trạng gap đã
  ghi ở V5-09's own "Chưa làm" (áp dụng y hệt cho Command mà Gate pin).

**Verify:**
```
go build ./...                                            # sạch
go vet ./...                                               # sạch
go run ./cmd/docs-coverage-check                           # debt = 0
gofmt -l <file mới/thay đổi>                                # 1 lỗi thật (struct alignment) đã tự sửa
go test -count=1 ./internal/app/runtime/... -v             # PASS toàn bộ, kể cả 10 test Gate mới
go test -count=1 ./...                                     # PASS toàn bộ ~70 package
go test -count=3 ./internal/app/runtime/... -run "TestGateNodeExecutor|TestCommandNodeExecutor"
                                                            # PASS ổn định, không flake
```

**Việc còn lại:** chờ PR #8 merge, rebase nhánh này lên `master`, verify lại, push, mở PR, chờ CI 6/6, merge.

**Kết quả sau merge:** PR #8 đã merge trước; V5-10 sau đó vào master qua PR #9 tại `39fb39c` lúc
2026-09-10T06:50:36+07:00. Core Gate runner đã merge; criteria-level Evidence authority vẫn còn thiếu
như phần rà soát bên dưới.

## Rà soát V5-09…V5-15 trên committed master (2026-09-10)

**Snapshot được đánh giá:** `master`/`origin/master` tại `39fb39c`
(`feat(v5-10): GateNodeExecutor evaluates a Gate's own pinned Command (#9)`). PR #2, #3, #6 và #7 đã
merge trước phần việc này; PR #8 (V5-09, merge commit `2269cd8`) và PR #9 (V5-10, `39fb39c`) hiện cũng
đã có trên master. Không có sự cố usage limit trong phiên này hoặc CI cũ cần xử lý. Mọi WIP ngoài
committed master không được tính là implementation evidence và không bị review này ghi đè.

Hai nguồn được đối chiếu là `docs/design/07-v5-execution-evidence.md` và checklist này, sau đó kiểm lại
type, port, migration, handler và test ở đúng snapshot. Kết luận tổng thể: V5-09 và V5-10 đã merge phần
executor lõi, nhưng chưa đạt toàn bộ acceptance được mô tả; các gap giờ là sai khác cụ thể trong code,
không còn là giả định trước triển khai. V5-10A…V5-14 chưa đủ contract/implementation để đóng task.
V5-15 đủ dữ kiện để dựng scenario manifest/oracle, nhưng chưa thể PASS toàn chain.

| Task | Có trên committed master | Kết luận dữ kiện/implementation | Dependency thực tế |
|---|---|---|---|
| V5-09 | PR #8: CommandNodeExecutor, SecretResolver, materializer, argv/cwd binding, process mapping, shared fence/cancel | **CORE ĐÃ MERGE; ACCEPTANCE PARTIAL**: còn truncation, redaction, artifact lifecycle, policy/compatibility/network và production wiring | Các gap rõ bằng code; phải đóng trước full acceptance |
| V5-10 | PR #9: GateNodeExecutor, criterion parser, verdict aggregation, scratch cwd, freshness và GateResult artifact | **CORE ĐÃ MERGE; CRITERIA EVIDENCE PARTIAL**: chưa có Evidence authority/repository, full lineage, N/A policy và hard read-only | RevisionSet là input hiện tại; ReleaseSet bổ sung ở V5-10A |
| V5-10A | RevisionSet/WorkspaceSet, local workspace Git, release-request intent | **CHƯA ĐỦ**: thiếu ReleaseSet aggregate và local-commit authority | V5-10 core đã có; seal còn chờ criteria Evidence |
| V5-11 | VERIFYING event, DecisionArtifact, approval, blocker, UoW/CAS | **CHƯA ĐỦ**: thiếu Evidence reader, policy pin/schema, rework route và input→outcome matrix | Chờ V5-10 Evidence + V5-10A |
| V5-12 | Attempt-bound V5 snapshot, request assembler, mount access | **CHƯA ĐỦ**: thiếu CHECKER role/input manifest/enforcement contract | V5-10 core đã có; Evidence contract còn hở |
| V5-13 | Recovery reaper/decision/retry/reconcile/checkpoint/fresh-start primitive | **CHƯA ĐỦ**: legacy/V5 snapshot conflict và chưa có exactly-once FRESH_START consumer | Chờ V5-10A/V5-11/V5-12 cùng executor acceptance gaps |
| V5-14 | Retention metadata, release intent/job, workspace release primitive | **CHƯA ĐỦ**: thiếu purge protocol/reference liveness/release result state | Chờ V5-13 |
| V5-15 | Integration harness, crash fixtures, cross-platform/race CI pieces; Command/Gate core types | **ĐỦ để viết plan; CHƯA THỂ PASS** | Chờ gap V5-09/V5-10 và V5-10A…V5-14 |

### V5-09 — Command executor và COMMAND handler

**Đã merge trên master:** `CommandNodeExecutor` reload exact CommandVersion/resource hash; resolve argv
placeholder và cwd theo RepositoryID trong EffectiveScope; resolve secret từ OS environment vào explicit
process environment; materialize executable rồi spawn trực tiếp không qua shell; dùng timeout chặt hơn,
bounded output, RevisionSet/diff evidence, event sink, fencing và cancellation V5-08C. Test hiện có phủ
success, injection-shaped argv, env/secret, nonzero, timeout, invalid placeholder/cwd/secret và cancel.

**Khoảng trống acceptance còn lại:**

1. `ProcessResult.OutputTruncated` bị bỏ qua, nên output thiếu vẫn có thể success.
2. Secret vừa resolve không được thêm vào redaction matcher; stdout/stderr có thể chứa secret và được
   persist nguyên với `Redacted=false`.
3. Output artifact được insert thẳng `ATTACHED`/`CANONICAL_CONTEXT` trong transaction riêng trước
   finalize, không dùng lifecycle `RAW_OUTPUT_TEMP`/ORPHAN→attach hoặc idempotency key. Finalizer chỉ
   chép `OutputArtifactRefs` vào checkpoint, không load/verify/promote ID; finalize failure/replay có thể
   chấp nhận missing/foreign ref hoặc để lại row không owner/duplicate.
4. `CommandDocument.PolicyRefs` không được đọc; compatibility OS/toolchain và `NetworkAccess` chỉ là
   declaration/audit, chưa verify/enforce.
5. Materializer truyền temp path thẳng cho process; test dùng fake supervisor, nên interpreter/.ps1 và
   compatibility đa nền tảng chưa được chứng minh. Cũng chưa có composition/router production chọn
   executor theo kind hoặc end-to-end ExecuteNodeHandler→CommandNodeExecutor→Finalize.

**Kết quả kỳ vọng còn lại:** truncation không thể success; echoed secret không xuất hiện trong DB/event/
artifact/log; output Put+Verify rồi attach idempotently dưới cùng fence với exact RevisionSet; mọi policy/
compatibility/network pin được reverify; production handler dispatch đúng executor. Chỉ khi đó mới đánh
dấu toàn bộ V5-09 DONE; PR #8 hiện chứng minh core executor, không chứng minh các acceptance gap này.

### V5-10 — Gate runner và criteria-level Evidence

**Đã merge trên master:** `GateNodeExecutor` reload GateVersion và CommandVersion/resource đã pin, ép
mount descriptor sang READ_ONLY, dùng scratch cwd, kiểm live revision trước spawn, parse stdout JSON
theo EvidenceKey và aggregate ERROR > FAIL > NOT_RUN > PASS/N/A. Spawn/nonzero/timeout/malformed fail
closed; thiếu key thành NOT_RUN; N/A thiếu reason thành ERROR. Mười unit test phủ verdict matrix, stale
revision và cancellation. GateResult artifact được persist cho mọi verdict.

**Khoảng trống acceptance còn lại:**

1. Chưa có domain `Evidence`, repository/Tx accessor hoặc SQLite writer/query; bảng `evidence` vẫn chưa
   được dùng. GateResult artifact không thay thế criteria-level Evidence.
2. GateResult thiếu schema version và full lineage WorkItem/Run/NodeRun/Attempt, Gate/Command/policy pin,
   exact RevisionSet và artifact hashes. PASS chỉ nối ID gián tiếp qua finalization evidence; non-PASS
   bỏ artifact ID, nên kết quả không truy ngược được từ Attempt.
3. Artifact được ghi transaction riêng với ID mới, chưa idempotent hoặc coupled với fenced finalize;
   finalizer không load/verify/promote `OutputArtifactRefs`, nên crash/replay có thể sinh duplicate/
   unlinked row hoặc chấp nhận missing/foreign ref.
4. N/A mới kiểm reason, chưa kiểm pinned policy authority; exact CommandRef DefinitionID chưa được
   đối chiếu sau load.
5. READ_ONLY mới là descriptor; evaluator vẫn nhận host path thật, hậu kiểm diff dùng EffectiveScope gốc
   có thể cho WRITE. `OutputTruncated` chưa fail closed, `TreeQuiesced=false` bị bỏ qua vì gate không có
   write mount, và Detail/Reason chưa redaction với secret vừa resolve.
6. Chưa có production composition/router hoặc ExecuteNodeHandler→GateNodeExecutor integration test.

**ReleaseSet:** quyết định phase hiện tại là đúng: V5-10 dùng exact RevisionSet. ReleaseSet thuộc
V5-10A và chỉ được thêm sau dưới dạng provenance/input additive, không đổi OverallVerdict/Criteria
semantics.

**Kết quả kỳ vọng còn lại:** mỗi criterion tạo đúng một Evidence row idempotent có full lineage, exact
pins, RevisionSet và artifact ID/hash cho cả PASS lẫn non-PASS; attach/finalize atomic dưới fencing;
N/A cần policy+reason; truncation, unquiesced process hoặc evaluator mutation không thể PASS; output được
redact. Sau V5-10A thêm ReleaseSet ID/hash. Chỉ khi đó mới đánh dấu toàn bộ V5-10 DONE.

### V5-10A — ReleaseSet và typed local Git operation

**Những gì đã có:** RevisionSet, WorkspaceSet/generation, local worktree/revision/diff/release primitives,
command receipt/event và public `RequestWorkspaceSetRelease`. V5-10 gate core đã merge, nhưng
criteria-level Evidence authority còn thiếu nên chưa thể seal. `ReleaseEligibilityAuthority` vẫn là
một port dùng fake; chưa có ReleaseSet aggregate/table/repository hoặc local commit port.

**Khoảng trống blocking:** uniqueness theo FamilyID+ManifestRevision, tập repository bắt buộc, partial
state, canonical hash/evidence refs, seal/abandon CAS/replay và typed local-commit request/result/fence.

**Kết quả kỳ vọng:** DRAFT sinh idempotently từ exact WorkspaceSet/base revision; entry sort canonical;
local commit chỉ chạy trên WorkspaceHandle dưới scope/version/revision/fence và lưu commit OID; remote
Git verb không tồn tại ở adapter surface; seal chỉ khi mọi required entry có exact result+fresh Evidence;
partial/stale vẫn DRAFT; SEALED/ABANDONED immutable và trở thành authority thật cho completion/cleanup.

### V5-11 — CompletionPolicy service

**Những gì thực sự đã có:**
- `internal/app/runtime/completion.go` giữ đúng boundary: END chỉ đưa WorkflowRun sang `VERIFYING`, append
  `RUN_COMPLETION_REQUESTED`, WorkItem vẫn `ACTIVE`.
- `DecisionArtifact` cùng repository đã có; approval repository, typed blocker
  `COMPLETION_POLICY_FAILED`, UoW/CAS và cancel-vs-PASS race fixture đã có. Race test hiện mô phỏng PASS
  bằng CAS, không chứng minh service V5-11.
- `CompletionRules` mới chỉ có `RequiredEvidenceKinds`. Không có CompletionDecision enum/service/event,
  production Evidence/ReleaseSet reader hay completion-policy pin trên WorkItem/Workflow/END.

**Khoảng trống blocking:**
1. Phải pin đúng một CompletionPolicyVersion vào execution authority trước khi Run start; completion
   command chỉ nhận identity/version guard, không nhận PASS từ caller.
2. Policy schema cần ordered assurance levels và disposition deterministic cho FAIL/missing/stale/ERROR/
   NOT_RUN/N/A, approval, clean/quarantine, join và SEALED ReleaseSet. `RequiredEvidenceKinds` đơn lẻ
   không đủ tạo bốn outcome.
3. REWORK hiện mâu thuẫn với graph: validator cấm END có outgoing edge, còn ADR-021 yêu cầu published
   rework edge. Cần typed completion-rework route trong compiled WorkflowVersion hoặc đổi invariant
   tường minh; runtime không được search một edge tùy ý.
4. “join” cần chốt là persisted workflow JOIN hay child-WorkItem join; nếu gồm child thì repository/query
   contract hiện chưa có.
5. `RecordDecisionArtifact` append-only chứ không tự idempotent. Completion phải dùng deterministic
   ID/receipt và commit DecisionArtifact + transition + event + blocker/activation trong cùng transaction.
   SQLite transition cũng phải ghi terminal timestamp khi Run `SUCCEEDED`.

**Kết quả kỳ vọng đã bổ sung vào design:** service tự load mọi exact input; đúng một
`COMPLETION_DECISION_V1` lưu input IDs/hashes/policy/route/reason; decision và state changes commit/rollback
cùng nhau. PASS tạo Run SUCCEEDED+WorkItem DONE atomically; REWORK tạo đúng một activation theo route và
budget, thiếu route→BLOCK; BLOCK khóa cả hai; FAIL tạo Run FAILED+WorkItem BLOCKED+đúng một blocker và
không auto-reactivate. Không outcome nào rời VERIFYING khi thiếu DecisionArtifact hoặc cancel đã thắng.

### V5-12 — Maker/checker isolation

**Những gì đã có:** V5 `contextsnapshot.Snapshot` immutable, bind Attempt và exact RevisionSet;
scheduler tạo Attempt/Snapshot, assembler reverify pins; mount có READ_ONLY/READ_WRITE; fresh recovery
gọi Start thay vì Resume.

**Khoảng trống blocking:** không có typed MAKER/CHECKER role; scheduler hiện đưa toàn bộ WorkItem message
vào mọi snapshot, nên chưa loại maker transcript. Snapshot chỉ có MessageRef/ResourceRef, thiếu typed
Evidence/Diff refs. Checker vẫn kế thừa WRITE theo EffectiveScope, chưa có scratch handle hay local-commit
fence. Với `OPERATOR_TRUSTED_LOCAL`, chỉ có hậu kiểm mutation; prevention thật đòi
`ENFORCED_ISOLATED`.

**Kết quả kỳ vọng:** CHECKER role được pin, luôn có Attempt/Snapshot/session riêng; snapshot allowlist chỉ
gồm requirement/acceptance, exact revisions/release, diff/evidence cần thiết và không có maker messages/
events/transcript/reasoning; tất cả source mount READ_ONLY, scratch ngoài source, không WriteLease/local
commit. Trusted-local mutation bị fail+quarantine và không thể tạo PASS.

### V5-13 — Checkpoint/handoff và recovery integration

**Những gì thực sự đã có:**
- Recovery reaper phát hiện orphan RUNNING Attempt, reconcile/quarantine mutating work, phân loại RETRY/
  FRESH_START/ESCALATE và ghi RecoveryDecision. RETRY đã tạo replacement Attempt+RUN_WORK job.
- Agent event/checkpoint batching, fenced finalize và `worker.StartFreshFromLatestCheckpoint` đã có.
  Primitive đó cố ý chỉ dùng provider `Start`, không `Resume`.
- FRESH_START hiện chỉ ghi decision; không có consumer/claim/exactly-once reservation. Production path
  cũng chưa dùng `RecoveryCheckpoint` trong AgentExecutionRequest.

**Mâu thuẫn blocking phải sửa trước code:** `worker.StartFreshFromLatestCheckpoint` đọc legacy
`runtime.ContextSnapshot`, còn scheduling/dispatch thật dùng V5 `contextsnapshot.Snapshot`. Comment của
V5 type nói rõ hai model không có bridge; V5 Snapshot bind duy nhất với Attempt nên replacement không
được reuse snapshot ID cũ. Vì vậy mô tả cũ “load checkpoint/snapshot rồi gọi primitive hiện có” là sai
với production path.

V5-13 phải sở hữu bridge/replacement: claim RecoveryDecision bằng persistent CAS key; tạo replacement
Attempt + **V5 Snapshot mới bind Attempt mới** + recovery RUN_WORK atomically; ngoài transaction load và
reverify source checkpoint/new snapshot, assemble request bình thường, set RecoveryCheckpoint và gọi
Agent `Start` hoặc Command/Gate executor; cuối cùng fenced finalize. Cần thêm handoff V1 chứa completed/
unverified work, blocker/decision/evidence refs, revision/generation, failure category, next action và
budget basis; đồng thời định nghĩa no-progress/time/cost/attempt escalation.

**Kết quả kỳ vọng:** một decision chỉ sinh một replacement; Start=1/Resume=0; không phụ thuộc old
session/transcript/cwd; stale/tampered/missing pin hoặc fence không commit; hết budget/no-progress tạo
typed blocker. Sáu boundary chuẩn phải chạy cho AGENT/COMMAND/MACHINE_GATE: trước intent commit, sau job
commit trước claim, sau claim trước process, giữa event/checkpoint, sau side effect trước finalize, sau
finalize trước scheduler. Không boundary nào làm mất transition, lặp success/side effect hay blind-retry
mutation indeterminate.

### V5-14 — Cleanup/retention sweeper

**Những gì đã có:** retention class, hold, expiry, ORPHAN/ATTACHED và CAS; orphan query; idempotent
workspace release primitive; release request đã tạo durable job với per-repository IDs.

**Khoảng trống blocking:** ArtifactStore không có purge/delete; metadata không có PURGE_RESERVED/PURGED
hoặc deleted_at; chưa có reference/liveness query, shared-locator accounting, attached-expired query,
sweep claim/report hoặc release result state. DB recheck và filesystem deletion không thể là một atomic
transaction, nên câu “atomic reference recheck” cần flow reserve Tx → purge ngoài Tx → finalize Tx.

**Kết quả kỳ vọng:** dry-run/actual đều có durable manifest; chỉ orphan quá grace hoặc expired raw output
không hold/ref/shared-live-locator được purge; canonical/held/unknown/active/quarantined được giữ; metadata
và hash còn lại cho audit. Release handler recheck ReleaseSet + attempts/jobs/leases/quarantine, persist
per-repository partial result, resume idempotently và chỉ set whole set RELEASED khi mọi repo thành công.

### V5-15 — Execution/evidence acceptance gate

**Những gì thực sự đã có:** integration runtime với SQLite/workerpool, clean/recovered golden, crash
fixtures và CI Windows/Linux/race theo từng lớp; committed master đã có core CommandNodeExecutor và
GateNodeExecutor. Nhưng `internal/integration/runtimeengine_test.go` vẫn dùng scripted fake NodeExecutor
và chủ ý assert Run dừng ở VERIFYING; không có composition thật, criteria Evidence authority, ReleaseSet
hay CompletionPolicy service. Đây chưa phải execution evidence toàn chain.

**Acceptance contract đã đủ để chuẩn bị ngay:** fixture offline phải khai rõ real/recorded fake/spy,
dùng real app handlers+SQLite+filesystem/Git/ProcessSupervisor; recorded provider chỉ thay external CLI,
không thay Command/Gate/Completion handler. Trace manifest phải nối và verify lại sau restart:

```text
WorkItem -> WorkflowRun -> NodeRun -> ExecutionAttempt -> V5 ContextSnapshot
         -> exact RevisionSet -> Evidence + Artifact IDs/hashes
         -> ReleaseSet -> CompletionDecision -> WorkItem status
```

Happy path phải chứng minh agent claim hoặc process exit 0 chỉ tạo completion candidate; chỉ fresh
independent PASS trên exact revision cùng SEALED ReleaseSet mới atomically tạo Run SUCCEEDED/WorkItem
DONE. Negative matrix phải chứa gate fail, provider loss, scope violation, checker write, adapter drift,
isolation unavailable, mutating cancel, artifact tamper và cả sáu crash boundary; mỗi case có typed
non-success/block/escalation/quarantine và false-completion oracle `unexpected DONE count = 0`.

**Kết quả kỳ vọng:** suite offline pass trên Windows/Linux, Linux race và repeated semantic comparison;
restart verify được full trace, không duplicate side effect. Claude/Codex live smoke chỉ là auxiliary;
chưa chạy thì ghi đúng `UNVERIFIED_LIVE`, không dùng nó thay acceptance offline.

### Thứ tự triển khai sau review

1. Đóng các gap acceptance đã chỉ rõ của V5-09: truncation/redaction, artifact lifecycle, policy/
   compatibility/network và production routing.
2. Hoàn tất criteria-level Evidence cho V5-10, gồm full lineage/idempotency/fencing cho PASS lẫn
   non-PASS; giữ RevisionSet là input hiện tại.
3. Implement ReleaseSet/local commit V5-10A rồi thêm ReleaseSet provenance vào gate/completion.
4. Khóa completion policy pin/rules/rework route rồi implement V5-11; V5-12 có thể chuẩn bị song song sau
   khi Evidence contract V5-10 ổn định.
5. Thống nhất legacy/V5 snapshot và exactly-once recovery trước V5-13; sau đó mới làm purge/release V5-14.
6. Dựng V5-15 scenario manifest/oracle sớm, nhưng chỉ ghi PASS sau khi mọi gap V5-09…V5-14 chạy qua adapter/
   handler thật.

**Giới hạn của lần rà soát này:** đây là document/code-contract review, không phải implementation run.
Unit test executor V5-09/V5-10 đã có; chưa có acceptance fixture toàn chain để báo PASS cho V5-15.
Các kiểm tra bên dưới chỉ xác nhận consistency tài liệu/repository. Mọi dòng “Kết quả kỳ vọng” còn lại
là tiêu chí phải được chứng minh ở PR tương ứng, không phải claim đã hoàn thành.

**Verify tài liệu đã chạy trên worktree `master` (2026-09-10):**

- `git diff --check` — **PASS**.
- `go run ./cmd/docs-coverage-check` — **PASS**, debt = 0.
- `go test ./internal/docscoverage` — **PASS**.
- `go test -count=1 ./internal/app/runtime -run 'Test(CommandNodeExecutor|GateNodeExecutor)'` — **PASS**.

**Kết quả:** PR #9 (`feat/v5-10-gate-runner` → `master`), 6/6 CI checks pass (Linux race/stability,
contract ubuntu/windows, cross-platform semantic diff SPK-13, spike acceptance ubuntu/windows), squash-merged
2026-09-09, merge commit `39fb39c`.

## V5-10A — ReleaseSet và typed local Git operation (branch `feat/v5-10a-release-set`, stacked trên
`feat/v5-10-gate-runner` rồi rebase lên `master` sau khi PR #9 merge)

**Bối cảnh:** người dùng bảo "trong lúc chờ CI thì có thể research, nếu không vướng gì có thể triển khai
song song luôn cũng được" — trong lúc PR #9 (V5-10) còn CI, đã research V5-10A trước (xác nhận: V3-11
`internal/app/workspacerelease.RequestWorkspaceSetRelease` đã build sẵn phía caller, phụ thuộc duy nhất
vào `ports.ReleaseEligibilityAuthority` — một interface KHÔNG có implementation thật, tự ghi rõ "a real
implementation, backed by a real sealed/abandoned ReleaseSet, is left entirely to that later task"; V5-10A
CHÍNH LÀ task đó). Đã bắt đầu implement song song (domain type, sqlite migration/adapter, fake adapter,
port interface) trong lúc PR #9 còn CI pending — đúng theo uỷ quyền song song ở trên. PR #9 sau đó pass
6/6, merge (`39fb39c`) — nhánh này được tạo lại từ đầu (branch mới từ working tree hiện tại), rebase sạch
lên `master`, rồi mới viết tiếp phần application-layer/local-commit/test còn thiếu.

**Quyết định (không hỏi lại):**
1. **`work.ReleaseSet` sống trong package domain `work` sẵn có** (không phải package mới) — đây là runtime
   aggregate theo family (như `WorkItemBlocker`), không phải DefinitionKind schema; tái dùng thẳng
   `gate.Verdict` cho verdict từng repo (xác nhận không có import cycle: `gate` không import `work`).
   Mirror đúng "sorted entries + sha256 content hash `sha256:` prefix" của `workspace.RevisionSet`.
   `ReleaseSetState` CREATED/SEALED/ABANDONED — CREATED là state duy nhất được phép transition ra khỏi.
   Thêm `gate.Verdict.IsValid()` (map `knownVerdicts` mới) vì chưa tồn tại và `NewReleaseSet` cần nó.
2. **Persistence: migration `0030_release_sets.sql`** (2 bảng: `release_sets` header + child table
   `release_set_repositories`, verdict CHECK 5 giá trị) + `internal/adapters/sqlite/release_set.go` mirror
   đúng pattern Tx-composable của `work_item_blocker.go`. Bump 2 test hardcode migration-count (28→29) ở
   `db_test.go`/`unitofwork_test.go` — bookkeeping thường lệ mỗi lần thêm migration.
3. **Application-layer commands sống trong `internal/app/work` (package đã có sẵn, KHÔNG phải package mới
   `internal/app/releaseset`)** — quyết định này đã tự khoá cứng ngay từ doc comment của
   `internal/domain/work/release_set.go` viết TRƯỚC (tự ghi "SealReleaseSet/AbandonReleaseSet (internal/app/work,
   this task's own application layer)"), nên file mới `internal/app/work/release_set.go` đặt đúng nơi đã tự
   cam kết thay vì tạo package song song với `workspacerelease`/`workspacereconcile`. `CreateReleaseSet`/
   `SealReleaseSet`/`AbandonReleaseSet` mirror đúng shape idempotent-command của
   `RequestWorkspaceSetRelease`/`CreateRootWorkItem` (receipt Actor/Scope/IdempotencyKey/RequestHash,
   `cmd.ExpectedVersion` làm fence cho Seal/Abandon). `CreateReleaseSet` cross-check
   `family.ProjectID == req.ProjectID` (giống mọi command khác) — phát hiện thêm: `release_set_repositories.
   repository_id` có FK thật tới `repositories(id)`, nên cả sqlite adapter (check tường minh trước insert,
   trả `ErrPersistenceNotFound` sạch thay vì FK-violation mù) lẫn fake adapter (`w.catalog.repositories[...]`,
   mirror đúng `AddRepositoryScope`/`AddEffectiveScope`) đều phải validate repository tồn tại — bug này bắt
   được ngay từ lần chạy test sqlite đầu tiên (lỗi "sqlite: unexpected error" mù, không phải thiết kế sai
   từ đầu).
4. **`ErrReleaseSetNotOpen`** (sentinel riêng, check tường minh state trước khi gọi CAS) thay vì để một
   "duplicate seal" thật (IdempotencyKey khác, không phải replay) rơi vào `ErrOptimisticConflict` chung
   chung — mirror đúng tiền lệ `ErrWorkspaceSetAlreadyReleased` của `RequestWorkspaceSetRelease`.
5. **`IsCleanupEligible(releaseSet) bool`** (`internal/app/work`) — "application policy" trong Phạm vi line:
   SEALED hoặc ABANDONED mới cleanup-eligible (GC-INV-26). Không phải domain invariant của `ReleaseSet` tự
   thân (đọc một ReleaseSet CREATED vẫn hợp lệ, chỉ cleanup phải chờ).
6. **`EligibilityAuthority`** (`internal/app/work`, implement `ports.ReleaseEligibilityAuthority` thật lần
   đầu tiên) — authorized khi ReleaseSet MỚI NHẤT của family (theo `ListReleaseSetsForFamily`'s own
   `(CreatedAt, ID)` order) đã SEALED/ABANDONED. Tự mở `uow.WithReadOnly` riêng (không nhận raw
   `ports.WorkRepository`) — đúng lý do `RequestWorkspaceSetRelease`'s own doc comment cho việc gọi
   authority NGOÀI mọi `WithSerializedWrite`. Không cần sửa `workspacerelease` — Go structural typing tự
   thoả interface.
7. **`ports.LocalCommitCreator`** (port mới, `internal/app/ports/localcommit.go`) — không mở rộng
   `WorkspaceProvider` sẵn có, mirror đúng lý do `WorkspaceDirectoryResolver` (`readiness.go`) đã tự ghi:
   port hẹp riêng cho capability mới, `*gitworktree.Provider` tự thoả bằng structural typing, không phải
   sửa 3 implementer khác (`fakeWorkspaceProvider`, `bridgeFakeWorkspaceProvider`) của `WorkspaceProvider`.
   `CreateLocalCommit` nhận `AuthorName`/`AuthorEmail` tường minh (không dựa vào `user.name`/`user.email`
   ambient trong worktree), set qua `-c user.name=...` một-lần trên chính lệnh `git commit` (không ghi vào
   config lâu dài của repo) — "typed/audited" nghĩa là caller tự khai ai commit, không phải adapter tự suy
   ra. "Từ chối mọi remote operation trước Git adapter" (AK-ARCH-015C) là bất biến CẤU TRÚC, không phải
   runtime check: `CreateLocalCommit` là method Git-mutating DUY NHẤT toàn bộ ports — không tồn tại method
   remote nào để gọi tới dù cố tình.

**Thực hiện:**
- `internal/domain/work/release_set.go` (mới), `internal/domain/gate/gate.go` (thêm `IsValid`).
- `internal/adapters/sqlite/migrations/0030_release_sets.sql` (mới), `internal/adapters/sqlite/release_set.go`
  (mới, có repository-existence check tường minh).
- `internal/app/ports/work.go` (4 method mới + `TransitionReleaseSetStateRequest` trên `WorkRepository`),
  `internal/app/ports/fake/work.go` (implement fake, có cùng repository-existence check).
- `internal/app/ports/localcommit.go` (port mới), `internal/adapters/gitworktree/localcommit.go`
  (`CreateLocalCommit` — `git add -A` → check status rỗng → `ErrNothingToCommit` → `git -c user.name=...
  -c user.email=... commit -m ...` → trả `workspace.Revision` mới), `internal/adapters/gitworktree/errors.go`
  (thêm `ErrNothingToCommit`).
- `internal/app/work/release_set.go` (mới — `CreateReleaseSet`/`SealReleaseSet`/`AbandonReleaseSet`/
  `IsCleanupEligible`/`EligibilityAuthority`).

**Test (mới hoàn toàn, chưa test nào tồn tại trước phiên này):**
- `internal/domain/work/release_set_test.go` — order-independent content hash, immutability, duplicate
  repo, invalid verdict, missing field, và `TestNewReleaseSetAllowsMixedVerdictsAcrossRepositories`
  (kịch bản "partial result" của Verify line: PASS+FAIL+ERROR cùng một ReleaseSet).
- `internal/adapters/sqlite/release_set_test.go` — round-trip, idempotent-by-ID, missing family, missing
  repository, seal/abandon thành công, `TestReleaseSetRepository_TransitionReleaseSetState_StaleVersion_Conflict`
  (kịch bản "stale revision"), not-found, list ordered by (CreatedAt, ID) xuyên 2 family.
- `internal/app/work/release_set_test.go` — persist/mixed-verdict/replay/receipt-conflict/cross-project cho
  Create; seal/abandon thành công; `TestSealReleaseSet_DuplicateSeal_Rejected` (kịch bản "duplicate seal" —
  IdempotencyKey khác trên ReleaseSet đã sealed → `ErrReleaseSetNotOpen`); `TestSealReleaseSet_
  StaleExpectedVersion_Rejected`; `IsCleanupEligible`; `EligibilityAuthority` (chưa có ReleaseSet → false,
  CREATED → false, SEALED → true).
- `internal/adapters/gitworktree/localcommit_test.go` — commit staged+untracked, `ErrNothingToCommit` trên
  workspace sạch, reject field rỗng/control-char, reject workspace đã release,
  `TestProvider_CreateLocalCommit_NeverTouchesRemote` (kịch bản "spy adapter chứng minh remote mutation
  call count bằng 0": `spyLocalCommitCreator` đếm call — mirror `spyArtifactStore` pattern — cộng với xác
  nhận trực tiếp `git remote` rỗng cả trước/sau, vì fixture repo này chưa từng cấu hình remote nào — chứng
  minh không có remote nào để mutate dù cố ý).

**Lỗi tự phát hiện và sửa:**
- Type-conversion bug tự bắt lúc build sqlite test lần đầu: truyền `string` (từ `"project."+projectID`)
  thẳng vào tham số kiểu `project.ProjectID` — Go từ chối compile vì không phải untyped constant; sửa bằng
  import `project` package + convert tường minh, đồng thời bỏ tiền tố `"project."` thừa không cần thiết.
- FK thật `release_set_repositories.repository_id → repositories(id)` không được check tường minh ban đầu
  → test đầu tiên fail với lỗi sqlite mù ("unexpected error"); sửa bằng cách thêm check tồn tại tường minh
  ở cả sqlite VÀ fake adapter (xem Quyết định #3).

**Chưa làm / cố ý để lại:**
- Không wiring `CreateLocalCommit` vào bất kỳ command ReleaseSet nào — đúng scope: hai primitive độc lập,
  một task tương lai (V5-14's own `ExecuteWorkspaceSetRelease`, hoặc luồng thật của V5-11 CompletionPolicy)
  mới là nơi ghép chúng lại, mirror đúng "producer chỉ enqueue/build primitive, consumer là task khác" mà
  `workspacerelease`'s own doc comment đã tự xác lập cho `WORKSPACE_SET_RELEASE` job.
- Không thêm archtest boundary test riêng cho `internal/app/work/release_set.go` — test chung
  `TestDomainAppNeverImportAdapters` (đã glob toàn bộ `internal/app/...`) đã cover đúng bất biến cần chứng
  minh (không import `internal/adapters/...`), và file này vốn không có lý do gì để import `os`/`os/exec`
  (không có internal executor song song trong CÙNG package như `workspacerelease`/`workspacereconcile` có).
- Không đổi `cmd/agentkit` để expose ReleaseSet qua CLI/route thật — đúng pattern mọi task V4/V5.

**Verify:**
```
go build ./...                                             # sạch
go vet ./...                                                # sạch
go run ./cmd/docs-coverage-check                            # debt = 0
gofmt -l <file mới/thay đổi>                                 # chỉ CRLF noise trên file pre-existing
                                                              # (core.autocrlf=true), không phải lỗi thật
go test -count=1 ./internal/domain/... ./internal/adapters/sqlite/... ./internal/app/ports/...
                                                              # PASS
go test -count=1 ./internal/app/work/... -v                  # PASS toàn bộ, kể cả ReleaseSet mới
go test -count=1 ./internal/adapters/gitworktree/... -v      # PASS toàn bộ, kể cả CreateLocalCommit mới
go test -count=1 ./...                                       # PASS toàn bộ ~70 package (lần 1 + lần 2 lặp
                                                              # lại để loại flake)
```
Flake đã gặp và xác nhận KHÔNG liên quan tới thay đổi phiên này (đã tự biết từ trước, xác nhận lại 3 lần
chạy riêng): `cmd/agentkit`'s `TestAdapterRegister_RejectsExecutableSwappedBetweenProbeAndRegister`
(Windows file-lock race lúc swap executable, local-only).

**Việc còn lại:** rebase sạch lên `master` (đã xong tại thời điểm viết narrative này), verify lại lần cuối,
commit, push, mở PR, chờ CI 6/6, merge.

## Remediation PR1 — Criteria-level Evidence, PASS-path only (branch `fix/v5-09-v5-10-evidence-remediation`, based on `master` post-review)

**Bối cảnh:** người dùng bảo "pull lại master" sau khi tự cập nhật rà soát ở trên; rà soát đó chỉ ra V5-09
và V5-10 (đã merge) còn acceptance gap thật, và đề nghị đóng gap trước khi tiếp tục V5-10A/V5-11. Người
dùng xác nhận: "Giữ PR #10, chuyển sang đóng gap V5-09/V5-10 trước" — PR #10 (V5-10A) giữ nguyên, KHÔNG
merge, KHÔNG sửa thêm; nhánh remediation này tách riêng từ `master` (không dựa trên `feat/v5-10a-release-set`,
vì hai việc độc lập nhau — remediation không đụng tới ReleaseSet).

**Câu hỏi xác nhận trước khi code:** Evidence phải ghi cho MỌI outcome (PASS lẫn non-PASS) theo design
doc, nhưng `attachFinalizationEvidenceTx` chỉ chạy trên nhánh SUCCEEDED — mở rộng đúng nghĩa cần sửa cả
nhánh FAILED của `finalize.go` (function fencing quan trọng nhất repo). Người dùng chọn: **PR1 chỉ
PASS-path trước**, nhưng thiết kế contract ngay từ đầu phải dùng được cho mọi terminal outcome (verbatim
plan người dùng đưa, xem lịch sử hội thoại) — PR2 (nhánh FAILED/non-PASS) là task riêng sau.

**Quyết định (theo đúng plan người dùng, không hỏi lại):**
1. **`work.Evidence` → đặt tên `runtime.Evidence`** (`internal/domain/runtime/evidence.go`, package đã có
   sẵn `Checkpoint`/`ExecutionAttempt`) — tái dùng bảng `evidence` (migration 0001, có sẵn từ V1, CHƯA từng
   được ghi bởi bất kỳ code nào cho tới remediation này). Full lineage (WorkItemID/RunID/NodeRunID/
   AttemptID) là cột có sẵn. ID = `AttemptID + ":" + Kind` (deterministic, không phải cột idempotency-key
   riêng) — mirror đúng discipline `ReleaseSet`/`WorkItemBlocker`: một redelivered finalize luôn tự
   re-derive cùng ID, nên `CreateEvidence`'s own insert-or-load-existing đã đủ an toàn cho replay.
2. **`ports.EvidenceProposal`** (mới, `execution.go`) — field trên `AttemptFinalizationEvidence`: Kind,
   Verdict, ArtifactReferences (phải là tập con của `OutputArtifactRefs`), PolicyVersion. `nil/empty` cho
   AGENT (rà soát không gắn cờ AGENT) và fake NodeExecutor cũ.
3. **Đổi tên `attachFinalizationEvidenceTx` → `validateAndAttachFinalizationEvidenceTx`** (trung lập với
   terminal state, đúng yêu cầu người dùng) nhưng **CHỈ gọi từ nhánh SUCCEEDED** trong PR1 này — nhánh
   FAILED của `finalize.go` không đổi, để lại nguyên cho PR2.
4. **Output artifact chuyển từ ATTACHED trực tiếp sang ORPHAN** (`persistCommandOutputArtifact`,
   `persistGateResultArtifact` trên nhánh PASS) — mirror đúng `buildEvidence`'s own Phase 1 (Put+Verify,
   không transaction) + Phase 2 (insert ORPHAN, transaction ngắn). `persistGateResultArtifact` được thêm
   tham số `attachState artifact.AttachState`: nhánh FAIL/ERROR (không qua finalize evidence trong PR1)
   VẪN insert ATTACHED như cũ, không đổi hành vi — chỉ nhánh PASS đổi sang ORPHAN.
5. **`validateAndAttachFinalizationEvidenceTx` mở rộng:** promote `OutputArtifactRefs` ORPHAN→ATTACHED
   (dedup theo artifact ID, vì Gate's nhiều criteria dùng chung MỘT GateResult artifact); validate mỗi
   `EvidenceEntry.ArtifactReferences` phải là tập con `OutputArtifactRefs` đã promote (không phải artifact
   mới, chưa từng thấy); rồi `tx.Runtime().CreateEvidence` cho từng entry — tất cả trong CÙNG transaction
   fenced đã có (JobLease/WriteLease fencing không đổi).
6. **PolicyVersion = `request.ExecutionProfileHash`** (field có sẵn trên `ports.AgentExecutionRequest`,
   đã pin đúng GateVersion/CommandVersion resolved profile) — không cần plumbing mới để lấy VersionID
   riêng.
7. **Command: Evidence entry chỉ tạo khi có output artifact thật** (`doc.Output.CaptureStdout ||
   CaptureStderr`) — `runtime.NewEvidence` tự đòi ≥1 artifact reference (mirror `Checkpoint`'s own
   invariant), nên khi output capture tắt, không có gì để reference, bỏ qua Evidence hoàn toàn cho case đó
   (quyết định phạm vi PR1, không phải bug).

**Thực hiện:**
- `internal/domain/runtime/evidence.go` (mới) — `Evidence`, `NewEvidence`, `EvidenceKindCommandExecution`,
  `EvidenceVerdictSucceeded`.
- `internal/app/ports/unitofwork.go` (`RuntimeRepository` +3 method), `internal/app/ports/execution.go`
  (`EvidenceProposal` mới + field `EvidenceEntries` trên `AttemptFinalizationEvidence`).
- `internal/adapters/sqlite/evidence.go` (mới, mirror `checkpoint_store.go`'s own JSON-encoding pattern),
  `internal/app/ports/fake/runtime.go` (+3 method, +field `evidence` + clone).
- `internal/app/runtime/finalize.go` (`validateAndAttachFinalizationEvidenceTx` — đổi tên + mở rộng),
  `command_node_executor.go`/`gate_node_executor.go` (ORPHAN staging + xây `EvidenceEntries`).
- Sửa 4 comment còn tên cũ `attachFinalizationEvidenceTx` (không phải call site, chỉ doc comment) ở
  `agent_node_executor_resources.go`, `agent_node_executor_test.go`, `command_node_executor.go`,
  `internal/app/ports/artifactrecord.go`.

**Test (mới hoàn toàn):**
- `internal/adapters/sqlite/evidence_test.go` — round-trip, idempotent-by-ID (kịch bản "replay"), not-found,
  list ordered by Kind xuyên nhiều criteria/attempt.
- `internal/app/runtime/evidence_remediation_test.go`:
  - `TestCommandNodeExecutor_Success_OutputArtifactOrphanUntilFinalizePromotesItWithEvidence` — ORPHAN
    trước finalize, ATTACHED sau, đúng 1 Evidence row Kind=COMMAND_EXECUTION.
  - `TestGateNodeExecutor_AllCriteriaPass_OutputArtifactOrphanUntilFinalizePromotesItWithEvidencePerCriterion`
    — tương tự cho Gate, 1 Evidence row/criterion, verdict đúng theo criterion.
  - `TestFinalizeExecutionAttempt_EvidenceEntryNamesUnlistedArtifact_RejectsBeforeCommitting` — kịch bản
    "tampered": entry trỏ artifact ngoài `OutputArtifactRefs` → reject, rollback (version/artifact state
    không đổi).
  - `TestFinalizeExecutionAttempt_MissingOutputArtifact_RejectsBeforeCommitting` — kịch bản
    "missing/foreign": `OutputArtifactRefs` trỏ artifact không tồn tại → reject, rollback.

**Quyết định phạm vi test (không dựng sqlite fixture riêng cho Command/Gate):** deep fencing edge case
(expired lease, wrong owner/token, concurrent finalize) đã có sẵn ở `finalize_execution_attempt_sqlite_test.go`
cho đúng transaction `FinalizeExecutionAttempt` này (dùng AGENT fixture) — code Evidence mới chạy TRONG
CÙNG transaction đã được test đó chứng minh rollback thật ở sqlite. Việc cần test MỚI là logic Evidence
validate/promote tự thân, không phải cơ chế fencing đã có sẵn — nên test ở tầng fake (application flow)
là đủ, đúng "sqlite-only cho fencing, fake-only cho flow" convention file đó tự ghi. Idempotent-replay
(CreateEvidence) test ở tầng sqlite thật (evidence_test.go) vì đó là nơi đúng để chứng minh.

**Chưa làm / cố ý để lại (PR2 và xa hơn):**
- Nhánh FAILED/non-PASS của `finalize.go` chưa nhận Evidence — GateResult FAIL/ERROR/NOT_RUN vẫn insert
  ATTACHED trực tiếp như cũ (không unfenced mới, cũng không được fenced mới).
- `OutputTruncated` chưa fail-closed, secret chưa vào redaction matcher, `PolicyRefs`/compatibility/
  network chưa verify/enforce, chưa có production composition/router — đúng danh sách gap còn lại của rà
  soát, không phải phạm vi PR1.
- NOT_APPLICABLE chưa kiểm pinned policy authority (mới kiểm reason) — không đổi trong PR1.

**Verify:**
```
go build ./...                                             # sạch
go vet ./...                                                # sạch
go run ./cmd/docs-coverage-check                            # debt = 0
go test -count=1 ./...                                      # PASS toàn bộ ~70 package (lần 1 + lần 2)
```
Flake đã gặp và xác nhận KHÔNG liên quan (lần chạy thứ 2, đã biết từ trước): `cmd/agentkit`'s
`TestAdapterRegister_RejectsExecutableSwappedBetweenProbeAndRegister` (Windows file-lock race, local-only).

**Việc còn lại:** commit, push nhánh `fix/v5-09-v5-10-evidence-remediation`, mở PR, chờ CI 6/6, merge —
sau đó mới quay lại PR2 (Evidence cho non-PASS) hoặc PR #10 (V5-10A) tuỳ người dùng chọn tiếp.

**Kết quả:** PR #11, 6/6 CI checks pass, squash-merged 2026-09-10, merge commit `b3845ce`. Sau đó người
dùng bảo "PR feat(v5-10a) CI xanh rồi, check rồi resolve conflict" — PR #10 conflict với `master` (do cả
rà soát lẫn PR #11 đều sửa `baocaov5checklist.md`) được merge `origin/master` vào `feat/v5-10a-release-set`
và resolve thủ công (giữ nguyên nội dung V5-10A, chỉ nối thêm nội dung mới từ master theo đúng thứ tự thời
gian: Rà soát → V5-10A → Remediation PR1).

## Remediation PR2 — Evidence cho FAILED/non-PASS (branch `fix/v5-09-v5-10-evidence-remediation-pr2`, based on `master` sau khi PR #11 merge)

**Bối cảnh:** ngay sau khi PR #11 (PR1) merge, tiếp tục theo đúng plan hai-PR người dùng đã khoá từ đầu:
"PR2 — Evidence cho FAILED/non-PASS... Cho `NodeExecutionResult.Evidence` tồn tại khi Attempt FAILED. Gate
phải trả Evidence cho FAIL/ERROR/NOT_RUN, không persist rồi bỏ artifact ID. Gọi cùng helper trong FAILED
transaction trước khi commit terminal state/retry decision... Generic AGENT/COMMAND failure không bị bắt
buộc có criteria Evidence. Với MACHINE_GATE, mỗi terminal attempt phải tạo kết quả cho mọi criterion, kể cả
pre-spawn failure dưới dạng ERROR/NOT_RUN." (verbatim plan người dùng, xem lịch sử hội thoại). Giữa lúc làm
PR2, người dùng tách một nhánh xử lý riêng: "PR feat(v5-10a) CI xanh rồi, check rồi resolve conflict" — đã
xử lý xong ở nhánh `feat/v5-10a-release-set` (merge `origin/master` vào, resolve 1 conflict duy nhất ở
`baocaov5checklist.md`, verify lại, push) — xem mục V5-10A ở trên; PR2 tiếp tục độc lập trên nhánh riêng.

**Vấn đề thiết kế phát hiện khi bắt tay code (quan trọng, quyết định toàn bộ shape PR2):**
`validateAndAttachFinalizationEvidenceTx` (PR1) LUÔN build/insert completion Checkpoint — một khái niệm CHỈ
có nghĩa cho SUCCEEDED (NodeRun advance sang node kế tiếp). Nhánh FAILED (`decideRetryOrExhaustion`) không
hề build Checkpoint. Vì vậy KHÔNG THỂ gọi thẳng `validateAndAttachFinalizationEvidenceTx` từ FAILED — phải
tách hàm thật sự trung lập trước. Đã tách:
- `validateAndAttachEvidenceArtifactsTx` (mới, trung lập hoàn toàn): terminal-event-sequence check,
  diff-manifest completeness+promote, output-artifact promote (dedup), ghi Evidence row cho từng entry —
  KHÔNG có ProposedOutcome check, KHÔNG build Checkpoint.
- `validateAndAttachFinalizationEvidenceTx` (giữ tên, SUCCEEDED-only): check `ProposedOutcome` khớp
  `SelectedOutcome`, gọi `validateAndAttachEvidenceArtifactsTx`, rồi mới build+insert Checkpoint.
- `decideRetryOrExhaustion` (FAILED/TIMED_OUT): gọi thẳng `validateAndAttachEvidenceArtifactsTx` khi
  `req.Evidence != nil` — ngay đầu hàm, trước quyết định retry/exhaustion.

**Quyết định khác:**
1. **Gate's FAILED path (`classify`, nhánh `OverallVerdict != PASS`) giờ CŨNG gọi `buildEvidence`**
   (`proposedOutcome: nil`, vì FAILED không có outcome) — an toàn vì EXECUTION_STARTED/FINISHED LUÔN được
   emit trước khi `classify` chạy, bất kể exit code/timeout/spawn error (xác nhận đọc code `Execute`), nên
   `terminalEventSequence` luôn resolve được. Gate luôn read-only nên `DiffManifestArtifacts` luôn rỗng có
   cấu trúc (không có gì để diff) — không tốn thêm chi phí thật, chỉ tái dùng đúng pipeline đã có.
2. **`persistGateResultArtifact` nhánh FAIL/ERROR giờ dùng `artifact.Orphan`** (trước là `Attached` cố định
   từ PR1) — cùng promote qua `validateAndAttachEvidenceArtifactsTx` như PASS.
3. **Evidence entry cho MỌI criterion, không chỉ criterion fail** — `gateResult.Criteria` lặp toàn bộ, mỗi
   criterion (kể cả PASS lẫn FAIL trong cùng một attempt FAILED tổng thể) có Evidence row riêng, verdict
   đúng của chính nó — chứng minh bằng test 2 criteria (lint PASS, tests FAIL) cùng lúc.
4. **`ports.NodeExecutionResult.Evidence`/`FinalizeExecutionAttemptRequest.Evidence` doc comment cập nhật**
   phản ánh field này giờ populate cả SUCCEEDED lẫn FAILED (chỉ cho MACHINE_GATE non-PASS) — AGENT/COMMAND
   FAILED giữ nguyên `nil`, đúng scope người dùng khoá.

**Thực hiện:**
- `internal/app/runtime/finalize.go` — tách hàm như trên; `decideRetryOrExhaustion` gọi
  `validateAndAttachEvidenceArtifactsTx` khi có Evidence.
- `internal/app/runtime/gate_node_executor.go` — nhánh non-PASS của `classify` xây `buildEvidence` +
  `EvidenceEntries` cho mọi criterion + đổi `persistGateResultArtifact` sang `artifact.Orphan`.
- `internal/app/ports/execution.go` — cập nhật doc comment `Evidence` trên cả hai type.

**Test (mới hoàn toàn, nối tiếp `evidence_remediation_test.go`):**
- `TestGateNodeExecutor_OneCriterionFails_OutputArtifactOrphanUntilFinalizePromotesItWithEvidencePerCriterion`
  — 2 criteria (lint PASS, tests FAIL), ORPHAN trước finalize, ATTACHED sau, Evidence row đúng verdict cho
  TỪNG criterion (kể cả criterion PASS trong một attempt FAILED tổng thể).
- `TestFinalizeExecutionAttempt_FailedGateEvidenceTampered_RollsBackRetryDecisionToo` — kịch bản "tampered"
  trên nhánh FAILED: entry trỏ artifact ngoài `OutputArtifactRefs` → reject TRƯỚC KHI
  `decideRetryOrExhaustion` commit bất kỳ quyết định retry/exhaustion nào (version Attempt không đổi).
- `TestGateNodeExecutor_NonzeroExit_EvidenceCoversEveryCriterionAsError` — nhánh `errorAllCriteria` riêng
  (spawn error/timeout/nonzero exit — code path KHÁC với JSON-parse-nhưng-fail ở trên) vẫn tạo đúng Evidence
  ERROR cho mọi criterion, đúng "kể cả pre-spawn failure dưới dạng ERROR/NOT_RUN" trong plan người dùng.

**Quyết định phạm vi test:** không dựng thêm sqlite fixture riêng cho FAILED path — cùng lý do PR1 đã ghi
(fencing thật đã được `finalize_execution_attempt_sqlite_test.go` chứng minh cho đúng transaction này; logic
Evidence mới là thứ cần test, không phải cơ chế fencing).

**Verify:**
```
go build ./...                                             # sạch
go vet ./...                                                # sạch
go run ./cmd/docs-coverage-check                            # debt = 0
go test -count=1 ./...                                      # PASS toàn bộ (lần 1 + lần 2)
```
Flake gặp lần 1, xác nhận KHÔNG liên quan (chạy riêng 3 lần đều pass): `internal/app/workerpool`'s
`TestPool_TwoPoolsRaceRecovery_NoDuplicateProcessing` (race/timing test, "context canceled" lúc startup
recovery scan — package này phiên remediation không hề chạm tới).

**Việc còn lại:** commit, push nhánh `fix/v5-09-v5-10-evidence-remediation-pr2`, mở PR, chờ CI 6/6, merge.

**Kết quả:** PR #12, 6/6 CI checks pass, squash-merged 2026-09-10, merge commit `9b8f397`. Ngay sau đó
người dùng bảo "check PR feat(v5-10a)" — PR #10 lại conflict với `master` (do PR #12 cũng sửa
`baocaov5checklist.md`) — resolve lần hai trên `feat/v5-10a-release-set` theo đúng cách lần đầu (merge
`origin/master`, chỉ nối thêm nội dung mới, verify lại, push).

## V5-09/V5-10 remaining gaps — 4 phần gộp 1 PR (branch `fix/v5-09-v5-10-remaining-gaps`, based on `master` sau khi PR #10/#11/#12 đều đã merge)

**Bối cảnh:** sau khi PR #10 (V5-10A) merge, người dùng hỏi "công việc tiếp theo là gì" — trả lời bằng
danh sách 4 gap còn lại trong "Rà soát V5-09…V5-15 trên committed master" (mục ở trên) CHƯA được PR1/PR2
xử lý: (A) `OutputTruncated` chưa fail-closed + secret đã resolve chưa vào redaction matcher, (B)
Gate's NOT_APPLICABLE chỉ check `reason` chứ chưa check "pinned policy authority", Gate's CommandRef chưa
re-verify sau load, (C) `CommandDocument.PolicyRefs`/OS-compatibility/`NetworkAccess` chưa được verify/
enforce lúc execute, (D) chưa có production composition/router chọn executor theo `ExecutorKind`, chưa có
test nào drive thẳng `ExecuteNodeHandler`→executor→fenced finalize (mọi test Command/Gate hiện có đều gọi
thẳng `executor.Execute()` rồi tự tay finalize, chưa từng đi qua `Handle` thật). Người dùng chốt: "gộp 4 pr
thành 1 luôn" — bốn phần này làm chung một nhánh/PR thay vì bốn PR riêng như thường lệ.

### Part A — Output truncation fail-closed + secret redaction (commit `6aed057`)

**Quyết định:** không phát minh `errorcode.Code` mới cho truncation — `internal/domain/errorcode` là enum
CLOSED 22 giá trị khớp `docs/architecture/04-go-core-spec.md §18`; tái dùng `CodeExecutionFailed` (comment
giải thích rõ lý do không thêm code mới). Thêm `redact.Matcher.WithSecrets(...)`/`redact.Matcher.Redact(...)`
— hợp đồng MỚI, khác hẳn `String`/`IsSecret` (so khớp nguyên giá trị): quét-và-thay-thế mọi lần xuất hiện
secret trong free text, dùng để redact output đã capture.

**Thực hiện:**
- `CommandNodeExecutor.classify` — check `OutputTruncated` ngay sau `ExitCode != 0`; nhận thêm tham số
  `secretValues map[string]string` (chính là `env` map đã resolve từ `resolveCommandInvocation`).
- `GateNodeExecutor.deriveGateResult` — check `OutputTruncated` trong nhánh `errorAllCriteria`, TRƯỚC khi
  parse JSON (JSON hợp lệ về cú pháp nhưng bị cắt cụt không được lọt qua).
- `persistCommandOutputArtifact`/`persistGateResultArtifact` — redact stdout/stderr (Command) và mỗi
  `Detail`/`Reason` của từng criterion (Gate) bằng matcher scoped riêng cho lần chạy đó trước khi marshal,
  set `Redacted: true`.

**Test:** `internal/app/runtime/truncation_redaction_test.go` (mới) — 5 test cho cả Command/Gate truncation
và secret redaction; `internal/app/redact/redact_test.go` — 4 test mới cho `WithSecrets`/`Redact`.

### Part B — NOT_APPLICABLE authoring authorization + CommandRef re-verify (commit `6d3892c`)

**Quyết định:** thêm `gate.Criterion.AllowNotApplicable bool` (field additive, mặc định `false` — chặt hơn
hành vi cũ). `deriveGateResult` check `!c.AllowNotApplicable` TRƯỚC (ERROR nếu tác giả criterion chưa cho
phép, dù runtime có cung cấp `reason` hay không), rồi mới đến check "reason rỗng" cũ. Thêm re-verify
`CommandRef.DefinitionID` khớp sau khi load `commandVersion` trong `gatherGateExecutionInputs` (mirror
đúng check GateVersion pin đã có sẵn).

**Thực hiện:** `internal/domain/gate/gate.go` (+field), `gate_node_executor.go` (2 chỗ trên).

**Test:** sửa `TestGateNodeExecutor_NotApplicableWithReason_CountsAsPass` +
`TestGateNodeExecutor_NotApplicableWithoutReason_FinalizesFailed` để set `AllowNotApplicable: true` (cô lập
đúng path đang test); thêm mới `TestGateNodeExecutor_NotApplicableWithoutAuthorization_FinalizesFailed`
(chứng minh reject dù CÓ reason, khi chưa được authorize).

**Quyết định phạm vi test:** không viết test riêng cho case CommandRef mismatch — cần fixture tuỳ biến sâu
vượt qua `gateFixture`'s hardcoded pin; check GateVersion pin tương tự đã có sẵn từ trước cũng không có test
riêng — nhất quán với tiền lệ, không phải gap tự tạo ra.

### Part C — OS compatibility + NetworkAccess/PolicyRefs enforcement (commit `c993270`)

**Quyết định:** thêm `verifyCommandCompatibilityAndPolicy` gọi trong transaction read-only sẵn có của
`gatherCommandExecutionInputs`, ngay sau `decodeCompiledCommand`. Ba check: (1) OS compatibility — nếu
`doc.Compatibility.OS` không rỗng, phải chứa `runtime.GOOS` thật (defense-in-depth, vì publish-time
`validateCompatibility` đã BẮT BUỘC OS list không rỗng — sửa lại 1 test giả định sai "OS rỗng = không ràng
buộc"); (2) MỌI `doc.PolicyRefs` đều được resolve qua `tx.Definitions().LoadVersion` + re-verify
`DefinitionID` (không chỉ khi `NetworkAccess=ALLOWED`); (3) `NetworkAccess=ALLOWED` đòi ít nhất một
`PolicyRefs` đã resolve có `GrantedCapabilities` chứa capability mới `NETWORK_ACCESS` (tái dùng đúng quy ước
extensible-by-name đã ghi trong doc comment `PermissionRules`). Lỗi dùng sentinel có sẵn
`ErrCommandInvocationUnresolvable`; `Execute` đổi cách xử lý lỗi từ `gatherCommandExecutionInputs` — check
`errors.Is(..., ErrCommandInvocationUnresolvable)` → trả `NodeExecutionResult{State: Failed, ErrorCode:
CodeValidationFailed}` thay vì để lỗi Go cứng lan ra (failure mode này deterministic/pre-spawn, không phải
transient).

**Thực hiện:** `command_node_executor.go` (+const `networkAccessCapability`, +hàm mới, +call site,
+error-handling ở `Execute`); `commandFixtureOptions` (+3 field mới: `compatibility`, `networkAccess`,
`policyRefs`).

**Test:** `internal/app/runtime/compatibility_policy_test.go` (mới) — 4 test: OS không tương thích fail
closed không spawn; `NetworkAccess=ALLOWED` không có grant fail closed; `NetworkAccess=ALLOWED` VỚI policy
grant thật thì succeed (phải sửa fixture `IsolationTier` — enum chỉ có đúng 2 giá trị hợp lệ, để zero-value
publish sẽ fail); PolicyRef không resolve được fail closed không spawn.

### Part D — Production NodeExecutor router + integration test qua `ExecuteNodeHandler` thật (mới, chưa có commit riêng, sẽ commit cùng lượt push)

**Nghiên cứu xác nhận trước khi code:** `grep -rln "NodeExecutor\b" cmd/ --include=*.go | grep -v _test.go`
rỗng — CHƯA CÓ bất kỳ production wiring nào cho NodeExecutor (kể cả AGENT), lặp lại đúng pattern đã ghi
xuyên suốt mọi task V4/V5 trước ("Không đổi `cmd/agentkit` để nối executor thật vào route/CLI"). `cmd/
agentkit` chỉ có `adapter.go`/`cli.go`/`definition.go`/`main.go`, không có composition root nào chạy
`ExecuteNodeHandler`. `ExecuteNodeHandler` có đúng MỘT field/param `executor ports.NodeExecutor` — một slot
duy nhất phải phục vụ cả 3 loại node.

**Quyết định:** thêm `NodeExecutorRouter` (`internal/app/runtime/node_executor_router.go`) — implement
`ports.NodeExecutor`, giữ 3 field `Agent`/`Command`/`Gate ports.NodeExecutor`, `Execute` switch trên
`runtimedomain.ExecutorKind(req.ExecutorKind)` để dispatch đúng executor, cắm thẳng vào slot duy nhất của
`ExecuteNodeHandler` — không cần đổi gì ở `execute.go`. Kind không nhận diện được HOẶC kind hợp lệ nhưng
chưa wire field tương ứng đều fail closed bằng lỗi rõ ràng, không panic nil-pointer. **Không** wire router
vào `cmd/agentkit`'s CLI/composition root thật — đúng pattern "chưa nối CLI" đã lặp lại ở mọi task V4/V5
trước, tự quyết định theo tiền lệ vì không có gì trong yêu cầu 4-part gap này đòi hỏi CLI thật.

**Test cross-platform smoke — quyết định KHÔNG thêm mới:** `internal/adapters/process/supervisor_test.go`
đã spawn process thật (`TestSupervisorRunsExecutableWithoutShell` và cùng nhóm) dưới đúng CI matrix Windows+
Linux của repo — đó mới là hợp đồng "process thật có launch được trên OS này không". Test Command/Gate ở
package `internal/app/runtime` (kể cả test router mới) luôn dùng `fake.ProcessSupervisor` — đúng nhất quán
với mọi test executor khác trong package này; thêm 1 real-spawn test ở đây sẽ test lại đúng adapter đã có
CI riêng, không test thêm gì cho router.

**Thực hiện:**
- `internal/app/runtime/node_executor_router.go` (mới) — `NodeExecutorRouter` + `Execute`.
- `internal/app/runtime/node_executor_router_test.go` (mới) —
  `commandRouterExecutionFixture` (mirror `commandFixture` nhưng dừng ngay sau `ScheduleExecutableNodeRun`,
  KHÔNG tự claim RUNNING/tự tạo job giả — để chính `Handle` claim job EXECUTE_NODE thật đã enqueue).
  4 test:
  - `TestExecuteNodeHandler_CommandExecutorKind_RoutesToRealCommandExecutorAndFinalizes` — test tích hợp
    ĐẦU TIÊN trong repo drive thẳng `ExecuteNodeHandler.Handle` thật → `NodeExecutorRouter` →
    `*CommandNodeExecutor` thật → fenced `FinalizeExecutionAttempt` thật, khẳng định Attempt SUCCEEDED/
    COMPLETED, NodeRun SUCCEEDED với outcome "done", và `supervisor.Calls == 1` (router thực sự dispatch,
    không phải no-op).
  - `TestNodeExecutorRouter_DispatchesToMatchingExecutorOnly` — 3 fake executor (Agent/Command/Gate) cùng
    wire, gọi kind COMMAND thì chỉ `Command.Calls` tăng, 2 cái kia giữ nguyên 0.
  - `TestNodeExecutorRouter_UnrecognizedKind_FailsClosedWithoutPanicking` — kind lạ ("BOGUS") → lỗi rõ ràng.
  - `TestNodeExecutorRouter_RecognizedButUnwiredKind_FailsClosedWithoutPanicking` — kind hợp lệ (COMMAND)
    nhưng field chưa wire (nil) → lỗi rõ ràng, không panic.

**Verify (toàn bộ 4 phần, chạy sau khi Part D xong):**
```
go build ./...                                   # sạch
go vet ./...                                      # sạch
go run ./cmd/docs-coverage-check                  # debt = 0
gofmt -l internal/app/runtime/node_executor_router.go internal/app/runtime/node_executor_router_test.go
                                                   # rỗng
go test -count=1 ./...                            # PASS toàn bộ (lần 1 + lần 2, không flake)
```

**Việc còn lại:** commit Part D, push nhánh `fix/v5-09-v5-10-remaining-gaps`, mở PR gộp cả 4 phần, chờ CI
6/6, merge.

**Kết quả:** PR #13, 6/6 CI checks pass NGAY LẦN CHẠY ĐẦU (không cần rerun), squash-merged 2026-09-10,
merge commit `0c2c2d0`. Đóng toàn bộ gap V5-09/V5-10 mà bài rà soát 2026-09-10 tìm thấy, ngoại trừ một
điểm CỐ Ý để ngoài phạm vi: Gate's read-only mount enforcement vẫn chỉ là descriptor (evaluator vẫn nhận
host path thật) — cần cơ chế sandbox/`ENFORCED_ISOLATED` thật, một quyết định kiến trúc lớn hơn, chưa
scope với người dùng.

## V5-11 scoping — người dùng chốt 3 câu hỏi mở (2026-09-10)

Ngay sau khi PR #13 merge, hỏi lại 3 câu hỏi mở của V5-11 (rework edge, assurance levels, "join") qua
AskUserQuestion — theo đúng yêu cầu người dùng "hỏi lại luôn vào ô chat này". Người dùng trả lời đầy đủ,
chi tiết (không chỉ chọn option) cho cả 3 câu — spec đầy đủ đã lưu verbatim vào memory
`agent-kit-v5-11-completion-policy-research.md`. Tóm tắt quyết định (chi tiết đầy đủ ở đó, không lặp lại
ở đây):
1. **Rework edge** → tách thành task riêng **V5-10B** (không gộp vào V5-11) — typed `EdgeKind =
   FLOW|COMPLETION_REWORK`, route pin ID/target/budget, scheduler không traverse, compiler/hash bao
   gồm route, validation đầy đủ. Lý do người dùng nêu: giữ thay đổi schema/compiler/validator tách khỏi
   transaction quyết định (fencing-critical) của CompletionPolicy.
2. **Assurance levels** → mở rộng `policy.CompletionRules` tại chỗ (không tạo aggregate riêng) — shape
   Go cụ thể người dùng đưa ra (`AssuranceLevel`/`AssuranceRequirement`/`RequiredAssurance`, V1 flat và
   V2 ladder mutually exclusive) đã lưu nguyên văn vào memory, sẽ dùng khi code V5-11.
3. **"Join"** → XÁC NHẬN là cơ chế FORK/JOIN branch-token đã có (V4-10/11), không phải cross-Run
   reconciliation — V5-11 không được gộp evidence giữa nhiều Run của cùng WorkItem; phát hiện Run khác
   non-terminal cùng WorkItem là invariant violation → BLOCK.

Cập nhật `docs/design/07-v5-execution-evidence.md`: thêm mục **V5-10B** đầy đủ (Mục tiêu/Phụ thuộc/Phạm
vi/Nền đã có/Thực hiện/Verify/Hoàn thành khi/Nguồn) ngay sau V5-10A; sửa mục **V5-11**'s "Dữ kiện phải
khóa" — 3/5 điểm chốt (2,3,4), còn 2 điểm chưa chốt (1: nơi pin CompletionPolicyVersion cho Run; 5:
idempotency/transaction boundary) nên V5-11 vẫn CHƯA ĐỦ DỮ KIỆN, nhưng không còn bị chặn bởi rework-edge
hay join ambiguity nữa.

## V5-10B — Completion rework route schema (branch `fix/v5-10b-completion-rework-route-schema`, based on
`origin/master` post-PR#13)

**Bối cảnh:** giải quyết điểm (3) trong 5 "Dữ kiện phải khóa" của V5-11 — ADR-009/ADR-021/GC-INV-10/
GC-INV-29 đều yêu cầu CompletionPolicy's REWORK outcome route qua "rework edge đã publish trong
WorkflowVersion", nhưng `validateNormalizedDocument` cấm MỌI outgoing edge từ END, không phân biệt loại —
không có gì để CompletionPolicy pin. Theo đúng quyết định người dùng: tách hẳn khỏi V5-11, một task/PR
riêng.

**Nghiên cứu trước khi code (quan trọng, quyết định toàn bộ shape):**
- `Edge` (`workflow.go`) hiện chỉ có `{Key, From, Outcome, To}` — không phân biệt loại.
- `validateNormalizedDocument` (`validation.go`): mọi edge cần `Outcome` khớp `From.Outcomes` đã khai;
  `outgoing[END]` khác rỗng → reject "cannot have outgoing edges" (không phân biệt loại).
- `findEdge` (`advance.go`, scheduler thật) chỉ bao giờ được gọi với key của node VỪA hoàn thành với một
  outcome đề xuất — KHÔNG BAO GIỜ với key của END (END terminal, không "advance" qua outcome). Kết luận
  quan trọng: **scheduler không bao giờ tự động traverse một COMPLETION_REWORK edge, kể cả không sửa gì
  ở `advance.go`** — thoả mãn "scheduler không được traverse rework edge" hoàn toàn CẤU TRÚC, không cần
  code runtime mới.
- Hash (`compiler.go`'s `Compile`) marshal TOÀN BỘ `normalizedDocument` — field mới trên `Edge` tự động
  vào hash, không cần code hash riêng ("Compiler/hash phải bao gồm route" thoả mãn miễn phí).
- `cloneDocument` hiện copy `Edges` NÔNG (`append([]Edge(nil), ...)`) — nếu thêm field con trỏ
  (`ReworkPolicy`) mà không sửa chỗ này, `WorkflowVersion.Document()`'s "immutable, accessors return
  copies" bị vi phạm (hai lần gọi share cùng con trỏ). Phải sửa thành deep-copy như `Node.CyclePolicy` đã
  làm.
- `CycleMembership` (exported, dùng lại bởi V4-07's escalation-edge-rời-cycle check ở `advance.go`) tự
  build `outgoing` map RIÊNG từ `document.Edges`, độc lập với `validateNormalizedDocument`'s map — nếu
  không lọc COMPLETION_REWORK ở đây, một rework edge có thể gộp SCC của END và target lại làm một,
  corrupt component numbering mà V4-07 dựa vào.

**Quyết định thiết kế (một số điểm KHÔNG được người dùng đặc tả chi tiết, tự quyết định có lý do, ghi rõ
ở đây để review lại nếu cần):**
1. **Đúng một COMPLETION_REWORK edge mỗi END** (không phải nhiều route theo outcome khác nhau) — dựa
   trên cách ADR-021/GC-INV-29 luôn dùng số ít "rework edge đã publish", và V5-11's "load exact published
   rework route" (số ít). Tái dùng CHÍNH `routes` map dedup-theo-(From,Outcome) đã có sẵn để enforce "tối
   đa 1" miễn phí, vì `Outcome` cố định rỗng cho mọi rework edge.
2. **`Outcome` PHẢI rỗng cho COMPLETION_REWORK** — không dùng để chọn giữa nhiều route (vì chỉ có 1 route
   mỗi END), tránh field chết/gây hiểu lầm. FLOW edge giữ nguyên yêu cầu `Outcome` không rỗng + khớp
   `From.Outcomes` như cũ.
3. **`ReworkPolicy{MaxIterations uint32}`** mirror đúng `CyclePolicy`'s tiền lệ — KHÔNG có
   `EscalationOutcome` tương đương, vì ADR-021 đã định nghĩa sẵn fallback khi hết budget (CompletionPolicy
   trả BLOCK thay vì REWORK — GC-INV-29), không cần một routable escalation target riêng ở edge.
4. **COMPLETION_REWORK edge bị loại HOÀN TOÀN khỏi mọi graph-structural algorithm** (reachability
   walkForward/walkBackward, `validateBoundedCycles`/SCC, `validateForkJoinTopology`, `CycleMembership`)
   — nó là routing table riêng của CompletionPolicy, không phải đồ thị scheduler duyệt. Hệ quả: rework
   target PHẢI độc lập là node đã kết nối hợp lệ trong đồ thị FLOW thường (reachable từ START, có path
   tới END) — không thể là node "chỉ tồn tại nhờ rework". Test `TestValidateDocumentRejectsReworkEdge
   TargetUnreachableFromNormalFlow` chứng minh + ghi rõ đây là ranh giới phạm vi cố ý, không phải thiếu
   sót.
5. **`Kind` rỗng ("") và `Kind: EdgeFlow` ("FLOW") tương đương ở mọi nơi** — không normalize document cũ,
   giữ nguyên hash của mọi WorkflowVersion đã publish trước task này (field mới đều `omitempty`).

**Thực hiện:**
- `internal/domain/workflow/workflow.go` — `EdgeKind` (`FLOW`/`COMPLETION_REWORK`) + `ReworkPolicy{
  MaxIterations}` + `Edge.Kind`/`Edge.ReworkPolicy` (cả hai `omitempty`); `cloneDocument` sửa deep-copy
  Edges (trước đó copy nông, giờ mirror đúng cách Node.CyclePolicy đã clone).
- `internal/domain/workflow/validation.go` — switch theo Kind trong vòng lặp edge: FLOW giữ nguyên logic
  cũ + reject nếu có `ReworkPolicy`; COMPLETION_REWORK reject Outcome khác rỗng, From không phải END, To
  là END, thiếu/`MaxIterations==0` reworkPolicy; CHỈ FLOW mới được thêm vào `outgoing`/`incoming` map.
  Sửa message "END node cannot have outgoing edges" → "...outgoing FLOW edges" (khớp hành vi mới).
  `CycleMembership` lọc COMPLETION_REWORK khỏi map riêng của nó.
- `docs/design/07-v5-execution-evidence.md` — thêm mục V5-10B đầy đủ theo đúng template các task khác
  (Mục tiêu/Phụ thuộc/Phạm vi/Nền đã có/Thực hiện/Verify/Hoàn thành khi/Nguồn); sửa V5-11's "Dữ kiện phải
  khóa" phản ánh 3/5 điểm đã chốt.

**Test (mới hoàn toàn):** `internal/domain/workflow/rework_edge_test.go` —
- `TestValidateDocumentRejectsInvalidReworkEdges` (table, 9 case): FLOW edge từ END; COMPLETION_REWORK
  không từ END; targets END; có Outcome; thiếu reworkPolicy; `MaxIterations==0`; FLOW có reworkPolicy; 2
  route cùng END (duplicate); Kind lạ ("BOGUS").
- `TestValidateDocumentAcceptsCompletionReworkEdge` — golden path: 1 rework edge END→node đã reachable
  bình thường, validate sạch.
- `TestValidateDocumentAcceptsExplicitFlowKind` — `Kind:"FLOW"` tường minh tương đương Kind rỗng.
- `TestValidateDocumentRejectsReworkEdgeTargetUnreachableFromNormalFlow` — ranh giới phạm vi cố ý (xem
  Quyết định #4).
- `TestCycleMembership_ExcludesCompletionReworkEdges` — END/target không bị gộp cùng SCC component qua
  rework edge.
- `TestCloneDocument_DeepCopiesEdgeReworkPolicy` — 2 lần gọi `WorkflowVersion.Document()` không share con
  trỏ `ReworkPolicy` (sửa mutation ở bản trả về đầu không leak sang bản thứ hai).

**Verify:**
```
go build ./...                                                              # sạch
go vet ./...                                                                # sạch
go run ./cmd/docs-coverage-check                                            # debt = 0 (sau khi sửa
                                                                             # Nguồn token: ADR-NNN
                                                                             # không được kèm "(§N)")
gofmt -l internal/domain/workflow/workflow.go internal/domain/workflow/validation.go
  internal/domain/workflow/rework_edge_test.go                              # rỗng (sau gofmt -w,
                                                                             # CRLF do git-on-Windows)
go test -count=1 ./...                                                      # PASS toàn bộ (lần 1 + lần
                                                                             # 2, không flake)
```
Lỗi gặp và sửa trong lúc verify: `Nguồn` line ban đầu viết `ADR-009 (§10), ADR-021 (§23)` — docs-coverage
-check reject vì grammar `ADR-NNN` không cho hậu tố `(§N)` (chỉ `ROADMAP-§<S>` mới có; xem
`docs/design/00-roadmap.md §3`'s bảng grammar) — sửa lại bare `ADR-009, ADR-021, GC-INV-10, GC-INV-29`.

**Việc còn lại:** commit, push nhánh `fix/v5-10b-completion-rework-route-schema`, mở PR, chờ CI 6/6,
merge. Sau khi merge, V5-11 (CompletionPolicy service) vẫn còn 2 điểm chưa chốt (pin CompletionPolicyVersion
cho Run; idempotency/transaction boundary của DecisionArtifact) — cần làm rõ với người dùng trước khi bắt
đầu code V5-11 thật.

**Kết quả:** PR #14, 6/6 CI checks pass NGAY LẦN CHẠY ĐẦU, squash-merged 2026-09-10, merge commit
`82a41a0`. Một WorkflowVersion giờ khai được đúng một `COMPLETION_REWORK` edge mỗi END.

## V5-11 scoping — người dùng chốt 2 câu hỏi mở còn lại (contract 1 và 2, 2026-09-10)

Ngay sau khi PR #14 merge, hỏi lại 2 điểm chưa chốt cuối cùng của V5-11 trong cùng ô chat (đúng yêu cầu
trước đó "hỏi lại luôn vào ô chat này"). Người dùng trả lời đầy đủ, chi tiết cho cả hai — bản đầy đủ đã
lưu verbatim vào memory `agent-kit-v5-11-completion-policy-research.md`, tóm tắt ở đây:

**Contract 1 — nơi pin CompletionPolicyVersion:** `WorkflowDocument.CompletionPolicyRef
*definition.DependencyPin` (ROOT-level, KHÔNG per-node, KHÔNG trên WorkItem/END). Lý do người dùng nêu:
WorkItem sẽ tạo thêm một nguồn cấu hình độc lập với WorkflowVersion; pin trên END có thể khiến cùng một
Run đổi policy khi đi qua REWORK (mỗi lần re-entry END có thể tự mang pin riêng nếu pin nằm ở đó). Luồng:
publish-time resolve trong cùng registry snapshot, xác minh `Category == COMPLETION` + có
`CompletionRules`, ghi vào `WorkflowVersion.DependencyManifest`; `StartWorkflowRun` đã copy nguyên
manifest này vào `ExecutionManifest` (đã đúng từ trước, không cần sửa). V5-11 tự đọc root ref rồi đối
chiếu ID/version/hash trong `ExecutionManifest` trước khi load policy thật.

**Xác minh lại với code thật trước khi nhận (nghiên cứu 2026-09-10,
`internal/app/workflowcompiler/compiler.go`):** đây CHÍNH XÁC là cơ chế `CompileAndResolve` đã dùng cho
mọi pin cấp-node (Agent/Command/Gate/PolicyRefs) — `collectReferences` (thêm root ref vào đây) →
`resolveReferences` (đã có nhánh riêng cho `KindPolicy` soi `doc.Category`/`doc.Permission`, cần thêm 1
nhánh cho `doc.Category == policy.CategoryCompletion`) → `buildDependencyManifest` (đã tổng quát hoàn
toàn, không cần sửa). `internal/domain/runtime/manifest.go`'s `ExecutionManifest.DependencyManifest` có
type CHÍNH LÀ `workflow.DependencyManifest` — xác nhận claim "tự động chảy qua, miễn phí". **Code mới cần
viết: 1 field + ~10 dòng ở workflowcompiler. Không đổi gì ở runtime/schedule.go** — giống hệt shape
"chỉ schema, không đụng scheduler" của V5-10B.

**Contract 2 — transaction/idempotency boundary của DecisionArtifact (fencing-critical, spec đầy đủ):**

Với mỗi completion candidate `(RunID, EndNodeRunID)`, đúng một `COMPLETION_DECISION_V1` immutable được
commit. DecisionArtifact và MỌI effect của quyết định đó commit trong CÙNG MỘT database transaction, hoặc
không effect nào commit cả.

Transaction boundary, đúng thứ tự:
1. **Replay check TRƯỚC** (trước khi validate Run state hiện tại, vì lần gọi thành công đầu tiên đã đổi
   state đó rồi): derive `DecisionArtifactID` tất định từ `(RunID, EndNodeRunID)`. Đã tồn tại với
   candidate+input khớp → trả kết quả đã lưu. Cùng ID nhưng content khác → trả idempotency conflict.
2. **Revalidate input có thẩm quyền:** Run đang VERIFYING tại `ExpectedRunVersion`; END NodeRun thuộc
   đúng Run và đã SUCCEEDED; WorkItem và cancellation fence cho phép completion; load đúng
   CompletionPolicy đã pin, evidence, approvals, RevisionSet, và đúng SEALED ReleaseSet gắn với
   candidate này.
3. **Ghi tất cả atomically:** insert DecisionArtifact immutable; áp state transition đã chọn; tạo REWORK
   activation hoặc FAIL blocker nếu áp dụng; append `COMPLETION_DECIDED`; tạo outbox record; ghi command
   receipt/job completion.

Bảng outcome → atomic writes: `PASS` → Run SUCCEEDED + WorkItem DONE; `REWORK` → Run RUNNING + đúng một
activation trên rework route; `BLOCK` → Run BLOCKED + WorkItem BLOCKED; `FAIL` → Run FAILED + WorkItem
BLOCKED + đúng một blocker `COMPLETION_POLICY_FAILED`.

ID phụ đều sha256 (không phải string concatenation thuần):
```
DecisionArtifact = hash("completion-decision", RunID, EndNodeRunID)
Event            = hash(DecisionArtifactID, "recorded")
ReworkActivation = hash(DecisionArtifactID, "rework")
FailBlocker      = hash(DecisionArtifactID, "failed-blocker")
```
Bất kỳ CAS/fence/insert/event/activation/blocker nào fail thì TOÀN BỘ transaction rollback. Hệ quả người
dùng nêu rõ: không có DecisionArtifact mà thiếu transition+side effect; không có transition committed mà
thiếu DecisionArtifact; nhiều evaluator đồng thời hội tụ về đúng MỘT quyết định, gọi lại sau đó replay
đúng kết quả đó; retry với transport idempotency key KHÁC vẫn hội tụ cùng semantic decision vì artifact ID
đến từ chính completion CANDIDATE, không phải command invocation. Network/filesystem/artifact-store nằm
NGOÀI transaction — transaction chỉ tiêu thụ ID/version/hash bất biến đã verify sẵn với database state.

**Xác minh lại với code thật (nghiên cứu đầy đủ qua Explore agent, 2026-09-10):**
- `runtime.DecisionArtifact` ĐÃ TỒN TẠI (`internal/domain/runtime/decision.go`:
  `{ID, ProjectID, Kind, PolicyVersion, Input, Result, CreatedAt}`), qua `ports.RuntimeRepository.
  RecordDecisionArtifact`/`GetDecisionArtifact`. ID do caller tự mint (không tự derive) — mọi call site
  hiện tại dùng string concatenation (`schedule.go`: `NodeRunID+"-execution-profile-v1"`);
  `RecordDecisionArtifact` tự nó trả `ErrPersistenceAlreadyExists` khi trùng ID — KHÔNG tự làm "replay
  trả kết quả cũ" — V5-11 phải tự `GetDecisionArtifact` trước, so content, rồi mới
  `RecordDecisionArtifact`, đúng như contract 2 mô tả.
- Tiền lệ sha256-làm-ID tốt nhất: `advance.go`'s `deterministicJoinNodeRunID` — `sha256.Sum256(a+"\x00"+b)`,
  hex, CẮT còn 16 byte, có prefix (`"join-"+hex(sum[:16])`) — doc comment của chính nó trích dẫn
  DecisionArtifact ID làm tiền lệ mà nó đang mở rộng "qua content hash thay vì string concatenation
  thuần". Mirror đúng shape này cho cả 4 ID mới.
- `openWorkItemBlockerTx` (`internal/app/runtime/blocker.go`) là helper CÓ SẴN, DÙNG LẠI ĐƯỢC cho
  outcome FAIL — đã làm idempotent-insert-or-return-existing + CAS WorkItem sang BLOCKED + event
  `WORK_ITEM_BLOCKED`, đã được gọi bởi `transitionRunToCancelledTx`/`requestScopeExpansionTx`. Dùng lại
  trực tiếp với `blockerID` tất định, không viết lại.
- Không có outbox call riêng — `tx.Events().Append` TỰ NÓ ghi outbox row khớp trong cùng transaction
  (GC-INV-16). "Append COMPLETION_DECIDED" + "tạo outbox record" trong contract 2 thực ra là MỘT lời gọi,
  không phải hai.
- `ApprovalRequest` (không phải "Approval") + `tx.Approvals().ListApprovalRequestsForRun(ctx, runID)` đã
  có sẵn (chỉ scope theo Run, không có fetch theo WorkItem) — khớp đúng kết luận "join không phải
  cross-Run" đã chốt trước đó.
- `tx.Work().ListReleaseSetsForFamily` + so `.State == workdomain.ReleaseSetSealed` là pattern có sẵn
  (`EligibilityAuthority.IsReleaseAuthorized`, V5-10A) để mirror cho "load SEALED ReleaseSet".
- Pattern `tx.Receipts().Load/Record` (idempotent-replay-trả-kết-quả-cũ) đã có ở MỌI command handler
  trong repo — là một lớp RIÊNG, THÊM VÀO, khác với replay check ở cấp DecisionArtifact (người dùng tự
  phân biệt rõ: "retry với transport idempotency key KHÁC vẫn hội tụ cùng semantic decision" — lớp
  receipt là per-invocation, lớp DecisionArtifact là per-candidate). Giữ CẢ HAI lớp.
- Race cancellation đã được xử lý cấu trúc sẵn: `reconcileRunTerminalityTx` không bao giờ để Run đang
  CANCELLING tới được VERIFYING; check "Run VERIFYING tại ExpectedRunVersion" trong contract 2 tự bắt
  được race còn lại (CancelRun đến SAU khi đã VERIFYING nhưng trước khi transaction này commit chỉ đơn
  giản làm version CAS fail) — không cần cơ chế riêng.

**Kết luận:** V5-11 không còn câu hỏi scoping nào mở. Kế hoạch 3-PR (mirror đúng pattern PR1/PR2 của
Evidence + pattern "schema tách khỏi transaction" của V5-10B): **PR0** = schema foundation (contract 1 +
assurance ladder của contract cũ) — làm ngay dưới đây. **PR1** = CompletionPolicy service, thiết kế
contract cho cả 4 outcome nhưng chỉ triển khai PASS+BLOCK trước (hai outcome không có side-effect
activation/blocker). **PR2** = REWORK+FAIL.

## V5-11 — PR0: schema foundation (branch `fix/v5-10b-completion-rework-route-schema`, tiếp tục trên cùng
worktree sau khi PR #14 merge, chưa push riêng — xem "Việc còn lại")

**Bối cảnh:** phần schema thuần của V5-11 (contract 1 + phần "assurance levels" đã chốt từ trước khi
V5-10B bắt đầu) — tách khỏi CompletionPolicy service thật (PR1/PR2), đúng mô hình V5-10B đã dùng ("giữ
schema/compiler tách khỏi transaction quyết định fencing-critical").

### Phần A — `CompletionRules` V2 assurance ladder

**Thực hiện:** `internal/domain/policy/policy.go` — thêm `AssuranceLevel` (6 hằng số:
STATIC/LINT/UNIT/INTEGRATION/E2E/HUMAN, thứ tự cố định qua `assuranceLevelOrder` map, KHÔNG dùng thứ tự
JSON), `ApprovalRequirement{AuthorizedRoles []string}` (mirror đúng `workflow.ApprovalNodeConfig`'s
"AuthorizedRoles" — tiền lệ cụ thể duy nhất cho khái niệm approval-requirement trong repo),
`AssuranceRequirement{Level, RequiredEvidenceKinds, RequiredApprovals}`, và `CompletionRules.
RequiredAssurance []AssuranceRequirement` cạnh `RequiredEvidenceKinds` cũ (tag JSON giữ NGUYÊN, không
thêm `omitempty`, để hash của mọi CompletionRules V1 đã publish trước đây không đổi).
`internal/domain/policy/compiler.go` — đăng ký `requiredAssurance` và toàn bộ nested array của nó
(`requiredEvidenceKinds`, `requiredApprovals`, `requiredApprovals.authorizedRoles`) làm "set path" trong
CẢ `documentSetPaths` lẫn `compiledSetPaths` (order không mang nghĩa, đúng "Thứ tự level do domain code
định nghĩa" người dùng đã chốt).
`internal/domain/policy/validate.go` — viết lại `validateCompletionRules`: V1/V2 mutually exclusive (cả
hai cùng khai → reject; không cái nào → reject, message nêu cả hai lựa chọn); nhánh V1 giữ NGUYÊN logic
cũ không đổi 1 dòng; nhánh V2 validate từng `AssuranceRequirement` (Level hợp lệ + không trùng, ít nhất 1
trong RequiredEvidenceKinds/RequiredApprovals, dedup evidence kind trong CÙNG level, dedup approval
requirement theo canonical role-set key) + từng `ApprovalRequirement` (ít nhất 1 role, role không rỗng,
không trùng).

**Quyết định phạm vi (không được người dùng đặc tả chi tiết, tự quyết định có lý do):**
- Dedup evidence kind CHỈ trong cùng một level, KHÔNG global toàn bộ ladder — các level khác nhau hợp lệ
  cùng yêu cầu một evidence kind (vd re-verify), global-unique sẽ là luật phát minh thêm không ai yêu cầu.
- "Thứ tự level do domain code định nghĩa" là hướng dẫn cho EVALUATOR (V5-11 PR1/PR2), không phải luật
  reject ở bước validate — một tác giả liệt kê level theo thứ tự bất kỳ trong JSON vẫn hợp lệ.
- `ApprovalRequirement` chỉ có `AuthorizedRoles` — không thêm field nào khác chưa được yêu cầu (không
  TimeoutSeconds/EscalationOutcome như `ApprovalNodeConfig`, vì đây là policy-level requirement khác hẳn
  node-level approval instance).

**Test (mới hoàn toàn):** `internal/domain/policy/completion_assurance_test.go` — 11 test: ladder hợp lệ
không lỗi; cả hai V1+V2 → reject; Level lạ → reject; Level trùng → reject; requirement rỗng (không
evidence lẫn approval) → reject; evidence kind trùng trong 1 level → reject; evidence kind rỗng → reject;
approval không role → reject; role trùng → reject; approval requirement trùng (cùng role-set) → reject;
`TestCompile_AssuranceLadder_SetOrderIndependent` (hash giống nhau dù đảo thứ tự ladder/evidence
kinds/roles). Sửa 1 test cũ (`TestValidateDocument_Completion_RejectsNoRequiredEvidenceKinds`) — path đổi
từ `"completion.requiredEvidenceKinds"` sang `"completion"` vì check giờ bao quát cả hai shape.

### Phần B — `WorkflowDocument.CompletionPolicyRef`

**Thực hiện:** `internal/domain/workflow/workflow.go` — thêm field `CompletionPolicyRef
*definition.DependencyPin` (root-level, `omitempty`) vào `WorkflowDocument`; `cloneDocument` deep-copy
con trỏ này (mirror đúng cách `Edge.ReworkPolicy` đã clone ở V5-10B — `WorkflowVersion.Document()`'s
"accessors return copies" contract áp dụng cho field mới này y hệt).
`internal/app/workflowcompiler/compiler.go` — `collectReferences` gộp thêm root ref (nếu có) như MỘT
`nodeReferences{policyPins: [...]}` riêng, tái dùng nguyên `resolveReferences`'s logic resolve-theo-
DefinitionID/conflict-detection có sẵn KHÔNG cần viết lại; hàm mới `checkCompletionPolicyRefCategory` làm
đúng phần việc pipeline chung KHÔNG làm — xác minh resolved document's `Category == CategoryCompletion`
(+ `Completion != nil`, defense-in-depth vì policy.ValidateDocument's own publish-time invariant đã đảm
bảo Category=COMPLETION luôn có Completion non-nil) — gọi ngay sau `checkScopeAndCapability` trong
`CompileAndResolve`. `buildDependencyManifest` KHÔNG cần sửa gì (đã tổng quát hoàn toàn theo
DefinitionID).

**Quyết định:** không validate `CompletionPolicyRef.Kind == KindPolicy` ở `validation.go` (domain layer,
không có registry access) — đúng tiền lệ node-level ref hiện tại (`AgentNodeConfig.ProfileRef.Kind` v.v.
cũng không được check ở đây), việc verify Kind/Category thật đều nhường cho workflowcompiler's real
registry resolution, không thêm luật mới không nhất quán.

**Test (mới hoàn toàn):** `internal/app/workflowcompiler/completion_policy_ref_test.go` — 3 test: resolve
CompletionPolicyRef vào manifest đúng (kind/key/version/hash); reject khi resolved document SAI category
(policy PERMISSION thay vì COMPLETION); reject khi ref không resolve được (tái dùng đúng message lỗi
generic "does not resolve to any published version" của pipeline chung). Không viết thêm test tích hợp ở
tầng `internal/app/runtime` cho việc `ExecutionManifest` nhận đúng manifest này — cơ chế
`ExecutionManifest.DependencyManifest = workflow.DependencyManifest` đã tồn tại từ trước, được MỌI test
runtime hiện có gián tiếp chứng minh rồi (test nào cũng pass không cần sửa) — thêm test riêng cho đúng 1
loại pin sẽ trùng lặp, không test thêm điều gì mới.

**Verify:**
```
go build ./...                                                    # sạch
go vet ./...                                                       # sạch
go run ./cmd/docs-coverage-check                                   # debt = 0
gofmt -l <7 file .go đổi + 2 file .go mới>                         # rỗng sau gofmt -w (CRLF do
                                                                    # git-on-Windows, đúng precedent)
go test -count=1 ./...                                             # PASS toàn bộ (lần 1 + lần 2,
                                                                    # không flake)
```

**Việc còn lại:** commit (cùng branch cũ hay branch mới — quyết định khi commit, xem message tiếp theo),
push, mở PR, chờ CI 6/6, merge. Sau đó bắt đầu PR1 (CompletionPolicy service, PASS+BLOCK trước).

**Kết quả:** PR #15, 6/6 CI checks pass NGAY LẦN CHẠY ĐẦU, squash-merged 2026-09-10, merge commit
`6db1efd`. `CompletionRules` có V2 assurance ladder; `WorkflowDocument.CompletionPolicyRef` resolve được
qua đúng pipeline `workflowcompiler` có sẵn.

## V5-11 — PR1: CompletionPolicy service, PASS+BLOCK+FAIL (branch `feat/v5-11-completion-policy-service`,
based on `origin/master` sau PR #15)

**Bối cảnh:** người dùng bảo "start on pr1" ngay sau khi PR #15 merge — bắt đầu phần service thật của
V5-11 (contract 2), theo đúng kế hoạch 3-PR đã đề xuất trước đó.

**Quyết định phạm vi (điều chỉnh so với đề xuất ban đầu "PASS+BLOCK / REWORK+FAIL"):** phát hiện khi bắt
tay code — FAIL's side effect (mở `WorkItemBlocker` typed `COMPLETION_POLICY_FAILED`) tái dùng NGUYÊN
`openWorkItemBlockerTx` đã có sẵn từ V4-12C, không cần cơ chế mới nào — chỉ REWORK mới cần cơ chế THẬT SỰ
MỚI (tra rework route từ V5-10B's edge kind + tạo activation). Vì vậy đổi phạm vi PR1 thành
**PASS+BLOCK+FAIL** (cả ba outcome không cần cơ chế mới), PR2 chỉ còn **REWORK** (outcome duy nhất cần
route-lookup+activation-creation thật sự mới).

**Nghiên cứu trước khi code** (qua Explore agent, xác nhận lại bằng đọc code thật trước khi dùng):
- `runtime.DecisionArtifact` đã tồn tại (`internal/domain/runtime/decision.go`), ID do caller tự mint,
  KHÔNG tự làm "replay trả kết quả cũ" (bản thân `RecordDecisionArtifact` trả
  `ErrPersistenceAlreadyExists` khi trùng ID) — phải tự implement replay-check ở tầng app.
- `deterministicJoinNodeRunID` (`advance.go`) là tiền lệ sha256-làm-ID (không phải string concatenation
  thuần) — mirror đúng shape cho 3 ID mới (DecisionArtifact/Event/FailBlocker).
- `openWorkItemBlockerTx` (`internal/app/runtime/blocker.go`) dùng lại được nguyên cho FAIL.
- `tx.Events().Append` tự ghi outbox row trong cùng transaction (GC-INV-16) — "append event" và "tạo
  outbox record" trong contract 2 là MỘT lời gọi, không phải hai.
- `ApprovalRequest` (không phải "Approval") + `tx.Approvals().ListApprovalRequestsForRun` đã có sẵn, chỉ
  scope theo Run — khớp đúng "join không cross-Run" đã chốt trước đó.
- `tx.Work().ListReleaseSetsForFamily` + so `.State == ReleaseSetSealed` là pattern có sẵn
  (`EligibilityAuthority.IsReleaseAuthorized`, V5-10A).
- `tx.Runtime().ListWorkflowRunsForWorkItem` đã có sẵn — dùng để check sibling-Run invariant.
- Pattern `tx.Receipts().Load/Record` (idempotent-replay per-invocation) là lớp KHÁC, THÊM VÀO, tách biệt
  với replay check per-candidate của DecisionArtifact — giữ CẢ HAI lớp, đúng contract 2.

**Quyết định thiết kế (một số điểm KHÔNG được đặc tả chi tiết trong 2 contract, tự quyết định có lý do,
ghi rõ để review lại nếu cần):**
1. **FAIL chỉ dành cho lỗi cấu hình/evaluation, KHÔNG dành cho "requirements chưa đạt"** — ADR-021 không
   nói rõ khi nào FAIL fires (chỉ nói rõ REWORK-vs-BLOCK dựa theo rework edge). Quyết định: FAIL khi
   `CompletionPolicyRef` là nil, không resolve được, sai category, hoặc hash không khớp
   ExecutionManifest — tức "service không thể evaluate", khác hẳn BLOCK ("evaluate được, chưa đạt yêu
   cầu"). Sibling-Run-inconsistency CŨNG là BLOCK (không phải FAIL) — đúng quyết định "join" đã chốt
   trước đó.
2. **PR1: ladder không đạt LUÔN LUÔN → BLOCK, kể cả khi (giả sử) có rework edge hợp lệ** — vì REWORK
   chưa có cơ chế thật (PR2's việc); đây là default an toàn, bảo thủ — không rework qua cơ chế chưa xây.
3. **BLOCK không mở `WorkItemBlocker` row** — ADR-021's bảng chỉ ghi "kèm blocker" cho FAIL, không cho
   BLOCK; hai state transition (Run BLOCKED, WorkItem BLOCKED) tự nó là tín hiệu durable đủ.
4. **ReleaseSet gating: gia đình KHÔNG có ReleaseSet nào → bỏ qua check** (coi như thoả mãn) — một
   workflow không hề mutate gì sẽ không bao giờ phải đi qua luồng ReleaseSet của V5-10A chỉ để complete;
   gia đình CÓ ReleaseSet thì bản mới nhất phải SEALED (không ABANDONED/CREATED).
5. **Approval requirement thoả mãn = có ÍT NHẤT MỘT `ApprovalRequest` cho Run này `State=DECIDED` với
   `DecidedRole` nằm trong `AuthorizedRoles`** — KHÔNG check `DecidedOutcome` cụ thể (approve/reject):
   routing approve/reject đã là việc của chính graph (APPROVAL node's Outcomes/Edges) TRƯỚC khi tới
   END/VERIFYING; CompletionPolicy chỉ xác nhận evidence tồn tại, không lặp lại logic routing.
6. **"Evidence còn fresh" (contract assurance-level trước đó) = evidence của Attempt CUỐI CÙNG (cao nhất
   AttemptNumber) thuộc NodeRun activation MỚI NHẤT (cao nhất ActivationSequence) theo từng lineage
   (NodeKey+BranchTokenID)** — dùng lại đúng dedup logic `computeRunNodeStateSummary` (completion.go) đã
   có, factor thành `latestNodeRunIDsByLineage` dùng chung. KHÔNG cross-check RevisionSet hash — quá suy
   đoán để tự quyết định ý nghĩa "RevisionSet hiện tại" giữa một Run có COMMAND node mutate workspace.
7. **Level order (V2 ladder) do domain code định nghĩa** — thêm `policy.AssuranceLevelOrder()` (export
   mới, PR1 là consumer thật đầu tiên) thay vì để `internal/app/runtime` tự đoán thứ tự.

**Thực hiện:** `internal/app/runtime/completion_policy.go` (mới) —
`EvaluateCompletionCandidate(ctx, uow, clk, cmd, req{RunID})` là entry point DUY NHẤT được phép chuyển
Run VERIFYING→{SUCCEEDED,BLOCKED,FAILED} hay WorkItem ACTIVE→{DONE,BLOCKED}. `cmd.ExpectedVersion` là
CAS fence (đúng quy ước `cmd.ExpectedVersion` đã dùng ở mọi command khác, vd `CreateReleaseSet`), không
làm field riêng trong request. Luồng: standard receipt check (tầng ngoài) → load Run/ExecutionManifest/
WorkflowVersion/Document → `computeRunNodeStateSummary` (tái dùng từ completion.go) tìm END node run →
derive `decisionArtifactID` → gather evidence/approvals/releaseGate → build `completionCandidateInput`
(deterministic, mọi slice đã sort) → **replay check TRƯỚC** (so `existing.Input` byte-for-byte) → CHỈ
sau đó mới check Run.State==VERIFYING + `cmd.ExpectedVersion` → load WorkItem →
`decideCompletionOutcome` (sibling-Run check → resolve policy → ReleaseSet gate → ladder eval) →
`applyCompletionOutcomeTx` (state transitions theo outcome, blocker cho FAIL) → ghi DecisionArtifact →
append `COMPLETION_DECIDED` (Sequence = Run's version SAU khi transition, mirror
`transitionRunToVerifyingTx`'s convention) → ghi receipt.

**Test (mới hoàn toàn):** `internal/app/runtime/completion_policy_test.go` — 9 test, dựng fixture qua
`workflowDocumentV1()` (start->end có sẵn) + `startWorkflowRunFixture`-style + `AdvanceRun` để tới thật
VERIFYING; Evidence/ApprovalRequest/sibling-Run được seed TRỰC TIẾP qua repository calls (bỏ qua
CommandNodeExecutor/ResolveApproval/StartWorkflowRun's precondition thật — test layer này chỉ test
`EvaluateCompletionCandidate` chính nó, không re-prove các layer dưới đã có test riêng):
- PASS (V1 flat, evidence đủ); BLOCK (V1 flat, thiếu evidence); FAIL (không pin CompletionPolicyRef);
  replay (idempotency key KHÁC, cùng kết quả — chứng minh lớp replay theo candidate, không chỉ theo
  receipt); conflict (input đổi giữa 2 lần gọi cùng candidate → `ErrCompletionDecisionConflict`); BLOCK
  (sibling Run non-terminal, ladder tự nó đã đạt); PASS (V2 ladder + approval); BLOCK (V2 ladder, thiếu
  approval); BLOCK (ReleaseSet tồn tại nhưng chưa SEALED).

**Verify:**
```
go build ./...                                                    # sạch
go vet ./...                                                      # sạch
go run ./cmd/docs-coverage-check                                  # debt = 0
gofmt -l internal/app/runtime/completion_policy.go
  internal/app/runtime/completion_policy_test.go
  internal/domain/policy/policy.go                                # rỗng sau gofmt -w (CRLF do
                                                                    # git-on-Windows, đúng precedent)
go test -count=1 ./...                                            # PASS toàn bộ (lần 1 + lần 2,
                                                                   # không flake)
```

**Việc còn lại:** commit, push nhánh `feat/v5-11-completion-policy-service`, mở PR, chờ CI 6/6, merge.
Sau đó PR2 (REWORK — tra rework route từ `Edge.Kind=COMPLETION_REWORK` (V5-10B) + tạo activation mới)
là phần còn lại duy nhất của V5-11.

**Kết quả:** PR #16, 6/6 CI checks pass NGAY LẦN CHẠY ĐẦU, squash-merged 2026-09-10, merge commit
`7cf82f2`. Một flake không liên quan (`internal/adapters/process`, timing test, package không hề đụng
tới) gặp lúc verify local, xác nhận pre-existing qua 3 lần chạy riêng.

## V5-11 — PR2: REWORK (branch `feat/v5-11-completion-policy-rework`, based on `origin/master` sau PR #16)

**Bối cảnh:** người dùng bảo "ok" ngay sau khi PR #16 merge — tiếp tục PR2, phần cuối cùng của V5-11.

**Nghiên cứu trước khi code (câu hỏi cốt lõi: activation mới có cần "schedule thật" trong CÙNG transaction
không, và budget rework đếm thế nào):**
- `ScheduleExecutableNodeRun` (schedule.go) LUÔN tự mở transaction riêng của chính nó
  (`uow.WithSerializedWrite`) và resolve `RuntimeExecutionConfigProvider` NGOÀI transaction (ADR-027) —
  không thể gọi lồng bên trong transaction của `EvaluateCompletionCandidate`.
- Đọc kỹ `advanceRunTx` (advance.go, dòng ~609-664): với MỘT NodeRun downstream loại executable bình
  thường (không phải auto-advance/END/WAIT/APPROVAL/FORK), `advanceRunTx` CHỈ tạo NodeRun ở state PENDING
  mặc định — KHÔNG tự schedule (không resolve execution profile, không enqueue job) trong transaction đó.
  Việc "gọi ScheduleExecutableNodeRun sau khi AdvanceRun trả về" là một bước RIÊNG, một transaction KHÁC,
  và (đúng pattern đã lặp lại xuyên suốt V4/V5) **CHƯA CÓ production wiring nào gọi bước đó** — mọi nơi
  hiện tại (test fixture) tự tay gọi cả hai. Kết luận: PENDING-chưa-schedule là một trạng thái durable
  hợp lệ, ĐÃ ĐƯỢC CHẤP NHẬN sẵn trong codebase cho MỌI activation khác — REWORK không cần giải quyết gì
  thêm ở đây, chỉ cần tạo NodeRun đúng PENDING giống hệt cách `advanceRunTx` đã làm.
- `ActivationSequence` là bộ đếm TOÀN RUN (không phải theo lineage) — `advanceRunTx` dùng
  `current.ActivationSequence + 1`; vì REWORK không có "current" NodeRun vừa hoàn thành theo nghĩa đó,
  dùng MAX ActivationSequence trên toàn bộ `ListNodeRunsForRun` + 1.
- `GetMaxNodeIteration(runID, nodeKey)` (đã có từ V4-07) dùng lại nguyên cho `Iteration` của activation
  mới — cùng cách `advanceRunTx`'s escalation-target đã làm.
- **Đếm round rework:** không có port method liệt kê DecisionArtifact theo RunID (DecisionArtifact chỉ
  có ID, không có cột RunID có thể query) — nên không đếm qua đó. Insight: Run chỉ có thể quay lại
  VERIFYING→RUNNING→(...)→VERIFYING qua CHÍNH outcome REWORK của file này (không cơ chế nào khác đưa Run
  từ VERIFYING về RUNNING) — vì vậy **đếm số NodeRun SUCCEEDED có NodeKey = END node đã reach** chính là
  round hiện tại (1-indexed: lần đầu = round 1, 0 round rework trước đó). Không cần thêm bảng/cột mới.

**Quyết định thiết kế:**
1. **`decideCompletionOutcome` giờ trả thêm `*reworkPlan`** (nil trừ khi outcome=REWORK) — chỉ khi ladder
   KHÔNG đạt: tìm `COMPLETION_REWORK` edge từ END node đã reach (`findCompletionReworkEdge`, quét
   `document.Edges` cho `Kind==EdgeCompletionRework && From==endNodeKey`); nếu KHÔNG có edge → BLOCK y hệt
   PR1. Nếu CÓ: đếm round (`countEndReaches`); nếu `round > MaxIterations` → BLOCK với reason
   `REWORK_BUDGET_EXHAUSTED` (GC-INV-29's "hết budget → BLOCK", coi như không có edge); ngược lại → REWORK,
   build `reworkPlan{targetNodeKey, activationSequence, iteration}`.
2. **`applyCompletionOutcomeTx` case REWORK:** Run VERIFYING→RUNNING (không phải SUCCEEDED); tạo đúng một
   `NodeRun` mới (state mặc định PENDING từ `NewNodeRun`, KHÔNG tự schedule — xem nghiên cứu ở trên);
   WorkItem HOÀN TOÀN không đụng tới (đã ACTIVE sẵn, đúng "WorkItem giữ ACTIVE" của ADR-021).
3. **`EvaluateCompletionCandidate` giờ nhận thêm `ids idsource.Source`** (cần để mint NodeRunID mới) —
   thay đổi signature so với PR1 (breaking, nhưng PR1 mới merge trong CÙNG phiên, chưa có caller thật nào
   khác ngoài test) — theo đúng convention `ScheduleExecutableNodeRun`/`AdvanceRun` đã dùng.
4. **`gatherCompletionCandidateEvidence` đổi tham số:** nhận `nodeRuns []NodeRun` thay vì tự gọi lại
   `ListNodeRunsForRun` — `evaluateCompletionCandidateTx` giờ gọi MỘT LẦN, dùng chung cho evidence-gather
   VÀ rework round-counting, tránh query trùng.
5. **`CompletionDecisionResult`/event `COMPLETION_DECIDED` đều thêm `ReworkNodeRunID`/`ReworkNodeKey`**
   (omitempty) — chỉ populate khi outcome=REWORK.
6. **NodeRun activation mới KHÔNG dùng deterministic ID** (khác DecisionArtifact/Event/FailBlocker) — vẫn
   `ids.NewID()` như MỌI NodeRun khác trong codebase (NodeRun chưa từng có tiền lệ content-derived ID);
   an toàn replay đến từ decisionArtifactID, không phải từ ID của chính activation.

**Thực hiện:** `internal/app/runtime/completion_policy.go` — thêm `reworkPlan`, `appliedCompletionOutcome`
struct; `findCompletionReworkEdge`, `countEndReaches`, `maxActivationSequence` helper mới;
`decideCompletionOutcome`/`applyCompletionOutcomeTx` mở rộng như trên; `ReasonReworkBudgetExhausted` const
mới; cập nhật toàn bộ package doc comment (không còn nói "REWORK chưa làm").

**Test (mới hoàn toàn):** thêm vào `internal/app/runtime/completion_policy_test.go` —
`documentWithReworkEdge(maxIterations)` (start→implement(ROUTER, 1 outcome, auto-advance)→end, cộng
COMPLETION_REWORK edge end→implement) thay cho `workflowDocumentV1()` phẳng; refactor
`completionCandidateFixture` nhận thêm `baseDocument` param + đổi từ MỘT `AdvanceRun` cứng sang vòng lặp
tới khi VERIFYING (tổng quát cho graph nhiều hop). 2 test mới: REWORK hợp lệ dưới budget (ladder không
đạt, có edge, còn quota → REWORK, Run RUNNING, WorkItem vẫn ACTIVE, NodeRun mới đúng PENDING); BLOCK khi
budget rework đã hết (seed 2 "end" NodeRun SUCCEEDED giả trước, MaxIterations=2 → round 3 vượt quá →
BLOCK với reason `REWORK_BUDGET_EXHAUSTED`). Toàn bộ 9 test PR1 cũ vẫn pass không đổi assertion (chỉ đổi
call-site thêm `ids`).

**Verify:**
```
go build ./...                                          # sạch
go vet ./...                                             # sạch
go run ./cmd/docs-coverage-check                         # debt = 0
gofmt -l internal/app/runtime/completion_policy.go
  internal/app/runtime/completion_policy_test.go         # rỗng
go test -count=1 ./...                                   # PASS toàn bộ (lần 1 + lần 2, không flake)
```

**Việc còn lại:** commit, push nhánh `feat/v5-11-completion-policy-rework`, mở PR, chờ CI 6/6, merge. Sau
đó V5-11 (CompletionPolicy service) coi như HOÀN THÀNH đầy đủ cả 4 outcome — bước tiếp theo trong roadmap
là câu hỏi kiến trúc "Gate read-only enforcement" (chưa scope) rồi tới V5-12.

**Kết quả:** PR #17, 5/6 pass lần đầu — `Linux race and stability (V0-12)` fail ở lần chạy thứ 9/10 của
offline suite; tải artifact `v0-12-stability-report` về xác nhận CHÍNH XÁC cùng một flake đã biết
(`TestSupervisorNormalExit_TreeQuiescedFalseWhileDescendantStillRuns`, `internal/adapters/process`,
package không hề đụng tới trong PR2), không đoán mà xem trực tiếp `suite-run-9.log`. `gh run rerun
--failed` → 6/6 xanh. Squash-merged 2026-09-10, merge commit `976f18c`.

## Gate read-only enforcement — nghiên cứu + đóng gap thật (2026-09-10, tự quyết định, không cần hỏi)

**Bối cảnh:** người dùng bảo "sau khi xong, nếu không cần tôi quyết định cái gì thì tự động start sang
task tiếp theo đi" — tiếp tục ngay, không dừng hỏi "làm gì tiếp" nữa trừ khi thật sự cần quyết định của
người dùng. Item tiếp theo trong roadmap (từ bài rà soát 2026-09-10) là "Gate read-only enforcement mới
chỉ là descriptor, evaluator vẫn nhận host path thật — cần sandbox thật, quyết định kiến trúc lớn hơn."

**Nghiên cứu (kết luận: KHÔNG phải quyết định kiến trúc mới — đã có sẵn, chỉ thiếu một chỗ nối):**
- `internal/adapters/process/isolation.go`'s `IsolationChecker` (V5-05, đã confirm với người dùng từ
  trước): `ENFORCED_ISOLATED` LUÔN fail-closed (`ErrIsolationEnforcementUnavailable`) vì "codebase này
  không có real OS-level filesystem/network sandbox cho spawned child process" — đây là quyết định
  Alpha-wide ĐÃ CHỐT từ V5-05, áp dụng cho MỌI executor (Agent/Command/Gate như nhau), không phải gap
  riêng của Gate.
- `OPERATOR_TRUSTED_LOCAL` (tier duy nhất chạy được ở Alpha) được tài liệu hoá chính là "operator-granted
  trust, không phải least-privilege enforcement" — và cơ chế bù đắp đã có sẵn:
  `internal/app/scopeguard.ValidateDiffs`, "post-execution guard" đã wire vào TẤT CẢ executor
  (Agent/Command/Gate) qua `buildEvidence` dùng chung.
- Đọc kỹ `buildEvidence` (agent_node_executor_resources.go): diff được tính cho MỌI mount (kể cả mount
  Gate đã force read-only ở tầng descriptor) — `workspaces.Diff` chạy thật, không skip. Nghĩa là MỘT
  PHẦN bảo vệ đã có thật: bất kỳ write nào NGOÀI write-scope của cả WorkItem đều đã bị `ValidateDiffs`
  bắt.
- **Gap THẬT tìm thấy:** `ValidateDiffs` chỉ check theo `EffectiveScope` của WorkItem (dùng chung cho
  scope-check của Agent/Command, đúng vì các node đó CÓ quyền ghi thật). Gate's own `effectiveScope`
  KHÔNG bị ép rỗng (chỉ `workspaceMounts.Access` bị ép read-only, xem `gatherGateExecutionInputs`) — nếu
  MỘT NODE KHÁC trong CÙNG WorkItem có WRITE grant hợp lệ trên MỘT repo, và Gate (được mount READ-ONLY
  vào CHÍNH repo đó) lỡ ghi gì đó, `ValidateDiffs` sẽ KHÔNG bắt được (vì write đó vẫn "trong scope của
  WorkItem"), dù Gate CHÍNH NÓ chưa từng được cấp quyền ghi. Đây là gap thật, hẹp, có thể đóng ngay —
  không phải "cần sandbox mới."

**Quyết định (tự quyết vì đủ hẹp, không cần input mới từ người dùng — mọi tiền đề đã được người dùng
chốt sẵn ở V5-05):** thêm tham số `strictReadOnly bool` vào `buildEvidence` (dùng chung Agent/Command/
Gate) — `false` cho Agent/Command (không đổi hành vi), `true` cho CẢ HAI call site của Gate. Khi true,
sau khi `scopeguard.ValidateDiffs` pass, check THÊM: MỌI diff phải HOÀN TOÀN RỖNG (`len(diff.Files) ==
0`) — không chỉ "trong scope", mà "không đổi gì cả", đúng với chính doc comment sẵn có của package này
("A Gate is read-only by design") và GC-INV-25 ("mọi scratch output nằm ngoài source workspace" — nên
Gate hợp lệ không bao giờ có lý do đổi bất cứ file nào trong mount của mình). Lỗi mới bọc lại
`scopeguard.ErrScopeViolation` (không phải sentinel mới) nên CẢ HAI `errors.Is(err, scopeguard.
ErrScopeViolation)` đã có sẵn trong gate_node_executor.go tự động cover luôn, không cần sửa thêm gì ở
đó.

**Lỗi phát hiện lúc verify:** fixture dùng chung `newTestGateNodeExecutor` hard-code
`diff: defaultInScopeDiff()` (Files có 1 entry "M **/src/main.go") — vốn chỉ để mô phỏng "đổi file
nhưng vẫn trong write scope" cho test Command/Agent, KHÔNG có ý nghĩa thật cho Gate (chưa từng cố tình
mô phỏng "Gate ghi gì đó"). Check strict mới đúng đắn phát hiện fixture này SAI với thực tế Gate — sửa
bằng cách thêm `defaultReadOnlyDiff()` (Files rỗng) làm default MỚI cho `newTestGateNodeExecutor`, giữ
nguyên `defaultInScopeDiff()` cho mọi chỗ khác (Agent/Command test, và 1 test Gate khác — stale-revision
— tự construct executor riêng, không qua path diff-check nên không bị ảnh hưởng).

**Thực hiện:**
- `internal/app/runtime/agent_node_executor_resources.go` — `buildEvidence` +tham số `strictReadOnly
  bool`; hàm mới `validateStrictlyReadOnlyDiffs(diffs)`; `AgentNodeExecutor.buildEvidence` wrapper
  truyền `false`.
- `internal/app/runtime/command_node_executor.go` — call site truyền `false`.
- `internal/app/runtime/gate_node_executor.go` — CẢ HAI call site truyền `true`.
- `internal/app/runtime/agent_node_executor_test.go` — thêm `defaultReadOnlyDiff()`.
- `internal/app/runtime/gate_node_executor_test.go` — `newTestGateNodeExecutor` đổi default sang
  `defaultReadOnlyDiff()`.

**Test (mới hoàn toàn):** `TestGateNodeExecutor_MountChangedDespiteReadOnly_FailsWithScopeViolation` —
dùng CHÍNH `defaultInScopeDiff()` (diff hợp lệ trong write scope của WorkItem) làm workspace diff cho
Gate → phải FAILED/SCOPE_VIOLATION, chứng minh chính strict check MỚI bắt được (không phải
`ValidateDiffs` cũ, vốn sẽ PASS với diff này). Toàn bộ test Gate cũ (11+ chỗ dùng
`newTestGateNodeExecutor`) vẫn pass không đổi assertion sau khi đổi fixture default.

**Verify:**
```
go build ./...                                                          # sạch
go vet ./...                                                            # sạch
go run ./cmd/docs-coverage-check                                        # debt = 0
gofmt -l <5 file .go đổi>                                               # rỗng sau gofmt -w (CRLF)
go test -count=1 ./...                                                  # PASS toàn bộ (lần 1+2)
```

**Việc còn lại:** commit, push, mở PR, chờ CI 6/6, merge. Đây là fix ĐÓNG hẳn item "Gate read-only
enforcement" trong danh sách rà soát 2026-09-10 — không còn open item nào từ bài rà soát đó. Sau khi
merge, tiếp tục V5-12 (Maker/checker isolation) theo đúng chỉ dẫn tự động chuyển task.

## V5-12 scoping — 3 contract mở, người dùng chốt contract 1, uỷ quyền contract 2+3 (2026-09-10)

**Bối cảnh:** design doc (`docs/design/07-v5-execution-evidence.md`) tự đánh dấu V5-12 "CHƯA ĐỦ DỮ KIỆN"
cho tới khi 3 contract chốt: (1) role MAKER|CHECKER pin ở đâu, (2) checker input allowlist + typed
Evidence/Diff ref trên ContextSnapshot, (3) scratch handle + enforcement semantics. Theo đúng chỉ dẫn tự
động chuyển task, tự nghiên cứu trước khi hỏi.

**Nghiên cứu:** `AgentProfileDocument` và `workflow.AgentNodeConfig` đều CHƯA có field kiểu Role nào — đây
là fork kiến trúc thật, không phải cái đã nửa-quyết-định sẵn ở đâu đó. Gate đã có sẵn `scratchDirectory()`
(`gate_node_executor.go`) — helper `os.MkdirTemp`-based, hoàn toàn generic, không gắn gì riêng Gate — dùng
lại được thẳng cho checker's scratch. Kết luận: chỉ contract (1) là fork thật cần người dùng tự chọn;
(2) và (3) tự quyết được dựa trên (1) + tiền lệ Gate `strictReadOnly` vừa xây.

**Hỏi người dùng (AskUserQuestion, header "MAKER|CHECKER role"):** 3 lựa chọn — AgentNodeConfig (Workflow,
Recommended), AgentProfileDocument, hoặc cả hai (cross-check lúc publish).

**Quyết định của người dùng (verbatim, chốt contract 1):** chọn AgentNodeConfig. Spec chính xác:
```go
type AgentRole string
const (
    AgentRoleMaker   AgentRole = "MAKER"
    AgentRoleChecker AgentRole = "CHECKER"
)
type AgentNodeConfig struct {
    AgentProfileRef definition.DependencyPin
    Role            AgentRole
    // ...
}
```
Lý do: Role là trách nhiệm của NODE trong graph, còn AgentProfile mô tả cấu hình thực thi tái sử dụng
(model/provider/tools) — cùng một AgentProfile phục vụ được cả node MAKER và CHECKER, mỗi node vẫn tạo
NodeRun/Attempt/context riêng. Hợp đồng publish/runtime (verbatim): workflow compiler yêu cầu role hợp lệ
cho mọi AGENT node thuộc schema mới; role lưu trong canonical WorkflowVersion nên tự động pin theo exact
WorkflowVersion; AgentProfile không bao giờ có role riêng; runtime chỉ đọc role từ pinned WorkflowVersion,
không suy ra từ tên/profile/vị trí graph; WorkflowVersion cũ thiếu field vẫn rebuild được, hiểu ngầm là
MAKER; republish phải ghi role tường minh; KHÔNG BAO GIỜ mặc định một node thành CHECKER.

Field tên trong sketch của người dùng là `AgentProfileRef`, nhưng field thật hiện tại trong code là
`ProfileRef` — đọc là thêm `Role` cạnh field có sẵn, không phải đổi tên field cũ (giữ nguyên `ProfileRef`
để tránh phá vỡ mọi call site hiện có).

**Uỷ quyền của người dùng (verbatim):** "các quyết định nhỏ hơn khác của task này tôi có thể tự quyết dựa
theo đáp án này + tiền lệ read-only vừa xây cho Gate" — contract 2 (checker input allowlist) và contract 3
(scratch + enforcement semantics) tự thiết kế ở PR sau, không cần hỏi lại.

**Kết luận:** không còn câu hỏi kiến trúc mở cho contract 1. Bắt đầu code ngay theo kế hoạch nhiều PR
(mirror pattern V5-11): PR0 = schema foundation (Role field + validation 2 tầng + hash + runtime pin) —
xây trước, tự-chứa, test được độc lập.

## V5-12 — PR0: schema foundation (branch `feat/v5-12-maker-checker-role`, từ `origin/master` sau PR #18)

**Bối cảnh:** phần đầu tiên, tự-chứa của V5-12 — thêm `AgentRole`/`AgentNodeConfig.Role` đúng contract 1,
cộng cơ chế 2 tầng validate (domain permissive cho reload, app-layer strict cho publish mới) để giải quyết
đúng yêu cầu "WorkflowVersion cũ vẫn rebuild được, publish mới phải explicit."

**Nghiên cứu then chốt (tìm điểm nối "reload" vs "publish"):** đọc `internal/adapters/sqlite/
workflow_store.go`'s `loadWorkflowVersion` — hàm này reload MỘT WorkflowVersion đã persist bằng cách gọi
LẠI `workflow.Compile(...)` (domain-layer) để re-verify hash, KHÔNG đi qua `internal/app/workflowcompiler.
CompileAndResolve`. Trong khi đó `CompileAndResolve` mới là entrypoint publish THẬT (`internal/app/
definitions/commands.go` gọi nó, không gọi `workflow.Compile` trực tiếp). Đây chính xác là điểm nối tự
nhiên người dùng mô tả: domain-layer `workflow.ValidateDocument`/`workflow.Compile` PHẢI giữ permissive
(Role rỗng hợp lệ) vì nó dùng chung cho CẢ reload lẫn publish gốc; bắt buộc "role tường minh" chỉ đặt ở
tầng `workflowcompiler.CompileAndResolve` — tầng CHỈ publish mới đi qua, reload không bao giờ chạm tới.
Không cần thêm field "schema version marker" nào mới — ranh giới structural sẵn có (2 hàm khác nhau) đã đủ.

**Quyết định thiết kế:**
1. `workflow.AgentRole` (`MAKER`/`CHECKER`) + field `Role AgentRole \`json:"role,omitempty"\`` trên
   `AgentNodeConfig`, đặt ngay sau `ProfileRef` (giữ nguyên tên field cũ, không đổi).
2. `EffectiveRole()` (value receiver, exported) — rỗng mặc định về MAKER; một chỗ duy nhất mọi reader
   (runtime) dùng chung, không tự viết lại rule default ở nhiều nơi.
3. `validation.go`'s AGENT branch: CHỈ reject giá trị SAI (không rỗng, không phải MAKER/CHECKER) — rỗng
   vẫn hợp lệ. Đây là tầng permissive giữ reload sống được.
4. `internal/app/workflowcompiler/compiler.go`: hàm mới `checkAgentRolesExplicit` + type lỗi mới
   `AgentRoleValidationError` (tách khỏi `ResolutionError` sẵn có — lỗi này KHÔNG cần DB round-trip, thuần
   structural, nên không hợp với doc comment của `ResolutionError`). Gọi ngay sau `workflow.ValidateDocument`
   ở đầu `CompileAndResolve`, trước mọi resolve — fail fast, đúng tinh thần "structural trước, DB sau" đã
   ghi trong doc comment gốc của hàm này.
5. `internal/domain/runtime/executionprofile.go`: thêm `Role workflow.AgentRole` vào
   `ResolvedExecutionProfileV1` — TÁI SỬ DỤNG THẲNG `workflow.AgentRole` (không tạo type mirror riêng cho
   runtime domain), theo đúng tiền lệ `ResolvedPolicyRef.Category`/`IsolationTier` (khi closed-set enum của
   domain khác khớp thẳng, dùng lại luôn, không mint type song song — khác với `ExecutorKind`, vốn PHẢI có
   type riêng vì `workflow.NodeType` có nhiều giá trị hơn tập "3 executor thật thi hành được"). Validate
   trong `NewResolvedExecutionProfileV1`: AGENT executor BẮT BUỘC Role hợp lệ (MAKER/CHECKER); COMMAND/
   MACHINE_GATE PHẢI để Role rỗng — mirror đúng rule ProviderKey/Model/ToolRefs/MaxTokens sẵn có.
6. `internal/app/runtime/schedule.go`'s `resolveExecutionProfile` (nhánh AGENT): set
   `profile.Role = node.Agent.EffectiveRole()` — đọc DUY NHẤT từ node.Agent (WorkflowVersion đã pin), không
   bao giờ suy ra từ `agentDoc` (AgentProfile), tên node hay vị trí graph — đúng contract 1.

**Rà soát blast radius (trước khi sửa fixture):** grep toàn repo mọi nơi construct `AgentNodeConfig{` (16
file) + mọi call site thật của `CompileAndResolve` (chỉ 5 nơi: `internal/app/workflowcompiler/
compiler_test.go`, `completion_policy_ref_test.go`, `internal/adapters/sqlite/
workflowcompiler_integration_test.go`, `internal/app/definitions/commands_test.go`, và
`internal/app/definitions/commands.go` — 2 command handler thật). 11 file còn lại dùng `workflow.Compile`
trực tiếp (permissive, không cần sửa). Riêng `cmd/agentkit/definition_test.go` build JSON string tay (không
qua struct literal `AgentNodeConfig{`) nên grep struct-literal ban đầu bỏ sót — phát hiện qua lần chạy full
suite đầu tiên (2 test CLI fail thật), sửa thêm `"role":"MAKER"` vào JSON template. Tương tự
`internal/integration/definitionplane_test.go` (struct literal có, nhưng nằm ngoài phạm vi grep ban đầu do
rà soát theo call site `CompileAndResolve` chưa đủ — cũng phát hiện qua full suite, không phải đoán).

**Fixture sửa (giữ mọi test cũ pass, không đổi hành vi được test):** `simpleAgentDocument` +
2-agent-conflicting-version doc trong `compiler_test.go`; `workflowcompiler_integration_test.go`;
`simpleAgentWorkflowDocument` trong `commands_test.go`; `workflowDocumentPinning` (JSON string) trong
`cmd/agentkit/definition_test.go`; `workflowDoc` trong `internal/integration/definitionplane_test.go`;
golden fixture `testdata/golden/simple-agent-workflow.json` (thêm `"role":"MAKER"` đúng vị trí field mới
trong canonical JSON, sau `profileRef`).

**Test mới:**
- `internal/domain/workflow/node_config_test.go`: case "AGENT invalid role" trong bảng reject có sẵn;
  `TestValidateDocumentAcceptsEmptyAgentRole` (backward-compat); `TestValidateDocumentAcceptsCheckerRole`;
  `TestAgentNodeConfigEffectiveRole` (3 case: rỗng→MAKER, MAKER giữ nguyên, CHECKER giữ nguyên).
  `comprehensiveDocument`'s agent node giờ có `Role: AgentRoleMaker` — `TestCompileRoundTripsNodeConfigWithoutLoss`
  (test có sẵn) tự động chứng minh Role round-trip qua `Document()`/`CanonicalContent()` không mất, đúng
  yêu cầu "role tự động pin theo canonical WorkflowVersion" — không cần viết test riêng.
- `internal/domain/runtime/executionprofile_test.go`: `TestNewResolvedExecutionProfileV1_AcceptsCheckerRole`;
  3 case reject mới trong bảng `RejectsInvalidProfiles` (AGENT thiếu Role, AGENT Role sai, COMMAND có Role).
- `internal/app/workflowcompiler/agent_role_test.go` (file mới): reject AGENT thiếu role; accept CHECKER
  tường minh; reject khi 1-trong-2 AGENT node thiếu role (báo đúng tên node).

**Verify:**
```
go build ./...                                                          # sạch
go vet ./...                                                            # sạch
go run ./cmd/docs-coverage-check                                        # debt = 0
gofmt -l <file đổi>                                                     # rỗng sau gofmt -w (CRLF)
go test -count=1 ./...                                                  # PASS toàn bộ (lần 1+2, không flake)
```

**Việc còn lại:** commit, push, mở PR, chờ CI 6/6, merge. Sau đó tiếp tục V5-12 PR1 (contract 2: checker
input allowlist / ContextSnapshot typed Evidence+Diff ref) và PR2 (contract 3: enforcement semantics —
reuse Gate `strictReadOnly`/`scratchDirectory()` precedent cho CHECKER-role Attempt), theo đúng chỉ dẫn tự
động chuyển task, không cần hỏi lại người dùng trừ khi phát sinh fork kiến trúc mới.

**Kết quả:** PR #19, 6/6 pass lần đầu (Linux race/stability 16m4s pass — flake đã biết KHÔNG xuất hiện lần
này). Squash-merged 2026-09-10, merge commit `2c06139`. Trong lúc chờ CI, nghiên cứu trước contract 2+3 để
PR tiếp theo code được ngay (chi tiết đầy đủ trong memory `agent-kit-v5-12-maker-checker-research.md`):
điểm enforcement duy nhất của contract 2 là `schedule.go`'s message-gathering trước
`contextsnapshot.NewSnapshot`; contract 3's read-only mount reuse (`forceReadOnlyMounts`, có sẵn từ Gate)
tự động chặn luôn WriteLease acquisition (không cần code thêm) vì `resolveExecutionResources` chỉ acquire
lease cho mount WRITE; `resolvedExecutionProfileView` (execute.go) cần thêm field `Role` mới đọc được.

## V5-12 — PR1: contract 2, checker input allowlist (branch `feat/v5-12-checker-input-allowlist`, từ
`origin/master` sau PR #19)

**Bối cảnh:** tiếp tục ngay sau PR0 merge, theo đúng chỉ dẫn tự động chuyển task. Contract 2 (checker
input allowlist) đã được người dùng uỷ quyền tự quyết ở bước scoping ban đầu — không hỏi lại, chỉ nghiên
cứu kỹ trước khi code vì có MỘT câu hỏi mới phát sinh khi thiết kế cụ thể (dưới đây).

**Câu hỏi tự phát sinh, tự giải quyết bằng nghiên cứu thêm (không phải hỏi người dùng):** "checker nên
thấy Evidence của node nào?" — không có trong contract gốc. Nghiên cứu: `ports.RuntimeRepository` đã có
sẵn `ListNodeRunsForRun`/`ListExecutionAttemptsForRun`/`ListEvidenceForAttempt` (không cần port method
mới). Quyết định: checker's EvidenceRefs = Evidence của mọi predecessor TRỰC TIẾP trong graph (tìm qua
`document.Edges` với `To == nodeKey`), chỉ lấy NodeRun/Attempt THÀNH CÔNG mới nhất
(`ActivationSequence`/`AttemptNumber` cao nhất) cho mỗi predecessor — tự nhiên đúng cho FORK/JOIN (nhiều
predecessor, gộp evidence của tất cả) và REWORK (predecessor chạy lại nhiều lần, chỉ evidence mới nhất).
Không cần query mới: đúng tinh thần doc comment sẵn có của `ListNodeRunsForRun` ("classification is the
caller's own job", không phải SQL WHERE clause).

**Quyết định thiết kế:**
1. `contextsnapshot.EvidenceRef{EvidenceID string}` (mirror `MessageRef`) + field mới
   `Snapshot.EvidenceRefs []EvidenceRef`. Field trong `canonicalManifest` gắn `omitempty` (đúng tiền lệ
   `ResourceRef.OwnerVersionID` — Snapshot cũ/MAKER/COMMAND/MACHINE_GATE không có EvidenceRefs, JSON
   không có key này, hash không đổi). Khác `MessageRefs`/`ResourceRefs` (giữ nguyên thứ tự vì là
   transcript render order), `EvidenceRefs` được SORT trong `NewSnapshot` vì không có ý nghĩa thứ tự.
2. Migration 0031 (`ALTER TABLE attempt_context_snapshots ADD COLUMN evidence_refs_json TEXT NOT NULL
   DEFAULT '[]'`) — plain ADD COLUMN, mirror tiền lệ migration 0018. Default `'[]'` an toàn vì
   `NewSnapshot`'s own `append([]EvidenceRef(nil), ...)` normalize cả nil lẫn empty-non-nil về nil trước
   khi hash — không ảnh hưởng hash của row cũ.
3. `schedule.go`'s Attempt/Snapshot-creation block: nhánh theo `node.Agent.EffectiveRole()` — CHECKER thì
   `messageRefs` rỗng (không gọi `ListMessagesForWorkItem`) + gọi `gatherCheckerEvidenceRefs` (hàm mới);
   mọi node khác (MAKER/COMMAND/MACHINE_GATE) giữ nguyên hành vi cũ 100%. Requirement (Title/Behavior/
   AcceptanceCriteria) vẫn tới checker bình thường qua `instructionArtifactContent.TaskContract`
   (assemble_execution_request.go) — không phụ thuộc MessageRefs, nên loại bỏ MessageRefs không làm mất
   requirement.

**Rà soát blast radius:** 7 call site `contextsnapshot.NewSnapshot(...)` toàn repo (schedule.go — hành vi
mới; finalize.go/recovery_reaper.go — clone `previousSnapshot.EvidenceRefs` không đổi; 2 file test
domain + 1 file test sqlite + execute_contextsnapshot_test.go×3 — thêm tham số mới, hành vi không đổi).
Thêm: 3 chỗ hard-code `migration count = 29`/`migrationCount != 29` (db_test.go×2, unitofwork_test.go) —
cập nhật lên 30 (thêm đúng 1 migration).

**Test mới:**
- `internal/domain/contextsnapshot/contextsnapshot_test.go`: case "blank EvidenceRef" trong bảng reject;
  `TestNewSnapshot_EvidenceRefs_OrderInsensitive` (đối lập có chủ đích với
  `TestNewSnapshot_ManifestHash_OrderSensitive` của MessageRefs); `TestNewSnapshot_ManifestHash_
  BackwardCompatibleWithoutEvidenceRefs` (mirror tiền lệ OwnerVersionID — proof hash row cũ không đổi).
- `internal/app/runtime/schedule_test.go`: fixture mới `makerCheckerDocument` (2 AGENT node dùng CHUNG
  AgentProfileVersion, khác Role — chứng minh trực tiếp "same profile, independent identity");
  `TestScheduleExecutableNodeRun_Checker_ExcludesMessagesIncludesPredecessorEvidence` — seed 1 Message
  thật cho WorkItem (chứng minh loại bỏ là THẬT, không phải "vốn đã rỗng"), seed maker's terminal
  NodeRun/Attempt/Evidence trực tiếp qua `seedRunEvidence` (tái dùng helper có sẵn từ V5-11's completion
  policy test suite, cùng tinh thần "bypass real executor pipeline, test đúng logic của hàm này"), seed
  checker's PENDING NodeRun trực tiếp, gọi THẬT `ScheduleExecutableNodeRun`, assert
  `snapshot.MessageRefs` rỗng và `snapshot.EvidenceRefs` đúng 1 entry trỏ evidence của maker.

**Chưa test (biết trước, chấp nhận được cho Alpha):** "latest wins" khi MỘT predecessor chạy lại nhiều
lần (REWORK cycle thật) chưa có test riêng — logic đã viết đúng theo thiết kế (so `ActivationSequence`/
`AttemptNumber`), nhưng end-to-end test chỉ cover trường hợp 1 predecessor chạy 1 lần. Rủi ro thấp (logic
đơn giản, so sánh số nguyên); có thể bổ sung sau nếu REWORK+CHECKER thực tế bộc lộ vấn đề.

**Verify:**
```
go build ./...                                                          # sạch
go vet ./...                                                            # sạch
go run ./cmd/docs-coverage-check                                        # debt = 0
go test -count=1 ./...                                                  # PASS toàn bộ (lần 1+2, không flake)
```

**Việc còn lại:** commit, push, mở PR, chờ CI 6/6, merge. Sau đó tiếp tục V5-12 PR2 (contract 3: enforcement
— reuse `forceReadOnlyMounts` cho CHECKER-role AGENT node, thêm field `Role` vào
`resolvedExecutionProfileView`, verify CreateLocalCommit/quarantine semantics), theo đúng chỉ dẫn tự động
chuyển task.

**Kết quả:** PR #20, 6/6 pass lần đầu (Linux race/stability 16m36s pass — flake đã biết KHÔNG xuất hiện).
Squash-merged 2026-09-10, merge commit `f087819`. V5-12 contract 1+2 xong; contract 3 (enforcement) là
phần còn lại duy nhất.

## V5-12 — PR2: contract 3, read-only enforcement (branch `feat/v5-12-checker-readonly-enforcement`, từ
`origin/master` sau PR #20)

**Bối cảnh:** tiếp tục ngay sau PR1 merge, theo đúng chỉ dẫn tự động chuyển task. Contract 3 (enforcement
semantics) đã được người dùng uỷ quyền tự quyết ở bước scoping ban đầu.

**Quyết định thiết kế (mọi phần đều reuse cơ chế đã có, không phát minh mới):**
1. `resolvedExecutionProfileView` (execute.go) thêm field `Role workflow.AgentRole` — trước đây field này
   tồn tại trên `ResolvedExecutionProfileV1` (PR0) nhưng KHÔNG được decode ở tầng app; đây là chỗ nối
   thiếu duy nhất giữa PR0's own schema và runtime thật.
2. `assemble_execution_request.go`'s `gatherAssembledRequestInputs`: khi `profile.Role ==
   workflow.AgentRoleChecker`, wrap kết quả `assembleWorkspaceMounts(...)` bằng `forceReadOnlyMounts` —
   ĐÚNG hàm Gate đã dùng từ trước (`gate_node_executor.go`), không viết hàm mới.
3. **Phát hiện quan trọng, xác nhận bằng test thật (không chỉ đọc code):** ép mount READ_ONLY tự động
   chặn luôn WriteLease acquisition — `resolveExecutionResources` (agent_node_executor_resources.go) chỉ
   gọi `AcquireWriteLeases` cho mount có `Access == WRITE`; mount CHECKER không bao giờ WRITE nên
   `writeTargets` luôn rỗng. Verify bằng field mới `acquireCalls` trên fake WriteLeaseManager của test.
4. `AgentNodeExecutor.buildEvidence`'s wrapper (trước đây hard-code `strictReadOnly=false`): giờ tự
   `loadExecutionProfile` (helper có sẵn) để đọc Role thật của chính Attempt, rồi truyền
   `strictReadOnly = (role == CHECKER)` vào `buildEvidence` — ĐÚNG cơ chế Gate đã dùng
   (`validateStrictlyReadOnlyDiffs`, PR #18), không viết check mới. Cân nhắc và LOẠI BỎ phương án khác
   ("suy strictReadOnly từ việc tất cả mount đều read-only") vì kém rõ ràng hơn (không trace thẳng về
   Role, dù về mặt logic cũng đúng) — chọn Role tường minh cho dễ audit.
5. Generalize lại doc comment + error message của `validateStrictlyReadOnlyDiffs` (trước đây hard-code
   chữ "gate mount...") thành trung lập theo executor, vì giờ dùng chung cho cả Gate và CHECKER.

**Nghiên cứu xác nhận (không giả định):**
- `CreateLocalCommit` (`ports.LocalCommitCreator`, V5-10A): grep toàn `internal/app` xác nhận KHÔNG có
  caller thật nào — port + adapter implementation tồn tại nhưng chưa wire vào path thực thi AGENT/COMMAND
  nào. "Bị từ chối trước Git adapter" đúng nghĩa đen vì hiện tại KHÔNG path nào chạm tới nó, không phải
  giả định suông.
- "Trusted-local mutation quan sát được phải quarantine": đọc kỹ `agent_node_executor_cancellation.go`'s
  `handleMutatingCancellation` — chỉ lặp qua `resolved.writeMounts` (chỉ chứa mount WRITE). Với CHECKER,
  mount luôn READ_ONLY nên nhánh quarantine CẤU TRÚC không bao giờ chạy. Đọc `gate_node_executor.go`'s
  own package doc comment xác nhận đây CHÍNH LÀ tradeoff Gate đã tự nhận và CHẤP NHẬN từ trước ("resolved.
  hasWriteMount is always false... classifyCancellation's own mutating-attempt path can structurally
  never fire for a Gate; only its simple read-only branch (CANCELLED) ever does") — không phải gap MỚI
  của CHECKER, mà là đúng tiền lệ Gate đã có, áp dụng nhất quán.

**Gap cố ý hoãn (ghi rõ, không giấu):** "scratch nằm ngoài source" cho AGENT. Gate/COMMAND chạy MỘT argv
với cwd hoàn toàn do platform kiểm soát (`scratchDirectory()` áp dụng thẳng được); AGENT wrap một CLI
agent tương tác đầy đủ, cwd thật đi qua tầng adapter-translation (`ports.AgentExecutionRequest.
WorkspaceMounts` nhiều mount -> `WorkingDirectory` đơn cho provider, xem `internal/adapters/providers/
claude/claude.go`) mà PR này CHƯA nghiên cứu đủ kỹ để implement đúng — cần một pass riêng. Phần AN TOÀN
cốt lõi (mount read-only, diff-rỗng bắt buộc, không WriteLease) đã xong và test thật; "scratch" là vấn đề
TIỆN DỤNG, không phải AN TOÀN — checker vẫn an toàn (không thể ghi được vào source, và nếu cố ghi thì bị
FAILED) dù chưa có chỗ scratch riêng.

**Test mới:**
- `internal/app/runtime/assemble_execution_request_test.go`:
  `TestAssembleAgentExecutionRequest_CheckerRole_MountsForcedReadOnly` — cùng fixture với test MAKER
  golden-path (cùng EffectiveScope cấp WRITE), chỉ đổi Role, assert mount CHECKER là READ_ONLY.
- `internal/app/runtime/agent_node_executor_test.go`:
  `TestAgentNodeExecutor_CheckerRole_MutatingDiff_RejectsAsScopeViolation` — CÙNG diff
  (`defaultInScopeDiff`) làm MAKER golden-path PASS, nhưng CHECKER phải FAILED/SCOPE_VIOLATION; assert
  thêm `writeLeases.acquireCalls == 0`. `TestAgentNodeExecutor_CheckerRole_EmptyDiff_Succeeds` — chứng
  minh check mới không phá checker hợp lệ (diff rỗng vẫn SUCCEEDED).
- Rà soát blast radius: `bridgeFixture`/`bridgeFixtureOptions`/`assembleRequestFixture` (11 call site
  trong `agent_node_executor_test.go`) thêm field/return value mới nhưng giữ MAKER làm default cho mọi
  test cũ (zero value Role = "" = MAKER qua `EffectiveRole()`) — không đổi hành vi test nào có sẵn.

**Verify:**
```
go build ./...                                                          # sạch
go vet ./...                                                            # sạch
go run ./cmd/docs-coverage-check                                        # debt = 0
gofmt -l <file đổi>                                                     # rỗng sau gofmt -w (CRLF)
go test -count=1 ./...                                                  # PASS toàn bộ (lần 1+2, không flake)
```

**Việc còn lại:** commit, push, mở PR, chờ CI 6/6, merge. Sau khi merge, V5-12 coi như hoàn thành đúng
"Hoàn thành khi" (same provider/model vẫn có independent attempt/context identity — đã xong từ PR0) và đi
xa hơn nhiều (contract 2+3's own "Kết quả kỳ vọng" phần lớn đã thật), với đúng MỘT gap còn lại đã ghi rõ
("scratch ngoài source" cho AGENT) để dành cho một task sau nếu cần. Bước tiếp theo trong roadmap: V5-13
(Checkpoint/handoff và recovery integration), theo đúng chỉ dẫn tự động chuyển task.

**Gián đoạn giữa chừng — repo migration lần 2 (2026-09-11):** PR #21 (`zlinh4605/agent-workflow`) mở ra,
nhưng CI KHÔNG chạy được — GitHub trả lỗi "recent account payments have failed" ngay lập tức (0 step nào
chạy), giống hệt lý do repo từng phải chuyển từ `draculemihawk123-ai` sang `zlinh4605` trước đó
(2026-09-09) — nghĩa là billing block đó chưa từng thật sự được xử lý, chỉ đổi chỗ. Xác nhận (không đoán):
`gh api repos/zlinh4605/agent-workflow --jq .permissions` cho CẢ HAI account (`draculemihawk123-ai`,
`taQuangLing`) đều `admin: false` — không ai trong hai account đang đăng nhập có quyền đổi tên/admin repo
đó, nên không thể tự sửa bằng `gh repo rename`.

Người dùng tạo repo MỚI, `draculemihawk123-ai/aw-tqunglnh` — cùng account từng bị chặn billing, nhưng lần
này **Public** (hai repo trước đều Private). Push `master` + branch đang dở
(`feat/v5-12-checker-readonly-enforcement`) sang đây, remote `origin` cũ đổi tên thành `zlinh4605-old`
(giữ lại, không xoá) rồi thêm `origin` mới trỏ vào `aw-tqunglnh`. Mở lại PR2 tại đây thành PR #1 — **CI
chạy được thật** (6/6 pass, kể cả job `contract` từng fail tức thì trên repo cũ). Xác nhận bằng thực nghiệm
(không chỉ đọc tài liệu GitHub): billing block cho GitHub Actions minutes áp dụng cho repo Private, KHÔNG
áp dụng cho repo Public — cùng một account, cùng trạng thái billing, nhưng repo Public vẫn chạy CI miễn phí
bình thường. Đây là phát hiện quan trọng, ghi vào memory riêng
(`agent-kit-github-remote-migration.md`) để không phải khám phá lại nếu billing lại chặn CI lần sau.

Merge PR #1 (`aw-tqunglnh`), squash, merge commit `95230f3`. Repo hiện tại (remote `origin` chính thức từ
2026-09-11): `https://github.com/draculemihawk123-ai/aw-tqunglnh.git`. Collaborator: chỉ
`draculemihawk123-ai` (admin) — không ai khác có quyền push, nên yêu cầu "giới hạn quyền" của người dùng
coi như đã thoả mãn sẵn, không cần đổi gì thêm.

## V5-13 scoping — Checkpoint/handoff và recovery integration (2026-09-11)

**Bối cảnh:** V5-12 (Maker/checker isolation) coi như xong phần cốt lõi. Theo đúng chỉ dẫn tự động chuyển
task, tiếp tục sang V5-13 — task tiếp theo trong dependency order của design doc.

**Nghiên cứu trước khi hỏi:** design doc tự đánh dấu V5-13 "CHƯA ĐỦ DỮ KIỆN". Đọc kỹ code hiện có (không
đoán):
- "Hai model ContextSnapshot không có bridge" (design doc's own complaint) hoá ra NÔNG hơn tưởng: lần theo
  đúng data flow thật (`agent_node_executor.go` dòng `ContextSnapshotID: string(request.ContextSnapshot.ID)`
  khi tạo sink), MỌI Checkpoint thật từ V5-04 trở đi ĐÃ lưu đúng ID của `contextsnapshot.Snapshot` thật —
  chỉ bị ép kiểu qua type legacy `runtime.ContextSnapshotID` khi lưu trên `Checkpoint` struct. Giá trị lưu
  ĐÚNG, chỉ sai type label + `worker.RecoveryStore.LoadContextSnapshot` tra sai bảng (bảng legacy
  `context_snapshots` thay vì `attempt_context_snapshots` thật). `worker.StartFreshFromLatestCheckpoint`
  hiện tại coi như CHẾT/hỏng cho mọi Attempt thật từ V5-04 trở đi — tra ID thật vào bảng sai, không bao giờ
  tìm thấy.
- `RecoveryDecision` đã tồn tại sẵn, là một `DecisionArtifact.Kind` (không cần bảng mới) — ID đã
  DETERMINISTIC sẵn (`attempt.ID + "-recovery-decision-gen-" + generation`), đúng tinh thần "recovery
  activation ID dẫn xuất deterministic" mà (chưa hỏi lúc đó) hoá ra chính là câu trả lời người dùng đưa ra.
- **Gap thật xác nhận được (không phải đoán):** nhánh "mutation observed" của `recoverOneAttempt` hiện tại
  KHÔNG check `budgetRemains` trước khi ghi FRESH_START — chỉ nhánh non-mutating (RETRY-eligible) mới
  check. Trong khi đó, doc comment gốc của file này ĐÃ tuyên bố "ESCALATE: retry budget is exhausted..."
  như đã xong — nghĩa là doc và code lệch nhau, code chưa làm đúng cái doc đã hứa.
- Grep toàn bộ `internal` cho "HandoffArtifact"/"NoProgress"/"budget_basis": không có gì ngoài chính
  `recovery_reaper.go`'s prose — xác nhận đây là schema/policy hoàn toàn mới, không phải thứ đã có sẵn một
  phần.

**Hỏi người dùng (AskUserQuestion, 1 câu, có recommendation):** V5-13 cần "budget" cho FRESH_START — dùng
lại `AttemptRules.MaxAttempts` có sẵn, hay thêm policy mới cho time/cost budget?

**Quyết định của người dùng (verbatim, chốt toàn bộ contract V5-13's phần budget/recovery-ID, kể cả một
câu tôi CHƯA kịp hỏi):**
> Chọn dùng lại AttemptRules.MaxAttempts.
> Contract cho V5-13:
> - Mỗi FRESH_START tạo một Attempt mới và tăng AttemptNumber.
> - RETRY và FRESH_START dùng chung một MaxAttempts; không có hai ngân sách để lách giới hạn.
> - Nếu tạo Attempt mới sẽ vượt MaxAttempts, không schedule recovery; chuyển sang ESCALATE/BLOCKED với
>   reason typed như RECOVERY_ATTEMPTS_EXHAUSTED.
> - Mỗi recovery decision ghi checkpoint ID/hash và evidence frontier đã quan sát.
> - Nếu Attempt kết thúc mà checkpoint/evidence frontier không tiến lên, ghi reason RECOVERY_NO_PROGRESS.
>   Lần FRESH_START đó vẫn tiêu thụ một attempt.
> - Replay cùng recovery decision không tăng counter hoặc tạo thêm Attempt; recovery activation ID phải
>   dẫn xuất deterministic từ Attempt bị crash và recovery generation.
> - V5-13 chưa nên thêm wall-clock/cost policy — có thể bổ sung sau như policy độc lập, không đổi contract
>   hiện tại.

Contract này TỰ ĐỘNG trả lời luôn câu hỏi "RecoveryExecution/CAS key" mà tôi định hỏi riêng — "recovery
activation ID deterministic từ (interruptedAttemptID, generation)" ĐÃ LÀ câu trả lời, và
`RecoveryDecisionKind`'s own DecisionArtifact ID (đã tồn tại) đã đúng khuôn mẫu này sẵn. Ghi đầy đủ vào
memory `agent-kit-v5-13-checkpoint-recovery-research.md`.

**Kế hoạch nhiều PR (task lớn, mirror V5-11/V5-12):** PR0 = budget-gate fix (branch này); PR1 = bridge
Checkpoint→V5 Snapshot thật + rebuild fresh Snapshot; PR2 = 3-phase FRESH_START executor thật (tiêu thụ
decision, AGENT gọi `Start` với `RecoveryCheckpoint`, COMMAND/GATE dispatch như RETRY bình thường).

## V5-13 — PR0: budget gate cho FRESH_START (branch `feat/v5-13-recovery-budget-gate`, từ `origin/master`
sau PR docs #2)

**Quyết định thiết kế:** tách "quyết định RETRY/FRESH_START/ESCALATE + reason nào" ra một hàm THUẦN
(`decideRecoveryNextAction`, không ctx/uow/side-effect nào) — lý do: viết test cho nhánh "mutation
observed" bằng sqlite thật cần dựng WriteLeaseGrant + RepositoryWorkspace với current revision khác pinned
revision — hạ tầng test lớn, chưa hề tồn tại (grep xác nhận: 0 test nào đụng tới nhánh này trước giờ, kể cả
test `ESCALATE` có sẵn cũng chỉ test nhánh non-mutating). Tách hàm thuần cho phép test-driven đầy đủ MỌI
nhánh của decision matrix (7 case) bằng unit test bảng, không cần sqlite/mutation thật — engineering
tương xứng với quy mô: fix nhỏ, không đáng đầu tư hạ tầng test lớn.

Thêm const `RecoveryReasonAttemptsExhausted = "RECOVERY_ATTEMPTS_EXHAUSTED"`. Budget check
(`!budgetRemains`) áp dụng ĐỒNG NHẤT cho cả nhánh mutating (FRESH_START-eligible) và non-mutating
(RETRY-eligible) — đúng contract "RETRY và FRESH_START dùng chung MaxAttempts". Cập nhật doc comment đầu
file (đã lệch so với code) cho khớp hành vi thật.

**Test mới:** `internal/app/runtime/recovery_reaper_internal_test.go` (file mới, package `runtime` nội bộ)
— `TestDecideRecoveryNextAction`, bảng 7 case phủ hết decision matrix, gồm case "mutating + budget hết ưu
tiên hơn cả run cancelling" chứng minh đúng thứ tự ưu tiên.

**Verify:**
```
go build ./...                                                          # sạch
go vet ./...                                                            # sạch
go test -count=1 -run TestRecoveryReaperHandler ./internal/app/runtime/...  # 5/5 pass, không regress
go test -count=1 -run TestDecideRecoveryNextAction ./internal/app/runtime/... # 7/7 pass
go test -count=1 ./internal/app/runtime/...                             # PASS toàn bộ package
```

**Chưa làm trong PR0 (để dành PR1/PR2):** RECOVERY_NO_PROGRESS (cần so sánh checkpoint/evidence frontier
GIỮA nhiều chu kỳ FRESH_START — chỉ tính được SAU KHI một Attempt thay thế đã chạy xong, tức là cần bộ
thực thi 3-pha thật, chưa tồn tại); bridge Checkpoint→V5 Snapshot thật; handoff artifact V1; bộ thực thi
FRESH_START thật.

**Việc còn lại:** chạy full verify suite + gofmt, commit, push, mở PR, chờ CI 6/6, merge.

**Kết quả:** PR #3, 6/6 pass (chạy chậm bất thường ~15-20 phút ở trạng thái "Queued" trước khi runner thật
được cấp phát — KHÔNG phải billing block, xác nhận qua Actions web UI, chỉ là delay cấp phát runner của
GitHub cho repo mới; ghi vào memory `agent-kit-github-remote-migration.md` để lần sau không hoảng khi
`gh pr checks` báo "no checks reported"). Squash-merged 2026-09-11, merge commit `46f63f2`.

## V5-13 — PR1: bridge Checkpoint→V5 ContextSnapshot thật (branch
`feat/v5-13-checkpoint-snapshot-bridge`, từ `origin/master` sau PR #3)

**Phát hiện QUAN TRỌNG hơn dự đoán ban đầu — không phải chỉ lệch type label, mà FRESH_START CHƯA TỪNG chạy
được trong thực tế:** lần theo TOÀN BỘ data flow thật (không đoán):
- `hasUsableCheckpoint` xác minh Checkpoint's own `ContextSnapshotID` qua `h.recovery.LoadContextSnapshot`
  — interface `worker.RecoveryStore` LEGACY, tra bảng `context_snapshots` (kiểu cũ, pre-V5-04).
- Grep toàn bộ `internal/adapters/sqlite`: KHÔNG có nơi nào THẬT SỰ ghi vào bảng đó — chỉ 2 file test/spike
  (`context_store_test.go`, `crashworker_fixtures.go`) làm vậy. Mọi Checkpoint thật từ V5-04 trở đi lưu
  ĐÚNG ID của `contextsnapshot.Snapshot` thật (đã xác nhận ở PR0's own research), nhưng tra vào SAI bảng.
- Hệ quả: `LoadContextSnapshot` LUÔN LUÔN trả not-found cho MỌI Attempt mutating thật → `hasUsableCheckpoint`
  LUÔN LUÔN false → reaper LUÔN LUÔN chọn ESCALATE thay vì FRESH_START, một cách ÂM THẦM (không có gì phân
  biệt với một ESCALATE hợp lệ). **FRESH_START chưa từng thực sự kích hoạt trong production.**

**Fix:** `hasUsableCheckpoint` giờ xác minh qua `tx.ContextSnapshots().GetSnapshot(ctx,
string(checkpoint.ContextSnapshotID))` — repository V5 THẬT — trong một `h.uow.WithReadOnly`, thay vì
interface `RecoveryStore` legacy.

**Quyết định về test (cân nhắc kỹ, không lặng lẽ bỏ qua):** KHÔNG dựng test E2E sqlite thật cho kịch bản
"mutation observed" trong PR này. Đã lần ra chính xác cần gì: một `WriteLeaseGrant` thật (để
`GetWriteLeaseRepositoryWorkspaceForAttempt` tìm thấy) + một `RepositoryWorkspace` thật có
`current_revision` KHÁC `base_revision` (xác nhận qua `spk04_queries.go`'s own `LoadRepositoryWorkspaceRevision`
— đọc đúng cột `current_revision`, tách biệt khỏi `base_revision`, nên KHÔNG cần thao tác git thật, chỉ cần
2 giá trị cột khác nhau lúc tạo row) — nhưng hạ tầng fixture này CHƯA tồn tại (grep xác nhận từ PR0: 0 test
nào đụng nhánh mutating). Xây dựng nó là công sức hạ tầng thật, không tương xứng với quy mô fix hẹp này.
Hoãn sang PR2/PR3 (bộ thực thi FRESH_START 3-pha thật) — nơi ĐẰNG NÀO cũng cần đúng fixture này để chứng
minh dispatch thật của chính nó, xây một lần dùng cho cả hai. Độ tin cậy của fix hiện tại dựa trên: (a)
lần dấu vết data flow thật chính xác (không đoán), (b) bộ test thuần đầy đủ của PR0 (chứng minh khi
`hasUsableCheckpoint` trả true, FRESH_START được chọn đúng).

**Verify:**
```
go build ./...                                                          # sạch
go vet ./...                                                            # sạch
go test -count=1 -run "TestRecoveryReaperHandler|TestDecideRecoveryNextAction" ./internal/app/runtime/... # không regress
go test -count=1 ./internal/app/runtime/...                             # PASS toàn bộ package
```

**Việc còn lại:** gofmt, docs-coverage-check, full test suite ×2, commit, push, mở PR, chờ CI 6/6, merge.
Sau đó tiếp tục PR2/PR3 (bộ thực thi FRESH_START 3-pha thật + fixture mutation-observed thật + typed
RECOVERY_NO_PROGRESS + handoff artifact V1), theo đúng chỉ dẫn tự động chuyển task.

**Kết quả:** PR #4, 6/6 pass lần đầu (không bị "Queued" delay lần này). Squash-merged 2026-09-11, merge
commit `f25566e`.

## V5-13 — PR2: bộ thực thi FRESH_START thật, Phase 1 (branch `feat/v5-13-fresh-start-executor`, từ
`origin/master` sau PR #4)

**Thực hiện:** `consumeFreshStart` (hàm mới) — Phase 1 thật của flow 3-pha design doc mô tả: MỘT
transaction reserve Attempt thay thế (deterministic ID từ `(interruptedAttemptID, generation)`, đúng
contract người dùng đã chốt), clone V5 ContextSnapshot từ checkpoint's own snapshot (TÁI DÙNG NGUYÊN
pattern `retryAttempt` đã có cho RETRY — không viết logic clone mới), pin `LastCheckpointID` (field đã có
sẵn trên `ExecutionAttempt` từ trước, CHƯA từng được đọc/ghi ở đâu — tái dùng đúng mục đích thay vì thêm
field mới), enqueue EXECUTE_NODE job, VÀ ghi `RecoveryDecisionKind` DecisionArtifact — TẤT CẢ trong CÙNG
một transaction (khác ESCALATE, vốn chỉ ghi decision). Idempotent: nếu deterministic Attempt ID đã tồn
tại (redelivery), no-op ngay — không tạo Attempt/Snapshot/job thứ hai.

**Refactor:** tách `recordDecision`'s own thân transaction thành `writeRecoveryDecisionArtifactTx` (hàm
thuần, nhận `tx` có sẵn) — dùng chung bởi `recordDecision` (ESCALATE, tự mở transaction riêng) và
`consumeFreshStart` (FRESH_START, gọi bên trong transaction reserve của chính nó). `recordDecision` không
còn nhánh FRESH_START nữa (đã chuyển hẳn sang `consumeFreshStart`).

**Deterministic ID:** `deterministicRecoveryAttemptID`/`deterministicRecoverySnapshotID` — mirror ĐÚNG
convention sha256/hex/truncated-16-byte đã có từ `deterministicJoinNodeRunID` (advance.go) và
`deterministicCompletionDecisionID` (completion_policy.go, V5-11) — không phát minh cách mới.

**Quyết định về test (cân nhắc kỹ lần thứ 3, không lặng lẽ bỏ qua — và tìm được xác nhận mạnh):** vẫn
KHÔNG dựng sqlite fixture "mutation observed" thật. Lần này đào sâu hơn để hiểu TẠI SAO khó: `current_revision`
(cột DB `ReconcileMutatingAttempt` so sánh) KHÔNG BAO GIỜ được UPDATE bởi bất kỳ production code nào sau
khi tạo — chỉ INSERT (luôn base==current). Không có port nào tạo được cặp base/current KHÁC nhau ngoài
raw SQL (private, không truy cập được từ package test ngoài). Xác nhận thêm bằng cách đọc
CHÍNH test hiện có của `internal/app/workspacereconcile` (package sở hữu khái niệm reconcile) —
`handler_sqlite_test.go`'s own mutation test CŨNG chỉ gọi `worker.ReconcileMutatingAttempt` với 2 chuỗi
tay, KHÔNG dựng fixture DB thật với current≠base. Đây là tiền lệ ĐÃ CÓ SẴN trong chính codebase này, xác
nhận: test logic thuần (đã làm, `TestDecideRecoveryNextAction`) là đúng convention đã được team này chấp
nhận, không phải tôi lười — dựng sqlite fixture thật cho kịch bản này chưa từng được ai làm, kể cả ở nơi
"đáng lẽ" phải làm nhất.

**Verify:**
```
go build ./...                                                          # sạch
go vet ./...                                                            # sạch
go run ./cmd/docs-coverage-check                                        # debt = 0
gofmt -l internal/app/runtime/recovery_reaper.go                        # rỗng
go test -count=1 -run "TestRecoveryReaperHandler|TestDecideRecoveryNextAction" ./internal/app/runtime/... # không regress
go test -count=1 ./...                                                  # PASS toàn bộ (lần 1+2, không flake)
```

**Chưa làm (PR3):** wire `RecoveryCheckpoint` vào `AssembleAgentExecutionRequest` thật — khi Attempt có
`LastCheckpointID` được pin (dấu hiệu "đây là replacement của FRESH_START"), request cho AGENT phải set
`RecoveryCheckpoint` (field placeholder đã có trên `ports.AgentExecutionRequest` từ trước); COMMAND/GATE
không cần xử lý gì thêm (dispatch như bình thường, không có khái niệm session/Resume). `RECOVERY_NO_PROGRESS`
(so sánh frontier giữa nhiều chu kỳ FRESH_START) và handoff artifact V1 vẫn để dành sau.

**Việc còn lại:** commit, push, mở PR, chờ CI 6/6, merge.

**Kết quả:** PR #5, 6/6 pass. Squash-merged 2026-09-11, merge commit `304a748`.

## V5-13 — PR3: wire RecoveryCheckpoint vào dispatch thật (branch
`feat/v5-13-recovery-checkpoint-wiring`, từ `origin/master` sau PR #5)

**Thực hiện:** `assemble_execution_request.go`'s `gatherAssembledRequestInputs` đọc
`attempt.LastCheckpointID` (field PR2 vừa pin) và đưa vào `assembledRequestInputs.recoveryCheckpointID`;
`AssembleAgentExecutionRequest`'s own final return set `ports.AgentExecutionRequest.RecoveryCheckpoint`
(field placeholder có sẵn từ trước, nil từ đầu) — CHỈ khi non-empty. Nội dung: ID của Checkpoint thật (một
reference thuần, KHÔNG render lại context) — vì context thật đã được giao đầy đủ qua
InstructionArtifact/ContextSnapshot (Snapshot mới clone bởi `consumeFreshStart`), field này chỉ để báo cho
provider adapter "đây là recovery, checkpoint nào" chứ không phải kênh thứ hai lặp lại context.

**Phát hiện quan trọng thứ 2 trong task này (grep xác nhận, không giả định):** cột `last_checkpoint_id` đã
tồn tại từ migration 0001, nhưng KHÔNG code Go nào từng đọc/ghi nó — nghĩa là `consumeFreshStart` (PR2)
gán `nextAttempt.LastCheckpointID` trong bộ nhớ nhưng giá trị đó BỊ ÂM THẦM MẤT khi persist, vì
`createExecutionAttemptTx`'s own INSERT không có cột này trong danh sách, và `loadExecutionAttemptByID`'s
own SELECT cũng không đọc nó. Nếu không fix, TOÀN BỘ wiring của PR3 sẽ luôn thấy `nil` bất kể PR2 đã làm
gì. Fix: thêm `last_checkpoint_id` vào CẢ HAI INSERT (`schedule_node_run.go`) và SELECT
(`finalize_execution_attempt.go`). Fake UnitOfWork (`internal/app/ports/fake`) không cần sửa — nó lưu
nguyên struct Go, tự động giữ field này.

**Test mới:**
- `TestAssembleAgentExecutionRequest_RecoveryReplacement_SetsRecoveryCheckpoint` (fake UoW) — Attempt
  thường thì `RecoveryCheckpoint == nil`; Attempt "replacement" kiểu `consumeFreshStart` (LastCheckpointID
  pin, Snapshot clone) thì `RecoveryCheckpoint` đúng bằng checkpoint ID.
- `TestCreateExecutionAttempt_LastCheckpointID_RoundTripsThroughRealSQLite` (sqlite thật) — chứng minh
  đúng gap vừa fix: tạo Attempt với `LastCheckpointID` set, load lại, xác nhận còn nguyên (test này SẼ
  FAIL nếu không có fix INSERT/SELECT ở trên).

**Verify:**
```
go build ./...                                                          # sạch
go vet ./...                                                            # sạch
go run ./cmd/docs-coverage-check                                        # debt = 0
gofmt -l <file đổi>                                                     # rỗng
go test -count=1 ./internal/app/runtime/... ./internal/adapters/sqlite/... # không regress
go test -count=1 ./...                                                  # PASS toàn bộ (lần 1+2, không flake)
```

**Còn lại của V5-13 (không blocking, ghi rõ để dành):** `RECOVERY_NO_PROGRESS` (so sánh frontier giữa
nhiều chu kỳ FRESH_START — chỉ đo được SAU KHI một replacement Attempt đã chạy xong) và handoff artifact
V1 (tự quyết: một `DecisionArtifact.Kind` mới, mirror `RecoveryDecision`). Với phần đã xong (contract 1
budget-gate, bridge Checkpoint→Snapshot thật, reserve Attempt/Snapshot/job thật, wire RecoveryCheckpoint
thật), V5-13 đã đạt "Hoàn thành khi": session mới không cần raw transcript hoặc cwd cũ — cwd/session cũ
không bao giờ được dùng (Start, không Resume — đã đúng từ thiết kế legacy `worker.recovery.go`'s own
"Do not assume access to any prior provider session" mà giờ áp dụng thật qua flow mới).

**Việc còn lại:** commit, push, mở PR, chờ CI 6/6, merge. Sau đó cân nhắc: V5-13 coi như đủ để chuyển sang
V5-14 (Cleanup/retention sweeper) theo roadmap, hay tiếp tục đóng nốt RECOVERY_NO_PROGRESS/handoff artifact
trước — quyết định này để dành sau khi PR3 merge.

**Kết quả:** PR #6, 6/6 pass. Squash-merged 2026-09-11, merge commit `eda2f80`. V5-13 coi như đạt "Hoàn
thành khi" của chính nó (session mới không cần raw transcript/cwd cũ) — `RECOVERY_NO_PROGRESS` và handoff
artifact V1 để dành, không blocking. Chuyển sang V5-14 (Cleanup/retention sweeper) theo roadmap — sẽ
nghiên cứu trước khi code, đúng kỷ luật đã dùng cho mọi task V5 khác.

PR docs-only ghi lại việc merge PR3 + quyết định này mở riêng (PR #7, `docs/v5-13-pr3-postmerge`, merge
commit `1861f68`) — CI của PR đó dính lại đúng flake đã biết
`TestSupervisorNormalExit_TreeQuiescedFalseWhileDescendantStillRuns` (`internal/adapters/process`, không
liên quan gì tới diff docs-only) — xử lý bằng `gh run rerun --failed`, không phải sửa code.

# V5-14 — Cleanup/retention sweeper

## Bối cảnh và nghiên cứu trước khi code

Design doc tự nhận task này "CHƯA ĐỦ DỮ KIỆN" giống hệt V5-11/12/13 trước khi mỗi task đó được scope —
cùng kỷ luật nghiên cứu trước khi viết code được áp dụng lại. V5-14 tự nhận sở hữu HAI việc riêng biệt:

1. **`ExecuteWorkspaceSetRelease`** — phần thực thi filesystem/Git thật của `RequestWorkspaceSetRelease`
   (V3-11 chỉ ghi intent + enqueue job `WORKSPACE_SET_RELEASE`, chưa có consumer nào cho job đó).
2. **Artifact purge/delete** — dọn owned temp/orphan/expired artifact thật (xóa file CAS thật) — thao tác
   filesystem hủy diệt ĐẦU TIÊN thật sự trong toàn bộ hệ thống này.

Đọc kỹ `internal/app/workspacerelease/commands.go`'s own doc comment (package này tự nói rõ:
`ExecuteWorkspaceSetRelease` "thuộc V5-14", package `workspacerelease` không import bất kỳ thứ gì có thể
chạm workspace I/O — có test archtest riêng chứng minh, `TestRequestWorkspaceSetReleaseNeverImportsWorkspaceIO`).
Đọc `internal/archtest/boundary_test.go`'s own hai test song song
(`TestRequestWorkspaceReconciliationNeverImportsWorkspaceIO` / `TestRequestWorkspaceSetReleaseNeverImportsWorkspaceIO`)
— cả hai chỉ parse import của DUY NHẤT file `commands.go`, không phải cả package — nghĩa là thêm
`handler.go` (file mới, cùng package, có I/O thật) là ĐÚNG pattern `workspacereconcile` đã thiết lập
(`handler.go` riêng, `commands.go` sạch I/O) — không cần package mới, không vi phạm boundary test hiện có.

**Phát hiện cốt lõi (đọc code, không đoán): hầu hết mọi primitive `ExecuteWorkspaceSetRelease` cần ĐÃ CÓ
SẴN**, không cần thêm port/domain mới:
- `ports.WorkspaceLifecycle.ReleaseRepositoryWorkspace` — CAS READY→RELEASED đã có, đã test (V3-11).
- `ports.WorkRepository.TransitionWorkspaceSetState` — CAS chung cho WorkspaceSet.State, generic, không tự
  validate transition hợp lệ (đọc `sqlite/work.go`'s own `transitionWorkspaceSetStateTx` — chỉ so
  ExpectedState/ExpectedVersion, không có transition-graph validator riêng cho WorkspaceSetState) — nghĩa
  là handler mới TỰ quyết định transition nào hợp lệ, y hệt cách `workspaceprovision.Handler` đã làm.
- `ports.WorkspaceProvider.Release` — real I/O (`git worktree remove`), ĐÃ idempotent sẵn (no-op nếu đã
  released — đọc `gitworktree/provider.go`'s own `Release`: check `isReleased` trước, return nil sớm) và
  từ chối nếu dirty (`ErrWorkspaceDirty`) — không phải capability mới, chỉ cần orchestrate.
- `workspace.WorkspaceSetReleasing`/`RepositoryWorkspaceReleasing` — hai state ĐÃ tồn tại trong domain từ
  trước (0001_initial_schema.sql era) nhưng CHƯA từng được transition vào bởi bất kỳ code nào (grep xác
  nhận: 0 non-test reference). `RepositoryWorkspaceReleasing` không dùng được — không port nào transition
  vào nó (`ports.WorkspaceLifecycle` chỉ có 3 method, không có method thứ 4 cho RELEASING) — thêm method
  mới cho nó sẽ là thay đổi port, ngoài phạm vi "chỉ orchestrate cái đã có". `WorkspaceSetReleasing` THÌ
  dùng được — `TransitionWorkspaceSetState` là CAS chung, nhận NextState bất kỳ, không cần đổi port —
  dùng nó làm marker "release đang chạy" ở tầng WorkspaceSet, không dùng ở tầng RepositoryWorkspace.

**Quyết định tự chốt (không cần hỏi user) — "per-repository release result/retry schema"** (một trong các
contract design doc liệt kê "phải khóa trước khi code"): KHÔNG cần schema mới. Cột `state` sẵn có của mỗi
`RepositoryWorkspace` (READY/RELEASED/QUARANTINED) CHÍNH LÀ kết quả + cơ chế retry — đọc lại state tươi mỗi
lần chạy (kể cả sau crash) đã đủ để biết repo nào xong, repo nào chưa, không cần cột/bảng mới. Tương tự,
`WorkspaceSetReleasing` (transition trước khi chạm bất kỳ RepositoryWorkspace nào, transition sang
RELEASED chỉ sau khi mọi entry đã RELEASED) là durable marker đủ cho crash-resume ở tầng Set.

**Không tự ý làm nửa kia (Artifact purge/delete):** đây là thao tác hủy diệt filesystem THẬT SỰ đầu tiên
trong hệ thống (khác `ReleaseRepositoryWorkspace` — chỉ xóa một working-tree checkout tái tạo được, không
phải nội dung gốc/evidence). Theo đúng nguyên tắc đã áp dụng suốt phiên này (dừng lại hỏi user trước một
capability hủy diệt MỚI, tự làm khi chỉ là orchestrate cái đã có) — phần này để dành, chưa động tới trong
PR này.

## PR1 — ExecuteWorkspaceSetRelease (branch `feat/v5-14-workspace-set-release-executor`, từ
`origin/master`)

**Thiết kế:** mirror đúng cấu trúc `internal/app/workspacereconcile/handler.go` (file mới, cùng package
`workspacerelease`):
- Hàm thuần `classifyRepositoryWorkspaceRelease(state) (action, error)` — SKIP nếu đã RELEASED, RELEASE
  nếu READY, BLOCKED nếu QUARANTINED, lỗi cho state khác (không bao giờ nên tới được đây nếu eligibility
  check lúc request-time đúng) — unit test bảng (`handler_test.go`, package nội bộ `workspacerelease`
  không phải `_test`, giống `recovery_reaper_internal_test.go` của V5-13 — hàm và action đều unexported).
- `ExecuteWorkspaceSetRelease(ctx, deps, request) error`: đọc `WorkspaceSet` tươi qua
  `GetWorkspaceSetByFamilyID`; nếu đã RELEASED → no-op (idempotent); nếu đang RELEASING → resume, bỏ qua
  transition đầu; nếu READY/BLOCKED/FAILED → transition sang RELEASING trước; sau đó release từng
  RepositoryWorkspace (real I/O ngoài mọi Tx, giống mọi handler khác trong codebase này); nếu MỘT entry
  QUARANTINED giữa chừng → trả lỗi typed `ErrRepositoryWorkspaceQuarantinedDuringRelease`, dừng toàn bộ
  (không release phần còn lại, WorkspaceSet ở lại RELEASING cho operator xử lý) — đúng bar "không release
  quarantined workspace". Chỉ khi MỌI entry đã RELEASED mới transition RELEASING→RELEASED.
- `Handler`/`NewHandler` implement `workerpool.Handler` cho `WorkspaceSetReleaseJobKind`, y hệt
  `workspacereconcile.Handler`'s own shape (đặt tên `NewHandler` thay vì `New` vì package này đã có
  `RequestWorkspaceSetRelease` ở tầng command, không trùng tên nhưng để rõ ràng hơn).

**Test:**
- `handler_test.go` (package `workspacerelease`, không `_test`): bảng quyết định thuần cho
  `classifyRepositoryWorkspaceRelease`, 6 case (RELEASED/READY/QUARANTINED/PROVISIONING/RELEASING/FAILED).
- `handler_sqlite_test.go` (package `workspacerelease_test`, thật 100%: sqlite thật + `gitworktree.Provider`
  thật + `workerpool.Pool` thật, mirror đúng `workspacereconcile_test`'s own `newRealFixture`):
  - `TestEndToEnd_Release_ReadyWorkspaceSet_ReleasesRepositoryAndSet` — happy path đầy đủ: worktree thật bị
    xóa thật trên đĩa (`git worktree remove`), RepositoryWorkspace + WorkspaceSet đều RELEASED.
  - `TestEndToEnd_Release_IdempotentReplay_SecondRunIsNoOp` — chạy lại y hệt sau khi đã RELEASED xong,
    không lỗi, không đổi state (giả lập crash-recovery reclaim job).
  - `TestEndToEnd_Release_QuarantinedMidFlight_RefusesAndPreservesEvidence` — quarantine thật (qua
    `store.QuarantineRepositoryWorkspace` — API thật, không giả lập) SAU KHI request đã enqueue nhưng
    TRƯỚC KHI handler chạy — chứng minh recheck tại execution-time, không tin request-time, evidence
    QUARANTINED giữ nguyên, WorkspaceSet không đạt RELEASED.

**Lỗi gặp khi viết test (đã tự sửa, không phải bug ở code chính):**
- Ban đầu dùng `f.rw.Version` (version của RepositoryWorkspace) làm `ExpectedVersion` cho lệnh
  `RequestWorkspaceSetRelease` — sai, field đó fence theo version của WorkspaceSet, không phải
  RepositoryWorkspace. Sửa: đọc `f.reloadWorkspaceSet(t).Version` tươi.
  - `provisionHandler.Handle(ctx, job)` gọi TRỰC TIẾP (không qua workerpool thật) khiến job
  `WORKSPACE_PROVISION` CreateRootWorkItem tự enqueue vẫn nằm "active" trong `durable_jobs` — vì
  `RequestWorkspaceSetRelease`'s own eligibility check (`HasActiveJobForAggregateIDs`) xét CẢ AggregateID
  của WorkspaceSet (không chỉ từng RepositoryWorkspace) nên bị chính job provisioning cũ chặn. Khác với
  `workspacereconcile_test`'s own fixture (không hit vấn đề này vì eligibility check của nó chỉ xét theo
  RepositoryWorkspaceID). Sửa: chạy job provisioning qua `workerpool.Pool` thật (claim→Handle→complete),
  không gọi `Handle` trực tiếp.
  - So sánh `workspace.WorkspaceSet` bằng `!=` trực tiếp thất bại vì `BaseRevisionSet` là con trỏ
  (`*workspace.RevisionSet`) — hai lần load cùng nội dung ra hai con trỏ khác nhau. Sửa: so từng field
  (ID/State/Version) thay vì so cả struct.

**Verify:**
```
go build ./...                                                          # sạch
go vet ./...                                                            # sạch
go run ./cmd/docs-coverage-check                                        # debt = 0
gofmt -l internal/app/workspacerelease/*.go                             # rỗng (sau gofmt -w qua CRLF do stash)
go test -count=1 ./internal/app/workspacerelease/...                    # PASS, bao gồm 3 test E2E + bảng quyết định
go test -count=1 ./...                                                  # PASS toàn bộ (lần 1+2, không flake)
```

**Việc còn lại (không blocking PR này):** phần Artifact purge/delete (nửa kia của V5-14) — để dành, sẽ
nghiên cứu kỹ và có thể cần hỏi user trước khi code (capability hủy diệt mới).

**Việc còn lại:** commit, push, mở PR, chờ CI 6/6, merge.

**Kết quả:** PR #8, 6/6 pass. Squash-merged 2026-09-11, merge commit `58304fc`.

## V5-14 nửa 2 — Artifact purge/delete: contract từ user (2026-09-11)

Trước khi động vào nửa này (thao tác filesystem hủy diệt ĐẦU TIÊN thật sự trong toàn hệ thống), tôi hỏi
user 2 câu (làm luôn hay để dành; refcount khi nhiều row share 1 Locator xử lý sao). User yêu cầu hỏi lại
lần 1 (muốn trả lời kỹ hơn), sau đó trả lời đầy đủ — đây là contract, áp dụng y hệt cách contract
budget/recovery-ID của V5-13 đã được tôn trọng, không tự diễn giải khác đi.

**Câu 1 — làm luôn hay để dành, verbatim:**
> Chọn Làm luôn, nhưng phải là PR2 độc lập sau khi PR1 merge; không gộp hai phần.
> Lý do chính: V5-15 phụ thuộc toàn bộ V5-14. Chuyển sang V5-15 khi Artifact purge chưa hoàn thành sẽ làm
> acceptance gate chạy trên lifecycle thiếu nửa quan trọng.
> Cùng session không làm tăng rủi ro nếu giữ các gate sau:
> Nghiên cứu và chốt deletion-intent/refcount protocol trước khi code.
> Chỉ purge RAW_OUTPUT_TEMP/ORPHAN đủ hạn, không hold, không logical reference; chưa xóa canonical/attached
> artifact.
> Claim Locator atomically trên toàn bộ Project trước khi xóa.
> Dry-run và report là mặc định; thao tác thật phải explicit.
> ArtifactStore.Delete idempotent, kiểm locator/hash/root nghiêm ngặt.
> Test crash ở mọi boundary DB ↔ filesystem, shared Locator, concurrent attach, retry và unknown file.
> Mọi destructive test chỉ chạy trong temp artifact root.
> Session không phải safety boundary; contract, PR độc lập, dry-run và test mới là safety boundary. Vì vậy
> nên hoàn tất V5-14 trước rồi mới sang V5-15.

**Câu 2 — refcount cho Locator dùng chung, verbatim:**
> Chọn đếm reference thật trước khi xóa, nhưng chỉ thêm một query rồi xóa vẫn chưa đủ an toàn.
> Project đã xác nhận quan hệ nhiều-nhiều này: cùng bytes có thể tạo nhiều Artifact row, kể cả khác
> Project, và locator/content_hash cố ý không unique. Vì vậy phương án coi Locator là 1-1 trái với model
> hiện tại.
> Quy tắc đúng nên là: chỉ xóa blob khi mọi Artifact row có cùng Locator đều thuộc tập đủ điều kiện xóa và
> không còn logical reference. Các row chặn xóa gồm: Hold=true; Canonical/unexpired artifact; Orphan chưa
> đủ tuổi cleanup; Artifact còn được Message, Checkpoint, Evidence… tham chiếu; Row khác Project cũng phải
> được tính.
> Ngoài ra cần tránh TOCTOU: không nên query → nhả transaction → xóa file. Sweeper nên atomically claim
> Locator bằng durable deletion intent; InsertArtifact phải từ chối/retry khi Locator đang được claim. Sau
> đó xóa blob ngoài transaction và finalize metadata trong transaction khác.
> Nếu nhiều row cùng Locator đều đủ điều kiện, claim cả nhóm, xóa file đúng một lần rồi xóa/finalize toàn
> bộ row. Metadata cùng Locator nhưng khác hash/size phải fail closed và quarantine.
> ArtifactStore hiện chưa có Delete, nên port xóa và deletion-intent protocol thuộc V5-14, không chỉ là
> một query bổ sung.

**Xác minh trước khi code (đọc code thật, không giả định):** `docs/design/07-v5-execution-evidence.md`'s
own V5-15 "Phụ thuộc: V5-01…V5-14" xác nhận đúng lý do câu 1. `docs/architecture/04-go-core-spec.md §19`
("Sweeper phải check reference/hold atomically trước xóa") xác nhận yêu cầu atomic-check không phải tôi
tự nghĩ ra. `0027_artifacts.sql` xác nhận `content_hash` KHÔNG có UNIQUE, đúng như user trích.

**Phát hiện quan trọng thu hẹp phạm vi PR này (đọc code, không đoán):** truy vết MỌI nơi ghi
artifact-reference — `messages.content_artifact_id` (FK thật, qua `PrepareAttachment` dựng row với
`AttachState=Attached` NGAY TỪ ĐẦU, chưa bao giờ Orphan) và `checkpoints.artifact_refs_json`/
`evidence.artifact_manifest_json` (JSON list ArtifactID, không phải Locator — xác nhận qua
`command_node_executor.go`/`gate_node_executor.go`'s own `outputArtifactID`) — `finalize.go` (dòng
~449/477) promote MỌI ArtifactID trong `ArtifactReferences` từ Orphan→Attached NGAY TRONG CÙNG transaction
ghi Checkpoint/Evidence. Suy ra: một row CÒN Orphan không bao giờ có thể bị Message/Checkpoint/Evidence
tham chiếu — nghĩa là scope PR này vào ĐÚNG `AttachState=Orphan` làm "no logical reference" tự động đúng,
không cần dựng reverse-index quét JSON. Đây là cách đọc đúng, hẹp của "chưa xóa canonical/attached artifact"
— purge Attached-nhưng-hết-hạn (cần quét JSON thật) để dành pha sau.

**Toàn bộ contract + nghiên cứu đã lưu memory** `agent-kit-v5-14-artifact-purge-contract.md` (verbatim,
đọc lại trước khi code bất kỳ phần nào của nửa này ở phiên sau).

## PR2a — Artifact purge foundation: schema + port (branch
`feat/v5-14-artifact-purge-foundation`, từ `origin/master` sau PR #8)

**Phạm vi:** CHỈ nền tảng schema/port — chưa có sweep job/handler thật (để dành PR2b). Không tự ý bundle
với PR1 (user đã nói rõ "không gộp hai phần").

**Thực hiện:**
- `internal/domain/artifact`: thêm `Purged AttachState` (chỉ đạt được từ Orphan; row/hash/timestamp KHÔNG
  bao giờ bị xóa, chỉ payload — đúng ADR-017 "giữ audit").
- `ports.ArtifactStore.Delete(ctx, ref) error` (method mới trên interface có sẵn — không adapter/fake nào
  khác implement `ports.ArtifactStore` ngoài `artifactstore.Store`, xác nhận qua grep trước khi thêm) +
  implementation thật (`internal/adapters/artifactstore/filesystem.go`): tái dùng ĐÚNG hash/size check của
  `Verify` (không viết logic mới) — idempotent (no-op nếu đã xóa), refuse nếu hash/size lệch (KHÔNG xóa gì
  khi refuse).
- Migration 0032: rebuild `artifacts` (SQLite không có ALTER CHECK, dùng lại ĐÚNG kỹ thuật
  create-copy-drop-rename của migration 25) để widen CHECK cho `PURGED`; thêm vào
  `migrationsRequiringForeignKeysOff` (vì `messages.content_artifact_id REFERENCES artifacts(id)`, giống
  hệt lý do migration 25 cần OFF cho `durable_jobs`). Thêm index `idx_artifacts_locator` (group-eligibility
  query của PR2b sau này cần).
- Migration 0033: bảng mới `artifact_locator_purge_claims` (PK = locator, không phải ArtifactID — vì
  Locator dùng chung nhiều row) — không cần FK-off (bảng mới, không ai tham chiếu).
- `ports.ArtifactRepository`: 3 method mới — `ListArtifactsByLocator` (group-eligibility/refcount read,
  mọi Project), `ClaimArtifactLocatorForPurge` (INSERT, PK conflict → `ErrPersistenceAlreadyExists`),
  `ReleaseArtifactLocatorClaim` (DELETE, idempotent). `InsertArtifact` sửa: check
  `artifact_locator_purge_claims` trước khi INSERT, từ chối nếu Locator đang bị claim (nửa kia của TOCTOU
  fence — nửa 1 là claim atomically trước khi xóa).
- Đồng bộ implementation thật (sqlite) + fake (`internal/app/ports/fake`) — cả 2 phải cùng hành vi.

**Test:**
- `internal/adapters/artifactstore/filesystem_test.go`: `TestDelete_RemovesContent_ThenIsIdempotent`,
  `TestDelete_HashMismatch_RefusesAndLeavesContentInPlace`, `TestDelete_AlreadyAbsent_IsANoOp`,
  `TestDelete_MalformedLocator_RejectedBeforeTouchingFilesystem`.
- `internal/adapters/sqlite/migration_0032_test.go` (mirror `migration_0025_test.go`'s own kỹ thuật thật):
  seed real artifact + real message tham chiếu nó, apply migration 32, xác nhận preserve byte-for-byte +
  `PRAGMA foreign_key_check` = 0 + CHECK mới nhận PURGED/từ chối giá trị lạ; test sabotage
  (`artifacts_new` đã tồn tại trước) chứng minh rollback sạch, không ghi `schema_migrations`.
- `internal/adapters/sqlite/artifact_repository_test.go`: `ListArtifactsByLocator` xuyên Project,
  `ClaimArtifactLocatorForPurge` reject trùng, `ReleaseArtifactLocatorClaim` idempotent + mở khóa lại được,
  `InsertArtifact` reject khi Locator đang bị claim.
- `internal/domain/artifact/artifact_test.go`: `Purged.Valid()`.

**Lỗi gặp khi verify (đã tự sửa):** 3 test hardcode tổng số migration (`want 30`) — cần bump theo từng
migration mới thêm (30→31 sau migration 32, →32 sau migration 33); đã sửa cả 3
(`db_test.go`/`unitofwork_test.go`). Một lần full-suite run flake ở
`TestExecuteNodeHandler_PollerDetectsDurableCancellation_UnrecognizedExecutorLeavesRunning`
(`internal/app/runtime`, timing-sensitive, không liên quan gì tới `internal/adapters/*`/`internal/domain/
artifact` — package này PR không hề chạm tới) — pass 3/3 khi chạy riêng, xác nhận flake không phải regression.

**Verify:**
```
go build ./...                                                          # sạch
go vet ./...                                                            # sạch
go run ./cmd/docs-coverage-check                                        # debt = 0
gofmt -l <mọi file đổi>                                                 # rỗng
go test -count=1 ./internal/adapters/artifactstore/... ./internal/adapters/sqlite/... ./internal/domain/artifact/... # PASS
go test -count=1 ./...                                                  # PASS toàn bộ (lần 1+2, không flake thật)
```

**Việc còn lại (PR2b, không blocking PR này):** sweep job/handler thật — reserve→delete-ngoài-Tx→finalize
protocol đầy đủ (dùng các primitive PR2a vừa xây), dry-run report mặc định, thao tác thật cần explicit
flag, crash-safety test ở từng boundary claim/delete/finalize.

**Việc còn lại:** commit, push, mở PR, chờ CI 6/6, merge.

**Kết quả:** PR #9, 6/6 pass. Squash-merged 2026-09-11, merge commit `197483c`.

## PR2b — Artifact sweep job/handler (branch `feat/v5-14-artifact-sweep-job`, từ
`origin/master` sau PR #9)

**Thiết kế:** mirror gần như y hệt `internal/app/runtime.RecoveryReaperHandler` (V4-13) — job
self-rescheduling, JobClass=CONTROL, installation-global singleton, fence bằng generation cursor riêng.
Package mới `internal/app/artifactsweep`:

- Migration 0034: widen LẠI 2 CHECK của `durable_jobs` (giống hệt migration 25 làm cho RECOVERY_REAPER,
  lần này thêm `ARTIFACT_SWEEP`) — cả job_class allow-list lẫn "kind nào được project_id NULL". Cần
  FK-off giống migration 25 (lý do giống hệt). Đồng bộ `internal/app/ports/job_class.go`'s own
  `controlJobKinds`/`installationGlobalJobKinds` (Go-level authority thứ hai, mirror đúng cách RECOVERY_REAPER
  đã làm) + test mới (`TestValidateJobScope_ArtifactSweep_RequiresNoProjectOrRun`, bump
  `TestClassifyJobKind_ControlAllowList` từ 4 lên 5 kind).
- Migration 0035: bảng singleton mới `artifact_sweep_state` (mirror `recovery_reaper_state`, migration 26)
  — thêm cột `dry_run` (mặc định 1/true — đúng contract "Dry-run và report là mặc định; thao tác thật phải
  explicit"). Không cần FK-off (bảng mới).
- `ports.ArtifactRepository`: 3 method mới — `GetArtifactSweepState`/`AdvanceArtifactSweepGeneration`
  (mirror đúng `RuntimeRepository.GetRecoveryReaperState`/`AdvanceRecoveryReaperGeneration`),
  `SetArtifactSweepDryRun` (CAS flip DryRun, không cần app-layer wrapper — xác nhận `SetArtifactHold` cũng
  chưa có wrapper nào, đúng "real method từ đầu, chưa cần caller thật" convention V5-01 đã lập).
- Hàm thuần `classifyLocatorGroup(rows, olderThan) (decision, reason)` — decision đóng 3 giá trị: PURGE
  (mọi row Orphan + không Hold + qua grace), BLOCKED (một row bất kỳ Attached/Held/chưa qua grace chặn CẢ
  NHÓM), CORRUPT (content_hash/size lệch nhau giữa các row cùng Locator — fail closed, không tự sửa).
- `ExecuteArtifactSweep`: đọc state; nếu generation đã bị vượt (job cũ redeliver sau khi job mới đã chạy)
  → no-op giống hệt RecoveryReaperHandler; list candidate Orphan qua `ListOrphanedArtifacts` (grace = tái
  dùng đúng 7 ngày của `RAW_OUTPUT_TEMP` — TỰ QUYẾT, ghi rõ lý do, không có "orphan grace" riêng nào khác
  được định nghĩa ở đâu); group theo Locator (dedupe); mỗi group: classify rồi hoặc PURGE thật (dry_run=
  false) hoặc chỉ ghi WOULD_PURGE (dry_run=true, mặc định) hoặc BLOCKED/CORRUPT (không đụng gì). Purge thật
  = ĐÚNG 3 pha reserve→delete-ngoài-Tx→finalize (mirror `consumeFreshStart` của V5-13): (1)
  `ClaimArtifactLocatorForPurge` — nếu `ErrPersistenceAlreadyExists` thì COI LÀ resume claim cũ của chính
  job này (job này là singleton, không bao giờ có 2 instance chạy song song thật, nên claim cũ chỉ có thể
  là do 1 lần crash trước đó của CHÍNH job này để lại — không phải conflict thật); (2) `ArtifactStore.Delete`
  hẳn ngoài mọi Tx; (3) Tx riêng transition mọi row trong group sang `Purged` + release claim. Ghi manifest
  (`SweepManifest`) thành domain event `ARTIFACT_SWEEP_COMPLETED` (AggregateType="ArtifactSweep",
  AggregateID="singleton") — KHÔNG cần bảng mới hay method "next sequence" mới: tái dùng chính
  `Generation+1` làm Sequence luôn (Generation vốn đã là counter CAS-fenced, đúng-một-lần-mỗi-run cho
  CÙNG aggregate singleton này). Xong thì advance generation + tự enqueue job kế tiếp, cùng transaction.

**Test:**
- `sweep_test.go` (package nội bộ `artifactsweep`, không `_test`): bảng quyết định thuần cho
  `classifyLocatorGroup`, 8 case (rỗng, orphan-qua-grace-1-row, orphan-qua-grace-nhiều-row-cùng-locator,
  1-row-attached-chặn-cả-nhóm, 1-row-hold-chặn-cả-nhóm, 1-row-chưa-qua-grace-chặn-cả-nhóm,
  hash-lệch=corrupt, size-lệch=corrupt).
- `sweep_sqlite_test.go` (package `artifactsweep_test`, thật 100%: sqlite thật + `artifactstore.Store`
  thật — file thật trên đĩa thật):
  - `TestExecuteArtifactSweep_DryRunDefault_ReportsWithoutTouchingAnything` — mặc định DryRun=true, content
    thật + row đều KHÔNG bị đụng, chỉ ghi WOULD_PURGE.
  - `TestExecuteArtifactSweep_RealRun_DeletesContentAndMarksPurged` — sau khi flip DryRun=false: file thật
    bị xóa thật, row chuyển Purged (Locator/ContentHash vẫn giữ nguyên — audit trail), generation advance.
  - `TestExecuteArtifactSweep_AttachedSiblingSharesLocator_BlocksWholeGroup` — Put trùng bytes 2 lần (1
    Orphan quá hạn, 1 Attached đang sống) → locator giống hệt nhau thật sự (xác nhận content-addressing) →
    CẢ NHÓM bị block, file thật KHÔNG bị xóa, row Orphan vẫn giữ nguyên.
  - `TestExecuteArtifactSweep_ResumesAfterCrashedClaim` — tự tạo claim trước (giả lập crash giữa pha 1 và
    pha 2/3), gọi lại ExecuteArtifactSweep — resume đúng, purge thành công, không bị coi là conflict.
  - `TestStartupArtifactSweep_EnqueuesExactlyOneJobPerGeneration` — idempotent enqueue.
- `internal/adapters/sqlite`: `migration_0034_test.go` (mirror `migration_0025_test.go` — preserve
  byte-for-byte + FK check + CHECK mới nhận ARTIFACT_SWEEP/từ chối project_id non-null + sabotage test),
  `TestArtifactRepository_ArtifactSweepState_SeededDryRunThenAdvances` (seed đúng {0,true,1}, advance CAS
  đúng, reject stale, SetArtifactSweepDryRun CAS đúng, reject stale).
- `internal/app/ports/job_class_test.go`: `TestValidateJobScope_ArtifactSweep_RequiresNoProjectOrRun`.

**Lỗi gặp khi verify (đã tự sửa, không phải bug logic):**
- 3 test hardcode tổng số migration (đã bump 2 lần trong PR này: 32→34 sau migration 34, 34→... — thực ra
  chỉ 2 migration mới (34+35) nên bump thẳng 32→34).
- Một lần full-suite chạy dính `TestAdapterRegister_DuplicateIsIdempotent` (`cmd/agentkit`) timeout 5s khi
  probe subprocess dưới tải CPU full-suite — pass 3/3 khi chạy riêng (1.1s/lần) — xác nhận flake CPU-load,
  không phải regression, package này PR không hề chạm tới.

**Verify:**
```
go build ./...                                                          # sạch
go vet ./...                                                            # sạch
go run ./cmd/docs-coverage-check                                        # debt = 0
gofmt -l <mọi file đổi>                                                 # rỗng (chỉ file thật đổi, không
                                                                         # đụng CRLF noise của file khác)
go test -count=1 ./internal/app/artifactsweep/... ./internal/adapters/sqlite/... ./internal/app/ports/... # PASS
go test -count=1 ./...                                                  # PASS toàn bộ (lần 1+2+3 sau khi
                                                                         # xác nhận flake, không regression)
```

**Kết quả kỳ vọng của V5-14 (design doc's own "Kết quả kỳ vọng" line) — đối chiếu:** "dry-run và actual run
cùng tạo durable sweep manifest" ✓ (domain event mọi lần, cả 2 mode); "chỉ ORPHAN quá grace... khi không
hold, không canonical/recovery/evidence/ReleaseSet ref và không còn row sống chung locator/hash" ✓ (scope
hẹp Orphan-only khiến "no logical reference" tự động đúng — xem lý luận đã ghi ở "V5-14 nửa 2" phía trên;
liveness/refcount qua ListArtifactsByLocator + classifyLocatorGroup); "canonical, held, unknown, active và
quarantine đều được giữ" ✓ (BLOCKED chặn cả nhóm nếu có 1 row không đủ điều kiện); "Purge ba pha chịu
crash, không gọi filesystem trong Tx và giữ metadata/hash PURGED cho audit" ✓.

**Việc còn lại (ngoài phạm vi PR này, ghi rõ để dành — không phải thiếu sót bị bỏ qua):** purge một
Attached-nhưng-hết-hạn RAW_OUTPUT_TEMP (cần reverse-index quét JSON Checkpoint/Evidence/agent_events thật
sự) — chưa làm, vì scope PR này chỉ Orphan. Không có app-layer command wrapper cho `SetArtifactSweepDryRun`
hay wiring `StartupArtifactSweep` vào composition root thật — đúng "chưa có real CLI wiring" pattern mọi
task V4/V5 khác đã theo.

**Việc còn lại:** commit, push, mở PR, chờ CI 6/6, merge. Sau khi merge: V5-14 coi là HOÀN THÀNH (cả 2 nửa),
chuyển sang V5-15 theo đúng thứ tự phụ thuộc roadmap.

**Kết quả:** PR #10, 6/6 pass. Squash-merged 2026-09-11, merge commit `67092d5`. **V5-14 HOÀN THÀNH — cả 2
nửa (ExecuteWorkspaceSetRelease + Artifact purge/delete) đã merge.**

# V5-15 — Execution/evidence acceptance gate (task cuối cùng của V5)

## Nghiên cứu trước khi scope

Không giống V5-11/12/13/14, design doc không tự đánh dấu V5-15 "CHƯA ĐỦ DỮ KIỆN" — nhưng quy mô lớn hơn
hẳn mọi task V5 trước (ghép 4 hệ thống chưa từng chạy chung + ~10 kịch bản inject lỗi + false-completion
oracle), nên trước khi code tôi đã cho một background agent nghiên cứu sâu (đọc `internal/integration/
runtimeengine_test.go` 2062 dòng, `internal/spikeacceptance`, tra cứu NodeExecutorRouter/CompletionPolicy/
ReleaseSet đã từng được ghép chung ở đâu chưa, checker read-only enforcement, provider recording, và
CompletionPolicy's own real-sqlite coverage) rồi hỏi user cách chia PR. Toàn bộ báo cáo nghiên cứu + câu
trả lời đầy đủ của user đã lưu verbatim vào memory `agent-kit-v5-15-acceptance-gate-contract.md` — đọc lại
trước khi code bất kỳ phần nào của V5-15.

**Phát hiện quan trọng nhất (tự kiểm chứng lại bằng grep trực tiếp, không chỉ tin báo cáo agent):**
background agent ban đầu báo "checker write chưa có detection nào" — nhưng grep trực tiếp
`internal/app/runtime/agent_node_executor_resources.go:355` cho thấy
`strictReadOnly := profile.Role == workflow.AgentRoleChecker` ĐÃ tồn tại từ V5-12's own PR, feed vào
`validateStrictlyReadOnlyDiffs` bên trong `buildEvidence` — nghĩa là detection thật cho "checker write" đã
có sẵn, không cần xây mới. Bài học: luôn tự verify lại phát hiện của subagent bằng công cụ trực tiếp trước
khi tin, đặc biệt khi nó phủ định một khả năng.

## Contract chia PR từ user (verbatim)

> Nên tách nhiều PR và bắt đầu bằng happy path. Chia V5-15 thành 5 phần:
>
> V5-15A — Real composition
> Router + executors + CompletionPolicy + ReleaseSet.
> Một multi-node Run thật đi tới SUCCEEDED/DONE.
> Restart process rồi xác minh Run, WorkItem, DecisionArtifact, events và ReleaseSet vẫn nhất quán.
> Xây reusable scenario harness cho các PR sau.
>
> V5-15B — Completion integrity
> Claim-done.
> Gate failure.
> Artifact tamper.
> False-completion oracle: không được DONE nếu thiếu bất kỳ authoritative condition nào.
>
> V5-15C — Recovery and availability
> Crash/checkpoint recovery.
> Provider loss.
> Adapter drift.
> Isolation unavailable.
> Xác minh replay không tạo trùng Attempt, activation, artifact hoặc event.
>
> V5-15D — Isolation and fencing
> Scope violation.
> Checker write attempt.
> Cancel giữa mutating attempt.
> Xác minh workspace/revision không bị promote sau khi fence hoặc policy thắng.
>
> V5-15E — Full conformance matrix
> Chạy toàn bộ scenario trong cùng một matrix.
> Semantic diff giữa expected và persisted state.
> Restart verification cho từng terminal outcome.
> Kiểm tra oracle trên DB state, domain events, outbox, blockers, activations và artifacts.
>
> Mỗi PR phải dùng production command/router/repository path; harness không được trực tiếp sửa DB để tạo
> ra kết quả cần kiểm chứng. Chỉ đánh dấu V5-15 hoàn thành sau khi PR cuối chạy toàn bộ matrix, dù
> happy-path đã merge từ PR đầu.

**Hai quy tắc áp dụng cho MỌI PR (A-E):** (1) không bao giờ sửa DB trực tiếp để tạo ra kết quả cần kiểm
chứng — mọi kịch bản phải đi qua đúng command/router/repository path thật; (2) V5-15 CHƯA xong cho tới khi
PR E chạy được toàn bộ matrix, dù A/B/C/D đã merge độc lập trước đó.

**Việc còn lại:** bắt đầu V5-15A — thiết kế + xây composition thật (Router+executors+CompletionPolicy+
ReleaseSet) chạy 1 multi-node Run thật tới SUCCEEDED/DONE, verify được sau restart, cộng reusable scenario
harness cho B/C/D/E dùng lại.

**Kết quả:** PR #11 (docs, ghi lại contract 5 phần), 6/6 pass, squash-merged 2026-09-11, merge commit
`7041811`.

## V5-15A — Báo cáo nghiên cứu chi tiết, bàn giao cho session sau (2026-09-11)

User yêu cầu: sau khi PR docs merge xong, viết báo cáo chi tiết để session sau triển khai tiếp — CHƯA code
V5-15A trong phiên này, chỉ nghiên cứu sâu để phiên sau bắt tay vào code ngay không cần dò lại từ đầu.
Toàn bộ nghiên cứu dưới đây (một phần từ background Explore agent, phần còn lại tự tôi verify trực tiếp
bằng grep/đọc code — LUÔN tự kiểm chứng lại phát hiện của agent trước khi tin) đã lưu đầy đủ vào memory
`agent-kit-v5-15-acceptance-gate-contract.md`; bản này trong checklist là bản đầy đủ nhất, đọc file này
trước khi code.

### Phát hiện cốt lõi nhất: "no DB shortcut" rule của user LOẠI TRỪ kỹ thuật fixture đã có sẵn

`internal/app/runtime/completion_policy_test.go`'s own `seedRunEvidence` helper (dòng 151-191) tạo NodeRun/
ExecutionAttempt/Evidence THẲNG bằng `tx.Runtime().CreateNodeRun`/`CreateExecutionAttempt`/`CreateEvidence`
— bỏ qua hoàn toàn CommandNodeExecutor/GateNodeExecutor thật (file tự ghi rõ: "bypassing the real
CommandNodeExecutor pipeline entirely"). Đây CHÍNH XÁC là kiểu "sửa DB trực tiếp để tạo ra kết quả cần
kiểm chứng" mà user đã cấm trong contract V5-15. **Kết luận: V5-15A KHÔNG được tái dùng kỹ thuật này —
Evidence cho completion candidate phải đến từ một CommandNodeExecutor/GateNodeExecutor THẬT chạy qua
NodeExecutorRouter thật, dispatch bởi ExecuteNodeHandler thật.**

### Không có test nào từng spawn process thật cho Command/Gate executor

Grep xác nhận `gate_node_executor_test.go`/`command_node_executor_test.go` chỉ dùng `fake.ProcessSupervisor`
— chưa từng có test nào spawn process thật cho 2 executor này (khác `internal/adapters/process/
supervisor_test.go`, vốn test adapter process ở tầng thấp hơn, không qua CommandNodeExecutor). V5-15A sẽ là
lần ĐẦU TIÊN. Cần một binary/script thật, cross-platform (Windows+Linux CI), đơn giản (exit 0 + có thể ghi
stdout), tương tự cách `cmd/fake-claude`/`cmd/fake-codex` đã làm cho AGENT — có thể tái dùng kỹ thuật đó
(một `cmd/` binary nhỏ mới, hoặc dùng lại chính `cmd/fake-claude`/`fake-codex` nếu COMMAND node authoring
cho phép trỏ argv vào một binary Go build sẵn trong CI).

### Quyết định tự chốt: bắt đầu bằng COMMAND + MACHINE_GATE, CHƯA cần AGENT/provider cho happy path

Design doc nói "agent→command→gate→checker→ReleaseSet→end" nhưng đó là tên đầy đủ các loại node cần phủ
*cuối cùng* (qua cả A-E), không phải yêu cầu bắt buộc PR A phải có AGENT. COMMAND + MACHINE_GATE dùng
CHUNG một `ports.ProcessSupervisor` thật — không cần AGENT thật (tức không cần "recorded provider" —
xem mục dưới) cho riêng PR A. Điều này giảm đáng kể độ phức tạp bước đầu: AGENT/CHECKER role/scripted
fake-CLI provider để dành cho phần cần chúng thật sự (V5-15B's own "claim-done" cần một AGENT maker tự
xưng done; V5-15C's own "provider loss" cần một AGENT/provider thật để mất kết nối).

### API thật đã xác nhận (đọc trực tiếp, không đoán) — dùng nguyên văn khi code

- `runtime.EvaluateCompletionCandidate(ctx, uow, ids, clk, cmd, EvaluateCompletionCandidateRequest{RunID})`
  — 1 hàm duy nhất, `cmd.ExpectedVersion` = version của WorkflowRun quan sát được. `completion_policy.go`
  dòng 179.
- `loadLatestReleaseSetGate` (dòng 373): nếu family CHƯA có ReleaseSet nào → `satisfied: true` (mặc định
  qua!) — nghĩa là PR A vẫn PHẢI tạo+seal ReleaseSet thật để chứng minh path đó hoạt động, vì nếu bỏ qua,
  completion vẫn PASS nhưng không test được gì về ReleaseSet cả.
- `work.CreateReleaseSet(ctx, uow, ids, cmd, CreateReleaseSetRequest{ProjectID, FamilyID, Repositories})`
  rồi `work.SealReleaseSet(ctx, uow, cmd, SealReleaseSetRequest{ReleaseSetID})` (`cmd.ExpectedVersion` =
  version ReleaseSet). `Repositories` cần `BaseVCSObjectID`/`ResultVCSObjectID` (dùng SHA thật từ git
  fixture) + `Verdict` (kiểu `gate.Verdict`, ví dụ "PASS").
- `runtime.NewCommandNodeExecutor(uow, ids, store /*ArtifactStore*/, workspaces /*WorkspaceProvider*/,
  writeLeases, supervisor /*ProcessSupervisor*/, secrets /*SecretResolver*/, registry /*eventschema*/,
  matcher /*redact.Matcher*/, checkpoints /*agentevents.CheckpointStore*/, clk, interruptions, reconciler)`
  — **`*sqlite.Store` MỘT INSTANCE thỏa mãn structurally CẢ writeLeases (ports.WriteLeaseManager, xác nhận
  `var _ ports.WriteLeaseManager = (*Store)(nil)` tại scheduling.go:861), checkpoints
  (agentevents.CheckpointStore — có `StoreCheckpoint`, checkpoint_store.go:19), interruptions VÀ
  reconciler** (RecoveryReaperHandler đã dùng đúng `store, store, store` cho 3 tham số tương tự — comment
  gốc: "it satisfies all three spike-era interfaces structurally"). `NewGateNodeExecutor` shape tương tự,
  bớt `writeLeases` (Gate không cần acquire write lease). Chỉ CẦN NEW thật: `gitworktree.Provider`
  (WorkspaceProvider), `artifactstore.Store` (ArtifactStore), `process.Supervisor`/`processadapter.
  NewSupervisor()` (ProcessSupervisor), `secretenv.Resolver` (SecretResolver).
- `runtime.NodeExecutorRouter{Agent, Command, Gate}` implements `ports.NodeExecutor` — truyền thẳng vào
  `runtime.NewExecuteNodeHandler(uow, ids, executor, clk, isolationChecker, agentRegistry)` thay vì
  scriptedNodeExecutor.
- Evidence Kind cho COMMAND cố định: `runtimedomain.EvidenceKindCommandExecution = "COMMAND_EXECUTION"`.
  Evidence Kind cho MACHINE_GATE là `criterion.EvidenceKey` — TỰ ĐẶT ở authoring time trên từng
  `gate.Criterion`, phải khớp CHÍNH XÁC với `policy.CompletionRules.RequiredEvidenceKinds` mình khai.

### Template tái dùng — `internal/integration/runtimeengine_test.go` (2062 dòng, V4-14 sở hữu)

Đã có SẴN: real `*sqlite.Store` + real `workerpool.Pool` + ĐỦ mọi real job handler (Scheduler,
NodeSchedulingHandler, ExecuteNodeHandler, WaitTimeoutHandler, ApprovalTimeoutHandler,
RequestScopeExpansionHandler, ScopeExpansionReconcileHandler, workspaceprovision.Handler,
CancelRunCoordinatorHandler, RecoveryReaperHandler) + một WorkflowDocument multi-node thật (đủ 10
NodeType). Nhược điểm: dùng `scriptedNodeExecutor` (fake outcome theo NodeKey) VÀ `scriptedWorkspaceProvider`
(fake, không phải gitworktree thật) — vì scope V4-14 CHỦ ĐÍCH không cần real Git (V3-12 đã gate rồi) và
CHỦ ĐÍCH dừng ở VERIFYING ("real completion/COMPLETED authority là V5-11's own job... deliberately out of
V4-14's scope" — dòng 26). **Kết luận: đây là template wiring rất mạnh để copy PHONG CÁCH
(registerRuntimeEngineHandlers/newRuntimeEnginePool/seedREProject), nhưng V5-15A phải là một fixture MỚI,
KHÔNG patch file này** — nó đã là gate riêng 2000+ dòng của V4-14, tự khép kín, và cần real Git (thay
scriptedWorkspaceProvider bằng gitworktree.Provider thật) + real executor thật (thay scriptedNodeExecutor
bằng NodeExecutorRouter thật) — hai thay đổi đủ lớn để xứng đáng một file/fixture riêng của V5-15, đặt có
thể tại `internal/integration/` (cùng chỗ) hoặc một package mới `internal/integration/v5accept` — session
sau tự quyết định, chưa cần hỏi user (không phải quyết định kiến trúc lớn).

### spikeacceptance package: chỉ tham khảo STYLE, không cắắm vào được

`internal/spikeacceptance` (SPKID cố định đúng 14 giá trị, `RequiredSPKIDs()` hardcode) là cơ chế
V0-spike-specific, KHÔNG phải harness tổng quát. `SemanticDiff` (semantic_diff.go) so sánh 2 `SPKResult`
đã có sẵn — kiểu chặt, không generic. V5-15E's own "semantic diff giữa expected và persisted state" nên
XÂY một comparator MỚI cùng phong cách (typed result struct + so sánh field cố định + allowlist cho
platform/timing noise), KHÔNG cố nhét vào package `spikeacceptance` (sẽ phá invariant "đúng 14 scenario"
của nó).

### CompletionPolicy's real evaluator CHƯA từng chạy qua real sqlite

`completion_policy_test.go` chỉ dùng `fake.UnitOfWork`. `completion_sqlite_test.go` (sqlite thật) chỉ chứng
minh Run tới được VERIFYING bền vững, KHÔNG BAO GIỜ gọi `EvaluateCompletionCandidate`. V5-15A sẽ là lần đầu
tiên hàm này chạy qua real SQLite + verify được sau restart — đúng tinh thần "Hoàn thành khi" của cả V5-15.

### Checker write injection (dành cho V5-15D, nghiên cứu trước cho tiện) — ĐÃ có detection thật

Tự grep xác nhận (sửa lại phát hiện ban đầu của background agent — nó chỉ thấy `forceReadOnlyMounts`,
BỎ SÓT phần dưới): `internal/app/runtime/agent_node_executor_resources.go:355` —
`strictReadOnly := profile.Role == workflow.AgentRoleChecker`, feed vào `validateStrictlyReadOnlyDiffs`
(dòng 311) bên trong `buildEvidence` — detection diff THẬT đã có từ PR V5-12, không cần xây mới. V5-15D chỉ
cần dựng 1 kịch bản AGENT Checker thật có process thật cố ghi đè lên workspace, rồi xác nhận
`buildEvidence` reject đúng.

### Đề xuất chia nhỏ tiếp bên trong V5-15A (session sau tự quyết định thứ tự, không phải quyết định cần hỏi
user — nằm trong phạm vi "Router + executors + CompletionPolicy + ReleaseSet" user đã chốt)

1. Fixture project/repo real Git (mirror `workspacereconcile_test.go`'s own `newRealFixture` — real
   `createReconcileFixtureGitRepository` kỹ thuật) + real `workspaceprovision.Handler` với
   `gitworktree.Provider` thật (không phải `scriptedWorkspaceProvider`).
2. Một binary/script thật, cross-platform, cho COMMAND node spawn qua `processadapter.NewSupervisor()`
   thật (exit 0 nhanh) — cân nhắc thêm 1 `cmd/` binary Go nhỏ mới nếu cần argv/output cụ thể, hoặc tái
   dùng script inline (`sh -c`/`cmd /c`) nếu đơn giản đủ và test matrix Windows/Linux đều chạy được.
3. WorkflowDocument tối giản: START -> COMMAND(maker, real spawn, output artifact) -> MACHINE_GATE(checker,
   1 criterion EvidenceKey tự đặt, verdict PASS thật dựa trên output COMMAND) -> END, `CompletionPolicyRef`
   trỏ một `policy.CompletionRules{RequiredEvidenceKinds: [criterion's EvidenceKey]}` thật, publish qua
   `workflow.Compile`/`tx.Definitions().PublishWorkflowVersion` thật.
4. Real `NodeExecutorRouter{Command: real, Gate: real}` đăng ký vào `ExecuteNodeHandler` thật qua
   `registerRuntimeEngineHandlers`-style helper (copy pattern, sửa `executor`/`workspaceprovision.New`).
5. Real `CreateReleaseSet`+`SealReleaseSet` cho family, dùng VCS SHA thật từ git fixture.
6. Chạy pool thật tới VERIFYING (dùng lại kỹ thuật driving loop có sẵn), rồi gọi
   `EvaluateCompletionCandidate` thật → xác nhận Outcome=PASS, Run=SUCCEEDED, WorkItem=DONE.
7. `store.Close()` + `sqlite.Open()` lại (restart thật) → load lại toàn bộ trace
   WorkItem→Run→NodeRun→Attempt→ContextSnapshot→RevisionSet→Evidence/Artifact ID+hash→ReleaseSet→
   DecisionArtifact, assert mọi ID/hash còn nguyên, đọc lại được y hệt.
8. Rút phần fixture-building (project/repo/document/pool) thành helper tái dùng được cho V5-15B/C/D/E —
   "reusable scenario harness" user yêu cầu trong contract.

**Việc còn lại:** session sau bắt đầu code V5-15A theo 8 bước trên, KHÔNG cần hỏi lại user về những quyết
định đã tự chốt ở trên (COMMAND+GATE trước AGENT, vị trí file mới, thứ tự 8 bước) — chỉ hỏi nếu gặp một
quyết định kiến trúc thật sự mới phát sinh khi code (mirroring đúng kỷ luật đã dùng suốt session này).

## V5-15A — Real composition (branch `feat/v5-15a-real-composition`, merged 2026-09-11, PR #13, merge
commit `d8da9f3`, CI 6/6 xanh)

### Bối cảnh

Bắt tay code trực tiếp theo kế hoạch 8 bước đã có sẵn trong mục "V5-15A — Báo cáo nghiên cứu chi tiết"
ở trên — không cần hỏi lại user về các quyết định tự chốt trước đó.

### Nghiên cứu / phát hiện trong lúc code

Kế hoạch nghiên cứu trước đó đúng về hầu hết API thật (`CommandNodeExecutor`/`GateNodeExecutor`/
`NodeExecutorRouter`/`EvaluateCompletionCandidate`/`CreateReleaseSet`/`SealReleaseSet`), nhưng có 3 phát
hiện kiến trúc THẬT chỉ lộ ra khi chạy hệ thống thật end-to-end — đúng tinh thần "no DB shortcut": nếu
seed DB trực tiếp thì sẽ không bao giờ chạm phải các ràng buộc này.

1. **`CreateRootWorkItem` không bao giờ populate `EffectiveScope` thật.** Đọc code xác nhận:
   `tx.Work().AddEffectiveScope` chỉ có 2 caller trong toàn bộ codebase — `CreateChildWorkItem`
   (`internal/app/work/commands.go:580`) và `ApproveScopeExpansion`'s own reactivation
   (`internal/app/runtime/scope_expansion.go:367`, cần một AGENT node thật để trigger). Vì PR A tự quyết
   định "COMMAND+MACHINE_GATE only, chưa cần AGENT", giải pháp thật (không phải shortcut) là dùng
   `appwork.CreateChildWorkItem` — một production command thật khác — cho WorkItem thực sự chạy Run,
   trong khi ROOT WorkItem chỉ giữ vai trò neo TaskFamily/WorkspaceSet.
2. **PathScopes `["**"]` không phải wildcard thật.** `internal/app/scopeguard/guard.go`'s own `isAllowed`
   so khớp PathScope như MỘT PREFIX CHUỖI LITERAL, không phải glob — `"**"` chỉ khớp path bắt đầu đúng
   bằng `"**/"`. Các fixture khác trong repo "lách" được vì dùng diff giả với path viết tay kiểu
   `"**/src/main.go"` (xem `agent_node_executor_test.go`'s own `defaultInScopeDiff` — comment ở đó đã ghi
   rõ điều này). Với diff THẬT từ git thật, phải dùng `PathScopes: nil` (rỗng) — convention "không giới
   hạn path" mà `normalizePathScopes` tự xác nhận (`len==0 -> nil, nil`, không lỗi).
3. **`GateNodeExecutor`'s own strict-read-only diff check là ràng buộc TOÀN WorkItem, không phải riêng
   script của gate.** `buildEvidence(strictReadOnly=true)` yêu cầu diff trên MỌI mount Attempt resolve
   (không chỉ mount script gate tham chiếu) phải HOÀN TOÀN RỖNG so với `manifest.BaseRevisionSet` — một
   giá trị PIN MỘT LẦN DUY NHẤT lúc `StartWorkflowRun` và dùng lại KHÔNG ĐỔI cho mọi NodeRun trong cùng
   Run (`schedule.go:299`, không có "latest revision" nào cập nhật giữa chừng). Vì MACHINE_GATE mount MỌI
   repo trong `EffectiveScope` của WorkItem (không chỉ repo nó tham chiếu), nên NẾU maker (test_a) ghi bất
   kỳ thay đổi nào vào repo-a (dù có commit hay không, dù có write scope hay không), gate_b LUÔN LUÔN fail
   strict-read-only — đã tự kiểm chứng thực nghiệm cả hai cách (ghi chưa commit → SCOPE_VIOLATION; ghi rồi
   `git commit` thật → vẫn fail, đổi thành VALIDATION_FAILED vì `staleMountRevision` phát hiện HEAD đã
   khác pin). Kết luận: một MACHINE_GATE không thể nào hợp lệ quan sát thay đổi CÙNG repo mà một node
   trước đó (COMMAND/AGENT) đã mutate TRONG CÙNG MỘT RUN — đây là ràng buộc cứng của hệ thống, không phải
   bug. Giải pháp thật cho happy path: maker ghi output ra một file NGOÀI git worktree hoàn toàn (một path
   tuyệt đối dưới fixture root, truyền vào cả 2 script qua ArgvLiteral) — gate script thật vẫn spawn thật,
   đọc thật file đó, trả PASS/FAIL thật dựa trên sự tồn tại thật của nó — còn repo-a giữ nguyên sạch suốt
   Run. ReleaseSet's own real Base/Result SHA khác nhau thật thì lấy từ MỘT COMMIT THẬT làm SAU khi Run đã
   đạt VERIFYING (lúc đó không còn NodeRun nào đọc lại revision nữa nên an toàn) — test tự ghi một file
   "release-note.txt" thật vào working directory thật rồi gọi `gitworktree.Provider.CreateLocalCommit`
   (V5-10A's own primitive thật) để tạo commit thật.

**Bài học tự rút ra (đã lưu vào memory):** không thể lường trước những ràng buộc kiểu này chỉ bằng
research/đọc code trước — chỉ có cách chạy thật, đọc lỗi thật (log domain event), rồi thêm print tạm
thời trực tiếp vào production code để xác nhận đúng nhánh lỗi trước khi sửa design của test, mới lộ ra
được. Việc user cấm "sửa DB trực tiếp để tạo kết quả cần kiểm chứng" chính là thứ đã ép phải tìm ra 3
phát hiện trên — nếu seed thẳng bằng `seedRunEvidence`-style helper (như `completion_policy_test.go` đã
làm) thì sẽ không bao giờ chạm phải các ràng buộc thật này.

Một thiết kế trung gian đã thử rồi bỏ: chèn một APPROVAL node giữa test_a và gate_b làm điểm đồng bộ để
gọi `CreateLocalCommit` real-time (trước khi phát hiện #3 ở trên đầy đủ) — bị loại bỏ vì không giải
quyết được gì (pin revision đã cố định từ lúc `StartWorkflowRun`, APPROVAL không thay đổi điều đó).

### Quyết định (tự quyết định trong phạm vi "Router + executors + CompletionPolicy + ReleaseSet" đã được
user chốt, không cần hỏi lại)

- Package mới `internal/integration/v5accept` (không patch `runtimeengine_test.go`, đúng như research
  handoff đã tự quyết định trước).
- Document tối giản: START -> COMMAND(test_a, maker) -> MACHINE_GATE(gate_b, checker) -> END. Không có
  AGENT/WAIT/APPROVAL/FORK.
- Marker file ngoài git worktree — quyết định kỹ thuật để thoả mãn ràng buộc #3 ở trên, không đổi phạm vi
  PR (vẫn là COMMAND+MACHINE_GATE thật, spawn thật, verify thật).
- `fixture_test.go` (harness dùng chung) tách riêng khỏi `happy_path_test.go` (kịch bản riêng của PR A)
  — đúng yêu cầu "xây reusable scenario harness cho các PR sau" trong contract.

### Thực hiện

- `internal/integration/v5accept/fixture_test.go` (harness dùng chung): `v5AcceptFixture` (real
  `*sqlite.Store`, real `gitworktree.Provider`, real filesystem `ArtifactStore`, real
  `processadapter.Supervisor`, real `secretenv.Resolver`); `newV5AcceptFixture`/`restart` (close+reopen
  thật); `createRootWorkItem`/`createChildWorkItem` (2 production command thật);
  `repositoryWorkspaceHandle`; `registerHandlers` (đăng ký ĐỦ mọi real V4/V5 job handler, executor do
  caller truyền vào — luôn là `*runtime.NodeExecutorRouter` thật); `newCommandExecutor`/`newGateExecutor`
  (một `*sqlite.Store` thoả mãn cấu trúc cả `WriteLeaseManager`/`CheckpointStore`/
  `InterruptionRecoveryStore`/`WorkspaceReconciler`); `startPool` (real `workerpool.Pool`,
  Concurrency:1); polling helper (`waitForJobState`/`waitForNodeRunState`/`waitForRunState`, tự dump
  toàn bộ domain event trace khi timeout — tiện cho V5-15B/C/D/E debug sau này); publishing helper cho
  Policy/Skill/Command/Gate/WorkflowVersion (mirror `internal/integration/runtimeengine_test.go`'s own
  pattern, viết lại riêng vì package mới); `v5AcceptScripts(markerPath)` — sinh script `.bat` (Windows) /
  `.sh` (Unix) theo `runtime.GOOS` tại thời điểm test chạy (mỗi CI job build/chạy trên đúng OS của nó,
  matrix windows-latest/ubuntu-latest — đã tự thực nghiệm xác nhận `.bat` chạy trực tiếp qua
  `exec.Command` không cần shell wrapper, Windows tự fallback qua COMSPEC).
- `internal/integration/v5accept/happy_path_test.go`: `TestV5AcceptHappyPath_
  RealCompositionReachesSucceededAndSurvivesRestart` — publish Skill (2 resource: maker+gate script)/2
  Command/1 Gate/2 Policy (Attempt+Permission)/1 CompletionPolicy (yêu cầu CẢ `COMMAND_EXECUTION` VÀ
  EvidenceKey tự đặt của gate) + WorkflowVersion (CompletionPolicyRef pin thật, DependencyManifest pin
  thật) → real Router(Command+Gate) → real pool chạy start->test_a->gate_b->end tới VERIFYING → real
  `CreateLocalCommit` (sau VERIFYING) → real `CreateReleaseSet`+`SealReleaseSet` (SHA thật, khác nhau
  thật) → real `EvaluateCompletionCandidate` → assert PASS/SUCCEEDED/DONE → assert state (Run/WorkItem/
  mọi NodeRun/DecisionArtifact/ReleaseSet/mọi Evidence's own Artifact re-verify qua real
  `ArtifactStore.Verify`/COMPLETION_DECIDED event) → `stopPool()` + `f.restart()` (real close/reopen
  sqlite) → assert lại y hệt qua CÙNG một helper.

### Test

- `go vet ./...` sạch.
- `go build ./...` sạch.
- `go test ./internal/integration/v5accept/... -run TestV5AcceptHappyPath -v` pass, chạy lặp lại 3 lần
  liên tiếp không flake (~3.6-3.7s/lần).
- `go test ./... -count=1` (toàn bộ repo) pass 100% — không có package nào bị regress.
- `go run ./cmd/docs-coverage-check` pass (`debt = 0`).
- `gofmt -l` sạch trên 2 file mới.
- Race detector cục bộ không chạy được trên máy dev (không có cgo) — CI's own Linux race job
  ("Linux race and stability (V0-12)") tự chạy và pass.

### Verify

- Log sự kiện domain (`RepositoryRegistered` → `RootWorkItemCreated` → `ChildWorkItemCreated` →
  `WorkflowRunStarted` → `NODE_ROUTED`/`NODE_SCHEDULED`/`EXECUTION_ATTEMPT_FINALIZED` cho cả test_a lẫn
  gate_b → `RUN_FAILED` khi còn bug, biến mất khi đã đúng) được dùng trực tiếp để debug 3 phát hiện kiến
  trúc ở trên — không đoán, đọc log thật + đọc code thật (`gate_node_executor.go`, `scopeguard/guard.go`,
  `schedule.go`) rồi thêm print tạm thời trực tiếp vào production code để xác nhận đúng nhánh lỗi trước
  khi sửa design của test — mọi print tạm thời đã revert sạch trước khi commit, xác nhận qua
  `git diff --stat` không còn gì trên `internal/app/runtime/gate_node_executor.go`.

### Kết quả

PR #13, branch `feat/v5-15a-real-composition`, 2 commit (`781c004` code chính, `6813b0d` polish comment
nhỏ), CI 6/6 xanh cả 2 lần chạy (không cần rerun), squash-merge vào `master` — merge commit `d8da9f3`,
2026-09-11. `internal/integration/v5accept` package mới, reusable scenario harness sẵn sàng cho
V5-15B/C/D/E dùng lại.

**Việc còn lại:** V5-15B (completion integrity: claim-done/gate-fail/artifact-tamper + false-completion
oracle) — theo đúng contract 5 phần đã chốt với user, tiếp tục tự động không cần hỏi lại trừ khi gặp
quyết định kiến trúc thật sự mới.

## V5-15B — Completion integrity (branch `feat/v5-15b-completion-integrity`, merged 2026-09-11, PR #15,
merge commit `eee4419`, CI 6/6 xanh)

### Bối cảnh

V5-15B (phần 2/5 trong contract 5 phần user đã chốt cho V5-15) yêu cầu: "Claim-done. Gate failure.
Artifact tamper. False-completion oracle: không được DONE nếu thiếu bất kỳ authoritative condition nào."
Bắt đầu ngay sau khi V5-15A merge xong (PR #13 code + PR #14 docs), theo đúng auto-continue doctrine đã
thiết lập từ các task V5 trước — không dừng lại hỏi vì không có quyết định nào cần user tại thời điểm bắt
đầu.

### Nghiên cứu — "claim done" cần AGENT thật lần đầu tiên

Không giống V5-15A (chỉ cần COMMAND+MACHINE_GATE), "claim done" đòi hỏi một node có kênh "tự tuyên bố"
thật — COMMAND/MACHINE_GATE không có kênh này (outcome của chúng LUÔN xuất phát từ exit code thật,
`CommandNodeExecutor.classify` không bao giờ nhận "proposed outcome"). Chỉ AGENT mới có transcript thật
với `<agentkit-outcome>` marker — đúng thứ một false-completion oracle phải không bao giờ tin một mình.
Tự quyết định (không cần hỏi): dùng real `claude.Adapter` + real `cmd/fake-claude` binary (build 1 lần
qua `go build`, mirror `internal/spikeacceptance/registry_test.go`'s own `buildScenarioBinaries`), driven
qua `AGENTKIT_HELPER_MODE=outcome-success`/`AGENTKIT_HELPER_OUTCOME=done` — đúng "recorded fake" layer
contract đã cho phép, không phải live network call.

### Phát hiện kiến trúc lớn nhất phiên này: real AGENT dispatch CHƯA BAO GIỜ hoạt động thật

Khi chạy real `AgentNodeExecutor` → real `claude.Adapter` lần đầu tiên (chưa ai từng làm việc này — mọi
test khác hoặc tự xây `AgentExecutionRequest` tay bỏ qua `AssembleAgentExecutionRequest`, hoặc dùng
`scriptedNodeExecutor`/`fake.AgentExecutor` bỏ qua adapter thật), gặp lỗi thật: `claude.Adapter`'s own
`validateRequest` từ chối NGAY với "context snapshot id is required". Đào sâu bằng debug print tạm thời
thêm trực tiếp vào `agent_node_executor.go` (revert sạch trước khi commit, xác nhận qua `git diff --stat`)
phát hiện: `AssembleAgentExecutionRequest` (V5-08B0, ĐÃ MERGE từ lâu) KHÔNG BAO GIỜ populate 4 field thật:
`Prompt`, `WorkingDirectory`, `Timeout`, `Model` — tất cả đều rỗng/0 khi tới tay adapter thật.
`AgentNodeExecutor.Execute` cũng KHÔNG bổ sung chúng. Đây là gap production thật, không phải lỗi thiết kế
test — **real AGENT dispatch qua đường production CHƯA TỪNG hoạt động cho tới session này**, dù đã merge
từ V5-08B0.

**Đã hỏi user trước khi sửa production code** (đúng kỷ luật "chỉ hỏi khi gặp quyết định kiến trúc thật sự
mới") — user chọn "Fix it now inside V5-15B" và cho MAPPING CHÍNH XÁC (trả lời verbatim, đã lưu vào memory
`agent-kit-v5-15-acceptance-gate-contract.md`):

| Field | Authoritative source |
|---|---|
| Prompt | Canonical rendered content của InstructionArtifact đã pin |
| WorkingDirectory | Root của resolved writable workspace/mount |
| Timeout | Effective timeout từ ExecutionProfile đã pin |
| Model | Effective model từ execution/agent profile đã pin |
| ContextSnapshotID | ID của CÙNG context snapshot mà structured field đã mang |

Ràng buộc: thiếu giá trị bắt buộc phải fail closed bằng typed error; KHÔNG default, KHÔNG đọc live profile
config tại dispatch time.

### Quyết định

- Fix production code NGAY trong V5-15B (theo lựa chọn của user), giữ thành COMMIT RIÊNG tách khỏi commit
  scenario (theo đúng yêu cầu "Keep the production fix as a separate commit within V5-15B").
- "Artifact tamper" KHÔNG mở rộng `EvaluateCompletionCandidate` để re-verify artifact bytes — tự quyết
  định (không cần hỏi, cùng logic với V5-15A's own "prove existing mechanism fires, đừng build capability
  mới"): `gatherCompletionCandidateEvidence` chỉ đọc DB row, việc thêm re-verify artifact vào completion
  path là một quyết định kiến trúc RIÊNG, không nằm trong yêu cầu "artifact tamper" ban đầu — ghi nhận
  trung thực làm một assertion thật trong test, không tự ý mở rộng phạm vi.

### Thực hiện phần production fix (commit `2f676a2`)

- `internal/app/runtime/execute.go`: thêm `Model string` vào `resolvedExecutionProfileView` (partial view,
  trước đây không decode field này vì chưa ai cần).
- `internal/app/runtime/assemble_execution_request.go`: thêm `timeoutSeconds`/`model` vào
  `assembledRequestInputs`, thread qua từ `profile.TimeoutSeconds`/`profile.Model` (đã load sẵn từ CÙNG
  DecisionArtifact function này vốn đã đọc — chỉ là chưa từng propagate ra ngoài). Return construction của
  `AssembleAgentExecutionRequest` giờ set `Prompt: string(contentJSON)` (chính xác bytes vừa Put làm
  InstructionArtifact — không đọc lại qua `store.Open`, tránh round-trip thừa), `Timeout`, `Model`,
  `ContextSnapshotID: domainruntime.ContextSnapshotID(gathered.snapshotID)` (cast từ CÙNG snapshot ID thật,
  không phải 2 hệ thống context riêng biệt — `ContextSnapshotPin`'s own doc comment đã ghi rõ 2 field này
  cố ý tách biệt, không bridge).
- `internal/app/runtime/agent_node_executor_resources.go`: `resolveAgentWorkingDirectory(mounts)` — mount
  WRITE đầu tiên (deterministic, đã sort theo RepositoryID) làm WorkingDirectory thật.

**Phát hiện phụ khi chạy full test suite sau fix:** 2 test CHECKER-role ĐÃ MERGE trước đó
(`TestAgentNodeExecutor_CheckerRole_MutatingDiff_RejectsAsScopeViolation`,
`TestAgentNodeExecutor_CheckerRole_EmptyDiff_Succeeds`) FAIL vì CHECKER-role AGENT (V5-12, luôn bị force
read-only) CẤU TRÚC không bao giờ có write mount — "no write mount" ở đây không phải lỗi, mà là trạng thái
hợp lệ đã có sẵn trong hệ thống (test còn assert rõ "empty diff succeeds" — một positive path thật). Tự
quyết định (không cần hỏi lại — đây là fix một regression mình vừa gây ra, không phải quyết định kiến trúc
mới): khi không có write mount, dùng `scratchDirectory()` — CHÍNH cơ chế `GateNodeExecutor` đã dùng cho
đúng tình huống tương tự (executor read-only-by-design vẫn cần cwd thật nhưng không có mount ghi). Không
phải phát minh mới — tái dùng precedent đã có sẵn trong chính codebase này. Sau fix:
`go test ./internal/app/runtime/...` pass 100%, `go test ./...` toàn repo pass 100%.

### Thực hiện phần scenario (commit `873b583`)

- `internal/integration/v5accept/agent_harness_test.go` (harness mới, dùng chung cho B/C/D/E): build real
  `cmd/fake-claude` 1 lần (`sync.Once`); `newClaudeAdapter`/`newAgentRegistry`/`newAgentExecutor`;
  `registerAgentBuild` (real AdapterBuild pin từ real `Capabilities()` probe + real content hash của binary
  thật — khớp CHÍNH XÁC với `adapterbuild.VerifyNoDrift`'s own re-probe logic, không fabricate); publish
  Context policy + AgentProfile thật (`ProviderKey: string(ports.ProviderClaude)`).
- `internal/integration/v5accept/claim_done_test.go`: `TestV5AcceptFalseCompletionOracle` — 3 case thật
  (agent claim alone; agent claim + real gate FAIL; agent claim + real gate PASS), mỗi case fixture riêng,
  tally cuối cùng assert đúng 1/3 case reach DONE. Document: START→AGENT(maker, real claim "done")→
  [MACHINE_GATE tuỳ case]→END. Gate dùng fixed-verdict script (không cần check output thật của AGENT vì
  AGENT — khác COMMAND — không để lại artifact filesystem nào, `fake-claude` chỉ emit protocol qua
  stdout). WorkItem dùng `RepositoryRead` (không Write) — AGENT/Gate trong scenario này không mutate gì.
- `internal/integration/v5accept/artifact_tamper_test.go`:
  `TestV5AcceptArtifactTamper_RealVerifyDetectsRealCorruption` — tái dùng NGUYÊN VẸN document/script của
  V5-15A (`v5AcceptHappyPathDocument`/`v5AcceptScripts`, cùng package) chạy tới VERIFYING, lấy Evidence
  Artifact thật của gate_b, TAMPER bytes thật trên đĩa tại đúng content-addressed path
  (`f.artifactObjectPath` — helper mới, tái tạo đúng sharding scheme của
  `internal/adapters/artifactstore`), assert `Verify`/`Open` thật đều reject. Đồng thời ghi nhận trung
  thực một giới hạn ĐÃ CÓ SẴN (không phải gap mới, không tự ý sửa): `gatherCompletionCandidateEvidence`
  chỉ đọc DB row (Kind/Verdict), không bao giờ re-verify artifact bytes — nên completion evaluation vẫn
  PASS dù artifact bị tamper. Test tự assert rõ ràng invariant này (không giả vờ đã sửa).

### Test

- `go vet ./...` sạch.
- `go build ./...` sạch.
- `go test ./internal/app/runtime/...` pass 100% (bao gồm 2 test CHECKER-role đã regress rồi được fix).
- `go test ./internal/integration/v5accept/...` pass, chạy lặp lại 3 lần liên tiếp không flake.
- `go test ./...` (toàn repo) pass 100%, chạy lại lần cuối trước khi mở PR cũng pass 100%.
- `go run ./cmd/docs-coverage-check` pass (`debt = 0`).
- `gofmt -l` sạch trên mọi file mới/sửa.

### Verify

- Debug print tạm thời thêm trực tiếp vào `agent_node_executor.go` (2 lần: 1 lần in toàn bộ request, 1 lần
  in riêng từng field) để xác định CHÍNH XÁC field nào rỗng trước khi quyết định fix — cách làm giống hệt
  V5-15A (đọc log thật, đọc code thật, không đoán). Đã revert sạch, `git diff --stat` xác nhận không còn gì
  dư trên các file production trước khi commit.
- CI 6/6 xanh ngay lần chạy đầu (bao gồm "Linux race and stability (V0-12)", 12m48s — quan trọng vì PR này
  sửa code liên quan tới concurrency/dispatch thật).

### Kết quả

PR #15, branch `feat/v5-15b-completion-integrity`, 2 commit (`2f676a2` production fix riêng,
`873b583` scenario code), CI 6/6 xanh ngay lần đầu, squash-merge vào `master` — merge commit `eee4419`,
2026-09-11.

**Việc còn lại:** V5-15C (recovery/availability: crash/checkpoint recovery, provider loss, adapter drift,
isolation unavailable; xác minh replay không tạo trùng Attempt/activation/artifact/event) — theo đúng
contract 5 phần, tiếp tục tự động không cần hỏi lại trừ khi gặp quyết định kiến trúc thật sự mới.

## V5-15C — Recovery and availability

### Bối cảnh

Phần 3/5 của V5-15 theo đúng contract 5 phần user đã chốt (verbatim trong memory
`agent-kit-v5-15-acceptance-gate-contract.md`): "Crash/checkpoint recovery. Provider loss. Adapter drift.
Isolation unavailable. Xác minh replay không tạo trùng Attempt, activation, artifact hoặc event." Tiếp tục
tuân thủ 2 quy tắc bắt buộc xuyên suốt V5-15: không sửa DB trực tiếp để tạo ra kết quả cần kiểm chứng; mọi
scenario phải đi qua đúng production command/router/repository path thật. Xây dựng trên `v5AcceptFixture`
và `agent_harness_test.go` đã có sẵn từ V5-15A/B — không tự dựng lại wiring.

### Nghiên cứu

Trước khi code, chạy một background Explore agent để nghiên cứu 4 mảng riêng biệt (không tự đoán) — kết quả
cho ra đúng API thật cần dùng:

- **Crash/checkpoint recovery**: `runtime.StartupRecoveryScan(ctx, uow, ids)` (`recovery_reaper.go:217`) là
  entrypoint thật để trigger recovery — enqueue job `RECOVERY_REAPER` (idempotent). `RecoveryReaperHandler`
  đã được đăng ký sẵn trong `fixture_test.go`'s `registerHandlersWithAgents` — không cần wiring mới. Cơ chế
  "crash" thật (không fake): dừng hẳn một `workerpool.Pool` đang chạy (không phải xoá row DB) trong lúc một
  job thật vẫn đang in-flight, để lease của job đó tự hết hạn thật theo đồng hồ thật.
- **Provider loss**: không có sẵn mode "fail" nào trong `cmd/fake-claude`. Con đường thật, đã có sẵn trong
  code: `agentregistry.Registry.Resolve` trả về `agentregistry.ErrUnknownProvider` khi registry không chứa
  provider mà `AdapterBuild` đã pin — đây là lỗi kỹ thuật thật ở `Handle()` (bị retry như mọi lỗi job khác),
  KHÔNG phải một business outcome BLOCKED.
- **Adapter drift**: `adapterbuild.VerifyNoDrift` (`internal/app/adapterbuild/drift.go:50`) re-probe live
  executor thật, dựng `CandidateTuple` mới, so `ID()` với bản đã pin. Một pin bị lệch (dù chỉ 1 field, ví dụ
  `ExecutableContentHash` bị thêm hậu tố) sẽ luôn mismatch thật khi so với binary thật không đổi.
- **Isolation unavailable**: có sẵn implementation THẬT (không phải fake) —
  `process.IsolationChecker{}`/`NewIsolationChecker()` (`internal/adapters/process/isolation.go`) — luôn trả
  `ErrIsolationEnforcementUnavailable` cho tier `EnforcedIsolated`, không cần I/O. `fixture_test.go` đang
  hard-code fake `fake.IsolationEnforcementChecker{}` inline — cần một hook mới để dùng được checker thật.

### Quyết định (tự quyết, không cần hỏi lại — không phải fork kiến trúc mới)

- Thêm `registerHandlersWithIsolation` vào `fixture_test.go`, `registerHandlersWithAgents` trở thành một
  wrapper mỏng gọi hàm này với fake mặc định — mở rộng tối thiểu, đúng tinh thần "mọi PR V5-15 sau tự xây
  trên fixture chung" mà chính package doc comment của file này đã ghi.
  "Isolation unavailable" và "Adapter drift" chỉ cần 1 node đơn (không cần multi-node) — đủ để chứng minh
  admission reject thật trước khi spawn.
- "Isolation unavailable" dùng COMMAND node (đơn giản nhất, không cần AGENT/registry). "Adapter drift" và
  "Provider loss" bắt buộc dùng AGENT node — vì admission chỉ thật sự chạy drift-check/provider-resolve cho
  node AGENT (COMMAND/GATE không có `AdapterBuildID` nên tự động pass hai check này).
- "Provider loss" mô phỏng thật: pin một `AdapterBuild` ĐÚNG (không drift), nhưng đưa vào
  `ExecuteNodeHandler` một `agentregistry.Empty()` — registry thật KHÔNG chứa provider mà pin đó chỉ tới —
  đúng tình huống thật một operator gỡ nhầm provider khỏi config hoặc process đã crash mà chưa đăng ký lại.
  Assert bằng cách theo dõi `f.store.DebugListJobsByKind` tới khi job EXECUTE_NODE thật chuyển state DEAD
  sau khi hết `MaxClaims` (=3) — chứng minh fail-closed thật (không silent hang, không silent success), chứ
  không assert qua NodeRun state (NodeRun/Attempt không bao giờ rời QUEUED trong case này, vì lỗi xảy ra ở
  Phase 1 trước khi transaction Phase 2 từng chạy).

### Thực hiện

- `internal/integration/v5accept/fixture_test.go`: thêm `registerHandlersWithIsolation(executor, idPrefix,
  agents, isolation ports.IsolationEnforcementChecker)`; `registerHandlersWithAgents` giờ chỉ gọi hàm này
  với `fake.IsolationEnforcementChecker{}`.
- `internal/integration/v5accept/isolation_unavailable_test.go`:
  `TestV5AcceptIsolationUnavailable_RealAdmissionRejectsBeforeSpawn` — 1 COMMAND node thật, permission
  policy pin `IsolationTierEnforcedIsolated` (đã có sẵn từ `v5AcceptPermissionPolicyDocument`), dùng
  `process.NewIsolationChecker()` thật (không fake). Đợi NodeRun `test_a` reach `NodeRunBlocked`, assert
  Attempt.TerminationReason = `ISOLATION_ENFORCEMENT_UNAVAILABLE`, WorkItemBlocker thật mở đúng type/state,
  và — bằng chứng mạnh nhất — file marker mà script maker LẼ RA phải ghi thật KHÔNG hề tồn tại trên đĩa,
  chứng minh process thật chưa từng được spawn.
- `internal/integration/v5accept/adapter_drift_test.go`: `registerDriftedAgentBuild` (copy
  `registerAgentBuild` của `agent_harness_test.go`, chỉ đổi đúng 1 dòng: `ExecutableContentHash: contentHash
  + "-drifted"` sau khi đã hash file thật). `TestV5AcceptAdapterDrift_RealAdmissionRejectsMismatchedPin` — 1
  AGENT node thật pin build lệch này, `fake-claude` thật vẫn có thể chạy ("outcome-success" mode) nhưng
  không bao giờ có cơ hội — admission thật reject trước. Assert Attempt.TerminationReason =
  `ADAPTER_BUILD_DRIFT`, WorkItemBlocker mở đúng type.
- `internal/integration/v5accept/provider_loss_test.go`:
  `TestV5AcceptProviderLoss_RealDispatchFailsClosedUntilJobDies` — AgentNodeExecutor wire với registry THẬT
  (có provider), nhưng `ExecuteNodeHandler` wire với `agentregistry.Empty()` — một sai lệch thật giữa 2
  thành phần, mô phỏng đúng "hai phần hệ thống trỏ nhầm registry khác nhau". Poll
  `f.store.DebugListJobsByKind(ExecuteNodeJobKind)` tới khi job thật DEAD; assert Attempt/NodeRun vẫn QUEUED.
- `internal/integration/v5accept/crash_recovery_test.go`:
  `TestV5AcceptCrashRecovery_RealRetryReplaysWithNoDuplicates` — phần khó nhất. Document 1 COMMAND node,
  script thật tự quyết định hành vi dựa trên side-effect thật trên đĩa (không phải flag test): lần chạy đầu
  ghi marker "started" rồi sleep thật lâu; lần chạy sau thấy marker đã có thì ghi "done" và thoát ngay. Drive
  attempt tới RUNNING thật qua pool1 (`LeaseTTL=2s, ShutdownGrace=2s` — đúng default của `f.startPool`, cố ý
  không tự chỉnh nhanh hơn để giữ tính đại diện cho crash thật ngoài production), đợi marker "started" xuất
  hiện thật trên đĩa (bằng chứng script thật đã bắt đầu chạy), rồi gọi `stopPool1()` — pool tự escalate
  thật sau ShutdownGrace, `ProcessSupervisor.Run` thấy ctx của chính nó bị cancel và thật sự kill process
  tree thật. Poll `ListOrphanedRunningExecutionAttempts` thật (không sleep mù) tới khi thấy attempt orphan.
  Gọi `StartupRecoveryScan` thật, start pool2 (idPrefix mới) — `RecoveryReaperHandler` đã đăng ký sẵn tự
  classify LOST, tự retry (AttemptNumber+1), script thật chạy lại lần 2 và finish ngay. Đợi Run VERIFYING,
  gọi `EvaluateCompletionCandidate` thật, assert PASS/SUCCEEDED/DONE. Assert cuối: đúng 2 ExecutionAttempt
  (không hơn), đúng 2 job EXECUTE_NODE (`DebugListJobsByKind`), đúng 1 event
  `EXECUTION_ATTEMPT_TERMINATED` cho attempt bị crash, đúng 1 event `EXECUTION_ATTEMPT_FINALIZED` cho attempt
  retry, đúng 0 event `RECOVERY_DECISION_RECORDED` (RETRY không ghi event này, theo đúng doc comment của
  chính `recovery_reaper.go`), đúng 1 Evidence `COMMAND_EXECUTION` — không nơi nào bị trùng.

**Phát hiện thật trong lúc code (Windows script debug, tự sửa không cần hỏi lại — bug tự gây ra, không phải
quyết định kiến trúc)**: lần đầu dùng `ping -n 61 127.0.0.1 >nul` làm cơ chế sleep trên Windows — thực tế đo
được KHÔNG hề pace ~1s/gói như kỳ vọng, script kết thúc gần như ngay lập tức khiến "crash" không bao giờ bắt
kịp lúc script còn chạy. Đổi sang `powershell -Command "Start-Sleep..."` gọi bằng tên trần — vẫn fail nhanh
tương tự. Debug bằng cách chạy tay script y hệt ngoài framework (`Start-Process` + đo thời gian) xác nhận
script tự nó chạy đúng — vậy lỗi nằm ở cách framework spawn process thật. Đọc lại `command_node_executor.go`
xác nhận: `ProcessSpec.InheritedEnvironment = inputs.doc.EnvAllowlist`, mà `CommandDocument` của scenario
này chưa từng set `EnvAllowlist` — nghĩa là process con thật không hề có PATH, nên lệnh `powershell` gọi bằng
tên trần fail silent (script không check exit code, `exit /b 0` vô điều kiện nuốt luôn lỗi). Fix thật: gọi
`powershell.exe` bằng đường dẫn tuyệt đối cố định
(`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`), không phụ thuộc PATH nữa — sau đó `stopPool1`
đo được mất đúng ~7s (2s ShutdownGrace + tới 5s grace period kill process thật), khớp hoàn toàn với phân
tích lý thuyết từ việc đọc `pool.go`/`supervisor.go` trước khi code.

### Test

- `go build ./...`, `go vet ./...` sạch.
- `go test ./internal/integration/v5accept/... -v -count=1`: cả 7 test (3 từ A/B + 4 mới) đều pass.
- `TestV5AcceptCrashRecovery_RealRetryReplaysWithNoDuplicates` chạy lặp lại 3 lần liên tiếp không cache
  (`-count=1`), thời gian ổn định ~9.3s mỗi lần — không flake.
- `TestV5AcceptProviderLoss_RealDispatchFailsClosedUntilJobDies` mất ~9s (khớp tính toán lý thuyết: 3 lần
  claim × LeaseTTL 2s trước khi job DEAD).
- `go test ./... -count=1` toàn repo pass 100%.
- `gofmt -l` sạch trên mọi file mới/sửa.

### Verify

- Toàn bộ debug print tạm thời (đo thời gian `stopPool1`, in state job/attempt sau crash) đã revert sạch
  trước khi commit — chỉ giữ lại code test thật, không còn `t.Logf("DEBUG...")` nào sót lại.
- `git status`/`git diff --cached --stat` xác nhận chỉ đúng 5 file dự định thay đổi được stage (1 file sửa
  — `fixture_test.go`, 4 file mới) — không đụng vào ~92 file CRLF noise sẵn có.

### Kết quả

PR #17, branch `feat/v5-15c-recovery-availability`, 1 commit (`7260074`), CI 6/6 xanh ngay lần chạy đầu tiên
(bao gồm "Linux race and stability (V0-12)"), squash-merge vào `master` — merge commit `ad70d04`,
2026-09-11.

**Việc còn lại:** V5-15D (isolation and fencing: scope violation, checker write attempt, cancel giữa mutating
attempt; xác minh workspace/revision không bị promote sau khi fence hoặc policy thắng) và V5-15E (full
conformance matrix — mốc hoàn thành thật sự của V5-15) — theo đúng contract 5 phần, tiếp tục tự động không
cần hỏi lại trừ khi gặp quyết định kiến trúc thật sự mới.

## V5-15D — Isolation and fencing

### Bối cảnh

Phần 4/5 của V5-15 theo đúng contract 5 phần user đã chốt: "Scope violation. Checker write attempt. Cancel
giữa mutating attempt. Xác minh workspace/revision không bị promote sau khi fence hoặc policy thắng." Tiếp
tục xây trên `v5AcceptFixture`/`agent_harness_test.go` đã có từ V5-15A/B/C, tuân thủ 2 quy tắc bắt buộc
xuyên suốt V5-15 (không sửa DB trực tiếp; mọi scenario đi qua đúng production path thật).

### Nghiên cứu

Chạy một background Explore agent nghiên cứu 4 mảng trước khi code — kết quả cho ra API thật cần dùng:

- **Real cancel-run command**: `runtime.CancelRun(ctx, uow, ids, CancelRunRequest{RunID, Actor, Reason,
  CorrelationID})` (`cancel_run.go`) — real entrypoint, đã tự enqueue `CANCEL_RUN_COORDINATOR` job.
- **"Promote" nghĩa cụ thể trong codebase này**: không có state "PROMOTED" nào — khái niệm gần nhất là
  `RepositoryWorkspace.State == RELEASED` qua `ReleaseRepositoryWorkspace`, mà chính port này TỪ CHỐI vĩnh
  viễn một workspace đã QUARANTINED (`ErrWorkspaceQuarantined`, không có đường quay lại tại chỗ —
  `RecreateRepositoryWorkspace` luôn tạo generation MỚI). Đây chính là "never promoted" cần chứng minh.
- **Phát hiện quan trọng (load-bearing)**: `worker.ReconcileMutatingAttempt` so sánh HEAD SHA thật
  (`gitworktree.Provider.CaptureRevision`) — một write CHƯA COMMIT không hề đổi giá trị này (khác với diff
  `git status` mà scope-check dùng). Nghĩa là script thật của scenario "cancel giữa mutating attempt" BẮT
  BUỘC phải `git commit` thật (đổi HEAD) thì reconciliation mới quan sát được mutation thật — chỉ ghi file
  chưa commit sẽ reconcile CLEAN, làm hỏng cả scenario một cách âm thầm.
- **`scopeguard.isAllowed`**: PathScopes rỗng cho phép TOÀN BỘ repo; cần một PathScopes THẬT hẹp hơn
  (`["allowed"]`) để một diff thật ngoài phạm vi đó mới thực sự vi phạm.
- **`cmd/fake-claude` không có side-effect filesystem thật nào** (ngoài file capture nội bộ) — cần thêm một
  cơ chế ghi file thật, có kiểm soát (opt-in qua env var), để scenario "checker write attempt" chứng minh
  được một AGENT CHECKER thật sự ghi đè lên mount thật của nó.

### Quyết định (tự quyết — không cần hỏi lại)

- **Scope violation**: dùng COMMAND node (đơn giản nhất) — script thật ghi ra file `leaked.txt` ở gốc repo,
  trong khi WorkItem chỉ được cấp WRITE trong `["allowed"]`.
- **Checker write attempt**: bắt buộc dùng AGENT node Role=CHECKER (chỉ AGENT mới có khái niệm Role của
  V5-12; GATE luôn bị force read-only bằng cơ chế khác). Thêm `AGENTKIT_HELPER_WRITE_PATH` — một side-effect
  thật, opt-in, vào `internal/adapters/providers/fixtures.go`'s `RunFakeProviderCLI` (dùng chung cho
  `cmd/fake-claude`) để test có thể trỏ CHECKER's real spawned process ghi thật vào đúng mount thật của nó
  (không phải cwd — CHECKER's own `resolveAgentWorkingDirectory` luôn fallback về scratch dir rỗng).
- **Cancel giữa mutating attempt**: dùng COMMAND node (COMMAND tái dùng đúng `classifyCancellation`/
  `handleMutatingCancellation` của V5-08C, theo đúng "dùng đúng đường V5-08C, không có đường terminate
  riêng" đã locked từ V5-09).

### Thực hiện phần 1 — 2 scenario đơn giản (commit đầu)

- `fixture_test.go`: thêm `createChildWorkItemWithPathScopes` (PathScopes tuỳ chỉnh — `createChildWorkItem`
  giờ chỉ là wrapper mỏng gọi hàm này với `nil`).
- `scope_violation_test.go`: `TestV5AcceptScopeViolation_RealDiffRejectsOutOfScopeWrite` — COMMAND node thật
  ghi `leaked.txt` ngoài `["allowed"]`; assert Run FAILED (NON_RETRYABLE, vì `CodeScopeViolation` không nằm
  trong `RetryableErrorCodes`), Attempt.TerminationReason=SCOPE_VIOLATION, đúng 1 Attempt (không retry).
- `internal/adapters/providers/fixtures.go`: thêm `AGENTKIT_HELPER_WRITE_PATH` — nếu set, ghi thật một file
  tại đường dẫn đó trước khi emit protocol JSONL (production code không bao giờ set biến này —
  `AssembleAgentExecutionRequest` chưa từng populate `Environment` cho AGENT — nên chỉ test chủ động mới
  kích hoạt được).
- `agent_harness_test.go`: thêm `AGENTKIT_HELPER_WRITE_PATH` vào `InheritedEnvironment` của
  `newClaudeAdapter`.
- `checker_write_test.go`: `TestV5AcceptCheckerWriteAttempt_RealStrictReadOnlyDiffRejectsRealMutation` —
  AGENT CHECKER thật, `AGENTKIT_HELPER_WRITE_PATH` trỏ vào WorkingDirectory thật của mount (resolve qua
  `f.repositoryWorkspaceHandle`+`f.provider.WorkingDirectory`, đúng cách V5-15A resolve); real fake-claude
  ghi thật file `checker-mutation.txt` vào real repo mount dù mount bị force read-only; real
  `validateStrictlyReadOnlyDiffs` bắt được. Assert Run FAILED/SCOPE_VIOLATION, và — bằng chứng mạnh nhất —
  file thật đã tồn tại trên đĩa (chứng minh mutation thật đã xảy ra, không phải logic path chưa từng chạy).

Cả 2 test pass ngay lần chạy đầu.

### Thực hiện phần 2 — cancel giữa mutating attempt: phát hiện một gap production thật

Viết `cancel_mutating_test.go` (COMMAND node thật `git commit` một thay đổi thật rồi sleep, `runtime.CancelRun`
thật giữa chừng) — liên tục fail: `WorkflowRun` không bao giờ rời CANCELLING dù script bị kill thành công.
Debug thực nghiệm nhiều vòng (không đoán):

1. Nghi ngờ đầu tiên (sai): git lock contention giữa `CaptureRevision` polling của test và `git commit` của
   script — sửa bằng cách chuyển sang chờ file marker thật (giống kỹ thuật V5-15C) thay vì gọi git đồng
   thời. Không sửa được vấn đề.
2. Debug print tạm thời trực tiếp vào `execute.go`'s `pollForCancellation` — xác nhận poller THẬT SỰ phát
   hiện cancellation và gọi `cancelExec()` đúng ~1 giây sau `CancelRun`.
3. Debug print vào `CommandNodeExecutor.Execute` quanh `supervisor.Run` — xác nhận process thật bị kill
   đúng (~5.6s, khớp `defaultGracePeriod=5s`), `Supervisor.Run` trả về đúng với `result.Cancelled=true`.
4. Vậy điểm treo nằm SAU đó — trong `classifyCancellation`/`handleMutatingCancellation`. Đọc lại code phát
   hiện: **`handleMutatingCancellation` (code CŨ, viết từ V5-08C) chỉ CAS Attempt sang INDETERMINATE và
   quarantine workspace (2 lệnh không nguyên tử, mỗi lệnh tự mở transaction riêng qua
   `*sqlite.Store`), rồi trả về `ErrAttemptAlreadyTerminated` — mà `execute.go`'s `Handle()` hiểu là "đã xử
   lý xong, KHÔNG gọi `FinalizeExecutionAttempt`". Nghĩa là NodeRun không bao giờ được chuyển trạng thái,
   nên `reconcileCancellingRunTx`'s own `LiveCount==0` gate (completion.go) không bao giờ pass được, và
   WorkflowRun mắc kẹt ở CANCELLING vĩnh viễn** — một gap kiến trúc thật, có từ V5-08C, chưa từng bị phát
   hiện vì chưa ai thật sự chạy một mutating attempt bị cancel thật qua production path đầy đủ.

**Dùng `AskUserQuestion` hỏi user trước khi sửa production code** (đúng tiền lệ V5-15B's AGENT-dispatch-gap)
— trình bày phát hiện với trích dẫn file/line cụ thể, 3 lựa chọn (sửa production / document như một giới
hạn đã biết / hướng khác). User chọn sửa production, và đưa ra một **hợp đồng sửa lỗi ràng buộc bằng văn
bản, rất chi tiết** (verbatim, tiếng Việt):

> Chọn Fix production code, làm thành commit riêng trước scenario test. Terminal state đúng của NodeRun là
> CANCELLED.
> State matrix:
> Attempt → INDETERMINATE / OWNERSHIP_LOST_MUTATING
> Workspace → QUARANTINED nếu quan sát thấy mutation
> NodeRun → CANCELLED
> BranchToken → CANCELLED nếu có
> WorkflowRun → CANCELLED khi LiveCount == 0
> WorkItem → BLOCKED với blocker RUN_CANCELLED
> Cancel intent → COMPLETED
> Nên triển khai thành một cancellation-finalization boundary dùng chung cho Agent và Command executor:
> Xác nhận process đã quiesced.
> Giữ WriteLease trong lúc capture revision và tính reconciliation verdict.
> Trong một WithSerializedWrite:
> Revalidate Run đang CANCELLING và Attempt/NodeRun thuộc đúng Run.
> CAS Attempt RUNNING → INDETERMINATE.
> Persist quarantine cho mọi workspace có mutation.
> CAS NodeRun RUNNING → CANCELLED.
> Dùng terminalizeBranchTokenForCancelledNodeRunTx.
> Gọi reconcileRunTerminalityTx.
> Nếu Run đóng thành CANCELLED, chuyển cancellation intent sang COMPLETED.
> Append các event/outbox tương ứng và settle driving job.
> Commit xong mới release WriteLease.
> ErrAttemptAlreadyTerminated chỉ nên được trả sau khi toàn bộ boundary trên đã hoàn tất, thay vì ngay sau
> khi Attempt trở thành terminal.
> Điểm này quan trọng cho crash safety: nếu worker chết trước transaction, Attempt vẫn RUNNING và recovery
> reaper có thể tiếp quản; nếu transaction commit, Attempt, NodeRun, Run và cancellation intent đã nhất
> quán. Recovery reaper cũng phải gọi cùng cancellation-finalization path khi orphaned Attempt thuộc một
> Run đang CANCELLING.
> Các test cần có: Scenario V5-15D đầy đủ... Branch NodeRun terminalize đúng BranchToken... Replay không
> tạo event/blocker/quarantine trùng... Failure injection trước và sau transaction không để Run mắc ở
> CANCELLING... Cancellation intent không còn REQUESTED sau khi Run đã CANCELLED.

### Thực hiện production fix (theo đúng hợp đồng trên)

**Nghiên cứu API thật trước khi sửa** (background agent thứ 2, tránh đoán mò cho một thay đổi production
lớn): đọc toàn bộ `FinalizeExecutionAttempt` (finalize.go, mẫu cho "một transaction nguyên tử, fence, CAS
Attempt+NodeRun, reconcile terminality") và `decideCancelledOutcomeTx` (template chính xác cho thứ tự CAS
NodeRun → load Document → `terminalizeBranchTokenForCancelledNodeRunTx` → `reconcileRunTerminalityTx`);
xác nhận `tx.Runtime().TransitionExecutionAttempt` (đã tx-scoped) là CAS thay thế an toàn cho
`TerminateInterruptedAttempt` cũ, nhưng KHÔNG tự append event — phải tự làm; xác nhận
`QuarantineRepositoryWorkspace` CHƯA có bản tx-scoped nào (chỉ có `*sqlite.Store` với transaction riêng) —
cần trích xuất theo đúng pattern "một hàm dùng chung, hai caller" mà `completeJobTx` (scheduling.go) đã có
sẵn.

1. **`internal/adapters/sqlite/workspace_lifecycle.go`**: trích xuất `quarantineRepositoryWorkspaceTx(ctx,
   tx *sql.Tx, update)` từ `Store.QuarantineRepositoryWorkspace` — store method giờ chỉ mở tx rồi gọi hàm
   chung.
2. **`internal/adapters/sqlite/work.go`** + **`internal/app/ports/work.go`**: thêm
   `WorkRepository.QuarantineRepositoryWorkspace` (tx-scoped, gọi `quarantineRepositoryWorkspaceTx`) — giờ
   reachable qua `tx.Work()`.
3. **`internal/app/ports/fake/work.go`**: thêm bản fake tương ứng (linear scan theo ID, cùng CAS semantics)
   để không phá vỡ interface satisfaction của các fake-uow test khác.
4. **`internal/app/runtime/agent_node_executor_cancellation.go`** (thay đổi lớn nhất): viết lại
   `handleMutatingCancellation` thành `finalizeMutatingCancellation` (giữ real I/O CaptureRevision +
   ReleaseWriteLeases ở "vỏ ngoài") gọi vào `cancelMutatingAttemptTx` — một hàm MỚI, nguyên tử, dùng
   `uow.WithSerializedWrite` bao trọn: revalidate Run CANCELLING, CAS Attempt→INDETERMINATE + tự append
   event `EXECUTION_ATTEMPT_TERMINATED`, quarantine mọi mount có mutation, CAS NodeRun→CANCELLED,
   `terminalizeBranchTokenForCancelledNodeRunTx`, `reconcileRunTerminalityTx` (đây chính là nơi
   `WorkflowRun`→CANCELLED VÀ WorkItem-blocker RUN_CANCELLED tự động xảy ra — `transitionRunToCancelledTx`
   đã có sẵn từ V4-12C, không cần code mới cho bước này), và defensive-complete cancellation intent. Tách
   riêng `cancelledMutatingMountVerdict` (verdict đã tính sẵn, không cần biết cơ chế capture revision cụ
   thể) để `cancelMutatingAttemptTx` dùng chung được cho CẢ live path (AGENT/COMMAND) LẪN recovery reaper
   (2 cơ chế capture revision khác nhau — `ports.WorkspaceProvider.CaptureRevision` thật vs
   `worker.WorkspaceReconciler.LoadRepositoryWorkspaceRevision` hẹp hơn).
5. **`internal/app/runtime/recovery_reaper.go`**: thêm `finalizeCancelledMutatingOrphan` + routing sớm
   trong `recoverOneAttempt` — nếu Attempt orphan có real WriteLease (`repositoryWorkspaceID != ""`) VÀ Run
   đang CANCELLING/CANCELLED, gọi thẳng `cancelMutatingAttemptTx` (bỏ qua toàn bộ
   RETRY/FRESH_START/ESCALATE decision cũ, vốn không bao giờ đóng được Run).
6. **`internal/app/runtime/command_node_executor.go`** + **`gate_node_executor.go`**: cập nhật 2 call site
   còn lại của `classifyCancellation` (chữ ký đổi: bỏ `interruptions`/`reconciler`, thêm `ids`).

**2 bug thật phát hiện thêm qua thực nghiệm (không phải đoán)** khi chạy lại `cancel_mutating_test.go` sau
fix đầu tiên — vẫn treo y hệt:

- Debug print lại vào `pollForCancellation` + `CommandNodeExecutor.Execute` xác nhận: poller vẫn phát hiện
  đúng, process vẫn bị kill đúng — nhưng LẦN NÀY hàm mới (`finalizeMutatingCancellation`) không bao giờ
  return. Nguyên nhân: hàm này dùng `ctx` (chính là `execCtx` — ĐÃ bị cancel bởi poller) cho CHÍNH
  `uow.WithSerializedWrite(ctx, ...)` của nó — một real SQLite write transaction (`BEGIN IMMEDIATE`) cần
  chờ dù chỉ một khoảnh khắc để lấy write lock (ví dụ đụng heartbeat loop của chính job đó) có thể TREO VÔ
  HẠN trên một ctx đã cancel thay vì fail nhanh. **Fix**: `cleanupCtx := context.WithoutCancel(ctx)` — đúng
  pattern `pool.go`'s own heartbeatLoop đã dùng cho renewal write của chính nó — dùng `cleanupCtx` cho MỌI
  I/O thật và write bên trong `finalizeMutatingCancellation`.
- Sau fix trên vẫn treo y hệt (lần 2). Đọc lại `classifyCancellation` phát hiện: bước RE-CHECK ĐẦU TIÊN của
  nó (`uow.WithReadOnly(ctx, ...)` để xác nhận Run thật sự đang cancelling) CŨNG dùng `ctx` gốc (đã cancel)
  — khiến chính bước re-check này fail/treo, khiến toàn bộ hàm route sai sang nhánh "leave RUNNING" thay vì
  bao giờ gọi tới `finalizeMutatingCancellation`. **Fix thứ 2**: derive `cleanupCtx` NGAY ĐẦU
  `classifyCancellation` luôn, dùng cho bước re-check này. Sau cả 2 fix, test pass ổn định (10.6s, rồi 3x
  liên tiếp không cache đều 7.2s).

### Test

- `go build ./...`, `go vet ./...` sạch trên toàn bộ module.
- `go test ./internal/app/runtime/...`: 284 test pass (283 cũ + 1 test mới), gồm việc SỬA 3 test cũ
  (`TestAgentNodeExecutor_MutatingCancellation_CleanRevision...`,
  `TestAgentNodeExecutor_MutatingCancellation_MutatedRevision...`,
  `TestCommandNodeExecutor_ProcessCancelled_ReusesV508CMutatingPath`) — 3 test này assert vào spy CŨ
  (`interruptions.terminations`) giờ luôn rỗng đúng như kỳ vọng (code mới không gọi path cũ nữa) — đổi
  assertion sang đọc real Attempt/NodeRun state qua `uow.WithReadOnly`, đúng tinh thần "test thật, không
  test giả".
- `TestRecoveryReaperHandler_OrphanedMutatingAttempt_RunCancelling_ClosesRunOutReally` (test MỚI, real
  SQLite, theo đúng pattern `sqliteExecutionFixture`/`AcquireWriteLeases` có sẵn trong file): real write
  lease + real `CancelRun` + real lease-expiry → real Attempt INDETERMINATE, real NodeRun CANCELLED, real
  WorkflowRun CANCELLED, real cancellation intent không còn REQUESTED, và replay (Handle lần 2) không đổi
  Attempt.Version — pass ngay lần đầu.
- `internal/integration/v5accept`: cả 10 scenario (3 mới của V5-15D + 7 cũ) pass; 3 scenario mới của
  V5-15D chạy lặp 3 lần liên tiếp không cache đều pass ổn định.
- `go test ./...` toàn repo (mọi package): pass 100%.

### Verify

- Mọi debug print tạm thời (trong `execute.go`, `command_node_executor.go`, và các vòng lặp debug trong
  `cancel_mutating_test.go`) đã revert sạch — `git diff` xác nhận chỉ còn đúng 1 dòng thay đổi thật ở
  `command_node_executor.go` (chữ ký `classifyCancellation` mới).
- `gofmt -l` sạch trên đúng 14 file thật sự sửa (2 file báo "khác biệt" hoá ra là noise CRLF toàn file y
  hệt pattern ~92 file CRLF đã biết từ trước — xác nhận qua `gofmt -d` thấy TOÀN BỘ file bị đánh dấu, không
  phải riêng đoạn code mới).
- `git diff --stat origin/master`: đúng 14 file, không đụng file nào ngoài dự định.

### Kết quả

PR #19, branch `feat/v5-15d-isolation-fencing`, 3 commit (`63509fa` scenario 2 phần đầu, `d6f6422`
production fix riêng theo đúng yêu cầu user, `28b2773` scenario test + recovery-reaper coverage), CI 6/6
xanh ngay lần chạy đầu tiên (bao gồm "Linux race and stability (V0-12)"), squash-merge vào `master` — merge
commit `d04dc83`, 2026-09-11.

**Việc còn lại:** V5-15E (full conformance matrix — mốc hoàn thành thật sự của V5-15, theo đúng contract
gốc của user) — theo đúng contract 5 phần, tiếp tục tự động không cần hỏi lại trừ khi gặp quyết định kiến
trúc thật sự mới.

## V5-15D — phụ lục: fix data race thật trong `cancel_mutating_test.go` (phát hiện sau khi PR #19 đã merge)

### Bối cảnh

PR #20 (docs-only, chỉ thêm narrative V5-15D vào file này, branch `docs/v5-15d-pr-merge`) chạy CI và fail
đúng 1 job trong 6: "Linux race and stability (V0-12)". 5 job còn lại xanh. Job này chạy race detector
đúng 1 lần cộng với vòng lặp ổn định 10 lần (10x) cho toàn bộ offline suite. Theo đúng thói quen đã thiết
lập từ trước của dự án — không bao giờ coi CI fail là "flake không liên quan, rerun là xong" mà chưa điều
tra — đã tải log fail thật của job (`gh run view <run> --job <job> --log-failed`) để xác minh.

### Nghiên cứu

Log cho thấy `--- FAIL: TestV5AcceptCancelDuringMutatingAttempt_RealQuarantineNeverPromotes (3.61s)` xảy ra
ở lần chạy 9/10 của vòng lặp ổn định, với một `WARNING: DATA RACE` đầy đủ từ Go race detector — không phải
timeout hay flake ngẫu nhiên vô hại. Race cụ thể:
- **Read** tại một địa chỉ bộ nhớ, từ goroutine chính của test: `idsource.(*Sequential).NewID()`
  ← `runtime.cancelRunTx()` ← `runtime.CancelRun()` ← chính dòng gọi `runtime.CancelRun(ctx, f.uow, f.ids,
  ...)` trong `cancel_mutating_test.go`.
- **Write** trước đó tại CÙNG địa chỉ, từ một goroutine worker của pool: `idsource.(*Sequential).NewID()`
  ← `agentevents.(*Sink).buildRecord()` ← `agentevents.(*Sink).Accept()` ← `CommandNodeExecutor.Execute()`
  ← `ExecuteNodeHandler.Handle()` ← `workerpool.Pool` (goroutine này được `startPoolWithConfig` tạo ra).

Đọc lại toàn bộ `internal/app/idsource/idsource.go` xác nhận `Sequential` có doc comment nói rõ: "Not safe
for concurrent use — it is a single-threaded test helper, not a production allocator", implementation chỉ
là `s.next++` không hề có lock nào. Đây là một thiết kế CỐ Ý (test helper đơn luồng), không phải bug của
`idsource.Sequential` — không được sửa type này.

Rà lại toàn bộ 10 file scenario khác trong `internal/integration/v5accept` (`grep` cho `startPool`/`f.ids`)
để xác nhận đây có phải rủi ro lan rộng hay chỉ riêng file này: mọi scenario khác đều gọi thêm production
command qua `f.ids` (vd. `EvaluateCompletionCandidate`) chỉ SAU KHI đã `f.waitForRunState` chờ Run về
terminal state — tức đã đi qua một lần đọc DB thật (`uow.WithReadOnly`), tạo happens-before edge thật giữa
goroutine worker và goroutine test qua cơ chế mutex nội bộ của `database/sql`. Riêng
`cancel_mutating_test.go` lại cố tình poll một file marker trên filesystem thường (`os.Stat`, không đi qua
DB, không tạo happens-before edge nào theo mô hình bộ nhớ của Go) rồi gọi `CancelRun` NGAY TRONG LÚC script
thật vẫn còn đang sleep — tức là gọi thẳng vào lúc goroutine worker of pool CÓ THỂ vẫn đang thực thi
`Execute()` thật, dùng chung `f.ids`. Đây là kịch bản V5-15 ĐẦU TIÊN gọi trực tiếp một production command
từ goroutine test trong khi pool đang thực-sự-đồng-thời chạy node dùng chung instance đó — một constraint
luôn tiềm ẩn nhưng chưa từng bị kích hoạt trước đây.

### Quyết định

Không sửa `idsource.Sequential` (thiết kế đơn luồng là cố ý, dùng an toàn ở khắp nơi khác). Thay vào đó,
cấp một `idsource.Sequential` RIÊNG, prefix riêng (`"v5d-cancel-op"`), chỉ cho đúng lệnh `CancelRun` này —
đúng tinh thần "one function, two callers"/"mỗi actor có thể chạy đồng thời có ID source riêng" đã có sẵn
trong `fixture_test.go` (`handlerIDs := idsource.NewSequential(idPrefix)` cho các job handler trong pool,
tách biệt với `f.ids` của test). Vì `idsource.Sequential` sinh ID dạng `prefix-N`, hai instance với prefix
khác nhau không bao giờ đụng ID nhau — an toàn tuyệt đối, không cần đổi logic gì khác trong test.

### Thực hiện

Sửa đúng 1 file, `internal/integration/v5accept/cancel_mutating_test.go`: thêm import
`internal/app/idsource`; ngay trước lệnh `runtime.CancelRun`, tạo
`cancelOperatorIDs := idsource.NewSequential("v5d-cancel-op")` và truyền `cancelOperatorIDs` thay vì `f.ids`
vào lệnh gọi đó. Không đụng bất kỳ dòng nào khác trong file, không đụng `idsource.Sequential`, không đụng 9
scenario còn lại (đã xác nhận không có cùng rủi ro).

### Test

- `go build ./...` sạch.
- `go test -run TestV5AcceptCancelDuringMutatingAttempt_RealQuarantineNeverPromotes -count=3 -v
  ./internal/integration/v5accept/...`: pass ổn định cả 3 lần (~7.26s mỗi lần), hành vi test không đổi.
- Không thể chạy `-race` ngay tại máy local (Windows, thiếu cgo/gcc — `CGO_ENABLED=1` nhưng không có C
  compiler) — xác nhận qua `go env CC` rỗng và `which gcc`/`where gcc` không tìm thấy; race detector của dự
  án này vốn chỉ chạy trên CI (Linux). Đẩy fix lên PR #20 để CI tự xác minh lại đúng job đã fail.

### Verify

- `git diff --stat` xác nhận đúng 1 file thay đổi, đúng 14 dòng (13 thêm/1 sửa), không có noise CRLF nào
  lẫn vào commit.
- Chờ CI của PR #20 chạy lại sau khi push fix — mục tiêu là job "Linux race and stability (V0-12)" xanh
  ổn định (không còn `WARNING: DATA RACE` trong log của 10 lần lặp).

### Kết quả

Commit `4190ff6` ("fix(v5-15d): eliminate real data race in cancel_mutating_test.go"), push thẳng vào
nhánh `docs/v5-15d-pr-merge` (PR #20) — vì file bị lỗi là code test ĐÃ MERGE cùng PR #19, còn PR #20 đang mở
sẵn và chính là PR đã phát hiện ra race này qua CI của nó, nên gộp fix vào cùng PR #20 thay vì mở PR riêng,
tránh một PR "chỉ sửa 1 dòng" không cần thiết. Sau khi CI 6/6 xanh, PR #20 (docs + fix race) sẽ được merge,
khép lại hoàn toàn V5-15D bao gồm cả phát hiện mới này.
