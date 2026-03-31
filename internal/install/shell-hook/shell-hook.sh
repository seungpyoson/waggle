# waggle shell-hook — surfaces messages on every agent command.
# Sourced from .zshenv/.bashrc. Each agent tool call = new shell = fresh source.
# Cost when no messages: 1 file stat (~ns). Guarded by PPID mapping.
__waggle_check() {
    local _wd="$HOME/.waggle/runtime"
    local _wm="$_wd/agent-ppid-$PPID"
    [ -f "$_wm" ] || return 0
    local _wa _wp
    { read -r _wa; read -r _wp; } < "$_wm" 2>/dev/null || return 0
    [ -n "$_wa" ] || return 0
    local _ws="$_wd/signals/$_wp/$_wa"
    [ -f "$_ws" ] || return 0
    # Atomic: rename then read. If daemon writes after mv, new file at original path.
    local _wt="$_ws.c-$$"
    mv "$_ws" "$_wt" 2>/dev/null || return 0
    cat "$_wt" >&2 2>/dev/null
    rm -f "$_wt" 2>/dev/null
    touch "$_wm" 2>/dev/null
}
__waggle_check
