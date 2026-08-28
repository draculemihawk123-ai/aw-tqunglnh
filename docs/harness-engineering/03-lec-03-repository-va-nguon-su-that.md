# Lec 03 — Repository và nguồn sự thật

> Nguồn: [Lecture 03 — Making the Repository the Single Source of Truth](https://github.com/walkinglabs/learn-harness-engineering/blob/77e7a3e21469dcbece2558086c8d91657abeaa40/docs/en/lectures/lecture-03-why-the-repository-must-become-the-system-of-record/index.md)

## Luận điểm

Agent không thể tuân thủ tri thức mà nó không nhìn thấy. Kiến trúc, convention, lệnh vận hành và lý do thiết kế phải nằm trong nguồn bền vững, có thể định tuyến và có version.

Với Agent Kit, “repo là source of truth” cần được điều chỉnh thành **một nguồn authoritative cho mỗi concern**:

- Git repository là sự thật cho code, architecture và convention của project.
- Agent Kit database là sự thật cho runtime state, assignment, approval và audit.
- Artifact store là sự thật cho blob/log/output lớn; database giữ metadata và hash.

Đây không phải ba nguồn cạnh tranh; chúng sở hữu ba loại dữ liệu khác nhau.

## Fresh-run questions

Một runner/session sạch phải tìm được câu trả lời có nguồn cho:

1. Hệ thống này làm gì cho người dùng?
2. Code được tổ chức theo repository/component/module nào?
3. Khởi động và chuẩn bị môi trường bằng cách nào?
4. Xác minh thay đổi bằng command/gate nào?
5. WorkItem đang ở đâu, đã có evidence gì và bước tiếp theo là gì?

Bốn câu đầu chủ yếu đến từ repo và Engineering Pack. Câu cuối đến từ Agent Kit runtime state.

## Tiêu chí bắt buộc

- **HE-03-M01 — Source ownership:** mỗi knowledge/state field MUST khai báo nguồn authoritative; không có hai writer độc lập.
- **HE-03-M02 — Repo revision:** mọi ContextSnapshot và evidence liên quan code MUST gắn repository, commit/base revision và workspace/worktree.
- **HE-03-M03 — Discoverable entrypoint:** mỗi repository/component MUST có entry resource hoặc manifest chỉ đường tới overview, architecture, commands và hard constraints.
- **HE-03-M04 — Knowledge proximity:** rule đặc thù component MUST nằm gần component hoặc được selector định tuyến chính xác; không chỉ nằm trong global blob.
- **HE-03-M05 — Fresh-run gate:** project onboarding/publish pack MUST có fresh-run test với năm câu hỏi trên.
- **HE-03-M06 — Knowledge provenance:** resource MUST có owner/source, applicability và `last_verified` hoặc revision tương đương.
- **HE-03-M07 — No dual status writes:** file task/progress trong repo MUST là artifact/projection nếu DB đã sở hữu runtime state; agent không được sửa file và DB như hai nguồn ngang hàng.
- **HE-03-M08 — Durable decisions:** quyết định hệ trọng và lý do MUST được lưu trong decision artifact có version, không chỉ trong transcript.

## ACID cho worktree/task family

- **Atomicity:** một logical change được merge/publish cùng evidence; attempt bỏ dở không lẫn vào worktree khác.
- **Consistency:** gate xác nhận repository không suy giảm so với baseline và đáp ứng invariant.
- **Isolation:** task family có worktree riêng; sibling writer dùng lease/path scope.
- **Durability:** commit, event, decision, evidence và artifact sống qua process/session.

## Tiêu chí nên có

- **HE-03-S01:** Context resolver SHOULD ưu tiên knowledge gần target component hơn tài liệu toàn cục.
- **HE-03-S02:** CI/quality workflow SHOULD phát hiện doc/resource stale khi code owner hoặc contract liên quan thay đổi.
- **HE-03-S03:** Project SHOULD đo knowledge visibility gap: số câu hỏi hệ trọng không có nguồn bền vững.
- **HE-03-S04:** UI SHOULD cho phép đi từ rule/evidence đến source revision và ngược lại.

## Evidence và chỉ số

- Fresh-run answer rate có trích source.
- Knowledge visibility gap.
- Discovery time/token trước khi bắt đầu task.
- Tỷ lệ resource quá hạn `last_verified`.
- Số lần DB và repo projection lệch nhau.
- Tỷ lệ ContextSnapshot thiếu commit/component provenance.

## Ánh xạ vào Agent Kit

- `Project -> Repository[] -> Component[]` catalog; mọi revision/context/evidence phải chỉ rõ `repository_id`.
- `KnowledgeResource` và `ContextRoute` có selector/version/owner.
- `ContextSnapshot` lưu manifest, không copy mù toàn bộ repository.
- `DecisionArtifact` gắn WorkItem/Component/Run.
- `RepositoryProvider` và `WorkspaceProvider` chịu trách nhiệm revision/worktree.
- Runtime DB là authority; tài liệu repo có thể được attach hoặc export nhưng không tự chuyển state.

## Anti-patterns

- Đồng bộ mọi Slack/Confluence vào prompt mà không chọn lọc và không có owner.
- Tin một file tài liệu stale hơn code.
- Lưu `current_status` cả trong Markdown và DB rồi cho hai phía cùng sửa.
- Context chỉ chứa path mà không giữ revision/hash.
- Dùng provider session làm nơi duy nhất lưu quyết định.

## Phép thử chấp nhận

1. Fresh runner nhận repo + WorkItem ID, trả lời đủ năm câu kèm source.
2. Sửa workflow runtime status trong DB không đòi agent sửa file Markdown để giữ consistency.
3. Thay code sau khi evidence được tạo phải khiến evidence cũ hiện rõ là gắn với revision trước.
4. Hai task family chạy song song phải có worktree và base revision tách biệt.
