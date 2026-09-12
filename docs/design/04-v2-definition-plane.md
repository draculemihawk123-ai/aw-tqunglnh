# V2 — Definition plane và declarative authoring

> Entry: V1 gate pass.
>
> Exit: người dùng validate/publish version bất biến cho Workflow, Block, Skill, Layer, Engineering
> Pack, Agent Profile, Command, Gate và Policy qua cùng application contract.

> Phạm vi task mặc định: kế thừa mục 3 của `00-roadmap.md`.

> Ghi chú lịch sử tên lệnh: các task V2 đã triển khai dưới tên `agentkit`. ADR-028/V6-15A migrate cùng
> command groups sang `aw` mà không đổi application contract; `agentkit-spike` không bị đổi tên.

## V2-01 — Definition identity và lifecycle chung

- **Mục tiêu:** model `Definition` mutable và `Version` immutable không trộn payload runtime.
- **Phụ thuộc:** V1.
- **Thực hiện:** kind-safe IDs/status/version metadata, project/global scope, create/archive rules.
- **Verify:** domain transition tests; không có update/delete public trên Version.
- **Hoàn thành khi:** các kind reuse lifecycle behavior nhưng vẫn có payload type riêng.
- **Nguồn:** AK-ARCH-001, HE-14-M01.

## V2-02 — Definition schema migrations

- **Mục tiêu:** thêm các cặp definition/version và dependency pins đúng thiết kế.
- **Phụ thuộc:** V2-01.
- **Thực hiện:** migration mới cho canonical source/source hash, compiled snapshot/compiled hash,
  dependency/resource pins và adapter build registry; unique/FK/check, direct project scope, immutable
  repository methods.
- **Verify:** migration upgrade/idempotency/cross-project constraint tests.
- **Hoàn thành khi:** publish concurrent không cấp trùng version number/hash.
- **Nguồn:** ROADMAP-§7.

## V2-03 — Strict YAML/JSON decoder và canonicalizer

- **Mục tiêu:** cùng semantic authoring input tạo cùng canonical JSON/SourceHash trên Windows/Linux.
- **Phụ thuộc:** V2-01.
- **Thực hiện:** reject unknown/duplicate/implicit ambiguous values; sort set-like fields, preserve
  semantic list; exclude publisher metadata khỏi SourceHash.
- **Verify:** golden/property/fuzz tests YAML/JSON/map order/platform.
- **Hoàn thành khi:** invalid input trả location + WHAT/WHY/FIX.
- **Nguồn:** AK-ARCH-001, AK-ARCH-020.

## V2-04 — BlockVersion contract

- **Mục tiêu:** typed responsibility, schemas, outcomes, executor/policy refs.
- **Phụ thuộc:** V2-02, V2-03.
- **Thực hiện:** node compatibility metadata, required capability, timeout, scope selector và done condition.
- **Verify:** valid/invalid block fixtures và hash tests.
- **Hoàn thành khi:** block không thể dùng Skill/Layer như executable ref.
- **Nguồn:** HE-14-M02.

## V2-05 — Command và Gate versions

- **Mục tiêu:** executable authority tách khỏi resource.
- **Phụ thuộc:** V2-04.
- **Thực hiện:** argv placeholder allowlist, cwd target, OS/toolchain, env/network/secret permission,
  timeout/output contract; Gate thêm verdict/evidence mapping.
- **Verify:** reject shell string, unknown placeholder, unpinned executable và missing evidence mapping.
- **Hoàn thành khi:** publish payload tạo exact content/dependency hash.
- **Nguồn:** AK-ARCH-017.

## V2-06 — Skill, Layer và Engineering Pack versions

- **Mục tiêu:** resource thụ động có selector, priority, provenance, dependency/conflict.
- **Phụ thuộc:** V2-02, V2-03.
- **Thực hiện:** priority classes, resource identity `owner_version + resource_key + content_hash`,
  owner/lastVerified, pack graph resolution; hard-constraint conflict fail closed.
- **Verify:** selector/dependency/cycle/conflict/golden manifest tests.
- **Hoàn thành khi:** install/resolve không tạo command, gate hay permission grant.
- **Nguồn:** HE-04-M02, HE-04-M04, HE-03-M06, HE-04-M03.

## V2-07 — Agent Profile và Policy versions

- **Mục tiêu:** pin provider/model/context/tool refs và policy semantics độc lập.
- **Phụ thuộc:** V2-02, V2-03.
- **Thực hiện:** schemas cho attempt, completion, permission, context, cleanup; context route là exact
  PolicyVersion; compatibility validation; profile chỉ tham chiếu published dependencies.
- **Verify:** missing capability, OS mismatch, invalid budget và policy dependency tests.
- **Hoàn thành khi:** effective profile canonical/hash được.
- **Nguồn:** ROADMAP-§2.

## V2-07A — Immutable AdapterBuildVersion registry

- **Mục tiêu:** provider adapter build/capability là dependency có identity, không phải string cấu hình
  mutable ngoài manifest.
- **Phụ thuộc:** V2-02, V2-03, V2-07.
- **Phạm vi:** registry schema và application commands (`ProbeAdapterBuild`, `RegisterAdapterBuild`,
  list/show); chưa spawn CLI provider thật — V5-06/07 mới xác minh với executable thật.
- **Thực hiện:** đăng ký provider key, build ID, executable/protocol hash, OS/toolchain và capability
  manifest bất biến; exact pin được resolve vào compiled dependency manifest. Theo ADR-022,
  AdapterBuildVersion **không** là DefinitionKind: nó không dùng cặp definition/version, không đi qua
  `POST /definitions/{kind}/publish` và không xuất hiện trong catalog authoring.
- **Verify:** duplicate/drift/cross-project/incompatible capability contract tests; test khẳng định
  registry không lộ ra như một DefinitionKind.
- **Hoàn thành khi:** build khác exact pin bị reject, version đã publish không sửa được, và run đang
  chạy không bị repin khi build mới được đăng ký.
- **Nguồn:** ADR-012, ADR-022, AK-ARCH-020A, HE-02-M07.

## V2-07B — Operator surface cho adapter build

- **Mục tiêu:** operator có đường thật để đưa một build mới vào registry. Thiếu task này, nâng cấp
  provider CLI sẽ khóa mọi workflow có AGENT node mà không có lối thoát trong sản phẩm.
- **Phụ thuộc:** V2-07A.
- **Phạm vi:** CLI `agentkit adapter probe|register|list|show` — một command group riêng, không nằm dưới
  `agentkit definition` vì AdapterBuildVersion không phải DefinitionKind; command hardening/API thuộc V6-10I/V6-10J và UI thuộc V7-05.
- **Thực hiện:** `probe` chạy executable đã cấu hình và in candidate fingerprint/protocol/capability mà
  **không** ghi registry; `register` yêu cầu operator xác nhận candidate rồi tạo AdapterBuildVersion bất
  biến; `list|show` trả JSON ổn định. Republish Workflow/Agent Profile là bước riêng, không tự động.
  Đóng TOCTOU theo đúng thứ tự của ADR-022: probe phát candidate token **do server ký, có expiry**;
  register **re-probe/re-hash ngoài database transaction** ngay trước commit, đối chiếu với token, reject
  khi mismatch hoặc token hết hạn, rồi transaction chỉ persist giá trị server vừa đo. Không re-hash bên
  trong transaction vì §11.1 cấm gọi filesystem/process trong application transaction. Capability manifest
  chỉ lấy từ giá trị hệ thống tự đo, không nhận từ input của client.
- **Verify:** CLI golden tests; probe không mutate registry; register hai lần cùng fingerprint là
  idempotent; register fingerprint khác tạo build mới; assert run đang chạy giữ nguyên build đã pin;
  test thay executable giữa probe và register phải bị reject; token hết hạn bị reject; token giả mạo
  chữ ký bị reject. Vì token bind **toàn bộ tuple**, cần thêm mismatch case cho protocol version,
  capability-manifest hash và OS/toolchain/config identity — mỗi cái đều phải reject riêng. Thêm test
  token vẫn dùng được **qua process restart** (probe và register là hai lần gọi CLI khác nhau nên signing
  key phải bền, không phải key trong bộ nhớ một lần chạy) và test **xoay signing key làm token cũ mất
  hiệu lực**. Test capability do client gửi lên bị bỏ qua thay vì được persist; và architecture test
  khẳng định không có lời gọi filesystem/process nào nằm trong transaction đăng ký.
- **Hoàn thành khi:** kịch bản “nâng cấp Claude CLI rồi chạy lại workflow” hoàn tất được bằng CLI, không
  cần sửa SQLite thủ công.
- **Nguồn:** ADR-022, GC-DS-08.

## V2-08 — Workflow graph schema đầy đủ Alpha

- **Mục tiêu:** author START/END/AGENT/COMMAND/MACHINE_GATE/APPROVAL/WAIT/ROUTER/FORK/JOIN.
- **Phụ thuộc:** V2-04…V2-07 và V2-07A.
- **Thực hiện:** typed node config, edges/outcomes, shared-state writers, attempt/iteration/join policies.
- **Verify:** schema fixtures cho từng node type.
- **Hoàn thành khi:** runtime không cần đọc authoring file để hiểu node.
- **Nguồn:** HE-14-M03, HE-14-M04.

## V2-09 — Graph/dependency compiler

- **Mục tiêu:** publish reject toàn bộ invalid graph/dependency trước runtime.
- **Phụ thuộc:** V2-08.
- **Thực hiện:** reachability/terminal/routes/schema/cycle/fork-join/scope/capability checks; mặc định
  đúng một repository WRITE, multi-repo chỉ với integration capability; resolve exact definition,
  resource và AdapterBuildVersion pins; tạo immutable dependency manifest và compiled snapshot.
- **Verify:** malformed corpus, fuzz/property và compiler golden tests.
- **Hoàn thành khi:** cùng SourceHash + exact registry snapshot tạo cùng CompiledSnapshotHash; thay một
  dependency pin tạo hash khác.
- **Nguồn:** AK-ARCH-003, HE-14-M06, AK-ARCH-005B.

## V2-10 — Validate/publish application commands

- **Mục tiêu:** CLI/API sau này dùng một contract, không gọi compiler/repository trực tiếp.
- **Phụ thuộc:** V2-09.
- **Thực hiện:** create definition, validate draft, publish, list/load version, idempotency/audit;
  deduplicate chỉ theo `DefinitionID + CompiledSnapshotHash`, không theo SourceHash.
- **Verify:** handler tests duplicate publish/concurrent publish/cross-project ref.
- **Hoàn thành khi:** publish event + version + receipt atomic.
- **Nguồn:** AK-ARCH-005B, GC-INV-15.

## V2-11 — Definition CLI

- **Mục tiêu:** Alpha usable chưa cần UI: create/validate/publish/inspect/diff từ file.
- **Phụ thuộc:** V2-10.
- **Thực hiện:** `agentkit definition create|validate|publish|list|show|diff`; JSON output; safe diagnostics.
- **Verify:** CLI golden và temp DB integration tests.
- **Hoàn thành khi:** không cần sửa SQLite thủ công để author/publish.
- **Nguồn:** ROADMAP-§2.

## V2-12 — Definition plane gate

- **Mục tiêu:** khóa immutability, reproducibility và passive-resource boundary.
- **Phụ thuộc:** V2-01…V2-11, V2-07A và V2-07B.
- **Thực hiện:** publish graph thực tham chiếu mọi definition kind cộng một AdapterBuildVersion đã đăng
  ký qua V2-07B, restart, publish V2, load V1 exact.
- **Verify:** full test/vet, Windows/Linux canonical diff, architecture import check.
- **Hoàn thành khi:** V1 snapshot không đổi; dependency/adapter drift và invalid executable authority
  đều bị reject.
- **Nguồn:** AK-ARCH-002, GC-INV-07.
