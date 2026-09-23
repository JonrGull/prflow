"""Drive prflow --dry-run through the demo storyline and record a timeline.

Events: [t, "o", data]   terminal output
        [t, "k", label]  a key press, for the on-screen keycap
        [t, "c", text]   caption change ("" hides it)
        [t, "z", rect]   camera target in cells {x,y,w,h}, or null for the whole window
"""
import os, pty, sys, time, select, json, fcntl, termios, struct, codecs

COLS, ROWS = 120, 40
here = os.path.dirname(os.path.abspath(__file__))
build = os.path.join(here, ".build")  # make.sh puts the binary and the fake repos here
KEYS = {"ENTER": ("\r", "⏎ Enter"), "ESC": ("\x1b", "Esc"), "SPACE": (" ", "Space"),
        "RIGHT": ("\x1b[C", "→"), "LEFT": ("\x1b[D", "←"), "DOWN": ("\x1b[B", "↓"), "UP": ("\x1b[A", "↑")}

# The storyline: (action, arg, pause-after-seconds).
STORY = [
    ("wait", None, 1.6),
    ("cap", "One TUI for your whole release train", 2.2),
    ("key", "2", 1.1),
    ("cap", "Pick the release step", 0.9),
    ("key", "ENTER", 2.6),
    ("cap", "Choose repos — commits load in the background", 0.6),
    ("key", "SPACE", 0.45), ("key", "DOWN", 0.4), ("key", "SPACE", 0.45),
    ("key", "RIGHT", 0.5), ("key", "SPACE", 0.45), ("key", "DOWN", 0.4), ("key", "SPACE", 1.7),
    ("key", "ENTER", 2.0),
    ("cap", "Tickets are pulled straight from the commits", 0.8),
    ("type", " · Sprint 42", 1.2),
    ("key", "ENTER", 0.8),
    ("cap", "Review every change before anything is created", 0.4),
    ("zoom", {"x": 54, "y": 3, "w": 64, "h": 19}, 3.0),
    ("zoom", None, 0.4),
    ("key", "y", 0.8),
    ("cap", "Created or updated in every repo, one at a time", 7.5),
    ("cap", "Every open release PR, one column per step", 0.4),
    ("key", "]", 3.2),
    ("cap", "Merge the whole step at once", 0.5),
    ("key", "a", 1.1),
    ("key", "ENTER", 1.0),
    ("key", "y", 3.4),
    ("cap", "Reviews, CI, previews and E2E at a glance", 0.4),
    ("key", "]", 2.6),
    ("key", "DOWN", 0.6), ("key", "DOWN", 0.6), ("key", "DOWN", 2.2),
    ("cap", "Watch GitHub Actions live", 0.4),
    ("key", "]", 2.6),
    ("key", "SPACE", 0.7), ("key", "DOWN", 0.4), ("key", "SPACE", 0.9),
    ("zoom", {"x": 50, "y": 6, "w": 68, "h": 16}, 3.4),
    ("zoom", None, 1.0),
    ("cap", "", 0.6),
]

def main(out):
    pid, fd = pty.fork()
    if pid == 0:
        # TERM_PROGRAM=WarpTerminal takes the app's own fast path past the
        # terminal colour queries, which nothing here would answer.
        env = dict(os.environ, XDG_CONFIG_HOME=build + "/world/cfg", TERM_PROGRAM="WarpTerminal", TERM="xterm-256color")
        os.execve(build + "/prflow", ["prflow", "--dry-run"], env)
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", ROWS, COLS, 0, 0))
    t0 = time.monotonic(); events = []
    # Reads can split a multi-byte character; decoding each read alone turns
    # one box-drawing char into two replacement chars, which wraps the line.
    utf8 = codecs.getincrementaldecoder('utf-8')('replace')
    now = lambda: round(time.monotonic() - t0, 4)

    def pump(secs):
        end = time.monotonic() + secs
        while time.monotonic() < end:
            r, _, _ = select.select([fd], [], [], 0.01)
            if r:
                try: data = os.read(fd, 65536)
                except OSError: return
                text = utf8.decode(data)
                if text: events.append([now(), "o", text])

    for action, arg, pause in STORY:
        if action == "key":
            seq, label = KEYS.get(arg, (arg, arg))
            events.append([now(), "k", label]); os.write(fd, seq.encode())
        elif action == "type":
            events.append([now(), "k", "⌨ " + arg.strip()])
            for ch in arg:
                os.write(fd, ch.encode()); pump(0.07)
        elif action == "cap":
            events.append([now(), "c", arg])
        elif action == "zoom":
            events.append([now(), "z", arg])
        pump(pause)
    os.kill(pid, 9)
    json.dump({"cols": COLS, "rows": ROWS, "duration": now(), "events": events}, open(out, "w"))
    print(f"recorded {now():.1f}s, {sum(e[1]=='o' for e in events)} output chunks")

main(sys.argv[1])
