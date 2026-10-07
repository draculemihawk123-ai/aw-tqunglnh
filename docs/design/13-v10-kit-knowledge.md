# V10 — Tri thức cho kit: chắt lọc từ ClaudeKit theo từng node

> Trạng thái: **THIẾT KẾ — chờ product owner duyệt, chưa triển khai.** Product owner đã xác nhận kế hoạch ngày
> 2026-10-07: 19 task dưới đây, đo hiệu quả 2 lượt mỗi lần đo, tri thức chắt lọc viết bằng tiếng Việt. File này chỉ chia
> task; chưa task nào được thực hiện. Khi triển khai: mỗi task một commit, merge vào `master` khi product owner duyệt.
>
> Entry: `master` tại `ef59084` (kho `kit/` và hai hướng dẫn đã merge).
>
> Exit: gate V10 ở cuối file; không có thay đổi nào trong core engine.

> Phạm vi task mặc định: kế thừa mục 3 của `00-roadmap.md`, cộng ràng buộc của V10: **chỉ sửa `kit/` và `docs/`**.

## Bối cảnh

Kho [`kit/`](../../kit/README.md) hiện có ít tri thức. `skill-feature-flow` có mỗi node một resource ngắn; ngoài ra
có `skill-maker`, `skill-review`, `skill-code-review` và hai Layer stack. Engine đã cưỡng chế đúng quy trình, nhưng
phần hướng dẫn agent **nghĩ thế nào** ở từng node còn mỏng. Lần chạy thật của
[hướng dẫn issue tracker](../guides/issue-tracker/README.md) cho thấy vòng sửa tốn nhiều nhất ở ba chỗ: hợp đồng sai
lint hai lần (C-01), lời gọi API lệch hợp đồng (W-01), test đỏ (A-01, T-02).

ClaudeKit Engineer (bản thương mại, product owner đã mua giấy phép) mạnh đúng ở phần này: checklist hành vi cho từng
vai trò (planner, debugger, tester, reviewer…), luật phát triển, và skill theo stack. Ngược lại, ClaudeKit để LLM tự
điều phối và phần lớn hook của nó cho qua khi lỗi (fail-open), còn aw cưỡng chế bằng engine và đo được kết quả. V10
đưa phần mạnh của ClaudeKit vào kit, rồi đo xem nó có giúp thật không.

Đầu vào: repository `claudekit/claudekit-engineer` phiên bản 2.20.0, commit `ed8a1fa`, gọi tắt là **CK**. Trường
**Đầu vào CK** của mỗi task ghi đường dẫn tính từ thư mục `claude/` của repository đó. Trường này không phải
`SourceRef`; trường `Nguồn` vẫn theo grammar ở mục 3 của `00-roadmap.md`.

## Nguyên tắc của V10

- **Không đụng core.** Không sửa `internal/`, `cmd/`, `web/`, `go.mod`, `go.sum`. Việc cần core thì ghi thành đề
  xuất cho version sau.
- **Chắt lọc, không chép nguyên.** Viết lại cho ngữ cảnh aw theo quy ước của V10-00: agent MAKER không chạy được
  lệnh; không có subagent; agent không hỏi người trực tiếp mà chọn outcome `needs_info`; outcome do engine kiểm.
- **Tri thức gắn theo node.** Đường chính là CONTEXT policy của agent ở node đó. Selector của aw chỉ lọc thêm theo
  component/path, MAKER/CHECKER và mức rủi ro, và các chiều nối với nhau bằng OR, nên không dùng selector để diễn đạt
  "chỉ ở vòng sửa".
- **Xuất xứ và giấy phép theo từng mục.** Nhiều skill của CK tự khai giấy phép trong `SKILL.md` (MIT, Apache-2.0); skill
  không khai thì theo giấy phép độc quyền của CK.
- **Đo trước, giữ sau.** Có baseline trước khi thêm tri thức; phần nào giữ lại phải có số đo theo node hoặc lý do ghi
  rõ.
- **Tiếng Việt**, giữ thuật ngữ kỹ thuật tiếng Anh, như kit hiện có.

## Không làm

- Sửa core engine; các nhu cầu doanh nghiệp (nhiều người dùng, SSO, sandbox, tích hợp SCM, báo cáo kiểm toán).
- Cài hook của CK vào aw hay vào repository của người dùng. V10-17 chỉ thử nghiệm và ghi verdict.
- Tính năng tương tác của CK: `AskUserQuestion`, `/ck:preview`, statusline, notification, team mode, memory của agent.
- Skill ngoài phát triển phần mềm: media, marketing, thiết kế đồ họa và 3D (ví dụ `ai-artist`, `remotion`, `threejs`,
  `copywriting`).

## Bản đồ: node của aw và tri thức lấy từ CK

| Node | Resource hiện tại | Đầu vào CK chính | Task |
|---|---|---|---|
| Mọi agent | `flow.working-rules`, `dev.definition-of-done` | `rules/CLAUDE.md`, `rules/development-rules.md`, `rules/review-audit-self-decision.md`, `rules/orchestration-protocol.md` | V10-03 |
| Hỏi người | `flow.needs-info` | `rules/CLAUDE.md`, `skills/ck-plan/references/validate-question-framework.md` | V10-03 |
| BRAINSTORM | `flow.brainstorm` | `skills/brainstorm`, `agents/brainstormer.md`, `skills/problem-solving` | V10-04 |
| SPEC | `flow.spec` | `skills/ck-plan/references/scope-challenge.md`, `skills/ck-scenario` | V10-05 |
| DESIGN | `flow.design` | `agents/planner.md`, `skills/ck-plan/references/` (`solution-design`, `codebase-understanding`, `red-team-personas`), `skills/ck-predict` | V10-06 |
| FRAME, PLAN | `flow.frame`, `flow.plan` | `skills/fix/references/` (`complexity-assessment`, `mode-selection`), `skills/cook/references/workflow-routing.md`, `agents/planner.md`, `skills/ck-plan/references/` (`plan-organization`, `task-management`) | V10-07 |
| BUILD, gồm vòng sửa | `flow.build`, `dev.implement-feature` | `skills/cook/references/workflow-steps.md`, `rules/primary-workflow.md`, `agents/fullstack-developer.md`, `skills/ck-debug`, `skills/fix/references/diagnosis-protocol.md`, `agents/debugger.md`, `agents/tester.md`, `skills/test` | V10-08 |
| REVIEW (CHECKER) | `review.*`, `codereview.*` | `skills/ck-code-review`, `agents/code-reviewer.md`, `skills/ck-security` | V10-09 |
| SYNC | `flow.sync` | `agents/docs-manager.md`, `skills/docs/references/update-workflow.md`, `rules/documentation-management.md` | V10-10 |
| Layer stack (BUILD, DESIGN, REVIEW theo component) | `spring.*`, `sqlite.*`, `react.*` | `skills/backend-development`, `skills/databases`, `skills/react-best-practices`, `skills/web-testing` | V10-11 |
| Node riêng của project (`implement`, `contract`, `ai-review`…) | — | Dùng lại tri thức của BUILD và REVIEW | V10-08, V10-09 |

## Thứ tự và ưu tiên

| Nhóm | Task | Kết quả |
|---|---|---|
| Nền | V10-00, V10-01, V10-02 | Quy ước chắt lọc và lint; công cụ nhập skill và kiểm wiring; bộ đo, fixture và baseline A0 |
| Tri thức theo node | V10-03 … V10-11 | Mỗi node có tri thức chắt lọc, gắn đúng agent |
| Đo lần 1 | V10-12 | A1 so với A0; giữ, sửa hay bỏ từng nhóm tri thức |
| Vai trò, workflow | V10-13, V10-14 | Agent mẫu theo vai trò trong kit; workflow mẫu có node debug, review, `wf-bugfix` |
| Hook → gate | V10-15 | Hook của CK thành Command/Gate fail-closed |
| Học dần | V10-16 | Bài học thành resource có version |
| Thử nghiệm | V10-17 | Verdict cho việc để nguyên skill CK trong `.claude/skills/` của repository đích |
| Kết | V10-18 | Đo lần 2 (A2), cập nhật tài liệu, verdict V10 |

Mặc định làm tuần tự theo Task ID, vì V10-03…V10-11 cùng sửa `kit/kit.json` và agent trong hai `aw-project.json` của
hướng dẫn.

> **Có thể song song:** V10-01 với V10-02 (hai công cụ riêng, chỉ cùng thêm test vào `kit/tests/check-kit.sh`).
> V10-17 với mọi task sau V10-02, vì spike chạy trên bản cài nháp và không sửa `kit/`.

**Chi phí agent thật (ước tính):** mỗi lượt golden workload khoảng 3 USD. Ba lần đo (A0, A1, A2) × 2 lượt ≈ 18 USD; cộng
một lần chạy thật workflow mới và spike ≈ 2–3 USD. Tổng khoảng **20 USD**. Nếu commit aw đổi giữa các lần đo thì phải
chạy lại A0 (thêm khoảng 6 USD). Kiểm wiring tri thức dùng agent giả lập, không tốn tiền.

## V10-00 — Quy ước chắt lọc, xuất xứ, giấy phép và lint

- **Mục tiêu:** biến "chắt lọc" thành quy tắc kiểm được bằng máy, trước khi nhập bất kỳ nội dung nào.
- **Phụ thuộc:** product owner duyệt file này.
- **Thực hiện:**
  - Thêm mục "Chắt lọc từ nguồn bên ngoài" vào `kit/README.md`:
    - đơn vị là một resource, một ý, tối đa khoảng 2 KB;
    - `HARD_CONSTRAINT` chỉ dành cho luật kiểm được mà vi phạm là sai, tối đa 15 mỗi agent; còn lại dùng
      `REQUIRED_PROCEDURE`, `GUIDANCE`, `REFERENCE`;
    - viết lại cho ngữ cảnh aw: không chạy lệnh, không subagent, không hỏi người trực tiếp, outcome do engine kiểm;
    - mỗi luật chỉ nằm ở một chỗ, nơi khác tham chiếu tới;
    - `provenance.source` có dạng `claudekit-engineer@ed8a1fa:<đường dẫn>`;
    - viết tiếng Việt, giữ thuật ngữ kỹ thuật tiếng Anh.
  - `kit.json`: mỗi entry lấy từ CK khai `origin`, `license` theo từng skill (đọc trường `license:` trong `SKILL.md`;
    không có thì ghi `ClaudeKit-Proprietary (licensed)`) và trường mới `redistributable` (false cho phần độc quyền).
    Cập nhật `kit/schema/aw-project.schema.json`.
  - Lint trong `aw-publish.py --check` và `check-kit.sh`:
    - cấu trúc chỉ có ở CK: `/ck:`, `AskUserQuestion`, `TaskCreate`, `TaskUpdate`, `SendMessage`, `Task(`, `repomix`,
      `.claude/`, `plans/reports`, marker `@@PRIVACY`;
    - resource vượt kích thước;
    - agent có quá 15 `HARD_CONSTRAINT`;
    - cùng một câu luật xuất hiện ở hai resource (cảnh báo).
  - Thêm chế độ `--share`: liệt kê và trả mã lỗi khi kit có entry `redistributable: false`, dùng trước khi chia sẻ kit ra
    ngoài.
- **Verify:** `kit/tests/check-kit.sh` có test cho từng luật lint (một mẫu vi phạm, một mẫu đạt); `aw-publish.py --check`
  vẫn `OK` cho hai hướng dẫn; `--share` trả lỗi trên mẫu có entry độc quyền.
- **Hoàn thành khi:** mọi luật lint có test; quy ước nằm trong `kit/README.md`; kit hiện có không vi phạm luật nào
  (riêng `skill-code-review` được ghim lại xuất xứ ở V10-09).
- **Nguồn:** HE-03-M06, HE-04-M04, HE-04-M07, HE-04-S01, HE-04-S04, HE-02-M07, ROADMAP-§3.

## V10-01 — Công cụ nhập skill và kiểm wiring tri thức

- **Mục tiêu:** chuyển một skill dạng `SKILL.md` thành bản nháp JSON của aw, và kiểm được tri thức đến đúng agent mà
  không tốn tiền.
- **Phụ thuộc:** V10-00.
- **Thực hiện:**
  - `kit/scripts/import-skill.py <thư mục skill | file agent .md | file rule .md>`: đọc frontmatter (`name`,
    `description`, `license`) cùng các mục và `references/*.md`. Sinh bản nháp vào `kit/drafts/` (không nằm trong
    `kit.json`, không publish), mỗi mục một resource, có `provenance`, `license`, báo cáo kích thước và các vi phạm lint
    của V10-00. Người chắt lọc sửa bản nháp rồi mới chuyển vào `kit/skills/` hoặc `kit/layers/`.
  - Công cụ dùng được cho mọi nguồn theo chuẩn `SKILL.md`, không riêng CK.
  - `kit/scripts/context-check.sh <aw-project.json> <node>`: publish lên một bản cài nháp, chạy node bằng agent giả lập
    và in danh sách resource của attempt lấy từ `aw context-snapshot show` (key, version, kích thước, resource bị bỏ vì
    ngân sách nếu có).
- **Verify:** test trong `check-kit.sh` với một `SKILL.md` mẫu tự viết (không dùng nội dung CK): sinh đúng số resource,
  đúng `license`, phát hiện cấu trúc cấm. `context-check.sh` chạy được trên hướng dẫn issue tracker cho node `build` và
  in ra các key `flow.*` hiện có.
- **Hoàn thành khi:** hai công cụ có test và được mô tả trong `kit/README.md`.
- **Nguồn:** HE-04-M02, HE-04-M03, HE-04-M06, HE-03-M06.

## V10-02 — Bộ đo, fixture và baseline A0

- **Mục tiêu:** đo được hiệu quả của tri thức theo từng node, với cùng model, cùng bộ việc và cùng môi trường; có
  baseline trước khi thêm tri thức.
- **Phụ thuộc:** V10-00.
- **Thực hiện:**
  - Fixture `kit/bench/issue-tracker-mvp/`: ba repository `contracts`, `api`, `web` ở trạng thái MVP (sau C-01, A-01,
    W-01 của hướng dẫn issue tracker), lưu dạng git bundle. Lấy từ lần chạy thật (contracts `f598e9b`, api `8971f33`, web
    `64df325`) nếu còn; nếu không, chạy lại C-01, A-01, W-01 một lần (khoảng 2 USD).
  - Golden workload: tính năng "bình luận", gồm F-00 (`wf-feature-definition`) → `split-tasks.sh` → T-01…T-03
    (`wf-task-delivery-*`) → review độc lập (`wf-code-review`). Workload đi qua BRAINSTORM, SPEC, DESIGN, FRAME, PLAN,
    BUILD, SYNC và REVIEW.
  - `kit/scripts/kit-bench.sh --kit <thư mục kit> --runs 2 --out <thư mục>`: mỗi lượt dựng một bản cài mới (`AW_DB` và
    worker riêng), dựng repository từ fixture, publish với bản kit được chỉ định (thêm tùy chọn `--kit` cho
    `aw-publish.py`) rồi chạy workload. Cổng người duyệt do script duyệt `approved`, chỉ trong bench; `needs_info` được
    ghi nhận rồi dừng lượt đó.
  - `kit/scripts/kit-metrics.py`: chỉ số theo node gồm số vòng sửa, tỉ lệ qua gate ngay lần đầu, attempt hỏng theo mã lỗi,
    số lần `needs_info`, token và chi phí, finding của reviewer theo mức. Với node viết tài liệu, thêm: `check-docs` qua
    ngay hay không, số AC có đường lỗi. Script so sánh được hai lần đo.
  - Rubric chấm tài liệu `kit/bench/rubric.md` cho BRAINSTORM, SPEC, DESIGN, PLAN, SYNC (thang 1–5, có mốc cho từng
    điểm). Script xuất tài liệu của các lượt với nhãn ngẫu nhiên để chấm mà không biết bản nào thuộc lần đo nào. Người
    chấm; hoặc một agent CHECKER chấm theo rubric rồi người xác nhận vài mẫu.
  - Chạy baseline A0 với kit hiện tại, 2 lượt. Báo cáo ghi commit aw, commit kit, phiên bản Claude CLI, model và thời
    gian chạy.
- **Verify:** `kit-metrics.py` có test trên dữ liệu mẫu; báo cáo A0 có đủ chỉ số theo node cho cả hai lượt và có run id
  để truy lại.
- **Hoàn thành khi:** `kit/bench/reports/A0.md` được commit; quy trình đo được mô tả đủ để người khác lặp lại.
- **Nguồn:** HE-01-M06, HE-01-S03, HE-02-S02, HE-11-S03, HE-10-S03, HE-12-S04.

## V10-03 — Luật chung cho mọi agent và cách hỏi người

- **Mục tiêu:** mọi agent theo cùng một bộ luật phát triển; khi phải hỏi người thì câu hỏi tự đứng được.
- **Phụ thuộc:** V10-01; V10-02 (A0 đã ghi).
- **Đầu vào CK:** `rules/CLAUDE.md`, `rules/development-rules.md`, `rules/review-audit-self-decision.md`,
  `rules/orchestration-protocol.md`, `skills/ck-plan/references/validate-question-framework.md`.
- **Thực hiện:**
  - Bổ sung `skill-maker` bằng luật phát triển:
    - YAGNI → KISS → DRY;
    - hành vi thật, không mock hay đường tắt chỉ để qua kiểm tra;
    - không giấu hay làm yếu test;
    - giữ hợp đồng công khai;
    - dùng pattern và helper có sẵn;
    - không ghi mã plan, finding hay task vào code, tên test, tên migration.
  - Bổ sung `flow.working-rules`: đọc code và tài liệu trước khi hỏi; không đảo quyết định người dùng đã chốt; quyết định
    đã kiểm chứng chỉ đổi khi có bằng chứng mới.
  - Bổ sung `flow.needs-info`: trước khi chọn `needs_info`, trình bày phân tích và các phương án (mỗi phương án tự đứng
    được, kèm ảnh hưởng); câu hỏi chia theo nhóm (phạm vi, hành vi, dữ liệu, rủi ro).
  - Ánh xạ giao thức trạng thái của CK sang outcome của aw: `DONE_WITH_CONCERNS` thành `done` kèm mục "Điểm còn lo ngại"
    trong câu trả lời cuối; `NEEDS_CONTEXT` thành `needs_info`; `BLOCKED` thành `escalated` khi node có outcome đó.
- **Verify:** lint V10-00 sạch; `aw-publish.py --check` OK cho hai hướng dẫn; `context-check.sh` cho node `build` và
  `spec` thấy resource mới và không resource nào bị bỏ vì ngân sách.
- **Hoàn thành khi:** các luật trên nằm đúng một chỗ trong kit và đến mọi agent qua resource dùng chung.
- **Nguồn:** HE-04-M07, HE-01-M03, HE-01-M04, HE-07-M05.

## V10-04 — BRAINSTORM: tìm vấn đề gốc trước giải pháp

- **Mục tiêu:** `01-brainstorm.md` nêu được vấn đề thật, các hướng thay thế và giả định cần kiểm chứng, thay vì nhận ngay
  giải pháp người dùng đưa ra.
- **Phụ thuộc:** V10-03.
- **Đầu vào CK:** `skills/brainstorm/SKILL.md`, `skills/brainstorm/references/problem-first.md`, `agents/brainstormer.md`,
  `skills/problem-solving`.
- **Thực hiện:** skill mới `skill-brainstorm` (key dự kiến: `brainstorm.problem-first`, `brainstorm.options`,
  `brainstorm.validation`), gồm:
  - nhận ra khi yêu cầu "nhảy vào giải pháp"; nêu vấn đề gốc và phát biểu vấn đề;
  - thách thức giả định; đưa 2–3 hướng có ưu/nhược và tác động lên người dùng, người phát triển, vận hành;
  - đánh giá thẳng thắn (quá phức tạp, chưa cần);
  - trạng thái bằng chứng của từng giả định và cách kiểm chứng.

  Cập nhật `flow.brainstorm` để yêu cầu các mục tương ứng trong `01-brainstorm.md`; gắn skill vào agent BRAINSTORM của hai
  hướng dẫn.
- **Verify:** lint sạch; `context-check.sh` cho node `brainstorm` có `brainstorm.*`, agent khác không có;
  `check-feature-docs` vẫn chấp nhận tài liệu mẫu.
- **Hoàn thành khi:** skill được publish và gắn đúng agent; rubric BRAINSTORM của V10-02 chấm được các mục mới.
- **Nguồn:** HE-01-M03, HE-07-M01.

## V10-05 — SPEC: thách thức phạm vi và kịch bản kiểm thử

- **Mục tiêu:** `02-spec.md` có phạm vi đã được thách thức và acceptance criteria phủ đường lỗi, edge case theo từng
  chiều.
- **Phụ thuộc:** V10-04.
- **Đầu vào CK:** `skills/ck-plan/references/scope-challenge.md`, `skills/ck-scenario/SKILL.md` (12 chiều phân rã),
  `skills/ck-plan/references/validate-question-framework.md`.
- **Thực hiện:**
  - Skill mới `skill-spec` (key dự kiến: `spec.scope-challenge`, `spec.scenarios`), gồm:
    - ba câu hỏi về phạm vi: cái gì đã có, thay đổi tối thiểu là gì, độ phức tạp ra sao; chọn mở rộng, giữ hoặc thu hẹp
      và ghi lý do;
    - lọc các chiều edge case áp dụng được, sinh kịch bản theo mức nghiêm trọng, chuyển thành AC Given/When/Then có đường
      lỗi.
  - Thêm checklist cho người duyệt GATE A vào hướng dẫn
    ([feature-task-flow.md](../guides/todolist-spring-react/feature-task-flow.md) và README issue tracker).
- **Verify:** lint sạch; `context-check.sh` cho node `spec`; `check-feature-docs` chấp nhận tài liệu mẫu có các mục mới.
- **Hoàn thành khi:** skill gắn vào agent SPEC của hai hướng dẫn; checklist GATE A có trong tài liệu.
- **Nguồn:** HE-01-M02, HE-07-M01, HE-08-S01.

## V10-06 — DESIGN: thiết kế có phản biện

- **Mục tiêu:** `03-design.md` có luồng dữ liệu, trade-off, rủi ro, failure mode và rollback, và đã tự phản biện trước
  GATE B; `tasks.json` chia việc theo quyền sở hữu file rõ ràng.
- **Phụ thuộc:** V10-05.
- **Đầu vào CK:** `agents/planner.md` (mô hình tư duy, checklist), `skills/ck-plan/references/solution-design.md`,
  `skills/ck-plan/references/codebase-understanding.md`, `skills/ck-plan/references/red-team-personas.md`,
  `skills/ck-predict/SKILL.md`.
- **Thực hiện:**
  - Skill mới `skill-design` (key dự kiến: `design.thinking`, `design.solution`, `design.codebase`, `design.red-team`),
    gồm:
    - đọc code liên quan trước; mọi khẳng định về code trích `file:line`;
    - luồng dữ liệu, phụ thuộc, bảo mật, hiệu năng, edge case và failure mode, tương thích ngược, rollback;
    - tự phản biện từ vài góc nhìn (kiến trúc, bảo mật, hiệu năng, người dùng) và ghi những điểm đã sửa;
    - `tasks.json`: không hai task cùng sửa một file; tiêu chí xong đo được.
  - Thêm checklist cho người duyệt GATE B vào hai hướng dẫn.
- **Verify:** lint sạch; `context-check.sh` cho node `design`; `check-feature-docs` chấp nhận `tasks.json` mẫu.
- **Hoàn thành khi:** skill gắn vào agent DESIGN của hai hướng dẫn; checklist GATE B có trong tài liệu.
- **Nguồn:** HE-07-M07, HE-03-M08, HE-01-M03.

## V10-07 — FRAME và PLAN: chọn lane, plan kiểm chứng được

- **Mục tiêu:** FRAME chọn lane theo tiêu chí kiểm được; `plan.md` đủ cụ thể để BUILD không phải đoán.
- **Phụ thuộc:** V10-06.
- **Đầu vào CK:** `skills/fix/references/complexity-assessment.md`, `skills/fix/references/mode-selection.md`,
  `skills/cook/references/workflow-routing.md`, `agents/planner.md` (Behavioral Checklist, Verification Discipline),
  `skills/ck-plan/references/plan-organization.md`, `skills/ck-plan/references/task-management.md`.
- **Thực hiện:** skill mới `skill-plan` (key dự kiến: `frame.complexity`, `plan.checklist`, `plan.verification`), gồm:
  - tiêu chí đơn giản / vừa / phức tạp và lane tương ứng;
  - plan có file sẽ sửa theo thứ tự, phụ thuộc, ma trận test, rủi ro, tiêu chí xong đo được;
  - kỷ luật kiểm chứng: tìm lại trong code thay vì chép tóm tắt, trích `file:line`, liệt kê đủ nơi gọi, kiểm vòng đời
    trước khi thêm trạng thái.
- **Verify:** lint sạch; `context-check.sh` cho node `frame` và `plan`.
- **Hoàn thành khi:** skill gắn vào agent FRAME và PLAN của hai hướng dẫn.
- **Nguồn:** HE-07-M01, HE-07-S01, HE-11-M05.

## V10-08 — BUILD: viết code, sửa khi kiểm tra đỏ, viết test

- **Mục tiêu:** BUILD làm theo pattern có sẵn; ở vòng sửa thì tìm nguyên nhân gốc trước khi sửa lại; test kiểm hành vi
  và có đường lỗi.
- **Phụ thuộc:** V10-07.
- **Đầu vào CK:** `skills/cook/references/workflow-steps.md` (checklist trước khi viết code), `rules/primary-workflow.md`,
  `agents/fullstack-developer.md`, `skills/ck-debug/references/` (`systematic-debugging`, `root-cause-tracing`,
  `verification`), `skills/fix/references/diagnosis-protocol.md`, `agents/debugger.md`, `agents/tester.md`,
  `skills/test/SKILL.md`.
- **Thực hiện:**
  - Bổ sung `skill-maker`: checklist trước khi viết code (quy ước, code lân cận, helper có sẵn, hợp đồng giao diện).
  - Skill mới `skill-debug` (key dự kiến: `debug.root-cause`): khi prompt có `checkFailures`, nêu 2–3 giả thuyết, loại
    trừ bằng bằng chứng đọc được từ output và code, nêu nguyên nhân gốc trước khi sửa. Lỗi bắt đầu bằng `MÔI TRƯỜNG:`
    thì không sửa code. Viết cho tình huống agent không chạy được lệnh.
  - Skill mới `skill-test` (key dự kiến: `test.behavior`, `test.error-paths`): test theo hành vi, có đường lỗi và giá trị
    biên; không làm yếu test để qua kiểm tra; ưu tiên test hẹp nhất đủ chứng minh.
  - Gắn vào agent BUILD và vào các node `implement`, `contract` của hướng dẫn.
- **Verify:** lint sạch; `context-check.sh` cho node `build` ở một vòng sửa giả lập (prompt có `checkFailures`) thấy
  `debug.*` và `test.*`; tổng resource của agent BUILD không vượt ngân sách.
- **Hoàn thành khi:** ba phần trên gắn đúng agent của hai hướng dẫn.
- **Nguồn:** HE-09-M07, HE-09-M08, HE-13-S01, HE-06-M07, HE-10-M01.

## V10-09 — REVIEW: dò edge case, báo cáo theo mức, bảo mật khi rủi ro cao

- **Mục tiêu:** reviewer độc lập tìm được lỗi thật, báo cáo có mức nghiêm trọng và câu hỏi chưa giải quyết; task rủi ro
  cao được kiểm bảo mật.
- **Phụ thuộc:** V10-08.
- **Đầu vào CK:** `skills/ck-code-review/SKILL.md`, `agents/code-reviewer.md`, `skills/ck-security/SKILL.md`,
  `rules/review-audit-self-decision.md` (phần threat model).
- **Thực hiện:**
  - `skill-code-review` v2: ghim lại xuất xứ theo CK `ed8a1fa`, thay cho bản người dùng cung cấp. Nội dung gồm:
    - dò edge case trước khi review;
    - báo cáo theo mức Critical / High / Medium / Low, kèm edge case tìm được và câu hỏi chưa giải quyết;
    - không hạ mức để cho qua;
    - xác định thứ thật sự cần bảo vệ trước khi nêu finding bảo mật.
  - Skill mới `skill-security` (selector `riskClasses: [HIGH]`, chỉ gắn vào agent reviewer): STRIDE và các nhóm lỗi OWASP
    chính, ở mức đọc code.
- **Verify:** lint sạch; `context-check.sh` cho node review: WorkItem rủi ro `MEDIUM` không có `security.*`, rủi ro `HIGH`
  có.
- **Hoàn thành khi:** `kit.json` không còn entry `license: UNKNOWN`; reviewer của hai hướng dẫn dùng bản mới.
- **Nguồn:** HE-09-M04, HE-09-M05, HE-09-S02, HE-11-M06, HE-11-S04.

## V10-10 — SYNC: cập nhật tài liệu đúng lúc

- **Mục tiêu:** SYNC chỉ cập nhật tài liệu khi hành vi, lệnh, kiến trúc hay hợp đồng thay đổi, và giữ tài liệu gọn.
- **Phụ thuộc:** V10-09.
- **Đầu vào CK:** `agents/docs-manager.md`, `skills/docs/references/update-workflow.md`,
  `rules/documentation-management.md`.
- **Thực hiện:** skill mới `skill-docs` (key dự kiến: `docs.when`, `docs.how`): khi nào cần cập nhật; tài liệu nào (README,
  `docs/`, tóm tắt codebase); giới hạn kích thước; kiểm liên kết. Gắn vào agent SYNC.
- **Verify:** lint sạch; `context-check.sh` cho node `sync`.
- **Hoàn thành khi:** skill gắn vào agent SYNC của hai hướng dẫn.
- **Nguồn:** HE-03-M03, HE-03-S02.

## V10-11 — Layer stack: API, SQL, React, test frontend

- **Mục tiêu:** tri thức theo stack đến đúng component cho BUILD, DESIGN và REVIEW.
- **Phụ thuộc:** V10-10.
- **Đầu vào CK:**
  - `skills/backend-development/references/` (`backend-api-design`, `backend-security`, `backend-testing`,
    `backend-code-quality`);
  - `skills/databases/references/` (`postgresql-queries`, `postgresql-performance`; chỉ phần SQL chung: index, migration,
    truy vấn);
  - `skills/react-best-practices` (Vercel Engineering; chỉ các luật áp dụng cho React + Vite);
  - `skills/web-testing/references/` (`unit-integration-testing`, `test-flakiness-mitigation`,
    `testing-pyramid-strategy`).
- **Thực hiện:** layer mới `layer-api-design` (thiết kế REST, lỗi dạng Problem, phân trang, tương thích ngược); bổ sung
  `layer-stack-spring-sqlite` (index, migration, truy vấn) và `layer-stack-react-vite` (hiệu năng render, test component,
  chống test chập chờn). Selector theo `componentTags`.
- **Verify:** lint sạch; `context-check.sh` cho task `backend` không thấy resource React, và ngược lại.
- **Hoàn thành khi:** layer gắn đúng pack và agent của hai hướng dẫn; giấy phép ghi theo từng skill nguồn (MIT, Apache-2.0
  hoặc độc quyền CK).
- **Nguồn:** HE-03-M04, HE-03-S01, HE-04-M02.

## V10-12 — Đo lần 1 (A1) và quyết định giữ hay bỏ

- **Mục tiêu:** biết phần tri thức nào giúp thật, theo từng node.
- **Phụ thuộc:** V10-03…V10-11.
- **Thực hiện:**
  - Chạy A1 (kit sau V10-11), 2 lượt, cùng fixture, model và phiên bản CLI với A0. Nếu commit aw khác với lúc chạy A0 thì
    chạy lại A0 trước.
  - So sánh theo node bằng `kit-metrics.py` và rubric, chấm mà không biết nhãn.
  - Quy tắc quyết định cho mỗi nhóm tri thức (V10-03…V10-11): giữ nếu không chỉ số nào của node đó xấu đi rõ (ngưỡng ghi
    trong báo cáo) và ít nhất một chỉ số tốt lên; nếu không, sửa một lần rồi đo lại ở V10-18, hoặc bỏ.
  - Ghi rõ giới hạn: 2 lượt mỗi lần đo chỉ thấy được khác biệt lớn.
- **Verify:** báo cáo `kit/bench/reports/A1.md` có run id, số liệu theo node của A0 và A1, điểm rubric và quyết định cho
  từng nhóm.
- **Hoàn thành khi:** mỗi nhóm V10-03…V10-11 có quyết định giữ / sửa / bỏ kèm số đo; phần bị bỏ được gỡ khỏi kit.
- **Nguồn:** HE-01-M06, HE-02-S02, HE-02-S03, HE-12-S04.

## V10-13 — Agent mẫu theo vai trò trong kit

- **Mục tiêu:** project không phải tự khai lại các agent của workflow mẫu; kit có thêm các vai trò debugger, reviewer,
  simplifier.
- **Phụ thuộc:** V10-12.
- **Đầu vào CK:** frontmatter `model` và mô tả vai trò trong `agents/` (`planner`, `debugger`, `tester`, `code-reviewer`,
  `code-simplifier`, `brainstormer`, `docs-manager`).
- **Thực hiện:**
  - Mục `agents` trong `kit.json`: các agent của workflow mẫu `agent-flow-brainstorm`, `-spec`, `-design`, `-frame`,
    `-plan`, `-build`, `-sync` (hiện đang khai lặp ở hai hướng dẫn) và vai trò mới `agent-debugger`, `agent-reviewer`,
    `agent-simplifier`; model mặc định theo vai trò.
  - `aw-publish.py`: hỗ trợ `{"id": …, "from": "kit"}` cho agent, kèm `addResources` để project thêm Layer và skill riêng.
  - Chuyển hai hướng dẫn sang dùng agent mẫu.
- **Verify:** `check-kit.sh` có test `from kit` cho agent; `aw-publish.py --check` OK cho hai hướng dẫn; sau khi chuyển,
  CONTEXT policy của từng agent giống trước (so bằng `aw definition show`), trừ phần cố ý thêm.
- **Hoàn thành khi:** hai hướng dẫn không còn khai lặp agent của workflow mẫu.
- **Nguồn:** HE-13-S02, HE-04-M02, HE-04-M07.

## V10-14 — Workflow mẫu: node debug, review độc lập, `wf-bugfix`

- **Mục tiêu:** chuyển đường đi `/cook` và `/fix` của CK thành graph có giới hạn của aw.
- **Phụ thuộc:** V10-13.
- **Đầu vào CK:** `skills/cook/references/workflow-steps.md`, `skills/fix/references/` (`diagnosis-protocol`,
  `prevention-gate`, `review-cycle`), `rules/primary-workflow.md`.
- **Thực hiện:**
  - `wf-task-delivery-plus`: frame → plan → build → gate1.
    - gate1 đỏ → **debug** (CHECKER, `agent-debugger`, chỉ đọc; chẩn đoán được gửi về maker) → build;
    - gate1 xanh → quality → **review** (CHECKER, `agent-reviewer`) → `rework` về build, hoặc `approved` → gate2 → sync;
    - mọi vòng lặp có giới hạn theo policy.
  - `wf-bugfix`: viết test tái hiện lỗi (MAKER) → COMMAND `expect-fail` (test phải đỏ trước khi sửa) → sửa (MAKER kèm
    `skill-debug`) → gate1 → review → gate2. Thêm script lệnh `kit/commands/expect-fail.sh`.
  - Gắn vào hướng dẫn issue tracker qua `from kit:` và `bind`.
- **Verify:**
  - `aw definition validate` qua;
  - agent giả lập đi hết mọi cạnh: gate đỏ → debug → build; review `rework`; `expect-fail` không đỏ thì quay lại viết
    test;
  - một lần chạy thật `wf-task-delivery-plus` trên fixture (khoảng 1–2 USD).
- **Hoàn thành khi:** hai workflow có trong kit, mọi cạnh có lần chạy giả lập và có một run thật.
- **Nguồn:** HE-14-M02, HE-14-M03, HE-14-M08, HE-09-M07, HE-13-M04, HE-13-S01.

## V10-15 — Hook của CK thành Command/Gate fail-closed

- **Mục tiêu:** các kiểm tra mà CK làm bằng hook (lỗi thì cho qua) trở thành bước kiểm tra của workflow (lỗi thì chặn).
- **Phụ thuộc:** V10-14.
- **Đầu vào CK:** `hooks/simplify-gate.cjs`, `hooks/workflow-artifact-gate.cjs`, `hooks/plan-format-kanban.cjs`,
  `hooks/privacy-block.cjs`, `agents/code-simplifier.md`.
- **Thực hiện:**
  - `cmd-diff-size`: diff vượt ngưỡng → outcome `large` → node simplify (MAKER, `agent-simplifier`) → gate1 lại. Gắn vào
    `wf-task-delivery-plus`.
  - `cmd-temp-artifacts`: chặn rác để lại trong diff (log gỡ lỗi, `debugger;`, `.only(`, `@Disabled`, TODO mới), thông
    báo theo dạng WHAT / WHY / FIX.
  - Mở rộng `check-feature-docs.sh`: kiểm các mục bắt buộc của `plan.md` theo quy ước của V10-07.
  - Ghi trong `kit/README.md`: `privacy-block` của CK tương ứng với `gate-secrets` đã có; `scout-block` không cần trong aw.
- **Verify:** `check-kit.sh` có mẫu đạt và không đạt cho từng lệnh; agent giả lập đi qua cạnh `large` → simplify.
- **Hoàn thành khi:** các lệnh có trong kit, có test và được gắn vào workflow mẫu.
- **Nguồn:** HE-04-S05, HE-10-M04, HE-10-M06, HE-12-M04.

## V10-16 — Học dần: bài học thành resource có version

- **Mục tiêu:** lỗi lặp lại ở review và gate trở thành luật mới có người duyệt, thay vì chỉ nằm trong transcript.
- **Phụ thuộc:** V10-15.
- **Đầu vào CK:** `skills/journal/SKILL.md`, `skills/retro/SKILL.md`, `agents/journal-writer.md`.
- **Thực hiện:**
  - `wf-retro`: agent đọc tóm tắt từ `kit-metrics.py` và finding của reviewer (gửi dạng message), đề xuất
    `docs/lessons/<ngày>.md`. Mỗi mục có bằng chứng, số lần lặp, luật đề xuất và chỗ gắn (`skill#key` hoặc gate). Sau đó
    là APPROVAL của người, rồi commit.
  - `kit/scripts/lessons-to-skill.py`: chuyển các mục đã duyệt thành resource của một skill riêng của project (ví dụ
    `skill-project-lessons`), tăng version và publish.
- **Verify:** chạy với dữ liệu của các lượt đo A0, A1 bằng agent giả lập: ra file bài học, người duyệt, sinh skill mới,
  publish ra version mới.
- **Hoàn thành khi:** quy trình được mô tả trong `kit/README.md` và có một ví dụ lấy từ dữ liệu đo thật.
- **Nguồn:** HE-10-M05, HE-10-S05, HE-12-S05, HE-03-M08.

## V10-17 — Thử nghiệm: để nguyên skill CK trong `.claude/skills/` của repository đích

- **Mục tiêu:** quyết định có nên dùng skill CK nguyên bản (không chắt lọc) bên cạnh tri thức của aw hay không.
- **Phụ thuộc:** V10-02.
- **Thực hiện:** aw gọi Claude CLI bằng `claude -p … --output-format stream-json` và không truyền `--setting-sources`, nên
  thư mục `.claude/` trong worktree nhiều khả năng được CLI nạp. Trên bản cài nháp dựng từ fixture, đặt vài skill CK vào
  `.claude/skills/` của repository, chạy một node thật và trả lời:
  - CLI có nạp skill không, và nạp lúc nào;
  - ContextSnapshot có ghi lại không (nếu không, tri thức đó nằm ngoài manifest của attempt);
  - hook trong `.claude/settings.json` có chạy không, có xung đột với chế độ không tương tác không;
  - token và chi phí tăng bao nhiêu.
- **Verify:** báo cáo `kit/bench/reports/spike-claude-skills.md` có run id và bằng chứng cho từng câu hỏi.
- **Hoàn thành khi:** có verdict `DÙNG | KHÔNG DÙNG | DÙNG CÓ ĐIỀU KIỆN`. Việc cần sửa core (ví dụ ghi skill nguyên bản vào
  manifest) được ghi thành đề xuất cho version sau, không làm trong V10.
- **Nguồn:** HE-04-M06, HE-04-M01, HE-02-M01.

## V10-18 — Đo lần 2 (A2), cập nhật tài liệu, verdict V10

- **Mục tiêu:** đánh giá toàn bộ V10 và đóng version.
- **Phụ thuộc:** V10-12…V10-17.
- **Thực hiện:**
  - Chạy A2 (kit cuối, workload dùng `wf-task-delivery-plus`), 2 lượt; so với A0 và A1. A1 tách được hiệu quả của tri
    thức; A2 cộng thêm hiệu quả của agent và workflow mới.
  - Cập nhật `kit/README.md` (bảng tri thức theo node, quy ước chắt lọc), hai hướng dẫn, và
    [`docs/guides/aw-entities.md`](../guides/aw-entities.md) nếu cần.
  - Ghi verdict `V10_DONE | REWORK | CHƯA ĐỦ EVIDENCE`.
- **Verify:** các lệnh ở mục "Gate của V10" chạy trên commit được đánh giá.
- **Hoàn thành khi:** verdict được ghi từ chính các lệnh gate chạy trên commit đã merge.
- **Nguồn:** ROADMAP-§6.

## Gate của V10

Kế thừa gate chung ở mục 6 của `00-roadmap.md`, thêm:

1. Không có thay đổi trong core: `git diff --name-only ef59084..HEAD -- internal cmd web go.mod go.sum` rỗng.
2. `kit/tests/check-kit.sh` xanh; `aw-publish.py --check` OK cho hai hướng dẫn; publish lần thứ hai không sinh version mới.
3. Mọi mục lấy từ CK có `origin` dạng `claudekit-engineer@ed8a1fa:<đường dẫn>` và `license` theo từng skill; lint không
   còn cấu trúc chỉ có ở CK; `--share` liệt kê đúng các mục không được phân phối lại.
4. Trong các lượt đo, không agent nào bị bỏ resource vì vượt ngân sách hay có quá 15 `HARD_CONSTRAINT`.
5. Báo cáo A0, A1, A2 có đủ chỉ số theo node; mọi phần tri thức giữ lại có số đo hoặc lý do ghi rõ.
6. `go test ./...` và CI hiện có (gồm `v8-alpha-gate`) vẫn xanh.
