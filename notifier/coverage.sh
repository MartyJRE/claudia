#!/usr/bin/env bash
# Runs the tests with coverage, prints the NotifierCore report and, last, its line percentage.
set -euo pipefail

cd "$(dirname "$0")"
swift test --enable-code-coverage
bin="$(swift build --show-bin-path)"
# The bundle's name differs between SwiftPM versions.
xctest=("$bin"/*.xctest)
obj="${xctest[0]}/Contents/MacOS/$(basename "${xctest[0]}" .xctest)"
prof="$bin/codecov/default.profdata"
xcrun llvm-cov report "$obj" -instr-profile "$prof" "$PWD/Sources/NotifierCore"
xcrun llvm-cov export -summary-only "$obj" -instr-profile "$prof" "$PWD/Sources/NotifierCore" |
  plutil -extract data.0.totals.lines.percent raw -
