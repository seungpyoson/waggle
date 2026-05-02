## Waggle Runtime

At the start of every session, run:

  WAGGLE_AGENT_PPID=$PPID waggle adapter bootstrap codex --format markdown

Do not skip this step. If the command fails or waggle is not installed, continue normally. Use the agent name from the output for any subsequent waggle commands in this session. If you need to rediscover your identity later, run `waggle whoami`; it reads the same runtime mapping used for pushed message delivery.
