#!/usr/bin/env node
// waggle-push.js — PreToolUse hook for Claude Code.
// Reads signal files via PPID pointer + session mapping. Atomic consume via rename.
// WAGGLE_PPID env var provides the real agent PID (set by hook command).
const fs = require('fs');
const path = require('path');

const home = process.env.HOME;
if (!home) process.exit(0);

const rtDir = path.join(home, '.waggle', 'runtime');
// Use WAGGLE_PPID (agent PID) not process.ppid (intermediate shell PID)
const ppid = process.env.WAGGLE_PPID || String(process.ppid);
const pointerFile = path.join(rtDir, 'agent-ppid-' + ppid);

try {
    if (!fs.existsSync(pointerFile)) process.exit(0);

    const nonce = fs.readFileSync(pointerFile, 'utf8').trim();
    if (!nonce) process.exit(0);

    const sessionFile = path.join(rtDir, 'agent-session-' + nonce);
    if (!fs.existsSync(sessionFile)) process.exit(0);

    const lines = fs.readFileSync(sessionFile, 'utf8').trim().split('\n');
    const agent = lines[0];
    const project = lines[1] || '';
    if (!agent) process.exit(0);

    const now = new Date();
    try { fs.utimesSync(sessionFile, now, now); } catch {}

    const sigFile = path.join(rtDir, 'signals', project, agent);
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
