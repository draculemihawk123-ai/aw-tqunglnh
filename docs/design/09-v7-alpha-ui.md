# V7 — Local web UI Alpha

> Entry: V6 API contract pass, UX artifact V6-00 hoàn tất và V0 đã `GO`.
>
> Exit: operator hoàn thành full Alpha journey bằng browser. Framework chỉ được chọn ở V7-01; các
> task sau phải tuân decision record đó.

> Phạm vi task mặc định: kế thừa mục 3 của `00-roadmap.md`.

## V7-01 — Quyết định UI framework

- **Mục tiêu:** chọn framework/toolchain bằng evidence, không theo sở thích ngầm.
- **Phụ thuộc:** V6.
- **Thực hiện:** spike nhỏ React/Angular hoặc candidate hợp lý với routing, form schema, SSE, graph,
  test, accessibility, build embedding; so tốc độ, ecosystem, bundle, team maintainability.
- **Verify:** cùng một mini screen/contract test cho candidates và decision matrix.
- **Hoàn thành khi:** ADR mới ghi choice, alternatives, versions, build/test commands; xóa spike không chọn.

## V7-02 — UI workspace, generated API client và quality gates

- **Mục tiêu:** scaffold theo ADR với strict TypeScript/lint/unit/component/E2E commands.
- **Phụ thuộc:** V7-01, V6-12.
- **Thực hiện:** API client từ contract, đọc per-start token từ bootstrap rồi chỉ giữ trong memory và
  gửi header cho mutation, route shell; token không đi vào URL/log/local/session storage, không copy DTO
  tay. Production UI build phải được chính `agentkit serve` phục vụ qua static handler của V6-01 — mọi
  E2E của V7 chạy qua binary-served UI, không qua dev server riêng.
- **Verify:** clean install/build/lint/test, client compatibility check, và smoke khẳng định
  `agentkit serve` phục vụ được bundle đã build.
- **Hoàn thành khi:** breaking API schema làm UI CI fail, và không journey nào của V7 phụ thuộc dev
  server.
- **Nguồn:** ADR-016.

## V7-03 — Design tokens và accessible primitives

- **Mục tiêu:** khóa spacing/color/type/focus/loading/error/empty patterns trước screen lớn.
- **Phụ thuộc:** V7-02.
- **Thực hiện:** buttons/forms/dialog/tabs/table/badge/toast/skeleton; light theme tối thiểu; semantic status colors.
- **Verify:** component tests, keyboard/focus và automated accessibility smoke.
- **Hoàn thành khi:** status không chỉ truyền bằng màu và error liên kết field.

## V7-04 — Application shell, routing và SSE state

- **Mục tiêu:** project navigation, reconnect banner và query invalidation dùng API authority.
- **Phụ thuộc:** V7-03.
- **Thực hiện:** route guards by selected project only, SSE reconnect/backoff theo JournalPosition,
  typed full-resync khi cursor quá cũ, stale/degraded projection indicator.
- **Verify:** navigation refresh, disconnect/reconnect, slow API component tests.
- **Hoàn thành khi:** browser cache không tự quyết runtime state.

## V7-05 — First-run Doctor screen

- **Mục tiêu:** operator biết DB/workspace/artifact/Git/provider nào ready hoặc blocked.
- **Phụ thuộc:** V7-04, V6-10F.
- **Thực hiện:** chỉ gọi Doctor API; readiness cards, remediation, rerun probe, provider capability/
  isolation profile; hiển thị adapter build là registered hay unregistered và cung cấp action probe →
  xác nhận → đăng ký theo ADR-022; observed fingerprint không được trình bày như registry admission;
  không tự probe filesystem/process và không hiển thị secret.
- **Verify:** healthy/degraded/blocked fixtures và keyboard tests.
- **Hoàn thành khi:** first run không cần mở terminal để hiểu lỗi cấu hình cơ bản, và nâng cấp provider
  CLI xử lý được hoàn toàn trong UI.
- **Nguồn:** ADR-022.

## V7-06 — Project/repository/component management

- **Mục tiêu:** create project, register/probe repo, view components/health.
- **Phụ thuộc:** V7-05.
- **Thực hiện:** canonical local locator input, state `REGISTERING|PROBING|ACTIVE|BLOCKED|DISABLED`,
  actionable retry, multi-repo project view và exact-version Engineering Pack assignment cho
  component; không hiển thị register response như ACTIVE sớm.
- **Verify:** success/invalid/dirty/partial probe E2E.
- **Hoàn thành khi:** UI dùng IDs từ API và không suy repo từ path/name.

## V7-07 — Definition catalog và version detail

- **Mục tiêu:** browse all definition kinds, versions, dependencies/hash/compatibility.
- **Phụ thuộc:** V7-04.
- **Thực hiện:** catalog filters, immutable version selector, SourceHash/CompiledSnapshotHash,
  dependency/resource/adapter pins và compatibility diagnostics.
- **Verify:** empty/list/filter/version selection/component tests.
- **Hoàn thành khi:** published version thể hiện immutable và run references exact version.

## V7-08 — Declarative editor, validate và publish

- **Mục tiêu:** author YAML/JSON, xem diagnostics/diff và publish confirm.
- **Phụ thuộc:** V7-07.
- **Thực hiện:** bounded editor, format-preserving draft local-only, validate locations, dependency pins,
  confirm SourceHash + CompiledSnapshotHash/dependency pins; không visual graph editing.
- **Verify:** invalid schema/duplicate/publish/idempotent version E2E.
- **Hoàn thành khi:** người dùng publish workflow mới không cần CLI.

## V7-09 — Kanban và filters

- **Mục tiêu:** project-level WorkItem projection với multi-repo badges/blocker/freshness.
- **Phụ thuộc:** V7-04, V7-06.
- **Thực hiện:** status columns, repository/component filter, pagination, typed transition action;
  drag/drop chỉ là command và rollback UI on reject.
- **Verify:** filter/reconnect/conflict/keyboard E2E.
- **Hoàn thành khi:** UI không tự set DONE và hiển thị agent claim khác verified.

## V7-10 — Create root/child WorkItem forms

- **Mục tiêu:** nhập đủ WHAT/DONE/scope/out-of-scope/workflow version.
- **Phụ thuộc:** V7-08, V7-09.
- **Thực hiện:** READ/WRITE repo/path selector, child subset UI, server diagnostics mapping.
- **Verify:** missing acceptance, invalid scope, concurrent version E2E.
- **Hoàn thành khi:** readiness errors actionable trước activate.

## V7-11 — Task detail overview và actions

- **Mục tiêu:** thấy intent, state, family/run, blockers, next valid approve/cancel/scope actions.
- **Phụ thuộc:** V7-10.
- **Thực hiện:** render server-provided valid actions/freshness, typed confirm dialogs và conflict
  refresh; không suy action từ status text hoặc tự chuyển DONE. Cancel hiển thị `CANCELLING` như một
  trạng thái tiến trình có thật, không báo đã hủy xong ngay khi request trả về. Ba hành động tách biệt
  trong UI: hủy run (`CancelRun`), hủy hẳn task (`CancelWorkItem`) và gỡ blocker để chạy lại
  (`ResolveWorkItemBlocker`) — không gộp thành một nút. Task detail chỉ hiển thị **summary + deep link**
  cho blocked activation; action retry thuộc V7-12 nơi có đủ ngữ cảnh node/attempt.
- **Verify:** active/blocked/waiting/done fixtures và optimistic conflict.
- **Hoàn thành khi:** destructive/privileged action có target/impact confirm.

## V7-12 — Runtime graph và timeline

- **Mục tiêu:** pinned definition overlay NodeRun/Attempt/routes/retries/checkpoints.
- **Phụ thuộc:** V7-11, V6-06A, V6-06B.
- **Thực hiện:** graph renderer read-only, accessible list fallback, timeline correlation/filter/failure
  detail; action `RetryBlockedActivation` trên một blocked activation, hiển thị đúng
  `TerminationReason` và — khi adapter drift không khôi phục được pin — trình bày `CancelRun` là valid
  action thay thế thay vì mời retry vô ích.
- **Verify:** fork/join/rework/restart fixtures; large graph performance smoke; fixture cho từng
  admission reason, và assert retry thất bại không sinh thêm blocked activation trong view.
- **Hoàn thành khi:** operator xác định được node đang chờ gì và route đã chọn.

## V7-13 — Workspace và source/diff/log viewers

- **Mục tiêu:** repository tabs với source/diff/log read-only, revision/scope/lease/quarantine status.
- **Phụ thuộc:** V7-11, V6-10B, V6-10C.
- **Thực hiện:** safe bounded source/diff/log viewer, per-repo status, reconcile link; không browser
  terminal trong Alpha.
- **Verify:** multi-repo, rename, binary/large output, stale revision, scope violation fixtures; assert
  không có control nào chạy lệnh trong workspace.
- **Hoàn thành khi:** focus repo không thay runtime scope.
- **Nguồn:** ADR-018.

## V7-13A — ReleaseSet actions và local commit

- **Mục tiêu:** thao tác release local có confirm rõ ràng và không có lối ra remote.
- **Phụ thuộc:** V7-13, V6-10D.
- **Thực hiện:** create/seal/abandon ReleaseSet, confirmed local commit, per-repository verdict và
  partial state; action release WorkspaceSet dispatch `RequestWorkspaceSetRelease` với confirm nêu rõ
  đây là intent bất đồng bộ, không phải thao tác tức thời.
- **Verify:** partial release, duplicate seal, stale revision; assert UI không render bất kỳ action
  push/PR/merge/force-push nào.
- **Hoàn thành khi:** quyết định release thực hiện được từ browser và remote Git vẫn vắng mặt.
- **Nguồn:** ADR-014.

## V7-14 — Evidence/artifact view

- **Mục tiêu:** criteria-level verdict, exact RevisionSet, output/hash/tamper state.
- **Phụ thuộc:** V7-11.
- **Thực hiện:** safe media/text preview, download, truncation indicator, PASS/FAIL/ERROR/N/A distinction.
- **Verify:** tampered/expired/redacted/large artifact E2E.
- **Hoàn thành khi:** raw HTML/script artifact không execute.

## V7-15 — Task chat và typed controls

- **Mục tiêu:** canonical message UI không lẫn approve/cancel/scope với free text.
- **Phụ thuộc:** V7-11.
- **Thực hiện:** append messages/attachments, attempt linkage, context-used indicator; separate control buttons/dialogs.
- **Verify:** ordering/retry/duplicate/SSE reconnect và message-not-approval tests.
- **Hoàn thành khi:** chat reload từ platform state, không provider transcript.

## V7-16 — Settings và run diagnostics

- **Mục tiêu:** provider/config/retention view và job/lease/recovery diagnostics.
- **Phụ thuộc:** V7-05, V7-12.
- **Thực hiện:** chỉ dùng allow-listed safe settings API hoặc restart-required guidance; hiển thị typed
  retention classes, correlation copy và remediation actions.
- **Verify:** redaction, invalid config, lease/provider/workspace failure fixtures.
- **Hoàn thành khi:** secret value không round-trip về browser.

## V7-17 — UI full-journey gate

- **Mục tiêu:** vận hành project→definition→task→run→approval→evidence→ReleaseSet→DONE bằng browser.
- **Phụ thuộc:** V7-02…V7-16 và V7-13A.
- **Thực hiện:** seed fake-provider environment qua public setup, chạy toàn journey bằng UI được
  `agentkit serve` phục vụ, cover onboarding BLOCKED/retry, adapter probe/register, projection resync,
  attachment, scope amendment, cancel một run, ReleaseSet/local commit và recovery action; không sửa
  DB/Git thủ công.
- **Verify:** deterministic E2E with fake providers, accessibility smoke, build/lint/unit/component tests,
  Windows/Linux browser CI.
- **Hoàn thành khi:** journey không cần CLI/SQLite/Git thủ công, không có interactive terminal và mọi
  blocked/error/degraded state có recovery path.
