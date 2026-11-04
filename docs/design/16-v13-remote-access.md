# V13 — Truy cập từ xa và màn duyệt cho điện thoại

> Trạng thái: **ĐANG SOẠN, chưa triển khai.** Nguồn: V12-32 (UX-40), chuyển sang V13 theo quyết định của product owner ngày
> 2026-11-04 ("task phức tạp, đụng ranh giới tin cậy của ADR-016"). Quyết định đã duyệt nằm ở ADR-064
> (`docs/architecture/02-architecture-decisions.md` mục 66). Làm tuần tự theo Task ID; mỗi task một commit.
>
> Exit: gate V13 ở cuối file.

> Phạm vi task mặc định: kế thừa mục 3 của `00-roadmap.md`. Như V11 và V12, **V13 được phép sửa core engine** (`internal/`,
> `cmd/`, `web/`). Mọi thay đổi hành vi công khai cần ADR (đã có ADR-064) và cập nhật `docs/operator/`. Tài liệu bằng tiếng Việt,
> giao diện bằng tiếng Anh. Fail-closed vẫn là mặc định.

## Bối cảnh

Giao diện của aw hiện chỉ mở được trên máy chạy aw: server nghe loopback, mọi mutation cần token phiên nhúng trong bootstrap HTML
(ADR-016), Host và Origin phải đúng địa chỉ loopback. Khi dùng aw thật cho `chochuyennha-platform`, product owner phải ngồi trước
máy để quyết định cổng duyệt, Retry hay Cancel, kể cả việc chỉ mất vài giây (UX-40).

## Quyết định đã duyệt (ADR-064)

1. Server vẫn chỉ nghe loopback. Truy cập từ xa qua tunnel có xác thực do người vận hành dựng; **khuyến nghị Tailscale** (mạng riêng,
   không mở hostname công khai) thay cho Cloudflare Tunnel công khai. aw không mở cổng ra ngoài.
2. Phiên từ xa: ghép cặp bằng mã một lần (QR) từ máy chạy aw, token gắn thiết bị, thu hồi được, hết hạn. Phiên local như cũ.
3. Phạm vi phiên từ xa: chỉ đọc, cộng các thao tác của "Needs you" (quyết định approval, Retry, Run Again), **duyệt kèm commit cục bộ**
   (không push; commit cục bộ hoàn tác được) và **Cancel Run**. Cấm push, merge, publish, xóa và đổi cấu hình.
4. Màn duyệt cho điện thoại: Needs you, chi tiết approval kèm diff (V12-17), nút quyết định.
5. Thông báo đẩy: một **webhook chung** (dùng được với ntfy, Telegram, Discord). Payload chỉ gồm loại sự kiện, tiêu đề task và link;
   không có code hay diff.

## Danh sách task

| Task | Tên | Nguồn | Ưu tiên | Tầng |
|---|---|---|---|---|
| V13-01 | Listener từ xa, ghép cặp thiết bị và token thiết bị | UX-40, ADR-064 mục 1–2 | Cao | engine, cmd, web |
| V13-02 | Giới hạn phạm vi của phiên từ xa | UX-40, ADR-064 mục 3 | Cao | engine |
| V13-03 | Màn duyệt cho điện thoại | UX-40, ADR-064 mục 4 | TB | web |
| V13-04 | Webhook thông báo | UX-28, UX-40, ADR-064 mục 5 | TB | engine, cmd |

V13-02 phụ thuộc V13-01. V13-03 cần V13-01 và V13-02. V13-04 độc lập với ba task kia; link trong thông báo chỉ mở được từ xa khi
đã có V13-01.

---

### V13-01 — Listener từ xa, ghép cặp thiết bị và token thiết bị

- **Mục tiêu:** một thiết bị (điện thoại) đã ghép cặp mở được giao diện qua tunnel; thiết bị chưa ghép cặp không đọc được gì.
- **Đề xuất kỹ thuật (chưa duyệt, kiểm lại khi làm):**
  - `aw serve --remote-port <n> --remote-origin https://<máy>.<tailnet>.ts.net`: một listener loopback **thứ hai**, chỉ dành cho
    tunnel (`tailscale serve` trỏ vào cổng này). Mọi request tới listener này là request từ xa, bất kể Host header, nên tunnel không
    thể làm request từ xa trông như request local. Listener local giữ nguyên ADR-016.
  - Host phải là host của `--remote-origin` (hoặc `127.0.0.1:<remote-port>` nếu tunnel viết lại Host); Origin phải đúng
    `--remote-origin` khi có. Không phát header CORS nào.
  - Ghép cặp: trên máy chạy aw (trang Settings), "Pair a device" tạo mã một lần ngẫu nhiên, hết hạn sau 10 phút, hiện dưới dạng QR
    của `<remote-origin>/ui/pair#<mã>`. Thiết bị gửi mã kèm tên thiết bị tới route ghép cặp của listener từ xa (route duy nhất
    không cần token) và nhận token thiết bị. Mã dùng một lần; vài lần sai liên tiếp thì hủy mọi mã đang chờ.
  - Token thiết bị: 32 byte ngẫu nhiên, DB chỉ giữ SHA-256 (bảng `remote_devices`: id, tên, hash, tạo lúc, hết hạn, lần cuối dùng,
    thu hồi lúc). Hết hạn mặc định 30 ngày. Giao cho trình duyệt bằng cookie `HttpOnly`, `SameSite=Strict`, `Secure` khi origin là
    https. Mutation từ xa còn phải mang header `X-Aw-Session-Token` bằng token CSRF của bootstrap từ xa (bootstrap chỉ phục vụ khi
    cookie hợp lệ, và không bao giờ chứa token phiên local).
  - Danh sách thiết bị và thu hồi trên trang Settings (local), CLI `aw remote-access pair|devices|revoke`. Thu hồi có hiệu lực ngay.
  - Actor của phiên từ xa ghi rõ thiết bị (ví dụ `operator@device:<tên>`), để nhật ký quyết định phân biệt được.
- **Test:** request không cookie, cookie sai, hết hạn, đã thu hồi đều bị từ chối; mã ghép cặp hết hạn hoặc dùng lại bị từ chối;
  bootstrap từ xa không chứa token local; Host/Origin lạ bị từ chối.

### V13-02 — Giới hạn phạm vi của phiên từ xa

- **Mục tiêu:** từ xa chỉ làm được những gì ADR-064 mục 3 cho phép; mọi thứ khác bị từ chối ở server, không chỉ bị ẩn trên giao diện.
- **Đề xuất kỹ thuật:** listener từ xa chỉ đăng ký một tập route đóng: mọi route `GET` (trừ quản lý thiết bị), cộng các mutation
  `resolveApproval` (kể cả `commit`, chỉ commit cục bộ), `retryBlockedActivation`, `retryWorkItem` (Run Again), `cancelRun`. Route
  không có trong tập trả 404 như route không tồn tại. Danh sách là một hằng có test riêng so với route inventory: thêm route mới không
  tự mở ra từ xa.
- **Cấm (test âm cho từng loại):** `pushWorkspaceBranch`, `createWorkspacePullRequest`, `mergeWorkspacePullRequest`, publish định
  nghĩa, mọi route xóa, đổi cấu hình (settings, secret, hồ sơ Run app, ghép cặp và thu hồi thiết bị).
- **Câu hỏi cho product owner khi làm:** có cho trả lời message (`appendMessage`, phản hồi kèm `revise`) và gửi tín hiệu WAIT từ xa
  không; ADR-064 không liệt kê hai thao tác này.
- **Test:** security matrix chạy lại trên listener từ xa; mỗi route bị cấm trả 404; route được phép vẫn qua đủ kiểm tra cross-project.

### V13-03 — Màn duyệt cho điện thoại

- **Mục tiêu:** trên màn hình hẹp, người vận hành thấy Needs you, mở một approval, đọc diff (trình xem của V12-17) hoặc checklist QA
  (V12-31), và quyết định (kèm commit cục bộ nếu node bật release), Retry, Run Again hay Cancel Run.
- **Đề xuất:** khi bootstrap báo phiên từ xa, giao diện chỉ hiện các màn này; nút cho thao tác bị cấm không xuất hiện.
- **Test:** vitest cho chế độ từ xa; Playwright ở viewport điện thoại cho đường Needs you → approval → quyết định.

### V13-04 — Webhook thông báo

- **Mục tiêu:** khi có mục mới trong Needs you (approval chờ, run hỏng, blocker), aw gửi một webhook; người vận hành nối nó vào
  ntfy, Telegram hay Discord.
- **Đề xuất:** `aw serve --notify-webhook <url> [--notify-format json|text|discord]`. Payload chỉ gồm loại sự kiện, tiêu đề task và
  link (dựng từ `--remote-origin`, không có thì link local); không có code, diff, tên file hay secret. Mỗi mục gửi một lần (ghi lại mục
  đã gửi), thử lại có giới hạn khi webhook lỗi, không chặn việc khác. URL webhook có thể chứa token của dịch vụ nhận: không ghi vào log.
- **Test:** server webhook giả nhận đúng payload, không gửi lặp, payload không chứa nội dung code.

---

## Gate V13

- `go test ./...`, test của `web/`, Playwright xanh trên CI.
- Security matrix cho listener từ xa xanh: chưa ghép cặp không đọc được gì, route bị cấm trả 404.
- Product owner ghép cặp một điện thoại thật qua Tailscale, quyết định một cổng duyệt thật của chochuyennha, nhận thông báo, rồi thu
  hồi thiết bị và thấy thiết bị không vào được nữa.
