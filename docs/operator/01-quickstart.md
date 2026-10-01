# Quickstart

Mọi lệnh và mọi ID trên trang này đều đã thực sự được chạy trên một binary `aw` thật, vừa build xong, trong lúc
viết tài liệu (trên Windows; luồng này chạy y hệt trên Linux, chỉ cần dùng `aw` thay cho `aw.exe` và đường dẫn
kiểu POSIX). Nó kết thúc bằng một lần chạy workflow thật đạt `SUCCEEDED`, evidence thật được ghi với verdict
`PASS`, và một lệnh `doctor` thật báo `HEALTHY`. Các ID bên dưới được copy nguyên từ lần chạy thật đó — khi bạn
tự chạy sẽ sinh ra các ID khác; hãy thay bằng ID của bạn ở từng bước.

## 1. Cài đặt

Không có trình cài đặt riêng: `aw`/`aw.exe` là một binary duy nhất, tự chứa (V8-08 nhúng UI đã build vào
trong nó — xem `docs/design/10-v8-alpha-hardening.md` V8-08). Đặt nó vào `PATH`, hoặc gọi bằng đường dẫn đầy
đủ như quickstart này làm.

## 2. Khởi động bản cài

Mỗi tiến trình `aw serve`/`aw worker` cần ba thư mục thật: một file database SQLite, một thư mục gốc lưu
artifact, và một thư mục gốc workspace (nơi lưu Git worktree thật). `aw serve` tự tạo và migrate database ở
lần khởi động đầu tiên; `--artifact-root` phải tồn tại sẵn.

```bash
mkdir -p ./aw-install/artifacts ./aw-install/workspaces
aw serve --db ./aw-install/aw.db --artifact-root ./aw-install/artifacts --workspace-root ./aw-install/workspaces \
  --host 127.0.0.1 --port 18080
```

Khi đã sẵn sàng, `serve` in một dòng JSON ra stdout: `{"address":"127.0.0.1:18080"}`. Nó không bao giờ bind
vào thứ gì khác ngoài loopback (ADR-028) — không có cách nào để expose nó ra mạng.

Ở terminal thứ hai, khởi động worker với CÙNG `--db`/`--artifact-root`/`--workspace-root` — sẽ không có gì
thực sự được thực thi (probe repository, node của workflow, cập nhật projection) nếu thiếu nó:

```bash
aw worker --db ./aw-install/aw.db --artifact-root ./aw-install/artifacts --workspace-root ./aw-install/workspaces
```

Khi sẵn sàng nó in `{"workerId":"aw-worker-<pid>"}`. Xem [02-configuration.md](02-configuration.md) để biết mọi
flag khác mà cả hai tiến trình chấp nhận.

Mọi lệnh resource bên dưới (`aw <resource> <action>`) đều cần lặp lại CÙNG các flag `--db`/`--artifact-root`
`--workspace-root` (hoặc đặt một lần qua biến môi trường `AW_DB`/`AW_ARTIFACT_ROOT`/`AW_WORKSPACE_ROOT` — xem
[02-configuration.md](02-configuration.md)); chúng được lược bỏ bên dưới cho dễ đọc.

## 3. Tạo project và đăng ký repository

```bash
echo '{"name":"quickstart"}' | aw project create --idempotency-key proj-1
```

```json
{"idempotencyKey": "proj-1", "replayed": false, "result": {
  "projectId": "e3b7544b-1b6a-4189-930b-c699bc492430", "name": "quickstart", "status": "ACTIVE"
}}
```

Đăng ký một Git repository cục bộ thật, đã tồn tại sẵn (một đường dẫn filesystem tuyệt đối — không bao giờ là
URL remote; xem [06-source-control-and-releases.md](06-source-control-and-releases.md) để biết lý do):

```bash
echo '{"repositoryId":"repo-a","name":"repo-a","remoteLocator":"/absolute/path/to/your/repo","defaultRef":"main"}' \
  | aw repository register --project-id e3b7544b-1b6a-4189-930b-c699bc492430 --idempotency-key repo-1
```

Lệnh này trả về ngay lập tức với `"status": "REGISTERING"` và một `probeJobId` — tiến trình `aw worker` thật
sẽ probe repository một cách bất đồng bộ (vài giây). Poll cho tới khi trạng thái ổn định:

```bash
aw repository list e3b7544b-1b6a-4189-930b-c699bc492430
```

Một đường dẫn cục bộ đúng, tồn tại thật sẽ ổn định ở `"status": "ACTIVE"`. **Một đường dẫn sai sẽ ổn định ở
`"status": "BLOCKED"` với `"lastProbeErrorCode": "NOT_FOUND"`** — đây là lỗi quickstart phổ biến nhất (một
đường dẫn tương đối hoặc đặc thù của shell mà tiến trình worker, chạy với working directory riêng của nó,
không resolve được); hãy đăng ký một repository ID MỚI với đường dẫn tuyệt đối đã sửa thay vì cố sửa cái đang
bị blocked (không có lệnh "edit repository").

## 4. Soạn và publish một workflow tối thiểu

Một Definition được publish qua hai bước: `create` vỏ definition (một lần), rồi `publish` một version (bao
nhiêu lần tùy ý — mỗi lần publish là một version mới, bất biến). Xem
[04-authoring-workflows.md](04-authoring-workflows.md) để có schema đầy đủ của mọi loại document; mục này
publish graph THẬT nhỏ nhất có thể chạy mà không cần provider Claude/Codex nào: một node `MACHINE_GATE` duy
nhất (`START -> MACHINE_GATE -> END`) chạy một script do bạn cung cấp, cùng một completion policy yêu cầu
chính evidence của gate đó.

```bash
# Attempt policy (hành vi retry/timeout cho mọi node)
echo '{"definitionId":"attempt-policy","name":"attempt policy"}' | aw definition create --kind POLICY --idempotency-key create-attempt
echo '{"category":"ATTEMPT","attempt":{"maxAttempts":3,"backoffSeconds":1,"timeoutSeconds":60}}' \
  | aw definition publish --kind POLICY --idempotency-key pub-attempt --yes attempt-policy

# Permission policy (isolation tier — xem 05-providers-and-isolation.md)
echo '{"definitionId":"permission-policy","name":"permission policy"}' | aw definition create --kind POLICY --idempotency-key create-perm
echo '{"category":"PERMISSION","permission":{"isolationTier":"OPERATOR_TRUSTED_LOCAL"}}' \
  | aw definition publish --kind POLICY --idempotency-key pub-perm --yes permission-policy

# Completion policy (một run phải tạo ra evidence gì thì mới được tính là xong)
echo '{"definitionId":"completion-policy","name":"completion policy"}' | aw definition create --kind POLICY --idempotency-key create-comp
echo '{"category":"COMPLETION","completion":{"requiredEvidenceKinds":["QUICKSTART_OUTPUT_VERIFIED"]}}' \
  | aw definition publish --kind POLICY --idempotency-key pub-comp --yes completion-policy
```

Script của gate nằm trong một Skill document (**hệ điều hành rất quan trọng ở đây** — một `MACHINE_GATE`
chạy command của nó như một tiến trình OS thật, không có shell interpreter trừ khi bản thân script là một file
thực thi thật cho OS đó; dùng `.sh` có shebang trên Linux/macOS, dùng `.bat`/`.cmd` thật trên Windows — nhầm
lẫn chỗ này là lỗi quickstart phổ biến thứ hai, và trên Windows sẽ gây ra lỗi thật `"gate evaluator could not
be spawned ... %1 is not a valid Win32 application"` nếu bạn dùng script `.sh` ở đó):

```bash
# Linux/macOS:
echo '{"resources":[{"key":"quickstart-gate.sh","instruction":"#!/bin/sh\necho '\''{\"QUICKSTART_OUTPUT_VERIFIED\":{\"verdict\":\"PASS\"}}'\''\nexit 0\n","priority":"GUIDANCE","global":true,"selector":{},"provenance":{"owner":"quickstart","source":"docs","revision":"v1"}}]}' \
  | aw definition publish --kind SKILL --idempotency-key pub-skill --yes scripts
```

```bash
# Windows:
echo '{"resources":[{"key":"quickstart-gate.bat","instruction":"@echo off\r\necho {\"QUICKSTART_OUTPUT_VERIFIED\":{\"verdict\":\"PASS\"}}\r\nexit /b 0\r\n","priority":"GUIDANCE","global":true,"selector":{},"provenance":{"owner":"quickstart","source":"docs","revision":"v1"}}]}' \
  | aw definition publish --kind SKILL --idempotency-key pub-skill --yes scripts
```

(Chạy `aw definition create --kind SKILL` với `{"definitionId":"scripts","name":"quickstart scripts"}` trước,
giống như mọi loại khác ở trên.) Trường `id` trong response của lệnh publish là id của VERSION skill này — gọi
nó là `$SKILL_VID`. Content hash của script gate là xác định (SHA-256 của instruction/priority/global/selector
của resource, không phụ thuộc version nào sở hữu nó); hash thật của nội dung `.bat` trên Windows ở trên là
`sha256:92984c5081c73046648d0d681f3ad752bd5d71602b453d34a80610fa966b323c` — hãy tự tính hash của bạn bằng
`aw definition validate` trên một Skill document nếu nội dung script của bạn khác dù chỉ một byte.

```bash
echo '{"definitionId":"gate-command","name":"gate command"}' | aw definition create --kind COMMAND --idempotency-key create-cmd
echo '{"executable":{"ownerVersionId":"'"$SKILL_VID"'","resourceKey":"quickstart-gate.bat","contentHash":"sha256:92984c5081c73046648d0d681f3ad752bd5d71602b453d34a80610fa966b323c"},"argv":[{"kind":"LITERAL","value":"run"}],"cwdRepositoryTarget":"unused-by-machine-gate","compatibility":{"os":["windows"]},"networkAccess":"NONE","timeoutSeconds":60,"output":{"captureStdout":true,"captureStderr":true,"maxOutputBytes":65536}}' \
  | aw definition publish --kind COMMAND --idempotency-key pub-cmd --yes gate-command
```

`cwdRepositoryTarget` là trường bắt buộc trên mọi Command document, nhưng command của một `MACHINE_GATE` không
bao giờ thực sự được chạy bên trong một checkout của repository (nó luôn nhận một thư mục scratch mới) — bất kỳ
chuỗi placeholder nào cũng thỏa mãn schema. Gọi trường `id` trong response của lệnh publish là `$CMD_VID`.

```bash
echo '{"definitionId":"machine-gate","name":"machine gate"}' | aw definition create --kind GATE --idempotency-key create-gate
echo '{"commandRef":{"kind":"COMMAND","definitionId":"gate-command","versionId":"'"$CMD_VID"'"},"criteria":[{"name":"output-verified","evidenceKey":"QUICKSTART_OUTPUT_VERIFIED"}]}' \
  | aw definition publish --kind GATE --idempotency-key pub-gate --yes machine-gate
```

Gọi trường `id` trong response publish gate là `$GATE_VID`. Cuối cùng là chính workflow — document duy nhất
trong cả chuỗi này có scope là PROJECT (mọi Policy/Skill/Command/Gate ở trên đều có scope là installation và
dùng lại được cho mọi project):

```bash
echo '{"schemaVersion":"1","nodes":[{"key":"start","type":"START","outcomes":["next"]},{"key":"gate","type":"MACHINE_GATE","outcomes":["passed"],"machineGate":{"gateRef":{"kind":"GATE","definitionId":"machine-gate","versionId":"'"$GATE_VID"'"},"policyRefs":[{"kind":"POLICY","definitionId":"attempt-policy","versionId":"<attempt-policy-version-id>"},{"kind":"POLICY","definitionId":"permission-policy","versionId":"<permission-policy-version-id>"}]}},{"key":"end","type":"END"}],"edges":[{"key":"start-gate","from":"start","outcome":"next","to":"gate"},{"key":"gate-end","from":"gate","outcome":"passed","to":"end"}],"completionPolicyRef":{"kind":"POLICY","definitionId":"completion-policy","versionId":"<completion-policy-version-id>"}}' \
  | aw definition create --kind WORKFLOW --project-id e3b7544b-1b6a-4189-930b-c699bc492430 --idempotency-key create-wf \
  && aw definition publish --kind WORKFLOW --project-id e3b7544b-1b6a-4189-930b-c699bc492430 --idempotency-key pub-wf --yes quickstart-workflow
```

(Thay bằng các `versionId` thật mà mỗi lần publish policy trả về.) Trường `id` trong response là
`$WORKFLOW_VID` — lần chạy cần đúng giá trị này.

## 5. Tạo task và chạy nó

Mỗi lần chạy thật cần một WorkItem CON (một workflow không bao giờ chạy trực tiếp trên WorkItem gốc) với một
CONTRACT readiness thật — một contract rỗng sẽ thực sự fail readiness với các lỗi như `"behavior is
required"`:

```bash
echo '{"projectId":"e3b7544b-1b6a-4189-930b-c699bc492430","title":"quickstart root task","initialScope":[{"repositoryId":"repo-a","access":"READ","reason":"quickstart"}]}' \
  | aw work-item create --project-id e3b7544b-1b6a-4189-930b-c699bc492430 --idempotency-key create-root
```

```bash
echo '{"title":"quickstart child task","parentJoinPolicy":"ALL_CHILDREN_DONE","effectiveScope":[{"repositoryId":"repo-a","access":"READ","reason":"quickstart"}],"contract":{"schemaVersion":1,"behavior":"Run the quickstart machine gate and confirm it passes.","verificationSpec":"The machine gate reports QUICKSTART_OUTPUT_VERIFIED with verdict PASS.","riskLevel":"LOW","acceptanceCriteria":[{"description":"Gate passes","verificationRef":"QUICKSTART_OUTPUT_VERIFIED"}],"workflowVersionId":"<workflow-version-id>"}}' \
  | aw work-item create-child --idempotency-key create-child <root-work-item-id>
```

Xác nhận nó thực sự đã sẵn sàng trước khi đánh dấu (một bước kiểm tra trước trung thực, không bắt buộc nhưng
rẻ):

```bash
aw work-item readiness --project-id e3b7544b-1b6a-4189-930b-c699bc492430 <child-work-item-id>
# {"workItemId": "...", "status": "BACKLOG", "version": 1, "ready": true}

aw work-item mark-ready --expected-version 1 --idempotency-key mark-ready <child-work-item-id>
```

Bắt đầu run — `--wait` sẽ block cho tới khi run đạt trạng thái terminal; với một graph một-gate như thế này
thường mất chưa tới hai giây:

```bash
aw run start --workflow-version-id <workflow-version-id> --idempotency-key start-run --wait --wait-timeout 30s <child-work-item-id>
```

Response của một run thành công thật có nhúng một object `"wait"` với `"state": "SUCCEEDED"`. Lần chạy thật mà
quickstart này được kiểm chứng đã trả về đúng dạng này.

## 6. Xác nhận nó thực sự đã chạy đúng

```bash
aw evidence list --project-id e3b7544b-1b6a-4189-930b-c699bc492430 <child-work-item-id>
```

Một mục evidence của run thành công thật có `"verdict": "PASS"` và một mục `artifactReferences` — lấy output
thô của gate bằng `aw artifact get <workItemId> <evidenceId> <artifactId> --project-id <id> --output -` (xem
[03-cli-reference.md](03-cli-reference.md)).

```bash
aw doctor
```

Một bản cài khỏe mạnh báo `status: HEALTHY` và mọi check (`process_liveness`, `app_config`, `database`,
`artifact_root`, `git`, `safe_settings`, `isolation_enforcement`) cũng đều HEALTHY.

## Khôi phục sau hai lỗi thật mà walkthrough này đã gặp

Cả hai lỗi sau đều đã thực sự xảy ra và được sửa trong lúc kiểm chứng chính quickstart này — chúng là những
lỗi đầu tiên có khả năng gặp nhất, không phải giả định:

1. **Repository kẹt ở BLOCKED / `NOT_FOUND`**: đường dẫn `remoteLocator` của bạn sai (thường là đường dẫn đặc
   thù của shell hoặc đường dẫn tương đối mà tiến trình worker không resolve giống như shell của bạn). Đăng ký
   một repository ID mới với đường dẫn tuyệt đối đã sửa — xem bước 3 ở trên.
2. **Gate evaluator báo "not a valid Win32 application" (Windows) hoặc "permission denied" (Linux/macOS)**:
   script gate của bạn không khớp với OS mà tiến trình worker thực sự chạy trên đó. Publish một version skill
   MỚI với script đúng cho OS của bạn, rồi một version command mới trỏ tới nó, một version gate mới trỏ tới
   command đó, và một version workflow mới trỏ tới gate đó — Definition là bất biến theo từng version, nên sửa
   lỗi luôn có nghĩa là publish tiến lên, không bao giờ sửa tại chỗ. Xem
   [09-troubleshooting.md](09-troubleshooting.md) để biết mẫu xử lý chung.

Với mọi thứ khác, xem [09-troubleshooting.md](09-troubleshooting.md).
