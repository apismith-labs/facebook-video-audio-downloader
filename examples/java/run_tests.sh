#!/usr/bin/env bash
# Compile and run the dependency-free Java test runners.
set -euo pipefail
cd "$(dirname "$0")"

JAVAC="${JAVAC:-javac}"
JAVA="${JAVA:-java}"

if ! command -v "$JAVAC" >/dev/null 2>&1; then
  echo "javac not found. Install a JDK 17+ and set JAVA_HOME, or set JAVAC/JAVA explicitly." >&2
  exit 2
fi

rm -rf out
"$JAVAC" -encoding UTF-8 -d out $(find src/main/java src/test/java -name '*.java')
"$JAVA" -cp out com.apismith.facebookdownload.OfflineTests
"$JAVA" -cp out com.apismith.facebookdownload.DownloadTests
