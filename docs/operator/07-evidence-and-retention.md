# Evidence và retention

## Evidence — nó là gì, dạng thật

Mỗi lần thực thi gate/command/agent mà bản cài này chạy đều tạo ra một hoặc nhiều dòng Evidence — một bản ghi bền
vững, truy vấn được về những gì đã xảy ra, được xâu chuỗi tới chính xác (các) revision repository liên quan:

```bash
aw evidence list --project-id <id> <workItemId> [--run-id <id>] [--kind <evidenceKind>]
aw artifact get <workItemId> <evidenceId> <artifactId> --project-id <id> --output <path|->
```

Một mục evidence thật (đã kiểm chứng trong run thành công của [01-quickstart.md](01-quickstart.md)):

```json
{"evidenceId": "<attemptId>:<evidenceKind>", "projectId": "...", "workItemId": "...", "runId": "...",
 "nodeRunId": "...", "attemptId": "...", "kind": "QUICKSTART_OUTPUT_VERIFIED", "verdict": "PASS",
 "artifactReferences": ["<artifactId>"],
 "revisions": [{"repositoryId": "...", "vcsObjectId": "<commit>", "workspaceGeneration": 1}],
 "revisionSetHash": "sha256:...", "policyVersion": "sha256:...", "createdAt": "..."}
```

Trường `revisions` của mỗi dòng evidence nêu CHÍNH XÁC (các) commit của repository mà evidence được tạo ra trên
đó — chuỗi mà AK-ARCH-021 yêu cầu (WorkItem → Run → NodeRun → Attempt → invocation → artifact → revision
repository chính xác) là thật, truy vấn được, và đầu-cuối: không mắt xích nào trong chuỗi này là một bản tóm tắt
kiểu "cứ tin tôi đi".

Artifact output thật của một `MACHINE_GATE` là một document `application/vnd.agentkit.gate-result+json` — nội
dung thật mà quickstart này lấy về là `{"overallVerdict":"PASS","criteria":[{"name":"...", "evidenceKey":
"...", "verdict":"PASS"}]}` (hoặc `"ERROR"` kèm một chuỗi `"detail"` thật giải thích chuyện gì đã sai, ví dụ lỗi
"%1 is not a valid Win32 application" mà mục troubleshooting của quickstart đã đi qua).

## Các retention class

Hai class, tập đóng (`internal/domain/artifact/artifact.go`):

- **`CANONICAL_CONTEXT`** — không bao giờ hết hạn. Các artifact hội thoại/ngữ cảnh canonical, và (tính tới
  Alpha) mọi artifact mà các producer thật của codebase này (`internal/app/message`, mọi node executor của
  `internal/app/runtime`) thực sự tạo ra.
- **`RAW_OUTPUT_TEMP`** — TTL 7 ngày tính từ lúc tạo. Output thô của provider/command không nhằm sống mãi mãi.
  **Tính tới bản Alpha này, không có đường code thật nào trong codebase thực sự tạo ra loại này** — retention
  class, phép tính TTL 7 ngày, và worker sweep sẽ dọn nó đều là thật và được test kỹ, nhưng hiện chưa có producer
  thật nào được nối vào. Đừng ngạc nhiên nếu bạn chưa thấy một artifact loại này trong một bản cài thật.

## Retention sweep

Một job worker định kỳ (`aw worker --sweep-interval`, mặc định 1 giờ) xóa các artifact `RAW_OUTPUT_TEMP` đủ điều
kiện — một artifact chỉ đủ điều kiện khi đã quá hạn ân hạn 7 ngày VÀ không ở trạng thái `Attached` VÀ không bị
`Hold` VÀ không dùng chung locator content-addressed với bất kỳ artifact nào ĐANG được attach/hold. Một lần sweep
không bao giờ chạm vào `CANONICAL_CONTEXT`, không bao giờ xóa một artifact đang được tham chiếu hoặc bị hold, và
không bao giờ xóa DÒNG DATABASE ngay cả với artifact đã bị purge — chỉ xóa phần byte thật của nó (một audit trail
được giữ mãi mãi; `ADR-017`).

```bash
aw settings update ...   # --sweep-interval là flag khởi động của serve/worker, không phải trường safe-setting thay đổi được lúc runtime
```

## Redaction

Một giá trị secret từng đi qua output của một command/agent thật (stdout/stderr, hoặc một artifact được lưu) sẽ
được redact trước khi được persist — đã được kiểm chứng bởi test quét secret đầu-cuối thật của repo này
(`internal/integration/v5accept`): một secret thật được dò tìm trên mọi object thật trong artifact store VÀ trên
chính file SQLite thô trên đĩa, xác nhận không có lần xuất hiện nào chưa bị redact ở bất cứ đâu được lưu bền vững.
