#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
out=THIRD_PARTY_LICENSES.txt
section() {
  echo "================================================================================"
  echo "$1"
  echo "================================================================================"
  echo
  cat "$2"
  for n in $(find "$(dirname "$2")" -maxdepth 1 -type f -iname 'notice*' | sort); do echo; cat "$n"; done
  echo
}
{
  echo "Quill includes the following third-party components."
  echo "Each component's licence is reproduced in full below."
  echo
  section "whisper.cpp (third_party/whisper.cpp)" third_party/whisper.cpp/LICENSE
  { go list -deps -f '{{if not .Standard}}{{with .Module}}{{.Path}} {{.Version}} {{.Dir}}{{end}}{{end}}' ./cmd/quill
    go list -deps -tags wayland -f '{{if not .Standard}}{{with .Module}}{{.Path}} {{.Version}} {{.Dir}}{{end}}{{end}}' ./cmd/quill
  } | sort -u | grep -v '^github.com/katachinosekai/' | while read -r path ver dir; do
      lic=$(find "$dir" -maxdepth 1 -type f \( -iname 'licen[cs]e*' -o -iname 'copying*' -o -iname 'unlicense*' \) | sort | head -1)
      [ -n "$lic" ] || { echo "no licence file for $path" >&2; exit 1; }
      section "$path ${ver:-(bundled in third_party)}" "$lic"
    done
} > "$out"
echo "wrote $out"
