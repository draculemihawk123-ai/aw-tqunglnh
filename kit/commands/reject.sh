#!/bin/sh
# COMMAND node "reject": the stop point of a run that is not completed - the reviewer chooses "rejected"/"abandon",
# or a fix loop has run out of budget (cyclePolicy). Exits 1 so the run ends FAILED instead of reaching END
# (END counts as completed). The WorkItem is then BLOCKED with a RUN_FAILED blocker: see README section 4.7.
echo "Run stopped: the change was rejected or the allowed number of fix rounds is used up." >&2
exit 1
