#!/usr/bin/env bash
# Compiles the NSIS installer with dummy build artifacts to catch script errors.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
stage="$(mktemp -d)"
trap 'rm -rf "${stage}"' EXIT
for name in ResearchAssistant.exe backend.exe sandbox.exe uv.exe; do
  printf 'dummy-%s' "${name}" > "${stage}/${name}"
done
mkdir -p "${stage}/webview"
printf '<html></html>' > "${stage}/webview/index.html"
cp "${root}/installer.nsi" "${stage}/installer.nsi"
cd "${stage}"
makensis -DVERSION=0.0.1 installer.nsi
test -s ResearchAssistant-Setup.exe
echo "installer smoke passed"
