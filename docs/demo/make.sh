#!/bin/bash
# Records the README demo: builds prflow, drives it through record.py's
# storyline in --dry-run against throwaway repos, and renders stage.html
# around the recording, frame by frame, into prflow-demo.mp4.
#
# Needs Go, Python 3, Node, a Chromium (CHROME=/path/to/chrome) and ffmpeg
# with libx264 (FFMPEG=/path/to/ffmpeg, default: ffmpeg on PATH).
set -euo pipefail
cd "$(dirname "$0")"

build=.build
rm -rf "$build" && mkdir -p "$build/world/cfg"

version=$(git describe --tags --abbrev=0 2>/dev/null || echo dev)
go build -ldflags "-X main.Version=$version" -o "$build/prflow" ../../cmd/prflow

# Batch mode discovers real repos even in --dry-run, so give it some.
for r in frontend/web frontend/mobile frontend/admin backend/api backend/workers backend/billing; do
  mkdir -p "$build/world/repos/$r"
  git -C "$build/world/repos/$r" init -q
  git -C "$build/world/repos/$r" remote add origin "https://github.com/acme/$(basename "$r").git"
done
cat > "$build/world/cfg/prflow.toml" <<EOF
[paths]
repos_dir = '$PWD/$build/world/repos'

[tickets]
pattern = 'PROJ-[0-9]+'
linear_org = 'acme'

[update]
enabled = false
EOF

[ -d node_modules ] || npm install --silent
python3 record.py "$build/timeline.json"
node render.mjs "$build/timeline.json" prflow-demo.mp4
echo "wrote $(pwd)/prflow-demo.mp4"
