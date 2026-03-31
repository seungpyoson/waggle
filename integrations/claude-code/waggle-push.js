#!/usr/bin/env node
// waggle-push.js — PreToolUse hook for Claude Code.
// Reads signal files via PPID mapping. Atomic consume via rename.
// WAGGLE_PPID env var provides the real agent PID (set by hook command).
const fs = require('fs');
const path = require('path');

const home = process.env.HOME;
if (!home) process.exit(0);

const rtDir = path.join(home, '.waggle', 'runtime');
// Use WAGGLE_PPID (agent PID) not process.ppid (intermediate shell PID)
const ppid = process.env.WAGGLE_PPID || String(process.ppid);
const mapFile = path.join(rtDir, 'agent-ppid-' + ppid);

try {
    if (!fs.existsSync(mapFile)) process.exit(0);

    const agent = fs.readFileSync(mapFile, 'utf8').trim().split('\n')[0];
    if (!agent) process.exit(0);

    const sigFile = path.join(rtDir, 'signals', agent);
    if (!fs.existsSync(sigFile)) process.exit(0);

    // Atomic: rename then read (daemon writes to original path are safe)
    const tmpFile = sigFile + '.c-' + process.pid;
    try { fs.renameSync(sigFile, tmpFile); } catch { process.exit(0); }

    const content = fs.readFileSync(tmpFile, 'utf8').trim();
    try { fs.unlinkSync(tmpFile); } catch {}

    if (!content) process.exit(0);

    console.log(JSON.stringify({
        additionalContext: '\n' + content +
            '\nRespond to waggle messages using: ' +
            'WAGGLE_AGENT_NAME="' + agent + '" waggle send <sender> "<reply>"\n'
    }));
} catch {
    process.exit(0);
}
