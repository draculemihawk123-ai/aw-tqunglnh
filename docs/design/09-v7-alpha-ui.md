# V7 — Local web UI Alpha

> Entry: V6 API contract pass và V0 đã `GO`.
>
> Exit: operator hoàn thành full Alpha journey bằng browser. Framework chỉ được chọn ở V7-01; các
> task sau phải tuân decision record đó.

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
- **Thực hiện:** API client từ contract, route shell, build output integration strategy; không copy DTO tay.
- **Verify:** clean install/build/lint/test và client compatibility check.
- **Hoàn thành khi:** breaking API schema làm UI CI fail.

## V7-03 — Design tokens và accessible primitives

- **Mục tiêu:** khóa spacing/color/type/focus/loading/error/empty patterns trước screen lớn.
- **Phụ thuộc:** V7-02.
- **Thực hiện:** buttons/forms/dialog/tabs/table/badge/toast/skeleton; light theme tối thiểu; semantic status colors.
- **Verify:** component tests, keyboard/focus và automated accessibility smoke.
- **Hoàn thành khi:** status không chỉ truyền bằng màu và error liên kết field.

## V7-04 — Application shell, routing và SSE state

- **Mục tiêu:** project navigation, reconnect banner và query invalidation dùng API authority.
- **Phụ thuộc:** V7-03.
- **Thực hiện:** route guards by selected project only, SSE reconnect/backoff/last-event ID, stale projection indicator.
- **Verify:** navigation refresh, disconnect/reconnect, slow API component tests.
- **Hoàn thành khi:** browser cache không tự quyết runtime state.

## V7-05 — First-run Doctor screen

- **Mục tiêu:** operator biết DB/workspace/artifact/Git/provider nào ready hoặc blocked.
- **Phụ thuộc:** V7-04.
- **Thực hiện:** readiness cards, remediation, rerun probe, provider version/capability; không hiển thị secret.
- **Verify:** healthy/degraded/blocked fixtures và keyboard tests.
- **Hoàn thành khi:** first run không cần mở terminal để hiểu lỗi cấu hình cơ bản.

## V7-06 — Project/repository/component management

- **Mục tiêu:** create project, register/probe repo, view components/health.
- **Phụ thuộc:** V7-05.
- **Thực hiện:** canonical local locator input, async onboarding result, multi-repo project view.
- **Verify:** success/invalid/dirty/partial probe E2E.
- **Hoàn thành khi:** UI dùng IDs từ API và không suy repo từ path/name.

## V7-07 — Definition catalog và version detail

- **Mục tiêu:** browse all definition kinds, versions, dependencies/hash/compatibility.
- **Phụ thuộc:** V7-04.
- **Verify:** empty/list/filter/version selection/component tests.
- **Hoàn thành khi:** published version thể hiện immutable và run references exact version.

## V7-08 — Declarative editor, validate và publish

- **Mục tiêu:** author YAML/JSON, xem diagnostics/diff và publish confirm.
- **Phụ thuộc:** V7-07.
- **Thực hiện:** bounded editor, format-preserving draft local-only, validate locations, dependency pins,
  confirm content hash; không visual graph editing.
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
- **Verify:** active/blocked/waiting/done fixtures và optimistic conflict.
- **Hoàn thành khi:** destructive/privileged action có target/impact confirm.

## V7-12 — Runtime graph và timeline

- **Mục tiêu:** pinned definition overlay NodeRun/Attempt/routes/retries/checkpoints.
- **Phụ thuộc:** V7-11.
- **Thực hiện:** graph renderer read-only, accessible list fallback, timeline correlation/filter/failure detail.
- **Verify:** fork/join/rework/restart fixtures; large graph performance smoke.
- **Hoàn thành khi:** operator xác định được node đang chờ gì và route đã chọn.

## V7-13 — Workspace/diff/lease view

- **Mục tiêu:** repository tabs với revision/diff/scope/lease/quarantine.
- **Phụ thuộc:** V7-11.
- **Thực hiện:** safe text diff viewer, per-repo status, recovery action link; không browser terminal trong Alpha.
- **Verify:** multi-repo, rename, binary/large diff, scope violation fixtures.
- **Hoàn thành khi:** focus repo không thay runtime scope.

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
- **Thực hiện:** safe editable config subset or restart-required guidance, correlation copy, remediation actions.
- **Verify:** redaction, invalid config, lease/provider/workspace failure fixtures.
- **Hoàn thành khi:** secret value không round-trip về browser.

## V7-17 — UI full-journey gate

- **Mục tiêu:** vận hành project→definition→task→run→approval→evidence→DONE bằng browser.
- **Phụ thuộc:** V7-02…V7-16.
- **Verify:** deterministic E2E with fake providers, accessibility smoke, build/lint/unit/component tests,
  Windows/Linux browser CI.
- **Hoàn thành khi:** journey không cần CLI/SQLite/Git thủ công và mọi blocked/error state có recovery path.
