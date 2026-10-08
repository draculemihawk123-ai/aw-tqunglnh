# Spike V10-17 — để nguyên skill, agent và hook của một bộ công cụ ngoài trong `.claude/` của repository đích

**Verdict: KHÔNG DÙNG** (cài đầy đủ `.claude/` gồm hook và settings). Bản chỉ có skill, không hook, chưa được kiểm qua aw nên **không khuyến nghị** (xem mục "Còn lại").

## Câu hỏi và bằng chứng

aw gọi `claude -p --input-format text --output-format stream-json --verbose --model … --permission-mode acceptEdits --effort … --max-budget-usd …`
(`internal/adapters/providers/claude/claude.go:211-222`) và **không** truyền cờ chọn nguồn cấu hình, nên CLI nạp cấu hình người dùng, dự án và local.

Hai lần đo, cùng một repository fixture: (A) gọi thẳng `claude -p` với đúng các cờ trên, ba bản repository (`v0` sạch, `v1` có đủ `.claude/` của bộ công cụ gồm khoảng 80 skill, 13 agent, hook, settings; `v2` chỉ hai skill); (B) chạy qua aw bằng
[`spike-claude-skills.py`](spike-claude-skills.py), một node AGENT của kit trên repository sạch / có `.claude/` (MAKER, `pathScopes: ["docs"]`) và một node CHECKER trên repository có `.claude/`.
Run id lần B: `41727f9b-1a23-4f8f-b5be-ca796dafec64` (v0-maker), `0a505049-119f-4135-bbec-cd89ad277968` (v1-maker), `543749cc-d1d2-48bc-ba70-7aac55657399` (v1-checker). Chi phí cả spike khoảng 0,6 USD.

| Câu hỏi | Trả lời | Bằng chứng |
|---|---|---|
| CLI có nạp skill của repository không, lúc nào? | **Có, ngay lúc khởi động phiên** (sự kiện `system init`), cùng agent và slash command trong `.claude/`. `system init` ở v1 liệt kê 103 skill (so với 20 ở repository sạch) và agent tự kể ra chúng ở lượt chạy v1 | A: `init.skills` có `ck-debug`, `fix`, … ở v1 và v2, không có ở v0; B: `finalMessage` của v1-maker và v1-checker liệt kê chúng |
| ContextSnapshot có ghi lại không? | **Không.** Snapshot chỉ liệt kê resource của aw (10 resource của agent BRAINSTORM); không có dòng nào nhắc tới skill, agent hay hook trong `.claude/`. Tri thức đó nằm ngoài manifest của attempt: không băm, không audit, không tái hiện được | B v1-maker: `aw context-snapshot show …` cho `resourceRefs` chỉ gồm `flow.*`, `rules.*`, `brainstorm.*`; 0 lần xuất hiện `ck-debug`, `.claude/skills` |
| Hook trong `.claude/settings.json` có chạy không, có xung đột không? | **Có chạy** trong chế độ không tương tác (`SessionStart`: hai hook, `hook_response … exit_code 0`). **Và làm hỏng attempt MAKER:** hook tự ghi `.claude/hooks/.logs/hook-log.jsonl` vào worktree nên attempt kết thúc `FAILED` với `SCOPE_VIOLATION` ("1 path(s) outside the granted scope") dù agent làm đúng việc | B v1-maker: `attempt FAILED SCOPE_VIOLATION … repo:.claude/hooks/.logs/hook-log.jsonl`; A: `hook_started`/`hook_response` ở v1 |
| Token và chi phí tăng bao nhiêu? | **Gấp khoảng 2,4 lần** cho một node nhỏ (B): 0,0702 → 0,1685 USD, token vào 66 K → 128 K. Ở A: cache tạo mới 6.020 → 25.298 token (+19 K) và 0,028 → 0,110 USD cho một lượt một câu hỏi; chỉ hai skill thì +204 token và +0,0009 USD (danh sách skill rất rẻ, hơn 80 skill thì đắt) | A: dòng `RESULT`; B: `usage` của từng lượt |

Phát hiện phụ (không phải câu hỏi ban đầu):

- **CHECKER cũng thấy `.claude/`.** Dù chạy trong thư mục scratch trống, node CHECKER nhận danh sách đủ skill của repository (qua mount `--add-dir`): 0,1172 USD cho một lượt không đọc file nào. Không có `SCOPE_VIOLATION` (read-only), nhưng chi phí và khoảng ngoài manifest như MAKER.
- **aw không cô lập cấu hình cấp người dùng.** Cả repository sạch (v0) vẫn nhận các skill trong `~/.claude/` của máy chạy worker (ví dụ `session-start-hook`), vì worker chuyển `HOME` cho CLI. Điều này cũng đúng với mọi lượt đo A0/A1 (như nhau ở hai bên nên không làm lệch so sánh, nhưng agent có thể thấy skill mà aw không biết).
- Agent **không gọi** skill nào trong cả ba lượt B (công cụ dùng: Bash, Write): nạp skill không đồng nghĩa dùng nó, và vì skill nằm ngoài snapshot nên không có cách buộc hay kiểm.

## Vì sao KHÔNG DÙNG

1. Hook ghi file vào worktree phá chốt `pathScopes` của aw (attempt MAKER hỏng); sửa bằng cách mở rộng scope sẽ gỡ chính lớp bảo vệ của aw.
2. Tri thức nạp qua `.claude/` không nằm trong ContextSnapshot: mất tính tái hiện và kiểm chứng, đúng cái aw tồn tại để đảm bảo. Hai lượt giống hệt nhau có thể nhận tri thức khác nhau chỉ vì `.claude/` đổi.
3. Chi phí tăng nhiều lần cho cả MAKER lẫn CHECKER mà agent không dùng skill đó.
4. Bộ skill nguyên bản mang quy trình riêng (kế hoạch, thư mục báo cáo, hỏi người) mâu thuẫn với quy trình do workflow của aw điều khiển.

Con đường đúng đã chọn ở V10: chắt lọc thành Layer/Skill của kit (vào ContextSnapshot, có version, có hash, nằm trong ngân sách context).

## Còn lại và đề xuất cho version sau (không làm trong V10)

- Chưa đo bản chỉ có skill, không hook, **qua aw** (chỉ đo trực tiếp ở A, +204 token cho hai skill). Vẫn không khuyến nghị vì skill nằm ngoài snapshot.
- Đề xuất cho core: (a) cho worker một tùy chọn cô lập cấu hình của CLI (người dùng và dự án) hoặc ghi băm của cây `.claude/` vào manifest của attempt; (b) loại các đường dẫn do hook ghi (`.claude/hooks/.logs/`) khỏi phép kiểm scope hoặc chạy CLI với hook tắt. Cần xác nhận cờ tương ứng của CLI trước khi thiết kế; chưa kiểm.
- Script `spike-claude-skills.py` đã được sửa phần đọc snapshot (dùng `aw context-snapshot show`) **sau** lần chạy ghi ở trên và chưa chạy lại; phần snapshot trong báo cáo này do chạy lệnh đó bằng tay trên bản cài của lượt v1-maker.
