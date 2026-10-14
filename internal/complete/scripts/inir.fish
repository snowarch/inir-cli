# fish completion for inir, answered by the inir binary on every Tab.
# Install: inir completions install fish

function __inir_complete
    # -x is fish 4; fish 3 only knows -o.
    set -l words (commandline -xpc 2>/dev/null); or set words (commandline -opc)
    set -e words[1]
    set -l cur (commandline -ct)
    set -l out (command inir __complete fish -- $words "$cur" 2>/dev/null)
    switch "$out[1]"
        case :dirs
            __fish_complete_directories "$cur"
        case ':hint*'
        case '*'
            printf '%s\n' $out
    end
end

complete -c inir -f -a '(__inir_complete)'
