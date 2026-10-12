# Shared library for COMMAND node scripts (not run directly). Load it with exactly one line at the top of a script:
#
#   . "${AW_KIT:?set AW_KIT=<kit directory> when running by hand}/commands/lib.sh" # @aw-include
#
# When published, aw-publish.py replaces that line with the contents of this file, so a published script is
# self-contained and its hash changes with the library. When run by hand (AW_KIT points at the kit directory) the line
# loads the real file.
#
# Contract of a COMMAND script (README section 3.4): exit code 0 means pass; errors go to STDERR because the agent only
# receives the last 4 KiB of stderr; no colour (NO_COLOR=1) so ANSI codes do not take space in those 4 KiB. The functions
# below implement that contract.
# COLORS=false and FORCE_COLOR=0: some libraries (Testing Library printing the DOM tree on a failed test) do not read
# NO_COLOR but read COLORS.
export NO_COLOR=1 CI=true COLORS=false FORCE_COLOR=0

# aw_fail <message...>: print a code error to stderr and exit 1 (the agent will fix the code).
aw_fail() { echo "$@" >&2; exit 1; }

# aw_env_fail <message...>: an error of the MACHINE running the step, not fixable by changing code. The exit code cannot tell
# the two kinds of error apart, so the message starts with "ENVIRONMENT:" and tells the agent to finish with the outcome
# needs_info instead of changing code to no purpose.
aw_env_fail() {
  echo "ENVIRONMENT: $* - not a code error; do not change code, finish with the outcome needs_info and state this clearly." >&2
  exit 1
}

# aw_step <step name> <command...>: run the command; silent on success. On failure print "<AW_STEP_HEAD> '<name>' failed"
# and the last ${AW_TAIL:-60} lines of its output (each line cut to 300 characters) to stderr, then exit 1.
# AW_STEP_HEAD (default "step") is the start of the message, for example "frontend: step".
# AW_ENV_ERRORS (regex, default empty): if the output of the failed step matches it, the error belongs to the MACHINE
# (network, certificate), not the code: print the last few lines and report it with aw_env_fail so the agent does not
# spend fix rounds on code in vain.
aw_step() {
  _aw_name=$1; shift
  _aw_log=$(mktemp)
  if "$@" > "$_aw_log" 2>&1; then rm -f "$_aw_log"; return 0; fi
  if [ -n "${AW_ENV_ERRORS:-}" ] && grep -q -E "$AW_ENV_ERRORS" "$_aw_log"; then
    tail -n 8 "$_aw_log" | cut -c1-300 >&2
    rm -f "$_aw_log"
    aw_env_fail "step '$_aw_name' cannot run because of the network or a certificate on the machine running it"
  fi
  echo "${AW_STEP_HEAD:-step} '$_aw_name' failed" >&2
  tail -n "${AW_TAIL:-60}" "$_aw_log" | cut -c1-300 >&2
  rm -f "$_aw_log"
  exit 1
}

# aw_wait_http <url> <name> <log file> [seconds, default 120]: wait until the url answers HTTP (needs curl).
# On timeout print an error with the last 40 lines of the log file and exit 1.
aw_wait_http() {
  _aw_i=0
  until curl -s -o /dev/null --max-time 2 "$1"; do
    _aw_i=$((_aw_i + 1))
    if [ "$_aw_i" -gt "${4:-120}" ]; then
      echo "$2 did not come up within ${4:-120} seconds" >&2
      tail -n 40 "$3" | cut -c1-300 >&2
      exit 1
    fi
    sleep 1
  done
}
