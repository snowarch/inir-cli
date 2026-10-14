#compdef inir
# zsh completion for inir, answered by the inir binary on every Tab.
# Install: inir completions install zsh

_inir() {
    local line kind ns value desc
    local -a lines command target alias function value_ flag nospace
    lines=("${(@f)$(command inir __complete zsh -- "${(@)words[2,CURRENT]}" 2>/dev/null)}")
    case "${lines[1]}" in
        :dirs) _files -/; return ;;
        :hint\ *) _message "${lines[1]#:hint }"; return ;;
    esac
    for line in "${lines[@]}"; do
        [[ -n "$line" ]] || continue
        IFS=$'\t' read -r kind ns value desc <<<"$line"
        value="${value//:/\\:}${desc:+:$desc}"
        if [[ "$ns" == 1 ]]; then
            nospace+=("$value")
            continue
        fi
        case "$kind" in
            command) command+=("$value") ;;
            target) target+=("$value") ;;
            alias) alias+=("$value") ;;
            function) function+=("$value") ;;
            value) value_+=("$value") ;;
            flag) flag+=("$value") ;;
        esac
    done
    local ret=1
    (( $#command )) && _describe -t commands 'command' command && ret=0
    (( $#target )) && _describe -t ipc-targets 'IPC target' target && ret=0
    (( $#alias )) && _describe -t ipc-aliases 'IPC alias' alias && ret=0
    (( $#function )) && _describe -t functions 'function' function && ret=0
    (( $#value_ )) && _describe -t values 'value' value_ && ret=0
    (( $#nospace )) && _describe -t values 'value' nospace -S '' && ret=0
    (( $#flag )) && _describe -t options 'option' flag && ret=0
    return ret
}

if [[ "${zsh_eval_context[-1]}" == loadautofunc ]]; then
    _inir "$@"
else
    compdef _inir inir
fi
