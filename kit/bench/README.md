# Bộ đo của kit (V10-02)

Đo xem tri thức trong kit có giúp agent làm tốt hơn không, **theo từng node**, với cùng model, cùng bài toán và cùng môi trường.
Quy trình đo nằm ở [docs/design/13-v10-kit-knowledge.md](../../docs/design/13-v10-kit-knowledge.md) (V10-02, V10-12, V10-18).

| Thành phần | Việc |
|---|---|
| `issue-tracker-mvp/{contracts,api,web}.bundle` | Fixture: ba repository của hướng dẫn issue tracker ở trạng thái MVP (sau C-01, A-01, W-01), nhánh `main`. Lấy từ lần chạy thật của hướng dẫn: contracts `f598e9b`, api `8971f33`, web `64df325`. |
| `work-items/review-comments.json` | Task review độc lập cuối workload (`wf-code-review`). |
| `rubric.md` | Rubric chấm tài liệu BRAINSTORM, SPEC, DESIGN, PLAN, SYNC (thang 1 đến 5). |
| `../scripts/kit-bench.sh` | Chạy workload với Claude CLI **thật**. |
| `../scripts/kit-metrics.py` | `collect` (một lượt), `compare` (hai nhãn), `blind` và `unblind` (chấm tài liệu không biết nhãn). |
| `runs/` | Kết quả chạy cục bộ (git bỏ qua). |
| `reports/` | Báo cáo đã chốt: `A0.md` (kit trước V10), `A1.md` (sau V10-11), `A2.md` (kit cuối). |

## Golden workload "bình luận"

Mỗi lượt dựng một bản cài aw mới (database, worker, worktree riêng), clone ba repository từ fixture, publish project của hướng dẫn
issue tracker với kho cần đo, rồi chạy:

1. **F-00** `wf-feature-definition`: BRAINSTORM, SPEC, GATE A, DESIGN, `check-docs`, GATE B. Commit tài liệu, `split-tasks.sh` chia thành T-01, T-02, T-03.
2. **T-01** (contracts, `wf-task-delivery-contracts`), **T-02** (api), **T-03** (web): FRAME, PLAN, BUILD, gate1, quality, GATE 2, SYNC. Mỗi task commit trước khi sang task sau.
3. **Review** độc lập (`wf-code-review`) trên cả ba repository.

Qua đủ các node BRAINSTORM, SPEC, DESIGN, FRAME, PLAN, BUILD, SYNC và REVIEW của kit. Cổng người duyệt do script tự duyệt `approved`
(chỉ trong bench; ghi lại để không ai nhầm là chất lượng người duyệt). Agent chọn `needs_info` thì lượt dừng và ghi `STOPPED`; run hỏng
được chạy lại đúng một lần.

## Cách chạy

```bash
# kho hiện tại, 2 lượt, song song (tốn tiền thật: khoảng 3 USD mỗi lượt)
kit/scripts/kit-bench.sh --label A1 --runs 2 --parallel

# kho ở một commit khác (git archive ra thư mục tạm): kit/ và docs/guides/issue-tracker/ cùng một commit
kit/scripts/kit-bench.sh --label A0 --runs 2 --tree <commit> --parallel

# thử trước, không tốn tiền: chỉ dựng môi trường (cần aw và agent giả lập fake-claude, hoặc Claude CLI thật)
kit/scripts/kit-bench.sh --label thu --runs 1 --setup-only --no-readiness

kit/scripts/kit-metrics.py compare kit/bench/runs/A0 kit/bench/runs/A1
kit/scripts/kit-metrics.py blind kit/bench/runs/A0 kit/bench/runs/A1 --out /tmp/cham    # rồi chấm /tmp/cham/diem.csv
kit/scripts/kit-metrics.py unblind /tmp/cham/key.json /tmp/cham/diem.csv
```

Điều kiện: `aw` (biến `AW`, PATH, hoặc tự `go build` trong repo aw), Claude CLI đã đăng nhập (`AW_CLAUDE_EXECUTABLE` hoặc PATH), Git,
JDK 21 và Maven, Node và npm, `jq`, Python 3.

## Cách đọc số đo

- Mỗi lượt có `metrics.json`: theo node (kích hoạt, vòng sửa, qua ngay lần đầu, attempt hỏng theo mã lỗi, `needs_info`, token, chi phí),
  tổng, finding của reviewer theo mức, và số đo tài liệu (số AC trong spec, số AC có đường lỗi, số task).
- **Vòng sửa** là node kích hoạt lặp (`iteration > 0`); **qua ngay lần đầu** là tỉ lệ WorkItem mà gate qua ở lần kích hoạt đầu tiên.
- Mỗi lần đo chỉ 2 lượt, nên chỉ tin vào khác biệt lớn và nhất quán giữa các lượt. Giữ cố định: model (`sonnet`), phiên bản Claude CLI,
  commit `aw`, fixture. Đổi commit `aw` giữa hai lần đo thì chạy lại lần đo trước.
- Chi phí là số provider tự báo; nó cộng cả lượt chạy hỏng.
