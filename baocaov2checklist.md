# V2 — Báo cáo triển khai liên tục

> File làm việc cá nhân, không commit vào git (giống `baocaov0checklist.md`/`baocaov1checklist.md`).
> Cập nhật sau mỗi task.
> Quy ước: khi vướng quyết định không tường minh trong task spec, tôi chọn theo khuyến nghị tốt nhất
> của tôi, ghi rõ lựa chọn + lý do ở đây, rồi tiếp tục — không dừng lại hỏi trừ khi thực sự chặn cứng
> (thiếu thông tin không thể suy luận được, hoặc rủi ro phá hoại/không thể đảo ngược). Kế thừa toàn bộ
> ràng buộc môi trường đã xác nhận ở V1 (Go toolchain local tại `.tools/go1.27.0/go/bin/go.exe`, verify
> local trước khi push, CI thật là gate cuối).

## Trạng thái tổng quan

| Task | Trạng thái | PR | CI | Ghi chú |
|---|---|---|---|---|
| V2-01 | DONE | [#19](https://github.com/taQuangLing/agent-workflow/pull/19) | ✅ | CI xanh cả 6 job ngay lần đầu |
| V2-02 | DONE | [#20](https://github.com/taQuangLing/agent-workflow/pull/20) | ✅ | CI xanh cả 6 job ngay lần đầu |
| V2-03 | DONE | [#21](https://github.com/taQuangLing/agent-workflow/pull/21) | ✅ | CI xanh cả 6 job ngay lần đầu; package `internal/domain/authoring` mới |
| V2-04 | DONE | [#22](https://github.com/taQuangLing/agent-workflow/pull/22) | ✅ (sau 1 rerun) | package `internal/domain/block` mới; V0-12 fail lần đầu ở `TestPool_HeartbeatKeepsLongRunningJobAlive` (package `internal/app/workerpool`, không đụng bởi V2-04) — rerun xanh sạch cả 6 job, xác nhận flake không phải regression |
| V2-05 | DONE | [#23](https://github.com/taQuangLing/agent-workflow/pull/23) | ✅ | CI xanh cả 6 job ngay lần đầu; package `internal/domain/command` + `internal/domain/gate` mới |
| V2-06 | DONE | [#25](https://github.com/taQuangLing/agent-workflow/pull/25) | ✅ | Triển khai SONG SONG bằng subagent (worktree) theo yêu cầu của bạn; CI xanh cả 6 job ngay lần đầu; package `internal/domain/skill` + `internal/domain/layer` + `internal/domain/engineeringpack` mới, thêm `internal/domain/definition/priority.go` (shared) |
| V2-07 | DONE | [#24](https://github.com/taQuangLing/agent-workflow/pull/24) | ✅ | Triển khai SONG SONG bằng subagent (worktree) theo yêu cầu của bạn; CI xanh cả 6 job ngay lần đầu; package `internal/domain/agentprofile` + `internal/domain/policy` mới |
| V2-07A | DONE | [#26](https://github.com/taQuangLing/agent-workflow/pull/26) | ✅ | CI xanh cả 6 job ngay lần đầu; migration 0005 + package `internal/domain/adapterbuild` + `internal/app/adapterbuild`, `ports.AdapterBuildRepository` mới trên `Tx` |
| V2-07B | DONE | [#27](https://github.com/taQuangLing/agent-workflow/pull/27) | ✅ (sau 1 rerun) | Song song với V2-08 (subagent worktree); `cmd/agentkit/adapter.go` — CLI `agentkit adapter probe\|register\|list\|show` |
| V2-08 | DONE | [#28](https://github.com/taQuangLing/agent-workflow/pull/28) | ✅ (sau 1 rerun) | Song song với V2-07B (subagent worktree); mở rộng `internal/domain/workflow` — 9 node type đầy đủ, typed node config, shared-state schema |
| V2-09 | DONE | [#29](https://github.com/taQuangLing/agent-workflow/pull/29) | ✅ (sau 1 rerun) | Task lớn nhất session này — tự làm (không giao subagent). V0-12 fail lần đầu ở `TestSPK09QuarantineRecreateFencesStaleGeneration` — CHÍNH LÀ flake SPK-09 đã biết từ V1 (không phải regression), rerun xanh sạch. Mở rộng `internal/domain/workflow` (fork/join topology) + `internal/app/ports.DefinitionsRepository` (real method đầu tiên) + package mới `internal/app/workflowcompiler` |
| V2-10 | DONE | [#30](https://github.com/taQuangLing/agent-workflow/pull/30) | ✅ | Giao subagent (brief rất chi tiết, tự thiết kế kiến trúc trước). CI xanh cả 6 job ngay lần đầu. Package mới `internal/app/definitions`; refactor `definitions.go`/`workflow_store.go` thành Tx-composable core functions. Tự bắt được 1 bug THẬT có sẵn từ V2-02 (workflow dependency pin hash bị bỏ trống khi convert `definition.DependencyPin`→`workflow.DependencyPin`, chưa test nào phát hiện vì chỉ test empty manifest) |
| V2-11 | DONE | [#31](https://github.com/taQuangLing/agent-workflow/pull/31) | ✅ | Giao subagent, CI xanh cả 6 job ngay lần đầu. `cmd/agentkit/definition.go` — CLI `create\|validate\|publish\|list\|show\|diff`. Tự bắt được 1 bug THẬT ở `internal/adapters/sqlite/txrunner.go` (V1-04A, hạ tầng chung) — lỗi domain bị `MapSQLiteError` nuốt mất message rõ ràng khi trả về từ `WithSerializedWrite`/`WithReadOnly`, không ai phát hiện suốt session vì mọi test đều dùng `errors.Is`/`errors.As` (xuyên qua được `Unwrap()`), chỉ CLI in `.Error()` ra người dùng mới lộ ra |
| V2-12 | DONE | [#32](https://github.com/taQuangLing/agent-workflow/pull/32) | ✅ | Tự làm (không giao subagent) — task đóng cả phase V2. CI xanh cả 6 job ngay lần đầu. Test mới `internal/integration/definitionplane_test.go` (`TestDefinitionPlaneGate`), không đụng code sản phẩm nào |

## V2 — HOÀN THÀNH (2026-09-02)

Toàn bộ 15 task (V2-01..V2-12, gồm V2-07A/V2-07B) đã DONE, merge vào `master`, CI xanh. V2-12 tự đóng gate
bằng integration test thật (publish đủ 9 kind + AdapterBuildVersion, restart, publish V2, xác nhận V1
snapshot bất biến, xác nhận 3 loại reject đều hoạt động đúng). Không có task nào còn lại trong phase này.

## Quyết định/khuyến nghị đã áp dụng khi vướng

- **V2-02, quyết định schema THEO ĐÚNG chỉ đạo trực tiếp của bạn (không phải tôi tự chọn):** đã hỏi rõ 3
  phương án trước khi code (shared table cho 8 kind mới / migrate luôn workflow / 8 bảng riêng từng kind)
  vì đây là quyết định kiến trúc ảnh hưởng toàn bộ V2-04..V2-11, khó đảo ngược rẻ một khi các kind sau xây
  trên nó. Bạn chọn phương án 1 (shared table cho 8 kind mới, không đụng workflow) kèm danh sách điều
  kiện khoá rất chi tiết — đã áp dụng ĐÚNG TỪNG ĐIỀU trong danh sách đó, liệt kê lại để đối chiếu:
  - `definitions.kind` CHECK chỉ nhận 8 kind mới, KHÔNG nhận WORKFLOW — test cả 2 tầng (Go
    `CreateSharedDefinition` reject + raw SQL insert bị CHECK constraint chặn).
  - `definition_versions` không có cột `kind` riêng — join qua `definition_id` để lấy kind khi cần (xem
    `loadSharedDefinitionVersion`).
  - UNIQUE `(definition_id, version_no)` và `(definition_id, compiled_hash)` — cả hai đều có trong
    migration 0004.
  - Version không update/delete public — `definition.VersionFields` (từ V2-01) đã đúng sẵn, không thêm
    method nào ở V2-02.
  - Cross-project dependency: **resolve từ repository** (SELECT `project_id` thật của definition được
    pin), KHÔNG tin giá trị client gửi — `DependencyPin` (package `definition`, file mới `dependency.go`)
    cố tình KHÔNG có field ProjectID để không ai lỡ tin nhầm giá trị đó.
  - AdapterBuildVersion KHÔNG đụng tới ở task này — bảng đó thuộc V2-07A (task riêng, có spec TOCTOU rất
    chi tiết, xây sớm ở đây rủi ro phải làm lại).
  - Application layer không biết có 2 layout — `ports.DefinitionPublisher.PublishDefinitionVersion` là
    MỘT method duy nhất, `*sqlite.Store` tự route theo `req.Kind` (WORKFLOW → `PublishWorkflowVersion` cũ;
    8 kind còn lại → bảng shared mới) — verify bằng test gọi cùng 1 interface value cho cả 2 nhánh.
  - Không dual-write, không compatibility migration — migration 0004 chỉ CREATE TABLE mới, không đụng 1
    dòng nào của `workflow_definitions`/`workflow_versions`/`workflow_runs`.
  - Đủ 7 test bắt buộc bạn liệt kê — đã viết đủ 7, tên test đặt rõ ràng khớp từng yêu cầu (xem
    `internal/adapters/sqlite/definitions_test.go`), tất cả PASS thật trên toolchain local.
- **V2-02, tự bắt được 1 bug thật trong CHÍNH TEST của mình (concurrent publish) trước khi coi task xong:**
  test đầu tiên cho 8 writer đua nhau publish dùng CHUNG một `VersionID` — vì `definition_versions.id` là
  PRIMARY KEY, 7/8 writer fail với lỗi PK conflict bị `MapSQLiteError` gộp chung thành
  `INTERNAL: unexpected error`, che mất đúng thứ cần verify (version_no/hash không trùng). Đã tự phát
  hiện qua log lỗi thật khi chạy test local, sửa bằng cách cho mỗi writer một `VersionID` riêng (đúng thực
  tế: mỗi candidate publish luôn có identity riêng, giống pattern `compileWorkflowVersion` đã dùng ở
  workflow test). Chạy lại xanh, xác nhận đúng 8 version_no phân biệt + đúng 8 dòng trong
  `definition_versions`.
- **V2-02, `SchemaVersion` giữa workflow (string, ví dụ "1") và shared kind (int) không khớp kiểu —
  convert bằng `strconv.Atoi` khi route qua nhánh WORKFLOW, chấp nhận fallback về 0 nếu parse lỗi (chưa
  từng xảy ra với fixture thật):** đây là type mismatch có sẵn giữa schema V0 (string) và schema V2-02
  mới (int, theo đúng patttern các kind mới sẽ dùng từ V2-03+) — không đổi kiểu của `workflow` package
  (rủi ro không cần thiết cho task này), chỉ convert tại đúng boundary nơi 2 model gặp nhau.


- **V2-01, thiết kế "kind-safe IDs" KHÔNG dùng generics trong package `definition` dùng chung:** cân nhắc
  `ID[K any] string` (phantom type generic) để ép compile-time an toàn NGAY trong package chung, nhưng
  chọn giữ đúng idiom đã có sẵn trong `internal/domain/workflow` (mỗi kind tự khai `type XDefinitionID
  string` riêng, không generic). Lý do: package `definition` chỉ định nghĩa SHAPE/RULE chung (Kind,
  Status, transition, Scope, Create/Archive/Activate) bằng `string` thô cho ID trong `VersionFields` —
  còn "kind-safe" thật sự (không lẫn `BlockVersionID` với `WorkflowVersionID`) là trách nhiệm của TỪNG
  package kind riêng (như `workflow.WorkflowVersionID` đã làm), y hệt pattern đã chứng minh hoạt động tốt
  từ V0. Generic phantom type sẽ ép mọi kind tương lai (V2-04..V2-07) phải theo đúng 1 khuôn `ID[K]`, có
  thể không khớp nhu cầu thật của từng kind khi chưa xây — đúng tinh thần "không chia chỉ để tạo
  field/kiểu chưa có contract test/behavior quan sát được" (`00-roadmap.md` §3).
- **V2-01, "Hoàn thành khi: các kind reuse lifecycle behavior" được chứng minh THẬT bằng cách refactor
  `internal/domain/workflow.DefinitionStatus` thành TYPE ALIAS của `definition.Status`, không phải chỉ
  xây package mới rồi để đó:** ban đầu cân nhắc chỉ xây `definition` package độc lập, không đụng
  `workflow` (rủi ro thấp hơn, giống cách V1-05 để 7 repo trống). Nhưng làm vậy sẽ khiến "Hoàn thành khi"
  chỉ đúng về LÝ THUYẾT (code tồn tại, chưa ai dùng thật). Chọn refactor tối thiểu, an toàn: `type
  DefinitionStatus = definition.Status` (alias, KHÔNG phải type mới) + 3 const trỏ thẳng sang
  `definition.Status*` — vì alias là CÙNG MỘT TYPE ở compile-time (không cần convert), mọi so sánh/switch
  hiện có trong `compiler.go`/`workflow_store.go`/test vẫn biên dịch và chạy giống hệt, không đổi 1 dòng
  logic nào. Đã verify: toàn bộ `go test ./...` (bao gồm `internal/domain/workflow`,
  `internal/adapters/sqlite`) xanh y hệt trước refactor.
- **V2-01, KHÔNG refactor logic validate trùng lặp trong `compiler.go` (dòng 24) và `workflow_store.go`
  (dòng ~793) sang gọi `definition.CanPublish`/`CanTransition` thật, dù đây chính xác là loại duplicate
  mà package mới sinh ra để giải quyết:** hai chỗ đó đã có test assert theo đúng error message hiện tại
  (`compiler_test.go`), sửa call site có rủi ro đổi message dù logic tương đương — lợi ích (xoá
  duplicate) không đủ lớn so với rủi ro (regress code V0 đã test kỹ) ở ĐÚNG task này. Để lại làm việc sau
  (task dọn dẹp riêng, hoặc tự nhiên xảy ra khi V2-04+ cần sửa lại các call site này cho kind mới).
  **Nếu bạn muốn hợp nhất logic ngay bây giờ, hãy yêu cầu sửa lại task này.**

- **V2-03, package mới `internal/domain/authoring` (`decode.go`, `canonical.go`, `diagnostic.go` +
  test), thêm dependency mới `gopkg.in/yaml.v3` (đã `go get` + `go mod tidy`, verify `go.sum` có đủ 2
  dòng hash `h1:`/`go.mod`):** strict decode (JSON qua `json.Decoder.DisallowUnknownFields` + tự viết
  duplicate-key walker bằng `json.Token`; YAML qua `yaml.Decoder.KnownFields(true)` + tự viết duplicate
  key/ambiguous-boolean walker trên `yaml.Node` vì thư viện không tự phát hiện 2 việc này) và canonicalize
  (round-trip qua `json.Marshal`→`json.Unmarshal(any)`→`json.Marshal` để lợi dụng đúng hành vi
  well-documented của Go: marshal `map[string]T` tự sort key theo alphabet, marshal struct thì không —
  nên round-trip qua map là cách rẻ nhất để có canonical JSON đã sort key, không cần tự viết sorter).
  `Diagnostic{Line,Column,Path,What,Why,Fix}` đúng convention đã có sẵn ở `internal/app/config.Validate`
  (tái dùng pattern, không bịa convention mới).
- **V2-03, phát hiện thật (không đoán) hành vi resolve boolean của yaml.v3 khác YAML 1.1:** ban đầu code
  detect ambiguous boolean (`yes/no/on/off/y/n`) bằng điều kiện `node.Tag == "!!bool"` — test
  `TestDecodeStrict_YAML_RejectsAmbiguousBoolean` fail cả 8 case vì yaml.v3 theo YAML 1.2 Core Schema, tag
  các token này là `!!str` chứ không phải `!!bool` (khác YAML 1.1). Verify bằng script `go run` tạm in
  thẳng `node.Tag`/`node.Style` cho `enabled: yes` — xác nhận `tag=!!str style=0`. Đồng thời verify thêm:
  dù tag là `!!str`, `Decode` vào field Go kiểu `bool` VẪN thành công (coerce ngầm, không nhất quán với
  chính Tag nó vừa resolve) — nghĩa là cùng 1 token, cùng lúc decode được vào cả field `bool` lẫn field
  `string` tuỳ đích, đúng là ambiguity thật cần chặn. Sửa: bỏ điều kiện theo `Tag`, chỉ match theo
  `Style == 0` (plain/không quote) + tra bảng `ambiguousBooleanTokens` cố định — 1 giá trị quote (`"on"`)
  không bao giờ bị flag vì `Style != 0`. Phạm vi cố ý giới hạn đúng 6 shorthand YAML 1.1
  (yes/no/on/off/y/n + biến thể hoa/thường), không xây detector ambiguity tổng quát.
- **V2-03, tự bắt được 2 bug thật trong CHÍNH TEST của mình trước khi coi task xong (không phải bug ở code
  chính):**
  1. `decodeAny`/`jsonUnmarshalForFuzz` (helper trong `canonical_test.go`/`fuzz_test.go`) ban đầu gọi 1
     helper `yamlUnmarshal` không tồn tại (sót lại từ 1 hướng thiết kế đã bỏ) — sửa bằng cách import thẳng
     `encoding/json`/`gopkg.in/yaml.v3` trong file test và gọi `Unmarshal` trực tiếp.
  2. `TestCanonicalize_PropertyPermutations`: version đầu gán value theo VỊ TRÍ sau khi shuffle
     (`tree[key] = i` với `i` là index trong slice đã xáo trộn) — mỗi permutation vô tình tạo ra document
     khác nhau thật sự (key khác value khác), nên hash khác nhau là ĐÚNG, không phải bug ở
     `Canonicalize`. Test tự fail với thông báo rõ ràng ("permutation 1 produced hash ..., want ...").
     Sửa: tách `fixedValue map[string]int` gán 1 lần theo TÊN key trước khi shuffle, mọi permutation dùng
     lại đúng map đó — đảm bảo test đo đúng thứ cần đo (key order không ảnh hưởng hash của CÙNG 1
     document), không phải đo nhầm document khác nhau.
- **V2-03, fuzz test verify THẬT (không chỉ chạy seed corpus):** chạy `go test -fuzz` thật (không phải chỉ
  `go test` thường, vốn chỉ chạy seed corpus như subtest) cho cả 2 target, ~20s/target trên toolchain
  local:
  - `FuzzDecodeStrict_NeverPanics`: 402.839 exec, 629 corpus entries mới, PASS, không panic.
  - `FuzzCanonicalize_DeterministicAndNoPanic`: PASS, không panic, không lệch hash giữa 2 lần gọi cùng 1
    tree.
  Không có thư mục `testdata/fuzz/<FuzzName>/` nào được tạo (thư mục đó chỉ sinh khi có case FAIL cần lưu
  lại để tái hiện) — xác nhận không có failure nào bị bỏ sót.

- **V2-04, package mới `internal/domain/block` (`block.go` types, `validate.go`, `compiler.go` + test),
  KHÔNG đụng `internal/adapters/sqlite`/`internal/app/ports`/CLI — quyết định phạm vi có cân nhắc trước
  khi code, dựa trên research kỹ docs (đã dùng 1 Explore agent đọc `docs/harness-engineering/14-*`,
  `docs/00-start-here.md`, `docs/architecture/*`, `docs/design/*` trước khi thiết kế type):**
  - `Compile` sản xuất THẲNG `definition.VersionFields` (không có type `BlockVersion` bọc riêng như
    `workflow.WorkflowVersion`) — vì Block đi qua shared publish path (V2-02) đã tổng quát cho MỌI kind
    khác Workflow; `definition.VersionFields` đã đủ generic (ID/DefinitionID/Kind/VersionNumber/
    SchemaVersion/CanonicalSource/SourceHash/CompiledSnapshot/CompiledHash/Dependencies/PublishedBy/
    PublishedAt) nên bọc thêm 1 type là duplicate không cần thiết. `internal/adapters/sqlite/
    definitions.go` không cần sửa gì để nhận KindBlock — đã tổng quát sẵn từ V2-02.
  - Việc resolve/verify pin thật (executorRef/policyRefs có tồn tại, tương thích không) KHÔNG làm ở đây
    — đó là job của V2-09 (graph/dependency compiler). V2-04 chỉ làm domain type + validate cấu trúc +
    hash tests, đúng đúng theo `Verify` line của task ("valid/invalid block fixtures và hash tests"),
    không có "publish"/"persistence"/"CLI" nào trong `Thực hiện`/`Hoàn thành khi` của V2-04.
  - **"block không thể dùng Skill/Layer như executable ref" (Hoàn thành khi) — chốt bằng allowlist
    `executableExecutorKinds = {BLOCK, COMMAND, GATE, AGENT_PROFILE}`**, dựa đúng cột "Quyền thực thi"
    trong `docs/design/01-system-design.md`: Skill/Layer/Engineering Pack "Không", Policy chỉ dùng qua
    `policyRefs` riêng (không phải executor), Workflow "Không trực tiếp" (composition qua SUBFLOW, khác
    khái niệm). Có test riêng cho từng kind bị từ chối (Skill/Layer) VÀ từng kind được chấp nhận
    (Block/Command/Gate/AgentProfile) — không chỉ test đúng 1 case yêu cầu.
  - **`ADR-013`'s `INTEGRATION_MULTI_REPOSITORY_WRITE`** dùng làm ví dụ fixture cho `RequiredCapabilities`
    (giá trị capability CỤ THỂ duy nhất đã được tham chiếu ở nơi khác trong kiến trúc đã accept) — field
    này để dạng `[]string` mở (không đóng thành enum), vì tài liệu chỉ xác nhận 1 giá trị, đóng thành enum
    sẽ buộc sửa package này mỗi khi có capability mới trong khi V2-09/runtime mới là nơi thật sự biết hết
    tập capability.
  - **3 khái niệm KHÔNG có định nghĩa rõ trong doc (research xác nhận), tự quyết định + ghi rõ ở đây để
    dễ sửa nếu bạn muốn khác:**
    1. `CompatibleNodeTypes` — dùng đúng 4 "executable" node type từ HE-14 (AGENT/COMMAND/MACHINE_GATE/
       HUMAN_TASK), KHÔNG gồm node type cấu trúc (START/END/ROUTER/FORK/JOIN/WAIT/SPAWN_WORK_ITEMS/
       SUBFLOW) vì các loại đó không bao giờ có executor ref.
    2. `ScopeSelector{Access READ|WRITE, PathScopes []string}` — mô hình theo `work.RepositoryScope`/
       `RepositoryScope` runtime đã có sẵn (`Access` + `PathScope[]`), là "declared requirement" phía
       authoring-time, KHÔNG phải grant thật (grant thật đối chiếu ở compiler V2-09).
    3. `DoneCondition` — cố tình để là `string` cơ hội (opaque, không tự bịa ra ngôn ngữ expression), chỉ
       validate non-empty; parse/evaluate thật thuộc về V4/V5 runtime, không phải package domain-only
       này. Tránh over-engineer 1 field chưa ai định nghĩa cú pháp.
  - **SourceHash vs CompiledHash tách bạch đúng ADR-012:** `Compile` canonicalize `BlockDocument` một
    mình cho SourceHash, và canonicalize `{Document, Dependencies}` gộp cho CompiledHash — có test riêng
    khẳng định 2 request cùng document nhưng khác dependency manifest cho CÙNG SourceHash nhưng KHÁC
    CompiledHash (`TestCompile_SourceHashExcludesDependencies_CompiledHashIncludesThem`).
  - Set-order independence dùng ĐÚNG cơ chế `authoring.Canonicalize`'s `SetPaths` (không tự `sort.Strings`
    tay như `workflow.compiler.go` làm trước V2-03 tồn tại) — đúng tinh thần "Phụ thuộc: V2-03" là Block
    phải THỰC SỰ tái dùng cơ chế đó, không xây lại song song.
- **V2-04, tự bắt được 1 bug thật ở package V2-02 (`definition.DependencyPin`) khi viết test YAML cho
  Block — không phải bug ở code V2-04 mới, mà là 1 gap có sẵn từ V2-02 chỉ lộ ra khi có consumer thật
  dùng YAML qua `authoring.DecodeStrict`:** `definition.DependencyPin` (file
  `internal/domain/definition/dependency.go`) và `block.BlockDocument`/`block.ScopeSelector` ban đầu chỉ
  có tag `json:"..."`, không có `yaml:"..."` — `authoring.DecodeStrict` cho YAML dùng
  `yaml.Decoder.KnownFields(true)`, mà khi thiếu tag `yaml`, thư viện yaml.v3 mặc định khớp theo tên field
  Go viết thường toàn bộ (vd `compatiblenodetypes`, `definitionid`), không phải camelCase như tag `json`
  — nên fixture YAML dùng đúng camelCase (khớp convention `json`) bị reject với lỗi "field ... not found
  in type". Phát hiện qua `TestCompileFrom_JSONAndYAML_SameContent_SameHash` fail thật khi chạy local.
  Sửa: thêm `yaml:"..."` khớp camelCase y hệt tag `json` hiện có cho `definition.DependencyPin` (3 field)
  và `block.BlockDocument`/`block.ScopeSelector` (toàn bộ field) — chỉ thêm tag, không đổi field/behavior
  nào, verify lại toàn bộ `go test ./...` (gồm `internal/domain/definition`, `internal/adapters/sqlite`,
  `internal/domain/workflow` — 3 package tiêu thụ `DependencyPin`) vẫn xanh y hệt trước khi sửa.
  **Đây là gap sẽ tái diễn cho MỌI kind tiếp theo (V2-05..V2-07) nếu kind đó cũng hỗ trợ author bằng YAML
  và cũng dùng `definition.DependencyPin` lồng bên trong — nay đã sửa 1 lần ở gốc nên các kind sau không
  cần tự phát hiện lại.**

- **V2-05, 2 package mới `internal/domain/command` và `internal/domain/gate` (tách riêng, không gộp 1
  package) — dựa trên research kỹ AK-ARCH-017 (`docs/architecture/03-system-architecture.md` §"Boundary
  và extensibility") và §10.2 "Executable Registry và Runner". Cùng kiến trúc với V2-04: domain-only,
  `Compile` → thẳng `definition.VersionFields`, KHÔNG đụng adapter/CLI (đó là job của V2-09/V2-10/V2-11).**
  - **Tách 2 package thay vì 1**, vì mỗi kind đã có convention "1 package/1 Kind" từ `workflow`/`block`, và
    Gate không cần import type Go nào của `command` — Gate chỉ cần 1 `definition.DependencyPin` với
    `Kind` ràng buộc `KindCommand` (giống hệt cách Block ràng buộc `ExecutorRef`), nên 2 package độc lập
    hoàn toàn, không coupling.
  - **`argv` là `[]ArgvElement{Kind: LITERAL|PLACEHOLDER, Value}`, KHÔNG bao giờ là 1 string** — đúng yêu
    cầu "reject shell string": mỗi phần tử argv là literal HOẶC placeholder đã khai trong
    `placeholderAllowlist`, không bao giờ nội suy vào giữa 1 string lớn hơn (đúng câu doc: "Template chỉ
    thay placeholder đã khai báo thành argv element riêng"). `argv` CỐ TÌNH không nằm trong
    `documentSetPaths` (khác các field khác) — thứ tự argv có ý nghĩa thật, có test riêng khẳng định đảo
    thứ tự argv làm đổi hash (`TestCompile_ArgvOrderIsSignificant`).
  - **"Pinned executable" = `ExecutableRef{OwnerVersionID, ResourceKey, ContentHash}`** — dùng ĐÚNG shape
    resource identity đã chốt ở ADR-012 ("Resource thụ động... có identity owner_version_id + resource_key
    + content_hash; không cần top-level ResourceDefinition riêng"), KHÔNG dùng `definition.DependencyPin`
    cho executable ref vì resource không phải 1 DefinitionKind có DefinitionID/VersionID riêng. Xác nhận
    đây KHÔNG phải AdapterBuildVersion (đó là provider CLI registry riêng của V2-07A, ADR-022) — 2 khái
    niệm hoàn toàn khác nhau dù cùng ý "pin bằng exact hash".
  - **"Evaluator ref" (tên field trong bảng DefinitionKind ở `01-system-design.md` cho Gate) — quyết định
    hiểu là CÙNG 1 field với "command ref", không tách 2 field riêng:** research xác nhận không có khái
    niệm "evaluator" nào khác được định nghĩa ở bất kỳ đâu trong docs; Gate chỉ có `CommandRef
    definition.DependencyPin` (bắt buộc `Kind == KindCommand`), không có field "evaluator" riêng. Đây là
    quyết định thu hẹp phạm vi có cân nhắc (an toàn, dễ mở rộng sau nếu bạn muốn tách 2 field), không phải
    bỏ sót — ghi rõ ở đây để bạn review/yêu cầu sửa nếu ý định khác.
  - **Verdict enum `PASS|FAIL|ERROR|NOT_RUN|NOT_APPLICABLE`** lấy đúng từ `docs/design/07-v5-execution-
    evidence.md` V5-10 (task RUNTIME sau này thực sự sinh ra verdict) — package `gate` ở V2-05 chỉ khai
    `Criterion{Name, EvidenceKey}` (mapping tên tiêu chí → evidence key), KHÔNG tự implement logic đánh giá
    verdict nào (đó là job V5-10 runtime, không phải authoring-time schema này).
  - **env/network/secret permission: chọn CẢ inline field LẪN `PolicyRefs` pin (không chỉ 1 trong 2)** —
    research xác nhận 2 đoạn doc độc lập (§10.2 và dòng `Thực hiện` của V2-05) đều liệt kê các field này
    như REQUIRED FIELD của executable definition (không nói "chỉ cần pin Policy"), nên `CommandDocument`
    có `EnvAllowlist []string` (set), `NetworkAccess NONE|ALLOWED` (enum đóng), `SecretRefs []string` (tên
    secret, KHÔNG BAO GIỜ giá trị — đúng "secret chỉ resolve ở worker ngay trước spawn và không persist")
    inline, CỘNG THÊM `PolicyRefs []definition.DependencyPin` (Kind phải POLICY) để pin authorization/grant
    semantics riêng — giống hệt pattern Block đã dùng cho `RequiredCapabilities` + `PolicyRefs`.
  - **OS/toolchain compatibility** (`Compatibility{OS, Toolchain}`) tái dùng đúng vocabulary Layer đã có
    sẵn trong `docs/architecture/04-go-core-spec.md` ("Layer phải khai OS/toolchain compatibility") — `OS`
    bắt buộc ít nhất 1 phần tử, `Toolchain` optional (không phải mọi command đều cần khai toolchain, vd
    shell script thuần).
  - **Cwd target** chỉ mô hình là `CwdRepositoryTarget string` (tên repository target trong scope của
    run) — KHÔNG tự dựng full `WorkspaceHandle` resolution (đó là runtime/V4-05 job); đúng câu doc "Cwd
    phải resolve từ WorkspaceHandle + repository target", field này chỉ khai phần "repository target".
  - SourceHash/CompiledHash tách bạch đúng ADR-012 (test riêng cho cả `command` và `gate`, y hệt pattern
    Block).

- **V2-06 và V2-07, TRIỂN KHAI SONG SONG lần đầu tiên trong session này — theo đúng yêu cầu trực tiếp của
  bạn ("có thể triển khai một số task song song mà không cần điều kiện task trước không").** Cả 2 task chỉ
  phụ thuộc V2-02+V2-03 (đã merge), không phụ thuộc lẫn nhau và không phụ thuộc V2-04/V2-05 — xác nhận qua
  đọc kỹ `Phụ thuộc` field của từng task trước khi quyết định chạy song song. Dùng 2 subagent
  `general-purpose` với `isolation: worktree` (mỗi agent làm việc trong 1 git worktree riêng, tự research
  docs + code + verify local + commit + push + PR + poll CI thật + merge), mỗi agent được brief đầy đủ
  toàn bộ convention đã thiết lập từ V2-04/V2-05 (kind-safe ID, `Compile` → thẳng `definition.VersionFields`,
  `documentSetPaths`/`compiledSetPaths`, `authoring.DecodeStrict`/`Canonicalize`, golden fixture qua
  `cmd/gen-golden-scratch` tạm rồi xoá, gate CI thật 6 job, không đụng adapter/CLI). Sau khi cả 2 agent xong,
  tôi đã tự VERIFY LẠI ĐỘC LẬP trên máy chính (không chỉ tin báo cáo của agent): `go build ./...`,
  `go vet ./...`, `go test ./...` (toàn bộ repo, cả 2 package mới lẫn mọi package cũ), `go run
  ./cmd/docs-coverage-check` — tất cả xanh sạch sau khi cả 3 PR (V2-05/06/07) đã nằm chung trên master.
  - **Sự cố quy trình tự gây ra (đã tự sửa ngay, không ảnh hưởng kết quả cuối):** khi cố resume agent V2-07
    sau khi nó dừng ở bước "chờ CI" (task-notification báo `completed` vì agent không có background job
    nào đang chạy thật — tự nó chỉ ĐỊNH sẽ chờ chứ chưa thực sự bắt đầu 1 lệnh chạy nền), tôi gọi NHẦM tool
    `Agent` (tạo agent MỚI, worktree mới, không có context gì) thay vì `SendMessage` (resume đúng agent cũ
    với context đầy đủ). Tự phát hiện ngay khi thấy `ListAgents` liệt kê 1 agent thừa đang chạy, gửi lệnh
    dừng ngay cho agent nhầm (nó tự kết luận không có việc gì làm, kết thúc vô hại, không commit/push gì),
    rồi gọi đúng `SendMessage` tới agent V2-07 thật để tiếp tục — agent đó tự poll CI thật, merge PR #24
    thành công. Không có hậu quả thật (agent nhầm không đọc/sửa gì), nhưng ghi lại ở đây làm bài học quy
    trình: `Agent` luôn tạo mới, `SendMessage` mới resume.
  - **V2-06 agent đụng vào 1 file SHARED (`internal/domain/definition/priority.go`, package đã merge từ
    V2-01/V2-02) — không phải lỗi phạm vi, mà là quyết định kiến trúc có lý do rõ:** `PriorityClass`
    (HARD_CONSTRAINT/REQUIRED_PROCEDURE/GUIDANCE/REFERENCE, theo HE-04-M03) và `ResourceIdentity`/
    `ResolvedResource` đặt ở `definition` package (không đặt riêng trong `skill`/`layer`) vì Engineering
    Pack cần so sánh priority giữa resource đến từ CẢ Skill lẫn Layer mà không import package cụ thể của
    kind nào — đúng lý do y hệt tại sao `DependencyPin` đã đặt ở `definition` từ V2-02 chứ không đặt riêng
    trong `workflow`. Đã tự verify: `internal/archtest` (domain/app never import adapters) vẫn xanh, và
    `engineeringpack` không import cả `skill` lẫn `layer` (xác nhận qua `go list -f '{{.Imports}}'`).
  - **V2-06, "pinned executable"/conflict/cycle:** `EngineeringPackDocument.Dependencies` dùng
    `definition.DependencyPin` giới hạn Kind ∈ {SKILL, LAYER, ENGINEERING_PACK} (cho phép pack-lồng-pack,
    quyết định có lý do: nếu không cho phép, "pack graph resolution" và cycle detection sẽ vô nghĩa vì đồ
    thị Pack→{Skill,Layer} luôn là bipartite, không bao giờ có cycle). Cycle detection dùng lại đúng thuật
    toán Tarjan's SCC đã có sẵn ở `internal/domain/workflow/validation.go` (`stronglyConnectedComponents`),
    chuyển thể cho đồ thị dependency pin thay vì node/edge workflow. `content_hash` của resource CỐ TÌNH
    loại trừ `Provenance` và `Key` khỏi hash (chỉ hash nội dung + priority + global + selector) — bump
    `lastVerified` hoặc đổi tên key không được phép làm đổi identity dùng để dedupe conflict, có test riêng
    khẳng định điều này (`TestResourceIdentities_ContentHashExcludesProvenance`).
  - **V2-07, category-closed Policy document — chốt bằng validate CẤU TRÚC (không chỉ quy ước đặt tên):**
    `PolicyDocument{Category, Attempt/Completion/Permission/Context/Cleanup *Rules}` — CHỈ đúng 1 con trỏ
    rule khớp `Category` được phép non-nil, `ValidateDocument` reject cả 2 chiều (thiếu rule khớp category
    LẪN có rule của category khác) — biến quy ước tài liệu "chỉ semantics tương ứng" thành invariant kiểm
    tra được thật ở compile-time thay vì chỉ là quy ước không cưỡng chế.
  - **V2-07, `AttemptRules.RetryableErrorCodes` validate theo đúng enum AppError đóng đã có sẵn ở
    `go-core-spec.md` §18, cấm rõ `ISOLATION_ENFORCEMENT_UNAVAILABLE`/`INDETERMINATE` được liệt kê là
    retryable** — trích đúng rule đã ghi trong spec (§14/§18), không tự bịa danh sách retryable.
  - **V2-07, `AgentProfileDocument.Budget{MaxTokens}` cố tình HẸP** (chỉ token ceiling, không attempts hay
    timeout) — tránh 2 nguồn thẩm quyền cạnh tranh nhau cho cùng 1 khái niệm (attempts/timeout đã thuộc về
    `policy.AttemptRules`, pin riêng qua policy version của node dùng profile này).
  - **V2-07, cả 2 package độc lập với `internal/domain/command` (V2-05) dù merge trước** — vì agent chạy
    trong worktree tách biệt, tại thời điểm nó code thì V2-05 chưa merge vào nhánh nó branch từ đó, nên
    `agentprofile.Compatibility{OS,Toolchain}` là struct riêng, KHÔNG tái dùng `command.Compatibility` dù
    hình dạng giống hệt — đã ghi rõ trong code + PR làm điểm hợp nhất tương lai (dọn dẹp duplicate, không
    phải bug, không cấp bách).
  - **V2-07, "effective profile canonical/hash được" verify bằng test cụ thể**
    (`TestCompile_EffectiveProfile_CanonicalHash`): cùng document, khác dependency manifest (context
    policy ref khác version) → SourceHash giữ nguyên, CompiledHash đổi — đúng tách bạch ADR-012.

- **V2-07A, "Immutable AdapterBuildVersion registry" — khác hẳn V2-04..V2-07 về kiến trúc (có schema DB
  + application command thật, không chỉ domain package), nên tôi TỰ LÀM (không giao subagent song song)
  vì đây là task DUY NHẤT unblock lúc đó (V2-07B và V2-08 đều phụ thuộc V2-07A — đã tự sửa sai của chính
  mình khi ban đầu đề xuất chạy V2-07A//V2-08 song song, đọc lại kỹ `Phụ thuộc` field mới phát hiện V2-08
  phụ thuộc CẢ V2-07A):**
  - Research kỹ ADR-022 (toàn văn, rất chi tiết — có sẵn flow probe/register bắt buộc, token tuple bắt
    buộc bind đủ 8 thành phần, thứ tự re-hash ngoài transaction) trước khi code, đúng tinh thần đã áp dụng
    từ V2-02.
  - **Package mới `internal/domain/adapterbuild`** (domain, không import `internal/domain/definition`)
    — `CandidateTuple` (8 field ADR-022 yêu cầu), `CapabilityManifest`, `Build` (immutable, ID = content
    hash của chính CandidateTuple — không cần cột ID riêng, mirror đúng pattern resource identity của
    ADR-012), `CandidateToken` + `SignToken`/`VerifyToken` (HMAC-SHA256, canonicalize qua
    `authoring.Canonicalize` rồi ký — tái dùng đúng cơ chế V2-03 thay vì tự viết canonical serialize).
  - **Package mới `internal/app/adapterbuild`** — `ProbeAdapterBuild` (hash file thật qua
    `os.Open`+`sha256`, KHÔNG spawn executable — đúng scope "chưa spawn CLI provider thật", mirror
    `internal/app/doctor.sha256File` đã làm ở V1; mutation DUY NHẤT là lazy-bootstrap signing key, không
    ghi registry), `RegisterAdapterBuild` (verify token chữ ký+expiry TRƯỚC, rồi re-hash executable VÀ
    re-hash capability manifest NGOÀI transaction — đúng thứ tự bắt buộc ADR-022, so cả 2 kết quả đo mới
    với token, reject bằng `ErrExecutableDrift`/`ErrCapabilityManifestDrift` nếu lệch — TRƯỚC KHI mở
    transaction; chỉ mở `WithSerializedWrite` để insert giá trị VỪA ĐO, không phải giá trị trong token),
    `ListAdapterBuilds`/`GetAdapterBuild`.
  - **`ports.AdapterBuildRepository` là accessor MỚI trên `Tx` (không nhét vào `Definitions()`)** — lý do
    ghi rõ trong doc comment: nhét chung sẽ làm mờ đúng ranh giới ADR-022 vẽ ra (AdapterBuildVersion
    KHÔNG phải DefinitionKind). Đây là accessor ĐẦU TIÊN trên `Tx` có method thật ngay từ đầu (không phải
    placeholder rỗng như `Catalog`/`Work`/`Definitions`/`Runtime`/`Jobs` — đúng vì V2-07A sở hữu toàn bộ
    concern này end-to-end). Cũng thêm implementation thật cho `fake.Tx.AdapterBuilds()` (package
    `internal/app/ports/fake`) — không chỉ sqlite — để app-layer test không cần SQLite thật (đúng V1-05's
    "app service có thể test không SQLite"), và 2 implementation độc lập (fake + sqlite) cùng thoả 1
    interface là bằng chứng interface thật, không phải giả định.
  - **Migration 0005** (`adapter_build_versions` + `adapter_build_signing_keys`) — bảng RIÊNG hoàn toàn
    với `definitions`/`definition_versions` (0004), KHÔNG có cột `project_id` (registry installation-scoped
    đúng go-core-spec.md's command table). Signing key: đúng 1 row (`id=1` CHECK), rotate = UPDATE tại
    chỗ (mọi token đang lưu hành mất hiệu lực ngay — đúng ý ADR-022, không phải bug).
  - **Đủ 4 loại contract test theo Verify line + 1 test riêng cho "không lộ ra như DefinitionKind":**
    duplicate (`TestRegister_DuplicateIsIdempotent` — register 2 lần y hệt tuple → cùng 1 row, giữ nguyên
    `RegisteredBy` gốc), drift (`TestRegister_DriftCreatesNewBuildNeverOverwrites` — file đổi nội dung →
    row MỚI riêng biệt, row cũ giữ nguyên; PLUS `TestRegister_RejectsStaleTokenAfterExecutableSwapped` —
    tráo file GIỮA probe và register mà không probe lại → reject bằng `ErrExecutableDrift`, đây chính là
    bullet "Hoàn thành khi" #1 "build khác exact pin bị reject"), cross-project
    (`TestAdapterBuildVersionsTable_HasNoProjectIDColumn` bằng `PRAGMA table_info` + test hành vi list
    không lọc project), incompatible capability (`TestProbe_RejectsInvalidCapabilityManifest`, validate
    y hệt rule `agentregistry.validateCapabilities` đã có ở V0/V1), và
    `TestAdapterBuildVersion_NeverLeaksAsDefinitionKind` (ép `definition.Kind("ADAPTER_BUILD_VERSION").
    Valid()` phải false, kể cả khi ép kiểu tay qua `definition.DependencyPin`).
  - **Bullet "Hoàn thành khi" #2 "version đã publish không sửa được"**: không có method Update/Delete nào
    trên `AdapterBuildRepository` (test reflection `TestAdapterBuildRepository_HasNoUpdateOrDeleteMethod`
    + `TestBuild_NoExportedMutationMethod`), và grep xác nhận không có câu SQL `UPDATE`/`DELETE` nào nhắm
    vào `adapter_build_versions` ở bất kỳ đâu trong repo — bất biến là do THIẾT KẾ (ID content-addressed,
    không phải do access-control check).
  - **Bullet "Hoàn thành khi" #3 "run đang chạy không bị repin khi build mới được đăng ký"**: verify TRỰC
    TIẾP bằng `TestAdapterBuild_RegisteringNewBuildNeverRepinsExistingWorkflowVersion` — publish 1
    `WorkflowVersion` thật (tái dùng helper có sẵn từ V0: `openWorkflowTestStore`/`compileWorkflowVersion`),
    chụp `dependency_manifest` TRƯỚC, register 1 adapter build mới, đọc lại `dependency_manifest` SAU,
    assert byte-identical — chứng minh THẬT (không chỉ suy luận "vì code chưa nối dây gì") rằng đăng ký
    build mới có ĐÚNG ZERO side effect lên bất kỳ WorkflowVersion đã publish nào.
  - **Quyết định KHÔNG có sẵn trong doc, tự chọn + ghi rõ lý do:** (1) thời hạn token 5 phút (`tokenTTL`)
    — ADR-022 chỉ nói "expiry ngắn", không nêu con số; (2) key sinh 32 byte qua `crypto/rand`, lưu dạng
    hex trong bảng riêng — đơn giản nhất thoả đúng yêu cầu "per-installation, giữ ngoài repository (git),
    xoay được"; (3) Register re-hash CẢ capability manifest (không chỉ executable) — ADR-022 chỉ nói rõ
    "re-hash executable", nhưng câu "so sánh kết quả đo mới với token" (số nhiều) và tinh thần đóng TOCTOU
    khiến tôi mở rộng sang manifest luôn, vì cùng 1 lớp rủi ro (giá trị bị đổi giữa probe/register); (4)
    `OS`/`Toolchain`/`ConfigIdentity` là plain string đơn (không phải list như
    `command.Compatibility{OS []string}`) — vì đây là tuple ĐO ĐƯỢC của 1 build cụ thể (build này chạy
    trên OS/toolchain nào ngay lúc đo), khác hẳn ý nghĩa "khai báo hỗ trợ nhiều OS" của Command/AgentProfile.

- **V2-07B và V2-08, triển khai SONG SONG lần 2 (subagent worktree) — cả hai chỉ phụ thuộc các task đã
  merge, không phụ thuộc lẫn nhau.** Lưu ý quy trình: cả 2 lần agent báo "hoàn thành" đầu tiên đều bị
  RATE LIMIT giữa chừng (lỗi `session limit` từ API, reset 4pm Asia/Barnaul) khi mới đang research, CHƯA
  viết code/commit gì — xác nhận qua `git worktree list`/`git branch -a` không có gì sót lại, nên relaunch
  lại từ đầu an toàn, không mất việc. Sau khi relaunch, cả 2 agent tự hoàn thành, tự mở PR, tự CI-poll, tự
  merge — tôi verify lại ĐỘC LẬP trên máy chính sau khi cả 2 merge xong (`go build/vet/test ./...` toàn
  repo + `docs-coverage-check`) thay vì chỉ tin báo cáo.
  - **V2-07B — CLI `agentkit adapter probe|register|list|show` (`cmd/agentkit/adapter.go`), package CLI
    đầu tiên trong `cmd/agentkit` mở kết nối SQLite thật** (serve/worker/doctor/definition vẫn là stub).
    Quyết định khó nhất: dòng `Thực hiện` của V2-07B ghi rõ "Capability manifest chỉ lấy từ giá trị hệ
    thống tự đo, không nhận từ input của client" — MÂU THUẪN bề mặt với chữ ký `ProbeRequest` tôi tự
    thiết kế ở V2-07A (nhận `CapabilityManifest` như 1 field do caller cung cấp). Agent đã tự đọc code
    `claude.go`/`codex.go` thật và xác nhận: `Capabilities(ctx)` của CẢ HAI adapter hiện tại trả về hằng
    số cứng, KHÔNG spawn executable thật (giới hạn có sẵn từ V0/V1, ngoài phạm vi sửa ở đây — đó là việc
    của V5-06/07). Giải quyết bằng cách: CLI KHÔNG BAO GIỜ có flag `--supports-start` hay tương tự cho
    operator gõ tay — luôn gọi `Capabilities(ctx)` thật của `claude.Adapter`/`codex.Adapter` (constructor
    y hệt pattern V0/V1) rồi mới truyền vào `ProbeAdapterBuild`. Có test riêng khẳng định KHÔNG có kênh
    input nào cho operator tự gõ capability
    (`TestAdapterProbe_CapabilityManifestIsSystemMeasured_NotClientSuppliable`, và `--supports-start` bị
    reject như flag lạ). `OS`/`Toolchain` tự suy ra từ `runtime.GOOS`/`runtime.Version()`, không phải flag
    — cùng tinh thần "system-measured, not client input".
  - **V2-07B, thêm 1 test archtest MỚI đáng chú ý** (`internal/archtest/boundary_test.go`,
    `TestRegisterAdapterBuildTransactionNeverCallsFilesystemOrProcess`): parse AST thật của
    `RegisterAdapterBuild`, tìm closure truyền vào `WithSerializedWrite`, fail nếu closure đó gọi
    `os`/`exec`/`ioutil` hay `hashExecutableFile` — chứng minh TĨNH (không chỉ đọc code 1 lần rồi tin) quy
    tắc §11.1 "không gọi filesystem/process trong application transaction" mà chính tôi đã tuân thủ khi
    viết V2-07A, giờ có test canh giữ nếu ai đó vô tình phá vỡ sau này.
  - **V2-08 — mở rộng `internal/domain/workflow` (package V0/V1 cũ, KHÔNG tạo package mới):**
    `NodeType` từ 4 lên 9 giá trị (START/END/AGENT/COMMAND/MACHINE_GATE/APPROVAL/WAIT/ROUTER/FORK/JOIN),
    `Node.ExecutorRef string` (field cũ) bị XOÁ HẲN, thay bằng 6 field config-theo-type riêng
    (`node_config.go` mới) — đây là breaking change có chủ đích (task brief chỉ rõ đây chính là gap cần
    đóng), buộc sửa 4 call site khác trong repo (test fixtures ở sqlite/providers/spikeacceptance) sang
    dùng config kiểu mới, không đổi hành vi test nào.
  - **V2-08, AGENT/COMMAND/MACHINE_GATE pin THẲNG AgentProfileVersion/CommandVersion/GateVersion** (qua
    `definition.DependencyPin`, Kind tương ứng chính xác) — CHỌN Ý này thay vì đi qua BlockVersion trung
    gian dù package `block`'s doc comment tự mô tả "mọi node thực thi nên qua Block" — vì brief V2-08 nói
    rõ trực tiếp. Đây là 1 điểm 2 nguồn tài liệu (block.go's doc comment vs V2-08 brief) chỉ 2 hướng khác
    nhau — agent chọn theo brief cụ thể của chính task này, ghi rõ trade-off trong doc comment để dễ sửa
    nếu ý định thật khác.
  - **V2-08, JOIN mode = ALL/ANY/QUORUM lấy ĐÚNG NGUYÊN VĂN từ HE-14 lecture** ("Join phải khai báo ALL,
    ANY, QUORUM hoặc policy tùy chỉnh có version") — KHÔNG tự bịa thêm `COUNT`; "policy tùy chỉnh có
    version" (lựa chọn thứ 4 trong lecture) cố tình để ngoài phạm vi vì đó là "unfounded elaboration".
    ROUTER và FORK CỐ TÌNH không có config field nào (chỉ Outcomes/Edges như cũ) — logic route/fork-join
    topology (branch reconverge, quota vs incoming-edge-count) là phạm vi V2-09 (compiler), không phải
    V2-08 (schema).
  - **V2-08, shared-state schema mới** (`WorkflowDocument.SharedState`) — mỗi field có Name/Type (bao gồm
    `ARTIFACT_REF` cho "large artifacts stored by reference" theo đúng HE-14-M04)/Owner/Writers/Readers
    (BẮT BUỘC non-empty allowlist, không có mặc định "readers rỗng = ai cũng đọc được" — theo đúng
    convention allowlist-tường-minh đã dùng khắp nơi từ V2-05)/MergeRule
    (LAST_WRITE_WINS/APPEND/REJECT_ON_CONFLICT — lecture không nêu enum cụ thể, đây là lựa chọn tối thiểu
    tự chọn theo đúng gợi ý trong brief). Chỉ là SCHEMA khai báo, KHÔNG implement merge logic thật (đó là
    việc runtime/V4) — `WorkflowRun.SharedState` ở tầng runtime vẫn là `json.RawMessage` mờ như cũ, xác
    nhận không đụng.
  - **V2-08, "Hoàn thành khi: runtime không cần đọc authoring file để hiểu node" verify bằng test cụ thể**
    (`TestCompileRoundTripsNodeConfigWithoutLoss`): compile fixture đủ cả 9 node type, so sánh
    `reflect.DeepEqual` giữa `Document()` và `CanonicalContent()` unmarshal lại — chứng minh không mất
    field nào qua canonicalize, không chỉ khẳng định suông.
  - Cả 2 task đều gặp CI fail 1 lần đầu ở job V0-12 (`internal/app/workerpool`, package không ai trong 2
    task đụng tới) — rerun xanh sạch cả 2, đúng loại flake đã biết từ V2-04.

- **V2-09, "Graph/dependency compiler" — task lớn/phức tạp nhất session này, TỰ LÀM (không giao subagent)
  vì (a) là task DUY NHẤT unblock lúc đó (V2-10/11/12 phụ thuộc tuần tự, không có gì để chạy song song),
  (b) cần tổng hợp/quyết định kiến trúc xuyên suốt TOÀN BỘ những gì đã xây từ V2-04 đến V2-08. Research kỹ
  trước bằng 1 Explore agent (12 câu hỏi cụ thể) rồi tự đọc lại full source `internal/domain/workflow/*.go`
  trước khi thiết kế, đúng kỷ luật đã áp dụng cho V2-02/V2-07A.**
  - **Phát hiện quan trọng nhất từ research: RẤT NHIỀU check trong "reachability/terminal/routes/schema/
    cycle" đã có sẵn từ V0/V1/V2-08** (`validateNormalizedDocument` đã làm start/end count, reachability
    2 chiều, route consistency, bounded-cycle qua Tarjan SCC) — việc THẬT SỰ mới của V2-09 chỉ có 3 mảng:
    (1) fork/join TOPOLOGY (V2-08 đã tự defer rõ ràng bằng doc comment), (2) RESOLVE pin thật (V2-04..V2-08
    chỉ validate CẤU TRÚC pin, chưa BAO GIỜ resolve chống lại registry thật — `Compile()` nhận thẳng
    `DependencyManifest` do caller tự cung cấp, hoàn toàn không đọc node pins), (3) scope/capability check
    (ADR-013's "compiler xác nhận scope").
  - **Kiến trúc: KHÔNG đưa resolve logic (cần I/O) vào `internal/domain/workflow` (pure, không I/O) — tạo
    package app-layer MỚI `internal/app/workflowcompiler`** làm orchestration: validate structure thuần
    (pure) trước → walk node pins → resolve TẤT CẢ trong ĐÚNG 1 `WithReadOnly` (đảm bảo "exact registry
    snapshot" là 1 điểm-thời-gian nhất quán, không phải nhiều lần đọc rời rạc) → check scope/capability →
    build `workflow.DependencyManifest` đã resolve (Hash = CompiledHash THẬT lấy từ registry, không phải
    string do caller tự bịa) → gọi `workflow.Compile` pure function không đổi. `CompileAndResolve` KHÔNG
    tự publish/persist/idempotency/event — đó rõ ràng là phạm vi V2-10 ("CLI/API sau này dùng một
    contract, không gọi compiler/repository trực tiếp") theo đúng câu spec của chính V2-10.
  - **`ports.DefinitionsRepository` (đã là placeholder rỗng từ V1-05) lần đầu tiên có method THẬT**
    (`LoadVersion`) — đúng task được chỉ định sở hữu ("Definitions: V2" trong comment gốc), CHỈ thêm 1
    method resolve-only (không thêm publish/create — quyền đó vẫn của `ports.DefinitionPublisher` cũ, và
    publish-qua-Tx vẫn là việc V2-10). Đã sửa 1 chỗ nhỏ trong code CŨ (`loadSharedDefinitionVersion`,
    file `definitions.go` từ V2-02) để phân biệt `sql.ErrNoRows` thành `ports.ErrDefinitionVersionNotFound`
    riêng — trước đây lỗi not-found bị gộp chung vào lỗi generic qua `MapSQLiteError`, không thể
    `errors.Is` phân biệt được. Verify lại toàn bộ `internal/adapters/sqlite` sau khi sửa vẫn xanh.
  - **AGENT node có field MỚI `AdapterBuildID *string` (optional)** — pin trực tiếp
    `adapterbuild.Build.ID()` (content-hash), KHÔNG dùng `definition.DependencyPin` (không hợp vì
    AdapterBuildVersion không phải DefinitionKind, không có DefinitionID+VersionID). Để optional (không
    bắt buộc) vì bắt buộc sẽ breaking mọi fixture AGENT hiện có và registry vẫn là optional infrastructure
    ở Alpha (chưa ai bắt buộc phải đăng ký build trước khi author workflow).
  - **Fork/join topology validation (thuần, không cần I/O) sống trong `internal/domain/workflow/
    validation.go`, KHÔNG phải app layer** — vì đây chỉ cần cấu trúc đồ thị (Node/Edge map đã có sẵn
    trong validate), không cần dữ liệu resolve. Thuật toán: mỗi FORK, forward-walk từng branch DỪNG LẠI ở
    JOIN đầu tiên gặp; tất cả branch của 1 fork phải hội tụ ĐÚNG 1 join chung; JOIN không được sở hữu bởi
    2 fork khác nhau ("ambiguous branch identity"); QuorumCount (Mode=QUORUM) phải ≤ số branch thật của
    fork sở hữu nó. **Cố tình THU HẸP phạm vi: fork/join LỒNG NHAU (nested) bị reject thẳng là "not
    supported" thay vì cố implement nửa-đúng** — quyết định có ghi lý do rõ trong doc comment, vì matching
    nested fork/join đúng (kiểu dấu ngoặc cân bằng) phức tạp hơn nhiều so với nhu cầu authoring Alpha hiện
    tại. 8 test topology (bao gồm cả case nested/ambiguous/quorum-vượt-branch-count) đều pass ngay lần đầu.
  - **Scope/capability check (ADR-013) — GAP THẬT được research phát hiện và phải tự quyết định cách xử
    lý: bề mặt khai báo scope/capability trong schema hiện tại BỊ PHÂN MẢNH** — `ScopeSelector` chỉ có ở
    `BlockDocument` (node KHÔNG BAO GIỜ pin Block, chỉ pin AgentProfile/Command/Gate trực tiếp theo đúng
    quyết định V2-08), `RequiredCapabilities` chỉ có ở Block+AgentProfile (không có ở Command/Gate). Tín
    hiệu "repository nào bị động tới" CỤ THỂ DUY NHẤT hiện có trong schema là `CommandDocument.
    CwdRepositoryTarget`. **Quyết định: check dựa ĐÚNG trên dữ liệu thật sự tồn tại** — thu thập
    `CwdRepositoryTarget` từ mọi COMMAND node đã resolve; nếu >1 giá trị khác nhau, bắt buộc phải có ít
    nhất 1 Policy (Category=PERMISSION, resolve qua PolicyRefs của bất kỳ node nào) có
    `GrantedCapabilities` chứa `INTEGRATION_MULTI_REPOSITORY_WRITE` — đúng nghĩa đen "policy grant" +
    "compiler xác nhận scope" trong ADR-013. Ghi rõ trong doc comment: đây là check HẸP HƠN mô hình đầy đủ
    ADR-013 mô tả, giới hạn đúng bằng dữ liệu schema THẬT hiện có, không tự bịa thêm cơ chế — nếu sau này
    Command/Gate/AgentProfile có `ScopeSelector` riêng (mirror Block), check này sẽ tự nhiên thấy đầy đủ
    hơn mà không cần sửa logic.
  - **"Hoàn thành khi" verify bằng test cụ thể, không chỉ suy luận:**
    `TestCompileAndResolve_SameRegistrySnapshot_SameCompiledHash` (2 lần compile cùng document+registry
    snapshot → cùng `ContentHash()`) và `TestCompileAndResolve_DifferentPin_DifferentCompiledHash` (đổi
    version 1 pin → khác hash) — cả 2 pass ngay lần đầu, cùng với 1 golden fixture test và 1 integration
    test chạy full chain qua SQLite THẬT (publish AgentProfile thật → probe/register AdapterBuild thật →
    CompileAndResolve → đúng hash resolve) để chứng minh không chỉ fake mà implementation thật cũng thoả
    contract — đúng kỷ luật "2 implementation độc lập cùng thoả 1 interface" đã áp dụng từ V2-07A.
  - Fuzz test `FuzzCompileJSON_NeverPanics` chạy thật 21s, 701,865 exec, 0 panic — đúng yêu cầu Verify
    "malformed corpus, fuzz/property" của task.

- **V2-10, "Validate/publish application commands" — giao subagent với brief RẤT chi tiết (tôi tự nghiên
  cứu + thiết kế kiến trúc trước, gồm cả cách giải quyết vấn đề "double-compile"/"nested transaction" cho
  Workflow, rồi mã hoá thành quyết định cụ thể trong brief). CI xanh cả 6 job ngay lần đầu dù đây là task
  refactor code CŨ (definitions.go, workflow_store.go) nhiều nhất từ trước đến giờ.**
  - **Package mới `internal/app/definitions`**: `CreateDefinition`, `ValidateDraft` (dry-run, không side
    effect), `PublishDefinitionVersion` (atomic: receipt-check → compile → insert version + event +
    receipt trong CÙNG 1 `WithSerializedWrite`), `ListVersions`, `LoadVersion`. Đúng pattern
    `handleCreateProject` (V1-06) — KHÔNG dùng pattern content-addressed `InsertIfAbsent` của V2-07A (2
    pattern khác nhau tồn tại song song trong repo, đã research rõ và chọn đúng pattern task này cần).
  - **`ports.DefinitionsRepository` (chỉ có `LoadVersion` từ V2-09) được bổ sung ĐẦY ĐỦ**:
    `CreateDefinition`, `PublishVersion` (8 kind chung), `PublishWorkflowVersion` (Workflow riêng — nhận
    THẲNG `workflow.WorkflowVersion` đã compile sẵn, không đi qua `PublishVersionRequest`), `ListVersions`.
    Refactor `definitions.go`/`workflow_store.go`: tách logic SQL cốt lõi ra hàm `tx *sql.Tx`-scoped riêng
    (`createSharedDefinitionTx`, `publishSharedDefinitionVersionTx`, `publishWorkflowVersionTx`,...),
    method `*Store` cũ giữ nguyên hành vi (chỉ là wrapper mỏng gọi hàm mới) — xác nhận qua chạy lại TOÀN BỘ
    test cũ của `internal/adapters/sqlite` không đổi 1 assertion nào.
  - **Tự bắt được 1 BUG THẬT có sẵn từ V2-02, chưa ai phát hiện**: đường publish Workflow cũ
    (`publishWorkflowDefinitionVersion`) convert `definition.DependencyPin` (không có field Hash) sang
    `workflow.DependencyPin` (CÓ field Hash bắt buộc) nhưng bỏ trống Hash — `workflow.Compile`'s
    `normalizeManifest` đáng lẽ phải reject pin thiếu hash, nhưng bug này CHƯA TỪNG bị test cũ bắt vì
    `TestPublishDefinitionVersion_UnifiedCommand_WorkflowAndSharedKind` (V2-02) chỉ test với dependency
    manifest RỖNG. Giải quyết bằng cách KHÔNG đi qua đường cũ: `PublishDefinitionVersion` (package mới)
    với Kind=Workflow gọi `workflowcompiler.CompileAndResolve` (V2-09) trước, lấy thẳng
    `workflow.WorkflowVersion` đã có hash ĐẦY ĐỦ, publish qua `DefinitionsRepository.PublishWorkflowVersion`
    mới — đường cũ bị lỗi vẫn còn nguyên (không sửa, ngoài phạm vi task này) nhưng không còn ai dùng qua
    đường mới. Ghi rõ đây là follow-up nên làm sau nếu có caller khác dùng contract cũ với dependency thật.
  - **Quyết định rõ ràng cho câu hỏi "duplicate publish có tạo event mới không" (đã yêu cầu suy nghĩ kỹ
    trong brief):** (1) cùng idempotency key + cùng request hash → receipt replay chặn TRƯỚC KHI chạm vào
    publish/event — 0 event mới; (2) cùng compiled content nhưng KHÁC idempotency key → vẫn publish qua
    (dedupe DefinitionID+CompiledHash ở tầng repository trả về version cũ) VÀ vẫn ghi receipt riêng (audit
    đúng theo command), nhưng có pre-check so `CompiledHash` để CHẶN event `DefinitionVersionPublished`
    trùng — 0 event mới. Trong lúc làm rõ điều này, tự phát hiện thêm 1 rủi ro Sequence-collision thật
    (event `DefinitionVersionPublished` phải key theo Version ID bất biến làm AggregateID, không phải
    Definition ID — vì `DefinitionCreated` đã chiếm Sequence=1 trên chính Definition ID đó) — đã sửa đúng.
  - **2 bug nhỏ khác tự bắt trong chính test của mình** (không phải bug code chính): 1 test helper tự vô
    tình gán nhầm `RequestHash` vào field `Type`, làm hỏng test conflict; 1 chỗ `Name` không khớp giữa
    `CreateDefinition` và publish request làm `ensureWorkflowDefinition`'s identity check tự fail.
  - Verify local đầy đủ (build/vet/test toàn repo/docs-coverage/gofmt) trước khi commit, xác nhận lại độc
    lập trên máy chính sau merge — tất cả xanh.

- **V2-11, "Definition CLI" — giao subagent, brief tham chiếu đúng pattern `cmd/agentkit/adapter.go` đã có
  từ V2-07B. CI xanh cả 6 job ngay lần đầu.**
  - **6 subcommand thay vì 5** (`create` thêm vào ngoài validate/publish/list/show/diff task brief liệt
    kê) — lý do hợp lý: `PublishDefinitionVersion` (V2-10) cần Definition đã tồn tại trước, gộp
    auto-create vào publish sẽ trộn lẫn 2 loại input (`--name`/`--project-id` chỉ cần cho create) vào 1
    command — tách riêng rõ ràng hơn.
  - **Tự bắt được 1 bug thật trong CHÍNH thiết kế của brief tôi đưa**: tôi khuyến nghị dùng VersionID cố
    định kiểu `"candidate"` (dựa trên giả định "server sẽ tự re-allocate ID theo content hash") — SAI, vì
    đọc kỹ `publishSharedDefinitionVersionTx`/`publishWorkflowVersionTx` thì `id` là PRIMARY KEY lưu
    THẲNG giá trị candidate, không re-allocate. Nếu dùng ID cố định, publish 2 nội dung KHÁC nhau sẽ đụng
    PK. Agent tự phát hiện, sửa bằng cách mint ID mới mỗi lần qua `internal/app/idsource.Random{}` (đúng
    convention "app code tạo ID" đã có sẵn trong repo) — đúng ví dụ cho thấy brief của tôi cũng có thể sai,
    và giá trị của việc agent tự verify lại thay vì tin brief một cách mù quáng.
  - **`diff` không có format chuẩn quy định sẵn (đã xác nhận không có trong doc)** — agent tự chọn: in
    thông tin tóm tắt 2 version (id/kind/hash/publishedBy/publishedAt) + unified line-diff (tự viết thuật
    toán LCS O(n·m), repo không có thư viện diff sẵn) trên JSON đã re-indent (vì `CanonicalSource()` luôn
    là JSON 1 dòng, diff trực tiếp vô nghĩa).
  - **Tự bắt được 1 BUG THẬT ở hạ tầng CHUNG đã merge từ V1-04A** (`internal/adapters/sqlite/txrunner.go`):
    `runTx` LUÔN route lỗi trả về từ `fn` (closure của caller) qua `MapSQLiteError`, kể cả khi đó là lỗi
    domain sạch (`workflowcompiler.ResolutionError`, `authoring.Diagnostics`,...) — khiến message rõ ràng
    WHAT/WHY/FIX bị THAY THẾ bằng "sqlite: unexpected error" chung chung, MÂU THUẪN với chính doc comment
    của hàm ("fn's own returned error... passes through MapSQLiteError unchanged in shape") và khác hành
    vi `fake.UnitOfWork` (không wrap gì cả). **Lý do KHÔNG TEST NÀO trong suốt session bắt được bug này:
    mọi test check lỗi đều dùng `errors.Is`/`errors.As`, mà `apperror.Error.Unwrap()` trả về đúng lỗi gốc
    (`e.cause`), nên `errors.Is` vẫn xuyên qua được lớp wrap — CHỈ có `.Error()` (chuỗi hiển thị cho người)
    mới bị ảnh hưởng, và V2-11 là task ĐẦU TIÊN thực sự in `.Error()` ra cho operator xem qua CLI.** Đã tự
    verify kỹ (xác nhận qua đọc `apperror.Wrap`'s `Unwrap()` implementation): sửa bằng cách thêm type
    `fnError` đánh dấu lỗi đến từ `fn` để `runTx` trả nguyên lỗi đó thay vì wrap, CHỈ còn lỗi THẬT của
    chính `runTx`/`runTxOnce` (BeginTx/Commit fail) mới qua `MapSQLiteError`. Tôi đã tự đọc lại code fix
    này VÀ tự verify logic `Unwrap()` ĐỘC LẬP (không chỉ tin báo cáo agent) trước khi merge — xác nhận đây
    là fix đúng, phạm vi hẹp, không đổi hành vi `isSQLiteBusy` retry-detection (vẫn xuyên qua `Unwrap()`).
    Full test suite + race job trên CI (Linux) đều xanh sau fix.

- **V2-12, "Definition plane gate" — task đóng cả phase V2, tự làm trực tiếp (không giao subagent) vì tính
  chất "closing gate" của nó.**
  - **Thu hẹp phạm vi thật của task dựa trên đọc lại hạ tầng CI hiện có, trước khi viết dòng code nào**:
    dòng Verify yêu cầu "Windows/Linux canonical diff" và "architecture import check" — cả hai đã có sẵn,
    chạy xanh trên MỌI PR suốt session này (SPK-13 job so sánh evidence manifest 2 OS; `internal/archtest`
    chạy trong mọi `go test ./...`) — nên phạm vi thật của V2-12 chỉ còn "viết một integration test đóng"
    (đúng như tên gọi "gate"), không phải xây thêm hạ tầng cross-platform mới.
  - **Diễn giải "publish graph thực tham chiếu mọi definition kind" theo đúng ràng buộc schema thật (không
    tự bịa field pin mới)**: publish thật cả 9 kind, nhưng SKILL/LAYER/ENGINEERING_PACK/BLOCK được publish
    ĐỘC LẬP (không nằm trong dependency manifest của Workflow đã compile) vì V2-08's schema chỉ cho node
    pin thẳng AGENT_PROFILE/COMMAND/GATE/POLICY — không có field nào cho phép 1 node pin Block/Skill/
    Layer/EngineeringPack trực tiếp (đây là thiết kế CÓ CHỦ ĐÍCH từ V2-08, không phải thiếu sót). Test vẫn
    verify graph compile được resolve đủ 5/5 loại kind thật sự reachable (AGENT_PROFILE, COMMAND, GATE,
    POLICY×2, ADAPTER_BUILD_VERSION) qua `v1Fields.Dependencies()`.
  - **"đăng ký qua V2-07B" hiểu là gọi thẳng application command `internal/app/adapterbuild.ProbeAdapterBuild`
    + `RegisterAdapterBuild`** (đúng function `cmd/agentkit/adapter.go`'s CLI của V2-07B gọi bên trong) chứ
    không build/spawn thật binary `agentkit` — nhất quán với convention "test không spawn subprocess thật"
    đã có sẵn trong repo (`internal/archtest`'s `TestRegisterAdapterBuildTransactionNeverCallsFilesystemOrProcess`
    của chính V2-07B).
  - **Restart** mirror đúng `TestFoundationCleanStartRestartContract` (V1-12): đóng `*sqlite.Store`, mở lại
    cùng file DB, tạo `UnitOfWork` mới — publish V2 (đổi 1 Command version pin) qua unit-of-work MỚI này.
  - **"Load V1 exact" phải gọi thẳng `store2.LoadWorkflowVersion`** (không qua `internal/app/definitions.LoadVersion`)
    vì `LoadVersion` (V2-09/V2-10) chỉ đọc bảng `definition_versions` dùng chung của 8 kind kia — Workflow
    có bảng riêng (`workflow_versions`), đúng như chính doc comment của `DefinitionsRepository.LoadVersion`
    đã ghi rõ ("Workflow keeps its own dedicated tables"). `internal/integration` được phép import thẳng
    cả `internal/adapters/sqlite` lẫn `internal/app` cùng lúc (tiền lệ đã có từ `foundation_test.go`, V1-12)
    nên gọi thẳng `Store.LoadWorkflowVersion` là đúng convention, không phải lách luật.
  - **3 test reject-case ở tầng tích hợp** (dependency drift: pin 1 Command VersionID chưa từng publish;
    adapter drift: pin 1 AdapterBuildID chưa từng đăng ký; sai kind thực thi: AGENT node pin thẳng SKILL
    thay vì AGENT_PROFILE) — đã đọc kỹ code thật (`workflowcompiler.resolveReferences`,
    `workflow.validateExecutorPin`) để xác nhận cả 3 case đều thật sự reject (2 case đầu ở tầng resolve
    registry qua `ports.ErrDefinitionVersionNotFound`/`ports.ErrAdapterBuildNotFound`; case thứ 3 bị chặn
    SỚM HƠN, ngay ở validate cấu trúc tĩnh `workflow.ValidateDocument` trước khi chạm registry) — không chỉ
    tin logic suy luận, đã chạy thật và xác nhận cả 3 đều fail đúng như kỳ vọng.
  - **Blocker môi trường tự phát hiện và tự giải quyết**: lúc bắt đầu verify, `go`/`gofmt` KHÔNG có trên
    PATH hệ thống (đã kiểm tra registry, Program Files, mọi vị trí cài đặt chuẩn — không thấy) — tưởng là
    Go bị gỡ khỏi máy. Đọc lại chính dòng ghi chú ở đầu file báo cáo này (dòng 8, đã ghi từ trước) mới nhớ
    ra: repo có toolchain Go cục bộ vendor sẵn tại `.tools/go1.27.0/go/bin/go.exe` — dùng đúng path này thì
    build/vet/test đều chạy bình thường. Bài học: PATH hệ thống có thể đổi giữa các phiên làm việc, nhưng
    toolchain vendor trong chính repo thì không — nên kiểm tra `.tools/` trước khi kết luận "thiếu Go".
  - **Verify local đầy đủ, tất cả xanh ngay lần chạy đầu** (không cần sửa gì ở code test sau khi build lần
    đầu): `go build ./...`, `go vet ./...`, `go test ./...` (toàn bộ package, gồm `internal/integration` và
    `internal/archtest`), `go run ./cmd/docs-coverage-check` (debt = 0), `gofmt -w` trên file mới (đã có
    lệch format nhỏ, tự sửa bằng `-w`, chạy lại test xác nhận không đổi hành vi).
