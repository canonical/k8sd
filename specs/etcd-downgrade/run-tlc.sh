#!/usr/bin/env bash
# Run TLC on the host JDK if present, else via Apple container.
# Usage: ./run-tlc.sh [config.cfg] [extra TLC args...]
set -euo pipefail

cd "$(dirname "$0")"
cfg="${1:-EtcdNativeDowngrade.cfg}"
shift || true

jar="$(ls -d "$HOME"/.vscode/extensions/tlaplus.vscode-ide-*/tools/tla2tools.jar | tail -1)"
tlc_args=(-workers auto -cleanup -config "$cfg" "$@" EtcdNativeDowngrade.tla)

java_bin="${JAVA_HOME:-/opt/homebrew/opt/openjdk@21}/bin/java"
if [[ -x "$java_bin" ]]; then
    meta="$(mktemp -d)"
    trap 'rm -rf "$meta"' EXIT
    exec "$java_bin" -XX:+UseParallelGC -cp "$jar" tlc2.TLC -metadir "$meta" "${tlc_args[@]}"
fi

container run --rm \
    -v "$(dirname "$jar"):/tools" \
    -v "$PWD:/spec" \
    -w /spec \
    eclipse-temurin:21-jre \
    java -XX:+UseParallelGC -cp /tools/tla2tools.jar tlc2.TLC -metadir /tmp/tlc "${tlc_args[@]}"
