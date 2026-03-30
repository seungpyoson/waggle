## Waggle Runtime

At the start of every session, run:

    waggle adapter bootstrap augment --format markdown

What to do with the result:

1. If the command succeeds, note the `Agent:` value from the output and use it for any subsequent waggle commands in this session.
2. If the command fails or waggle is not installed, continue normally — this is not an error.

Do not start background listeners or invent a separate transport path. The bootstrap command is the single entry point for waggle coordination.
