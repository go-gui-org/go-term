# go-term shell integration for bash.
#
# Emits OSC 133 semantic marks (A/B/C/D) and OSC 7 working-directory
# reports, which is what turns on prompt jumping, jump-to-last-failure,
# command-output selection, and open-new-pane-in-the-same-directory.
#
# Install by sourcing it from ~/.bashrc:
#
#     source /path/to/go-term/scripts/shell-integration/goterm.bash
#
# The sequences are emitted unconditionally rather than only under go-term.
# OSC 133 and OSC 7 are understood by iTerm2, kitty, and wezterm, and are
# ignored by terminals that do not implement them, so one rc file can be
# shared across terminals.

# Sourcing twice must not append a second marker to PS1 or chain the hooks
# onto themselves, so everything below runs once.
if [ -n "${__goterm_integration_loaded:-}" ]; then
	return 0
fi
__goterm_integration_loaded=1

# Only interactive shells have a prompt to mark up.
case $- in
*i*) ;;
*) return 0 ;;
esac

# __goterm_urlencode percent-encodes $1 for the path portion of a file:// URI.
# LC_ALL=C makes the loop iterate over *bytes*, which is what percent-encoding
# is defined over — a UTF-8 path must become one %XX per byte, not per rune.
__goterm_urlencode() {
	local LC_ALL=C
	local str=$1 out= i c n
	for ((i = 0; i < ${#str}; i++)); do
		c=${str:i:1}
		case $c in
		[-_.~a-zA-Z0-9/]) out+=$c ;;
		*)
			# "'$c" makes printf yield the byte's numeric value; the mask
			# keeps a high byte from widening to a sign-extended value.
			printf -v n '%d' "'$c"
			printf -v c '%%%02X' "$((n & 0xFF))"
			out+=$c
			;;
		esac
	done
	printf '%s' "$out"
}

# __goterm_osc7 reports the working directory as file://host/encoded/path.
__goterm_osc7() {
	printf '\033]7;file://%s%s\033\\' "${HOSTNAME:-}" "$(__goterm_urlencode "$PWD")"
}

# __goterm_precmd closes the previous command (D with its exit status) and
# opens the next prompt (A). It must be the *first* entry in PROMPT_COMMAND:
# $? is the status of whatever ran last, and a hook running ahead of it would
# overwrite that with its own.
__goterm_precmd() {
	local ret=$?
	if [ -n "${__goterm_running:-}" ]; then
		printf '\033]133;D;%s\007' "$ret"
		unset __goterm_running
	fi
	__goterm_osc7
	printf '\033]133;A\007'
}

# __goterm_arm arms the DEBUG trap and must run *last* in PROMPT_COMMAND.
# Arming in __goterm_precmd instead would fire C on whichever hook ran next,
# marking a foreign PROMPT_COMMAND entry as the user's command — the chain
# has to be fully drained before the trap starts believing what it sees.
__goterm_arm() {
	__goterm_preexec_armed=1
}

# __goterm_preexec emits C — the command was accepted, output starts here.
# bash has no preexec hook, so this rides the DEBUG trap, which fires before
# *every* simple command. The armed flag reduces that to one firing per
# prompt, and the pattern guard keeps our own functions from consuming it.
__goterm_preexec() {
	[ -n "${__goterm_preexec_armed:-}" ] || return 0
	case "$BASH_COMMAND" in
	__goterm_*) return 0 ;;
	esac
	unset __goterm_preexec_armed
	__goterm_running=1
	printf '\033]133;C\007'
}

# __goterm_bp_preexec is the bash-preexec entry point. bash-preexec has
# already decided this is a real user command, so none of the DEBUG-trap
# filtering above applies — it fires exactly once per command.
__goterm_bp_preexec() {
	__goterm_running=1
	printf '\033]133;C\007'
}

# Installation takes one of two routes.
#
# bash-preexec (https://github.com/rcaloras/bash-preexec) owns the DEBUG trap
# and publishes preexec_functions/precmd_functions for everyone else to append
# to. Starship, atuin, and most framework prompts load it. When it is present,
# registering there is strictly correct: no trap is touched, ordering is
# managed, and bash-preexec restores $? before each precmd function so the
# exit status stays readable no matter what else is registered.
#
# Otherwise we install the DEBUG trap ourselves. Note that a trap already set
# by the user CANNOT be preserved: `source` enters a function context, and
# bash does not expose the outer DEBUG trap to it — `trap -p DEBUG` inside a
# sourced file reports nothing even with functrace set, so there is nothing to
# chain onto. If you set your own DEBUG trap, either load bash-preexec first
# or install your trap so that it calls __goterm_preexec itself.
#
# Detection is on $__bp_imported, the marker bash-preexec sets when sourced.
# Testing the hook arrays instead does not work: bash-preexec deliberately
# leaves them undeclared (callers create them by appending), so both
# `declare -p preexec_functions` and `${preexec_functions+x}` come back unset
# while it is perfectly well installed — which routed us into the trap branch,
# where our DEBUG trap replaced its own and C fired against the wrong command.
if [ -n "${__bp_imported:-}" ] || [ "$(type -t __bp_install 2>/dev/null)" = function ]; then
	case " ${precmd_functions[*]} " in
	*" __goterm_precmd "*) ;;
	*) precmd_functions+=(__goterm_precmd) ;;
	esac
	case " ${preexec_functions[*]} " in
	*" __goterm_bp_preexec "*) ;;
	*) preexec_functions+=(__goterm_bp_preexec) ;;
	esac
else
	trap '__goterm_preexec' DEBUG

	# __goterm_precmd goes first so it reads the real $?, __goterm_arm goes
	# last so the DEBUG trap only arms once every other hook has run. bash
	# 5.1 made PROMPT_COMMAND assignable as an array; when it already is
	# one, appending a string would clobber it, so the two representations
	# are handled separately.
	if [[ "$(declare -p PROMPT_COMMAND 2>/dev/null)" == "declare -a"* ]]; then
		PROMPT_COMMAND=(__goterm_precmd "${PROMPT_COMMAND[@]}" __goterm_arm)
	else
		case "${PROMPT_COMMAND:-}" in
		*__goterm_precmd*) ;;
		"") PROMPT_COMMAND="__goterm_precmd;__goterm_arm" ;;
		*) PROMPT_COMMAND="__goterm_precmd;$PROMPT_COMMAND;__goterm_arm" ;;
		esac
	fi
fi

# B marks where the command line begins, so it belongs at the very end of the
# prompt. \[...\] tells readline the bytes occupy no columns — without it the
# shell miscounts the prompt width and redraws long lines wrongly.
case "$PS1" in
*'\[\033]133;B\007\]'*) ;;
*) PS1="$PS1"'\[\033]133;B\007\]' ;;
esac
