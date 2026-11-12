#!/bin/sh
# COMMAND "ui-expect-fail" of wf-bugfix-ui (V12-31): the browser reproduction script must report the bug (see ui-check.sh).
set -eu
. "${AW_KIT:?set AW_KIT=<kit directory> when running by hand}/commands/lib.sh" # @aw-include
. "${AW_KIT:?set AW_KIT=<kit directory> when running by hand}/commands/ui-check.sh" # @aw-include
ui_check expect-fail
