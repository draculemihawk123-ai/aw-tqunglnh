# Kế hoạch Go core spike

> Trạng thái: IN PROGRESS — đang thực thi gate kiểm chứng trước khi xây alpha runtime/UI.
>
> Phạm vi: CLI-only, chạy offline mặc định, ưu tiên bằng chứng có thể tái lập.
>
> Baseline bắt buộc: [mô hình Project–Repository–WorkspaceSet](../architecture/01-project-repository-workspace-model.md)
> và [ADR-001 đến ADR-010](../architecture/02-architecture-decisions.md).
> Trạng thái/handoff session: [Start here](../00-start-here.md).

## 1. Mục đích của spike

Spike trả lời một câu hỏi duy nhất: core Go đề xuất có đủ primitive để vận hành workflow bền vững,
đa repository và độc lập provider trước khi đầu tư vào alpha runtime/UI hay không?

Năm giả thuyết phải được chứng minh bằng chương trình và test chạy thật:

1. Một `WorkflowRun` luôn dùng đúng `WorkflowVersion` bất biến đã pin, kể cả khi definition tiếp tục
   được sửa và publish.
2. Sau khi process orchestrator bị dừng đột ngột, một process mới có thể khôi phục từ SQLite mà
   không dựa vào bộ nhớ, current directory hoặc provider session còn sống.
3. Một `TaskFamily` đa repository sở hữu đúng một `WorkspaceSet`; các root family khác nhau không
   dùng chung mutable worktree, còn child trong cùng family dùng lại workspace của family.
4. Lease có fencing token thực sự ngăn writer cũ ghi state/evidence sau khi mất quyền, kể cả khi có
   nhiều worker process cạnh tranh.
5. Claude và Codex đi qua cùng `AgentExecutor` contract; khác biệt protocol chỉ nằm trong adapter và
   không tạo nhánh provider trong domain/orchestrator.

Spike chỉ được coi là đạt khi cả năm giả thuyết có evidence máy đọc được và có thể tái lập offline.

## 2. Goals

- Dựng vertical slice nhỏ nhất từ publish definition đến run, attempt, process execution, evidence
  và resume.
- Dùng SQLite file thật, Git repository/worktree thật và OS child process thật.
- Chứng minh state transition và side effect có optimistic version/fencing protection.
- Chứng minh multi-repository `RevisionSet` và diff provenance.
- Chứng minh hai adapter Claude/Codex có chung contract bằng deterministic fake CLI.
- Chạy cùng contract suite trên Windows và Linux; path, process và signal không lọt vào domain.
- Xuất evidence bundle cho từng test để review được nguyên nhân pass/fail.
- Giữ implementation đủ nhỏ để có thể bỏ đi sau khi học được điều cần học.

## 3. Non-goals

Spike không xây:

- UI, Kanban, chat UI hoặc workflow designer;
- HTTP API, authentication, organization/team/RBAC;
- PostgreSQL, object store, distributed scheduler hoặc Kubernetes;
- production sandbox, secret manager hay policy engine đầy đủ;
- bộ Layer/Skill hoàn chỉnh, scaffold framework hoặc technology convention;
- push, pull request, merge, ReleaseSet hay atomic operation xuyên repository;
- semantic merge giữa sibling cùng sửa một repository;
- full event sourcing, analytics hoặc projection UI;
- compatibility đầy đủ với mọi phiên bản Claude/Codex CLI;
- chất lượng production cho retry/backoff, telemetry hay cleanup.

Fake CLI không được dùng để tuyên bố tương thích tuyệt đối với CLI thật. Live smoke chỉ phát hiện
protocol drift sớm; nó không thay thế test offline và không quyết định tính đúng đắn của domain.

## 4. Vertical slice nhỏ nhất

Spike giữ đúng các object sau:

    Project
      -> Repository[]
      -> Root WorkItem
           -> TaskFamily
                -> WorkspaceSet
                     -> RepositoryWorkspace[]

    WorkflowDefinition file
      -> published immutable WorkflowVersion
           -> WorkflowRun
                -> NodeRun
                     -> ExecutionAttempt
                          -> JobLease
                          -> WriteLease[]
                          -> AgentSessionRef?
                          -> ContextSnapshot
                          -> Evidence[]

Chỉ cần ba loại node:

- `AGENT`: gọi `AgentExecutor` qua process adapter;
- `COMMAND`: chạy deterministic helper command cho fault/revision test;
- `TERMINAL`: kết thúc run theo outcome hợp lệ.

Graph mẫu tối thiểu là `AGENT -> COMMAND -> TERMINAL`. Publish vẫn phải reject node/edge không hợp
lệ, terminal không reachable và cycle không có iteration budget/escalation edge theo ADR-009.

State tables là runtime authority. Mỗi application transaction thay state đồng thời append audit
event và durable job/outbox tương ứng; spike không replay toàn bộ event để dựng state.

## 5. Boundary phải được thể hiện trong code spike

Domain không import package SQLite, Git, filesystem, `os/exec`, Claude hoặc Codex. Các port tối thiểu:

| Port | Trách nhiệm |
|---|---|
| `DefinitionRepository` | Publish và đọc immutable `WorkflowVersion` |
| `RunRepository` | State/version của run, node run và attempt |
| `JobRepository` | Claim, heartbeat, complete và recover durable job |
| `LeaseRepository` | Cấp lease, renew và validate fencing token |
| `WorkspaceProvider` | Provision/inspect/release worktree theo repository |
| `RevisionInspector` | Pin revision, diff và tạo `RevisionSet` |
| `AgentExecutor` | Start/resume/cancel và stream normalized event |
| `ProcessRunner` | Chạy/giám sát OS process theo execution spec |
| `Clock` | Thời gian cho lease/test; domain không gọi wall clock trực tiếp |
| `EvidenceStore` | Lưu manifest, log, hash và locator artifact |

Application service điều phối các port. SQLite/Git/process/provider là adapter. CLI chỉ gọi
application command; CLI không chứa rule domain.

`AgentExecutor` contract tối thiểu:

    Describe() -> provider, adapter_version, capabilities
    Start(ExecutionRequest) -> event stream, ExecutionOutcome
    Resume(ExecutionRequest, ProviderSessionRef) -> event stream, ExecutionOutcome
    Cancel(ExecutionHandle) -> result

`ExecutionRequest` chứa attempt ID, workspace mount/scope, `ContextSnapshot`, timeout và policy đã
resolve. Nó không chứa switch buộc orchestrator biết Claude hay Codex. Normalized event tối thiểu:
`started`, `message`, `tool_requested`, `tool_result`, `checkpoint`, `completed`, `failed`.

## 6. Layout dự kiến

Layout này là giới hạn tổ chức cho spike, chưa phải cấu trúc repository production cuối cùng:

    cmd/
      agentkit-spike/             # publish/run/resume/inspect/evidence commands
      fake-claude/                # executable mô phỏng Claude protocol
      fake-codex/                 # executable mô phỏng Codex protocol
      spike-helper/               # deterministic command/fault helper
    internal/
      domain/                     # value objects, invariant, transition
      application/                # use case/orchestration
      ports/                      # interface hướng vào adapter
      adapters/
        sqlite/                   # state, event, outbox, lease
        git/                      # repo/worktree/revision/diff
        process/                  # Windows/Linux child process
        providers/
          claude/
          codex/
        evidence/                 # local filesystem bundle
      spikefixture/               # seed project/repo/workflow/fault scenarios
    testdata/
      workflows/
      provider-events/
      repositories/
    docs/spikes/evidence/         # generated, không commit artifact chứa dữ liệu nhạy cảm

Fake provider là executable riêng được build rồi spawn. Không thay bằng Go mock gọi trực tiếp trong
process, vì như vậy không kiểm chứng boundary process/protocol và crash behavior.

## 7. CLI contract dùng để chạy spike

Tên command dưới đây là acceptance contract; implementation có thể chia package khác nhưng không
được yêu cầu thao tác DB/Git thủ công để làm test pass.

    agentkit-spike init --db <path> --evidence-dir <path>
    agentkit-spike project register --manifest <path>
    agentkit-spike workflow publish --file <path>
    agentkit-spike task create --file <path>
    agentkit-spike run start --task <id> --workflow-version <id> --provider <name>
    agentkit-spike worker start --id <id> [--fault <fault-point>]
    agentkit-spike inspect run --id <id> --json
    agentkit-spike inspect workspace --family <id> --json
    agentkit-spike evidence verify --run <id>

Test harness được phép gọi CLI và kill process bằng API phù hợp OS. Test không được sửa trực tiếp
SQLite để tạo trạng thái mong muốn, ngoại trừ fixture kiểm tra migration/corruption được khai báo
riêng.

## 8. Fixture chuẩn

Mỗi test tạo temp directory độc lập và tối thiểu hai local Git repository có commit thật:

- `user-service`, base revision `U0`;
- `web-app`, base revision `W0`.

Hai repository thuộc cùng `Project P1`. `TaskFamily F1` có WRITE scope trên cả hai; child `C-user`
chỉ WRITE `user-service`, child `C-web` chỉ WRITE `web-app`. `TaskFamily F2` dùng lại repository
`user-service` nhưng phải có worktree khác.

Workflow fixture có:

- `v1`: agent tạo marker `contract=v1`, command xác minh marker, rồi terminal;
- `v2`: agent tạo marker `contract=v2`, được publish sau khi run v1 đã bắt đầu;
- provider fixture Claude và Codex phát cùng normalized intent/outcome qua raw protocol khác nhau.

Mọi ID trong fixture được sinh/ghi qua application API. Expected revision, hash và state không được
suy từ tên thư mục.

## 9. Acceptance tests bắt buộc

### SPK-01 — Publish tạo version bất biến và hash ổn định

**Arrange:** publish cùng nội dung workflow v1 hai lần với khác biệt whitespace/key order không có
ý nghĩa; sau đó publish nội dung v2 có semantic change.

**Assert:** canonical content của v1 có cùng hash; publish có thể deduplicate hoặc trả version đã
tồn tại. V2 có version ID/hash khác. Không command nào sửa được snapshot v1 đã publish. Graph sai
hoặc terminal unreachable bị reject trước khi lưu runtime version.

**Evidence:** manifest gồm source hash, canonical hash, version ID, validation result và audit event.

### SPK-02 — Run pin version xuyên qua publish mới và restart

**Arrange:** start `R1` bằng v1 rồi dừng worker ở fault point trước node thứ hai. Sửa file authoring,
publish v2, restart worker và resume `R1`. Sau đó start `R2` bằng v2.

**Assert:** `R1.workflow_version_id/hash` vẫn là v1 và marker cuối là `contract=v1`; `R2` dùng v2.
Restart hoặc thay đổi file trên disk không đổi graph/node config của R1.

**Evidence:** hai run manifests, exact version snapshot/hash, state transition và marker artifact.

### SPK-03 — Hard crash và durable resume

**Arrange:** worker spawn fake provider process thật. Fake provider phát một checkpoint rồi giữ
process sống. Test cưỡng bức terminate worker process, không gọi graceful shutdown. Khởi động worker
mới chỉ với DB/evidence root; không truyền object/session trong memory.

**Assert:** completed node không chạy lại. Attempt bị gián đoạn được chuyển `RUNNING -> LOST` hoặc
`UNKNOWN` qua reconciliation có audit event, không bị coi là success. Retry tạo attempt ID mới theo
policy; nó dùng canonical ContextSnapshot và có thể dùng `ProviderSessionRef` nếu còn hợp lệ nhưng
không phụ thuộc ref đó. Run đi đến terminal đúng một lần.

**Evidence:** PID/process timeline, attempt IDs, checkpoint, reconciliation event, retry relation,
context manifest và terminal outcome.

### SPK-04 — Crash tại transaction boundaries

Chạy SPK-03 lần lượt tại các fault point xác định:

1. `after_process_exit_before_outcome_commit`;
2. `after_outcome_commit_before_job_ack`;
3. `after_node_complete_before_next_job_dispatch`;
4. `after_job_claim_before_process_spawn`.

**Assert chung:** restart không mất job, không tạo hai terminal transitions, không tự suy success từ
process exit đơn lẻ, và không chạy lại committed side effect như một attempt cũ. Nếu side effect
không chứng minh được, trạng thái phải là `UNKNOWN/BLOCKED` hoặc retry attempt mới theo policy, không
được âm thầm `DONE`.

**Evidence:** fault point, state/event/outbox sequence, attempt/process correlation và invariant check.

### SPK-05 — WorkspaceSet đa repository và child reuse

**Arrange:** tạo F1 scope hai repository và provision workspace. Tạo hai child scope riêng từng repo.

**Assert:** F1 có đúng một WorkspaceSet và hai RepositoryWorkspace. Cả hai child giữ `family_id=F1`
và locator/generation đúng workspace tương ứng; không tạo child worktree mới. Mỗi evidence/diff có
`repository_id`. RevisionSet ban đầu pin chính xác U0 và W0.

**Evidence:** family/workspace manifest, normalized absolute locator đã redact phần máy-specific,
base/current revision và RevisionSet.

### SPK-06 — Isolation giữa hai root family

**Arrange:** F1 và F2 cùng WRITE `user-service`; mỗi family sửa marker khác nhau nhưng chưa merge về
base repository.

**Assert:** worktree locator và branch của F1/F2 khác nhau; thay đổi của F1 không xuất hiện trong
working tree/diff của F2. Base repository không bị dirty. Identity dựa trên repository ID và family,
không dựa vào cwd/remote slug.

**Evidence:** worktree list, porcelain/diff cho ba location, branch/revision manifest.

### SPK-07 — Scope enforcement và diff violation

**Arrange:** attempt của `C-user` được WRITE `user-service`, READ `web-app`. Fake provider/helper cố
thay đổi cả hai repository.

**Assert:** adapter không cấp writable mount cho `web-app` khi OS isolation hỗ trợ. Dù process tìm
cách tạo thay đổi, post-execution diff enforcement phát hiện repository/path ngoài scope; attempt
không pass và evidence giữ violation. Không cập nhật current revision của repository vi phạm.

**Evidence:** effective scope, mount/access manifest, pre/post RevisionSet, diff manifest và outcome.

### SPK-08 — Lease exclusivity và parallelism đúng repository

**Arrange:** chạy hai worker process đồng thời, cùng claim WRITE lease của
`F1/user-service/generation=1`; đồng thời thử lease `F1/web-app/generation=1`.

**Assert:** đúng một worker giữ lease user-service; worker còn lại không được mutate. Lease web-app
có thể active song song. Kết quả phải đúng qua ít nhất 100 race iterations và không có hai active
lease hợp lệ cho cùng key tại cùng thời điểm.

**Evidence:** worker IDs, lease key/token, DB timestamps, race iteration summary và overlap timeline.

### SPK-09 — TTL, takeover và fencing token

**Arrange:** W1 lấy token T1 rồi ngừng heartbeat. Sau TTL, W2 lấy token T2. W1 thức lại và thử
checkpoint outcome/current revision bằng T1; sau đó recreate workspace để tăng generation và thử
lại token cũ.

**Assert:** T2 có fencing value lớn hơn T1. Mọi mutation với T1 bị repository/application layer từ
chối ngay cả khi W1 vẫn giữ process handle. Generation mới làm toàn bộ token generation cũ vô hiệu.
Chỉ mutation có token hiện hành mới xuất hiện trong runtime state/evidence authority.

**Evidence:** lease history, heartbeat/expiry/takeover events, rejected write result, generations và
final authoritative state.

### SPK-10 — Optimistic concurrency của runtime state

**Arrange:** hai process đọc cùng expected version của một Attempt/NodeRun rồi cùng commit transition
khác nhau.

**Assert:** đúng một compare-and-swap thành công; transaction còn lại nhận conflict và phải reload.
Không có trạng thái kép, lost update hoặc hai durable jobs cho cùng transition logic.

**Evidence:** expected/actual versions, transaction results, event/outbox rows tương quan.

### SPK-11 — Provider-neutral contract với fake Claude và fake Codex

**Arrange:** chạy cùng workflow/context/workspace hai lần, lần lượt qua fake Claude và fake Codex.
Hai executable phát raw fixture protocol khác nhau, gồm message, tool request/result, checkpoint và
terminal outcome.

**Assert:** normalized event schema và domain transition giống nhau trừ provider metadata/session
ref. Không package domain/application import adapter Claude/Codex; orchestrator lựa adapter qua
registry/capability. Unknown capability bị reject trước execution.

**Evidence:** raw event logs, normalized event logs, adapter version/capabilities, contract-test diff
và import/dependency check.

### SPK-12 — Mất provider session vẫn resume được

**Arrange:** hoàn thành một attempt có `ProviderSessionRef`, xóa/invalidate fake provider session,
crash trước node kế tiếp rồi resume.

**Assert:** runtime dựng ContextSnapshot từ platform message/resource/revision manifest và tiếp tục
qua `Start` mới hoặc fallback policy. ProviderSessionRef mất không làm mất conversation/context và
không đổi pinned RevisionSet.

**Evidence:** canonical messages, context snapshot/hash, invalid-session outcome, fallback event và
revision manifest.

### SPK-13 — Windows/Linux contract

**Arrange:** chạy toàn bộ test không-live trên Windows và Linux với cùng fixture semantics.

**Assert:** ID, canonical workflow hash, normalized state/event schema và final Git content tương
đương; chỉ locator/path/process metadata được phép khác. Domain tests không dùng OS-specific path,
signal hoặc shell syntax.

**Evidence:** platform manifests và semantic result diff đã normalize.

### SPK-14 — Evidence completeness và tamper detection

**Arrange:** verify evidence của một run thành công, sau đó sửa một artifact/log đã được manifest.

**Assert:** bundle ban đầu verify được toàn bộ hash/correlation. Bundle bị sửa phải fail verify.
Evidence không chứa access token/environment dump; raw output đi qua redaction tối thiểu.

**Evidence:** evidence manifest tự tham chiếu bằng bundle ID, verification report và negative test.

## 10. Fault injection contract

Fault injection là hook có tên, chỉ build/enable trong spike/test; không dùng timing ngẫu nhiên làm
điều kiện chính. Hook phải ghi `FaultInjected` evidence trước khi process bị cưỡng bức dừng nếu
transaction boundary cho phép.

| Fault point | Cách gây lỗi | Điều cần chứng minh |
|---|---|---|
| before process spawn | terminate worker | claimed job được recover, không có phantom process |
| after provider checkpoint | terminate worker | session chỉ là optimization; attempt được reconcile |
| after process exit, before outcome commit | terminate worker | exit code không tự biến thành committed success |
| after outcome commit, before job ack | terminate worker | durable state ngăn duplicate completion |
| after node complete, before dispatch | terminate worker | outbox/durable job nối tiếp không bị mất |
| after lease expiry, before stale write | pause/resume W1 | fencing chặn writer cũ |
| after first repo mutation | helper returns failure | multi-repo result là partial, không giả atomic rollback |
| after workspace recreate | submit old token | generation fence vô hiệu hóa cache/lease cũ |

Test crash phải terminate process thật từ parent harness. `panic`, returned error hoặc cancel context
trong cùng process chỉ là unit test bổ sung, không thay cho acceptance crash test.

## 11. Offline fake CLI và optional live smoke

### Offline path — bắt buộc

`fake-claude` và `fake-codex` là hai binary deterministic, không network và không credential. Mỗi
binary:

- đọc request qua protocol mà adapter tương ứng kỳ vọng;
- phát raw JSONL/event fixture có khác biệt provider thực tế cần normalize;
- hỗ trợ start, resume, cancel, invalid-session và configurable fault/checkpoint;
- chỉ sửa file được khai báo trong fixture;
- có adapter/fixture protocol version rõ ràng.

Toàn bộ SPK-01 đến SPK-14 phải pass khi máy không có Claude/Codex CLI và bị chặn network.

### Live smoke — tùy chọn, không phải merge gate

Live smoke chỉ chạy khi operator chủ động bật và máy đã cài/authenticate CLI:

    AGENTKIT_LIVE_PROVIDER=claude agentkit-spike live-smoke
    AGENTKIT_LIVE_PROVIDER=codex  agentkit-spike live-smoke

Smoke dùng temp repository không chứa source thật, prompt vô hại và budget/timeout thấp. Nó chỉ xác
minh adapter có thể start, nhận ít nhất một normalized message, hoàn thành/cancel sạch và redact log.
Không lưu credential, home-directory transcript hoặc toàn bộ environment vào evidence.

Live smoke fail được ghi là compatibility finding; không được sửa domain để chiều protocol provider.
Adapter/fixture phải cập nhật bằng thay đổi versioned riêng. CI/offline gate không phụ thuộc dịch vụ,
tài khoản, quota hay network bên ngoài.

## 12. Evidence bundle bắt buộc

Mỗi run/test tạo một bundle bất biến logic dưới `<evidence-dir>/<suite-run-id>/`:

    manifest.json
    environment.json
    workflow/
      source-hash.json
      published-version.json
    runtime/
      run.json
      transitions.jsonl
      attempts.json
      jobs.json
      leases.json
    workspace/
      workspace-set.json
      revisions-before.json
      revisions-after.json
      diffs/
    providers/
      raw-redacted.jsonl
      normalized.jsonl
      sessions.json
    processes/
      timeline.jsonl
      exits.json
    assertions/
      report.json
    checksums.sha256

`manifest.json` ghi schema version, suite/test ID, Git commit của spike, Go/OS/arch, DB schema
version, adapter versions, start/end time và kết quả. Mọi record liên quan execution phải correlate
được ít nhất `project_id`, `family_id`, `run_id`, `node_run_id`, `attempt_id`; record code-related
phải có `repository_id` và exact revision.

Evidence text được redact trước khi persist. Hash chứng minh integrity, không chứng minh nội dung an
toàn; vì vậy fixture không dùng secret thật. Artifact generated không mặc định commit vào Git.

## 13. Cách chạy acceptance gate

Interface dự kiến của gate:

    go test ./...
    go test -race ./...
    agentkit-spike acceptance --offline --repeat-race 100 --evidence-dir <path>
    agentkit-spike evidence verify --suite <suite-run-id>

Trên platform không hỗ trợ Go race detector cho tổ hợp kiến trúc cụ thể, `-race` được chạy trên một
CI platform hỗ trợ và ghi rõ trong evidence; không được âm thầm bỏ qua.

Gate chạy Windows trước, sau đó chạy Linux với cùng semantics. Cả hai result manifest phải được đưa
vào cùng review trước khi full gate đạt. Live smoke là job thủ công riêng.

## 14. Pass/fail gate

### PASS

Spike chỉ PASS khi:

1. SPK-01 đến SPK-14 đều pass offline trên Windows và Linux.
2. Không có flaky failure trong 10 lần chạy toàn suite liên tiếp; SPK-08 đạt ít nhất 100 race
   iterations mỗi suite run.
3. Evidence verify thành công và negative tamper test thất bại đúng dự kiến.
4. Không có domain/application dependency đến SQLite, Git, OS process, Claude hoặc Codex adapter.
5. Crash tests dùng process kill thật và restart bằng process mới.
6. Không invariant WorkspaceSet, RevisionSet, lease/fencing hay version pinning nào chỉ được chứng
   minh bằng mock trong memory.
7. Kết quả và giới hạn được ghi vào spike report, kể cả live smoke chưa chạy hoặc fail.

### FAIL

Spike FAIL và chưa được bắt đầu alpha UI/runtime nếu xảy ra một trong các trường hợp:

- run đọc workflow file hiện tại thay vì immutable published snapshot;
- restart cần in-memory state, cwd hoặc provider session;
- completed state/job bị chạy lại không có attempt/evidence mới;
- hai writer cùng có quyền hợp lệ trên một lease key;
- stale fencing token cập nhật được state, revision hoặc evidence authority;
- child tạo workspace riêng ngoài TaskFamily hoặc hai root family dùng chung mutable worktree;
- diff ngoài scope vẫn được coi là pass;
- orchestrator/domain có nhánh hành vi riêng cho Claude/Codex;
- acceptance bắt buộc cần network/credential;
- evidence không truy được từ run đến attempt, repository và exact revision.

FAIL không đồng nghĩa phải bỏ Go. Nhóm phải viết finding nêu primitive/boundary sai, thay đổi đề xuất
và acceptance test cần chạy lại. Không giảm tiêu chí chỉ để cho spike đạt.

## 15. Exit artifacts và bước kế tiếp

Spike kết thúc với đúng các artifact reviewable:

1. source code spike và reproducible commands;
2. acceptance evidence bundle Windows/Linux;
3. `spike-report.md` ghi kết quả từng SPK, fault finding và giới hạn;
4. dependency/boundary report;
5. danh sách ADR cần bổ sung hoặc supersede, nếu có;
6. đề xuất `GO`, `REWORK` hoặc `STOP` cho core Go.

Chỉ khi kết quả là `GO` mới bắt đầu alpha runtime và quyết định UI. `REWORK` quay lại đúng primitive
thất bại bằng một spike hẹp hơn. `STOP` phải nêu bằng chứng rằng kiến trúc hoặc lựa chọn Go không đạt,
không được suy từ cảm nhận về số lượng code đã viết.
