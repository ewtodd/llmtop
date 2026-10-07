"""Drive the app in a real pty and snapshot the emulated screen over time."""

import argparse
import fcntl
import os
import pty
import select
import struct
import subprocess
import sys
import termios
import time

import pyte


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--seconds", type=float, default=60.0)
    parser.add_argument("--snapshot-every", type=float, default=10.0)
    parser.add_argument("--width", type=int, default=150)
    parser.add_argument("--height", type=int, default=46)
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command or [ "python", "-m", "llmtop", "--demo" ]
    if command and command[0] == "--":
        command = command[1:]

    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", args.height, args.width, 0, 0))
    process = subprocess.Popen(
        command,
        stdin=slave,
        stdout=slave,
        stderr=slave,
        close_fds=True,
        start_new_session=True,
    )
    os.close(slave)

    screen = pyte.Screen(args.width, args.height)
    stream = pyte.Stream(screen)
    deadline = time.time() + args.seconds
    next_snapshot = time.time() + args.snapshot_every
    snapshot_index = 0
    while time.time() < deadline:
        ready, _, _ = select.select([ master ], [], [], 0.2)
        if ready:
            try:
                data = os.read(master, 1 << 20)
            except OSError:
                break
            if not data:
                break
            stream.feed(data.decode("utf-8", "replace"))
        if time.time() >= next_snapshot:
            next_snapshot += args.snapshot_every
            snapshot_index += 1
            print(f"===== snapshot {snapshot_index} at {args.seconds - (deadline - time.time()):.0f}s =====")
            for line in screen.display:
                print(line.rstrip())
            print()

    print("===== final snapshot =====")
    for line in screen.display:
        print(line.rstrip())

    process.terminate()
    try:
        process.wait(timeout=3)
    except subprocess.TimeoutExpired:
        process.kill()
    os.close(master)
    return 0


if __name__ == "__main__":
    sys.exit(main())
