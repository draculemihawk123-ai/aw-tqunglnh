# Soạn workflow

## Vòng đời publish

Mỗi definition được `create` một lần (sinh ra một `definitionId` + `kind`, có scope installation hoặc
project), rồi `publish` bao nhiêu lần tùy ý (mỗi lần publish là một version mới, bất biến, được đánh số —
không bao giờ sửa một version đã có tại chỗ). `aw definition validate` chạy đúng các kiểm tra schema/dependency
giống hệt `publish` nhưng không thực sự publish — dùng nó để kiểm tra một document trước khi chốt một số
version.

```bash
echo '{"definitionId":"<id>","name":"<human name>"}' | aw definition create --kind <KIND> [--project-id <id>]
echo '<document JSON>' | aw definition publish --kind <KIND> [--project-id <id>] --yes <id>
```

## 9 loại definition (tập đóng)

`WORKFLOW`, `BLOCK`, `SKILL`, `LAYER`, `ENGINEERING_PACK`, `AGENT_PROFILE`, `COMMAND`, `GATE`, `POLICY`
(`internal/domain/definition/lifecycle.go`). Chỉ `WORKFLOW` là có scope project — mọi loại khác đều có scope
installation và dùng lại được cho mọi project (đã xác nhận: việc publish `POLICY`/`SKILL`/`COMMAND`/`GATE`
trong [01-quickstart.md](01-quickstart.md) chưa bao giờ truyền `--project-id`).

## POLICY — các dạng thật, đã kiểm chứng

Một Policy document có đúng một `category` và đúng MỘT bộ quy tắc tương ứng với category đó:

```json
{"category": "ATTEMPT", "attempt": {"maxAttempts": 3, "backoffSeconds": 1, "timeoutSeconds": 60}}
{"category": "PERMISSION", "permission": {"isolationTier": "OPERATOR_TRUSTED_LOCAL", "grantedCapabilities": ["INTEGRATION_MULTI_REPOSITORY_WRITE"]}}
{"category": "COMPLETION", "completion": {"requiredEvidenceKinds": ["MY_EVIDENCE_KEY"]}}
{"category": "CONTEXT", "context": {"selector": ["my-context-selector"], "budget": {"maxTokens": 4096}}}
```

Các category: `ATTEMPT`, `COMPLETION`, `PERMISSION`, `CONTEXT`, `CLEANUP` (`internal/domain/policy/policy.go`).
`isolationTier` của `PERMISSION` là `OPERATOR_TRUSTED_LOCAL` hoặc `ENFORCED_ISOLATED` — xem
[05-providers-and-isolation.md](05-providers-and-isolation.md) để biết vì sao chỉ giá trị đầu tiên dùng được
trong Alpha.

## SKILL — dạng thật, đã kiểm chứng

Một Skill document là một danh sách các resource có tên, có version (script, đoạn prompt) mà một Command hoặc
AgentProfile có thể tham chiếu theo key:

```json
{"resources": [{"key": "my-script.sh", "instruction": "#!/bin/sh\necho done\nexit 0\n",
  "priority": "GUIDANCE", "global": true, "selector": {},
  "provenance": {"owner": "you", "source": "docs", "revision": "v1"}}]}
```

Content hash của mỗi resource (cần thiết cho bất kỳ Command nào tham chiếu tới nó) là một SHA-256 xác định của
`instruction`/`priority`/`global`/`selector` — không phụ thuộc SkillVersion nào sở hữu nó, nên publish lại đúng
nội dung script đó dưới một số version mới sẽ dùng lại đúng hash cũ.

## COMMAND — dạng thật, đã kiểm chứng

```json
{"executable": {"ownerVersionId": "<skill-version-id>", "resourceKey": "my-script.sh", "contentHash": "sha256:..."},
 "argv": [{"kind": "LITERAL", "value": "run"}],
 "cwdRepositoryTarget": "<repositoryId-or-placeholder>",
 "compatibility": {"os": ["linux"]},
 "networkAccess": "NONE",
 "timeoutSeconds": 60,
 "output": {"captureStdout": true, "captureStderr": true, "maxOutputBytes": 65536}}
```

> **Ghi chú khi dịch (kiểm chứng lại 2026-10-01):** `argv` không được rỗng; và một Command khai
> `"networkAccess": "ALLOWED"` thì **chính Command document** phải có `policyRefs` trỏ tới một PERMISSION policy với
> `"grantedCapabilities": ["NETWORK_ACCESS"]`, nếu không node COMMAND fail ngay với `VALIDATION_FAILED`.
> `envAllowlist` của Command quyết định biến môi trường nào (ví dụ `PATH`, `HOME`) được truyền cho script.

`cwdRepositoryTarget` phải là một repository ID thật **đối với một COMMAND node** (được resolve theo effective
scope của chính node run đó tại thời điểm thực thi — một repository không nằm trong scope sẽ fail admission);
đối với command của một **MACHINE_GATE**, trường này bắt buộc theo schema nhưng không bao giờ thực sự được
resolve (một `MACHINE_GATE` luôn chạy command của nó trong một thư mục scratch mới, không bao giờ trong một
checkout của repository) — bất kỳ chuỗi placeholder nào cũng đúng ở đó, đã được kiểm chứng trong lần chạy thật
của [01-quickstart.md](01-quickstart.md).

## GATE — dạng thật, đã kiểm chứng

```json
{"commandRef": {"kind": "COMMAND", "definitionId": "<id>", "versionId": "<versionId>"},
 "criteria": [{"name": "my-criterion", "evidenceKey": "MY_EVIDENCE_KEY"}]}
```

Stdout của command được tham chiếu phải là JSON khớp dạng `{"<evidenceKey>": {"verdict":
"PASS"|"FAIL"|"ERROR"}}` — đã được xác nhận qua định dạng output thật của gate evaluator mà `aw artifact get`
trả về trong walkthrough của [01-quickstart.md](01-quickstart.md).

## WORKFLOW — dạng thật, đã kiểm chứng

```json
{"schemaVersion": "1",
 "nodes": [
   {"key": "start", "type": "START", "outcomes": ["next"]},
   {"key": "gate", "type": "MACHINE_GATE", "outcomes": ["passed"],
    "machineGate": {"gateRef": {"kind": "GATE", "definitionId": "...", "versionId": "..."},
                    "policyRefs": [{"kind": "POLICY", "definitionId": "...", "versionId": "..."}]}},
   {"key": "end", "type": "END"}
 ],
 "edges": [
   {"key": "start-gate", "from": "start", "outcome": "next", "to": "gate"},
   {"key": "gate-end", "from": "gate", "outcome": "passed", "to": "end"}
 ],
 "completionPolicyRef": {"kind": "POLICY", "definitionId": "...", "versionId": "..."}}
```

Các loại node (tập đóng): `START`, `END`, `AGENT`, `COMMAND`, `MACHINE_GATE`, `APPROVAL`, `WAIT`, `ROUTER`,
`FORK`, `JOIN` (`internal/domain/workflow/workflow.go`). Mọi graph cần đúng những edge chỉ rõ `outcome` nào
của node nào dẫn tới node kế tiếp nào — một outcome không có edge tương ứng là một lỗi validation thật lúc
publish, không phải một bất ngờ lúc runtime.

**`MACHINE_GATE` là loại node duy nhất dùng được nguyên trạng bất kể WorkItem có scope vào repository nào**
(nó không bao giờ resolve `cwdRepositoryTarget`, luôn chạy trong một thư mục scratch mới) — đây là lý do ví dụ
tối thiểu, không cần provider của [01-quickstart.md](01-quickstart.md) dùng nó. Một node `AGENT` cần một
provider thật đã được đăng ký (xem [05-providers-and-isolation.md](05-providers-and-isolation.md)) và một
definition `AGENT_PROFILE`; một node `COMMAND` cần repository đích thực sự nằm trong effective scope của
WorkItem đang chạy. Cả hai đều là loại node thật, hoạt động được — lượt kiểm chứng thật của chính tài liệu này
bao phủ `MACHINE_GATE` từ đầu tới cuối; các dạng node AGENT/COMMAND bên dưới được chép lại từ bộ acceptance
`internal/integration/v6accept` đang pass của repo này (một graph nhiều node thật, đã được CI chứng minh),
không được kiểm chứng lại độc lập trong lúc viết trang này:

```json
{"key": "maker", "type": "AGENT", "outcomes": ["done"],
 "agent": {"profileRef": {"kind": "AGENT_PROFILE", "definitionId": "...", "versionId": "..."},
           "policyRefs": [...], "adapterBuildId": "<adapter-build-id>", "role": "MAKER"}}
```

```json
{"key": "test_a", "type": "COMMAND", "outcomes": ["passed"],
 "command": {"commandRef": {"kind": "COMMAND", "definitionId": "...", "versionId": "..."}, "policyRefs": [...]}}
```

## AGENT_PROFILE — dạng thật (từ cùng fixture đã được chứng minh)

```json
{"providerKey": "claude", "model": "your-model-name", "toolRefs": ["read_file"],
 "contextPolicyRef": {"kind": "POLICY", "definitionId": "...", "versionId": "..."},
 "compatibility": {"os": ["linux"]}, "budget": {"maxTokens": 4096}}
```

## BLOCK, LAYER, ENGINEERING_PACK

Các loại này ghép các definition đã publish thành những đơn vị soạn thảo dùng lại được (một Block gom các
node/policy mà một workflow có thể kéo vào nguyên khối; một Layer/EngineeringPack gom các skill/policy cho cả
một team hoặc quy ước của một repository). Chưa loại nào được lượt kiểm chứng thật của tài liệu này chạy qua —
hãy tham khảo `docs/design/04-v2-definition-plane.md` và `internal/domain/{block,layer,engineeringpack}` để có
schema chính xác trước khi soạn, và coi mục này là một con trỏ, không phải tham chiếu đã kiểm chứng, cho tới khi
một lượt kiểm chứng sau chạy chúng từ đầu tới cuối.

> **Ghi chú khi dịch (2026-10-01):** LAYER, ENGINEERING_PACK, CONTEXT policy có `resourceRefs`, AGENT_PROFILE, node
> AGENT/COMMAND/APPROVAL và vòng lặp có `cyclePolicy` đã được chạy từ đầu tới cuối trong
> [hướng dẫn todolist Spring Boot + SQLite + React](../guides/todolist-spring-react/README.md), kèm file JSON và
> script publish dùng lại được. Mục [Cách định nghĩa workflow cho project bất kỳ](../guides/todolist-spring-react/README.md#3-cách-định-nghĩa-workflow-cho-project-bất-kỳ)
> có bảng chọn loại node, quy tắc graph và trạng thái kiểm chứng của từng loại node. Tóm tắt dạng document:
>
> ```json
> {"resources": [{"key": "spring.rest-api", "convention": "...", "priority": "HARD_CONSTRAINT", "global": true,
>   "selector": {}, "provenance": {"owner": "team", "source": "docs", "revision": "v1"}}]}
> {"dependencies": [{"kind": "LAYER", "definitionId": "...", "versionId": "..."}, {"kind": "SKILL", "definitionId": "...", "versionId": "..."}]}
> {"category": "CONTEXT", "context": {"selector": ["..."], "budget": {"maxTokens": 65536},
>   "resourceRefs": [{"ownerVersionId": "<layer/skill version>", "resourceKey": "...", "contentHash": "sha256:..."}]}}
> ```
>
> (lần lượt: LAYER — SKILL giống hệt nhưng dùng `instruction` thay cho `convention`; ENGINEERING_PACK; CONTEXT policy.)
> Runtime Alpha chọn resource cho agent **chỉ** từ `resourceRefs` của CONTEXT policy mà agent profile trỏ tới;
> pack-assignment (`aw pack-assignment assign`) được lưu và hiển thị nhưng không ảnh hưởng prompt.

## `aw definition list` / `show` / `versions` / `version show` / `version diff`

Các query chỉ-đọc trên mọi thứ đã publish cho tới nay:

```bash
aw definition list [--project-id <id>]                  # mọi definition, mọi loại
aw definition show <definitionId>                        # metadata của một definition
aw definition versions <definitionId>                     # mọi version đã publish, mới nhất trước
aw definition version show <definitionId> <versionNumber> # document đã compile đầy đủ của một version
aw definition version diff <definitionId> <v1> <v2>        # diff theo từng trường giữa hai version
```

> **Ghi chú khi dịch (kiểm chứng lại 2026-10-01):** với binary hiện tại, `definition show` và `definition versions`
> bắt buộc `--kind` (`aw definition versions --kind WORKFLOW --project-id <id> <definitionId>`). Không có action
> `definition version`; lệnh tương ứng là `aw version show <versionId>` / `aw version diff <versionIdA> <versionIdB>`
> nhưng từ `version` bị lệnh tiến trình `aw version` chiếm trước nên luôn báo `version takes no arguments`. Để so sánh
> hai version, dùng HTTP của `aw serve`:
> `curl -s "http://127.0.0.1:<port>/projects/<projectId>/definitions/versions/diff?a=<versionIdA>&b=<versionIdB>"`
> (bỏ `/projects/<projectId>` với definition scope installation), hoặc trang Definitions trên UI.
