#!/usr/bin/env node
// waggle-push.js — PreToolUse hook
// Reads pushed messages from waggle listener and injects via additionalContext

const fs = require('fs');

const agentName = process.env.WAGGLE_AGENT_NAME;
if (!agentName) process.exit(0);

const listenFile = process.env.WAGGLE_LISTEN_FILE || `/tmp/waggle-${agentName}.jsonl`;

try {
    if (!fs.existsSync(listenFile)) process.exit(0);

    const content = fs.readFileSync(listenFile, 'utf8').trim();
    if (!content) process.exit(0);

    // Clear the file immediately (atomic: write empty, not unlink)
    fs.writeFileSync(listenFile, '');

    // Parse messages
    const messages = content.split('\n')
        .filter(line => line.trim())
        .map(line => {
            try { return JSON.parse(line); }
            catch { return null; }
        })
        .filter(Boolean);

    if (messages.length === 0) process.exit(0);

    // Format for injection
    const formatted = messages.map(m =>
        `[waggle] Message from ${m.from}: ${m.body}`
    ).join('\n');

    // Output additionalContext
    console.log(JSON.stringify({
        additionalContext: `\n📨 Waggle: ${messages.length} new message(s):\n${formatted}\n`
    }));
} catch (e) {
    // Silent failure — don't block tool calls
    process.exit(0);
}

