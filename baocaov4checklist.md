# V4 — Báo cáo triển khai liên tục

> File làm việc cá nhân, không commit vào git (giống `baocaov0/1/2/3checklist.md`).
> Cập nhật sau mỗi task.
> Quy ước: khi vướng quyết định không tường minh trong task spec, tôi chọn theo khuyến nghị tốt nhất
> của tôi, ghi rõ lựa chọn + lý do ở đây, rồi tiếp tục — không dừng lại hỏi trừ khi thực sự chặn cứng.
> Kế thừa toàn bộ ràng buộc môi trường đã xác nhận ở V1/V2/V3 (Go toolchain local tại
> `.tools/go1.27.0/go/bin/go.exe`, verify local trước khi push, CI thật là gate cuối, repo hiện tại là
> `draculemihawk123-ai/agent-workflow`).

## Trạng thái tổng quan

| Task | Trạng thái | PR | CI | Ghi chú |
|---|---|---|---|---|
| V4-01 | DONE | [#7](https://github.com/draculemihawk123-ai/agent-workflow/pull/7) | ✅ 6/6 ngay lần đầu | Tự làm trực tiếp (không giao subagent — user yêu cầu tuần tự, không song song, cho cả phase V4). Verify local đầy đủ: build/vet/test toàn repo, docs-coverage-check (debt=0), gofmt sạch trên mọi file đổi/mới. Merge fast-forward vào master (2c649cc). |
| V4-02 | DONE | [#8](https://github.com/draculemihawk123-ai/agent-workflow/pull/8) | ✅ 6/6 ngay lần đầu | Kế thừa/hoàn thiện WIP có sẵn từ phiên trước bị ngắt (xem chi tiết mục riêng bên dưới). Squash-merge vào master (3659da5). |
| V4-03 | DONE | [#9](https://github.com/draculemihawk123-ai/agent-workflow/pull/9) | ✅ 6/6 (1 job flaky lần đầu, xanh khi rerun riêng job đó — xem chi tiết bên dưới) | Squash-merge vào master (2b5acf9). |
| V4-03 correction | DONE | [#10](https://github.com/draculemihawk123-ai/agent-workflow/pull/10) | ✅ 6/6 ngay lần đầu | User review V4-03 sau merge, phát hiện 4 gap P1. Squash-merge vào master (836703a). |
| V4-03 correction round 2 | DONE | [#11](https://github.com/draculemihawk123-ai/agent-workflow/pull/11) | ✅ 6/6 ngay lần đầu | User review lại PR #10 sau merge, phát hiện thêm 5 P1 + 5 P2. Squash-merge vào master (ab40c90). |
| Hotfix: TestProjectWorkspaceGate flake | DONE | [#13](https://github.com/draculemihawk123-ai/agent-workflow/pull/13) | ✅ 6/6 sau khi fix | Chặn PR #12 — không phải regression V4-04, đã confirm lỗi có sẵn trên `master`. Squash-merge vào master (bf070ad). Chi tiết bên dưới. |
| V4-04 | DONE | [#12](https://github.com/draculemihawk123-ai/agent-workflow/pull/12) | ✅ 6/6 sau khi rebase lên PR #13 | Go toolchain bị mất khỏi máy giữa phiên (môi trường reset) — phải `winget install GoLang.Go` lại (user duyệt trực tiếp). CI lần đầu fail 3/3 lần rerun vì flake pre-existing (`TestProjectWorkspaceGate`, không liên quan code V4-04) — tách PR #13 sửa flake trước, merge, rebase PR #12 lên, CI xanh 6/6. Squash-merge vào master (4789fa3). Chi tiết bên dưới. |
| V4-05 | DONE | [#14](https://github.com/draculemihawk123-ai/agent-workflow/pull/14) | ✅ 6/6 ngay lần đầu | Hỏi user 3 câu thiết kế lớn trước khi code (fenced-finalize pattern, cancel/lease-loss scope, outcome routing) — user trả lời rất chi tiết, có sửa cả hướng tôi đề xuất. Squash-merge vào master (a557645). Chi tiết bên dưới. |
| V4-06 | DONE | [#15](https://github.com/draculemihawk123-ai/agent-workflow/pull/15) | ✅ 6/6 sau 1 lần rerun (job Windows flake không liên quan, xem chi tiết bên dưới) | Hỏi user 2 câu thiết kế lớn trước khi code (exhaustion outcome, kiến trúc error-code) — user trả lời rất chi tiết kèm 1 chỉnh lý (§18 có 22 mã, không phải 20). Squash-merge vào master (68f0ca7). Chi tiết bên dưới. |
| V4-07 | DONE | [#16](https://github.com/draculemihawk123-ai/agent-workflow/pull/16) | ✅ 6/6 ngay lần đầu | Hỏi user 2 câu thiết kế lớn trước khi code (cơ chế forced-escalation, phạm vi đếm Iteration) — user trả lời rất chi tiết kèm semantics khoá thêm hẳn tôi chưa nghĩ tới (đặc biệt: check áp dụng lên downstreamNode chứ không phải node vừa hoàn thành; Iteration không được là COUNT(*) vì V4-12A tương lai). Squash-merge vào master (79de402). Chi tiết bên dưới. |
| V4-08 | DONE | [#17](https://github.com/draculemihawk123-ai/agent-workflow/pull/17) | ✅ 6/6 sau 1 lần rerun (job Windows flake không liên quan diff — stress test SPK-09 pre-existing từ V2-08, xem chi tiết bên dưới) | Hỏi user 2 câu thiết kế lớn trước khi code (outcome khi timeout, signal identity) — user trả lời rất chi tiết. Tự phát hiện thêm khi code: cần state ELAPSED thứ 4 ngoài dự kiến ban đầu (ACTIVE/CONSUMED/TIMED_OUT/CANCELLED không đủ cho DURATION). Squash-merge vào master (d7059d6). Chi tiết bên dưới. |
| V4-09 | DONE | [#18](https://github.com/draculemihawk123-ai/agent-workflow/pull/18) | ✅ 6/6 ngay lần đầu | Hỏi user 1 câu thiết kế lớn trước khi code (cơ chế check actor role, vì codebase chưa có role/permission system nào). Tự siết thêm 1 validation cũ (EscalationOutcome từ optional thành required) — cùng loại phát hiện như V4-08's own CompletionOutcome/TimeoutOutcome. Squash-merge vào master (53beef1). Chi tiết bên dưới. |
| V4-10 | DONE | [#19](https://github.com/draculemihawk123-ai/agent-workflow/pull/19) | ✅ 6/6 sau 1 lần rerun (race detector flake pre-existing từ V3-06/V1-10, không liên quan diff — xem chi tiết bên dưới) | Hỏi user 2 câu thiết kế lớn trước khi code (branch identity tracking, "static write-scope admission" nghĩa là gì trong Alpha). User tự phát hiện một lỗi schema V4-01 (unique constraint không sống sót qua V4-07 cycle reactivation) ngay trong lúc trả lời câu 1. Squash-merge vào master (c44bffe). Chi tiết bên dưới. |
| V4-11 | DONE | [#20](https://github.com/draculemihawk123-ai/agent-workflow/pull/20) | ✅ 6/6 ngay lần đầu | Hỏi user 2 câu thiết kế lớn trước khi code (verdict/idempotency/short-circuit, cancel-remaining-branches). User sửa sâu cả hai: siết công thức verdict chính xác hơn đề xuất gốc, và BÁC BỎ hẳn hướng cancel-remaining-sớm tôi đề xuất (Alpha không được tự cancel branch còn ACTIVE dù threshold đã đạt sớm). Tự phát hiện 1 gap khi code: `dispatchForkBranches`'s own zero-hop JOIN case (V4-10) cũng phải trigger evaluation. Squash-merge vào master (09a74c2). Chi tiết bên dưới. |
| V4-12 | DONE | [#21](https://github.com/draculemihawk123-ai/agent-workflow/pull/21) | ✅ 6/6 ngay lần đầu | Hỏi user 2 câu thiết kế lớn trước khi code (aggregation thật hay chỉ END dispatch; điều kiện "không thể tiến thêm"). User xác nhận aggregation thật, sửa sâu thành một reducer chung `reconcileRunTerminalityTx` gọi từ nhiều call site. Tự phát hiện + sửa 1 BUG THẬT của V4-11 đã merge khi viết test (thứ tự `Outcomes` bị compiler sort, JOIN có thể quyết sai token set chưa đầy đủ). Chi tiết bên dưới. |
| Hotfix: SPK-13/SPK-10 cross-platform nondeterministic winner | DONE | [#23](https://github.com/draculemihawk123-ai/agent-workflow/pull/23) | ✅ 6/6 sau 1 lần rerun (job Linux race/stability flake không liên quan diff — `TestPool_TwoPoolsRaceRecovery_NoDuplicateProcessing` context-canceled ở run 4/10, package `workerpool` không hề đụng) | Chặn PR #22 — SPK-13 fail lặp lại 3 lần (kể cả evidence hoàn toàn mới), không phải flake mà lỗi thiết kế: SPK-10's own race (RUNNING/CANCELLED đều là thắng hợp lệ) ghi thẳng state cụ thể vào evidence SPK-13 so hash byte-exact. User chốt hướng sửa: giữ race nondeterministic thật, giữ SemanticDiff strict (không allow-list), chuẩn hóa SPK-10's own evidence thành invariant ổn định (`winners/stale/finalVersion/finalStateAllowed`, bỏ literal state). Squash-merge vào master (2556671). Chi tiết bên dưới. |
| V4-12A | DONE | [#22](https://github.com/draculemihawk123-ai/agent-workflow/pull/22) | ✅ 6/6 ngay lần đầu sau rebase lên hotfix #23 | Hỏi user 2 câu thiết kế lớn trước khi code (cơ chế raise request thật + giữ liên kết; cơ chế biết đã approved+provision xong). User sửa sâu cả hai: RequestID phải RESERVE trong transaction finalize BLOCKED (không backfill — tránh crash window mất liên kết); reconcile job chỉ enqueue SAU KHI approved (không polling từ lúc PENDING), và phải verify `BaseRevisionSet` phủ đủ grant thay vì tin `State=READY` một mình. CI vòng đầu chặn bởi lỗi gate SPK-13 không liên quan (xem hotfix ở trên) — tách hotfix riêng, merge, rebase lên master mới, CI xanh 6/6 ngay lần đầu. Squash-merge vào master (6728110). Chi tiết bên dưới. |
| V4-12B | DONE | [#24](https://github.com/draculemihawk123-ai/agent-workflow/pull/24) | ✅ 6/6 ngay lần đầu | Hỏi user 2 câu thiết kế lớn trước khi code (coordinator job làm gì/Run đóng CANCELLED bằng đường nào; CONTROL allow-list xử lý sao khi doc lệch code thật). Câu 1's own vòng trả lời thứ hai bị lỗi công cụ AskUserQuestion lặp lại y hệt nội dung câu hỏi SPK-13 hai lần liên tiếp — không hỏi lại lần ba, tự chọn tiến với phương án Recommended (một job CONTROL sweep một lần), báo minh bạch. Câu 2 user trả lời rõ, dùng đúng 3 hằng số thật, loại RECOVERY_REAPER. Tự phát hiện 3 vấn đề khi code: migration count off-by-one (thiếu file 0013 lịch sử, đúng phải là 22 không phải 23), RUNNING structural node (START/FORK/END) bị bỏ sót khỏi sweep gây mắc kẹt vĩnh viễn, và một stale-read-trong-vòng-lặp gây ErrOptimisticConflict giả khi JOIN tự quyết giữa chừng sweep. 30 test mới, tất cả xanh. Squash-merge vào master (ef19dbf). Chi tiết bên dưới. |
| V4-12C | DONE | [#25](https://github.com/draculemihawk123-ai/agent-workflow/pull/25) | ✅ 6/6 ngay lần đầu | Hỏi user 2 câu thiết kế lớn trước khi code (V4-12B's own Run-close có nên mở rộng để tạo blocker RUN_CANCELLED không; SCOPE_EXPANSION_REQUIRED có cần wiring thật không). User sửa sâu cả hai: bắt phải mở rộng `transitionRunToCancelledTx` để tạo blocker + BLOCKED WorkItem trong cùng transaction (đổi cả điều kiện đóng Run của V4-12B từ `LiveCount==0 && BlockedCount==0` xuống chỉ `LiveCount==0`, vì không gì trong protocol từng resume một activation BLOCKED); xác nhận wiring thật cho SCOPE_EXPANSION_REQUIRED (dùng producer có sẵn từ V4-12A), đổi Phụ thuộc thành V4-12A+V4-12B, khoá rõ authority matrix (WAIVED luôn bị từ chối cho admission/scope-expansion; RESOLVED sinh cho scope-expansion cũng bị từ chối — chỉ approval/reconcile flow được resolve loại đó). Tự phát hiện 1 bug thật khi viết test: `ResolveWorkItemBlocker`'s own `WorkItemUnblocked` tính sai bằng `status != BLOCKED` (dương tính giả khi WorkItem đã CANCELLED vì lý do khác) — sửa bằng cách trả tín hiệu unblocked thật từ `closeWorkItemBlockerTx` thay vì suy lại từ status cuối. 27 test mới, tất cả xanh. Squash-merge vào master (`f1426ae`). Chi tiết bên dưới. |
| V4-13 | DONE | [#26](https://github.com/draculemihawk123-ai/agent-workflow/pull/26) | ✅ 6/6 sau 1 lần rerun (job `contract (ubuntu-latest)` fail ở `TestPool_TwoPoolsRaceRecovery_NoDuplicateProcessing`, `internal/app/workerpool` — package hotfix này không hề đụng, đúng flake pre-existing đã ghi nhận ở hotfix PR #23; `git diff` xác nhận package không nằm trong diff, rerun `--failed` một lần → 6/6 xanh) | Hỏi user 3 câu thiết kế lớn trước khi code (nâng đời reason vocabulary; cơ chế migration cho RECOVERY_REAPER schema; phạm vi FRESH_START) + 1 câu kiến trúc phát sinh giữa chừng khi viết test sqlite-backed (`durable_jobs.project_id` NOT NULL nhưng RECOVERY_REAPER là job toàn cục). User sửa sâu cả bốn. Tự phát hiện 1 bug thật khi viết test: helper claim-tìm-kind vô tình "cứu" một job EXECUTE_NODE khác đang cần orphaned. Squash-merge vào master (`736a833`). Chi tiết bên dưới. |
| V4-14 | DONE | [#27](https://github.com/draculemihawk123-ai/agent-workflow/pull/27) | ✅ 6/6 sau 2 lần push + 1 lần rerun (xem chi tiết) | Task cuối chuỗi V4, quy mô lớn nhất: 1 file test tích hợp ~1575 dòng, real `workerpool.Pool` lần đầu chạy `EXECUTE_NODE`/`ADVANCE_RUN`/`SCHEDULE_NODE_RUN` trong `internal/integration`. Hỏi user 2 vòng thiết kế lớn trước khi code (thiết kế golden hybrid; cấu trúc fixture hai Run). Tự phát hiện VÀ SỬA 3 bug thật trong code production đã merge từ V4-12/V4-12A (chưa test nào trước đây cho một Run scope-expansion-reactivate đi tiếp thật tới END): DecisionArtifact ID đụng UNIQUE constraint vĩnh viễn, `ReactivationReason` không bao giờ được ghi/đọc ở tầng sqlite, và `BlockedCount` đếm cả NodeRun BLOCKED đã lịch sử khiến không Run nào từng qua scope-expansion đạt được VERIFYING. CI vòng đầu lộ thêm 2 nguồn nondeterminism thật trong chính golden-trace mechanism (chỉ hiện trên CI, không tái hiện được local dù rerun nhiều lần) — sửa cả hai, verify lại bằng nhiều lần regenerate độc lập trước khi push lại. **Đóng toàn bộ pha V4 (V4-01…V4-14) — squash-merge vào master (`1cac228`).** Chi tiết bên dưới. |

**🎉 Pha V4 "Durable workflow runtime engine" DONE — cả 14 task (V4-01…V4-14) đã merge vào master, cộng
2 hotfix xen giữa (flake SPK-13/SPK-10, `TestProjectWorkspaceGate` flake). Commit merge cuối: `1cac228`.**

## V4-14 — Runtime engine acceptance gate

**Bối cảnh:** tiếp tục thẳng sau khi V4-13 merge (`736a833`), theo đúng lệnh "tự động sang task tiếp
theo". Đọc task text ("chạy graph chứa tất cả node types bằng fake executors qua restart") — task cuối
cùng đóng cả pha V4, vai trò tương tự V3-12's own closing gate cho pha V3. Nghiên cứu hạ tầng có sẵn:
xác nhận qua grep KHÔNG có test nào trong repo trước đây từng wiring một `workerpool.Pool` thật chạy
`EXECUTE_NODE`/`ADVANCE_RUN`/`SCHEDULE_NODE_RUN` (mọi test `internal/app/runtime` từ trước tới giờ chỉ
gọi handler trực tiếp, không qua Pool thật) — `internal/integration/workplane_test.go` (V3-12) là tiền
lệ gần nhất (Pool thật, nhưng chỉ cho `WORKSPACE_PROVISION`). `internal/adapters/sqlite/crashworker.go`
(subprocess crash thật, SPK-04) được xác nhận là quá nặng cho gate này — V4-13's own `StartupRecoveryScan`
làm cho một kill/restart trong-process (mirror V1-12/V2-12/V3-12) đủ dùng.

**User chốt hướng (2 câu trước khi code, cả hai sửa sâu):**

- **Câu 1 (thiết kế golden):** Verify line "deterministic event/activation golden" +
  "Hoàn thành khi: same input/decisions → same domain result" có thể đọc theo hai hướng khác nhau thật
  (self-comparison thuần, hay checked-in golden thuần). User bác bỏ cả hai, chốt **hybrid**:
  self-comparison cho `FinalDomainResult` (business outcome ngữ nghĩa — trạng thái cuối, outcome theo
  NodeKey+Iteration, shared state, manifest revision, FORK/JOIN verdict, blocker mở — KHÔNG Attempt
  count/recovery event, vì crash hợp lệ tạo thêm Attempt) giữa bản sạch và bản crash+restart của CÙNG
  fixture; checked-in golden byte-exact riêng cho `OperationalTrace` của TỪNG kịch bản (không so hai
  bản với nhau — bản crash có event LOST/retry/recovery-decision thật bản sạch không có). Canonicalize
  chỉ theo allow-list đóng (alias ổn định cho ID sinh ra, bỏ timestamp), không bao giờ xóa event/sort
  lại journal. Golden chỉ regenerate bằng lệnh tường minh, CI không tự cập nhật.
- **Câu 2 (cấu trúc fixture cancel vs completion):** "một cancel giữa chừng" cùng chỗ với "terminal
  candidate" trong task text — nhưng CANCELLED và VERIFYING loại trừ nhau trong cùng lineage. User chốt
  **hai Run riêng trên hai WorkItem/TaskFamily khác nhau** (không cùng WorkItem), cùng chung Project/
  store/Pool/một lần restart. Run A đi hết golden path (retry, rework, WAIT, APPROVAL, FORK/JOIN,
  scope-expansion BLOCKED→reactivation) tới VERIFYING thật — không gọi COMPLETED, completion authority
  thuộc V5-11. Run B dừng ở một barrier tất định (wait_node WAITING thật, không sleep chọn thời điểm)
  rồi bị CancelRun thật, xác nhận: job RUN_WORK bị fence, CONTROL coordinator vẫn claim được, WorkItem B
  → BLOCKED với đúng một blocker RUN_CANCELLED, Run A hoàn toàn không bị ảnh hưởng.

**Phạm vi thực hiện:**
- **`internal/integration/runtimeengine_test.go`** (mới, ~1400 dòng): một `WorkflowDocument`
  (`runtimeEngineDocument`) phủ đủ cả mười `workflow.NodeType` (START/ROUTER/AGENT/WAIT/APPROVAL/FORK/
  COMMAND/MACHINE_GATE/JOIN/END): `start→gate(ROUTER)→implement(AGENT, rework một vòng)→wait_node(SIGNAL)
  →approval_node→fork_node→{test_a(COMMAND), gate_b(MACHINE_GATE)}→join_node(ALL)→scope_node(AGENT,
  BLOCKED lần đầu, reactivate xong thì done)→end`. `scriptedNodeExecutor` script outcome theo NodeKey/
  Iteration/ReactivationReason đọc THẬT từ NodeRun (không đoán) — không dùng `fake.NodeExecutor` có sẵn
  vì nó chỉ trả một kết quả cố định, không đủ cho graph nhiều node khác nhau. `registerRuntimeEngineHandlers`
  wiring 9 handler thật vào một `workerpool.Registry`: `Scheduler`, `NodeSchedulingHandler`,
  `ExecuteNodeHandler`, `WaitTimeoutHandler`, `ApprovalTimeoutHandler`, `RequestScopeExpansionHandler`,
  `ScopeExpansionReconcileHandler`, `workspaceprovision.Handler` (scripted stub provider, không real Git
  — phạm vi V4 là node/job orchestration, không phải provisioning mà V3-12 đã gate xong), và V4-13's own
  `RecoveryReaperHandler` (chính `*sqlite.Store` thỏa cả ba interface spike-era structurally).
- **Inject crash:** `scriptedNodeExecutor.blockNodeKey` — khi khớp NodeKey, `Execute` chặn tại
  `<-ctx.Done()` thay vì trả kết quả, mô phỏng worker chết giữa chừng thật (không phải giả lập). Test
  đợi Attempt của "test_a" đạt RUNNING thật, `cancelPool()` khiến context lan vào derived-deadline
  context của `ExecuteNodeHandler`, `Execute` trả về `ctx.Err()` vì lý do KHÁC deadline riêng — handler
  để Attempt RUNNING, không finalize (nhánh đã có sẵn từ V4-05) — một orphaned attempt thật. Đợi lease
  hết hạn thật (2.5s > LeaseTTL 2s), `store.Close()`/`sqlite.Open()` lại (restart thật), gọi
  `runtime.StartupRecoveryScan`, dựng Pool thứ hai với executor mới (không còn block) — V4-13's own
  `RecoveryReaperHandler` tự tìm và retry attempt orphaned, graph tiếp tục bình thường.
- **`internal/adapters/sqlite/event_queries.go`** (mới): `ListDomainEventsForProject` — query test-only
  đầu tiên cần toàn bộ causal event log của một Project (không chỉ slice theo aggregate), cho golden
  trace.
- **`internal/adapters/sqlite/runtime_engine_queries.go`** (mới): `GetWaitRegistrationByNodeRunID`/
  `GetApprovalRequestByNodeRunID` — một Run chạy qua job dispatch thật (không gọi `AdvanceRun` trực tiếp)
  không bao giờ thấy được `hop.NextWaitRegistrationID`/`NextApprovalRequestID` (giá trị trả về in-memory
  của `AdvanceRun`) — cần tra lại từ durable state qua `UNIQUE(node_run_id)` đã có sẵn trên cả hai bảng.
- **Golden trace (`canonicalizeRuntimeEngineTrace`):** alias mọi `(AggregateType, AggregateID)` distinct
  thành `<type>-N` theo thứ tự xuất hiện thật trong journal, cộng một alias riêng `uuid-N` cho UUID rời
  rạc không phải aggregate (như `jobId`, vì durable_jobs không phải domain_events aggregate). Regenerate
  qua biến môi trường `AGENTKIT_REGENERATE_RUNTIME_ENGINE_GOLDEN=1`, không bao giờ tự động.
- **Race test (`TestRuntimeEngineGate_ConcurrentPoolsNoDuplicateExecution`):** hai `workerpool.Pool` thật
  đua claim cùng một EXECUTE_NODE job thật cho "implement", qua `ExecuteNodeHandler`/
  `FinalizeExecutionAttempt` thật (không phải handler tổng hợp như
  `TestPool_TwoPoolsRaceRecovery_NoDuplicateProcessing`) — đúng một `Execute` call, đúng một Attempt
  SUCCEEDED.

**Tự phát hiện VÀ SỬA 3 bug thật trong code production đã merge (V4-12/V4-12A), khi viết test end-to-end
đầu tiên từng cho một Run scope-expansion-reactivate đi tiếp thật tới END:**

1. **DecisionArtifact ID đụng UNIQUE constraint vĩnh viễn:** `reactivateBlockedNodeRunTx`
   (`internal/app/runtime/scope_expansion.go`, V4-12A) copy `DecisionArtifact` cũ sang ID tất định
   `<reactivatedID>-execution-profile-v1` "cho `ExecuteNodeHandler`'s own `loadExecutionProfile`" — nhưng
   NGAY SAU ĐÓ vẫn enqueue `SCHEDULE_NODE_RUN` thật, và `ScheduleExecutableNodeRun` tự ghi lại ĐÚNG ID đó
   lần nữa khi job chạy → `ErrPersistenceAlreadyExists` mỗi lần, retry vô hạn, NodeRun reactivate kẹt
   PENDING vĩnh viễn. Debug bằng cách chèn tạm `fmt.Println` dọc theo `ScheduleExecutableNodeRun` (revert
   sau khi xác định nguyên nhân qua `git diff`/`git checkout`). Sửa: bỏ hẳn bước copy — job thật luôn là
   writer duy nhất của artifact này, không cần pre-fill.
2. **`ReactivationReason` không bao giờ được persist:** cột `reactivation_reason` (migration 0022,
   V4-12A) tồn tại trong schema và domain struct (`NodeRun.ReactivationReason`) nhưng
   `createNodeRunTx`/`loadNodeRunByID` (`internal/adapters/sqlite`) thiếu cột này trong CẢ INSERT lẫn
   SELECT — mọi NodeRun reactivate đọc lại `ReactivationReason=""`, không phân biệt được "lần đầu
   BLOCKED" với "sau khi reactivate". Sửa: thêm cột vào cả hai câu lệnh.
3. **`BlockedCount` đếm cả NodeRun lịch sử:** `reconcileRunTerminalityTx`'s own
   `computeRunNodeStateSummary` (`internal/app/runtime/completion.go`, V4-12) đếm CẢ NodeRun BLOCKED đã
   được reactivate xong vào `BlockedCount` — `case summary.BlockedCount > 0: return nil` khiến MỌI Run
   từng qua scope-expansion không bao giờ đạt VERIFYING dù graph đã thật sự tới END, vì `BlockedCount`
   không bao giờ về 0. **User chốt hướng sửa chính xác** (bác bỏ đề xuất ban đầu chỉ so `NodeKey`): nhận
   diện "superseded" bằng lineage `(NodeKey, BranchTokenID)` — hai nhánh FORK có thể dispatch cùng
   NodeKey độc lập, một nhánh SUCCEEDED không được che blocker của nhánh kia. Trong mỗi lineage, chỉ
   activation có `ActivationSequence` lớn nhất mới được phân loại — không giới hạn nó phải SUCCEEDED/
   FAILED (activation mới hơn vẫn BLOCKED thì vẫn chặn đúng; vẫn live thì Run vẫn chạy tiếp; FAILED thì
   Run vẫn aggregate FAILED đúng).

**Một khoảng trống thiết kế thật khác phát hiện khi mới bắt đầu wiring Pool (trước khi tới 3 bug ở
trên):** `workerpool.Pool` claim job kind-agnostic ở tầng DB, chỉ tra registered handler SAU khi đã
claim (`"no handler registered for kind %q"` lỗi RA SAU claim, không NGĂN claim) — nghĩa là bất kỳ pool
nào chạy song song với việc TẠO một job loại nó không có handler vẫn có nguy cơ claim-rồi-lỗi job đó.
Ảnh hưởng hai nơi: (a) race test's own setup phase ban đầu định dùng một "setup pool" (chỉ đăng ký
`Scheduler`/`NodeSchedulingHandler`, cố tình bỏ `ExecuteNodeHandler`) rồi dừng khi thấy NodeRun QUEUED —
nhưng có cửa sổ race thật giữa lúc job EXECUTE_NODE xuất hiện và lúc test kịp gọi `cancelSetup()`. Sửa:
driving Run A/B's own setup hoàn toàn qua gọi trực tiếp `runtime.AdvanceRun`/`runtime.ScheduleExecutableNodeRun`
(không qua job/Pool nào), chỉ khởi Pool đúng lúc cần race thật. (b) Cùng lý do, thứ tự dựng Run B trong
kịch bản chính PHẢI xong hoàn toàn TRƯỚC khi Run A's own approval resolve (bước kích hoạt fork dispatch)
— nếu không, với Concurrency:1 và test_a đang bị block (crash injection), worker duy nhất kẹt vĩnh viễn
trước khi kịp xử lý job của Run B.

**Verify (chạy local — Go toolchain `C:\Program Files\Go\bin`, PATH export thủ công mỗi lần gọi):**
```
go build ./...                     # sạch
go vet ./...                       # sạch
go test ./... -count=1             # toàn bộ package PASS (kể cả spikeacceptance, archtest, docscoverage)
go run ./cmd/docs-coverage-check   # debt = 0
gofmt -l <file .go đổi/mới>        # rỗng sau gofmt -w
```
Golden trace (`v4-runtime-clean.json`, `v4-runtime-crash-recovery.json`) xác nhận reproducible byte-exact
qua nhiều lần regenerate độc lập trước khi commit (không chỉ "test pass một lần") — phát hiện và sửa 2
bug thật trong CHÍNH canonicalization logic của test lúc kiểm chứng: (a) `jobId` là UUID rời rạc không
thuộc aggregate nào, ban đầu không được alias, gây khác biệt run-to-run — thêm alias `uuid-N` riêng; (b)
substitution ban đầu dùng string-replace thô trên toàn payload text, khiến một RunID ngắn như "t-7" vô
tình khớp SUBSTRING vào cuối một UUID không liên quan ("...t-7"), làm hỏng field khác — sửa bằng parse
JSON có cấu trúc, chỉ thay thế khi giá trị khớp CHÍNH XÁC (không bao giờ substring-scan qua text thô).
`TestRuntimeEngineGate`/`TestRuntimeEngineGate_ConcurrentPoolsNoDuplicateExecution` chạy ổn định 5-8 lần
liên tiếp không flake trước khi coi là xong.

**Hoàn thành khi:** same input/decisions tạo same domain result trừ IDs/timestamps cho phép — chứng minh
qua `TestRuntimeEngineGate`'s own `diffRuntimeEngineFinalResult` (self-comparison) VÀ golden byte-exact
(`assertRuntimeEngineTraceGolden`) cùng lúc, cho cả hai kịch bản sạch/crash-recovery.

**CI PR #27 — vòng 1:** `contract (windows-latest)` fail đúng ở `TestRuntimeEngineGate`'s own golden trace
so sánh (`v4-runtime-crash-recovery.json`) — golden vừa được verify reproducible byte-exact qua 5+ lần
regenerate LOCAL trước khi commit, nhưng CI's own Windows runner cho ra trace khác. Vì PR này đụng
`internal/app/runtime/completion.go`/`scope_expansion.go` (code production thật), không coi đây là flake
mặc định — điều tra tận gốc thay vì rerun mù:
- **Nguồn 1:** `dispatchForkBranches` (V4-10) tạo CẢ HAI job `SCHEDULE_NODE_RUN` của hai nhánh FORK trong
  CÙNG một transaction (cùng `created_at`) — `ClaimJob`'s own tie-break rơi vào `id` thô.
  `registerRuntimeEngineHandlers` đang dùng `idsource.Random{}` cho handler-minted ID, nên nhánh nào
  được worker duy nhất claim (và, với kịch bản crash, bị block) trước là một "tung đồng xu" thật mỗi
  lần chạy — máy tôi tình cờ luôn ra cùng kết quả qua nhiều lần rerun local, CI thì không. Sửa: hai
  `idsource.NewSequential` riêng biệt cho `testIDs`/`handlerIDs` (an toàn vì Concurrency:1 nghĩa là
  worker của pool không bao giờ chạy đồng thời với chính nó, chỉ với goroutine test — hai biến tách
  biệt).
- **Nguồn 2:** trường `jobId` trong payload event VÀ bất kỳ ID sinh ra nào chỉ từng xuất hiện như một
  FIELD trong payload (không bao giờ là AggregateID của chính event nào — ví dụ NodeRunID của node "end",
  vì node terminal không tự route đi đâu nên không bao giờ có NODE_ROUTED event của chính nó) không được
  canonicalization pass theo AggregateID bắt được. Riêng `jobId` còn phát hiện thêm: giá trị của nó
  KHÔNG PHẢI một hàm ổn định của quyết định nghiệp vụ — V4-13's own recovery reaper tự lên lịch lại một
  số vòng thật sự biến thiên trước khi worker duy nhất quay lại xử lý EXECUTE_NODE job của Attempt vừa
  retry, khiến mọi ID sinh sau đó trôi dạt. Sửa: `jobId` chuẩn hóa về placeholder cố định (`<job>` —
  metadata nhân quả/debug, không phải nội dung nghiệp vụ); mọi ID sinh ra dạng khác (mở rộng pattern
  nhận cả UUID lẫn `t-N`/`h-N`/`h2-N`) được alias ổn định theo thứ tự xuất hiện đầu tiên, bất kể xuất
  hiện ở đâu trong payload.
  Verify lại: reproducible byte-exact qua 6+ lần regenerate LOCAL độc lập cho CẢ HAI kịch bản trước khi
  push lại (commit `882910f`).

**CI PR #27 — vòng 2:** `contract` cả hai OS xanh (xác nhận fix determinism đúng). `Linux race and
stability (V0-12)` fail ở `TestWriteLeaseHeartbeatRejectsStaleJobLease`
(`internal/adapters/sqlite/scheduling_test.go`, lỗi "durable job lease is no longer authoritative" dưới
`-race`) — `git diff` xác nhận file này KHÔNG nằm trong diff của PR (lần sửa cuối từ V4-13, `736a833`),
đúng flake pre-existing dưới overhead thời gian của `-race` (test nhạy cảm về TTL/heartbeat). Rerun
`--failed` một lần → 6/6 xanh (bao gồm cả 10 vòng offline-suite stability).

**Kết quả:** PR [#27](https://github.com/draculemihawk123-ai/agent-workflow/pull/27), CI 6/6 xanh sau 2
lần push (1 fix determinism thật) + 1 lần rerun (flake không liên quan diff). Squash-merge vào master
tại `1cac228`. **Đây là task CUỐI CÙNG của chuỗi V4 (V4-01…V4-14) — toàn bộ pha V4 "durable workflow
runtime engine" DONE.**

## V4-13 — Recovery coordinator

**Bối cảnh:** tiếp tục thẳng sau khi V4-12C merge (`f1426ae`), theo đúng lệnh "tự động sang task tiếp
theo". Đọc task text ("quyết định retry/fresh-Start/reconcile/escalate từ durable state") rồi đọc lại
toàn bộ nền tảng đã có: SPK-04's own `ClassifyInterruptedAttempt`/`TerminateInterruptedAttempt`,
SPK-09's own `ReconcileMutatingAttempt`/`QuarantineRepositoryWorkspace` (`internal/app/worker`), sáu
fault boundary chuẩn (`docs/spikes/01-go-core-spike-plan.md §9`) đã có crash-simulation mode sẵn trong
`internal/adapters/sqlite/crashworker.go`, và V4-12B's own `PollGeneration` self-rescheduling pattern
(`ScopeExpansionOrigin`) làm mẫu cho self-rescheduling CONTROL job. Xác nhận qua grep: chưa có
`RECOVERY_REAPER` nào trong `controlJobKinds`/CHECK allow-list (migration 23 cố tình loại nó, để "một
quyết định riêng sau này" — chính là task này), và ADR-020's own reason vocabulary
(`LEASE_LOST`/`OWNERSHIP_LOST_MUTATING`) chưa từng được dùng ở đâu — mọi chỗ vẫn phát ra reason cũ trước
SPK-04.

**User chốt hướng (3 câu trước khi code, cả ba đều sửa sâu):**

- **Câu 1 (nâng đời reason vocabulary):** mapping chính xác LOST→`LEASE_LOST`,
  INDETERMINATE→`OWNERSHIP_LOST_MUTATING`; phải cập nhật `ClassifyInterruptedAttempt`/SPK-03/SPK-04/mọi
  sqlite integration test đang assert reason cũ; giữ reason cũ CHỈ để đọc evidence lịch sử, thêm
  validator mới từ chối MỌI ghi mới dùng reason cũ (không dùng mode/flag chuyển đổi — user bác bỏ hướng
  đó tường minh).
- **Câu 2 (cơ chế migration cho RECOVERY_REAPER schema):** quy trình 11 bước chính xác cho migration 25
  (tắt FK NGOÀI transaction, verify, rebuild TRONG transaction, `foreign_key_check`, commit, bật lại FK,
  verify) — kèm yêu cầu đổi hẳn `Store.Migrate` từ một transaction chung cho mọi migration pending sang
  một transaction riêng cho TỪNG migration, mô tả rõ đây "nguyên tắc hơn hẳn một DB/domain inconsistency
  vĩnh viễn" (nếu migration N fail giữa chừng mà N-1 đã lỡ nằm chung transaction, N-1 cũng bị rollback
  dù bản thân nó đúng).
- **Câu 3 (phạm vi FRESH_START):** thực thi FRESH_START thật (gọi `worker.StartFreshFromLatestCheckpoint`,
  primitive có sẵn nhưng chưa handler nào gọi) tường minh NGOÀI phạm vi V4-13 — thuộc V5-13. Lý do kiến
  trúc: RECOVERY_REAPER là `JobClass=CONTROL`, không có RunID/JobLease/cancel_epoch riêng cho một lần
  gọi provider thật; để CONTROL handler tự spawn process sẽ phá chính cancellation/fencing contract
  V4-12B/V4-12C vừa xây. V4-13 CHỈ ghi `DecisionArtifact` cho FRESH_START (checkpoint đã chọn, generation,
  reason, next action) — không bao giờ tạo Attempt/job mồ côi. V5-13 mới là task thật thực thi ba pha
  (Tx reserve → gọi provider thật ngoài transaction → Tx finalize có fencing).

**Phạm vi thực hiện (tóm tắt — chi tiết đầy đủ trong PR diff):**
- `internal/app/worker/interruption.go`, `internal/adapters/sqlite/attempt_store.go`
  (`ErrLegacyTerminationReason` + guard trong `TerminateInterruptedAttempt`),
  `internal/domain/runtime/termination.go`, `internal/spikeacceptance/spk03_scenario.go`/
  `spk04_scenario.go`, hai sqlite integration test cũ (`crash_resume_attempt_termination_integration_test.go`,
  `crash_resume_checkpoint_integration_test.go`): nâng đời reason vocabulary theo đúng Câu 1.
- `internal/adapters/sqlite/migrations.go` (viết lại `Migrate`: một `*sql.Conn` ghim cố định — vì
  `PRAGMA foreign_keys` gắn theo connection — chạy từng migration trong transaction riêng của chính nó;
  `migrationsRequiringForeignKeysOff` đánh dấu migration nào cần thủ tục FK-off đặc biệt, hiện chỉ
  `{25: true}`), `migrations/0025_recovery_reaper_job_class.sql` (rebuild `durable_jobs`, nới CHECK
  `job_class` nhận `RECOVERY_REAPER`), `migrations/0026_recovery_reaper_state.sql` (bảng singleton
  `recovery_reaper_state`), `migrations_test.go` (2 test atomicity: migration fail giữa chừng chỉ tự nó
  rollback, migration trước đó sống sót), `migration_0025_test.go` (mới: upgrade với dữ liệu thật giữ
  nguyên mọi hàng + FK sống, fail giữa rebuild để lại bảng gốc nguyên vẹn) — kiểm chứng thật bằng một
  scratch program `modernc.org/sqlite` xác nhận `PRAGMA foreign_keys=OFF` là no-op trong một transaction
  đã mở (đúng theo Câu 2, không suy đoán).
- `internal/app/ports/job_class.go` (thêm `RECOVERY_REAPER` vào `controlJobKinds`),
  `internal/app/ports/unitofwork.go` (6 method mới trên `RuntimeRepository` +
  `RecoveryReaperState`/`AdvanceRecoveryReaperGenerationRequest`),
  `internal/adapters/sqlite/recovery_reaper.go` (mới, implement 6 method) + `fake/unitofwork.go`/
  `fake/runtime.go` (fake counterpart, thêm cross-reference `RuntimeRepository.jobs` để
  `ListOrphanedRunningExecutionAttempts` đọc được lease state).
- `internal/app/runtime/recovery_reaper.go` (mới, lớn nhất): `RecoveryReaperHandler` — nhận cả
  `ports.UnitOfWork` (bước V4-native) lẫn ba interface spike-era `InterruptionRecoveryStore`/
  `WorkspaceReconciler`/`RecoveryStore` (bước reconcile duy nhất còn dùng lại primitive SPK-04/SPK-09,
  một `*sqlite.Store` thật thỏa cả ba structurally — không ép hợp nhất sớm). Ba sweep một lần chạy:
  orphaned RUNNING attempt (ma trận RETRY/RECONCILE/ESCALATE/FRESH_START), stranded Run cancellation
  intent, stranded WorkItem cancellation intent — cả hai loại intent stranded là "coordinator chết giữa
  quiesce, phải resume không tạo intent trùng" đúng theo Verify line gốc của task. Tự lên lịch lại theo
  generation qua `recovery_reaper_state`, fence bằng idempotency key `recovery-reaper:<generation>`.
- `internal/app/runtime/event_schema.go` + test + golden fixture: đăng ký `RECOVERY_DECISION_RECORDED`.
- `internal/app/runtime/recovery_reaper_test.go` (mới, fake-backed, 4 test): hai coordinator tranh nhau
  chỉ tạo một job cho một generation; self-reschedule tăng generation đúng một lần; resume stranded Run
  cancellation intent; resume stranded WorkItem cancellation intent.
- `internal/app/runtime/recovery_reaper_sqlite_test.go` (mới, sqlite-backed, 2 test — xem Tự phát hiện
  bên dưới): nhánh RETRY tạo đúng một Attempt/job mới, không lặp ở sweep thứ hai; nhánh ESCALATE (budget
  hết) ghi đúng một `DecisionArtifact`, không Attempt/job mới.

**Câu hỏi kiến trúc phát sinh giữa chừng (Câu 4, chốt với user trước khi sửa tiếp):** hai test
sqlite-backed đầu tiên fail ngay ở `EnqueueJob` — `durable_jobs.project_id TEXT NOT NULL
REFERENCES projects(id)` cho MỌI job, nhưng RECOVERY_REAPER là CONTROL job singleton toàn cục (đúng
theo Câu 3 ở trên, không theo per-origin), không thuộc project nào; mọi job kind khác trong codebase
(kể cả ba CONTROL kind còn lại) đều có `run.ProjectID` thật để truyền. User bác bỏ phương án seed một
"system" Project sentinel (rò rỉ vào mọi query/authorization/export/UI project-scoped) và chốt
**nullable có invariant chặt theo kind, không phải nullable chung cho mọi CONTROL job**: gộp thay đổi
này vào chính migration 25 (đang rebuild `durable_jobs` sẵn) — `project_id` mất `NOT NULL`, CHECK mới ép
`kind = 'RECOVERY_REAPER'` bắt buộc `project_id`/`run_id`/`cancel_epoch` đều NULL VÀ
`job_class = 'CONTROL'`, mọi kind khác bắt buộc `project_id NOT NULL`. Ở tầng Go: thêm
`internal/app/ports.ValidateJobScope(kind, projectID, runID)` làm authority thứ hai, gọi từ CẢ
`sqlite.enqueueJobTx`/`insertDispatchedJob` lẫn `fake.JobsRepository.EnqueueJob` (cùng một hàm, không
lệch giữa hai backend); `DurableJob.ProjectID`/`EnqueueJobRequest.ProjectID` giữ nguyên kiểu giá trị
(không đổi sang con trỏ) với chuỗi rỗng = không có Project, mirror đúng convention `RunID string` sẵn có
— nhưng tầng SQL luôn bind `nil` thật khi rỗng (không bao giờ `''`), scan lại qua `sql.NullString`.

**Tự phát hiện 1 bug thật khi viết test sqlite-backed:** helper tìm job RECOVERY_REAPER
(`firstSQLiteJobOfKind`) claim-rồi-bỏ-qua từng job không đúng kind trong một vòng lặp — trong test
ESCALATE, lúc đó có một job `EXECUTE_NODE` khác (của chính attempt đang cần orphaned) vẫn còn AVAILABLE;
helper vô tình "cứu" nó bằng một lease mới còn hạn 1 phút khi lướt qua tìm đúng kind, khiến
`ListOrphanedRunningExecutionAttempts` không còn thấy nó orphaned nữa vào lúc `Handle` thật sự quét —
symptom: attempt vẫn RUNNING sau `Handle`, không lỗi nào nổi lên (classification chỉ lặng lẽ không chạy
tới). Debug bằng cách gọi trực tiếp `ListOrphanedRunningExecutionAttempts` trước/sau từng bước để cô lập
đúng thời điểm mất orphaned status. Sửa: claim đúng job `EXECUTE_NODE` cần hết hạn TRƯỚC khi gọi
`StartupRecoveryScan`/tìm RECOVERY_REAPER — không bao giờ để một job đang cần orphaned nằm AVAILABLE lúc
search kind khác lướt qua nó.

**Đã triển khai (test mới):**
- `internal/app/ports/job_class_test.go` (2 test mới): `ValidateJobScope` chấp nhận RECOVERY_REAPER với
  ProjectID/RunID đều rỗng, từ chối khi một trong hai không rỗng; mọi kind khác (kể cả ba CONTROL kind
  còn lại) đều bị từ chối khi ProjectID rỗng.
- `internal/adapters/sqlite/scheduling_test.go` (3 test mới): RECOVERY_REAPER với ProjectID rỗng được
  chấp nhận và đọc lại đúng SQL NULL (không phải chuỗi rỗng); kind khác với ProjectID rỗng bị từ chối;
  ProjectID giả không tồn tại bị FK từ chối, không hàng nào được ghi.
- `internal/app/ports/fake/job_scope_test.go` (mới, 5 case): fake `JobsRepository.EnqueueJob` từ chối/
  chấp nhận đúng như sqlite cho cùng tập request.
- `internal/adapters/sqlite/migration_0025_test.go` (mở rộng): RECOVERY_REAPER+CONTROL+NULL
  project/run/cancel_epoch được chấp nhận; RECOVERY_REAPER với ProjectID/RunID/cancel_epoch không NULL
  đều bị CHECK từ chối riêng lẻ; CANCEL_RUN_COORDINATOR với ProjectID NULL vẫn bị từ chối (exemption hẹp,
  không lan sang CONTROL kind khác); assertion mới ProjectID của job cũ sống sót nguyên vẹn qua rebuild.
- `internal/app/runtime/recovery_reaper_sqlite_test.go`: 2 test RETRY/ESCALATE (mô tả ở trên).

**Verify (chạy local — Go toolchain `C:\Program Files\Go\bin`, PATH export thủ công mỗi lần gọi):**
```
go build ./...                     # sạch
go vet ./...                       # sạch
go test ./... -count=1             # toàn bộ package PASS (kể cả spikeacceptance, archtest, docscoverage)
go run ./cmd/docs-coverage-check   # debt = 0
gofmt -l <file .go đổi/mới>        # rỗng sau gofmt -w
```

**CI PR #26:** vòng đầu `contract (ubuntu-latest)` fail ở `TestPool_TwoPoolsRaceRecovery_NoDuplicateProcessing`
(`internal/app/workerpool`) — `git diff origin/master...feat/v4-13-recovery-coordinator -- internal/app/workerpool/`
rỗng, package này hoàn toàn không nằm trong diff (file test cuối sửa từ V1-10, `400a911`) — đúng flake
pre-existing đã ghi nhận trước đó ở hotfix PR #23 ("Linux race and stability... không liên quan diff").
Rerun `--failed` một lần → 6/6 xanh (bao gồm `Linux race and stability`, `cross-platform semantic diff
(SPK-13)`, `spike acceptance` cả hai OS).

**Kết quả:** PR [#26](https://github.com/draculemihawk123-ai/agent-workflow/pull/26), CI 6/6 xanh sau 1
lần rerun (flake không liên quan diff). Squash-merge vào master tại `736a833`. Tiếp tục V4-14 ngay sau
đây theo đúng lệnh "tự động sang task tiếp theo".

## Hotfix: SPK-13/SPK-10 cross-platform nondeterministic winner

**Bối cảnh:** PR #22 (V4-12A) code-complete, verify local xanh toàn bộ, nhưng CI fail ở
`cross-platform semantic diff (SPK-13)`. Log: `SPK-10 diffs=3` — `assertions[1].detail: "version=2
state=RUNNING" != "version=2 state=CANCELLED"` cộng sha256/size của `runtime/transitions.jsonl` lệch
theo. Nghi ngờ ban đầu là flake — nhưng theo đúng protocol đã lập (verify predates diff qua `git log`,
rerun `--failed` một lần, escalate nếu tái diễn): `git log` xác nhận `spk10_scenario.go` lần cuối sửa
2026-09-01 (V0-10A), SPK-13 pass sạch trên cả 3 lần merge master gần nhất (V4-10/V4-11/V4-12, cùng ngày
hôm nay) — không liên quan diff của tôi. Rerun `--failed` lần 1 vẫn fail nhưng dùng LẠI evidence cũ (cùng
timestamp manifest) — không phải một trial độc lập thật. Rerun TOÀN BỘ workflow (không chỉ job fail) để
lấy evidence hoàn toàn mới — **tái diễn với sha256/size giống hệt lần trước** (không chỉ "có khác biệt"
mà là CÙNG một khác biệt) — dấu hiệu rõ ràng đây là một bias xác định trong lịch goroutine scheduler giữa
Windows/Linux, không phải nhiễu ngẫu nhiên. Đã dừng rerun, đọc code `spk10_scenario.go`, xác nhận root
cause thật: SPK-10's own race cố ý để hai goroutine tranh CAS không có tie-break — RUNNING hay CANCELLED
đều là kết quả hợp lệ (`consistent := final.Version==2 && (state==Running||state==Cancelled)` đã viết sẵn
đúng ý đó từ đầu) — nhưng lại ghi THẲNG state cụ thể đó vào `Assertion.Detail` và artifact
`runtime/transitions.jsonl`, trong khi `SemanticDiff` (semantic_diff.go) so sánh CHÍNH XÁC byte-for-byte
mọi assertion detail và mọi non-process artifact hash — đúng theo doc comment của chính nó: "If one of
these ever legitimately differed across platforms, that is exactly the unknown difference this function
must report, not an artifact to normalize away." Đây KHÔNG PHẢI lỗi ngẫu nhiên CI mà là gap thiết kế thật
giữa hai gate — dừng lại hỏi user hướng sửa trước khi tự quyết (không admin-merge, không rerun mù).

**User chốt hướng sửa (Vietnamese, rất cụ thể):** Giữ race SPK-10 thật sự nondeterministic — không thêm
tie-break giả. Chuẩn hóa đầu ra semantic của SPK-10 thành invariant ổn định: `winners=1 stale=1
finalVersion=2 finalStateAllowed=true` — không đưa giá trị cụ thể RUNNING/CANCELLED vào assertion detail
hay artifact được SPK-13 so hash. Giữ SemanticDiff nghiêm ngặt — không thêm allow-list rộng bỏ qua toàn
bộ artifact SPK-10 (có thể che regression thật). Thêm test chứng minh: (a) Windows thắng RUNNING, Linux
thắng CANCELLED ⇒ semantic-diff vẫn bằng nhau; (b) sai số winner/stale/version hoặc trạng thái ngoài tập
hợp hợp lệ ⇒ vẫn fail. Merge hotfix khi toàn bộ gate xanh, rebase PR #22 lên commit đó, chạy lại CI tại
HEAD mới — không admin-merge PR #22, không tiếp tục rerun PR #22 ở trạng thái cũ.

**Phạm vi thực hiện:**
- **`internal/spikeacceptance/spk10_scenario.go`**: hàm mới `spk10TransitionsEvidence(winners, stale int,
  finalVersion uint64, finalState domainruntime.WorkflowRunState) (detail string, payload map[string]any,
  consistent bool)` — tính `finalStateAllowed := finalState==Running || finalState==Cancelled`, trả về
  detail `"version=%d finalStateAllowed=%t"` (không còn `state=%s`) và payload
  `{winners, stale, finalVersion, finalStateAllowed}` (không còn key `finalState` mang giá trị cụ thể).
  `runSPK10Scenario` gọi hàm này thay vì tự format/build map trực tiếp — logic Pass/Fail
  (`winners==1 && stale==1`, `consistent`) giữ nguyên hoàn toàn, chỉ đổi CÁCH BÁO CÁO.
- **`internal/spikeacceptance/spk10_scenario_test.go`** (mới, 7 test): 2 test thuần trên helper
  (RUNNING/CANCELLED cho evidence giống hệt nhau — cả detail lẫn payload qua `reflect.DeepEqual`; state
  CREATED ngoài tập hợp hợp lệ → `finalStateAllowed=false`/không consistent); 1 test version sai → không
  consistent; `spk10GoldenResult` helper dùng REAL `evidence.Bundle` (không tự tính hash tay) để build
  SPKResult đầy đủ; `TestSemanticDiff_SPK10_WindowsRunningVsLinuxCancelled_AreEqual` — đúng yêu cầu (a) của
  user, `SemanticDiff` giữa hai golden result (một thắng RUNNING, một thắng CANCELLED) trả về 0 diff;
  `TestSemanticDiff_SPK10_GenuineInvariantViolation_StillReported` — đúng yêu cầu (b), `winners=2` (bug
  giả định) vẫn bị `SemanticDiff` báo diff dù finalStateAllowed vẫn true ở cả hai bên;
  `TestRunSPK10Scenario_RealRace_NeverLeaksConcreteWinningState` — chạy THẬT `runSPK10Scenario` (không
  mock), assert Detail không bao giờ chứa literal "RUNNING"/"CANCELLED" — chạy 10 lần liên tiếp local đều
  pass (không flake).

**Verify (chạy local, trên branch riêng `fix/spk13-spk10-nondeterministic-winner` tách từ `master`, không
lẫn với thay đổi V4-12A):**
```
go build ./...                     # sạch
go vet ./...                       # sạch
go test ./... -count=1             # toàn bộ package PASS
go test ./internal/spikeacceptance/... -run "SPK10|Semantic" -count=10  # 10 lần liên tiếp, không flake
go run ./cmd/docs-coverage-check   # debt = 0
gofmt -l <file .go đổi/mới>        # rỗng sau gofmt -w
```

**CI PR #23:** vòng đầu SPK-13 đã **pass** (xác nhận fix đúng), nhưng `Linux race and stability` fail ở
run 4/10 của stability loop — `TestPool_TwoPoolsRaceRecovery_NoDuplicateProcessing`
(`internal/app/workerpool`, package hotfix này không hề đụng) với lỗi "context canceled" lúc khởi động
recovery scan, 9/10 lần chạy khác pass sạch — flake tài nguyên CI runner không liên quan diff. Rerun
`--failed` một lần → 6/6 xanh.

**Kết quả:** PR [#23](https://github.com/draculemihawk123-ai/agent-workflow/pull/23), squash-merge vào
master tại `2556671`. Rebase PR #22 (`feat/v4-12a-scope-expansion-reactivation`) lên master mới (fast-
forward sạch, không conflict — không file nào chồng lấn), force-push, verify local lại toàn bộ (xanh),
chờ CI chạy lại tại HEAD mới.

## V4-12C — WorkItem cancellation và blocker commands

**Bối cảnh:** tiếp tục thẳng sau khi V4-12B merge (`ef19dbf`), theo đúng lệnh "tự động sang task tiếp
theo". Đọc task text ("CancelWorkItem/ResolveWorkItemBlocker có application authority thật; hiện chỉ
được nhắc như command riêng mà không task nào sở hữu handler") rồi nghiên cứu scaffolding có sẵn: xác
nhận qua grep KHÔNG có blocker domain type/table nào tồn tại (`WorkItemBlocked` chỉ là một
`WorkItemStatus` enum value, `internal/domain/work/work.go`), `WorkItemCancellationIntent` đã tồn tại từ
V4-01 (schema + `RecordWorkItemCancellationIntent`/`GetWorkItemCancellationIntent`) nhưng chưa có
`TransitionWorkItemCancellationIntentState` hay bất kỳ coordinator nào drive nó, và `StartWorkflowRun`
đã có sẵn GC-INV-39 fence đọc intent này. Đọc lại toàn bộ ADR-020 (đã đọc một phần cho V4-12B, đọc nốt
phần còn lại: `RetryBlockedActivation`, ADR-021 CompletionDecision outcome table, ADR-025 command scope)
để lấy đúng blocker-type × resolution-mode table và semantics RESOLVED/WAIVED.

Trước khi code, phát hiện một câu hỏi kiến trúc thật (không phải chi tiết implementation bị bỏ ngỏ):
V4-12B (đã merge) chưa hề tạo blocker hay đụng `WorkItem.Status` khi Run đóng CANCELLED — đúng theo
Phạm vi của chính task đó ("orchestrator/scheduler, cancel_epoch"). Nhưng ADR-020's own cancel-outcome
table nói rõ "CancelRun → Run CANCELLED, WorkItem BLOCKED kèm blocker RUN_CANCELLED" — nghĩa là hậu điều
kiện này CHƯA từng được triển khai ở đâu cả. Dừng lại hỏi user trước khi quyết định có nên sửa ngược
lại code V4-12B đã merge hay không, và SCOPE_EXPANSION_REQUIRED (đã có producer thật từ V4-12A) có cần
wiring blocker thật ngay bây giờ hay chỉ khai type.

**User chốt hướng (2 câu, cả hai đều sửa sâu):**

- **Câu 1 (RUN_CANCELLED blocker wiring):** *"V4-12C phải mở rộng điểm đóng của V4-12B. Nếu chỉ tạo
  schema/command rồi seed blocker trong test thì contract ADR-020 vẫn chưa được triển khai end-to-end."*
  Kèm một sửa quan trọng thêm: `reconcileCancellingRunTx` hiện đòi cả `LiveCount==0` VÀ
  `BlockedCount==0` — điều này có thể làm Run mắc kẹt CANCELLING vĩnh viễn (không gì trong protocol từng
  resume một activation BLOCKED). Điều kiện đóng đúng phải là `LiveCount==0` một mình; NodeRun/Attempt
  BLOCKED giữ nguyên làm lịch sử, không cản Run đóng. Trong cùng transaction đóng Run: CAS
  CANCELLING→CANCELLED, tạo blocker tất định (Type=RUN_CANCELLED, State=OPEN, liên kết SourceRunID,
  idempotent theo Run), WorkItem ACTIVE→BLOCKED (hoặc chỉ thêm blocker nếu đã BLOCKED), append event.
  Ngoại lệ quan trọng: nếu Run được quiesce do `CancelWorkItem` (đã có `work_item_cancellation_intent`),
  KHÔNG được tạo blocker mở — WorkItem đã trên đường tới CANCELLED, không được đẩy ngược về BLOCKED.
- **Câu 2 (SCOPE_EXPANSION_REQUIRED wiring):** *"Chọn wiring thật SCOPE_EXPANSION_REQUIRED, nhưng phải
  nối đủ cả vòng đời, không chỉ tạo blocker."* Producer thật ngay bây giờ chỉ có hai: RUN_CANCELLED và
  SCOPE_EXPANSION_REQUIRED (dùng producer có sẵn từ V4-12A). Bốn admission reason + COMPLETION_POLICY_FAILED
  chỉ ở tầng type/matrix — producer thuộc V5-08 (admission enforcement)/V5-11 (CompletionPolicy), không
  phải phạm vi task này. Blocker mở trong CÙNG transaction Attempt/NodeRun chuyển BLOCKED; approval đơn
  thuần CHƯA resolve blocker (workspace có thể còn provisioning); chỉ khi SCOPE_EXPANSION_RECONCILE xác
  nhận approved + WorkspaceSet READY + tạo activation mới xong, TRONG CÙNG transaction: blocker
  OPEN→RESOLVED, WorkItem BLOCKED→ACTIVE (không phải READY — cùng Run tiếp tục, chưa từng dừng). Quan
  trọng: `ResolveWorkItemBlocker` công khai KHÔNG được tự resolve/waive SCOPE_EXPANSION_REQUIRED — authority
  chỉ thuộc approval/reconcile flow (ADR-011). Đổi Phụ thuộc task thành V4-12A + V4-12B. Loại chưa có
  producer được seed trực tiếp trong test là chấp nhận được, nhưng test phải khoá authority matrix (WAIVED
  luôn bị từ chối cho admission + scope-expansion; RESOLVED sinh (generic) cho scope-expansion cũng bị từ
  chối).

**Phạm vi thực hiện:**
- **`internal/domain/work/blocker.go`** (mới): `BlockerID`/`BlockerType`/`BlockerState`/`WorkItemBlocker`
  + `NewWorkItemBlocker`. `BlockerType` cố tình là string thuần (không import
  `runtime.TerminationReason`) — `internal/domain/runtime` đã import package này, import ngược lại sẽ
  thành cycle; mọi hằng số là literal trùng giá trị `TerminationReason` tương ứng, giống chính xác cách
  `ports.ClassifyJobKind`'s own CONTROL allow-list (V4-12B) đã xử lý cùng vấn đề cycle. `Waivable()` (chỉ
  true cho RUN_CANCELLED/COMPLETION_POLICY_FAILED) và `ResolvableViaCommand()` (false chỉ cho
  SCOPE_EXPANSION_REQUIRED) là authority matrix chính, khoá bằng test.
- **`internal/adapters/sqlite/migrations/0024_work_item_blockers.sql`** (mới): bảng `blockers`
  (id/project_id/work_item_id/type/state/source_run_id/source_node_run_id/source_attempt_id/reason/
  opened_at/resolved_at/resolved_by/resolution_note/decision_artifact_id/version), CHECK constraint 7
  giá trị type + 3 giá trị state, index `(work_item_id, state)`. File `0013_*.sql` vẫn thiếu lịch sử
  (đã xác nhận từ V4-12B) nên tổng migration file = 23 (0001-0012 + 0014-0024) — 3 test hard-code count
  (`db_test.go` x2, `unitofwork_test.go`) đổi từ 22 → 23.
- **`internal/app/ports/unitofwork.go`**: `RuntimeRepository` thêm `TransitionWorkItemCancellationIntentState`
  (mirror `TransitionRunCancellationIntentState` — fenced theo `(WorkItemID, ExpectedState)`, không có
  Version) và `ListWorkflowRunsForWorkItem` (mọi Run một WorkItem từng có, không lọc state — cả
  CancelWorkItem's own quiesce sweep lẫn ResolveWorkItemBlocker's own nonterminal-Run precondition đều
  cần).
- **`internal/app/ports/work.go`**: `WorkRepository` thêm `CreateWorkItemBlocker` (idempotent theo ID —
  mọi producer thật đều mint ID tất định từ aggregate gốc), `GetWorkItemBlocker`,
  `ListWorkItemBlockersForWorkItem`, `TransitionWorkItemBlockerState` (CAS OPEN→RESOLVED|WAIVED) +
  `TransitionWorkItemBlockerStateRequest`.
- **`internal/adapters/sqlite/work_item_blocker.go`** (mới) + **`runtime_manifest.go`** (thêm
  `TransitionWorkItemCancellationIntentState`) + **`advance_run.go`** (thêm
  `ListWorkflowRunsForWorkItem`): sqlite implementation, theo đúng pattern `xxxTx(ctx, tx *sql.Tx, ...)`
  file cùng concern đã dùng.
- **`internal/app/ports/fake/work.go`** + **`fake/runtime.go`**: fake counterpart cho toàn bộ method mới
  ở trên, cùng discipline CAS/idempotent-insert-or-load các fake khác đã dùng.
- **`internal/app/runtime/cancel_run.go`** (refactor): tách phần thân `CancelRun` thành `cancelRunTx(ctx,
  tx, ids, runID, actor, reason, correlationID)` — `CancelRun` giờ chỉ mở transaction rồi gọi hàm này.
  Bắt buộc phải tách vì `CancelWorkItem` cần drive đúng protocol CancelRun cho từng active Run TRONG
  transaction của chính nó — một `UnitOfWork.WithSerializedWrite` không được mở transaction lồng.
- **`internal/app/runtime/cancel_work_item.go`** (mới): `CancelWorkItem` — ghi
  `WorkItemCancellationIntent` (idempotent theo WorkItemID, `ErrWorkItemAlreadyTerminal` nếu WorkItem đã
  DONE/CANCELLED), gọi `cancelRunTx` cho từng Run chưa terminal (`ListWorkflowRunsForWorkItem`), rồi gọi
  `reconcileWorkItemCancellationTx` ngay trong transaction (đóng luôn nếu 0 active Run hoặc mọi Run đã
  terminal từ trước — không cần đợi một Run-closing transaction không bao giờ tới).
  `reconcileWorkItemCancellationTx` (dùng lại từ `transitionRunToCancelledTx`/`transitionRunToFailedTx`
  ở completion.go — mọi nơi một Run chạm terminal thật): nếu intent đang REQUESTED VÀ mọi Run của
  WorkItem đã terminal, CAS WorkItem→CANCELLED, intent→COMPLETED, append `WORK_ITEM_CANCELLED`.
- **`internal/app/runtime/resolve_work_item_blocker.go`** (mới): `ResolveWorkItemBlocker` — blocker đã
  RESOLVED|WAIVED là no-op idempotent; nếu OPEN: kiểm `ResolvableViaCommand()` (từ chối
  SCOPE_EXPANSION_REQUIRED ở CẢ hai mode), WAIVED thêm kiểm `Waivable()` + bắt buộc
  Actor/Reason/PolicyGrantRef + tạo `DecisionArtifact` (Kind="WorkItemBlockerWaiver", PolicyVersion=
  PolicyGrantRef — không có registry policy-grant thật nào tồn tại trong codebase, validate chỉ ở mức
  non-blank, cùng "loosely-specified vocabulary" treatment `JoinPolicy` đã nhận); rồi kiểm 2 precondition
  chung (không Run nonterminal, không RepositoryWorkspace nào QUARANTINED trong family — dùng lại
  `ListWorkspaceSetRepositoryWorkspaces` + check state, cùng pattern `workspacerelease`'s own
  `ErrWorkspaceSetHasQuarantinedRepository` đã dùng); cuối cùng gọi `closeWorkItemBlockerTx`
  (unlockedStatus=READY — Run gây block đã mất, một run mới là bước tiếp theo).
- **`internal/app/runtime/blocker.go`** (mới): hai helper dùng chung — `openWorkItemBlockerTx` (tạo
  blocker + CAS WorkItem ACTIVE→BLOCKED nếu cần + append `WORK_ITEM_BLOCKED`, dùng bởi
  `transitionRunToCancelledTx` và `requestScopeExpansionTx`) và `closeWorkItemBlockerTx` (CAS blocker +
  unlock WorkItem CHỈ khi CẢ hai điều kiện tách rời đúng: 0 blocker OPEN còn lại VÀ không
  WorkItemCancellationIntent nào đang REQUESTED — dùng bởi `ResolveWorkItemBlocker` và
  `reactivateBlockedNodeRunTx`). Mọi event đúc aggregate identity mới `"WorkItemBlocker"`/blocker.ID
  (Sequence 1 mở, Sequence 2 đóng) thay vì tái dùng `"WorkItem"`/WorkItem.Version — một WorkItem có thể
  tích luỹ nhiều blocker OPEN mà Version không đổi giữa các lần, tái dùng `"WorkItem"` sẽ đụng
  `UNIQUE(aggregate_type, aggregate_id, sequence)` ngay lần blocker thứ hai.
- **`internal/app/runtime/completion.go`**: `reconcileCancellingRunTx` bỏ điều kiện `BlockedCount==0`
  (chỉ còn `LiveCount==0`, đúng theo Câu 1). `transitionRunToCancelledTx` thêm gọi
  `openRunCancelledBlockerTx` (bỏ qua nếu `WorkItemCancellationIntent` tồn tại — bất kỳ state nào, kể cả
  COMPLETED, vì đây là câu hỏi "Run này có phải bị CancelWorkItem drive không", khác câu hỏi "WorkItem
  còn việc cần đóng không" mà `reconcileWorkItemCancellationTx` tự hỏi) rồi gọi
  `reconcileWorkItemCancellationTx`. `transitionRunToFailedTx` cũng thêm gọi
  `reconcileWorkItemCancellationTx` (một Run FAILED bình thường có thể vẫn là Run cuối cùng còn sống của
  một WorkItem đang bị CancelWorkItem quiesce).
- **`internal/app/runtime/finalize.go`**: `requestScopeExpansionTx` thêm gọi `openWorkItemBlockerTx`
  (blockerID tất định = `AttemptID + "-scope-expansion-blocker"`, trùng namespace với
  `ScopeExpansionOriginID` đã dùng AttemptID).
- **`internal/app/runtime/scope_expansion.go`**: `reactivateBlockedNodeRunTx` thêm bước cuối resolve
  blocker (tolerant `ErrPersistenceNotFound` — một origin seed tay trong test cũ trước khi có wiring này
  không có blocker tương ứng, không được lỗi).
- **`internal/app/runtime/event_schema.go`** + test + 4 golden fixture: đăng ký
  `WORK_ITEM_CANCELLATION_REQUESTED`, `WORK_ITEM_CANCELLED`, `WORK_ITEM_BLOCKED`,
  `WORK_ITEM_BLOCKER_RESOLVED`.

**Tự phát hiện 1 bug thật khi viết test** (`TestRace_ResolveWorkItemBlockerVsCancelWorkItem_IntentFirst_...`):
`ResolveWorkItemBlocker`'s own result tính `WorkItemUnblocked: finalItem.Status != workdomain.WorkItemBlocked`
— sai, vì WorkItem có thể đã rời BLOCKED vì một lý do HOÀN TOÀN khác (CancelWorkItem đóng nó thành
CANCELLED trong khi blocker vẫn còn OPEN) trước khi `ResolveWorkItemBlocker` này chạy — kết quả là
dương tính giả `WorkItemUnblocked=true` kèm `WorkItemStatus=CANCELLED` (WorkItem "unblocked" xuống một
trạng thái KHÔNG PHẢI unblocked). Sửa bằng cách đổi `closeWorkItemBlockerTx` trả về một
`closeWorkItemBlockerResult{Blocker, Unblocked, NewStatus}` — tín hiệu unblocked THẬT được tính MỘT LẦN
bên trong hàm (nơi hai điều kiện tách rời được kiểm), không bao giờ suy lại từ status cuối cùng ở tầng
gọi.

**Đã triển khai (tóm tắt test, 27 test mới):**
- `cancel_work_item_test.go` (6 test): 0/1/nhiều active Run (Run cũ terminal bị bỏ qua hoàn toàn, Run
  mới được quiesce đúng); idempotent duplicate call; đã DONE/CANCELLED bị `ErrWorkItemAlreadyTerminal`;
  race intent-trước-PASS mô phỏng CAS thật của một CompletionPolicy tương lai (không có command đó trong
  codebase — mô phỏng CAS trực tiếp, cùng discipline V4-12B's own race test đã dùng) → `ErrOptimisticConflict`
  tự nhiên.
- `resolve_work_item_blocker_test.go` (21 test kể cả 11 case bảng ma trận): blocker OPEN resolve thành
  công + unblock READY; đã RESOLVED/WAIVED no-op idempotent; Run nonterminal bị từ chối; workspace
  QUARANTINED bị từ chối; thiếu mode bị từ chối; ma trận resolution-mode × blocker-type đầy đủ (WAIVED
  chỉ RUN_CANCELLED/COMPLETION_POLICY_FAILED; SCOPE_EXPANSION_REQUIRED từ chối cả hai mode; 4 admission
  reason nhận RESOLVED, từ chối WAIVED); WAIVED thiếu PolicyGrantRef bị từ chối; nhiều blocker giữ BLOCKED
  tới cái cuối; race resolve-vs-cancel cả hai thứ tự (intent trước → WorkItem giữ CANCELLED vĩnh viễn dù
  blocker sau đó vẫn resolve được; resolve trước → cancel sau vẫn quiesce Run mới đúng).
- `scope_expansion_test.go` (mở rộng
  `TestScopeExpansion_EndToEnd_NewRepositoryProvisionedReactivatesNodeRun`): thêm assertion blocker
  SCOPE_EXPANSION_REQUIRED RESOLVED + WorkItem ACTIVE sau khi reactivation thật xảy ra.
- `cancel_run_test.go`: viết lại `TestFinalizeExecutionAttempt_BlockedThenCancel_...` (tên cũ
  `_NotAutoClosedByCancelling` giờ sai — đổi thành `_ClosesWhileNodeRunStaysBlocked`) — hành vi đúng bây
  giờ là Run đóng CANCELLED dù NodeRun còn BLOCKED, với 2 blocker OPEN cùng tồn tại
  (SCOPE_EXPANSION_REQUIRED + RUN_CANCELLED) khiến WorkItem giữ BLOCKED.

**Verify (chạy local — Go toolchain tìm thấy tại `C:\Program Files\Go\bin` phiên này, cùng version
go1.27.0 với `.tools/go1.27.0` đã ghi ở đầu file; PATH export thủ công mỗi lần gọi):**
```
go build ./...                     # sạch
go vet ./...                       # sạch
go test ./... -count=1             # toàn bộ package PASS (kể cả archtest, docscoverage)
go run ./cmd/docs-coverage-check   # debt = 0
gofmt -l <23 file .go đổi/mới>     # rỗng sau gofmt -w (CRLF do git-on-Windows, đúng issue tái diễn đã ghi)
```

**Kết quả:** PR [#25](https://github.com/draculemihawk123-ai/agent-workflow/pull/25), CI 6/6 xanh ngay
lần đầu. Squash-merge vào master tại `f1426ae`. Tiếp tục V4-13 ngay sau đây theo đúng lệnh
"tự động sang task tiếp theo".

## V4-12B — Cancellation coordinator

**Bối cảnh:** tiếp tục thẳng sau khi V4-12A merge (6728110), theo đúng lệnh "tự động sang task tiếp theo".
Đây là task LỚN NHẤT trong toàn bộ phase V4 tính đến giờ: đọc task text ("CancelRun là protocol quiesce có
durable intent, không phải một CAS thẳng CANCELLED; cancel fence cancel_epoch chỉ áp cho JobClass=RUN_WORK;
worker re-check tại hai chốt") rồi nghiên cứu kỹ scaffolding có sẵn (dùng một Explore agent nền để map toàn
bộ: `DurableJob`/`EnqueueJobRequest` chưa có `RunID`/`JobClass` field nào; `ClaimJob` chưa phân biệt class gì
cả; `WorkflowRunCancelled`/`NodeRunCancelled`/`ExecutionAttemptCancelled`/`BranchTokenCancelled`/
`WaitRegistrationCancelled`/`ApprovalRequestCancelled` đều đã có trong enum từ V4-01 nhưng ZERO producer
thật; `run_cancellation_intents`/`work_item_cancellation_intents` đã có bảng từ migration 0016 nhưng chỉ
mỗi `StartWorkflowRun`'s own GC-INV-39 fence từng đọc nó; legacy `CompareAndSwapWorkflowRun`/
`WorkflowRunTransition` (crashworker.go) hoàn toàn tách biệt V4, tự verify xong để loại khỏi phạm vi mà
không cần hỏi). Phát hiện 2 fork thiết kế thật sự trước khi code, đã hỏi user cả 2.

**Câu hỏi 1 — coordinator job CancelRun tự enqueue thực sự làm gì, Run đi tới CANCELLED bằng đường nào:**
đề xuất "Recommended" của tôi (một job CONTROL `CANCEL_RUN_COORDINATOR` sweep đồng bộ MỘT LẦN trong một
transaction — CAS mọi WAIT ACTIVE/APPROVAL PENDING/Attempt+NodeRun QUEUED/NodeRun chưa chạy khác sang
CANCELLED — rồi hoặc CAS thẳng Run CANCELLING→CANCELLED nếu hết live, hoặc để lại CANCELLING chờ Attempt
RUNNING cuối cùng tự finalize; mở rộng `reconcileRunTerminalityTx` — V4-12 — thêm nhánh CANCELLING-và-
zero-live→CANCELLED làm điểm đóng DUY NHẤT) được CHỌN — nhưng câu trả lời của user cho câu hỏi này (cả hai
lần hỏi lại) bị **lỗi công cụ AskUserQuestion**: lần đầu lặp lại y hệt nội dung câu trả lời cho một câu hỏi
KHÁC (về hotfix SPK-13, đã hỏi ở phiên trước); hỏi lại riêng lần hai vẫn lặp lại nội dung (lần này là toàn
bộ text cả 3 option ghép lại), không phải một lựa chọn rõ ràng. Quyết định KHÔNG hỏi lại lần ba (tránh lặp
lỗi công cụ, tránh làm phiền user) — tự tiến với phương án "Recommended" vì: (a) đó là lựa chọn xuất hiện
ĐẦU TIÊN trong nội dung bị lặp cả hai lần, dấu hiệu gần nhất với việc user đã thật sự chọn nó qua UI dù
nội dung trả về bị hỏng; (b) tự nó biện minh chặt chẽ nhất từ chính task text (Verify line đòi test
"cancellation job CONTROL vẫn claim được trong khi workload job của cùng Run thì không" — vô nghĩa nếu
không có job CONTROL nào thực sự cần chạy việc gì); (c) khớp tiền lệ codebase (sweep một lần trong một
transaction, giống hệt `dispatchForkBranches`/`reconcileRunTerminalityTx` chính nó, chưa từng có tiền lệ
polling nhiều lần ngoài V4-12A's own SCOPE_EXPANSION_RECONCILE — mà job đó tự lên lịch lại vì phải CHỜ một
sự kiện ngoài, không giống sweep cancel chỉ cần CHẠY MỘT LẦN vì mọi dữ liệu cần đọc đã sẵn sàng ngay lúc
đó). Đã báo minh bạch với user đây là quyết định do lỗi công cụ, không phải do user thật sự xác nhận qua
văn bản, mời sửa nếu sai trước khi merge.

**Câu hỏi 2 — JobClass CONTROL allow-list xử lý sao khi ADR/design doc dùng tên lệch code thật:** user trả
lời rõ ràng, đầy đủ (không bị lỗi công cụ): dùng ĐÚNG ba hằng số thật `CANCEL_RUN_COORDINATOR` (mới, task
này tạo)/`WorkspaceReconciliationJobKind` (`"WORKSPACE_RECONCILIATION"`, không phải `WORKSPACE_RECONCILE`
như doc cũ ghi nhầm)/`WorkspaceSetReleaseJobKind` (`"WORKSPACE_SET_RELEASE"`), loại HẲN `RECOVERY_REAPER`
khỏi allow-list — không phải placeholder cho tương lai, không mở rộng scope biến nó thành job thật — vì
`Store.RecoverExpiredJobs` (`internal/app/workerpool`) là một thao tác bảo trì TOÀN CỤC do ticker của
`Pool` gọi TRỰC TIẾP, không có row/lease/claim/RunID riêng trong `durable_jobs` nên JobClass/cancel_epoch
không có gì để gắn vào; biến nó thành job thật là một quyết định/task riêng trong tương lai, không phải
khả năng đã tồn tại ở Alpha. Khóa bằng test: đúng 3 kind trên phân loại CONTROL; `RECOVERY_REAPER`,
`WORKSPACE_RECONCILE` (chính tên lệch), kind lạ và mọi kind khác đều mặc định `RUN_WORK`; caller không có
field nào để tự khai `JobClass=CONTROL` (xác nhận: `EnqueueJobRequest` không có field `JobClass`); cancel
fence không ảnh hưởng 3 control job thật. Cần đồng bộ naming lệch không chỉ 2 chỗ mà TẤT CẢ chỗ normative
đang lệch — task này chỉ sửa `docs/design/06-v4-runtime-engine.md`'s own V4-12B section (nơi trực tiếp);
`docs/architecture/02-architecture-decisions.md`/`04-go-core-spec.md`/`01-system-design.md` để nguyên,
ghi chú lại đây làm known follow-up nếu có task doc-sync sau này.

**Tự phát hiện 3 vấn đề khi code (không phải câu hỏi mới):**
1. *Migration count off-by-one:* phát hiện file `0013_*.sql` đã bị THIẾU từ lịch sử trước (chỉ có
   0001-0012 rồi nhảy thẳng 0014) — nghĩa là SỐ FILE migration luôn kém SỐ HIỆU cao nhất đúng 1. Bump ban
   đầu `migrationCount != 21` lên `!= 23` (theo đúng số hiệu file mới `0023_cancel_run_coordinator.sql`)
   SAI ngay lần đầu chạy test — con số ĐÚNG là `22` (21 file cũ + 1 file mới, tính theo SỐ FILE thật, không
   theo số hiệu). Sửa lại `db_test.go`/`unitofwork_test.go` đúng `22`, verify pass.
2. *RUNNING structural node bị bỏ sót khỏi sweep, mắc kẹt vĩnh viễn:* thiết kế coordinator ban đầu chỉ
   sweep PENDING/READY/QUEUED/WAITING, loại HẲN RUNNING (lý do ban đầu: "Alpha không force-stop được
   Attempt thật đang chạy"). Viết test `TestCancelRun_CreatedRun_MovesToCancellingThenCancelled` phát hiện
   ngay: một node CẤU TRÚC (START/ROUTER/FORK/END, auto-advance) được gán RUNNING NGAY LÚC TẠO và KHÔNG
   BAO GIỜ có ExecutionAttempt thật đi kèm — đường tiến duy nhất của nó là một job `ADVANCE_RUN` mà
   `FenceAndCancelRunJobs` đã fence từ trước, nên nếu sweep bỏ qua nó, NodeRun đó mắc kẹt RUNNING vĩnh
   viễn, không ai đóng, Run không bao giờ tới CANCELLED. Sửa: sweep thêm RUNNING nhưng CHỈ khi NodeRun đó
   KHÔNG có ExecutionAttempt nào (phân biệt qua một tập NodeRunID-có-Attempt xây từ
   `ListExecutionAttemptsForRun`'s own kết quả ngay trong cùng transaction) — một AGENT node RUNNING có
   Attempt thật vẫn được bỏ qua nguyên vẹn như thiết kế gốc.
3. *Stale read giữa các lần lặp trong cùng transaction gây ErrOptimisticConflict giả:* sweep NodeRun ban
   đầu dùng MỘT snapshot duy nhất từ `ListNodeRunsForRun` cho cả vòng lặp. Viết test FORK+JOIN(ALL) phát
   hiện: khi branch thứ nhất bị cancel, `terminalizeBranchTokenForCancelledNodeRunTx`'s own `evaluateJoinTx`
   có thể NGAY LẬP TỨC CAS JOIN's own NodeRun sang FAILED (ALL-mode infeasible ngay khi có 1 token
   CANCELLED, không cần chờ branch còn lại) — nhưng vòng lặp bên ngoài vẫn cầm bản snapshot CŨ (WAITING,
   version cũ) của ĐÚNG NodeRun đó (JOIN có ActivationSequence thấp hơn "to_implement" vì compiler sort
   alphabet Outcomes khiến "shortcut" được xử lý trước ở fan-out), nên khi vòng lặp tới lượt xử lý entry
   đó, nó CAS nhầm `ExpectedVersion` đã lỗi thời → `ErrOptimisticConflict` làm SẬP TOÀN BỘ transaction
   sweep (mọi cancellation khác trong CÙNG sweep cũng bị rollback theo). Sửa: `GetNodeRun` lại (fresh)
   ngay trước mỗi lần quyết định/CAS trong vòng lặp, không bao giờ tin snapshot ban đầu của chính lần liệt
   kê đó.

**Phạm vi thực hiện:**
- **Migration `0023_cancel_run_coordinator.sql`**: `workflow_runs` thêm `cancel_epoch INTEGER`;
  `durable_jobs` thêm `run_id TEXT REFERENCES workflow_runs(id)`, `job_class TEXT NOT NULL DEFAULT
  'RUN_WORK' CHECK(...)` (constraint 2 chiều thật giữa `job_class` và `kind`, không chỉ enum rỗng — đúng
  yêu cầu "mapping tĩnh có constraint chứ không phải cột tự do"), `cancel_epoch INTEGER`; 3 index mới:
  `idx_durable_jobs_claim_run_work` (partial, `job_class='RUN_WORK' AND state='AVAILABLE' AND
  cancel_epoch IS NULL`), `idx_durable_jobs_claim_control` (partial, `job_class='CONTROL' AND
  state='AVAILABLE'`), `idx_durable_jobs_run_id`.
- **`internal/app/ports/job_class.go`** (mới): `JobClass`/`JobClassRunWork`/`JobClassControl`,
  `ClassifyJobKind` (pure function, allow-list hardcode 3 literal string để tránh ports phụ thuộc ngược
  app-layer), `ErrRunCancelling`.
- **`internal/app/ports/scheduling.go`**: `DurableJob` thêm `RunID`/`JobClass`/`CancelEpoch`;
  `EnqueueJobRequest` thêm `RunID` (KHÔNG thêm `JobClass` — server-derive duy nhất).
- **`internal/app/ports/unitofwork.go`**: `TransitionWorkflowRunStateRequest` thêm `NextCancelEpoch`;
  `RuntimeRepository` thêm `ListExecutionAttemptsForRun`, `TransitionRunCancellationIntentState`;
  `JobsRepository` thêm `FenceAndCancelRunJobs`.
- **`internal/app/ports/wait.go`/`approval.go`**: thêm `ListWaitRegistrationsForRun`/
  `ListApprovalRequestsForRun`.
- **`internal/domain/runtime/runtime.go`**: `WorkflowRun` thêm `CancelEpoch *uint64`.
- **sqlite** (`scheduling.go`, `advance_run.go`, `runtime_manifest.go`, `wait.go`, `approval.go`,
  `workflow_store.go`): `enqueueJobTx` tính `job_class` + fence "run not cancelling" (SELECT trước INSERT,
  cùng transaction); `ClaimJob`'s own WHERE thêm `(job_class='CONTROL' OR (job_class='RUN_WORK' AND
  cancel_epoch IS NULL))`; `FenceAndCancelRunJobs` (một UPDATE với CASE, fence + flip AVAILABLE→CANCELLED
  cùng lúc); `TransitionWorkflowRunState` ghi `cancel_epoch`/set `finished_at` khi CANCELLED;
  `TransitionRunCancellationIntentState`; `ListExecutionAttemptsForRun` (JOIN qua `node_runs`, vì
  `execution_attempts` không có cột `run_id` riêng); `ListWaitRegistrationsForRun`/
  `ListApprovalRequestsForRun`; `loadWorkflowRun` đọc thêm `cancel_epoch`.
- **fake** (`unitofwork.go`, `runtime.go`, `wait.go`, `approval.go`): `JobsRepository` thêm field
  `runtime *RuntimeRepository` (mirror `WorkRepository`'s own `catalog` cross-repo reference pattern, vì
  `EnqueueJob`'s own fence cần đọc `WorkflowRun.State`) + `cancelled map[string]bool` + `IsCancelled`/
  `FenceAndCancelRunJobs`; mọi method còn lại mirror sqlite.
- **`internal/app/runtime/completion.go`**: `reconcileRunTerminalityTx` tách nhánh CANCELLING riêng, gọi
  `reconcileCancellingRunTx` (yêu cầu CẢ `LiveCount==0` LẪN `BlockedCount==0` mới đóng CANCELLED — một
  BLOCKED lingering không bao giờ tự đóng Run, đúng "BLOCKED không được tự cancel Run" mirror từ V4-12's
  own "không được tự fail Run"); `transitionRunToCancelledTx` + event `RUN_CANCELLED` mới.
- **`internal/app/runtime/advance.go`**: `advanceRunTx` thêm guard "Run cancelling → reconcile rồi return,
  không tạo downstream NodeRun/job mới" — đặt SAU khi NodeRun hiện tại đã CAS SUCCEEDED (lịch sử thật vẫn
  đứng), TRƯỚC khi bắt đầu tạo NodeRun mới.
- **`internal/app/runtime/finalize.go`**: `decideRetryOrExhaustion` thêm điều kiện "Run cancelling → không
  tạo Attempt retry mới, rơi thẳng xuống nhánh FAILED sẵn có" (một dòng: `&& !runCancelling`); case mới
  `ExecutionAttemptCancelled` trong switch Step 6 + `decideCancelledOutcomeTx` (CAS NodeRun RUNNING→
  CANCELLED, terminalize BranchToken nếu có, reconcile); tách `terminalizeBranchTokenForCancelledNodeRunTx`
  dùng chung giữa đây và coordinator.
- **`internal/app/runtime/execute.go`**: `claimRunning` thêm chốt re-check #1 (trước QUEUED→RUNNING) —
  Run cancelling thì từ chối tiến, để lại cho coordinator; `Handle` thêm chốt re-check #2 (ngay trước gọi
  executor — "ProcessSupervisor.Start" analogue gần nhất hiện có) — Run cancelling thì finalize thẳng
  CANCELLED (`TerminationReasonRunCancelled`) thay vì bao giờ gọi executor.
- **`internal/app/runtime/cancel_run.go`** (mới): `CancelRun` command đầy đủ (record intent idempotent,
  CAS CANCELLING+CancelEpoch, `FenceAndCancelRunJobs`, enqueue `CANCEL_RUN_COORDINATOR`, event
  `RUN_CANCELLATION_REQUESTED`), `ErrRunAlreadyTerminal`.
- **`internal/app/runtime/cancel_run_coordinator.go`** (mới): `CancelRunCoordinatorHandler` — sweep WAIT/
  APPROVAL/QUEUED-Attempt/NodeRun (PENDING/READY/QUEUED/WAITING/RUNNING-không-Attempt, KHÔNG BAO GIỜ
  BLOCKED) trong một transaction, mark intent COMPLETED, reconcile.
- **`internal/app/runtime/event_schema.go`**: đăng ký `RUN_CANCELLATION_REQUESTED`/`RUN_CANCELLED` +
  golden fixture + round-trip test cho cả hai.

**Test (30 test mới — `internal/app/ports/job_class_test.go`, `internal/adapters/sqlite/cancel_run_test.go`,
`internal/app/runtime/cancel_run_test.go`, `cancel_run_race_test.go`):** xem "Đã triển khai" trong
`docs/design/06-v4-runtime-engine.md`'s own V4-12B section để có danh sách đầy đủ — tóm tắt: 2 test
`ClassifyJobKind`; 9 test sqlite (CHECK constraint 2 chiều, EnqueueJob tính đúng job_class, enqueue fence 2
chiều — cancelling bị từ chối/non-cancelling được chấp nhận với 5 state, claim CAS split thật, fence LEASED
sống sót qua recovery vẫn không claim lại được, restart schema); 13 test app-flow (CREATED, already-terminal,
duplicate, WAIT, APPROVAL, QUEUED-attempt StartedAt rỗng, FORK+JOIN, no-new-work-after-intent, claim-vs-cancel
2 thứ tự, BLOCK-không-no-op, REWORK-không-no-op); 8 test race (attempt-success 2 thứ tự, END 2 thứ tự,
CompletionPolicy-PASS giả lập 2 thứ tự, stale-finalize 2 dạng).

**Verify (chạy local):**
```
go build ./...                     # sạch
go vet ./...                       # sạch
go test ./... -count=1             # toàn bộ package PASS (kể cả 5 package sqlite/spikeacceptance chậm nhất)
go run ./cmd/docs-coverage-check   # debt = 0
gofmt -l <30 file .go đổi/mới>     # rỗng sau gofmt -w
```

**Kết quả:** PR [#24](https://github.com/draculemihawk123-ai/agent-workflow/pull/24), CI 6/6 xanh ngay lần
đầu (kể cả job "Linux race and stability" 10m32s và "cross-platform semantic diff (SPK-13)" — job vừa được
hotfix riêng ở PR #23, xanh sạch không tái diễn). Squash-merge vào master tại `ef19dbf`. V4-12B DONE — tiếp
tục thẳng sang V4-12C theo đúng lệnh tự động trước đó.

## V4-12A — Scope amendment và NodeRun reactivation

**Bối cảnh:** tiếp tục thẳng sau khi V4-12 merge (1939ea4), theo đúng lệnh "tự động sang task tiếp theo".
Đọc task text ("áp scope expansion đã duyệt mà không sửa quyền lịch sử hoặc mở quyền cho sibling; Attempt
hiện tại kết thúc BLOCKED với SCOPE_EXPANSION_REQUIRED; sau provision append amendment, tạo activation mới
với ReactivationReason=SCOPE_EXPANDED") rồi đọc thật code trước khi hỏi: `runtimedomain.ExecutionAttemptBlocked`
(V4-01) đã có trong enum từ đầu nhưng CHƯA từng có producer thật nào; V3-08 (`internal/app/work/
scope_expansion.go`) đã có đầy đủ bốn command `RequestScopeExpansion`/`ApproveScopeExpansion`/
`RejectScopeExpansion`/`WithdrawScopeExpansion` nhưng comment của chính nó ghi rõ "no runtime engine exists
yet in this codebase for it to touch" — nghĩa là toàn bộ cầu nối từ một Attempt đang chạy tới các command đó,
và ngược lại từ approval về một NodeRun cụ thể, chưa hề tồn tại. Phát hiện 2 fork thiết kế thật sự trước khi
code: cơ chế nào để một Attempt thực sự raise được request thật (không chỉ đổi state), và cơ chế nào để
V4-12A biết khi nào request đã approved VÀ workspace đã provision xong để mới reactivate. Đã hỏi user cả 2.

**Câu hỏi 1 — cơ chế raise request thật và giữ đúng liên kết:** đề xuất "widen `NodeExecutionResult` +
job riêng gọi `work.RequestScopeExpansion`" được chọn về HƯỚNG, nhưng user sửa một chi tiết chốt tôi chưa
nghĩ đủ sâu: `ScopeExpansionRequestID` phải được **RESERVE ngay trong transaction finalize BLOCKED**, không
được backfill sau khi handler tạo request xong. Lý do user đưa ra là một race/crash-window cụ thể:
"`RequestScopeExpansion` tạo request thành công. Process crash trước khi ghi RequestID vào runtime link.
Operator có thể approve request mà V4-12A chưa tìm được Attempt nguồn." User chốt luôn state/field matrix
chặt tôi chưa đề xuất: `NodeExecutionResult.RequestedScopeExpansion` chỉ hợp lệ khi
`State=BLOCKED`+`TerminationReason=SCOPE_EXPANSION_REQUIRED`; SUCCEEDED chỉ có outcome, FAILED chỉ có
ErrorCode, sai hình dạng ở bất kỳ state nào → `OUTCOME_REJECTED`, KHÔNG BAO GIỜ tạo scope request từ một
proposal dị dạng. Transaction finalize (theo đúng thứ tự user khoá) phải: validate JobLease/WriteLease/cancel
fence như bình thường, validate + canonicalize proposal, RESERVE RequestID, tạo row liên kết durable
(`attempt_scope_expansion_origins`: attempt_id PK, request_id UNIQUE NOT NULL, proposal_hash,
reactivated_node_run_id UNIQUE NULL), CAS Attempt rồi NodeRun sang BLOCKED, ghi TerminationReason, enqueue
job `REQUEST_SCOPE_EXPANSION` (`JobClass=RUN_WORK`, `IdempotencyKey=scope-expansion:<attempt-id>`), append
event, complete job EXECUTE_NODE cũ — và nhấn mạnh dứt khoát "**BLOCKED tuyệt đối không đi qua retry
policy**" (không được lẫn với AttemptPolicy retry budget của FAILED). Handler của job đó gọi
`work.RequestScopeExpansion` THẬT trong transaction RIÊNG của chính nó, truyền đúng RequestID đã reserve
(không tự mint mới) — actor phải là một system actor như `system:runtime` (không phải identity của executor,
vì executor chỉ ĐỀ XUẤT chứ không tự authenticate; cũng không phải một operator thật, vì không ai quyết
định raise request này — chính Attempt BLOCKED tự làm); scope lấy từ Run (không phải từ executor);
`ReferencedWorkItemID` lấy từ Run; idempotency/hash tất định từ chính origin row. User nhấn mạnh thêm:
"Public API không nên cho client tùy ý chọn RequestID; chỉ internal runtime path được truyền reserved ID" —
nên `RequestScopeExpansionRequest.RequestID` phải là optional, để trống ở MỌI caller công khai/UI (giữ
nguyên hành vi tự mint cũ), chỉ runtime path nội bộ mới truyền non-blank. Trên crash giữa lúc request đã
tạo và job chưa complete: replay với cùng idempotency key trả về đúng cùng request, không tạo request thứ
hai, không mất liên kết. Cho reactivation sau approval, user chốt luôn: tìm origin theo RequestID, kiểm tra
Attempt/NodeRun gốc còn đúng BLOCKED, kiểm tra Run chưa CANCELLING/terminal, append RunManifestAmendment,
tạo activation NodeRun mới, **copy NodeKey và Iteration, không tăng cycle budget**, **nếu node nằm trong
FORK branch thì copy luôn BranchTokenID, nếu không JOIN sẽ chờ sai branch mãi mãi**, pin scope/manifest
revision mới, set ReactivationReason=SCOPE_EXPANDED, CAS `reactivated_node_run_id` để hai lần approve/
reconcile trùng nhau chỉ bao giờ tạo đúng MỘT activation. Request bị reject/withdraw thì KHÔNG reactivate
NodeRun — Run vẫn ở trạng thái BLOCKED cho tới khi operator quyết định khác. User từ chối dứt khoát phương
án hẹp hơn tôi đưa ra ("chỉ build phần reactivate, không build đường raise request thật") — lý do: "để
thiếu chính đường tạo request mà ADR-002 đã yêu cầu và không thể kiểm thử flow end-to-end của V4-12A."

**Câu hỏi 2 — V4-12A biết khi nào request đã approved VÀ đã provision xong bằng cách nào:** user chọn
hướng job tự lên lịch lại (self-rescheduling reconcile job) nhưng sửa một điểm tôi đề xuất sai: KHÔNG được
polling ngay từ lúc request còn PENDING chờ người duyệt — job reconcile đầu tiên chỉ được enqueue KHI ĐÃ
approved, vì "`WAIT_TIMER`/`APPROVAL_TIMER` (V4-08/V4-09) hiện chỉ chạy một lần tại `DueAt`; chúng không
phải tiền lệ chính xác cho polling lặp" (một job tự lên lịch lại nhiều lần là một pattern MỚI so với hai
tiền lệ đó, không được lẫn lộn với chúng). Flow đã khoá: `ApproveScopeExpansion` (V3-08, KHÔNG sửa hành vi
cũ — bump ScopeVersion/ghi grant/enqueue WORKSPACE_PROVISION giữ nguyên) trong CÙNG transaction, thêm bước
enqueue ĐÚNG MỘT job `SCOPE_EXPANSION_RECONCILE` **khi và chỉ khi** request có origin runtime thật (một
request tạo qua UI/thủ công không có origin — `ErrPersistenceNotFound` là trường hợp bình thường, không
phải lỗi). Handler mỗi lần claim đọc state authoritative thay vì tin payload cũ: request PENDING là nhánh
phòng thủ/không thể xảy ra trong thực tế (no-op, vì approve luôn enqueue job mới chỉ sau khi đã quyết);
REJECTED/WITHDRAWN → origin chuyển REJECTED, không bao giờ reactivate; APPROVED + WorkspaceSet
REQUESTED/PROVISIONING → enqueue successor với backoff tăng dần có trần rồi complete job hiện tại; APPROVED
+ WorkspaceSet READY → user nhấn mạnh còn phải verify `BaseRevisionSet` THỰC SỰ phủ MỌI repository đã
request (không được tin `State=READY` một mình, vì đó có thể là một snapshot cũ/thiếu tính từ TRƯỚC đúng
approval này) rồi mới append amendment + tạo activation mới; WorkspaceSet BLOCKED/RELEASING/RELEASED →
không polling vô hạn, đánh dấu origin NEEDS_RECOVERY và dừng hẳn, để operator xử lý. Kỷ luật job user
chốt: `SCOPE_EXPANSION_RECONCILE` phải là `RUN_WORK` (không phải `CONTROL`) vì bản thân nó có thể tạo
activation mới nên phải chịu cancel fence (khái niệm V4-12B, chưa xây nhưng phải tôn trọng layering trước);
enqueue successor và complete job hiện tại phải nằm trong CÙNG một transaction, dùng
`IdempotencyKey=scope-expansion-reconcile:<attempt-id>:<generation>` với CAS trên một field
`poll_generation` để duplicate delivery không bao giờ mint được hai successor; backoff phải tăng dần có
trần, không hot loop. User từ chối dứt khoát phương án hẹp hơn tôi đưa ra ("chỉ hỗ trợ trường hợp KHÔNG cần
provision repository mới, tức chỉ nâng quyền trên repo đã có") — lý do: "nó để hở chính trường hợp quan
trọng nhất của scope expansion: thêm repository mới."

**Phạm vi thực hiện (sau khi khoá xong cả 2 câu hỏi):**
- **`internal/domain/runtime/scope_expansion.go`** (mới): `ScopeGrantProposal`/`ScopeExpansionProposal`
  (`Validate` — Reason bắt buộc, ≥1 grant, RepositoryID/Access hợp lệ, không trùng repository;
  `CanonicalJSON` — sort theo RepositoryID để hai proposal giống nhau về nội dung nhưng khác thứ tự slice
  luôn hash giống hệt nhau); `ScopeExpansionReconcileStatus` (PENDING/REACTIVATED/REJECTED/NEEDS_RECOVERY);
  `ScopeExpansionOrigin`/`NewScopeExpansionOrigin` (luôn khởi tạo PENDING/pollGeneration=0, tính
  `ProposalHash` bằng sha256 trên CanonicalJSON ngay lúc construct — không tính lại ở tầng application).
- **Migration `0022_scope_expansion_reactivation.sql`**: `attempt_scope_expansion_origins` (attempt_id PK
  REFERENCES execution_attempts, node_run_id/run_id/work_item_id/family_id, request_id UNIQUE NOT NULL,
  proposal_json/proposal_hash, reactivated_node_run_id UNIQUE NULL, reconcile_status CHECK 4 giá trị DEFAULT
  'PENDING', poll_generation DEFAULT 0, version) + index trên run_id; `ALTER TABLE node_runs ADD COLUMN
  reactivation_reason TEXT NOT NULL DEFAULT ''`. Bump migration count 21→22 (`db_test.go`,
  `unitofwork_test.go`, cả hai file test cùng hardcode con số này, giống mọi migration trước).
- **`ports.RuntimeRepository`** thêm 4 method: `CreateScopeExpansionOrigin`,
  `GetScopeExpansionOriginByAttemptID`, `GetScopeExpansionOriginByRequestID`, `TransitionScopeExpansionOrigin`
  (fenced CAS, `NextReconcileStatus`/`NextPollGeneration`/`NextReactivatedNodeRunID` optional) — sqlite
  (`scope_expansion_origin.go`, mới, dùng chung một helper `loadScopeExpansionOrigin` cho cả hai cách tra
  cứu theo cột khác nhau) và fake (`ports/fake/runtime.go`, thêm map `scopeOrigins` keyed theo AttemptID +
  wiring vào `clone()`) implement cả bốn.
- **`ports.NodeExecutionResult`** thêm `RequestedScopeExpansion *runtime.ScopeExpansionProposal` — mutually
  exclusive với SelectedOutcome/ErrorCode theo đúng state/field matrix đã khoá.
- **`internal/app/runtime/finalize.go`**: `isFinalizableExecutionAttemptState` giờ chấp nhận
  `ExecutionAttemptBlocked` (lần đầu tiên state này có đường thật để reach). Validation tầng finalize (thêm
  MỚI, không chỉ dựa vào domain-level `Validate()`): BLOCKED bắt buộc
  `TerminationReason=SCOPE_EXPANSION_REQUIRED` + proposal hợp lệ; MỌI state khác cấm hẳn
  `RequestedScopeExpansion` non-nil. `FinalizeExecutionAttemptResult` thêm `ScopeExpansionRequested bool`/
  `ScopeExpansionRequestID`/`ScopeExpansionJobID` để caller (và test) quan sát được. Hàm mới
  `requestScopeExpansionTx`: CAS NodeRun RUNNING→BLOCKED (không đụng BranchToken — token vẫn ACTIVE, vì
  Alpha không cancel branch), reserve `requestID := ids.NewID()` NGAY trong transaction này, build+persist
  `ScopeExpansionOrigin`, enqueue job `REQUEST_SCOPE_EXPANSION` (`IdempotencyKey="scope-expansion:"+attemptID`),
  set các field kết quả, rồi gọi `reconcileRunTerminalityTx` (reducer chung của V4-12) ở cuối — vì BLOCKED
  loại bỏ live activation cuối giống hệt FAILED, Run có thể cần biết "không còn gì sống" (dù không bao giờ
  tự FAILED chỉ vì còn BLOCKED, đúng luật đã khoá ở V4-12).
- **`internal/app/runtime/execute.go`**: `ExecuteNodeHandler.Handle`'s own logic dịch `NodeExecutionResult`
  sang `FinalizeExecutionAttemptRequest` được tái cấu trúc thành `switch execResult.State` với case mới
  `ExecutionAttemptBlocked` — validate `RequestedScopeExpansion`, sai hình dạng → FAILED/
  `TerminationReasonOutcomeRejected`/`errorcode.CodeValidationFailed`, hợp lệ → BLOCKED/
  `TerminationReasonScopeExpansionRequired`, threading `scopeProposal` vào request. **Tự phát hiện một
  thiếu sót khi code** (không phải câu hỏi mới): ban đầu quên wire case này vào `execute.go` — chỉ phát
  hiện khi double-check `fake.NodeExecutor` có hỗ trợ trả về BLOCKED hay không (nó có, chỉ echo lại
  `Result` được set sẵn) nhưng `execute.go` chính nó thì chưa hề được sửa để dịch state đó.
- **`internal/app/work/scope_expansion.go`**: `ScopeExpansionReconcileJobKind = "SCOPE_EXPANSION_RECONCILE"`
  (định nghĩa TẠI ĐÂY vì package này là nơi enqueue đầu tiên, giống tiền lệ `WorkspaceProvisionJobKind`),
  `ScopeExpansionReconcileJobPayload{AttemptID, PollGeneration}`. `RequestScopeExpansionRequest` thêm
  `RequestID` (optional, chỉ runtime path nội bộ truyền — public/UI luôn để trống, tự mint như cũ).
  `ApproveScopeExpansion` — SAU bước CAS request PENDING→APPROVED cũ, thêm: tra
  `GetScopeExpansionOriginByRequestID`, nếu tồn tại thì enqueue đúng một job `SCOPE_EXPANSION_RECONCILE`
  (`IdempotencyKey=scope-expansion-reconcile:<attempt-id>:<origin.PollGeneration>`), `ErrPersistenceNotFound`
  là no-op bình thường (request không có nguồn runtime).
- **`internal/app/runtime/scope_expansion.go`** (mới, file lõi của phần app layer):
  `RequestScopeExpansionHandler` (đọc origin read-only, build `[]work.ScopeGrantRequest` từ
  `origin.Proposal.RequestedGrants`, gọi `work.RequestScopeExpansion` thật với actor `system:runtime`,
  RequestID=origin.RequestID, RequestHash=origin.ProposalHash); `ScopeExpansionReconcileHandler` (state
  machine đầy đủ đã khoá ở câu hỏi 2: PENDING request no-op phòng thủ, REJECTED/WITHDRAWN → origin
  REJECTED, APPROVED+WorkspaceSet REQUESTED/PROVISIONING → successor, APPROVED+READY+phủ đủ grant →
  reactivate, WorkspaceSet terminal khác → NEEDS_RECOVERY); `workspaceSetCoversEveryGrant` (kiểm tra
  `BaseRevisionSet` có Revision cho MỌI grant đã request, không tin `State` một mình);
  `enqueueScopeExpansionReconcileSuccessorTx` (bump `PollGeneration` bằng CAS fenced, backoff 5s base/300s
  cap tăng dần theo generation); `reactivateBlockedNodeRunTx` (append RunManifestAmendment, tạo activation
  mới bằng `runtimedomain.NewNodeRun` với NodeKey/Iteration copy từ activation BLOCKED gốc, set
  `.BranchTokenID`/`.ReactivationReason="SCOPE_EXPANDED"` sau construct, copy DecisionArtifact profile
  sang ID mới — `ExecuteNodeHandler`'s own `loadExecutionProfile` tra theo NodeRunID nên activation mới
  cần bản sao riêng, tolerate `ErrPersistenceNotFound` nếu profile gốc chưa từng ghi — CAS
  `reactivated_node_run_id`, rồi enqueue job `ScheduleNodeRunJobKind` để activation mới đi qua ĐÚNG pipeline
  `ScheduleExecutableNodeRun` sẵn có thay vì tự resolve EffectiveScope/ManifestRevision/ExecutionProfileHash
  một lần nữa ở đây).
- **`internal/app/runtime/finalize_retry_test.go`**: một test cũ
  (`TestFinalizeExecutionAttempt_BlockedState_RejectedNeverConsumesRetryBudget`) giả định gọi
  `FinalizeExecutionAttempt` với `NextState=Blocked, TerminationReason=ExecutionFailed` (sai reason) sẽ bị
  `ErrUnsupportedFinalizeState` — giả định này SAI sau khi `isFinalizableExecutionAttemptState` được mở
  rộng chấp nhận BLOCKED (giờ lỗi tới từ validation state/field matrix MỚI, cụ thể hơn, không phải blanket
  rejection cũ nữa). Đổi tên thành `..._MismatchedReasonRejectedNeverConsumesRetryBudget`, assertion đổi
  sang generic "phải reject" thay vì check đúng sentinel error cũ, xoá import `"errors"` không còn dùng.

**Test (fake + sqlite, `scope_expansion_test.go`/`scope_expansion_sqlite_test.go`, 6 test):**
`TestFinalizeExecutionAttempt_Blocked_CreatesOriginAndRequestsScopeExpansion` (base transition — Attempt/
NodeRun BLOCKED, ĐÚNG MỘT Attempt tồn tại — BLOCKED không bao giờ spawn attempt retry thứ hai, origin PENDING
với RequestID đã reserve non-empty);
`TestScopeExpansion_EndToEnd_NewRepositoryProvisionedReactivatesNodeRun` (test trung tâm — chạy TOÀN BỘ
pipeline THẬT không mock bất kỳ đâu: BLOCKED → `RequestScopeExpansionHandler` thật tạo `work.
ScopeExpansionRequest` thật dưới đúng RequestID reserved → `work.ApproveScopeExpansion` thật cho một
repository HOÀN TOÀN MỚI [repo-2] → `workspaceprovision.Handler` (V3-06) THẬT, không mock, provision repo-2
thật sự → reconcile job tự reschedule đúng một lần khi WorkspaceSet còn PROVISIONING [verify PollGeneration
tăng lên 1, chưa reactivate] → reconcile job thứ hai reactivate khi WorkspaceSet đã READY thật; assert
activation mới có ReactivationReason=SCOPE_EXPANDED, NodeKey/Iteration giống hệt activation gốc, family giờ
có RepositoryScope cho repo-2, Attempt/NodeRun gốc vẫn BLOCKED vĩnh viễn, job ScheduleNodeRunJobKind đã
enqueue cho activation mới, RunManifestAmendment đúng Revision=1/ApprovedScopeVersion=2);
`TestScopeExpansionReconcile_DuplicateDelivery_OnlyOneReactivation` (redeliver đúng job reconcile generation
đã REACTIVATED lần thứ hai — origin/node run count không đổi, chứng minh CAS `reactivated_node_run_id`
chặn đúng);
`TestScopeExpansionReconcile_Rejected_NeverReactivates` (`work.RejectScopeExpansion` thật → origin REJECTED
→ Attempt/NodeRun BLOCKED vĩnh viễn, không bao giờ có activation mới);
`TestScopeExpansion_SiblingNodeRunsNeverTouchedByReactivation` (so sánh snapshot MỌI NodeRun trước/sau
reactivation — không NodeRun nào đã tồn tại từ trước [START, activation BLOCKED gốc] bị đụng, chỉ đúng một
activation mới xuất hiện, BranchTokenID so sánh THEO GIÁ TRỊ chứ không theo con trỏ vì fake repository
`clone()` mỗi lần round-trip sẽ tạo pointer mới dù giá trị logic giống hệt — tự phát hiện khi viết test,
sửa bằng helper `sameBranchToken` dereferencing thay vì so sánh `!=` trực tiếp trên hai con trỏ);
`TestFinalizeExecutionAttempt_SQLite_Blocked_OriginSurvivesRestart` (đóng/mở lại `sqlite.Store` thật ngay
sau khi finalize BLOCKED commit, verify cả `GetScopeExpansionOriginByAttemptID` lẫn
`GetScopeExpansionOriginByRequestID` đọc đúng row sau restart, kể cả `Proposal.RequestedGrants` deserialize
đúng, Attempt/NodeRun vẫn BLOCKED).

**Verify (chạy local):**
```
go build ./...                     # sạch
go vet ./...                       # sạch
go test ./... -count=1             # toàn bộ package PASS (kể cả 5 package sqlite chậm nhất)
go run ./cmd/docs-coverage-check   # debt = 0
gofmt -l <file .go đổi/mới>        # rỗng sau gofmt -w (execution.go/fake/runtime.go/unitofwork.go/
                                    # execute.go/finalize.go/finalize_retry_test.go/scope_expansion.go
                                    # cần gofmt -w — CRLF quen thuộc trên Windows từ Edit tool)
```

**Kết quả:** PR [#22](https://github.com/draculemihawk123-ai/agent-workflow/pull/22). CI vòng đầu (trước
khi rebase) chặn bởi lỗi gate SPK-13/SPK-10 không liên quan diff (xem mục "Hotfix" phía trên) — tách hotfix
riêng ở PR #23, merge (2556671), rebase PR #22 lên master mới (fast-forward sạch, không conflict), verify
local lại toàn bộ (xanh), CI chạy lại 6/6 xanh ngay lần đầu. Squash-merge vào master tại `6728110`. V4-12A
DONE — tiếp tục thẳng sang V4-12B theo đúng lệnh tự động trước đó.

## V4-12 — Run completion candidate và WorkItem projection proposal

**Bối cảnh:** tiếp tục thẳng sau khi V4-11 merge (09a74c2), theo đúng lệnh "tự động sang task tiếp theo".
Đọc task text ("END chuyển run sang VERIFYING, chưa tự SUCCEEDED hoặc DONE WorkItem; terminal path
validation, run VERIFYING|FAILED, emit completion-request event") — mơ hồ ngay từ đầu: "run VERIFYING|FAILED"
có thể đọc theo 2 hướng rất khác nhau về độ lớn công việc. Đọc kỹ ADR-011/ADR-021 (
`docs/architecture/02-architecture-decisions.md`) và V5-11 CompletionPolicy service
(`docs/design/07-v5-execution-evidence.md`) xác nhận: PASS/REWORK/BLOCK/FAIL từ VERIFYING hoàn toàn thuộc
V5-11 (chưa xây, phụ thuộc V4-12), KHÔNG phải việc của task này. Nhưng grep code V4-06/V4-11 đã merge lộ ra
2 comment forward-reference rất rõ: `decideRetryOrExhaustion` (finalize.go) — "never an automatic
WorkflowRun failure (V4-12's own scope)"; `evaluateJoinTx` (advance.go) — "Run-level failure aggregation
stays V4-12's own scope". Đây là fork thiết kế thật sự về QUY MÔ task (END-only dispatch nhỏ, hay
Run-level failure aggregation lớn phản ứng với thất bại ở bất kỳ đâu trong graph) — đã hỏi user trước khi
code thay vì tự đoán.

**Câu hỏi 1 — "run VERIFYING|FAILED" là gì, aggregation thật hay chỉ END dispatch:** user xác nhận
**aggregation thật** ("Đây là yêu cầu thật, không chỉ là chú thích dự định" — bằng chứng: V4-06/V4-11 đều
chủ động dừng ở NodeRun FAILED và giao việc tổng hợp cho V4-12; nếu không xử lý, một node hết retry budget
sẽ để Run mắc vĩnh viễn ở RUNNING dù không còn job hay đường tiến nào). Nhưng user SỬA SÂU đề xuất ban đầu
của tôi (tôi chỉ định gọi aggregation ngay tại điểm NodeRun chuyển FAILED) ở 3 điểm quan trọng:
(a) **Không chỉ gọi lúc NodeRun FAILED.** Ví dụ cụ thể user đưa ra: JOIN fail sớm trong khi một branch khác
còn chạy — lúc đó Run chưa được fail (đúng); nhưng khi branch cuối đó SAU NÀY tự hoàn tất (dù SUCCEEDED),
JOIN đã quyết định rồi nên không tạo downstream nào cả — nếu aggregation chỉ chạy đúng một lần lúc JOIN
fail ban đầu, Run sẽ mắc kẹt RUNNING mãi mãi dù giờ đã hết việc thật. Cần một helper DÙNG CHUNG
(`reconcileRunTerminalityTx`) chạy sau MỌI transaction có thể loại bỏ live activation cuối mà không chắc
chắn tạo downstream: NodeRun retry exhausted/non-retryable → FAILED; JOIN → FAILED; branch cuối hoàn tất
sau khi JOIN đã fail; WAIT/APPROVAL terminal; END dispatch — và LUÔN chạy SAU khi downstream activation/job
(nếu có) đã được tạo trong cùng transaction, để không quan sát nhầm một trạng thái "tạm thời không còn node
sống" giữa hai bước.
(b) **BLOCKED không phải non-terminal cũng không phải failure.** User chỉ ra activation BLOCKED cũ không
được hồi sinh (RetryBlockedActivation tạo activation MỚI); nên phân loại NodeRun state thành 3 nhóm riêng:
Live (PENDING/READY/QUEUED/RUNNING/WAITING), Recoverable-stalled (BLOCKED), Terminal (SUCCEEDED/FAILED/
SKIPPED/CANCELLED). Nếu chỉ còn BLOCKED, KHÔNG được đổi Run thành FAILED — blocker/retry/cancel protocol
(V4-12A/V4-12B, chưa xây) sở hữu riêng case đó.
(c) **END không có "hai outcome bình thường".** User khoá hẳn priority order của reducer: Run đã
CANCELLING/terminal/VERIFYING → no-op; có live NodeRun → giữ nguyên Run; không live nhưng có BLOCKED →
không fail, blocker authority xử lý; END hợp lệ đã đạt → VERIFYING + `RUN_COMPLETION_REQUESTED`; không có
live/BLOCKED/END nhưng có NodeRun hoặc JOIN FAILED → FAILED + `RUN_FAILED` (reason `RUN_FAILED`); không có
gì trong tất cả những điều trên (terminal path bị corrupt/vi phạm invariant) → FAILED + `RUN_FAILED`
(reason riêng `TERMINAL_PATH_INVALID`, nhánh phòng thủ fail-closed, không phải business outcome của END).

**Câu hỏi 2 — điều kiện chính xác "Run không thể tiến thêm":** tôi đề xuất "zero NodeRun còn non-terminal",
user CHỌN đúng hướng (không cần graph reachability đầy đủ) nhưng sửa lại LÝ DO và mô tả chính xác hơn:
compiled graph chỉ biểu diễn đường đi CÓ THỂ xảy ra — publish-time validation đã đảm bảo mọi node có đường
LÝ THUYẾT tới END, nhưng một node cụ thể đã fail và không có outcome để route thì đường lý thuyết đó không
giúp Run tiến tiếp THẬT — dùng graph reachability sẽ dễ kết luận sai rằng Run vẫn còn khả năng chạy. Điều
kiện khoá: Run đang RUNNING/WAITING AND không có NodeRun live AND không có NodeRun BLOCKED cần
operator/recovery xử lý AND chưa có END completion candidate hợp lệ AND tồn tại failure/dead-end
authoritative (NodeRun FAILED, JOIN FAILED hoặc terminal-path invariant vi phạm) → Run FAILED. Cần query
theo NHÓM (`RunNodeStateSummary{LiveCount, BlockedCount, FailedCount, ReachedEndNodeRunID}`), không phải
một boolean mơ hồ như tôi đề xuất ban đầu.

**Tự phát hiện + sửa một BUG THẬT của V4-11 đã merge (không phải câu hỏi, phát hiện khi viết test cho task
này):** `dispatchForkBranches`'s own zero-hop "branch's own first edge đã là JOIN" case (V4-10, gọi
`evaluateJoinTx` từ V4-11 tôi tự thêm) đánh giá JOIN's own policy dựa trên token set TẠI THỜI ĐIỂM ĐANG XỬ
LÝ trong một vòng lặp SINGLE-PASS — nhưng `workflow.Compile` CANONICALIZE (sort alphabetically)
`Node.Outcomes` (`compiler.go:108`), nên thứ tự `forkNode.Outcomes` sau compile KHÔNG PHẢI thứ tự authoring
gốc. Viết test cho fork có 1 branch zero-hop-tới-join ("shortcut") và 1 branch AGENT thật ("to_implement")
dưới ALL mode phát hiện: sau sort, "shortcut" < "to_implement" alphabetically nên được dispatch TRƯỚC —
tại thời điểm đó "to_implement"'s own BranchToken CHƯA được tạo (vòng lặp chưa tới lượt nó), nên
`ListBranchTokensForFork` chỉ thấy 1 token (shortcut, SUCCEEDED) → ALL mode tính sai S=1/N=1 (thay vì đúng
N=2) → JOIN quyết SUCCEEDED sớm và route thẳng tới END, dù "to_implement" còn chưa hề chạy. Bug này ĐÃ TỒN
TẠI trong V4-11 (PR #20, đã merge), chỉ chưa bị test nào bắt được vì V4-11's own test dùng
`joinPolicyDocument` (không có branch zero-hop nào cùng tồn tại với branch thật). Sửa: tách
`dispatchForkBranches` thành HAI PASS rõ ràng — pass 1 tạo MỌI BranchToken của MỌI branch trước (không
đánh giá/dispatch gì cả), pass 2 mới dispatch (và với zero-hop case, evaluate JOIN) từng branch — đúng
nghĩa đen HE-14-M09's own "create tokens atomically": không bao giờ có evaluation nào chạy trên một token
set chưa đầy đủ nữa, bất kể thứ tự compile sắp xếp Outcomes thế nào.

**Phạm vi thực hiện (sau khi khoá xong cả 2 câu hỏi + sửa xong bug V4-11):**
- **`internal/app/runtime/completion.go`** (mới, trái tim của task): `RunNodeStateSummary` (LiveCount/
  BlockedCount/FailedCount/ReachedEndNodeRunID/ReachedEndNodeKey), `computeRunNodeStateSummary` (đọc
  `ListNodeRunsForRun` + phân loại theo NodeRunState + nhận diện END-type node qua document), hằng số
  `RunFailureReasonRunFailed`/`RunFailureReasonTerminalPathInvalid`, `reconcileRunTerminalityTx` (reducer
  chung implement đúng priority order đã khoá), event mới `RUN_COMPLETION_REQUESTED`/`RUN_FAILED`
  (registered `eventschema` ngay, golden fixture, round-trip test).
- **`internal/app/runtime/advance.go`**: `isEndNode := finalTargetNode.Type == workflow.NodeEnd` thêm vào
  chuỗi if/else gán State (END → SUCCEEDED ngay, giống FORK — không cần case riêng trong dispatch switch,
  vì reconciler tail call generic đã đủ xử lý mọi node type). Reconciler được gọi ở CẢ HAI exit point của
  `advanceRunTx`: cuối dispatch switch chính, và cuối block JOIN-arrival early-return. Sửa bug V4-11: tách
  `dispatchForkBranches` thành `forkBranchPlan` (pass 1) + dispatch loop (pass 2).
- **`internal/app/runtime/finalize.go`**: `decideRetryOrExhaustion` giờ load `document` UNCONDITIONALLY
  (trước đây chỉ load khi có BranchTokenID) vì reconciler cần nó luôn; gọi `reconcileRunTerminalityTx` ở
  cuối hàm, sau khi NODE_RUN_FAILED event đã append.
- **`ports.RuntimeRepository`** thêm `ListNodeRunsForRun(ctx, runID)` (mọi NodeRun của một Run, không lọc
  theo state — phân loại là việc của Go code, không phải SQL WHERE) và `TransitionWorkflowRunState`
  (fenced CAS trên `WorkflowRun.State`, set `finished_at` khi FAILED, không set khi VERIFYING vì đó chưa
  phải terminal thật — ADR-011's own "chỉ là completion candidate") — sqlite + fake implement.

**Test (fake + sqlite + white-box nội bộ, 9 test tổng cộng):**
- `completion_test.go` (`package runtime_test`): END reached → Run VERIFYING + `RUN_COMPLETION_REQUESTED`
  đúng payload, WorkItem KHÔNG đổi (vẫn ACTIVE) — đúng test "agent proposed done/no evidence" Verify line
  yêu cầu (AK-ARCH-005/GC-INV-12); NodeRun FAILED không còn live nào khác (dạng thường, dùng
  `scheduledExecutionFixture`) → Run FAILED; NodeRun FAILED qua FORK branch, cũng không còn live nào khác
  → Run FAILED (case tương tự nhưng qua đường FORK); branch A fail trong khi branch B còn ACTIVE (dùng
  `joinPolicyDocument` từ V4-11's own test file) → Run vẫn RUNNING ngay sau đó, chỉ chuyển FAILED khi
  branch B SAU ĐÓ tự terminate thật (dù SUCCEEDED) — chứng minh chính xác điểm (a) đã khoá ở câu hỏi 1;
  BLOCKED sibling (seed trực tiếp qua `TransitionNodeRun`, chưa có producer thật) không bao giờ tự fail Run
  dù là NodeRun duy nhất còn lại — đúng điểm (b).
- `completion_sqlite_test.go`: đóng/mở lại `sqlite.Store` thật ngay sau khi Run vừa chuyển VERIFYING, verify
  cả `WorkflowRun.State` lẫn END NodeRun lẫn WorkItem status (chưa từng DONE) đều đúng sau restart.
- `completion_internal_test.go` (`package runtime`, mirror tiền lệ `event_schema_test.go`'s own white-box
  test — cần vì 2 case này không có cách nào trigger qua public API hôm nay): nhánh `TERMINAL_PATH_INVALID`
  (seed một NodeRun CANCELLED trực tiếp — chưa có producer thật nào cho state này, V4-12B's own scope —
  rồi gọi thẳng `reconcileRunTerminalityTx`, verify FAILED với đúng reason); và "Run đã VERIFYING rồi thì
  reducer không bao giờ re-fire" (no-op, không event mới, version không đổi).

**Verify (chạy local):**
```
go build ./...                     # sạch
go vet ./...                       # sạch
go test ./... -count=1             # toàn bộ package PASS
go run ./cmd/docs-coverage-check   # debt = 0
gofmt -l <file .go đổi/mới>        # rỗng sau gofmt -w
```

**Kết quả:** PR [#21](https://github.com/draculemihawk123-ai/agent-workflow/pull/21), CI 6/6 xanh ngay lần
đầu (không gặp flake lần này). Squash-merge vào master tại `1939ea4`. V4-12 DONE — tiếp tục thẳng sang
V4-12A theo đúng lệnh tự động trước đó.

## V4-11 — JOIN ALL/ANY/QUORUM

**Bối cảnh:** tiếp tục thẳng sau khi V4-10 merge (c44bffe), theo đúng lệnh "tự động sang task tiếp theo".
Đọc task text ("join theo persisted tokens và declared policy; readiness/short-circuit/cancel remaining
policy, shared-state merge validation, integration scope") rồi đọc thật code trước khi hỏi: `BranchToken`
(V4-10) đã có sẵn state machine ACTIVE/SUCCEEDED/FAILED/CANCELLED và `ListBranchTokensForRun`, nhưng CHƯA
ai từng đọc lại token set để tính verdict — `advanceRunTx`'s own JOIN-arrival block (V4-10) chỉ terminalize
MỘT token đến nơi rồi dừng, không đánh giá gì thêm; `decideRetryOrExhaustion` (V4-06) chỉ terminalize token
của branch FAILED, cũng không đánh giá gì thêm; `JoinNodeConfig.Mode/QuorumCount` (V2-08) đã có nhưng
runtime chưa từng đọc chúng. Phát hiện 2 fork thiết kế thật sự trước khi code: cách tính verdict + idempotent
activation (bao gồm cả câu hỏi ẩn "JOIN's own NodeRun được tạo khi nào, ID gì"), và số phận của những branch
còn ACTIVE khi verdict đã quyết. Đã hỏi user cả 2.

**Câu hỏi 1 — verdict tính từ token set thế nào, JOIN đi đâu khi bất khả thi:** đề xuất ban đầu của tôi (
inline, không đổi schema, get-or-create qua ID tất định kiểu `<forkNodeRunID>-join`, JOIN auto-advance
THẲNG sang SUCCEEDED ngay khi verdict đạt) được chọn về HƯỚNG nhưng user siết lại RẤT nhiều điểm tôi chưa
nghĩ đủ sâu: (a) token set phải scope đúng `ForkNodeRunID`, không phải RunID hay ForkNodeKey tĩnh — mỗi lần
V4-07 cycle kích hoạt lại FORK là một tập độc lập (điều này tôi đã tự nhận ra đúng từ trước khi hỏi, user
xác nhận lại); (b) công thức chính xác: ALL thành công khi S=N/bất khả thi khi có FAILED hoặc CANCELLED;
ANY thành công khi S≥1/bất khả thi khi S=0&&ACTIVE=0; QUORUM(q) thành công khi S≥q/bất khả thi khi
S+ACTIVE<q; (c) JOIN NodeRun ID phải dùng HELPER HASH (content hash của ForkNodeRunID+JoinNodeKey), KHÔNG
nối chuỗi tuỳ ý như tôi đề xuất ban đầu — tránh va chạm định dạng ID thật khác; (d) **điểm quan trọng nhất
tôi sai hoàn toàn**: JOIN's own NodeRun phải được branch ĐẦU TIÊN tạo ở **WAITING**, không phải SUCCEEDED
thẳng như tôi đề xuất — verdict evaluation phải chạy MỖI LẦN token đổi trạng thái (kể cả một branch FAILED
giữa chừng, chưa từng tới JOIN), không chỉ khi branch THỰC SỰ tới JOIN; (e) JOIN phải khai đúng MỘT
outcome — user yêu cầu compiler reject 0 hoặc nhiều hơn 1 (tôi chưa hề đề cập điều này); (f) event riêng
`JOIN_DECIDED` với đủ 4 count + reason `JOIN_POLICY_UNSATISFIABLE`, không giả làm RETRY_EXHAUSTED của
V4-06; (g) user từ chối hẳn phương án "Widen JoinNodeConfig thêm FailureOutcome" (option B tôi đưa ra) —
lý do: biến một lỗi kết hợp song song thành business route mới là mở rộng semantics chưa có authority nào
trong tài liệu, một FAILED JOIN chỉ CAS NodeRun FAILED, không outcome, không route (mirror
decideRetryOrExhaustion's own precedent).

**Câu hỏi 2 — nhánh còn ACTIVE khi verdict đã quyết:** đây là điểm user SỬA SÂU NHẤT — tôi đề xuất "CAS
chúng sang CANCELLED ngay" (recommended option của chính tôi) khi ANY/QUORUM đạt ngưỡng sớm mà vẫn còn
branch ACTIVE. User BÁC BỎ hoàn toàn: dù threshold có thể tính được sớm (mathematically locked in, không
thể đảo ngược), Alpha KHÔNG được tự ý cancel — worker có thể vẫn đang chạy hoặc ghi dữ liệu thật, và Alpha
chưa có cơ chế branch-level cancellation/quiescence fencing nào tồn tại để làm việc đó AN TOÀN. Chốt policy:
có thể TÍNH được success sớm, nhưng JOIN vẫn ở WAITING và KHÔNG route downstream cho tới khi MỌI branch của
đúng fork occurrence đó đạt trạng thái terminal — chỉ nhánh FAILED (bất khả thi) mới được short-circuit sớm,
vì một verdict FAILED không bao giờ route đi đâu cả nên không có downstream nào chạy song song với branch
còn sống để mà race; SUCCESS luôn đợi đủ. Không có early branch cancellation nào trong V4-11 — route sớm
thật sự khi ANY/QUORUM đã chắc chắn cần cancellation/quiescence có fencing riêng, ngoài phạm vi task này.

**Phát hiện thêm khi code (tự phát hiện, không phải câu hỏi mới):** viết xong `evaluateJoinTx` và wire vào
`advanceRunTx`'s own JOIN-arrival block (branch tới JOIN qua hop bình thường) và `finalize.go`'s own
`decideRetryOrExhaustion` (branch FAILED giữa chừng), rồi nhận ra còn sót MỘT call site thứ ba:
`dispatchForkBranches`'s own zero-hop "branch's own first edge đã là JOIN" case (V4-10) — một FORK mà MỌI
branch đều là zero-hop tới JOIN (fork -> join trực tiếp, không qua node thật nào) sẽ KHÔNG BAO GIỜ có hop
bình thường nào để trigger evaluation, nếu bỏ sót thì JOIN's own NodeRun sẽ không bao giờ được tạo dù
policy đã thoả mãn ngay từ lúc fan-out. Sửa: thêm gọi `evaluateJoinTx` vào đúng case đó, threading thêm
tham số `jobID` xuyên `dispatchForkBranches` để JOIN_DECIDED's own audit trail nhất quán.

**Phạm vi thực hiện (sau khi khoá xong cả 2 câu hỏi):**
- **`internal/domain/workflow/validation.go`**: `validateJoinConfig` thêm rule JOIN phải khai đúng 1
  outcome (0 hoặc ≥2 đều reject) — compile-time, không để runtime tự khám phá không resolve được.
- **`ports.RuntimeRepository`**: thêm `ListBranchTokensForFork(ctx, forkNodeRunID)` (scoped đúng MỘT fork
  occurrence, khác `ListBranchTokensForRun` Run-wide của V4-10) — sqlite + fake implement.
- **`internal/app/runtime/advance.go`** (phần lõi):
  - `AdvanceRunResult` thêm `JoinDecided`/`JoinVerdict`/`JoinNodeRunID`/`JoinRoute *AdvanceRunResult`.
    `JoinNodeRunID` phơi ra LUÔN khi `ReachedJoin=true` (không chỉ khi Decided) — sửa lại sau khi viết
    xong test sqlite restart, vì cần cách nào đó cho test lấy đúng ID JOIN từ branch A's own kết quả mà
    không cần đọc event (ports.EventsRepository chỉ có Append, không có read API).
  - `deterministicJoinNodeRunID(forkNodeRunID, joinNodeKey)` — content hash (sha256 truncated), đúng yêu
    cầu user ở câu hỏi 1(c).
  - `resolveForkJoinNode(document, forkNode)` — walk forward từ mọi outcome của FORK tới JOIN duy nhất
    (compiler đã đảm bảo tồn tại đúng 1 qua `validateForkJoinTopology`), duplicate cục bộ thay vì reach vào
    hàm unexported `firstJoinsReachableFromBranch` của package `workflow` — cùng lý do
    `dispatchForkBranches`'s own doc comment đã nêu cho việc duplicate của chính nó.
  - `evaluateJoinTx` (hàm mới, trái tim của task): get-or-create JOIN NodeRun (WAITING nếu mới), nếu đã
    không còn WAITING thì AlreadyDecided=true no-op ngay (đúng "duplicate completion"); đọc token set tươi
    qua `ListBranchTokensForFork`; tính infeasible/satisfied theo đúng công thức khoá; nếu infeasible → CAS
    FAILED, append JOIN_DECIDED, dừng; nếu satisfied (mọi token terminal) → append JOIN_DECIDED rồi GỌI LẠI
    `advanceRunTx` chính nó (NodeRunID=JOIN's own, Outcome=outcome duy nhất đã khai) để route downstream —
    tái dùng nguyên CAS/shared-state/cycle-exhaustion/dispatch-switch đã có, không viết dispatch thứ hai.
  - Wire vào 3 call site: `advanceRunTx`'s own JOIN-arrival block, `dispatchForkBranches`'s own zero-hop
    JOIN case (gap tự phát hiện), `finalize.go`'s own `decideRetryOrExhaustion` (cần resolve document/
    forkNode/joinNode từ `branchToken.ForkNodeRunID` trước khi gọi, vì một branch FAILED giữa chừng chưa
    từng chạm JOIN nên không có `downstreamNode` sẵn).
  - Event mới `JOIN_DECIDED` (type + payload + đăng ký `eventschema` ngay, golden fixture, 2 test
    round-trip — payload có slice-free nên so sánh `==` được, không cần `reflect.DeepEqual` như
    NODE_FORKED).

**Test (fake + sqlite, `join_test.go`/`join_sqlite_test.go`, 9 test):**
`TestJoin_AllMode_SucceedsOnceEveryBranchSucceeds`, `TestJoin_AllMode_FailsAsSoonAsOneBranchFails_
WithoutWaitingForOthers` (short-circuit fail, token branch kia vẫn ACTIVE không bị đụng),
`TestJoin_AnyMode_WaitsForFullTerminationEvenAfterThresholdMetEarly` (đúng correction câu hỏi 2 — threshold
đạt sớm nhưng JOIN vẫn chưa quyết cho tới khi branch cuối terminal), `TestJoin_AnyMode_FailsWhenEveryBranchFails`,
`TestJoin_QuorumMode_ImpossibleQuorum_FailsWithoutWaitingForRemaining` (đúng "impossible quorum" Verify
line — QUORUM(2)/3 branch, 2 FAILED đã đủ để bất khả thi, branch thứ 3 vẫn ACTIVE không bị đụng),
`TestJoin_QuorumMode_SucceedsOnceMetAndAllTerminal`, `TestJoin_DuplicateCompletion_LateArrivalAfterAlready
DecidedIsNoOp` (đúng nghĩa đen "duplicate completion" — branch B hoàn thành THẬT sau khi JOIN đã FAILED từ
branch A, token B vẫn SUCCEEDED bình thường nhưng JOIN không re-decide/re-event/re-route),
`TestJoin_SharedStatePatchesFromDifferentBranches_BothMergeByTheTimeJoinRoutes` (chứng minh cơ chế
`applySharedStatePatch`+CAS có sẵn từ V4-03 áp dụng đúng cho hội tụ FORK/JOIN mà không cần merge machinery
mới — đúng "shared-state merge validation" trong Thực hiện line) — cộng
`TestAdvanceRun_SQLite_Join_PersistsAcrossRestart` (đóng/mở lại `sqlite.Store` thật GIỮA lúc branch A đã
hoàn thành qua pipeline Schedule/Finalize thật và branch B chưa chạy; verify JOIN NodeRun WAITING trước
restart, rồi SUCCEEDED/route đúng "end" sau restart trên `UnitOfWork` hoàn toàn mới — đúng "restart" Verify
line).

**Verify (chạy local):**
```
go build ./...                     # sạch
go vet ./...                       # sạch
go test ./... -count=1             # toàn bộ package PASS
go run ./cmd/docs-coverage-check   # debt = 0
gofmt -l <file .go đổi/mới>        # rỗng sau gofmt -w
```

**Kết quả:** PR [#20](https://github.com/draculemihawk123-ai/agent-workflow/pull/20), CI 6/6 xanh ngay lần
đầu (không gặp flake lần này, kể cả job "Linux race and stability" 13m27s — 10x offline suite + race
detector toàn bộ). Squash-merge vào master tại `09a74c2`. V4-11 DONE — tiếp tục thẳng sang V4-12 theo đúng
lệnh tự động trước đó.

## V4-10 — FORK branch tokens

**Bối cảnh:** tiếp tục thẳng sau khi V4-09 merge (53beef1), theo đúng lệnh "tự động sang task tiếp theo".
Đọc task text ("phát persisted branch identity không dựa queue order; create tokens atomically, activate
branches idempotently, static write-scope admission") rồi đọc thật code trước khi hỏi: `runtime.BranchToken`
(V4-01) đã có schema/domain type từ trước — `BranchTokenState{ACTIVE,SUCCEEDED,FAILED,CANCELLED}`,
`ID/RunID/ForkKey/BranchKey/CurrentNodeKey/State/Version` — nhưng CHƯA có consumer thật nào: không NodeRun
nào từng gắn token, `advanceRunTx` (V4-03) chưa có case nào cho FORK, dispatch switch dừng ở
"enqueue only, task sau sở hữu" cho FORK/JOIN/WAIT/APPROVAL/END đúng như comment gốc đã ghi. Phát hiện 2
fork thiết kế thật sự trước khi code: cách theo dõi branch identity, và "static write-scope admission"
nghĩa cụ thể là gì khi schema hiện tại không có scope riêng cho branch. Đã hỏi user cả 2.

**Câu hỏi 1 — theo dõi branch identity thế nào:** đề xuất "thêm `BranchTokenID` vào `NodeRun`" được chọn,
user khoá semantics chi tiết hơn nhiều so với đề xuất gốc: trước FORK `BranchTokenID=nil`; FORK dispatch
tạo ATOMICALLY một token + NodeRun đầu cho mỗi branch (hoặc mark SUCCEEDED ngay nếu branch's own first edge
đã là JOIN — không NodeRun nào); mỗi hop thường trong branch kế thừa cùng BranchTokenID, đồng thời CAS
`BranchToken.CurrentNodeKey` trong CÙNG transaction; khi branch tới JOIN — không gắn một token duy nhất vào
JOIN NodeRun (nhiều branch cùng hội tụ), chỉ mark token đến nơi SUCCEEDED, JOIN's own NodeRun là activation
DÙNG CHUNG mà V4-11 mới đánh giá; sau JOIN downstream trở lại nil; NodeRun trong branch FAILED/CANCELLED
phải terminalize token tương ứng CÙNG transaction; mọi update token dùng expected version chống
stale/duplicate hop. **User tự phát hiện một lỗi schema V4-01 ngay trong câu trả lời** (không phải câu hỏi
riêng của tôi — tôi chưa nhìn ra vấn đề này khi hỏi): unique constraint gốc `(RunID, ForkKey, BranchKey)`
không sống sót qua một FORK bị kích hoạt lại trong cycle (V4-07, đã merge trước đó) — lần FORK thứ hai
trong cùng Run sẽ đụng token của lần activation đầu. Sửa: thêm `ForkNodeRunID NodeRunID` vào `BranchToken`,
đổi unique thành `(ForkNodeRunID, BranchKey)`, giữ `ForkKey` lại chỉ để query/audit.

**Câu hỏi 2 — "static write-scope admission" nghĩa là gì:** tôi hỏi thẳng vì nhận ra không thể tự suy luận
— schema hiện tại (`work.RepositoryScope`, `NodeRun.EffectiveScope`) không có khái niệm "scope của một
branch" tách biệt, dựng một kiểm tra path-overlap tĩnh giả sẽ là bịa đặt semantics không có ADR nào hậu
thuẫn. User xác nhận đúng lo ngại đó và khoá phạm vi Alpha thật sự hẹp hơn cái tên gợi ý: mỗi branch giữ
NGUYÊN Run/TaskFamily/WorkspaceSet/manifest authority hiện có (không tự thêm repo, không tự nâng
READ→WRITE, không tự mở PathScope); executable NodeRun trong branch pin effective scope từ authority hiện
có khi được schedule (KHÔNG copy literal `FORK.NodeRun.EffectiveScope` — trường đó có thể còn rỗng vì FORK
là node cấu trúc chưa từng được schedule thật); nhiều branch cùng quyền WRITE trên một RepositoryWorkspace
vẫn được DISPATCH — serialize là việc của WriteLease ở THỜI ĐIỂM EXECUTION, không phải ở dispatch time. User
tự chỉnh lý luôn cả cách tôi định đặt tên phần việc: đổi từ "static write-scope admission" (nghe như đã
enforce xong) thành "scope-containment admission + repository-level write serialization **contract**" — vì
`ExecuteNodeHandler` (V4-04/V4-05) hiện CHƯA thật sự acquire WriteLease nào (mới có contract + fenced-
finalize), caller acquisition end-to-end là việc của V5; V4-10 không được claim write concurrency đã enforce
hoàn chỉnh.

**Phạm vi thực hiện (sau khi khoá xong cả 2 câu hỏi):**
- **Migration `0021_fork_branch_tokens.sql`**: `DROP TABLE branch_tokens` (bảng V4-01 chưa từng có writer
  thật — an toàn drop/recreate thay vì `ALTER`) rồi `CREATE TABLE` lại với `fork_node_run_id TEXT NOT NULL
  REFERENCES node_runs(id)`, `UNIQUE(fork_node_run_id, branch_key)` thay cho `(run_id, fork_key,
  branch_key)` gốc; `ALTER TABLE node_runs ADD COLUMN branch_token_id TEXT REFERENCES branch_tokens(id)`.
  Bump migration count 19→20 (`db_test.go`, `unitofwork_test.go`).
- **`internal/domain/runtime/branch.go`**: `BranchToken` thêm field `ForkNodeRunID NodeRunID`;
  `NewBranchToken` thêm tham số tương ứng.
- **`internal/domain/runtime/runtime.go`**: `NodeRun` thêm `BranchTokenID *BranchTokenID` (nil ngoài branch,
  set POST-CONSTRUCTION bởi caller — không thêm tham số vào `NewNodeRun`, y hệt cách `.State` đã được set
  post-construction ở nơi khác trong `advance.go`, tránh phải sửa cả 3 call site hiện có của constructor).
- **`ports.RuntimeRepository`**: `GetBranchToken` đổi khoá từ `(runID, forkKey, branchKey)` sang
  `(forkNodeRunID, branchKey)`; thêm `GetBranchTokenByID(ctx, id)` (mới — cần vì `advanceRunTx` chỉ có sẵn
  bare `NodeRun.BranchTokenID`, không có cặp `(ForkNodeRunID, BranchKey)` mà `GetBranchToken` gốc cần) và
  `TransitionBranchToken` (fenced CAS: state + CurrentNodeKey + expected version, mirror
  `TransitionWaitRegistration`/`TransitionApprovalRequest`). Sqlite (`runtime_manifest.go`) và fake
  (`ports/fake/runtime.go`) implement cả hai.
- **`internal/adapters/sqlite/start_workflow_run.go`/`node_dispatch.go`**: `createNodeRunTx` ghi
  `branch_token_id`; `loadNodeRunByID` đọc lại đúng cột đó vào `NodeRun.BranchTokenID`.
- **`internal/app/runtime/advance.go`** (phần lõi):
  - `AdvanceRunResult` thêm `ReachedJoin`/`JoinBranchTokenID`/`JoinNodeKey` (khi downstream là JOIN và
    upstream NodeRun mang BranchTokenID — CAS token đó SUCCEEDED, KHÔNG tạo NodeRun nào cho JOIN, trả kết
    quả sớm) và `ForkedBranches []ForkedBranch` (khi downstream là FORK).
  - Hàm mới `dispatchForkBranches`: với mỗi outcome khai báo trên FORK (= một branch), tạo token; nếu
    branch's own first edge đã là JOIN thì terminalize SUCCEEDED ngay, không NodeRun; ngược lại tạo branch's
    own first NodeRun (gắn BranchTokenID) rồi dispatch bằng đúng logic autoAdvance/executable/WAIT/APPROVAL
    routing đã có trong dispatch switch chính — cố ý DUPLICATE một phần nhỏ logic đó thay vì tổng quát hoá
    route chính (đã test rất kỹ từ V4-03 đến V4-09) thành một hàm đệ quy dùng chung, đổi lấy việc path
    chính không bị đụng. Một branch's own first node là FORK khác (nested fork) bị bỏ PENDING có chủ đích —
    thực ra `validateForkJoinTopology` (V2-08) đã reject nested fork/join ở compile time nên nhánh này
    defensive/unreachable trên document hợp lệ, không phải một giới hạn thật sự bị thiếu.
  - FORK's own NodeRun tự CAS thẳng SUCCEEDED với SelectedOutcome rỗng ngay khi fan-out xong (một FORK nhận
    MỌI outcome khai báo cùng lúc, không chỉ một).
  - Event mới `NODE_FORKED` (type + payload + đăng ký `eventschema` NGAY trong changeset này, golden
    fixture, 2 test round-trip — đúng kỷ luật lặp lại từ correction V4-03).
- **`internal/app/runtime/finalize.go`**: `decideRetryOrExhaustion` (V4-06) mở rộng — khi CAS NodeRun sang
  FAILED (non-retryable hoặc hết budget) và NodeRun đó mang BranchTokenID, CAS token đó FAILED trong CÙNG
  transaction (đúng quy tắc đã khoá ở câu hỏi 1: "NodeRun trong branch FAILED/CANCELLED phải terminalize
  token tương ứng").

**Test (fake + sqlite, `fork_test.go`/`fork_sqlite_test.go`, 4 test):**
`TestAdvanceRun_Fork_FansOutToEveryBranchAtomically` (fan-out tạo đúng BranchToken/NodeRun cho mỗi declared
branch, kể cả zero-hop "branch's own first edge là chính JOIN", cộng một `NODE_FORKED` event);
`TestAdvanceRun_Fork_DuplicateDispatch_IsIdempotent` (redelivered job trên chính NodeRun đã route đi không
tạo token/NodeRun/job/event thứ hai — tái dùng nguyên idempotent-replay guard đã có từ V4-03, không cần
logic mới); `TestExecuteNodeHandler_Fork_BranchFailure_TerminalizesOwnBranchToken` (một branch's own NodeRun
chạy thật tới FAILED non-retryable CAS đúng token của NÓ sang FAILED, không đụng token branch khác) —
mượn nguyên pipeline `ScheduleExecutableNodeRun`/`ExecuteNodeHandler`/fake `NodeExecutor` đã có từ
V4-04/V4-05/V4-06, không viết lại; `TestAdvanceRun_SQLite_Fork_PersistsAcrossRestart` (đóng/mở lại
`sqlite.Store` thật giữa fan-out và lần đọc lại — cả FORK's own NodeRun lẫn hai BranchToken quan sát đúng
state sau restart, vì toàn bộ fan-out cam kết trong đúng MỘT transaction nên chỉ có thể quan sát "toàn bộ"
hoặc "chưa gì cả", đây chính là "partial crash" từ Verify line của task).

**Verify (chạy local):**
```
go build ./...                     # sạch
go vet ./...                       # sạch
go test ./... -count=1             # toàn bộ package PASS
go run ./cmd/docs-coverage-check   # debt = 0
gofmt -l <file .go đổi/mới>        # rỗng sau gofmt -w
```

**CI lần đầu — 1 job fail không liên quan diff:** `Linux race and stability (V0-12)` fail sau ~10m50s với
data race thật (`go test -race`) trong `TestEndToEnd_PartialFailure_OneReadyOneFailed_SetBlockedRowsKept`
(`internal/app/workspaceprovision/handler_sqlite_test.go`), trace đi qua `workerpool/pool.go`'s own
`invokeContained`. PR này không đụng `internal/app/workspaceprovision` hay `internal/app/workerpool` ở bất
kỳ file nào. `git log` xác nhận `handler_sqlite_test.go`/`handler.go` lần cuối sửa ở V3-06 (`b6e57b9`) và
`pool.go` ở V1-10 (`400a911`) — cả hai đều xa trước V4. Rerun đúng 1 lần (`gh run rerun <id> --failed`) cho
6/6 xanh ngay, bao gồm chính job race detector vừa fail (10x offline-suite stability + race detector cả 10
lần đều pass ở lần rerun). Kết luận: pre-existing race, CI-runner timing noise một lần, không phải regression
từ diff này — hợp lý để merge sau khi xanh lại, không cần tách hotfix riêng — cùng kết luận như flake gặp ở
V4-06/V4-08.

**Kết quả:** PR [#19](https://github.com/draculemihawk123-ai/agent-workflow/pull/19), CI 6/6 xanh sau 1 lần
rerun. Squash-merge vào master tại `c44bffe`. V4-10 DONE — tiếp tục thẳng sang V4-11 theo đúng lệnh tự động
trước đó.

## V4-09 — APPROVAL semantics

**Bối cảnh:** tiếp tục thẳng sau khi V4-08 merge (d7059d6), theo đúng lệnh "tự động sang task tiếp theo".
Đọc task text ("approval node chỉ resolve bằng typed operator command; approve/reject outcomes") rồi đọc
thật code trước khi hỏi: `ApprovalNodeConfig` (AuthorizedRoles/TimeoutSeconds/EscalationOutcome/
RequestedEvidenceKinds) đã có đầy đủ từ V2-08, khác hẳn WAIT — không cần mở schema như V4-08 phải làm.
Nhưng phát hiện ngay: `AuthorizedRoles` là danh sách TÊN ROLE, còn `ports.Command.Actor` (dùng chung cho
MỌI command trong repo) luôn chỉ là chuỗi identity thuần — grep toàn bộ `internal/domain` không tìm thấy
bất kỳ khái niệm Role/permission nào từng được xây. Đây là fork thiết kế thật sự, đã hỏi user trước khi
code.

**Câu hỏi — cơ chế check actor role:** đề xuất ban đầu của tôi ("request tự khai ActorRoles trong payload
nghiệp vụ") được user SỬA lại theo hướng đúng đắn hơn nhiều: KHÔNG đặt ActorRoles trong
`ResolveApprovalRequest` như dữ liệu do client gửi — mà mở rộng CHÍNH `ports.Command` (struct dùng chung
toàn repo) thêm field `ActorRoles []string`, coi đây là **authentication context** do transport/API layer
tạo từ local session, đúng y hệt cách `cmd.Actor` vốn đã luôn là giá trị đã được vinh danh từ trước, chưa
từng bị re-auth ở bất kỳ command handler nào trong repo. Quy tắc khoá: API/transport tạo Actor VÀ
ActorRoles từ session, không decode từ request body; handler check intersection chính xác/case-sensitive;
rỗng hoặc không giao nhau → `POLICY_DENIED` (dùng thẳng `errorcode.CodePolicyDenied` từ V4-06), không ghi
approval, không route NodeRun; ghi actor VÀ role đã match vào record để audit; ActorRoles KHÔNG thuộc
RequestHash (context, không phải payload — nếu tính vào thì 2 request giống hệt nhau nhưng session khác
sẽ conflict sai). Alpha dùng local-session principal tối giản; Beta thay resolver/identity provider thật
mà không đổi semantics V4-09.

**Phát hiện thêm khi code (tự phát hiện, không phải câu hỏi mới — cùng LOẠI phát hiện như V4-08's own
CompletionOutcome/TimeoutOutcome):** `ApprovalNodeConfig.EscalationOutcome` (V2-08) được document là
"Optional: not every approval needs an escalation path" — nhưng `TimeoutSeconds` lại LUÔN bắt buộc và
dương (validate sẵn từ V2-08, khác hẳn WAIT's own optional ceiling cho SIGNAL). Một timeout CHẮC CHẮN sẽ
xảy ra (không như SIGNAL wait có thể chờ vô hạn nếu không khai timeout) mà không có escape route sẽ tái
tạo đúng cái unbounded-wait TimeoutSeconds tồn tại để ngăn. Doc comment cũ còn hứa hẹn "a standardized
timeout outcome" convention — grep toàn repo xác nhận convention đó KHÔNG BAO GIỜ được xây (y hệt tình
huống WAIT gặp phải). Sửa: `validateApprovalConfig` giờ bắt buộc `EscalationOutcome` non-empty, xoá
`omitempty` khỏi JSON tag.

**Phạm vi thực hiện:**
- **Migration `0020_approval_requests.sql`**: `approval_requests` (run/node_run/node_key,
  authorized_roles_json, requested_evidence_kinds_json, due_at LUÔN NOT NULL — khác wait_registrations's
  own nullable due_at, escalation_outcome LUÔN NOT NULL, state CHECK 4 giá trị PENDING/DECIDED/ESCALATED/
  CANCELLED, decided_by/decided_role/decided_outcome/reason/decided_at nullable — chỉ set cùng lúc CAS
  sang DECIDED, version, UNIQUE(node_run_id)); partial index `WHERE state='PENDING'`.
- **`ports.Command`** (`internal/app/ports/command.go`): thêm field `ActorRoles []string` — authentication
  context, không thuộc RequestHash (đã ghi rõ trong doc comment).
- **`internal/domain/runtime/approval.go`** (mới): `ApprovalRequest` domain type +
  `NewApprovalRequest` constructor.
- **`internal/domain/workflow`**: `validateApprovalConfig` siết EscalationOutcome thành required.
- **`ports.ApprovalRepository`** (mới) + sqlite + fake: `CreateApprovalRequest`, `GetApprovalRequest`,
  `TransitionApprovalRequest` (fenced CAS, y hệt pattern `WaitRepository.TransitionWaitRegistration`).
- **`internal/app/runtime/advance.go`**: case mới `isApprovalNode` trong dispatch switch — LUÔN tạo
  ApprovalRequest + LUÔN enqueue `APPROVAL_TIMER` job (khác WAIT's own conditional timer, vì TimeoutSeconds
  luôn bắt buộc). NodeRun dùng chung state `NodeRunWaiting` với WAIT (không mint state riêng) — generic
  "durably pending an external event", đã có sẵn trong enum từ V4-01.
- **`internal/app/runtime/approval.go`** (mới, app layer): `ApprovalTimerJobKind`/`ApprovalTimerJobPayload`,
  `ApprovalTimeoutHandler` (workerpool.Handler, idempotent no-op khi request không còn PENDING), public
  command `ResolveApproval` (đúng envelope Command/Receipts, cộng check `cmd.ActorRoles` trước cả khi chạm
  state của request).

**Test (fake + sqlite, `approval_test.go`/`approval_sqlite_test.go`):** 8 test — request/timer job tạo
đúng lúc activate, happy path approve+route, unauthorized actor (role sai VÀ không có role nào) reject
bằng typed `POLICY_DENIED`/record không đổi/NodeRun không đổi, duplicate decision (command invocation
khác sau khi đã DECIDED) Won=false/không re-route/không đè DecidedOutcome đã thắng, timer route qua
EscalationOutcome, timer replay sau khi đã DECIDED (no-op, không re-route) — cộng 1 test sqlite bắt buộc:
`TestApprovalTimeoutHandler_SQLite_PersistsAcrossRestart` (đóng/mở lại sqlite.Store thật, timer job vẫn
claim/fire đúng sau restart).

**Verify (chạy local):**
```
go build ./...                     # sạch
go vet ./...                       # sạch
go test ./... -count=1             # toàn bộ package PASS
go run ./cmd/docs-coverage-check   # debt = 0
gofmt -l <file .go đổi/mới>        # rỗng sau gofmt -w
```

**Kết quả:** PR [#18](https://github.com/draculemihawk123-ai/agent-workflow/pull/18), CI 6/6 xanh ngay
lần đầu (không gặp flake lần này). Squash-merge vào master tại `53beef1`. V4-09 DONE — tiếp tục thẳng
sang V4-10 theo đúng lệnh tự động trước đó.

## V4-08 — WAIT persistence và signal semantics

**Bối cảnh:** tiếp tục thẳng sau khi V4-07 merge (79de402), theo đúng lệnh "tự động sang task tiếp theo".
Đọc task text ("node chờ durable signal/timer không giữ worker/job lease, signal chỉ consume đúng một
lần") rồi đọc thật code trước khi hỏi: `workflow.WaitNodeConfig` (DURATION/SIGNAL, TimeoutSeconds) đã có
từ V2-08 nhưng KHÔNG có field nào tên outcome nào fire khi timeout — khác hẳn `ApprovalNodeConfig.
EscalationOutcome` đã có cho đúng vấn đề tương tự; `NodeRunWaiting` tồn tại trong enum từ V4-01 nhưng
chưa task nào từng sản xuất; `wait_registrations`/`wait_signals` hoàn toàn chưa tồn tại (bảng mới, task
này sở hữu trọn). Phát hiện 2 fork thiết kế thật sự trước khi code, đã hỏi user cả 2.

**Câu hỏi 1 — outcome khi timeout:** đề xuất "mở rộng authoring schema" (mirror EscalationOutcome) được
chọn, nhưng user khoá sâu hơn nhiều so với đề xuất gốc của tôi (tôi chỉ đề xuất 1 field TimeoutOutcome,
chưa nghĩ đến việc CompletionOutcome cũng cần pin tường minh khi nhiều outcome): thêm CẢ HAI
`CompletionOutcome` và `TimeoutOutcome` vào `WaitNodeConfig`. Bảng ánh xạ: DURATION đến hạn →
CompletionOutcome; SIGNAL nhận signal → CompletionOutcome; SIGNAL hết TimeoutSeconds → TimeoutOutcome.
Quy tắc: node 1 outcome thì cả hai field để trống (suy luận được); node nhiều outcome thì MỌI đường có
thể xảy ra phải khai báo tường minh, ambiguity reject ngay lúc compile/publish; TimeoutOutcome chỉ hợp lệ
với SIGNAL có TimeoutSeconds>0; DURATION hết hạn luôn là completion, không phải timeout.
`SignalWait`/timer job không tự chọn outcome — chỉ tranh CAS trên registration, bên thắng dùng outcome
đã pin sẵn trong WorkflowVersion (đã immutable, không cần cache/pin lại giá trị riêng — đọc thẳng từ
WaitNodeConfig lúc registration được tạo là đủ, không lệch).

**Câu hỏi 2 — signal identity:** đề xuất "field riêng SignalKey trong request, không dùng
cmd.IdempotencyKey" được chọn, user khoá contract chi tiết: `SignalWaitRequest{SignalKey, Payload}`,
SignalKey required/opaque/caller-supplied. Unique trên wait_signals là (WaitRegistrationID, SignalKey) —
không lặp thêm cột Run/NodeRun/SignalName vì WaitRegistrationID đã định danh đúng activation.
`cmd.IdempotencyKey` bảo vệ MỘT lần gọi command + receipt; SignalKey bảo vệ CÙNG một external event gửi
qua NHIỀU command invocation khác nhau (actor/idempotency key khác nhau, vd webhook retry với delivery id
mới) — hai lớp dedupe tách biệt, cột đặt tên `signal_key` không phải `idempotency_key`. Cùng SignalKey +
cùng payload → idempotent replay; cùng SignalKey + payload khác → typed IDEMPOTENCY_CONFLICT (dùng thẳng
`errorcode.CodeIdempotencyConflict` từ V4-06 — lần đầu code thật ngoài test dùng code này); hai SignalKey
khác nhau đồng thời → CAS registration quyết định đúng 1 winner. Timer timeout không tạo wait_signals row
nào cả. Không tự sinh SignalKey ở server (mất khả năng dedupe xuyên command); caller không có external
identity riêng có thể tự chọn dùng lại idempotency key của mình.

**Phát hiện thêm khi code (không phải câu hỏi user, tôi tự nhận ra khi viết `WaitTimeoutHandler`):**
`WaitRegistrationState` ban đầu tôi định 3 giá trị (ACTIVE/CONSUMED/TIMED_OUT/CANCELLED — tính cả
ACTIVE là 4 nhưng chỉ 2 terminal "thành công") — không đủ. Timer job firing cho DURATION (đến hạn bình
thường) và cho SIGNAL (hết timeout, thất bại) là hai tình huống NGỮ NGHĨA khác nhau hoàn toàn, nhưng
CONSUMED ngụ ý "có signal thật" (sai cho DURATION) và TIMED_OUT ngụ ý "thất bại" (sai cho DURATION — hết
duration là hoàn thành đúng thiết kế). Thêm state thứ 4: **ELAPSED** — DURATION đến hạn, route qua
CompletionOutcome, không phải lỗi. Timer handler tự phân biệt DURATION/SIGNAL bằng
`registration.SignalName == ""` (đúng discriminator `validateWaitConfig` đã enforce sẵn — SignalName
luôn rỗng cho DURATION, luôn có giá trị cho SIGNAL) — không cần lưu thêm cột Mode riêng.

**Quyết định kỹ thuật khác (engineering judgment call, không hỏi vì không phải fork kiến trúc):**
`advanceRunTx`'s own idempotent-replay guard (V4-03, `current.State != NodeRunRunning`) nới thành chấp
nhận CẢ RUNNING lẫn WAITING — một WAIT NodeRun nằm ở WAITING (không bao giờ RUNNING, không Attempt nào)
và cần được route đi từ đó y hệt cách một RUNNING NodeRun được route (SharedStatePatch, cycle-budget
check, NODE_ROUTED event, dispatch switch — toàn bộ pipeline). Tái dùng nguyên `advanceRunTx` cho cả
`SignalWait` VÀ `WaitTimeoutHandler` (thay vì viết lại logic routing riêng cho WAIT) — chỉ cần đổi
`TransitionNodeRunRequest.ExpectedState` từ hardcode `NodeRunRunning` sang `current.State` thật.
`Tx` gains accessor mới `Wait() WaitRepository`, mirror đúng `AdapterBuilds()`/`Readiness()` — "gets a
real interface from the start" cho concern task này sở hữu trọn vẹn (không phải placeholder chờ task
sau). WAIT registration được tạo INLINE trong `advanceRunTx`'s own transaction (không tách job/handler
riêng như V4-04's executable scheduling), vì không có concern kiểu ADR-027 nào (không cần resolve gì
ngoài transaction cả) — registration là pure data từ compiled WaitNodeConfig.

**Phạm vi thực hiện:**
- **Migration `0019_wait_registrations_and_signals.sql`**: `wait_registrations` (run/node_run/node_key,
  signal_name, due_at, completion_outcome/timeout_outcome, state CHECK 5 giá trị, consumed_signal_id
  không FK — theo đúng convention cột ref tuỳ chọn hiện có, version, UNIQUE(node_run_id)); partial index
  `WHERE state='ACTIVE' AND due_at IS NOT NULL` cho timer query. `wait_signals` (wait_registration_id,
  signal_key, payload_json/hash, actor, received_at, UNIQUE(wait_registration_id, signal_key)).
- **`internal/domain/runtime/wait.go`** (mới): `WaitRegistration`/`WaitSignal` domain type +
  `NewWaitRegistration`/`NewWaitSignal` constructor, theo đúng độ sâu validate `NewDecisionArtifact` đã
  làm mẫu.
- **`internal/domain/workflow`**: `WaitNodeConfig` thêm `CompletionOutcome`/`TimeoutOutcome`;
  `validateWaitConfig` thêm rule ambiguity-reject đầy đủ.
- **`ports.WaitRepository`** (`internal/app/ports/wait.go`, mới) + sqlite (`internal/adapters/sqlite/
  wait.go`) + fake (`internal/app/ports/fake/wait.go`): `CreateWaitRegistration`, `GetWaitRegistration`,
  `RecordWaitSignal` (idempotent-replay/conflict contract), `TransitionWaitRegistration` (fenced CAS).
- **`internal/app/runtime/advance.go`**: case mới `isWaitNode` trong dispatch switch — resolve
  CompletionOutcome/TimeoutOutcome/DueAt từ WaitNodeConfig đã pin, tạo WaitRegistration + (nếu có due
  time) enqueue `WAIT_TIMER` job, tất cả trong cùng transaction advanceRunTx đang mở.
- **`internal/app/runtime/wait.go`** (mới, app layer): `WaitTimerJobKind`/`WaitTimerJobPayload`,
  `WaitTimeoutHandler` (workerpool.Handler, idempotent no-op khi registration không còn ACTIVE), public
  command `SignalWait` (đúng envelope Command/Receipts như StartWorkflowRun, cộng lớp SignalKey riêng).

**Test (fake + sqlite, `wait_test.go`/`wait_sqlite_test.go`):** 12 test — happy path DURATION (timer tạo
đúng, DueAt trong khoảng kỳ vọng), happy path SIGNAL không timeout (không tạo job), SignalWait resolve +
route, duplicate SignalKey khác command invocation (idempotent), same-key-different-payload
(IDEMPOTENCY_CONFLICT), wrong-run reject, signal đến sau khi đã CONSUMED (Won=false, không re-route),
timer DURATION route qua CompletionOutcome, timer SIGNAL timeout route qua TimeoutOutcome, timer replay
sau khi đã CONSUMED (no-op, không re-route) — cộng 2 test sqlite bắt buộc:
`TestWaitTimeoutHandler_SQLite_PersistsAcrossRestart` (đóng/mở lại sqlite.Store thật, timer job vẫn
claim/fire đúng sau restart) và `TestSignalWait_SQLite_ConcurrentSameSignalKey_ExactlyOneWinner` (5
goroutine gọi SignalWait THẬT SỰ đồng thời, cùng SignalKey/payload, khác command invocation mỗi cái —
đúng 1 winner, registration CONSUMED đúng một lần) — đây chính là "hai signal đồng thời cùng identity" từ
Verify line của task.

**Verify (chạy local):**
```
go build ./...                     # sạch
go vet ./...                       # sạch
go test ./... -count=1             # toàn bộ package PASS
go run ./cmd/docs-coverage-check   # debt = 0
gofmt -l <file .go đổi/mới>        # rỗng sau gofmt -w
```

**CI lần đầu — 1 job fail không liên quan diff:** `contract (windows-latest)` fail sau ~8.5 phút với
`TestDefaultScenariosFormAValidRegistryAndCleanRun` (`internal/spikeacceptance`, stress `-count=5`) —
"spk09: W1 acquire write lease: durable job lease is no longer authoritative, want nil (clean run)".
PR này không đụng `internal/spikeacceptance`, write-lease, hay SPK-09 quarantine code ở bất kỳ file nào.
`git log` xác nhận file test này lần cuối sửa ở `49808be` (commit V2-08, trước toàn bộ V4), và lịch sử
riêng nó đã có tiền lệ "fix: race thoi gian thu hai tren Windows CI (V0-11A phan 2)" — một stress test đã
biết là nhạy timing trên Windows CI từ trước, không phải regression từ V4-08. Rerun đúng 1 lần
(`gh run rerun <id> --failed`) cho 6/6 xanh ngay. Kết luận: CI-runner timing noise, hợp lý để merge sau
khi xanh lại, không cần tách hotfix riêng — cùng kết luận như job flake gặp ở V4-06.

**Kết quả:** PR [#17](https://github.com/draculemihawk123-ai/agent-workflow/pull/17), CI 6/6 xanh sau 1
lần rerun (job Windows flake một lần, không tái lập, không liên quan diff). Squash-merge vào master tại
`d7059d6`. V4-08 DONE — tiếp tục thẳng sang V4-09 theo đúng lệnh tự động trước đó.

## V4-07 — Business rework/cycle budget

**Bối cảnh:** tiếp tục thẳng sau khi V4-06 merge (68f0ca7), theo đúng lệnh "tự động sang task tiếp theo".
Đọc task text ("rework chỉ qua edge và tạo NodeRun activation mới; iteration counter, max iteration,
escalation route, audit causation") rồi đọc thật code (`advance.go`, `runtime.go`'s own `NodeRun.Iteration`
field, `internal/domain/workflow`'s own `CyclePolicy`/`validateBoundedCycles`) trước khi hỏi bất cứ gì —
phát hiện: rework tự nó KHÔNG cần cơ chế mới (`advanceRunTx` đã luôn tạo activation mới cho bất kỳ edge
target nào, kể cả node key đã visit trước đó — GC-INV-10 vốn đã thoả); cái thiếu thật là RUNTIME chưa BAO
GIỜ enforce `CyclePolicy.MaxIterations` — `NodeRun.Iteration` (cột đã có từ V4-01's own migration, field
domain cũng có sẵn) mọi caller đều hardcode 0, và compiler's own `validateBoundedCycles` chỉ chứng minh
tại publish-time rằng một escape route TỒN TẠI, không có gì runtime từng đếm/ép đi nó. Phát hiện thêm 2
fork thiết kế thật sự (không phải judgment call nhỏ) trước khi code: cơ chế enforce khi budget hết, và
phạm vi/nguồn sự thật cho việc đếm Iteration. Đã hỏi user cả 2 trước khi viết code.

**Câu hỏi 1 — cơ chế forced-escalation:** đề xuất ban đầu của tôi (NodeRun SKIPPED + ép đi escalation edge,
check tại thời điểm sắp tạo activation cho `downstreamNode`) được chọn, kèm 6 điểm khoá chi tiết hơn: (a)
check chạy trong `advanceRunTx` sau khi xác thực outcome/edge upstream nhưng TRƯỚC khi tạo activation
downstream; (b) activation đầu Iteration=0, vòng hợp lệ 1..MaxIterations; (c) candidate MaxIterations+1 →
tạo NodeRun `SKIPPED` (Iteration=candidate, SelectedOutcome=EscalationOutcome, KHÔNG Attempt/job); (d)
CÙNG transaction route tiếp qua escalation edge — đúng tối đa HAI hop, không recursion tự do (escalation
target không tự bị re-check exhaustion dù nó cũng có CyclePolicy); (e) upstream NodeRun (node vừa hoàn
thành) giữ nguyên SUCCEEDED với outcome thật — không biến execution thật thành lỗi chỉ vì hết cycle
budget; (f) append event typed `NODE_CYCLE_EXHAUSTED` với maxIterations/attemptedIteration/triggering
edge/escalation outcome-edge/causation NodeRun. User cũng tự chỉ ra runtime vẫn cần kiểm tra phòng thủ
escalation edge thật sự thoát khỏi cycle (dù compiler đã kiểm tra lúc publish) — điều tôi chưa đề cập
trong đề xuất gốc.

**Câu hỏi 2 — phạm vi đếm Iteration:** per (RunID, NodeKey) được chọn (đúng đề xuất), NHƯNG user tự chỉnh
lý một điểm tôi chưa nghĩ tới: KHÔNG được định nghĩa bằng `COUNT(*)` mọi NodeRun row cho key đó, vì một
`V4-12A` tương lai (scope-expansion reactivation) sẽ tạo NodeRun MỚI cho một key đã visit mà KHÔNG phải
business rework — nó COPY Iteration cũ forward thay vì tăng, nên COUNT(*) sẽ vô tình tiêu cycle budget vì
lý do không liên quan. `Iteration` phải là "business cycle generation number" (nguồn sự thật =
`MAX(iteration)` qua lịch sử durable), không phải row-sequence-number. Không cần port SCC computation từ
compiler sang runtime cho việc ĐẾM — nhưng bước "verify escalation edge thoát khỏi cycle" (phòng thủ, câu
hỏi 1) vẫn cần biết SCC membership.

**Phạm vi thực hiện (sau khi khoá xong cả 2 câu hỏi):**
- **`internal/domain/workflow/validation.go`**: export `CycleMembership(document) map[string]int` — bọc
  lại đúng hàm `stronglyConnectedComponents` (Tarjan) đã có sẵn, không viết lại thuật toán (tránh drift
  giữa 2 bản); build `nodes`/`outgoing` map trực tiếp từ document, tách biệt hoàn toàn khỏi
  `validateNormalizedDocument`'s own full validation pass.
- **`ports.RuntimeRepository.GetMaxNodeIteration(ctx, runID, nodeKey) (uint32, bool, error)`** (Tx-
  composable, mới) — sqlite: `SELECT MAX(iteration) FROM node_runs WHERE run_id=? AND node_key=?`
  (`advance_run.go`); fake: linear scan qua `nodeRuns` map (`ports/fake/runtime.go`). `found=false` nghĩa
  là node key này chưa từng activate trong Run — activation đầu Iteration=0.
- **`internal/app/runtime/advance.go`** (phần lõi, viết lại phần thân `advanceRunTx` sau khi resolve
  `downstreamNode`, giữ nguyên toàn bộ phần trước đó — resolve outcome/edge/CAS upstream — không đổi):
  resolve `candidateIteration` qua `GetMaxNodeIteration`; nếu `downstreamNode.CyclePolicy != nil &&
  candidateIteration > MaxIterations` → resolve escalation edge (lỗi phòng thủ mới
  `ErrCycleEscalationRouteNotFound` nếu thiếu), verify escalation edge thoát cycle qua
  `workflow.CycleMembership` (lỗi phòng thủ mới `ErrCycleEscalationNotBounded` nếu không), tạo NodeRun
  `SKIPPED` cho `downstreamNode`, append `NODE_CYCLE_EXHAUSTED`, rồi resolve lại
  `finalTargetNode`/`finalIteration`/`finalSequence` cho escalation target và tiếp tục pipeline "tạo
  activation bình thường" y hệt nhánh không-exhausted (dùng chung code, không nhân đôi). `NODE_ROUTED`'s
  own `NextNodeRunID`/`NextNodeKey` cố tình mô tả edge target THẬT (downstreamNode, trỏ vào hàng SKIPPED
  khi exhausted) — tách biệt khỏi `AdvanceRunResult`'s own `Next*` (mô tả node cần dispatch tiếp, tức
  escalation target khi exhausted) — hai bộ field khác mục đích, field mới `CycleExhausted`/
  `SkippedNodeRunID`/`SkippedNodeKey` lộ case này ra ngoài mà không đè field cũ.
- **Event mới `NODE_CYCLE_EXHAUSTED`** (type + payload + đăng ký `eventschema` NGAY trong changeset này,
  golden fixture, 2 test round-trip — đúng kỷ luật đã lặp lại từ correction V4-03).

**Test (fake + sqlite, `cycle_test.go`):**
- `boundedCycleDocument()`: start -> maker(AGENT, không CyclePolicy) -> checker(AGENT,
  CyclePolicy{MaxIterations:2, EscalationOutcome:"escalate"}), checker--rework-->maker (cycle 2 node),
  checker--pass-->end_pass, checker--escalate-->end_escalated (escape route compiler yêu cầu).
- `TestAdvanceRun_BoundedCycle_PassesUntilExhaustedThenForcesEscalation`: đi hết 3 vòng maker->checker->
  rework->maker hợp lệ (checker's own Iteration lần lượt 0,1,2, đúng "hoạt động đầu + 2 vòng rework hợp
  lệ = MaxIterations=2"), rồi ở vòng thứ 4 (maker's own "done" — LƯU Ý: exhaustion check nổ ra trên hop
  TẠO checker's activation kế tiếp, tức maker's "done", không phải checker's "rework" — bug thiết kế test
  ban đầu của chính tôi, tự phát hiện và tự sửa khi test fail lần đầu, xem "Errors and fixes" dưới) —
  assert đầy đủ: `CycleExhausted=true`, `SkippedNodeKey=checker`, `NextNodeKey=end_escalated`, maker's own
  SUCCEEDED/"done" không đổi, SKIPPED row đúng Iteration=3/outcome=escalate, payload
  NODE_CYCLE_EXHAUSTED đầy đủ field, và NODE_ROUTED vẫn trỏ vào hàng SKIPPED (edge target thật) chứ không
  bị viết lại thành escalation target.
- `TestAdvanceRun_SQLite_CycleExhaustion_PersistsAcrossRestart`: cùng kịch bản trên real sqlite.Store,
  đóng/mở lại DB, verify cả hàng SKIPPED lẫn chính `GetMaxNodeIteration` (không chỉ đọc lại 1 hàng, mà gọi
  lại đúng query production code sẽ dùng cho vòng kế tiếp) quan sát đúng giá trị sau restart.

**Errors and fixes (tự phát hiện, không phải do user):** viết test lần đầu với topology sai — gọi
`AdvanceRun(checkerID, "rework")` mong đợi exhaustion nổ ra ở ĐÓ, nhưng thiết kế thật (đã khoá với user)
check áp dụng lên `downstreamNode` (node SẮP được tạo activation), và edge target của "rework" là
`maker`, không phải `checker` — maker không có CyclePolicy nên không bao giờ exhausted qua nhánh đó. Sửa:
exhaustion thật sự nổ ra trên hop TẠO checker's activation kế tiếp — tức maker's own "done" outcome (edge
target = checker, node có CyclePolicy). Viết lại toàn bộ vòng lặp test cho đúng topology, cả bản fake lẫn
sqlite.

**Verify (chạy local):**
```
go build ./...                     # sạch
go vet ./...                       # sạch
go test ./... -count=1             # toàn bộ package PASS
go run ./cmd/docs-coverage-check   # debt = 0
gofmt -l <8 file .go đổi/mới>      # rỗng sau gofmt -w
```

**Kết quả:** PR [#16](https://github.com/draculemihawk123-ai/agent-workflow/pull/16), CI 6/6 xanh ngay
lần đầu. Squash-merge vào master tại `79de402`. V4-07 DONE — tiếp tục thẳng sang V4-08 theo đúng lệnh tự
động trước đó.

## V4-06 — Technical retry policy

**Bối cảnh:** tiếp tục thẳng sau khi V4-05 merge (a557645), theo đúng lệnh "tự động sang task tiếp theo"
đã cho từ trước. Đọc task text ("retryable failure tạo Attempt mới dưới cùng NodeRun với budget/backoff")
và đối chiếu GC-INV-09/GC-INV-27, phát hiện 2 fork thiết kế thật sự (không phải judgment call nhỏ): (1)
outcome khi hết retry budget — CAS gì, event gì, có đụng WorkflowRun không; (2) kiến trúc lưu error code
để retry check được bền vững qua restart. Đã hỏi user cả 2 trước khi viết code (`AskUserQuestion`), rồi
user còn chủ động paste lại nguyên văn cả 2 câu trả lời một lần nữa ngay sau đó (rõ ràng để đảm bảo tôi
đọc kỹ, không phải chỉ dẫn mới) — đã đối chiếu 2 lần giống hệt nhau trước khi code.

**Câu hỏi 1 — outcome khi hết retry budget:** user chọn NodeRun → FAILED + domain event typed, khoá đúng
6 quy tắc: (a) MaxAttempts tính cả Attempt đầu tiên; (b) retryable + còn budget → Attempt mới với
`AvailableAt = now + backoff`; (c) retryable nhưng hết budget → CAS NodeRun RUNNING → FAILED; (d)
non-retryable → NodeRun → FAILED ngay, không chờ hết budget; (e) BLOCKED/INDETERMINATE không đi qua logic
này — không tạo blocker giả (bảng `blockers` chưa tồn tại) và không dùng NodeRun.BLOCKED; không tự
chuyển WorkflowRun → FAILED (tổng hợp cấp Run là V4-12); (f) dùng event `NODE_RUN_FAILED` với payload
`failureKind: RETRY_EXHAUSTED | NON_RETRYABLE_FAILURE, lastAttemptId, attemptsUsed, maxAttempts,
lastErrorCode, attemptPolicyVersionId` — Attempt cuối giữ nguyên `TerminationReason` gốc, KHÔNG thêm
RETRY_EXHAUSTED vào đó (exhaustion là quyết định cấp NodeRun). User cũng tự chỉ ra một prerequisite tôi
chưa nghĩ tới: `NodeExecutionResult`/`ExecutionAttempt` chưa lưu `AppError.Code` nào cả, nên retry không
áp được `RetryableErrorCodes` bền vững qua restart nếu chỉ dựa `TerminationReason` — phải bổ sung
`FailureCode` typed trước, tuyệt đối không parse error message. Toàn bộ (transition Attempt cuối, quyết
định retry/exhaustion, CAS NodeRun, append event, complete job) phải nằm trong cùng một fenced
transaction.

**Câu hỏi 2 — kiến trúc error-code:** user chọn mở rộng enum hiện có, KHÔNG tạo `runtime.ErrorCode` song
song — kèm một chỉnh lý quan trọng: "§18 hiện có 22 mã, không phải 20" (tôi đếm nhầm lần đầu khi ước
lượng blast radius; `apperror.Code` cũ chỉ có 5 nên thiếu tới 17 mã, không phải 15 như tôi tưởng). Kiến
trúc khoá: đặt enum chuẩn ở tầng domain (`internal/domain/errorcode.Code`) để không vi phạm luật domain
<- app; `apperror.Code` trở thành type alias/re-export để ~80 usage hiện tại không đổi; `AttemptRules.
RetryableErrorCodes` dùng thẳng type này thay vì `[]string`; `ExecutionAttempt.FailureCode` lưu bền;
`NodeExecutionResult.ErrorCode` bắt buộc khi FAILED, rỗng khi SUCCEEDED; timeout do chính execution
envelope xác định và ghi TIMEOUT, không do executor tự đề xuất; retry chỉ dựa code + AttemptPolicy đã
pin, không dùng `apperror.Error.Retryable()` làm authority; khi hết budget, Attempt cuối vẫn giữ code gốc
(vd PROVIDER_UNAVAILABLE), RETRY_EXHAUSTED chỉ là lý do thất bại cấp NodeRun trong `NODE_RUN_FAILED`,
không ghi đè code của Attempt cuối.

**Phạm vi thực hiện (sau khi khoá xong cả 2 câu hỏi):**
- **`internal/domain/errorcode`** (package mới): enum 22 giá trị đúng go-core-spec §18
  (`CodeInvalidArgument` … `CodeInternal`), `Valid()`, `NeverRetryable()` (phủ
  `CodeIsolationEnforcementUnavailable`/`CodeIndeterminate`).
- **`apperror.Code`** đổi thành `type Code = errorcode.Code`; 5 hằng cũ giữ nguyên tên, trỏ sang hằng
  tương ứng bên `errorcode` — verify toàn bộ test `apperror` cũ PASS không sửa gì, xác nhận zero call-site
  churn thật sự trên ~80 chỗ dùng.
- **`policy.AttemptRules.RetryableErrorCodes`**: `[]string` → `[]errorcode.Code`; xoá hẳn 2 map riêng
  `knownErrorCodes`/`nonRetryableErrorCodes`, validate bằng `errorcode.Code.Valid()`/`.NeverRetryable()`.
  Kiểm blast radius trước khi đổi: chỉ 1 file test ngoài package `policy` tự dựng field này
  (`validate_test.go` chính nó) — an toàn, verify lại bằng `go build ./...` sạch.
- **`runtime.ExecutionAttempt.FailureCode`** (mới) + **migration `0018_execution_attempt_failure_code.sql`**
  (`ALTER TABLE` — bảng đã có data thật, không rebuild). Bump 3 chỗ assert migration count 16→17
  (`db_test.go` x2, `unitofwork_test.go` x1 — count file thực tế là 17 vì migration `0013` bị skip trong
  đánh số, không trùng với "migration cao nhất").
- **`ports.TransitionExecutionAttemptRequest.FailureCode`** xuyên qua cả sqlite
  (`transitionExecutionAttemptTx` — `COALESCE(?, failure_code)`) và fake (`RuntimeRepository.
  TransitionExecutionAttempt`).
- **`ports.NodeExecutionResult.ErrorCode`** (mới) — executor tự phân loại khi FAILED.
- **`internal/app/runtime/finalize.go`** (phần lõi, viết lại `FinalizeExecutionAttempt`):
  - `FinalizeExecutionAttemptRequest` thêm `FailureCode errorcode.Code` (bắt buộc khi NextState FAILED/
    TIMED_OUT, cấm khi state khác — `ErrFailureCodeRequired`).
  - Ký hiệu hàm thêm tham số `clk clock.Clock` (package `internal/app/clock` có từ V1-02 nhưng đây là
    real caller đầu tiên trong toàn bộ codebase).
  - Step 6 (branch theo outcome) thêm case FAILED/TIMED_OUT gọi `decideRetryOrExhaustion` — helper mới
    resolve `attemptRules`/`attemptPolicyVersionId` qua `resolvePinnedAttemptRules` (đọc lại
    `DecisionArtifact` `"<nodeRunId>-execution-profile-v1"` V4-04 đã ghi, tìm PolicyRef category ATTEMPT,
    `tx.Definitions().LoadVersion` + `decodeCompiledPolicy` tái dùng từ `schedule.go`), rồi rẽ nhánh đúng
    6 quy tắc đã khoá ở câu hỏi 1.
  - Event mới `NODE_RUN_FAILED` (type + payload + đăng ký `eventschema` NGAY trong changeset này, không
    hoãn — đúng bài học đã lặp lại nhiều lần từ correction V4-03).
- **`internal/app/runtime/execute.go`**: `ExecuteNodeHandler` thêm field `clk clock.Clock`,
  `NewExecuteNodeHandler` thêm tham số thứ 4; set `FailureCode: errorcode.CodeTimeout` ở nhánh TIMED_OUT,
  `FailureCode: execResult.ErrorCode` (fallback `errorcode.CodeExecutionFailed` khi executor trả bare Go
  error hoặc không set ErrorCode) ở nhánh FAILED.

**Test (fake + sqlite, `internal/app/runtime`):**
- Cập nhật mọi call site V4-05 cũ (`execute_test.go`, `finalize_execution_attempt_sqlite_test.go`) để
  truyền `clock.System{}`/`clock.Clock` mới. Hai test cũ (`TestExecuteNodeHandler_Failure_*`,
  `_AttemptDeadlineFinalizesTimedOut`) đổi assertion từ "NodeRun giữ nguyên RUNNING" sang "NodeRun →
  FAILED" — đúng như comment gốc của chính 2 test đó đã ghi trước ("left for V4-06's own retry policy to
  decide what happens next"), vì `attemptPolicyDocument` fixture dùng chung không khai báo
  `RetryableErrorCodes` nào nên mọi code đều non-retryable theo đúng thiết kế.
- `finalize_retry_test.go` (file mới): `TestExecuteNodeHandler_RetryableFailure_
  CreatesNextAttemptWithBackoff` (dùng `clock.Fixed`, chứng minh `AvailableAt` = đúng now + BackoffSeconds
  không sai một giây), `TestExecuteNodeHandler_RetryableFailure_BudgetExhausted_FailsNodeRun` (MaxAttempts=1,
  assert đầy đủ payload NODE_RUN_FAILED/RETRY_EXHAUSTED và Attempt cuối không đổi),
  `TestFinalizeExecutionAttempt_BlockedState_RejectedNeverConsumesRetryBudget` (chứng minh BLOCKED bị
  `ErrUnsupportedFinalizeState` từ chối trước khi chạm logic retry — cấu trúc, không phải một check runtime
  riêng), `TestFinalizeExecutionAttempt_SQLite_RetryChain_PersistsAcrossRestart` (đóng/mở lại
  `sqlite.Store` thật giữa lúc có 1 retry chain đang treo, backoff 1s + sleep thật 1.2s để job claim được
  sau khi mở lại — chứng minh Attempt/job retry sống qua restart thật, không chỉ trong một transaction).
- INDETERMINATE không cần test riêng: cùng lý do cấu trúc với BLOCKED —
  `isFinalizableExecutionAttemptState` (không đổi từ V4-05) chỉ chấp nhận
  SUCCEEDED/FAILED/TIMED_OUT/CANCELLED.

**Verify (chạy local):**
```
go build ./...                     # sạch
go vet ./...                       # sạch
go test ./... -count=1             # toàn bộ package PASS
go run ./cmd/docs-coverage-check   # debt = 0
gofmt -l <21 file .go đổi/mới>     # rỗng sau gofmt -w (file Edit-touched bị CRLF do git-on-Windows)
```

**CI lần đầu — 1 job fail không liên quan diff:** `contract (windows-latest)` fail với
`TestEndToEnd_TwoPoolsRaceSameProbeJob_NoDuplicateProcessing` ("probe calls = 2, want exactly 1") trong
`internal/app/repositoryprobe` — package/test này có từ V3-02, PR này không đụng tới
`internal/app/repositoryprobe` hay `internal/app/workerpool` ở bất kỳ file nào. Trước khi coi là "chỉ
flaky rồi rerun", đối chiếu với tiền lệ `TestProjectWorkspaceGate` (V4-04): lần đó fail 3/3 lần rerun liên
tiếp — bằng chứng đủ mạnh cho một bug thật (race trong `pool.go`'s best-effort `CompleteJob` + test tự
`cancelPool()` quá sớm), buộc phải tách hotfix PR riêng từ master. Lần này khác hẳn: rerun đúng 1 lần
(`gh run rerun <id> --failed`) cho 6/6 xanh ngay, bao gồm chính job Windows vừa fail. Test này tự dùng cửa
sổ race cố định `time.Sleep(150ms)` để "cho một lần claim trùng cơ hội xảy ra thật" cộng clone git fixture
thật — đúng loại test nhạy cảm với runner chậm/tải cao, khác về bản chất với một bug tái lập ổn định. Kết
luận: CI-runner timing noise một lần, không phải regression từ diff này — hợp lý để merge sau khi xanh lại,
không cần tách hotfix riêng (không giống trường hợp V4-04).

**Verify CI:**
```
gh pr checks 15                    # 6/6 xanh sau rerun 1 lần
```

**Kết quả:** PR [#15](https://github.com/draculemihawk123-ai/agent-workflow/pull/15), CI 6/6 xanh sau 1
lần rerun (job Windows flake một lần, không tái lập, không liên quan diff — xem phân tích trên).
Squash-merge vào master tại `68f0ca7`. V4-06 DONE — tiếp tục thẳng sang V4-07 theo đúng lệnh tự động
trước đó.

## V4-05 — Generic worker execution envelope với fake executor

**Bối cảnh:** tiếp tục thẳng sau khi V4-04 merge (4789fa3), theo đúng "sau khi xong thì tự động sang task
tiếp theo" đã cho trước đó. Khi đọc task text ("job claim, attempt RUNNING, optional write leases, fake
typed events/result, fenced finalize") và đối chiếu với tiền lệ gần nhất (`FinalizeWorkflowRun` spike-era)
và `termination.go`'s own doc comment (chỉ định rõ V4-05 sở hữu COMPLETED/EXECUTION_FAILED/
DEADLINE_EXCEEDED, không phải RUN_CANCELLED hay LEASE_LOST), phát hiện 3 fork thiết kế thật sự — không
phải "engineering judgment call" như V4-04's SCHEDULE_NODE_RUN job kind, mà là quyết định có blast radius
tới V4-06..V4-12 sau này. Đã hỏi user cả 3 trước khi viết code (`AskUserQuestion`), thay vì tự quyết rồi
ghi lại như các quyết định nhỏ hơn trước đó.

**Câu hỏi 1 — pattern fenced-finalize:** đề xuất ban đầu của tôi là Tx-composable mới (fake+sqlite song
song, nhất quán với V4-01..V4-04). User đồng ý hướng đó NHƯNG khoá thêm 3 điều kiện tôi chưa nghĩ tới:
(a) `FinalizeWorkflowRun` chỉ nên dùng làm tiền lệ SQL semantics, không nhân bản boundary flat/sqlite-only
của nó; (b) đúng MỘT application service `FinalizeExecutionAttempt` phối hợp toàn bộ 7 bước trong một
`WithSerializedWrite` — worker (`ExecuteNodeHandler`) không được tự chain các repository method riêng lẻ;
(c) **P1 phải sửa TRƯỚC khi code**: `EXECUTE_NODE` job (V4-04, đã merge) đang gắn `AggregateType="NodeRun"`
— sai, vì không chứng minh được job thuộc đúng Attempt một khi V4-06 tạo nhiều Attempt dưới cùng NodeRun;
phải đổi sang `AggregateType="ExecutionAttempt"`, `AggregateID=AttemptID`. Cũng chỉ rõ: test fake không đủ
chứng minh GC-INV-17/18 (chỉ chứng minh application flow) — bằng chứng fencing cuối cùng PHẢI chạy sqlite
thật (stale/expired lease, sai owner/token, WriteLease sai fence, concurrent finalize một-winner).

**Câu hỏi 2 — scope cancel/lease-loss:** đề xuất ban đầu của tôi đúng hướng ("test hành vi envelope, không
tạo TerminationReason mới") và được chọn, nhưng user bổ sung một quy tắc tôi chưa nêu: KHÔNG được map mọi
`context.DeadlineExceeded`/`Canceled` thành DEADLINE_EXCEEDED — chỉ timeout do CHÍNH envelope tự tạo (từ
AttemptPolicy's TimeoutSeconds) mới được terminal hoá; deadline/cancel từ hạ tầng (pool shutdown, heartbeat
loss) phải để Attempt RUNNING cho V4-12B/V4-13 xử lý sau. Đây là lý do `execute.go` derive một
`context.WithDeadline` RIÊNG từ context ngoài, rồi phân biệt bằng `errors.Is(ownCtx.Err(),
context.DeadlineExceeded)` — không dùng chung context với workerpool.Pool's own jobCtx.

**Câu hỏi 3 — outcome routing:** tôi đề xuất "có, nhưng gọi AdvanceRun ở transaction RIÊNG sau khi
finalize commit". User BÁC BỎ phương án này với lý do cụ thể: hai transaction tạo crash gap (Attempt
SUCCEEDED + job completed commit xong, process crash, không còn durable job nào kích hoạt lại AdvanceRun,
NodeRun kẹt mãi). Yêu cầu: trích logic bên trong `AdvanceRun` thành `advanceRunTx(ctx, tx, ids, req)`
Tx-composable, gọi trực tiếp NGAY TRONG transaction fenced-finalize của `FinalizeExecutionAttempt` khi
outcome SUCCEEDED. `AdvanceRun` public giờ chỉ là wrapper `WithSerializedWrite` mỏng quanh
`advanceRunTx` — hành vi bên ngoài y hệt cũ (đã verify lại toàn bộ 20 test `AdvanceRun` cũ PASS không đổi
sau refactor).

**Phạm vi thực hiện (sau khi khoá xong cả 3 câu hỏi):**
- **P1 fix code V4-04 đã merge** (`schedule.go`): `EXECUTE_NODE` job đổi `AggregateType`/`AggregateID`
  sang `ExecutionAttempt`/AttemptID, `IdempotencyKey` đổi sang `"execute-"+attemptID`. `DecisionArtifact`'s
  ID đổi từ `ids.NewID()` (non-deterministic) sang tất định `"<nodeRunId>-execution-profile-v1"` — cần
  thiết vì V4-05 phải đọc lại đúng canonical `ResolvedExecutionProfileV1` để lấy `TimeoutSeconds`, và
  NodeRun chỉ được schedule đúng một lần (chính hàm này tự đảm bảo bằng idempotent early-return) nên ID
  tất định không bao giờ va chạm.
- **Refactor `advance.go`**: trích `advanceRunTx(ctx, tx, ids, req) (AdvanceRunResult, error)` khỏi
  `AdvanceRun`'s own closure; `AdvanceRun` giờ chỉ `WithSerializedWrite` rồi gọi `advanceRunTx`. Verify:
  toàn bộ 20 test `AdvanceRun` cũ PASS không sửa gì — refactor thuần, không đổi hành vi.
- **6 method Tx-composable mới:**
  - `ports.RuntimeRepository.GetExecutionAttempt`, `.TransitionExecutionAttempt` (CAS thuần, không fencing
    — fencing là việc của `FinalizeExecutionAttempt`), `.ValidateWriteLeaseFencing` (implement sqlite bằng
    cách gọi thẳng `validateActiveWriteLeaseInTx` có sẵn từ `FinalizeWorkflowRun` — đúng chỉ dẫn "chỉ trích
    SQL helper, không copy nguyên khối"; fake implement trivial pass-through vì deep WriteLease fencing là
    sqlite-only theo quyết định câu hỏi 1).
  - `ports.JobsRepository.ValidateActiveJob`, `.CompleteJob` (Tx-composable, mirror `Store.CompleteJob`
    flat cũ qua `completeJobTx` dùng chung cả hai). Fake `JobsRepository` thêm `leases`/`completed` map +
    `SetActiveLease` test-only helper (fake trước đây hoàn toàn không có state cho job lease, chỉ list
    enqueue request — phải bổ sung thật để test được).
- **`internal/app/runtime/finalize.go`**: `FinalizeExecutionAttempt` — application service DUY NHẤT phối
  hợp 7 bước (validate job lease → load+cross-check Attempt → validate mọi WriteLease → CAS Attempt →
  append event `EXECUTION_ATTEMPT_FINALIZED` (đăng ký eventschema ngay, không hoãn) → route qua
  `advanceRunTx` nếu SUCCEEDED → complete job — tất cả trong một `WithSerializedWrite`).
- **`internal/app/runtime/execute.go`**: `ExecuteNodeHandler` (workerpool.Handler cho `EXECUTE_NODE`) —
  claim (CAS cả NodeRun và Attempt QUEUED→RUNNING, unfenced) → resolve `TimeoutSeconds` từ
  `DecisionArtifact` → chạy `ports.NodeExecutor` dưới `context.WithDeadline` tự tạo → gọi
  `FinalizeExecutionAttempt` đúng một lần với kết quả (SUCCEEDED/FAILED/TIMED_OUT), hoặc không finalize gì
  nếu context bị cancel vì lý do khác (để lại RUNNING).
- **Phát hiện gap thật giữa lúc viết test** (không phải quyết định trước, phát hiện khi test fail): không
  có bước nào transition NodeRun QUEUED→RUNNING trước khi route — `advanceRunTx`'s own routing logic yêu
  cầu NodeRun đang RUNNING mới route được, nhưng V4-04 chỉ để NodeRun ở QUEUED mãi. Sửa: `claimRunning`
  (execute.go) transition CẢ NodeRun VÀ Attempt QUEUED→RUNNING cùng một transaction.
- **`ports.NodeExecutor`** (`internal/app/ports/execution.go`) + `fake.NodeExecutor`
  (`internal/app/ports/fake/execution.go`, có field `Block chan struct{}` để mô phỏng executor chạy quá
  deadline cho test timeout).
- Write-lease ACQUISITION (không phải validation) chưa implement — để lại cho V5's real executor, ghi rõ
  trong code comment tại sao (chưa có real executor nào cần ghi repository thật; `ports.WriteLeaseManager`
  vốn đã không có fake counterpart nào trong repo — tiền lệ V3-09/V3-11 đã ghi rõ "checkable for real
  today, not faked").

**Test (fake + sqlite, `internal/app/runtime`):**
- `execute_test.go` (11 test, fake): success (finalize + advance), failure (finalize không advance),
  executor trả error thường (không phải cancel/timeout), AttemptPolicy deadline → TIMED_OUT (executor
  block bằng channel không đóng, TimeoutSeconds=1), context ngoài bị cancel → không finalize, idempotent
  replay (Attempt đã RUNNING → không re-execute), 3 test `FinalizeExecutionAttempt` validation trực tiếp
  (thiếu SelectedOutcome cho SUCCEEDED, NextState không hỗ trợ, stale JobLease rollback toàn bộ).
- `finalize_execution_attempt_sqlite_test.go` (4 test bắt buộc theo quyết định câu hỏi 1): expired lease
  thật (claim TTL ngắn rồi sleep qua, không phải giả lập bằng field JobLease.LeaseUntil — phát hiện field
  đó chỉ mô tả phía caller, không được `ValidateActiveJob`/`CompleteJob` tin dùng, chỉ cột `lease_until`
  thật trong DB mới có giá trị); sai owner/token; WriteLease thật acquire qua `AcquireWriteLeases` rồi
  tamper `FenceToken` → `ErrWriteLeaseLost`; concurrent finalize 5 goroutine cùng một lease → đúng 1
  winner. Toàn bộ đều assert Attempt/NodeRun/event không đổi khi fencing fail.
- Phát hiện lúc viết fixture: `store.ClaimJob` claim job CŨ NHẤT theo `created_at`, nhưng fixture gọi
  `AdvanceRun`/`ScheduleExecutableNodeRun` trực tiếp (không qua job handler thật) nên job `ADVANCE_RUN`/
  `SCHEDULE_NODE_RUN` cũ hơn vẫn còn AVAILABLE — `ClaimJob` trần sẽ claim nhầm job đó thay vì
  `EXECUTE_NODE`. Sửa bằng helper `claimExecuteNodeJob` claim lặp tới khi đúng `Kind`.

**Verify (chạy local):**
```
go build ./...                     # sạch
go vet ./...                       # sạch
go test ./... -count=1             # toàn bộ package PASS (chạy 2 lần, trước/sau gofmt)
go run ./cmd/docs-coverage-check   # debt = 0
gofmt -l <16 file .go đổi/mới>     # rỗng sau gofmt -w (9 file Edit-touched bị CRLF, file mới thì sạch)
```

**Kết quả:** PR [#14](https://github.com/draculemihawk123-ai/agent-workflow/pull/14), CI 6/6 xanh ngay lần
đầu (job race/stability chạy ~7-8 phút, không phải hang, đã kiểm tra trước khi merge). Squash-merge vào
master tại `a557645`. V4-05 DONE — tiếp tục thẳng sang V4-06 theo đúng lệnh tự động trước đó.

## V4-04 — NodeRun/Attempt scheduling transaction

**Bối cảnh:** tiếp tục thẳng từ correction round 2 (đã merge `ab40c90`) theo đúng lệnh user "sau khi
xong thì tự động sang task tiếp theo" — không hỏi lại. Giữa phiên, môi trường máy bị reset: Go
toolchain biến mất hoàn toàn khỏi máy (không có trong PATH, registry, hay bất kỳ thư mục cài đặt chuẩn
nào — chỉ còn lại module cache cũ ở `C:\Users\admin\go\pkg\mod`), dù trước đó vẫn dùng bình thường. Đã
hỏi user xác nhận cách xử lý; user chọn cho tôi tự chạy `winget install GoLang.Go`. Cài xong (Go
1.27.0 tại `C:\Program Files\Go\bin`), verify lại toàn bộ code cũ trước khi tiếp tục (build/test full
suite PASS) rồi mới viết code mới.

**Nhận diện gap thiết kế trước khi code:** V4-03's `AdvanceRun` tạo NodeRun `PENDING` cho executable
node (AGENT/COMMAND/MACHINE_GATE) rồi DỪNG — không có job/consumer nào được enqueue cho nó (khác
ROUTER/START auto-advance, vốn tự enqueue `ADVANCE_RUN` tiếp). Nghĩa là trước task này, một NodeRun
PENDING loại executable sẽ nằm im vĩnh viễn, không ai bao giờ chạm tới. "Hoàn thành khi: queue delivery
lặp không tạo duplicate attempt" của chính V4-04 hàm ý rõ cơ chế trigger PHẢI là một durable job — nên
quyết định (kỹ thuật, không phải kiến trúc mới, không hỏi lại user): mở rộng `AdvanceRun` thêm một
nhánh enqueue job mới `SCHEDULE_NODE_RUN` khi downstream node là executable, atomically cùng transaction
tạo NodeRun activation đó — giữ đúng discipline "một hop, một job" đã có, chỉ thêm một loại job thứ hai
song song với `ADVANCE_RUN` thay vì tái dùng nhầm ý nghĩa của job đó.

**Kiến trúc hai pha bắt buộc (đã chốt với user trước đây, round 2):** `ports.RuntimeExecutionConfigProvider`
phải resolve NGOÀI transaction (có thể I/O thật), core tự canonicalize/tính hash — không bao giờ nhận
hash trần từ caller. Vì vậy `SCHEDULE_NODE_RUN` KHÔNG thể gộp vào transaction có sẵn của `AdvanceRun`
(lúc đó transaction đã mở) — bắt buộc phải là job/handler riêng, đúng như thiết kế "resolve config →
mở transaction riêng → re-check pin" user đã mô tả.

**Phạm vi thực hiện:**
- `internal/domain/runtime/runtimeexecutionconfig.go` (đã có sẵn từ trước khi máy mất Go, verify lại):
  `RuntimeExecutionConfigSnapshotV1` + `NewRuntimeExecutionConfigSnapshotV1` — contract ADR-027, nơi
  DUY NHẤT được tính `RuntimeExecutionConfigHash`.
- `internal/app/ports/runtimeexecutionconfig.go` + `fake/runtimeexecutionconfig.go`: port +
  fake provider deterministic (`NewRuntimeExecutionConfigProvider()`, `Err` field để test fail-closed).
- Migration `0017_node_run_scheduling_pins.sql`: `node_runs` thêm `effective_scope_json`,
  `execution_profile_hash`, `manifest_revision` (ADD COLUMN — bảng đã có row thật từ V4-02/03, không còn
  rebuild an toàn được nữa, khác V4-01's `execution_attempts`/`workflow_runs`). Sửa 3 assertion migration
  count cũ (15→16), bài học lặp lại từ V3-07/08/09/V4-01.
- `ports.RuntimeRepository` thêm `ScheduleNodeRun` (CAS PENDING→QUEUED, pin cả 3 field trên cùng lúc)
  và `CreateExecutionAttempt` — implement cả sqlite (`internal/adapters/sqlite/schedule_node_run.go`,
  file mới) và fake (`ports/fake/runtime.go`, thêm map `attempts` + accessor test-only
  `Attempts()`/`Decisions()`).
- `internal/adapters/sqlite/schedule_node_run.go`: thêm `encodeEffectiveScopeJSON`/
  `decodeEffectiveScopeJSON` — `work.RepositoryScope` toàn field unexported nên phải có DTO JSON riêng,
  round-trip qua `work.NewRepositoryScope` (constructor validate) chứ không bypass. `node_dispatch.go`'s
  `loadNodeRunByID` (dùng chung bởi cả V4-03 lẫn spike-era `DispatchNodeIntent`/
  `CompleteNodeAndDispatchNext`) mở rộng đọc thêm 3 cột mới — backward-compatible (cột nullable).
- `internal/app/runtime/schedule.go` (file mới, ~330 dòng): hàm lõi `ScheduleExecutableNodeRun` —
  phase 1 resolve+hash config ngoài transaction; phase 2 mở `WithSerializedWrite`, re-check NodeRun còn
  PENDING (không thì no-op idempotent — discipline giống hệt `AdvanceRun`), resolve
  `ResolvedExecutionProfileV1` qua `resolveExecutionProfile` (dispatch theo node type: AGENT đọc
  `ProfileRef`→`agentprofile.AgentProfileDocument` qua `tx.Definitions().LoadVersion` +
  `decodeCompiledAgentProfile`, COMMAND/MACHINE_GATE tương tự nhưng không có field riêng; mọi
  `PolicyRef` được `LoadVersion`+`decodeCompiledPolicy`, category ATTEMPT→`TimeoutSeconds`,
  PERMISSION→`IsolationTier`/`GrantedCapabilities`; `AdapterBuildID` khai báo thì `tx.AdapterBuilds().Get`
  fail-closed nếu không resolve được), resolve effective scope qua `ListWorkItemEffectiveScopes`, exact
  manifest revision qua `ListRunManifestAmendments` (0 nếu chưa có amendment), ghi `DecisionArtifact`
  (Kind `EXECUTION_PROFILE_V1`, canonical profile JSON cho audit — đúng yêu cầu round-1 "phải lưu được
  canonical snapshot, chỉ hash không đủ"), gọi `ScheduleNodeRun`+`CreateExecutionAttempt`
  (AttemptNumber=1, InputRevisionSet từ manifest's BaseRevisionSet), append event `NODE_SCHEDULED`
  (đăng ký ngay trong `event_schema.go` + golden fixture — KHÔNG lặp lại lỗi hoãn đăng ký của
  NODE_ROUTED lần đầu), enqueue job `EXECUTE_NODE` (V4-05's consumer tương lai, idempotency key
  `execute-<nodeRunId>`, không reuse giữa activation).
- `internal/app/runtime/node_scheduling_handler.go`: `NodeSchedulingHandler` implement
  `workerpool.Handler` cho `SCHEDULE_NODE_RUN`, mirror `Scheduler` (V4-03) — giữ dependency
  `RuntimeExecutionConfigProvider` làm field riêng.
- **Phát hiện quan trọng lúc viết resolver:** `agentprofile.Compile`/`policy.Compile`'s
  `CompiledSnapshot()` không phải `AgentProfileDocument`/`PolicyDocument` trần — nó là wrapper riêng
  `{"document": ..., "dependencies": [...]}` (type unexported `compiledAgentProfileSnapshot`/
  `compiledPolicySnapshot`). Gọi thẳng `agentprofile.DecodeDocument`/`policy.DecodeDocument` (dùng
  `authoring.DecodeStrict`, `DisallowUnknownFields`) trên snapshot đó sẽ FAIL vì field lạ ở top-level —
  xác nhận bằng đọc code thật trước khi viết, không đoán. Phải tự viết `decodeCompiledAgentProfile`/
  `decodeCompiledPolicy` (json.Unmarshal vào wrapper struct cục bộ) làm điểm unwrap riêng.

**Quyết định fail-closed bổ sung (chưa hỏi lại user trước — đã ghi vào `docs/00-start-here.md` mục 22
và `docs/design/06-v4-runtime-engine.md`'s V4-04 để user tự review/override nếu cần, theo đúng tinh
thần "không tự tuyên bố hoàn thành, ghi rõ quyết định để verify sau"):**
Một node executable KHÔNG pin đúng một Policy category ATTEMPT (nguồn `TimeoutSeconds`) hoặc PERMISSION
(nguồn `IsolationTier`) bị từ chối lên lịch — `ErrAttemptPolicyRequired`/`ErrPermissionPolicyRequired`.
Lý do: `ResolvedExecutionProfileV1` (đã khóa từ round 2) không cho phép `TimeoutSeconds=0` hay
`IsolationTier` rỗng — một node thiếu policy tương ứng không thể tạo ra profile hợp lệ, nên fail closed
ở đây là hệ quả trực tiếp của contract đã có, không phải một rule mới bịa ra.

**Test (fake + sqlite, `internal/app/runtime`):**
- `schedule_test.go` (11 test, fake): happy path AGENT (assert đủ: NodeRun QUEUED + 3 field pin đúng,
  Attempt QUEUED/AttemptNumber=1/ProviderKey đúng, job `EXECUTE_NODE` đúng idempotency key, event
  `NODE_SCHEDULED` đúng CorrelationID, DecisionArtifact `EXECUTION_PROFILE_V1` có ghi); idempotent
  replay (không tạo Attempt/job thứ hai); nil provider; provider.Resolve lỗi; thiếu policy ATTEMPT;
  thiếu policy PERMISSION; AdapterBuildID khai báo nhưng không resolve được; ProfileRef không resolve
  được (tái dùng `agentSingleOutcomeDocument()` sẵn có từ `advance_test.go`, vốn cố tình không publish
  profile); NodeRunID không thuộc RunID; `AdvanceRun` enqueue đúng `SCHEDULE_NODE_RUN` job cho downstream
  executable; `NodeSchedulingHandler.Handle` delegate đúng.
- `schedule_sqlite_test.go` (1 test): full chain thật qua sqlite (publish AgentProfile+2 Policy qua
  `definitions.PublishDefinitionVersion` thật — không mock — rồi StartWorkflowRun→AdvanceRun→
  ScheduleExecutableNodeRun), verify CAS/Attempt/job sống qua restart thật (close/reopen store), và
  replay sau restart vẫn no-op đúng (không double-schedule).
- Bỏ qua test COMMAND/MACHINE_GATE node type riêng (quyết định phạm vi, ghi lại minh bạch): nhánh code
  cho 2 loại đó chỉ khác AGENT ở việc bỏ qua field riêng của AGENT (ProviderKey/Model/ToolRefs/
  AdapterBuild), không có logic mới nào chưa được test qua nhánh AGENT hay qua
  `executionprofile_test.go`'s test loại trừ COMMAND/MACHINE_GATE khỏi field AGENT-only. Dựng fixture
  `command.CommandDocument` hợp lệ đầy đủ (ExecutableRef/ArgvElement/OutputContract/NetworkAccess) tốn
  effort không tương xứng với rủi ro thực — sẵn sàng bổ sung nếu user muốn.

**Verify (chạy local):**
```
winget install GoLang.Go           # Go 1.27.0, do máy mất toolchain giữa phiên (user duyệt)
go build ./...                     # sạch
go vet ./...                       # sạch
go test ./... -count=1             # toàn bộ package PASS (chạy 2 lần, trước và sau khi thêm doc)
go run ./cmd/docs-coverage-check   # debt = 0 (cả trước và sau khi thêm mục 22 + cập nhật V4-04 Nguồn)
gofmt -l <15 file .go đổi/mới>     # rỗng sau gofmt -w
```

**PR [#12](https://github.com/draculemihawk123-ai/agent-workflow/pull/12) — CI đỏ lần đầu, KHÔNG phải
regression V4-04:** `contract (windows-latest)` fail 3/3 lần rerun liên tiếp, luôn cùng lỗi
`TestProjectWorkspaceGate` ("workspace set has an active durable job"). Điều tra trước khi merge (không
tự cho qua CI đỏ, và không tự ý `gh pr merge --admin` bỏ qua check — đã hỏi user xác nhận hướng xử lý):
- `git diff master...feat/v4-04-node-scheduling` không đụng file nào test đó hay dependency của nó
  (`workspacerelease`, `workerpool`, sqlite scheduling) tham chiếu tới.
- Test PASS 10/10 lần chạy local trên cùng Windows.
- Cùng test, cùng lỗi y hệt (`workspace set id-9`) đã fail trên chính `master` một ngày trước (run
  33898161457, ngay sau merge PR #10) — trước khi PR #12 tồn tại.
- Một test khác (`TestDefaultScenariosFormAValidRegistryAndCleanRun`) cũng fail trên `windows-latest` ở
  một lần chạy `master` khác (sau PR #11) — cùng job luôn fail chỉ trên Windows, Ubuntu luôn pass.

User tự đọc code và chỉ ra root cause chính xác (tôi ban đầu đoán sai là race `cancelPool()`, bị user bác
bỏ bằng dẫn chứng dòng code cụ thể): `internal/app/workerpool/pool.go`'s `runJob` (dòng ~237) cố tình
best-effort `CompleteJob` — nếu lease 500ms hết hạn giữa lúc handler xong và lúc gọi `CompleteJob`, job
kẹt `LEASED` mãi (chờ recovery reaper reclaim), trong khi WorkspaceSet business state đã `READY` từ
transaction riêng của handler. Test cũ chỉ chờ `WorkspaceSet == READY` rồi `cancelPool()` ngay — không
đủ thời gian cho reaper reclaim trước khi pool dừng. Thêm: test share một `idsource.Sequential` (doc
comment ghi rõ "not safe for concurrent use") giữa pool 3 worker concurrent và goroutine chính — race
thật, không chỉ giả thuyết.

User quyết định: **không rerun thêm, không admin-merge, tạm dừng PR #12** để sửa flake bằng PR riêng từ
`master` trước. Đã làm theo đúng plan user chốt: (1) tách PR [#13](https://github.com/draculemihawk123-ai/agent-workflow/pull/13)
sửa flake từ `master` — thêm `waitForDurableJobState` (chờ đúng job row SUCCEEDED, không chỉ business
state) và đổi handler sang `idsource.Random{}` riêng thay vì share `Sequential`; verify 25 lần chạy local
sạch + full suite PASS; (2) CI PR #13 xanh 6/6 thật (kể cả `windows-latest` — xác nhận fix đúng trên CI
thật, không chỉ local); (3) squash-merge PR #13 vào master (`bf070ad`); (4) rebase PR #12 lên master mới
(`git rebase origin/master`, không conflict), force-push; (5) CI PR #12 xanh 6/6; (6) squash-merge.

**Kết quả cuối cùng:** PR #12 squash-merge vào master tại `4789fa3`. V4-04 DONE. Bài học ghi lại: khi CI
fail 2 lần liên tiếp cùng lỗi trên một job vốn nổi tiếng flaky (giống bài học V4-03's workerpool flake),
đừng dừng lại ở "diff không đụng file này nên chắc chắn flake" — vẫn cần dừng rerun mù quáng sau ngưỡng
hợp lý, hỏi user, và nếu có thể thì tìm root cause thật + sửa tận gốc thay vì chỉ rerun chờ may mắn hoặc
bypass check.

## V4-03 correction — GC-INV-15/shared-state/ROUTER/ExecutionProfileHash gaps

**Bối cảnh:** sau khi V4-03 (PR #9) merge và tôi bắt đầu đọc context cho V4-04, user tự review lại code
V4-03 đã merge và chỉ ra 4 vấn đề P1 cụ thể, kèm quyết định rõ ràng cho từng cái — không phải câu hỏi mở,
là chỉ dẫn để tôi thực hiện đúng theo thứ tự user chốt: (1) ROUTER reject tại compile-time, (2) shared-state
typed patch, (3) domain event GC-INV-15, (4) khóa contract `ResolvedExecutionProfileV1`, rồi mới làm V4-04.
Đây không phải task trong roadmap gốc (không có Task ID riêng) — là correction changeset trên code V4-03
đã merge, theo đúng thứ tự user yêu cầu.

**1. ROUTER reject tại compile-time (`internal/domain/workflow/validation.go`):**
- Thêm rule: `node.Type == NodeRouter && len(node.Outcomes) > 1` → publish error. Trước đây chỉ có runtime
  (`AdvanceRun`) từ chối khi không có outcome được cung cấp — nghĩa là một workflow hợp lệ (compile được)
  vẫn có thể deadlock vĩnh viễn tại runtime vì không producer nào tính được outcome. Giờ compiler chặn
  ngay lúc publish.
- Sửa fixture dùng chung `comprehensiveDocument()` (`node_config_test.go`) và 6 test trong
  `fork_join_test.go` vốn dựa vào router 2-outcome ("fanout"/"skip") — route lại qua approval's "timeout"
  outcome, và `TestValidateForkJoinTopology_RejectsAmbiguousSharedJoin` phải chuyển nguồn fork2 từ router
  sang một outcome thứ ba trên "approval" (nếu gắn vào node bên trong nhánh của "fork" thì trúng nhầm rule
  "nested fork/join" thay vì "ambiguous shared join" — phát hiện qua chạy test thật, không đoán).
- Bỏ `TestAdvanceRun_MissingOutcome_MultiOutcomeRouterRejected`/`twoOutcomeRouterDocument()` trong
  `internal/app/runtime` — scenario đó giờ không thể tạo được qua `workflow.Compile` (constructor công khai
  duy nhất) nữa, nên không còn cách nào test được nhánh runtime đó từ ngoài package; coverage tương đương
  vẫn còn qua `TestAdvanceRun_AgentNodeNeverAutoAdvances`.
- Test mới: `TestValidateDocumentRejectsInvalidGraphs`'s "router with more than one outcome" case.

**2. Shared-state typed patch (`internal/app/runtime/advance.go`):**
- `AdvanceRunRequest` thêm `SharedStatePatch map[string]json.RawMessage`. Validate: field phải được khai
  báo trong `WorkflowDocument.SharedState`; node đang route (NodeKey) phải nằm trong `Writers`; JSON kind
  của value phải khớp `SharedStateFieldType` khai báo; merge theo đúng `MergeRule`
  (LAST_WRITE_WINS/APPEND/REJECT_ON_CONFLICT). Kết quả merge dùng làm `InputStateHash` của NodeRun
  downstream — đúng như user chỉ ra, đây không phải phần tách rời được khỏi routing.
- Thêm `ports.RuntimeRepository.UpdateWorkflowRunSharedState` (CAS hẹp chỉ SharedState+Version, không đụng
  State — khác `CompareAndSwapWorkflowRun` spike-era vốn gộp cả State transition) + implement sqlite/fake.
- 4 sentinel error mới: `ErrSharedStateFieldNotDeclared`, `ErrSharedStateWriterNotAllowed`,
  `ErrSharedStateTypeMismatch`, `ErrSharedStateMergeConflict`.
- 8 test mới (`shared_state_test.go`): LAST_WRITE_WINS áp dụng + hash đúng; APPEND tích lũy qua 2 hop;
  REJECT_ON_CONFLICT từ chối lần ghi thứ hai (không làm hỏng NodeRun đang route); field chưa khai báo;
  writer không được phép; type mismatch; không patch thì không tăng version của WorkflowRun.

**3. Domain event GC-INV-15 (`advance.go`):** append `NODE_ROUTED` event (AggregateType=NodeRun,
AggregateID=NodeRun đang route, Sequence=1 — mỗi NodeRun chỉ có đúng một event "routed" trong đời) cùng
transaction với transition/activation/job. Quyết định trước đó (không emit event, dựa theo tiền lệ
`workspaceprovision.Handler`/`repositoryprobe` không emit) sai vì có invariant tường minh (GC-INV-15: "mọi
state transition tạo domain event trong cùng transaction") — tiền lệ không override invariant. Test mới:
`TestAdvanceRun_NodeRoutedEvent_AppendedInSameTransaction`.

**4. Khóa contract `ResolvedExecutionProfileV1` (`internal/domain/runtime/executionprofile.go`):**
- Type domain thuần (không I/O, không resolve từ Tx thật — đó là việc của V4-04's resolver, task này chỉ
  khóa shape/canonicalization/hash). `ExecutorKind` (AGENT/COMMAND/MACHINE_GATE), `ResolvedExecutorRef`
  (definition+version+compiled hash), `ResolvedPolicyRef[]` (mọi Policy đã resolve), field riêng cho AGENT
  (ProviderKey/Model/ToolRefs/MaxTokens) và COMMAND (EnvAllowlist/NetworkAccess/SecretRefs) loại trừ lẫn
  nhau theo `Executor.Kind` (giống discipline `workflow.Node`'s typed config), `AdapterBuild` optional,
  `TimeoutSeconds`/`IsolationTier`/`AllowedCapabilities`.
- `NewResolvedExecutionProfileV1` validate đầy đủ + normalize (sort/dedupe mọi set-like field và
  `Policies`) rồi trả `ExecutionProfileHash = "sha256:" + hex(SHA-256(canonical JSON))` — deterministic bất
  kể thứ tự input, khác input thì khác hash (test xác nhận cả hai).
- Loại trừ khỏi hash (theo đúng chỉ dẫn user): secret value, ContextSnapshot, RevisionSet, repository scope
  — mỗi thứ đã có pin riêng trên ExecutionAttempt/NodeRun, nhét vào đây là pin trùng.
- Sửa doc comment `ExecutionManifest.ExecutionProfileHash/ContextRoutePolicyHash` (`manifest.go`) — comment
  cũ nói "V4-04 sẽ fill sau" nhưng manifest immutable, không có update path; sửa thành: hai field này ở cấp
  RUN nhưng profile là khái niệm cấp NODE (mỗi node pin executor/policy/timeout khác nhau), nên không bao
  giờ có một giá trị run-wide nào đúng cho mọi node — pin thật nằm ở NodeRun/ExecutionAttempt, hai field
  trên manifest chỉ là chỗ dự trữ cho một run-wide default/fallback profile có thể có sau này, không phải
  chỗ V4-04 hay task nào ghi vào.
- 17 test case mới (`executionprofile_test.go`): accept cả 3 executor kind; hash deterministic + order-
  independent; hash đổi khi content đổi; dedupe set-like field; 12 case reject (schema version sai, executor
  kind sai, thiếu field bắt buộc, timeout=0, isolation tier sai, policy thiếu hash, adapter build thiếu id,
  và exclusivity giữa AGENT/COMMAND/MACHINE_GATE field theo từng executor kind).

**Verify (chạy local):**
```
go build ./...                     # sạch
go vet ./...                       # sạch
go test ./... -count=1             # toàn bộ package PASS (54 test mới/sửa across 3 package)
go run ./cmd/docs-coverage-check   # debt = 0
gofmt -l <13 file đổi/mới>         # rỗng sau gofmt -w (CRLF do git-on-Windows, không phải logic)
```

**Kết quả:** PR [#10](https://github.com/draculemihawk123-ai/agent-workflow/pull/10), CI 6/6 xanh ngay lần
đầu (không gặp lại flake của V0-12 lần này). Squash-merge vào master tại `836703a`. Correction changeset
DONE — V4-04 bắt đầu từ đây.

## V4-03 — Deterministic activation/router core

**Bối cảnh:** user yêu cầu "tiếp tục các task tiếp theo" (không chỉ định phạm vi) sau khi V4-02 merge —
hiểu là tiếp tục tuần tự các task V4 kế tiếp, mỗi task vẫn đi hết chu trình (code → verify → commit → PR
→ CI xanh → merge) trước khi bắt đầu task sau, giống đúng quy trình V4-01/V4-02, không phải mở nhiều PR
song song.

**Quyết định thiết kế quan trọng nhất — hỏi user trước khi code:** V4-03 cần route qua node type
`ROUTER`, nhưng schema compile hiện tại (V2-08, `internal/domain/workflow/workflow.go`) cho `ROUTER`
**zero** config field — không có chỗ nào biểu diễn "deterministic rule" mà go-core-spec §6 nói ROUTER
dùng để chọn outcome. Grep toàn bộ ADR/design/harness-engineering docs không tìm thấy định nghĩa rule đó
ở đâu — đây là gap thật trong spec, không phải xung đột hai nguồn. Đã hỏi user trước khi viết code; user
chọn phương án tôi đề xuất: **giới hạn V4-03 chỉ tự resolve outcome cho node có ĐÚNG 1 outcome khai báo
(START, và ROUTER-1-outcome coi như linear pass-through)**; ROUTER ≥2 outcome không có outcome cung cấp
từ bên ngoài bị từ chối bằng typed error (`ErrOutcomeRequired`), không đoán rule. Rule multi-outcome thật
để lại cho task/ADR sau khi có spec rõ.

**Phạm vi thực hiện:**
- 3 method mới trên `ports.RuntimeRepository` (`GetWorkflowRun`, `GetNodeRun`, `TransitionNodeRun` — CAS
  State/SelectedOutcome/Version, mirror `TransitionWorkItemStatusRequest`), implement cả sqlite
  (`internal/adapters/sqlite/advance_run.go`, tái dùng `loadWorkflowRun`/`loadNodeRunByID` sẵn có từ spike
  thay vì viết lại) và fake.
- `internal/app/runtime/advance.go`: hàm lõi `AdvanceRun(ctx, uow, ids, req)` — đúng MỘT hop mỗi lần gọi
  (khớp go-core-spec §7 rule 5 "commit outcome ... và downstream job atomically", không loop nhiều hop
  trong một transaction): đọc NodeRun hiện tại (idempotent no-op nếu không còn RUNNING — không dùng
  Command/Receipts vì đây không phải public command), resolve outcome (auto nếu node là START/ROUTER và
  có đúng 1 outcome; nếu không phải node "structural" — AGENT/COMMAND/MACHINE_GATE/APPROVAL/WAIT/FORK/
  JOIN — **không bao giờ** tự resolve dù chỉ có 1 outcome khai báo, vì node đó cần Attempt/signal/decision
  thật trước khi có outcome thật), validate outcome ∈ allow-list (GC-INV-11), tra edge từ compiled
  document (không parse authoring file), tạo NodeRun downstream (`ActivationSequence` = current+1); nếu
  downstream cũng auto-resolvable (ROUTER-1-outcome) thì enqueue thêm `ADVANCE_RUN` job cho nó, còn lại
  (kể cả END) chỉ tạo NodeRun `PENDING` rồi dừng — không có consumer nào tồn tại cho các type đó (để lại
  cho V4-04/08/09/10/12), giống discipline "enqueue only, later task consumes" REPOSITORY_PROBE/
  WORKSPACE_PROVISION đã có.
- `internal/app/runtime/scheduler.go`: `Scheduler` implement `workerpool.Handler`, unmarshal
  `AdvanceRunJobPayload` rồi gọi `AdvanceRun` — consumer thật đầu tiên cho `AdvanceRunJobKind` mà V4-02
  đã enqueue nhưng để trống ("sits AVAILABLE until V4-03 builds one").
- Không emit domain event ở lớp routing này — kiểm tra thấy `workspaceprovision.Handler`/
  `repositoryprobe`'s handler (tiền lệ gần nhất cho job-driven mutation) cũng không emit event, CAS
  version/updated_at là audit trail, giữ nhất quán thay vì tự thêm event mà không tiền lệ nào đòi.

**Việc tái dùng quan trọng từ spike:** phát hiện `internal/adapters/sqlite/node_dispatch.go` đã có
`CompleteNodeAndDispatchNext`/`DispatchNodeIntent` từ SPK-04 — đọc kỹ và xác nhận đây là cơ chế RIÊNG,
gắn với `JobLease`/claim thủ công của chính SPK-04's crash-fault-boundary test, KHÔNG tạo NodeRun
downstream và không compose qua `ports.Tx`. Quyết định không tái dùng trực tiếp — xây `AdvanceRun` mới
theo đúng convention Tx-composable V4-01/V4-02 đã lập (giống hệt lý do V4-02 không tái dùng nguyên khối
`WorkflowPersistence.StartWorkflowRun`), chỉ tái dùng hai loader thuần (`loadWorkflowRun`,
`loadNodeRunByID`) làm nền cho `GetWorkflowRun`/`GetNodeRun`.

**Test (20 test mới, `internal/app/runtime`):**
- Fake (`advance_test.go`, 7 test): start→end (node không sở hữu bởi task này); router chain 2 hop kiểm
  `ActivationSequence` tăng đúng 1→2→3→4; GC-INV-11 "outside outcome" (outcome không trong allow-list);
  "missing outcome" (ROUTER 2-outcome không tự resolve được, và test bằng outcome tường minh vẫn hoạt
  động); **guard quan trọng nhất**: AGENT node 1-outcome KHÔNG BAO GIỜ tự hoàn thành dù có đúng 1 outcome
  (seed trực tiếp NodeRun RUNNING vì chưa có dispatcher thật, đúng discipline "poke state" đã dùng cho
  WorkItem READY ở V4-02); idempotent replay; NodeRunID không thuộc RunID bị từ chối.
- Sqlite (`advance_sqlite_test.go`, 1 test): router chain qua restart thật (close/reopen store), verify
  bằng `Store.LoadNodeRunState` có sẵn từ spike.
- Scheduler (`scheduler_test.go`, 3 test): Handle delegate đúng, payload hỏng/thiếu field bị từ chối.

**Verify (chạy local):**
```
go build ./...                     # sạch
go vet ./...                       # sạch
go test ./... -count=1             # toàn bộ package PASS
go run ./cmd/docs-coverage-check   # debt = 0
gofmt -l <8 file đổi/mới>          # rỗng sau gofmt -w (CRLF do git-on-Windows, không phải logic)
```

**Kết quả:** PR [#9](https://github.com/draculemihawk123-ai/agent-workflow/pull/9). Lần chạy CI đầu tiên,
job "Linux race and stability (V0-12)" fail ở run 3/10 với
`TestPool_TwoPoolsRaceRecovery_NoDuplicateProcessing` (`internal/app/workerpool/pool_test.go`) —
`pool_test.go:262: Run (B): workerpool: startup recovery scan: recover expired durable jobs: context
canceled`. Điều tra trước khi merge (không tự cho qua CI đỏ): xác nhận `git diff master...feat/v4-03-...
-- internal/app/workerpool/` rỗng tuyệt đối (PR này không đụng file nào trong package đó), file test lần
cuối sửa ở commit V1-10 (`400a911`), và lỗi có dạng timing-race kinh điển (context canceled giữa hai Pool
instance đua nhau trong startup recovery). Rerun đúng job đó (`gh run rerun --job`) — PASS sạch 10/10,
race detector clean. Kết luận: flake pre-existing trong `workerpool`, không liên quan V4-03. Squash-merge
vào master (2b5acf9) sau khi rerun xanh.

## V4-02 — StartWorkflowRun transaction

**Bối cảnh phiên này:** khi bắt đầu, working tree đã có sẵn phần lớn code V4-02 (domain/ports/sqlite/fake)
ở trạng thái uncommitted, chưa hề được ghi vào file báo cáo này — dấu hiệu một phiên trước đó bị ngắt
giữa chừng (context/crash) trước khi kịp log. Đã đọc kỹ lại toàn bộ code đó (không tin tưởng mù quáng):
build/vet/test repo sạch trước khi động vào gì, đối chiếu từng chỗ với domain constructor thật
(`NewWorkflowRun`, `NewExecutionManifest`, `NewNodeRun`), xác nhận khớp go-core-spec §7 và GC-INV-39. Đã
hỏi user xác nhận tiếp tục hoàn thiện WIP này (không viết lại từ đầu) trước khi sửa bất kỳ dòng nào —
user chọn "Tiếp tục hoàn thiện V4-02 (khuyến nghị)".

**Phạm vi đã có sẵn từ trước (giữ nguyên, chỉ verify lại):**
- `internal/app/runtime/commands.go`: `StartWorkflowRun` — validate WorkItem READY/workspace
  READY/version pin khớp, CAS chặn `work_item_cancellation_intent` đang `REQUESTED` (GC-INV-39), tạo
  WorkflowRun/ExecutionManifest/START NodeRun/domain event/`ADVANCE_RUN` job và chuyển WorkItem
  READY→ACTIVE, tất cả trong một `WithSerializedWrite`; idempotent theo `IdempotencyKey`/`RequestHash`
  giống `CreateRootWorkItem`.
- `internal/adapters/sqlite/start_workflow_run.go`: `CreateWorkflowRun`/`CreateNodeRun` implement
  `ports.RuntimeRepository` phần V4-02.
- Ports (`ports.RuntimeRepository.CreateWorkflowRun/CreateNodeRun`,
  `ports.WorkRepository.TransitionWorkItemStatus`, `ports.DefinitionsRepository.GetWorkflowVersion`) +
  fake tương ứng.
- Domain: `NewNodeRun` nới lỏng bắt buộc `ExecutionProfileHash` (blank hợp lệ cho node cấu trúc như
  START).

**Việc làm trong phiên này (bổ sung, hoàn thiện phần còn thiếu để đóng task):**
1. `internal/app/runtime/commands_test.go` (fake-based, 7 test): happy path tạo đủ
   WorkflowRun/Manifest/NodeRun/event/job + chuyển WorkItem ACTIVE; `ErrWorkItemNotReady`;
   `ErrWorkspaceNotReady`; GC-INV-39 fence (`ErrWorkItemCancellationPending`, dựng pending intent trực
   tiếp qua `tx.Runtime().RecordWorkItemCancellationIntent` vì `CancelWorkItem` thật chưa tồn tại — V4-12C);
   idempotent replay cùng key trả đúng result cũ không tạo job trùng; key trùng hash khác trả
   `ErrReceiptConflict`.
2. `internal/app/runtime/commands_sqlite_test.go` (sqlite thật, 4 test): persist-qua-restart (dùng
   `Store.LoadWorkflowRun`/`LoadNodeRunState` có sẵn từ spike); rollback-không-orphan-row (ép fail thật ở
   bước cuối — `EnqueueJob` — bằng cách pre-seed một `durable_jobs` row trùng đúng `idempotency_key` mà
   `ADVANCE_RUN` job sẽ sinh, dự đoán được nhờ `idsource.Sequential` độc lập cho riêng lệnh
   `StartWorkflowRun`; assert cả 4 bảng `workflow_runs`/`execution_manifests`/`node_runs`/domain event về 0
   và WorkItem về đúng READY@version cũ); concurrent race (nhiều goroutine, mỗi cái một
   IdempotencyKey/RequestHash thật khác nhau — double-submission thật, không phải retry — chỉ đúng 1 tạo
   Run, còn lại `ErrWorkItemNotReady`); `ErrWorkflowVersionMismatch`.
3. `internal/adapters/sqlite/work_queries.go`: thêm `CountWorkflowRuns`/`CountExecutionManifests`/
   `CountNodeRuns` (test-support, giống `CountWorkItems` V3-04 đã có) và
   `SetWorkItemWorkflowVersionForTest` (xem quyết định #2 dưới).
4. gofmt -w lại toàn bộ 13 file đổi/mới — cả 9 file uncommitted từ trước lẫn file phiên này đều bị CRLF
   hoá toàn file (git-on-Windows `core.autocrlf`, không phải nội dung logic đổi), rebuild/test lại sau
   khi format xác nhận vẫn PASS.

**Quyết định/khuyến nghị đã áp dụng khi vướng:**

1. **Test "concurrent start" chuyển hẳn sang sqlite thật, bỏ khỏi bộ test fake.** Lý do: khi viết test
   goroutine đua trên `fake.UnitOfWork`, phát hiện `fake.UnitOfWork.run` (đã có từ V1-06) không hàng đợi
   caller đồng thời — nó chỉ giữ mutex đủ lâu để check/set cờ `inTx` rồi trả lỗi `ErrNestedTransaction`
   ngay cho caller thứ hai đến trong lúc caller đầu còn chạy (cờ chống-dùng-sai-đơn-luồng, không phải
   primitive hàng đợi). Test đua thật trên fake sẽ chủ yếu chứng minh cờ đó nổ, không chứng minh CAS
   nghiệp vụ của `StartWorkflowRun`. `*sqlite.Store.RunSerializedWrite` (`txrunner.go`) thật sự hàng đợi
   writer đồng thời (SQLite writer serialization + busy-retry sẵn có), nên chuyển toàn bộ race test sang
   đó — chạy ổn định 10/10 lần lặp lại khi verify.
2. **`ErrWorkflowVersionMismatch` test dùng SQL trực tiếp pin `work_items.workflow_version_id`, không
   qua bất kỳ command nào.** Lý do: grep toàn repo xác nhận chưa có command thật nào từng set field này —
   đúng như doc comment `CreateRootWorkItemRequest` (V3-03) đã ghi rõ: contract field
   (`WorkflowVersionID` gồm) do một caller tương lai set trực tiếp lên struct domain, không qua command
   nào trong tập phụ thuộc hiện tại. Thêm `Store.SetWorkItemWorkflowVersionForTest` (test-only, giống
   discipline tamper-test của `runtime_manifest_test.go`) thay vì bịa một command giả hoặc bỏ qua nhánh
   lỗi này.
3. **Race `StartWorkflowRun`-vs-`CancelWorkItem` (Verify line của V4-02) chỉ test được nửa đầu ("intent
   trước → start bị CAS từ chối") bằng thật.** Nửa sau ("start trước → Run mới được quiesce cùng các Run
   khác") mô tả hành vi của coordinator V4-12B/`CancelWorkItem` thật (V4-12C) — cả hai chưa tồn tại. Test
   `TestStartWorkflowRun_ThenWorkItemCancellationIntentRecorded_IntentStillSucceeds` chỉ chứng minh
   `StartWorkflowRun` không vô tình khoá đường ghi intent sau khi Run đã start (không giữ lock/side-effect
   nào cản trở) — quiesce Run đã chạy là trách nhiệm của V4-12B, không phải task này.
4. **Không thêm getter `GetWorkflowRun`/`GetNodeRun` vào `ports.RuntimeRepository`.** V4-01 đã áp dụng
   discipline "populated now" — chỉ thêm method khi có caller thật cần. V4-02 không cần đọc lại
   WorkflowRun/NodeRun qua port này (test dùng `Store.LoadWorkflowRun`/`LoadNodeRunState` — hai method
   spike-era có sẵn — cho phần sqlite, và `GetWorkItem`/`Jobs().Items()`/`Events().Items()` cho phần fake);
   để việc thêm getter lại cho task nào thật sự cần (nhiều khả năng V4-03 routing).

**Verify (chạy local):**
```
go build ./...                     # sạch
go vet ./...                       # sạch
go test ./... -count=1             # toàn bộ package PASS (kể cả spikeacceptance/integration/archtest)
go test ./internal/app/runtime/... -run ...ConcurrentDistinctCommands... -count=10 -v   # 10/10 PASS, không flaky
go run ./cmd/docs-coverage-check   # debt = 0
gofmt -l <13 file đổi/mới>         # rỗng sau gofmt -w (CRLF do git-on-Windows, không phải logic)
```
11 test mới trong `internal/app/runtime` (7 fake + 4 sqlite), tất cả PASS riêng lẻ (verbose) lẫn trong
full suite.

**Kết quả:** PR [#8](https://github.com/draculemihawk123-ai/agent-workflow/pull/8), CI 6/6 job xanh ngay
lần đầu (contract ubuntu/windows, Linux race and stability V0-12, spike acceptance ubuntu/windows,
cross-platform semantic diff SPK-13) — Windows job mất ~6 phút (stress write-lease race path +
boundary/dependency report), không phải hang. Squash-merge vào master tại `3659da5`. V4-02 DONE.

**Bài học nhỏ ghi lại cho phiên sau:** khi poll CI qua `gh pr view --json statusCheckRollup`, field trạng
thái đang chạy là `status` (`IN_PROGRESS`/`COMPLETED`), không phải `state` — dùng nhầm field khiến vòng
lặp poll thoát sớm giả (coi `null` là "không còn pending"). Cũng đừng gọi lại lệnh kiểm tra ngay trong
cùng lượt vừa `ScheduleWakeup` — làm vậy chỉ đo được thời gian round-trip của chính lượt đó, không phải
thời gian đã trôi qua thật, gây cảm giác sai là CI "đứng yên".

## V4-01 — Runtime schema và ExecutionManifest

**Phạm vi thực hiện:**
- Migration `0016_runtime_manifest_and_cancellation.sql`:
  - Rebuild (`DROP TABLE`; `CREATE TABLE`, giống kỹ thuật 0002/0003/0006 đã chứng minh an toàn) `execution_attempts`
    (+`BLOCKED`) và `workflow_runs` (+`VERIFYING`, +`CANCELLING`) — an toàn vì chưa từng có row thật nào
    (không phải fixture test) được ghi vào hai bảng này trước task này (chưa có runtime engine).
  - 6 bảng mới: `execution_manifests`, `run_manifest_amendments`, `branch_tokens`, `decision_artifacts`,
    `run_cancellation_intents`, `work_item_cancellation_intents`.
  - `agent_events` thêm cột `artifact_refs_json` (ADD COLUMN, không cần rebuild).
- Domain (`internal/domain/runtime`): `ExecutionManifest`/`RunManifestAmendment` (manifest.go),
  `BranchToken`+`BranchTokenState` (branch.go), `DecisionArtifact` (decision.go),
  `RunCancellationIntent`/`WorkItemCancellationIntent`+`CancellationIntentState` (cancellation.go); mở
  rộng `WorkflowRunState` (+VERIFYING, +CANCELLING), `ExecutionAttemptState` (+BLOCKED); mở rộng
  `TerminationReason` theo đúng ma trận ADR-020 (giữ nguyên giá trị spike-era
  `PROCESS_EXIT_BEFORE_OUTCOME_COMMIT`, không đụng); đổi `ExecutionAttempt.TerminationReason` từ `string`
  sang type `TerminationReason` (khớp với `AttemptTerminationUpdate.Reason` đã dùng type này từ trước).
- Ports: `ports.RuntimeRepository` (trước là `interface{}` rỗng) nay có 12 method thật (Create/Get
  ExecutionManifest, Append/List RunManifestAmendment, Create/Get/List BranchToken, Record/Get
  DecisionArtifact, Record/Get cả hai loại CancellationIntent).
- Adapter sqlite (`runtime_manifest.go`): implement đầy đủ 12 method trên, cross-project check theo đúng
  discipline `catalog.go` (resolve từ row thật, reject `ErrCrossProjectReference`), immutability theo đúng
  discipline `workflow_store.go` (idempotent same-content, `ErrImmutableVersionConflict` khi khác).
- Fake (`internal/app/ports/fake/runtime.go`): in-memory tương đương, để handler test tương lai không cần
  sqlite (V1-05 discipline).
- `workflow_store.go`'s `validateWorkflowRunTransition`: thêm additive mọi state không terminal (CREATED,
  RUNNING, WAITING, BLOCKED) → CANCELLING, không đụng các transition trực tiếp →CANCELLED cũ (V4-12B mới
  là chủ sở hữu việc thay thế protocol thật).
- Test mới (`runtime_manifest_test.go`, 9 test): tamper CHECK constraint, BLOCKED/CANCELLING
  persist-and-reload qua restart (CANCELLING qua đúng CAS path thật `CompareAndSwapWorkflowRun`, không
  phải raw SQL), CreateExecutionManifest (immutable pin + cross-project reject + idempotent), append-only
  amendment (continuity check + race/stale rejection + không đổi manifest gốc), branch token idempotent,
  decision artifact immutable, cả hai loại cancellation intent idempotent theo run/work-item.
- Sửa 3 assertion đếm migration cũ (14→15) do thêm migration mới — đúng bài học đã ghi ở V3-07/08/09.

**Quyết định/khuyến nghị đã áp dụng khi vướng:**

1. **Thêm `VERIFYING` vào enum/CHECK constraint của `workflow_runs` ngay bây giờ, dù task text chỉ nêu tên
   `CANCELLING`.** Lý do: go-core-spec §4.5 (authority cao hơn file version) đã liệt kê `VERIFYING` là
   state chuẩn của `WorkflowRun`; V4-01 là task cuối cùng có thể rebuild bảng này một cách an toàn (bảng
   còn rỗng) — từ V4-02 trở đi sẽ có row thật, và tới lúc V4-12 cần `VERIFYING` (task đầu tiên thật sự tạo
   transition này) thì rebuild sẽ không còn an toàn nữa. Không wire bất kỳ transition nào vào/ra
   `VERIFYING` — chỉ enum/CHECK đã sẵn sàng, để V4-12 tự thêm transition khi có caller thật.
2. **Siết `RunManifestAmendment.Revision` phải đúng bằng `PreviousRevision + 1`** (thay vì chỉ
   `Revision > PreviousRevision` như go-core-spec liệt kê field mà không nói rõ ràng buộc số học). Lý do:
   tránh gap trong chuỗi revision; phát hiện khi viết test (một amendment "nhảy cóc" revision=3 với
   previous=1 lẽ ra phải bị từ chối nhưng lại được chấp nhận vì check ở tầng repository chỉ so
   `PreviousRevision` với high-water mark, không so `Revision` với `PreviousRevision+1`).
3. **`branch_tokens` được tạo ở V4-01** (không đợi V4-10) vì `Mục tiêu` của V4-01 nêu tường minh "persist
   run/node/attempt/branch token". V4-01 chỉ cho Create (idempotent)/Get/List; toàn bộ lifecycle logic
   (activate branch, static write-scope admission) để lại đúng cho V4-10 như "Thực hiện" của V4-10 tự ghi.
4. **`decision_artifacts.kind` và `agent_events.kind` đều để TEXT tự do, không CHECK constraint** — mỗi
   task sau (V4-09, V4-12A, V4-13, V5-11) tự đặt tên kind riêng, giống cách `agent_events.kind` đã làm từ
   spike.
5. **Cả hai bảng cancellation intent (run + work-item) đều đặt trong `RuntimeRepository`**, dù
   `work_item_cancellation_intents` về mặt domain thuộc `WorkRepository` hơn — vì V4-01's "Phạm vi" nêu
   tường minh "V4 sở hữu evolution Alpha của... cancellation_intents" (số nhiều, không tách run/work-item).
6. **Không đụng `TerminationReasonProcessExitBeforeOutcomeCommit`** (giá trị termination reason từ
   SPK-04/SPK-09, không khớp ma trận ADR-020) — đây là code sản xuất thật đã có test/caller thật
   (`internal/app/worker/interruption.go`), và việc quyết định nó có nên đổi tên/thay thế bằng
   `LEASE_LOST`/`OWNERSHIP_LOST_MUTATING` hay không thuộc về V4-13 (Recovery coordinator) — task đó mới là
   nơi pre-existing primitive này được wire vào durable state thật.
7. **Không rebuild `execution_manifests`/`run_manifest_amendments`/`branch_tokens`/`decision_artifacts`/
   `*_cancellation_intents` để thêm `blockers` FK** — bảng `blockers` (design doc §6.1) chưa tồn tại ở
   bất kỳ migration nào trước đây (kiểm bằng grep); không phải phạm vi V4-01, để lại cho V4-12A/V4-12C như
   design đã ngụ ý (chúng là task đầu tiên cần `blockers`).
8. **Không migrate/xóa `WorkflowPersistence` (interface phẳng thời spike)** — `RuntimeRepository` mới chỉ
   có 5 aggregate hoàn toàn mới của riêng V4-01 (không overlap với WorkflowRun/NodeRun/ExecutionAttempt/
   ContextSnapshot/Checkpoint mà `WorkflowPersistence` đã có từ spike); việc hợp nhất hai interface để lại
   cho task nào thật sự cần chúng compose chung trong một Tx (nhiều khả năng V4-02).

**Verify (chạy local):**
```
go build ./...                     # sạch
go vet ./...                       # sạch
go test ./... (-count=1)           # toàn bộ package PASS, kể cả spikeacceptance/integration/archtest
go run ./cmd/docs-coverage-check   # debt = 0
gofmt -l <14 file đổi/mới>         # rỗng (đã gofmt -w, xác nhận build/test lại vẫn PASS sau khi format)
```
9 test mới trong `internal/adapters/sqlite/runtime_manifest_test.go`, cả 9 PASS riêng lẻ (verbose) lẫn
trong full suite. Sửa đúng 3 chỗ migration-count assertion (14→15) ở `db_test.go`×2 và
`unitofwork_test.go`×1 (bài học V3-07/08/09).

**Việc còn lại trước khi coi V4-01 DONE thật sự:** mở PR trên `draculemihawk123-ai/agent-workflow`, chờ CI
xanh (6/6 job như các PR V3 trước), rồi merge. Roadmap yêu cầu "mỗi session một Task ID" — đây là session
đầu của V4, đã hoàn thành đúng một Task ID (V4-01); V4-02 là task kế tiếp có thể bắt đầu sau khi V4-01
merge.

## V4-03 correction round 2 — 5 P1 + 5 P2 sau khi user review PR #10 đã merge

**Bối cảnh:** user tiếp tục review sâu code correction round 1 (PR #10, đã merge 836703a) và chỉ ra
thêm 5 vấn đề P1 (chặn V4-04) và 5 vấn đề P2 (nên sửa cùng lượt), kết luận rõ: KHÔNG quay lại phương án
"hash chỉ từ PolicyRefs" (phần đó đã đúng), nhưng V4-04 không được bắt đầu cho tới khi đóng hết P1. User
cũng ra lệnh tường minh: "sau khi xong thì tự động sang task tiếp theo" — không dừng lại hỏi lần nữa,
làm xong round này rồi vào thẳng V4-04.

**P1-1 — ExecutionProfileHash chưa bao phủ toàn bộ effective config:**
`executionprofile.go` trước đó chỉ đưa `EnvAllowlist`/`NetworkAccess`/`SecretRefs` vào COMMAND, AGENT
không có field nào cho environment/network/secret — hai AGENT execution khác nhau ở các giá trị này vẫn
ra cùng hash. Sửa: bỏ hẳn 3 field COMMAND-only đó, thay bằng một field duy nhất
`RuntimeExecutionConfigHash string` (bắt buộc, áp dụng đều cho cả 3 executor kind) — opaque hash đại
diện cho composition-root Configuration (go-core-spec §19: process timeout, output limit,
environment/network/secret policy), giống discipline "pin bằng identity, không phải content" mà
`CompiledHash` đã dùng ở nơi khác. Đây là fix đúng vì AGENT không bao giờ tự khai environment/network
riêng trong authoring schema — nó luôn là composition-root concern giống COMMAND, nên một hash chung là
hình dạng duy nhất áp dụng đều được cho cả ba.

**P1-2 — Event scheduler không giữ correlation chain:**
`CorrelationID` tồn tại trên `AdvanceRunRequest` nhưng không ai truyền vào — `Scheduler.Handle` không
đọc nó từ job payload. Event cũng thiếu `WorkItemID`/`JobID` (trái go-core-spec §20: "Mọi log/event có
CorrelationID, ProjectID, WorkItemID, RunID, NodeRunID, AttemptID, JobID khi có"). Sửa: `AdvanceRunJobPayload`
thêm `CorrelationID`; V4-02's `StartWorkflowRun` (commands.go, đã merge trước đó — sửa lại vì đây đúng
phạm vi correction) seed từ `cmd.CorrelationID`; mỗi `AdvanceRun` hop forward `req.CorrelationID` vào
follow-up job's payload; `Scheduler.Handle` đọc `payload.CorrelationID` + `job.ID` truyền vào
`AdvanceRunRequest.CorrelationID`/`JobID` mới; event payload thêm `WorkItemID`/`JobID`. Test mới
(`TestAdvanceRun_CorrelationID_PropagatesAcrossHopsAndIntoFollowUpJob`) chứng minh chain sống qua thật
sự một job round-trip (marshal/unmarshal payload), không chỉ copy struct trong memory.

**P1-3 — Event sequence hard-code = 1:**
Sẽ đụng UNIQUE (aggregate_type, aggregate_id, sequence) ngay khi V4-04 thêm transition
PENDING→QUEUED→RUNNING trên cùng NodeRun aggregate (mỗi transition cũng cần event theo GC-INV-15). Sửa:
dùng `completedNodeRun.Version` (trả về từ `TransitionNodeRun`, vốn đã bị discard trước đó) làm
`Sequence` — không cần query/allocate thêm, tự động monotonic đúng 1-1 với transition sinh ra event đó.

**P1-4 — NODE_ROUTED chưa đăng ký với event-schema registry:**
`internal/app/eventschema` (V1-07A) có registry thật nhưng NODE_ROUTED chưa có decoder/registration/
golden fixture — nếu `EnforcingEventsRepository` được nối vào production sau này, event này sẽ bị từ
chối. Thêm `event_schema.go`: `DecodeNodeRoutedV1` + `RegisterEventSchemas(registry)`, golden fixture
`testdata/golden/node_routed_v1.json`, 2 test (`TestNodeRoutedV1_GoldenFixtureDecodes`,
`TestNodeRoutedV1_RealEventPayloadDecodes` — cái sau chứng minh payload `AdvanceRun` thật sự marshal
cũng decode đúng, không chỉ fixture tay viết). Phạm vi CHỈ NODE_ROUTED — không backfill đăng ký cho mọi
event type cũ khác trong codebase (WorkflowRunStarted, ProjectCreated thật, v.v.), việc đó ngoài phạm vi
correction này.

**P1-5 — Quyết định ROUTER chỉ tồn tại trong code, chưa vào authority docs:**
Thêm **ADR-026** (`docs/architecture/02-architecture-decisions.md` mục 28, baseline cũ mục 28→29):
Alpha giới hạn `ROUTER` đúng một outcome; multi-outcome hoãn tới ADR + `RouterNodeConfig` riêng sau này.
Cập nhật go-core-spec §6 ROUTER row trỏ về ADR-026; `docs/00-start-here.md` mục 20 (quyết định cốt lõi
mới); `docs/harness-engineering/09-lec-09-...` chú thích ví dụ `PASS/FAIL/NEEDS_INFO` (vocabulary minh
họa từ lecture gốc, không phải authoring khả dụng của Alpha); `docs/design/06-v4-runtime-engine.md`'s
V4-03 Nguồn field thêm ADR-026.

**P2-1 — Policies chỉ sort, chưa dedupe, chưa validate Category:**
Thêm `dedupeAndSortPolicies` (dedupe theo DefinitionID+VersionID trước khi sort) + validate
`Category.Valid()` (dùng method có sẵn trong `internal/domain/policy`, không tự viết lại). Test:
`TestNewResolvedExecutionProfileV1_DeduplicatesRepeatedPolicy`, case "policy has invalid category".

**P2-2 — AdapterBuild cho phép thiếu protocol/capability hash và xuất hiện trên COMMAND/MACHINE_GATE:**
Sửa: `ProtocolVersion`/`CapabilityHash` bắt buộc non-empty khi `AdapterBuild` được set (bỏ
`omitempty`/optional trên 2 field đó); `AdapterBuild` chỉ hợp lệ khi `Executor.Kind == AGENT` (khớp
authoring schema thật — chỉ `AgentNodeConfig` có `AdapterBuildID`, COMMAND/MACHINE_GATE không có field
này). V4-04 fail-closed khi build không resolve được là việc của resolver thật (V4-04), không phải
contract V1 này.

**P2-3 — Hai field reserved trong ExecutionManifest vẫn được constructor nhận giá trị:**
`NewExecutionManifest` giờ reject non-empty `executionProfileHash`/`contextRoutePolicyHash` thay vì âm
thầm chấp nhận — không xoá field/param (giữ schema V4-01 đã ship ổn định), chỉ đóng đường tạo authority
thứ hai.

**P2-4 — Shared-state patch thiếu sqlite integration/rollback test:**
Thêm `TestAdvanceRun_SQLite_SharedStatePatch_PersistsAndHashesCorrectly` và
`TestAdvanceRun_SQLite_SharedStatePatch_RollbackOnMidTransactionFailure` (`advance_sqlite_test.go`) —
cùng kỹ thuật ép fail bằng job idempotency-key trùng đã dùng ở V4-02's rollback test.

**P2-5 — Patch key nên sort trước khi validate:**
`applySharedStatePatch` giờ sort tên field trước khi lặp — lỗi trả về deterministic khi request có
nhiều field sai cùng lúc (trước đó phụ thuộc thứ tự map iteration ngẫu nhiên của Go).

**Verify (chạy local):**
```
go build ./...                     # sạch
go vet ./...                       # sạch
go test ./... -count=1             # toàn bộ package PASS
go run ./cmd/docs-coverage-check   # debt = 0 (kể cả sau khi thêm ADR-026 vào V4-03's Nguồn)
gofmt -l <10 file .go đổi/mới>     # rỗng sau gofmt -w (CRLF do git-on-Windows)
```

**Kết quả:** PR [#11](https://github.com/draculemihawk123-ai/agent-workflow/pull/11), CI 6/6 xanh ngay lần
đầu. Squash-merge vào master tại `ab40c90`. Round 2 DONE — theo đúng lệnh user, vào thẳng V4-04 ngay sau
đây, không dừng lại hỏi thêm.
