#!/usr/bin/env bash
# Model-check every scenario and print one result line each.
set -euo pipefail

cd "$(dirname "$0")"

M3="m1,m2,m3"
CFG='{"configure"}'
SVC='{"service"}'
# Columns:
#   name | Members | FixRow4 FixGuard FixArm | ArmTimeouts AllowCrashes |
#   FailureModes | invariants | properties
P="FALSE FALSE FALSE"  # proposal as written
X="TRUE TRUE TRUE"     # all fixes
scenarios=(
    "proposal                   |$M3|$P|TRUE TRUE  |{}  |NoDowngradePanic StorageMigratedBeforeSwap|"
    "fixed                      |$M3|$X|TRUE TRUE  |{}  |NoDowngradePanic StorageMigratedBeforeSwap|"
    "configure-abort/proposal   |$M3|TRUE FALSE FALSE|FALSE FALSE|$CFG|NoDowngradePanic|"
    "configure-abort/fixed      |$M3|$X|FALSE FALSE|$CFG|NoDowngradePanic NoStrandedMember StorageMigratedBeforeSwap|"
    "configure-abort/+crashes   |$M3|$X|FALSE TRUE |$CFG|NoDowngradePanic StorageMigratedBeforeSwap|StartupSafe"
    "configure-abort/+timeouts  |$M3|$X|TRUE FALSE |$CFG|NoDowngradePanic StorageMigratedBeforeSwap|"
    "configure-abort/+timeouts-s|$M3|$X|TRUE FALSE |$CFG|NoStrandedMember|"
    "configure-abort/all-faults |$M3|$X|TRUE TRUE  |$CFG|StorageMigratedBeforeSwap|StartupSafe"
    "service-abort/fixed        |$M3|$X|FALSE FALSE|$SVC|NoDowngradePanic StorageMigratedBeforeSwap|"
    "service-abort/stranding    |$M3|$X|FALSE FALSE|$SVC|NoStrandedMember|"
    "service-abort/all-faults   |$M3|$X|TRUE TRUE  |$SVC|StorageMigratedBeforeSwap|StartupSafe"
    "single/configure-proposal  |m1 |TRUE FALSE FALSE|FALSE FALSE|$CFG|NoDowngradePanic NoStrandedMember|"
    "single/configure-fixed     |m1 |$X|FALSE FALSE|$CFG|NoDowngradePanic NoStrandedMember|"
    "single/service-abort       |m1 |$X|FALSE FALSE|$SVC|NoStrandedMember|"
)

cfg=".check-all.cfg"
trap 'rm -f "$cfg" ./*_TTrace_*' EXIT

for s in "${scenarios[@]}"; do
    IFS='|' read -r name members fixes faults failures invariants properties <<<"$s"
    read -r row4 guard arm <<<"$fixes"
    read -r timeouts crashes <<<"$faults"
    {
        echo "CONSTANTS"
        echo "    Members = {${members// /}}"
        echo "    FixRow4 = ${row4}"
        echo "    FixGuard = ${guard}"
        echo "    FixArm = ${arm}"
        echo "    ArmTimeouts = ${timeouts}"
        echo "    AllowCrashes = ${crashes}"
        echo "    FailureModes = ${failures// /}"
        echo "SPECIFICATION Spec"
        echo "INVARIANTS TypeOK ${invariants}"
        [[ -n "$properties" ]] && echo "PROPERTIES ${properties}"
    } >"$cfg"
    out="$(./run-tlc.sh "$cfg" 2>&1 || true)"
    if grep -q 'No error has been found' <<<"$out"; then
        result="PASS ($(grep -oE '[0-9]+ distinct states found' <<<"$out" | tail -1))"
    elif v="$(grep -m1 -oE '(Invariant|Action property) [A-Za-z]+ is violated' <<<"$out")"; then
        result="FAIL: ${v} (trace length $(grep -cE '^State [0-9]+:' <<<"$out"))"
    else
        result="ERROR: $(grep -m1 -iE 'error' <<<"$out")"
    fi
    printf '%s %s\n' "$name" "$result"
done
