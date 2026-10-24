#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# Runs a CI step and, if it fails, publishes the end of its output as an
# annotation (readable through the GitHub API without downloading logs).
name="$1"; shift
log="$RUNNER_TEMP/step-$(echo "$name" | tr -c 'A-Za-z0-9' '_').log"
set -o pipefail
"$@" 2>&1 | tee "$log"
rc=${PIPESTATUS[0]}
if [ "$rc" -ne 0 ]; then
  # prefer compiler/test error lines, then the tail
  { grep -E "error:|Error |FAIL|panic:|undefined|cannot |fatal" "$log" | head -60; echo "----- last lines -----"; tail -80 "$log"; } > "$log.sum"
  msg=$(sed -e 's/%/%25/g' -e 's/\r/%0D/g' "$log.sum" | awk '{printf "%s%%0A", $0}')
  echo "::error title=$name failed::$msg"
fi
exit $rc
