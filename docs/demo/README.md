# Demo video

The README video is rendered from a scripted `--dry-run` session rather than
screen-recorded, so it can be re-made in one command after a UI change:

```bash
CHROME=/path/to/chrome FFMPEG=/path/to/ffmpeg ./make.sh   # writes prflow-demo.mp4
```

It needs Go, Python 3, Node, a Chromium and an ffmpeg built with libx264.
`make.sh` builds prflow, makes a few throwaway repos for batch mode to find,
and then:

1. **`record.py`** drives the app through a pseudo-terminal, following
   `STORY`, and saves a timeline: the terminal output, each key press, and
   the caption and zoom cues. Edit `STORY` to change what the video shows.
2. **`render.mjs`** plays that timeline in **`stage.html`** (xterm.js inside
   the window, with the captions, keycaps, chapters and camera around it) and
   screenshots it frame by frame at 30 fps into ffmpeg. Stepping frames rather
   than capturing in real time keeps the timing exact on any machine.

`node render.mjs .build/timeline.json stills 12,30` writes PNG stills at those
seconds instead, which is the quick way to check a change.

To publish, drag `prflow-demo.mp4` into the README in GitHub's editor. Only
uploaded attachments play inline; a video committed to the repo does not.
