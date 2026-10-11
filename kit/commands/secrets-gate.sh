#!/bin/sh
# MACHINE_GATE "secrets": no secret file or database file is in the repository (tracked, or not ignored by
# .gitignore). The gate runs in a scratch directory; $2 is the worktree path (read-only).
# The result is ONE JSON line on stdout; the exit code is always 0.
set -eu
repo=$2
found=$(git --no-optional-locks -C "$repo" ls-files --cached --others --exclude-standard \
  | grep -E '(^|/)\.env(\..*)?$|\.pem$|\.p12$|\.db$' | tr '\n' ' ' || true)
jq -cn --arg found "$found" '
  {NO_SECRET_FILES: (if $found == "" then {verdict: "PASS"} else {verdict: "FAIL", detail: ("files that must not be there: " + $found)} end)}'
