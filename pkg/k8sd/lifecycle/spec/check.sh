#!/usr/bin/env bash
# Model-checks NodeLifecycle.tla and NodeLifecyclePlusCal.tla with TLC.
#
# Requires java (>= 11). Set TLA2TOOLS to an existing tla2tools.jar, otherwise
# v1.8.0 is downloaded to ${XDG_CACHE_HOME:-$HOME/.cache}/tlaplus.
set -euo pipefail

cd "$(dirname "$0")"

JAVA="${JAVA:-java}"
TLA2TOOLS="${TLA2TOOLS:-${XDG_CACHE_HOME:-$HOME/.cache}/tlaplus/tla2tools-1.8.0.jar}"
if [[ ! -f "$TLA2TOOLS" ]]; then
  mkdir -p "$(dirname "$TLA2TOOLS")"
  curl -fsSL -o "$TLA2TOOLS" https://github.com/tlaplus/tlaplus/releases/download/v1.8.0/tla2tools.jar
fi

meta="$(mktemp -d)"
trap 'rm -rf "$meta"; rm -f ./*_TTrace_*' EXIT

tlc() {
  "$JAVA" -XX:+UseParallelGC -cp "$TLA2TOOLS" tlc2.TLC -workers auto -metadir "$meta/$1" -config "$1" "$2" 2>&1
}

expect_pass() {
  local out
  out="$(tlc "$1" "$2")" || true
  if ! grep -q "No error has been found" <<<"$out"; then
    echo "FAIL: $1 should pass"; echo "$out" | tail -40; exit 1
  fi
  echo "PASS: $1 ($(grep -Eo '[0-9,]+ distinct states found' <<<"$out" | tail -1))"
}

expect_violation() {
  local out
  out="$(tlc "$1" "$2")" || true
  if ! grep -Eq "$3 is violated" <<<"$out"; then
    echo "FAIL: $1 should violate $3"; echo "$out" | tail -40; exit 1
  fi
  echo "PASS: $1 counterexample found ($3)"
}

expect_pass NodeLifecycle.cfg NodeLifecycle.tla
expect_pass NodeLifecyclePlusCal.cfg NodeLifecyclePlusCal.tla
expect_violation NodeLifecycle_stop_before_remove.cfg NodeLifecycle.tla "OrchestratorNeverBreaksQuorum"
expect_violation NodeLifecycle_unguarded_remove.cfg NodeLifecycle.tla "EtcdReconfigPreservesQuorum"
expect_violation NodeLifecycle_force_remove.cfg NodeLifecycle.tla "NoOrphanedEtcdMember"
