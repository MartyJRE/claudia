# A stand-in for Claude Code's plan prompt: draws it, asks for bracketed paste
# like Claude Code does, and appends every byte it receives to argv[1] until
# enter comes after a paste. With argv[2..4] (profile dir, session id, cwd) it
# also registers itself as a live session, as Claude Code does.
import json, os, sys, termios, tty

log = sys.argv[1]
if len(sys.argv) > 4:
    prof, sid, cwd = sys.argv[2:5]
    os.makedirs(os.path.join(prof, "sessions"), exist_ok=True)
    with open(os.path.join(prof, "sessions", "%d.json" % os.getpid()), "w") as f:
        json.dump({"pid": os.getpid(), "sessionId": sid, "cwd": cwd, "status": "waiting", "kind": "interactive"}, f)

sys.stdout.write(" Claude has written up a plan and is ready to execute. Would you like to proceed?\r\n")
sys.stdout.write("   1. Yes, and use auto mode\r\n   2. Yes, manually approve edits\r\n")
sys.stdout.write(" > 3. Tell Claude what to change\r\n\x1b[?2004h")
sys.stdout.flush()
fd = sys.stdin.fileno()
old = termios.tcgetattr(fd)
tty.setraw(fd)
got = b""
try:
    while True:
        b = os.read(fd, 4096)
        if not b:
            break
        got += b
        with open(log, "ab") as f:
            f.write(b)
        if b"\x1b[201~" in got and got.endswith(b"\r"):
            break
finally:
    termios.tcsetattr(fd, termios.TCSADRAIN, old)
