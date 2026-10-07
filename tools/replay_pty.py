"""Replay a recorded pty session through pyte and print the final screen."""

import sys

import pyte

path = sys.argv[1]
width = int(sys.argv[2]) if len(sys.argv) > 2 else 150
height = int(sys.argv[3]) if len(sys.argv) > 3 else 46

screen = pyte.Screen(width, height)
stream = pyte.Stream(screen)
with open(path, "rb") as handle:
    stream.feed(handle.read().decode("utf-8", "replace"))

for line in screen.display:
    print(line.rstrip())
