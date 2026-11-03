# V12 — Run graph: sơ đồ workflow có lịch sử theo node

> Trạng thái: **ĐÃ CHỐT THIẾT KẾ, chưa triển khai** (product owner chốt 7 quyết định ngày 2026-11-03, xem mục "Quyết định đã chốt").
> Làm tuần tự theo Task ID; mỗi task một commit, merge vào `master` khi product owner duyệt.
>
> Exit: gate V12 ở cuối file.

> Phạm vi task mặc định: kế thừa mục 3 của `00-roadmap.md`. V12 sửa `web/` và thêm trường đọc vào API/projection
> (`internal/`, `cmd/`, OpenAPI). Không đổi engine chạy workflow, không đổi định nghĩa workflow.

## Bối cảnh

Nguồn: dùng aw thật để phát triển dự án `chochuyennha-platform` (2026-11-01 … 2026-11-03), ghi ở mục UX-27 của danh sách bất cập
người vận hành giữ ngoài repo. Tab **Graph & Timeline** (`web/src/screens/TaskDetail.tsx`, `GraphTimelineTab`, `computeLayout`)
không trả lời được ba câu người vận hành cần: run đang ở bước nào, vì sao hỏng, giờ làm gì.

Bằng chứng (run `71307cba…` của task P2-02, workflow `wf-task-delivery-plus`, 18 node, 32 cạnh):

- `computeLayout` xếp hạng theo đường dài nhất và đặt mọi node cùng hạng trên một hàng: SVG rộng 2120px, cao 270px, phải cuộn
  ngang; cạnh vẽ thẳng nên cắt ngang node; nhãn outcome đè nhau; END nằm giữa hàng.
- Mỗi node chỉ hiện lần chạy mới nhất. Vòng `gate1 → size → simplify → gate1` chạy 3 lần trông như 1 lần.
- Cột Timeline là 35 dòng `NODE_RUN` / `EXECUTION_ATTEMPT` mã thô, không có giờ, thời lượng, chi phí hay kết quả bằng lời.
- Banner chỉ ghi `RUN_FAILED … (RUN_FAILED)`. Nguyên nhân thật (gate1 lần 4 không khởi chạy được, không có evidence) phải đọc
  bằng CLI mới biết.

## Quyết định đã chốt (product owner, 2026-11-03)

1. **Giữ sơ đồ workflow làm giao diện chính.** Bấm một node thì hiện lịch sử thao tác và kết quả của node đó. Giao diện tiếng Anh.
2. **Bộ quy tắc hình dạng** ở mục "Quy tắc hiển thị" (gate là hình thoi).
3. **Mặc định chỉ hiện đường chính**; node thoát (ví dụ `needs-info`, `reject`) ẩn, nút **All paths** hiện đầy đủ.
4. **Bỏ hẳn cột Timeline** cũ.
5. **Panel lịch sử đặt bên phải** sơ đồ.
6. **Task có nhiều run: gộp lịch sử** các run vào cùng sơ đồ.
7. **Huy hiệu số lần chạy đếm theo số lần chạy thật** (mọi activation, kể cả lần bị chặn rồi retry), không đếm theo vòng.

## Quy tắc hiển thị

### Hình dạng (suy ra từ dữ liệu sẵn có, không thêm trường vào định nghĩa workflow)

| Node | Hình |
|---|---|
| `AGENT` | Chữ nhật bo góc |
| `COMMAND` có từ 2 outcome trở lên (ví dụ `gate1`, `size`, `hygiene`, `quality`, `check-plan`, `expect-fail`) | Hình thoi (gate) |
| `APPROVAL` | Hình thoi viền kép (cổng người) |
| `COMMAND` chỉ có 1 outcome (ví dụ `reject`) | Chữ nhật góc vuông (hành động) |
| `START`, `END` | Viên thuốc |
| `FORK`, `JOIN` | Thanh ngang mảnh (chưa workflow mẫu nào dùng; vẫn phải vẽ được) |

### Bố cục

- **Đường chính** xếp dọc ở giữa: chuỗi node đi từ `START` tới `END` theo outcome không phải thoát, dài nhất. Cạnh trên đường
  chính vẽ thẳng, nhãn outcome bên phải.
- **Cạnh quay lại** (tới node có hạng nhỏ hơn) đi theo làn riêng bên trái, mỗi cạnh một làn, vào node đích ở các độ cao khác
  nhau để không cắt nhau. Cạnh quay lại ngắn (ví dụ `check-plan → plan`, `expect-fail → repro`) và cạnh tắt (ví dụ
  `frame → build` qua outcome `fast`) đi bên phải.
- **Node phụ của một vòng** (ví dụ `debug`, `simplify`) đặt cạnh đường chính, ngang hàng với node mà vòng quay về.
- **Node thoát**: node chỉ tới được qua outcome thoát (`needs_info`, `escalated`, `rejected`, `abandon`) và dẫn về `END`. Ở
  chế độ mặc định chúng ẩn; node có cạnh tới node thoát mang huy hiệu `+n` (n = số cạnh bị ẩn). Chế độ **All paths** vẽ node
  thoát ở cột phải, cạnh nét đứt, các cạnh vào `reject` gom chung một làn.
- **Fallback**: workflow không có đường chính rõ (ví dụ đồ thị do người dùng tự viết, rất lớn) thì dùng bố cục xếp hạng hiện
  tại; không bao giờ ném lỗi khi vẽ.

### Trạng thái

- Màu node theo trạng thái lần chạy cuối (thành công, thất bại, đang chạy, đang chờ, chưa chạy tới = viền nét đứt), dòng phụ
  trong node là outcome của lần chạy cuối.
- Huy hiệu `×n` khi n > 1: n = số activation của node, gộp mọi run (quyết định 6 và 7).
- Cạnh đã đi qua tô màu, nhãn kèm `×n` = số lần đi qua (từ `takenEdges`), gộp mọi run.
- Banner trên sơ đồ: trạng thái run mới nhất, lý do dừng bằng lời (task V12-03), tổng thời gian và chi phí.

### Panel lịch sử (bên phải)

Bấm một node (chuột hoặc bàn phím) mở panel gồm:

- Tên node, loại, mô tả ngắn, giới hạn vòng (`cyclePolicy.maxIterations`) nếu có.
- **Outcomes**: mỗi outcome dẫn tới node nào, đã đi bao nhiêu lần.
- **History**: mỗi activation một dòng, gộp mọi run, mới nhất ở cuối: số run, số lần chạy, giờ, thời lượng, chi phí, outcome,
  tóm tắt kết quả một dòng (V12-03). Mở một dòng ra: link Agent output / Files changed (AGENT), Command log (COMMAND),
  quyết định và diff đã duyệt (APPROVAL), Evidence; dùng lại nội dung của `NodeDetailPane` hiện có.
- **Hành động** chỉ hiện khi server trả trong `validActions` (ví dụ Retry cho node bị chặn, các outcome của cổng duyệt đang chờ).
  Không suy hành động từ chữ trạng thái.

## Dữ liệu: đã có và còn thiếu

| Cần | Hiện trạng (`web/src/api/rundetail.ts`) |
|---|---|
| Node, cạnh, outcome, `cyclePolicy` | Có: `GraphNodeView`, `GraphEdgeView` |
| Số activation mỗi node, trạng thái, outcome | Có: `NodeActivationView` (`iteration`, `state`, `selectedOutcome`) |
| Số lần đi mỗi cạnh | Có: `TakenEdgeView` |
| Giờ, thời lượng, chi phí, mã lỗi từng attempt | Có: `TimelineEntryView` (`startedAt`, `finishedAt`, `usage`, `failureCode`, `terminationReason`) |
| Evidence, sự kiện attempt, lời agent khi hỏng | Có: `getNodeRunDetail`, `listAttemptEvents` (V11-11) |
| Danh sách run của một WorkItem để gộp lịch sử | Cần xác minh đầu V12-04: API nào trả mọi run của WorkItem kèm `workflowVersionId` |
| **Tóm tắt kết quả một dòng mỗi activation** | **Thiếu** (V12-03) |
| **Lý do dừng run có kiểu, viết bằng lời** | **Thiếu** (V12-03) |

## Danh sách task

| Task | Nội dung | Phạm vi | Phụ thuộc |
|---|---|---|---|
| V12-01 | Hàm bố cục thuần `layoutRunGraph` + phân loại hình dạng | `web/` | không |
| V12-02 | Vẽ sơ đồ mới: hình dạng, trạng thái, huy hiệu, cạnh, Main/All paths, bàn phím | `web/` | V12-01 |
| V12-03 | API: tóm tắt kết quả mỗi activation và lý do dừng run | `internal/`, `cmd/`, OpenAPI, ADR | không |
| V12-04 | Panel lịch sử bên phải, gộp mọi run của WorkItem | `web/` (+ API nếu khảo sát thấy thiếu) | V12-02, V12-03 |
| V12-05 | Banner, bỏ cột Timeline và `computeLayout` cũ, Playwright trên stack thật | `web/`, `e2e` | V12-04 |

Mặc định làm tuần tự theo Task ID.

## V12-01 — Hàm bố cục và phân loại hình dạng

- **Mục tiêu:** một hàm thuần, không phụ thuộc React, nhận `nodes` + `possibleEdges` và trả vị trí node, đường đi của từng cạnh
  (danh sách điểm), loại cạnh (đường chính, quay lại, tắt, thoát), hình dạng node, tập node thoát.
- **Thực hiện:**
  1. Phát hiện cạnh quay lại bằng DFS từ `START` theo thứ tự outcome khai báo; cạnh `COMPLETION_REWORK` luôn là cạnh quay lại.
  2. Xác định node thoát theo quy tắc ở mục "Bố cục" (danh sách outcome thoát là hằng số có tài liệu, không suy từ tên node).
  3. Đường chính = đường dài nhất từ `START` tới `END` trên đồ thị không có cạnh quay lại, bỏ node thoát.
  4. Gán làn cho cạnh quay lại sao cho đoạn ngang của làn ngoài không cắt đoạn dọc của làn trong (làn ngoài vào node đích cao hơn).
  5. FORK/JOIN: các nhánh song song đặt cạnh nhau giữa FORK và JOIN.
  6. Fallback về bố cục xếp hạng khi không tìm được đường chính.
- **Test (unit, Vitest):** fixture là JSON thật của `kit/workflows/` (`wf-task-delivery-plus`, `wf-task-delivery`, `wf-bugfix`,
  `wf-feature-definition`, `wf-retro`) và `wf-docs-change` của chochuyennha (chép vào fixture), cộng một workflow có FORK/JOIN.
  Mỗi fixture kiểm: mọi node có vị trí, không hai node chồng nhau, mọi cạnh có đường đi, đúng tập node thoát, đúng hình dạng theo
  bảng, làn không cắt nhau theo quy tắc ở bước 4.

## V12-02 — Vẽ sơ đồ mới

- **Mục tiêu:** thay phần canvas của `GraphTimelineTab` bằng sơ đồ theo mục "Quy tắc hiển thị", dùng kết quả của V12-01.
- **Thực hiện:**
  1. Vẽ SVG theo hình dạng; huy hiệu `×n` theo số activation; huy hiệu `+n` cho cạnh ẩn; nút Main path / All paths
     (lưu lựa chọn theo người xem bằng `localStorage`, bọc try/catch).
  2. **Truy cập bằng bàn phím:** SVG đổi từ `role="img"` sang `role="group"`; mỗi node là phần tử có `role="button"`,
     `tabindex="0"`, `aria-label` gồm tên, loại, trạng thái, số lần chạy; Enter/Space mở panel. Giữ chế độ **Accessible List**
     hiện có (hợp đồng accessibility của V7) cho tới khi V12-05 chứng minh sơ đồ mới đạt cùng mức.
  3. Theo dõi run đang chạy như hiện nay (`refetchInterval` 4 giây khi run còn sống).
- **Test:** component test với dữ liệu của 4 run thật đã ghi lại (P2-02 hỏng giữa vòng, P1-01 nhiều vòng có node bị chặn rồi
  retry, P2-01 hai run, P0-03 docs-only) dưới dạng fixture JSON của API.

## V12-03 — API: tóm tắt kết quả và lý do dừng

- **Mục tiêu:** người vận hành đọc được kết quả từng lần chạy và lý do run dừng mà không cần CLI.
- **Khảo sát đầu task:** nội dung thật của evidence `AGENT_OUTPUT` và `COMMAND_EXECUTION`; các mã `failureCode` /
  `terminationReason` / blocker đang có; trường hợp attempt `EXECUTION_FAILED` mà không có evidence (P2-02, gate1 lần 4).
- **Thực hiện (đề xuất, chốt sau khảo sát):**
  1. Thêm `resultSummary` (chuỗi ngắn, đã che bí mật) vào mỗi activation trong projection đọc của run: AGENT lấy từ báo cáo cuối
     có cấu trúc của agent; COMMAND lấy dòng tóm tắt do lệnh in ra theo quy ước (ví dụ dòng `diff-size: large diff: …`) hoặc dòng
     cuối không rỗng; APPROVAL lấy quyết định, người duyệt và ghi chú.
  2. Thêm `stopReason` có kiểu vào chẩn đoán run: `code` (ví dụ `COMMAND_NOT_STARTED`, `SCOPE_VIOLATION`,
     `PROVIDER_QUOTA_EXCEEDED`, `CYCLE_ESCALATED`, `ADAPTER_BUILD_DRIFT`, `APPROVAL_REJECTED`), `nodeKey`, `nodeRunId`,
     `message` bằng tiếng Anh.
  3. Cập nhật OpenAPI, sinh lại client `web/src/api/generated`; ADR mới vì đổi bề mặt API công khai; cập nhật `docs/operator/`.
- **Test:** Go test cho từng mã `stopReason`, gồm ca P2-02 (command không khởi chạy được) tái hiện bằng agent/command giả lập.

## V12-04 — Panel lịch sử bên phải

- **Mục tiêu:** panel ở mục "Panel lịch sử", thay vị trí của cột Timeline.
- **Khảo sát đầu task:** cách lấy mọi run của WorkItem; khi các run dùng workflow version khác nhau (ví dụ sau Redo with Latest
  Version), sơ đồ vẽ theo version của run mới nhất, lịch sử của node không còn trong version đó vẫn hiện trong panel với nhãn
  version cũ.
- **Thực hiện:** gộp activation của mọi run theo `nodeKey`; dòng lịch sử ghi số run; dùng lại nội dung `NodeDetailPane` khi mở
  rộng dòng; nút hành động lấy từ `validActions`.
- **Test:** component test với fixture P2-01 (lịch sử run 1 hỏng ở `build` và run 2 thành công cùng hiện ở node `build`).

## V12-05 — Banner, dọn mã cũ, Playwright

- **Thực hiện:** banner dùng `stopReason`; bỏ cột Timeline, `computeLayout` và mã chỉ phục vụ chúng; cập nhật test cũ của tab.
- **Test:** Playwright trên stack thật (`aw serve` + worker với agent giả lập) cho các hành trình: run có vòng lặp, run hỏng
  giữa chừng, task hai run, node bị chặn rồi retry, cổng duyệt đang chờ; kiểm bàn phím mở được panel của mọi node. Đọc phần
  bài học Playwright của V7-17C trước khi viết.

## Ngoài phạm vi V12

- Cổng người sau vòng `simplify` và ngưỡng `diff-size` theo component (UX-26): thay đổi workflow mẫu và cấu hình Component,
  làm ở version khác.
- Hộp "Việc cần bạn", thông báo, trình xem diff có bình luận, hàng đợi task (UX-28 … UX-31).

## Gate V12

- `go test ./...`, test của `web/` và Playwright xanh trên CI.
- Unit test của V12-01 xanh với mọi workflow trong `kit/workflows/` và fixture FORK/JOIN.
- Product owner mở 4 run thật của chochuyennha (P2-02, P1-01, P2-01, P0-03) trên giao diện mới và xác nhận: thấy đúng vòng lặp
  và số lần chạy, đọc được lý do dừng của P2-02, mở được lịch sử mọi node bằng chuột và bàn phím.
