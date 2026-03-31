# waggle shell-hook — surfaces messages on every agent command.
# Sourced from .zshenv/.bashrc. Each agent tool call = new shell = fresh source.
# Uses WAGGLE_AGENT_PPID (set by agent process, inherited by child shells) for
# session identity; falls back to $PPID (correct when agent spawns shells directly).
__waggle_check() {
    local _wd="$HOME/.waggle/runtime"
    local _apid="${WAGGLE_AGENT_PPID:-$PPID}"
    local _pm="$_wd/agent-ppid-$_apid"
    [ -f "$_pm" ] || return 0
    local _nonce
    read -r _nonce < "$_pm" 2>/dev/null || return 0
    [ -n "$_nonce" ] || return 0
    local _sm="$_wd/agent-session-$_nonce"
    [ -f "$_sm" ] || return 0
    local _wa _wp
    { read -r _wa; read -r _wp; } < "$_sm" 2>/dev/null || return 0
    [ -n "$_wa" ] || return 0
    local _ws="$_wd/signals/$_wp/$_wa"
    # Recover orphaned .c-* files from crashed consumers
    for _orphan in "$_ws".c-*; do
        [ -f "$_orphan" ] || continue
        cat "$_orphan" >&2 2>/dev/null
        rm -f "$_orphan" 2>/dev/null
    done
    if [ -f "$_ws" ]; then
        # Atomic: rename then read. If daemon writes after mv, new file at original path.
        local _wt="$_ws.c-$$"
        if mv "$_ws" "$_wt" 2>/dev/null; then
            cat "$_wt" >&2 2>/dev/null
            rm -f "$_wt" 2>/dev/null
        fi
    fi
    touch "$_sm" "$_pm" 2>/dev/null
}
__waggle_check
