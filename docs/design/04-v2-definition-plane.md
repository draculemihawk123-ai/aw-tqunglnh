# V2 — Definition plane và declarative authoring

> Entry: V1 gate pass.
>
> Exit: người dùng validate/publish version bất biến cho Workflow, Block, Skill, Layer, Engineering
> Pack, Agent Profile, Command, Gate và Policy qua cùng application contract.

## V2-01 — Definition identity và lifecycle chung

- **Mục tiêu:** model `Definition` mutable và `Version` immutable không trộn payload runtime.
- **Phụ thuộc:** V1.
- **Thực hiện:** kind-safe IDs/status/version metadata, project/global scope, create/archive rules.
- **Verify:** domain transition tests; không có update/delete public trên Version.
- **Hoàn thành khi:** các kind reuse lifecycle behavior nhưng vẫn có payload type riêng.

## V2-02 — Definition schema migrations

- **Mục tiêu:** thêm các cặp definition/version và dependency pins đúng thiết kế.
- **Phụ thuộc:** V2-01.
- **Thực hiện:** migration mới, unique/FK/check, direct project scope, immutable repository methods.
- **Verify:** migration upgrade/idempotency/cross-project constraint tests.
- **Hoàn thành khi:** publish concurrent không cấp trùng version number/hash.

## V2-03 — Strict YAML/JSON decoder và canonicalizer

- **Mục tiêu:** cùng semantic input tạo cùng canonical JSON/hash trên Windows/Linux.
- **Phụ thuộc:** V2-01.
- **Thực hiện:** reject unknown/duplicate/implicit ambiguous values; sort set-like fields, preserve
  semantic list; exclude publisher metadata khỏi content hash.
- **Verify:** golden/property/fuzz tests YAML/JSON/map order/platform.
- **Hoàn thành khi:** invalid input trả location + WHAT/WHY/FIX.

## V2-04 — BlockVersion contract

- **Mục tiêu:** typed responsibility, schemas, outcomes, executor/policy refs.
- **Phụ thuộc:** V2-02, V2-03.
- **Thực hiện:** node compatibility metadata, required capability, timeout, scope selector và done condition.
- **Verify:** valid/invalid block fixtures và hash tests.
- **Hoàn thành khi:** block không thể dùng Skill/Layer như executable ref.

## V2-05 — Command và Gate versions

- **Mục tiêu:** executable authority tách khỏi resource.
- **Phụ thuộc:** V2-04.
- **Thực hiện:** argv placeholder allowlist, cwd target, OS/toolchain, env/network/secret permission,
  timeout/output contract; Gate thêm verdict/evidence mapping.
- **Verify:** reject shell string, unknown placeholder, unpinned executable và missing evidence mapping.
- **Hoàn thành khi:** publish payload tạo exact content/dependency hash.

## V2-06 — Skill, Layer và Engineering Pack versions

- **Mục tiêu:** resource thụ động có selector, priority, provenance, dependency/conflict.
- **Phụ thuộc:** V2-02, V2-03.
- **Thực hiện:** priority classes, resource refs/hash, owner/lastVerified, pack graph resolution;
  hard-constraint conflict fail closed.
- **Verify:** selector/dependency/cycle/conflict/golden manifest tests.
- **Hoàn thành khi:** install/resolve không tạo command, gate hay permission grant.

## V2-07 — Agent Profile và Policy versions

- **Mục tiêu:** pin provider/model/context/tool refs và policy semantics độc lập.
- **Phụ thuộc:** V2-02, V2-03.
- **Thực hiện:** schemas cho attempt, completion, permission, context, cleanup; compatibility validation;
  profile chỉ tham chiếu published dependencies.
- **Verify:** missing capability, OS mismatch, invalid budget và policy dependency tests.
- **Hoàn thành khi:** effective profile canonical/hash được.

## V2-08 — Workflow graph schema đầy đủ Alpha

- **Mục tiêu:** author START/END/AGENT/COMMAND/MACHINE_GATE/APPROVAL/WAIT/ROUTER/FORK/JOIN.
- **Phụ thuộc:** V2-04…V2-07.
- **Thực hiện:** typed node config, edges/outcomes, shared-state writers, attempt/iteration/join policies.
- **Verify:** schema fixtures cho từng node type.
- **Hoàn thành khi:** runtime không cần đọc authoring file để hiểu node.

## V2-09 — Graph/dependency compiler

- **Mục tiêu:** publish reject toàn bộ invalid graph/dependency trước runtime.
- **Phụ thuộc:** V2-08.
- **Thực hiện:** reachability/terminal/routes/schema/cycle/fork-join/scope/capability checks; resolve exact
  pins; create immutable dependency manifest.
- **Verify:** malformed corpus, fuzz/property và compiler golden tests.
- **Hoàn thành khi:** cùng input + registry snapshot tạo cùng compiled hash.

## V2-10 — Validate/publish application commands

- **Mục tiêu:** CLI/API sau này dùng một contract, không gọi compiler/repository trực tiếp.
- **Phụ thuộc:** V2-09.
- **Thực hiện:** create definition, validate draft, publish, list/load version, idempotency/audit.
- **Verify:** handler tests duplicate publish/concurrent publish/cross-project ref.
- **Hoàn thành khi:** publish event + version + receipt atomic.

## V2-11 — Definition CLI

- **Mục tiêu:** Alpha usable chưa cần UI: validate/publish/inspect/diff từ file.
- **Phụ thuộc:** V2-10.
- **Thực hiện:** `agentkit definition validate|publish|list|show|diff`; JSON output; safe diagnostics.
- **Verify:** CLI golden và temp DB integration tests.
- **Hoàn thành khi:** không cần sửa SQLite thủ công để author/publish.

## V2-12 — Definition plane gate

- **Mục tiêu:** khóa immutability, reproducibility và passive-resource boundary.
- **Phụ thuộc:** V2-01…V2-11.
- **Thực hiện:** publish graph thực tham chiếu mọi definition kind, restart, publish V2, load V1 exact.
- **Verify:** full test/vet, Windows/Linux canonical diff, architecture import check.
- **Hoàn thành khi:** V1 snapshot không đổi và invalid executable authority bị reject.
