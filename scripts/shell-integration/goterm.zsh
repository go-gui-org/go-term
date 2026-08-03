# go-term shell integration for zsh.
#
# Emits OSC 133 semantic marks (A/B/C/D) and OSC 7 working-directory
# reports, which is what turns on prompt jumping, jump-to-last-failure,
# command-output selection, and open-new-pane-in-the-same-directory.
#
# Install by sourcing it from ~/.zshrc:
#
#     source /path/to/go-term/scripts/shell-integration/goterm.zsh
#
# The sequences are emitted unconditionally rather than only under go-term.
# OSC 133 and OSC 7 are understood by iTerm2, kitty, and wezterm, and are
# ignored by terminals that do not implement them, so one rc file can be
# shared across terminals.

# Sourcing twice must not append a second marker to PROMPT, so everything
# below runs once. add-zsh-hook is idempotent on its own, but PROMPT is not.
if [[ -n ${__goterm_integration_loaded:-} ]]; then
	return 0
fi
typeset -g __goterm_integration_loaded=1

# Only interactive shells have a prompt to mark up.
[[ -o interactive ]] || return 0

# __goterm_urlencode percent-encodes $1 for the path portion of a file:// URI.
# LC_ALL=C makes the loop iterate over *bytes*, which is what percent-encoding
# is defined over — a UTF-8 path must become one %XX per byte, not per rune.
__goterm_urlencode() {
	emulate -L zsh
	local LC_ALL=C
	local str=$1 out= i c
	for ((i = 1; i <= ${#str}; i++)); do
		c=$str[i]
		case $c in
		[-_.~a-zA-Z0-9/]) out+=$c ;;
		*) out+=$(printf '%%%02X' "'$c") ;;
		esac
	done
	print -rn -- $out
}

# __goterm_precmd closes the previous command (D with its exit status) and
# opens the next prompt (A). precmd hooks run before the prompt is drawn, and
# $? here is still the status of the command that just finished.
__goterm_precmd() {
	local ret=$?
	if [[ -n ${__goterm_running:-} ]]; then
		printf '\033]133;D;%s\007' "$ret"
		unset __goterm_running
	fi
	printf '\033]7;file://%s%s\033\\' "${HOST:-}" "$(__goterm_urlencode "$PWD")"
	printf '\033]133;A\007'
}

# __goterm_preexec emits C — the command was accepted, output starts here.
__goterm_preexec() {
	typeset -g __goterm_running=1
	printf '\033]133;C\007'
}

# add-zsh-hook appends to the hook arrays rather than replacing them, and
# refuses to register the same function twice, so an existing precmd/preexec
# chain (oh-my-zsh, starship, a user's own) keeps working untouched.
autoload -Uz add-zsh-hook
add-zsh-hook precmd __goterm_precmd
add-zsh-hook preexec __goterm_preexec

# B marks where the command line begins, so it belongs at the very end of the
# prompt. %{...%} tells zsh the bytes occupy no columns — without it the shell
# miscounts the prompt width and redraws long lines wrongly.
if [[ $PROMPT != *'133;B'* ]]; then
	PROMPT="$PROMPT"$'%{\033]133;B\007%}'
fi
