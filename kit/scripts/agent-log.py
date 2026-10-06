#!/usr/bin/env python3
"""In những gì agent đã NÓI trong một run: thông điệp cuối của từng lần agent chạy (tóm tắt việc đã làm, câu hỏi,
kết luận review), kèm số token và chi phí mà provider báo cáo cho lần đó.

`aw` (CLI, HTTP, UI) chưa có thao tác nào đọc lời của agent: tab Chat chỉ có message của người vận hành. Script này
đọc thẳng bảng agent_events trong database của bản cài (chỉ đọc). Đây là chi tiết cài đặt của Alpha, không phải API
công khai, giống như worktree-path.sh.

Cách dùng:
    agent-log.py <runId>                 # thông điệp cuối của mỗi lần agent chạy trong run
    agent-log.py <runId> --node build    # chỉ node build
    agent-log.py <runId> --all           # mọi thông điệp, không chỉ thông điệp cuối
    agent-log.py <runId> --denied        # thêm các lệnh agent định chạy nhưng bị từ chối

Biến môi trường: AW_DB (giống aw serve/worker).
"""
import argparse
import json
import os
import sqlite3
import sys


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("run_id")
    parser.add_argument("--node", help="chỉ in node có key này")
    parser.add_argument("--all", action="store_true", help="in mọi thông điệp của agent")
    parser.add_argument("--denied", action="store_true", help="in các lần gọi công cụ bị từ chối")
    args = parser.parse_args()
    database = os.environ.get("AW_DB")
    if not database:
        sys.exit("cần biến AW_DB")
    if hasattr(sys.stdout, "reconfigure"):
        sys.stdout.reconfigure(encoding="utf-8")
    db = sqlite3.connect("file:" + database.replace("\\", "/") + "?mode=ro", uri=True)
    db.row_factory = sqlite3.Row
    attempts = db.execute(
        """select a.id, a.attempt_no, a.state, a.termination_reason, n.node_key, n.iteration, n.activation_sequence
           from execution_attempts a join node_runs n on n.id = a.node_run_id
           where n.run_id = ? and a.provider_key is not null and a.provider_key != ''
           order by n.activation_sequence, a.attempt_no""", (args.run_id,)).fetchall()
    if not attempts:
        sys.exit(f"run {args.run_id} không có attempt nào của agent")
    for attempt in attempts:
        if args.node and attempt["node_key"] != args.node:
            continue
        events = db.execute("select kind, payload_json from agent_events where attempt_id = ? order by sequence",
                            (attempt["id"],)).fetchall()
        messages, usage, denied, pending = [], None, [], {}
        for event in events:
            payload = json.loads(event["payload_json"])
            if event["kind"] == "ASSISTANT_MESSAGE" and payload.get("message", "").strip():
                messages.append(payload["message"].strip())
            elif event["kind"] == "USAGE_REPORTED":
                usage = payload.get("usage")
            elif event["kind"] == "TOOL_CALL_STARTED":
                tool = payload.get("tool") or {}
                pending[tool.get("CallID")] = tool
            elif event["kind"] == "TOOL_CALL_FINISHED":
                tool = payload.get("tool") or {}
                if tool.get("IsError") and "requires approval" in str(tool.get("Output", "")):
                    started = pending.get(tool.get("CallID")) or {}
                    denied.append("%s %s" % (started.get("Name", "?"), json.dumps(started.get("Input"), ensure_ascii=False)[:200]))
        header = "== #%s %s (vòng %s) attempt %s: %s" % (attempt["activation_sequence"], attempt["node_key"],
                                                         attempt["iteration"], attempt["attempt_no"], attempt["state"])
        if usage:
            header += "  [%s token vào, %s token ra, %.4f USD]" % (
                usage.get("InputTokens", 0) + usage.get("CachedInputTokens", 0), usage.get("OutputTokens", 0),
                usage.get("CostUSD", 0))
        print(header)
        for message in (messages if args.all else messages[-1:]):
            print(message)
            print()
        if not messages:
            print("(agent không để lại thông điệp nào)\n")
        if args.denied and denied:
            print("-- %d lần gọi công cụ bị từ chối vì cần phê duyệt:" % len(denied))
            for line in denied:
                print("   " + line)
            print()


if __name__ == "__main__":
    main()
