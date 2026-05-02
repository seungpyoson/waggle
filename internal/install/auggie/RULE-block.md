## Waggle Runtime

At the start of every session, run:

  WAGGLE_AGENT_PPID=$PPID waggle adapter bootstrap auggie --format markdown

If waggle is not installed or the command fails, continue normally. Use the agent name from the output for any subsequent waggle commands in this session. If you need to rediscover your identity later, run `waggle whoami`.
