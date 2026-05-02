## Waggle Runtime

At the start of every session, run:

    WAGGLE_AGENT_PPID=$PPID waggle adapter bootstrap gemini --format markdown

Do not skip this step. If waggle is not installed, the command will fail silently — continue normally. Use the agent name from the output for any subsequent waggle commands in this session. If you need to rediscover your identity later, run `waggle whoami`.
