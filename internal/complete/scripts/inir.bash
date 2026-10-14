# bash completion for inir, answered by the inir binary on every Tab.
# Install: inir completions install bash

_inir() {
    local line="${COMP_LINE:0:COMP_POINT}" cur lead c
    local -a words out
    read -r -a words <<<"$line"
    [[ -z "$line" || "$line" == *[[:space:]] ]] && words+=("")
    cur="${words[${#words[@]}-1]}"
    mapfile -t out < <(command inir __complete bash -- "${words[@]:1}" 2>/dev/null)
    COMPREPLY=()
    case "${out[0]:-}" in
        :dirs)
            compopt -o filenames 2>/dev/null
            mapfile -t COMPREPLY < <(compgen -d -- "$cur")
            return ;;
        :hint*) return ;;
    esac
    # bash splits words at ':' and '=' too (COMP_WORDBREAKS) and replaces only
    # what follows the last one: hand it candidates without what comes before.
    lead=""
    [[ "$COMP_WORDBREAKS" == *:* && "$cur" == *:* ]] && lead="${cur%"${cur##*:}"}"
    [[ "$COMP_WORDBREAKS" == *=* && "${cur#"$lead"}" == *=* ]] && lead="${cur%"${cur##*=}"}"
    for c in "${out[@]}"; do
        COMPREPLY+=("${c#"$lead"}")
    done
    if ((${#out[@]} == 1)) && [[ "${out[0]}" == *[:/] ]]; then
        compopt -o nospace 2>/dev/null
    fi
}
complete -F _inir inir
