# V3 — Báo cáo triển khai liên tục

> File làm việc cá nhân, không commit vào git (giống `baocaov0checklist.md`/`baocaov1checklist.md`/`baocaov2checklist.md`).
> Cập nhật sau mỗi task.
> Quy ước: khi vướng quyết định không tường minh trong task spec, tôi chọn theo khuyến nghị tốt nhất
> của tôi, ghi rõ lựa chọn + lý do ở đây, rồi tiếp tục — không dừng lại hỏi trừ khi thực sự chặn cứng
> (thiếu thông tin không thể suy luận được, hoặc rủi ro phá hoại/không thể đảo ngược). Kế thừa toàn bộ
> ràng buộc môi trường đã xác nhận ở V1/V2 (Go toolchain local tại `.tools/go1.27.0/go/bin/go.exe`,
> verify local trước khi push, CI thật là gate cuối).

## ⚠️ Repo đã đổi (2026-09-03, ~14:00 UTC)

Do tài khoản GitHub `taQuangLing` bị khoá Actions vì billing, đã chuyển remote `origin` sang
**`https://github.com/draculemihawk123-ai/agent-workflow`** (repo mới, `taQuangLing` được thêm làm
collaborator). Toàn bộ lịch sử `master` (41 commit, PR #1-#41 cũ) đã push sang. PR từ V3-07/08/09 mở lại
trên repo mới với **số PR bắt đầu lại từ #1** — không liên quan gì tới PR #39/40/41 cũ (những PR đó vẫn
còn treo trên repo cũ nhưng không merge, coi như bỏ). Từ đây về sau mọi PR/CI đều tham chiếu tới repo
`draculemihawk123-ai/agent-workflow`.

## Trạng thái tổng quan

| Task | Trạng thái | PR | CI | Ghi chú |
|---|---|---|---|---|
| V3-01 | DONE | [#33](https://github.com/taQuangLing/agent-workflow/pull/33) | ✅ (sau 1 rerun) | Giao subagent (worktree), đã tự verify độc lập kỹ (đọc lại toàn bộ diff chính + tự chạy lại build/vet/test/docs-coverage/gofmt trên máy, không chỉ tin báo cáo). CI fail 1 lần ở flake đã biết (`TestPool_TwoPoolsRaceRecovery_NoDuplicateProcessing`, package không đụng), rerun xanh |
| V3-02 | DONE | [#35](https://github.com/taQuangLing/agent-workflow/pull/35) | ✅ | Giao subagent (worktree), chạy SONG SONG với V3-03. Bị rate-limit giữa chừng (chưa commit gì lúc đó, resume không mất việc). Đụng số migration 0007 với V3-03 — đã rebase + đổi thành 0008. Agent bị kẹt loop "tự chờ Monitor không ai đánh thức" nhiều lần liên tiếp — tôi tự dừng agent (`TaskStop`) và tự theo dõi CI + verify + merge trực tiếp thay vì tiếp tục nhắc. Tự verify độc lập kỹ: đọc handler.go (2-phase transaction, CAS, idempotent early-return) + catalog.go's CAS UPDATE statement, build/vet/test/docs-coverage/gofmt trên máy |
| V3-03 | DONE | [#34](https://github.com/taQuangLing/agent-workflow/pull/34) | ✅ (sau 1 rerun) | Giao subagent (worktree). Bị rate-limit giữa chừng (đã có sẵn migration+sửa work.go chưa commit), resume qua SendMessage không mất việc. Agent cũng bị 1 lần "tự chờ background job không ai đánh thức" (giống lỗi đã biết) — nhắc lại và nó tự xử lý đúng. Tự verify độc lập kỹ: đọc lại `work.go`/`ValidateReadinessGate` đầy đủ, tự build/vet/test/docs-coverage/gofmt trên máy |
| V3-04 | DONE | [#36](https://github.com/taQuangLing/agent-workflow/pull/36) | ✅ | Giao subagent (worktree), CI xanh 6/6 ngay lần đầu (agent tự poll trực tiếp, không bị kẹt loop lần này). Tự verify độc lập: đọc kỹ `commands.go` (atomic 5-aggregate create, same-project/ACTIVE check, FK-safe persist order) + test rollback thật trên sqlite (assert 0 row ở cả 5 bảng + 0 event + 0 receipt). Agent tự phát hiện thêm 1 bug schema thật: `family_repository_scopes` thiếu cột `added_by` dù domain type đã yêu cầu — thêm migration 0009 |
| V3-05 | DONE | [#37](https://github.com/taQuangLing/agent-workflow/pull/37) | ✅ | Giao subagent, bị rate-limit giữa chừng (chưa commit gì, resume không mất việc). CI xanh 6/6, agent tự poll đồng bộ đúng (không kẹt loop lần này). Tự verify độc lập: đọc `CreateChildWorkItem` (tái dùng đúng `ValidateEffectiveScopes` có sẵn, không viết lại thuật toán subset) + test chứng minh bằng row-count thật (trước/sau) rằng không tạo WorkspaceSet/job mới |
| V3-06 | DONE | [#38](https://github.com/taQuangLing/agent-workflow/pull/38) | ✅ | Giao subagent, CI xanh 6/6 ngay lần đầu (kể cả race-detector). Tích hợp `gitworktree.Provider` thật (đã có từ trước) qua `ports.WorkspaceProvider`. Tự verify độc lập: đọc kỹ `handler.go` (idempotent early-return, lost-CAS coi là benign ở cả begin lẫn aggregate step, BLOCKED fail-fast không chờ sibling khác, row thành công vẫn giữ khi sibling fail) + chạy lại package mới 3 lần liên tiếp không thấy flake |
| V3-07 | DONE | [#3](https://github.com/draculemihawk123-ai/agent-workflow/pull/3) | ✅ (sau 1 rerun) | Code xong trên repo cũ (bị chặn billing lúc đó), chuyển sang repo mới thì CI chạy thật. Rebase lên master mới (đã có V3-08's migration 0015) làm lộ 1 lỗi cơ học: assertion đếm migration lệch (13 vs thực tế 14) — cả V3-07 và V3-08 cùng bump 1 dòng số độc lập nên git merge "im lặng" ra sai; tôi tự sửa (13→14) trước khi merge. CI fail 1 lần ở flake đã biết (`TestPool_TwoPoolsRaceRecovery_NoDuplicateProcessing`), rerun xanh. Tự verify độc lập: đọc `readiness.go` (Repository-level profile, argv-only CommandSpec, narrow blocker thay vì bảng `blockers` chung, GREEN/RED/ENVIRONMENT_ERROR 3 chiều) — thiết kế rất chỉn chu, đúng từng judgment call đã giao |
| V3-08 | DONE | [#2](https://github.com/draculemihawk123-ai/agent-workflow/pull/2) | ✅ | Code xong trên repo cũ (bị chặn billing), chuyển sang repo mới. CI xanh 6/6. Tự verify độc lập: đọc `ApproveScopeExpansion` — bug fix thật ở V3-06 (`workspaceprovision`'s WorkspaceSet không có đường quay lại khi đã READY) được xử lý gọn từ phía V3-08, có test end-to-end chứng minh, KHÔNG đụng file gốc |
| V3-09 | DONE | [#1](https://github.com/draculemihawk123-ai/agent-workflow/pull/1) | ✅ | Code xong trên repo cũ (bị chặn billing), chuyển sang repo mới. CI xanh 6/6. Audit-only PR (chỉ thêm test, 0 sửa code sản phẩm — xác nhận qua diff) — cả 5 tiêu chí production đã đúng sẵn từ trước, chỉ có `HeartbeatWriteLeases` thiếu test coverage, đã bổ sung 10 test thật |
| V3-10 | DONE | [#4](https://github.com/draculemihawk123-ai/agent-workflow/pull/4) | ✅ (sau 1 rerun flake đã biết) | Giao subagent (worktree). Tự verify độc lập: đọc `ClassifyReconciliation` (quyết định ACCEPT/BLOCK/RECREATE thuần, có căn cứ rõ ràng từ tín hiệu Inspect thật, không suy đoán) + archtest boundary mới (public command không được import I/O trực tiếp) + test chứng minh workspace bị quarantine thật sự không cấp WriteLease/Release được (dùng đúng code V3-09 đã merge) |
| V3-11 | DONE | [#5](https://github.com/draculemihawk123-ai/agent-workflow/pull/5) | ✅ (sau 1 rerun flake `workerpool` khác) | Giao subagent, bị rate-limit giữa chừng (chưa commit, resume không mất việc). Agent lại kẹt loop chờ background CI — tôi tự theo dõi/verify/merge thay (không cần TaskStop lần này, chỉ 1 lần kẹt). Đúng phạm vi hẹp đã giao: CHỈ command public `RequestWorkspaceSetRelease`, xác nhận KHÔNG có `ExecuteWorkspaceSetRelease` nào được implement (chỉ xuất hiện trong comment giải thích ngoài phạm vi). Tự verify độc lập: đọc kỹ eligibility check (authority gọi TRƯỚC khi mở write transaction, CAS qua ExpectedVersion, idempotency 2 lớp: business-check + job idempotency-key) |
| V3-12 | DONE | [#6](https://github.com/draculemihawk123-ai/agent-workflow/pull/6) | ✅ (6/6 ngay lần đầu) | Tự làm trực tiếp (không giao subagent) — task đóng cả phase V3, giống cách làm V2-12. Test mới `internal/integration/workplane_test.go` (`TestProjectWorkspaceGate`), assert trực tiếp GC-ACC-06/07/08 |

## V3 — HOÀN THÀNH (2026-09-04, ~19:18 UTC)

Toàn bộ 12 task (V3-01..V3-12) đã DONE, merge vào `master` trên repo `draculemihawk123-ai/agent-workflow`
(đã chuyển từ `taQuangLing/agent-workflow` giữa chừng do billing — xem mục "Repo đã đổi" ở đầu file). PR
cuối: [#1](https://github.com/draculemihawk123-ai/agent-workflow/pull/1)-[#6](https://github.com/draculemihawk123-ai/agent-workflow/pull/6)
trên repo mới (V3-07/08/09 mở lại từ PR cũ #41/#40/#39 trên repo cũ đã bỏ). V3-12 tự đóng gate bằng
integration test thật: 2 root family cùng chạm 1 repo (GC-ACC-06), 1 family 2-repo tạo WorkspaceSet 2
worktree + child tái dùng đúng (GC-ACC-07), song song lease khác repo + serialize cùng repo (GC-ACC-08),
cộng restart và release-eligibility primitive (V3-11). Completion/evidence/ReleaseSet thật để lại cho V5
đúng như phạm vi task tự ghi. Không có task nào còn lại trong phase này.

**Tổng kết sự cố lớn của cả phase**: 2 lần đụng migration (số + assertion đếm) giữa các task chạy song
song, 1 lần billing GitHub Actions chặn toàn bộ CI buộc phải chuyển repo/tài khoản, nhiều lần agent kẹt
loop "tự chờ background job không ai đánh thức" (đã có quy trình xử lý: nhắc 1-2 lần, không được thì
`TaskStop` và tự làm nốt). Không có bug thật nào lọt qua tới merge — mọi bug (kể cả ở code V1-04A/V2-02/
V3-06 đã merge từ trước) đều được các agent hoặc chính tôi tự bắt và sửa trước khi merge.

## Sự cố billing GitHub Actions (đã giải quyết — xem mục "Repo đã đổi" ở đầu file)

Tóm tắt: tài khoản `taQuangLing` bị khoá Actions do vấn đề thanh toán (~13:03 UTC 2026-09-03), mọi CI job
fail ngay lập tức 0 step. Đã chuyển sang repo `draculemihawk123-ai/agent-workflow` (theo yêu cầu user) —
CI hoạt động bình thường trên repo mới, cả 3 PR V3-07/08/09 đã merge thành công (số PR mới #1/#2/#3, xem
bảng trên). PR #39/#40/#41 cũ trên repo `taQuangLing` bị bỏ, không merge.

Về nghi vấn flake ở CI push lên `master` ngay sau khi merge V3-06 (trên repo cũ, TRƯỚC khi billing bị
khoá) — 2 test `TestWriteLeaseRequiresItsOriginalActiveJobFence`/`TestEndToEnd_Restart_RemainingJobCompletesAndSetReachesReady`
fail lần đó: đã xác nhận đây đúng là flake do tải/timing, KHÔNG phải regression thật — bằng chứng: cả 2
test này pass sạch trong mọi lần verify độc lập của tôi sau đó (V3-07 verify, V3-08 verify) và trong CI
thật trên repo mới (V3-07/08/09 đều xanh 6/6, có lần phải rerun 1 job nhưng luôn là flake `workerpool` đã
biết, không phải 2 test này).

## Sơ đồ phụ thuộc (để quyết định song song hóa)

```
V3-01 → V3-02 ─────────────────┐
      └→ V3-03 → V3-04 → V3-05 ─┼→ V3-08 → V3-11 → V3-12
                        └→ V3-06 ┴→ V3-09 → V3-10 ┘
                                 └→ V3-07 ┘ (cần V3-02 + V3-06)
```

- V3-02 và V3-03 đều chỉ cần V3-01 → song song được sau khi V3-01 merge.
- V3-05 và V3-06 đều chỉ cần V3-04 → song song được.
- V3-07 cần V3-02 + V3-06; V3-09 chỉ cần V3-06 → V3-07/V3-09 song song được sau khi cả hai điều kiện xong.
- V3-08 cần V3-05 + V3-06.
- V3-10 cần V3-09; V3-11 cần V3-07 + V3-10.
- V3-12 (gate đóng phase) cần toàn bộ V3-01..V3-11.

## Quyết định/khuyến nghị đã áp dụng khi vướng

- **V3-01, "Project/Repository/Component persistence" — giao subagent (brief tự nghiên cứu kỹ go-core-spec
  §4.1/§8, design doc §6.1/§6.2, HE-03-M04, HE-06-M05 trước khi viết). Tự verify độc lập sau khi subagent
  báo xong: đọc lại migration + regression test, `project.go`/`component.go`, `catalog.go` (sqlite),
  `commands.go` (app layer) — tự chạy lại build/vet/test/docs-coverage/gofmt trên máy (không chỉ tin CI/báo
  cáo). Mọi claim kỹ thuật đều verify đúng.**
  - **Kỹ thuật migration cho CHECK constraint của `repositories.status`**: dự định ban đầu (trong brief của
    tôi) là "safe rebuild" chuẩn SQLite (CREATE bảng mới → INSERT SELECT copy dữ liệu → DROP bảng cũ →
    RENAME). Agent tự phát hiện qua thực nghiệm thật (có test regression giữ lại làm bằng chứng,
    `migration_0006_test.go`): dưới `PRAGMA foreign_keys=ON`, `DROP TABLE repositories` tự làm "xoá từng
    row rồi check FK" TRƯỚC khi xoá bảng — nên thất bại y hệt dù có "rebuild dance" hay không, MỘT KHI có
    bất kỳ row nào ở bảng con (`family_repository_scopes`/`repository_workspaces`) tham chiếu tới. Cách
    sửa thật (tắt `PRAGMA foreign_keys=OFF` quanh rebuild) không dùng được vì toàn bộ migration chạy trong
    1 transaction chung (`Store.Migrate`) và pragma này là no-op khi transaction đã mở. Do CHƯA TỪNG có
    row thật nào (ngoài fixture test) được ghi vào 3 bảng này trước task này (V3-01 là command ĐẦU TIÊN tạo
    row `repositories`), `DROP TABLE; CREATE TABLE` đơn giản (giống 0002/0003) là an toàn — agent đã viết
    2 test riêng chứng minh cả 2 chiều: migrate sạch trên DB mới thành công, VÀ migrate trên DB có row thật
    tham chiếu chéo THẤT BẠI RÕ RÀNG (không silent corrupt) đúng như dự đoán.
  - **CAS/`ExpectedVersion`**: task này không có mutation nào trên aggregate đã tồn tại (RegisterRepository/
    AssignComponentPack/CreateComponent đều là pure create) — nên KHÔNG có call site thật để test CAS.
    Agent không tự bịa ra 1 update command giả chỉ để test — quyết định đúng, ghi rõ lý do, để CAS thật sự
    xuất hiện tự nhiên ở task update đầu tiên sau này.
  - **`ComponentPackAssignment.PackVersionID`**: string trần (không import
    `engineeringpack.EngineeringPackVersionID`) — đã tự verify: `project` không thể import `engineeringpack`
    vì sẽ tạo cycle thật (`engineeringpack` → `definition` → `project`, do `definition.Scope.ProjectID`
    đã import `project` từ trước) — xác nhận đúng bằng grep trực tiếp.
  - **`AssignComponentPack` dùng full Command+Receipt+Event envelope** (được nêu tên tường minh trong bảng
    command của go-core-spec), **`CreateComponent` thì KHÔNG** (không được nêu tên, chỉ dựa vào
    `UNIQUE(repository_id, path)` làm replay protection) — phân biệt hợp lý, nhất quán với cách "Nguồn"
    field hoạt động trong repo này (chỉ command được cite mới bắt buộc theo full rigor).
  - **1 phát hiện out-of-scope**: `command_handler_example_test.go`'s `handleCreateProject` (ví dụ minh hoạ
    từ V1-06) reject command installation-scoped, nhưng ADR-025 lại yêu cầu `CreateProject` phải
    installation-scoped (vì Project chưa tồn tại để scope theo nó) — mâu thuẫn thật nhưng ở file
    test-only/minh hoạ, không phải `CreateProject` thật (task này tự viết `catalog.CreateProject` đúng,
    không có ràng buộc sai này). Không sửa (ngoài phạm vi V3-01), chỉ ghi nhận.
  - Verify local đầy đủ (build/vet/test toàn repo/docs-coverage/gofmt) — cả agent và tôi đều chạy độc lập,
    khớp nhau. CI xanh 6/6 sau 1 lần rerun do flake đã biết.

- **V3-02 + V3-03, chạy song song — 2 sự cố quy trình đáng ghi lại:**
  - **Cả 2 agent bị rate-limit giữa chừng** (session limit, không phải lỗi code) — đã xác nhận không mất
    việc trước khi resume: V3-02 chưa commit gì (branch rỗng), V3-03 đã có sẵn migration + sửa `work.go`
    còn nguyên trong worktree (chưa commit). Resume bằng `SendMessage` (không phải `Agent` mới) — đúng
    protocol đã thiết lập từ trước.
  - **V3-03 lặp lại đúng lỗi "tự launch background job rồi dừng, không ai đánh thức"** đã gặp nhiều lần
    trong session này (trước đây là với `gh run watch`, lần này là `go test ./...` tự chạy nền) — nhắc lại
    qua `SendMessage` là nó tự kiểm tra và tiếp tục đúng.
  - **Phát hiện xung đột migration SỐ 0007 giữa V3-02 và V3-03** (cả 2 agent tách branch từ CÙNG một
    master trước khi cái nào merge, nên cả 2 đều tự chọn "0007" làm số migration tiếp theo — hợp lý tại
    thời điểm mỗi agent tự quyết, nhưng khi V3-03 merge trước thì V3-02 sẽ đụng số ngay lập tức vì
    `loadMigrations()` (migrations.go) reject cứng "duplicate migration version"). Tôi tự phát hiện bằng
    cách kiểm tra file list của PR #35 SAU KHI merge PR #34, trước khi merge #35 — đã báo V3-02 rebase +
    đổi số thành 0008 trước khi merge, tránh việc merge xong mới vỡ CI. Bài học quy trình: khi chạy nhiều
    task song song có khả năng cùng thêm migration mới, phải chủ động kiểm tra đụng số TRƯỚC khi merge task
    thứ hai, không chỉ tin CI của mỗi PR riêng lẻ (CI mỗi PR chạy trên snapshot master tại thời điểm mở PR,
    không thấy được migration mới do task song song khác thêm sau đó).
  - **V3-02 (thiết kế thật, đã tự đọc kỹ code, không chỉ tin báo cáo)**: `Handler.Handle` tách rõ 2
    transaction quanh I/O git thật (transaction 1: CAS REGISTERING→PROBING; I/O git chạy NGOÀI transaction;
    transaction 2: CAS PROBING→ACTIVE|BLOCKED + tạo Component discovery + ghi `repository_probe_attempts`,
    cùng 1 transaction). Handler trả `nil` (job SUCCEEDED) cho CẢ HAI kết quả ACTIVE và BLOCKED — đúng
    nguyên tắc "job làm đúng việc của nó (probe) khác với việc probe tìm ra repo hỏng"; chỉ lỗi thật (payload
    hỏng, persistence lỗi, prober trả lỗi không phân loại được) mới để job fail/retry. CAS thật
    (`TransitionRepositoryStatus`) dùng đúng 1 câu `UPDATE ... WHERE id=? AND status=? AND version=?`, phân
    biệt rõ "not found" và "stale/conflict" (`ports.ErrOptimisticConflict` mới). "Active probe idempotent"
    được đảm bảo ở 2 lớp: DB-level `UNIQUE(job_id)` trên `repository_probe_attempts`, VÀ handler tự early-
    return khi repository đã ở trạng thái terminal (ACTIVE/BLOCKED/DISABLED) — đúng cho trường hợp job cũ
    bị crash-reclaim sau khi đã hoàn tất.
  - **Sự cố quy trình đáng ghi lại thứ 3**: agent V3-02 bị kẹt trong loop tự launch Monitor rồi dừng chờ,
    lặp lại RẤT NHIỀU lần liên tiếp dù đã nhắc qua `SendMessage` 2 lần (không thoát loop được — khác V3-03,
    lần đó nhắc 1 lần là xử lý đúng ngay). Quyết định: dừng hẳn agent bằng `TaskStop` (việc thật đã xong —
    code/PR/CI đều đã ở trạng thái tốt, agent chỉ còn "chờ" một cách vô ích) rồi tự tôi theo dõi CI + verify
    + merge trực tiếp. Bài học: khi 1-2 lần nhắc không phá được loop "tự chờ notification không tới", đừng
    tiếp tục nhắc thêm — dừng agent và tự làm nốt phần còn lại (thường chỉ còn chờ CI + merge, việc nhẹ).

- **V3-07/V3-08/V3-09 chạy song song lần 2 — sự cố billing + đổi repo + đụng migration-count assertion lần
  2 (khác lần V3-02/V3-03: lần này KHÔNG phải đụng SỐ migration, mà đụng ASSERTION ĐẾM migration).**
  - **Billing GitHub Actions**: xem mục riêng "Repo đã đổi" ở đầu file — tài khoản `taQuangLing` bị khoá
    Actions giữa chừng lúc cả 3 PR đã code xong. User quyết định chuyển sang tài khoản/repo khác
    (`draculemihawk123-ai/agent-workflow`) thay vì chờ xử lý billing trên tài khoản cũ. Tôi: đổi
    `git remote`, push toàn bộ lịch sử + 3 nhánh, mở lại PR trên repo mới (bị auto-mode chặn 1 lần, user
    xác nhận trong chat KHÔNG đủ vượt qua — cần user tự đăng nhập `gh auth login` bằng tài khoản mới thì
    lệnh `gh pr create` mới chạy được).
  - **Đụng assertion đếm migration (0013/0014/0015 gán số riêng đã tránh được đụng SỐ, nhưng KHÔNG tránh
    được đụng ASSERTION)**: V3-07 (viết 0014) và V3-08 (viết 0015) đều tự bump dòng `migrationCount != N`
    từ giá trị base LÊN ĐÚNG 1 (mỗi nhánh tự cộng riêng dựa trên nhánh của mình), nên cả 2 diff đều đổi
    cùng 1 dòng thành CÙNG MỘT giá trị literal — git merge/rebase "im lặng" chấp nhận (không coi là
    conflict vì text giống hệt nhau) nhưng kết quả SAI (thiếu 1). Phát hiện khi tôi tự rebase nhánh V3-07
    lên master (đã có V3-08) rồi chạy lại test: 3 test fail đúng ngay "migration count = 14, want 13". Tự
    sửa (13→14) ở cả `db_test.go` và `unitofwork_test.go`, push lại, CI chạy lại xanh 6/6 rồi mới merge —
    KHÔNG merge dựa trên verify-local-một-mình. Bài học mới: gán số migration cố định trước chỉ giải quyết
    được đụng SỐ, không giải quyết được đụng ASSERTION ĐẾM — mỗi lần merge task có thêm migration mới vào
    sau 1 task song song khác cũng thêm migration, PHẢI tự rebase + chạy lại test trước khi tin CI cũ.
  - **Xác nhận lại nghi vấn flake V3-06** (2 test fail ở CI push master trên repo cũ, TRƯỚC billing): cả 2
    test này pass sạch trong verify-độc-lập của tôi (V3-07, V3-08) và trong CI thật trên repo mới nhiều
    lần — xác nhận chắc chắn là flake do tải/timing, không phải regression thật.

- **V3-12, "V3 multi-repository acceptance gate" — tự làm trực tiếp (không giao subagent), giống V2-12,
  vì đây là task đóng cả phase.**
  - **Kịch bản test** (`internal/integration/workplane_test.go`, `TestProjectWorkspaceGate`): 1 project 2
    repo thật (git fixture thật) → 2 root TaskFamily (family-1 chỉ scope repo-a; family-2 scope cả 2 repo,
    CỐ TÌNH cùng chạm repo-a với family-1) → chờ cả 2 WorkspaceSet READY qua pool thật + `gitworktree.Provider`
    thật → child WorkItem dưới family-2 (assert KHÔNG tạo WorkspaceSet/job mới) → scope expansion thêm
    repo-b vào family-1 (assert WorkspaceSet reopen đúng, RevisionSet mở rộng đúng) → dừng pool → RESTART
    (đóng/mở lại sqlite file, assert state + RevisionSet content hash giữ nguyên) → parallel leases (2
    sibling khác repo cùng giữ lease được; cùng repo bị `ErrWriteLeaseConflict`) → release: `RequestWorkspaceSetRelease`
    (V3-11) bị từ chối khi còn active lease, thành công sau khi release lease.
  - **3 invariant chính thức được assert trực tiếp**: GC-ACC-06 (2 root cùng repo-a → 2 `RepositoryWorkspace`
    row độc lập, khác locator), GC-ACC-07 (family scope 2 repo → WorkspaceSet có đúng 2 worktree; child tái
    dùng đúng WorkspaceSet, không tạo mới), GC-ACC-08 (2 sibling khác repo chạy song song được; cùng repo
    bị serialize).
  - **Đúng ranh giới "Hoàn thành khi"**: KHÔNG cố chứng minh completion/evidence/ReleaseSet thật (để lại
    cho V5 đúng như task tự ghi) — bước "release" chỉ test đúng phạm vi V3-11 đã xây (primitive eligibility
    với fake authority), không có executor thật nào được gọi (vì V3-11 không xây executor).
  - **Bug thật tự bắt trong khi viết test (không phải ở code V3-01..V3-11 đã merge, mà là thiếu 1 fixture
    helper)**: `write_leases.holder_attempt_id` là FK thật tới `execution_attempts`, mà bảng này lại cần cả
    chain `workflow_definitions→versions→runs→node_runs` — đây là hạ tầng V4/V5 chưa có writer thật nào,
    chỉ tồn tại để test dùng raw SQL fixture. Đã có sẵn `sqlite.SeedFixtureWriteLease` (V3-11 thêm) nhưng nó
    tự làm luôn cả EnqueueJob+ClaimJob+AcquireWriteLeases trong 1 lần, không trả lại `WriteLeaseGrant` nên
    không dùng lại được cho kịch bản cần nhiều lease độc lập (parallel + conflict + release). Thêm 1 hàm
    fixture MỚI thuần additive `sqlite.SeedFixtureExecutionAttempt` (chỉ seed chain tới `execution_attempts`,
    để caller tự làm phần EnqueueJob/ClaimJob/AcquireWriteLeases) — KHÔNG sửa `SeedFixtureWriteLease` đã có,
    zero rủi ro cho code cũ.
  - Verify local đầy đủ: build/vet/test toàn repo (bao gồm chạy riêng `TestProjectWorkspaceGate` 3 lần liên
    tiếp không thấy flake), `go run ./cmd/docs-coverage-check` (debt = 0), gofmt sạch (đã xác nhận phần bị
    flag chỉ là CRLF noise).
