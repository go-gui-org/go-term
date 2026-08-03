# go-term shell integration for fish.
#
# Emits OSC 133 semantic marks (A/B/C/D) and OSC 7 working-directory
# reports, which is what turns on prompt jumping, jump-to-last-failure,
# command-output selection, and open-new-pane-in-the-same-directory.
#
# Install by sourcing it from ~/.config/fish/config.fish:
#
#     source /path/to/go-term/scripts/shell-integration/goterm.fish
#
# fish 4.0 and newer emit the whole set natively — A and B around the prompt,
# C with the command line, D with the exit status, and OSC 7 on every
# directory change. This script installs nothing there; a second copy of every
# mark would give the terminal two overlapping command spans to reconcile. It
# exists for fish 3.x, which has none of them.
#
# The sequences are emitted unconditionally rather than only under go-term.
# OSC 133 and OSC 7 are understood by iTerm2, kitty, and wezterm, and are
# ignored by terminals that do not implement them, so one rc file can be
# shared across terminals.

# Sourcing twice must not wrap the prompt function around itself, so
# everything below runs once.
if set -q __goterm_integration_loaded
    return 0
end
set -g __goterm_integration_loaded 1

# Only interactive shells have a prompt to mark up.
if not status is-interactive
    return 0
end

# fish 4.0+ does all of this itself.
if test (string split . -- $FISH_VERSION)[1] -ge 4
    return 0
end

# __goterm_urlencode percent-encodes $argv[1] for the path portion of a
# file:// URI. fish's `string escape --style=url` encodes exactly the
# unreserved set, so the path separators are restored afterwards — a URI path
# with its slashes escaped is not a path.
function __goterm_urlencode
    string escape --style=url -- $argv[1] | string replace --all '%2F' /
end

# __goterm_prompt opens a prompt: report the directory, then mark A. The
# fish_prompt event fires immediately before the prompt function runs.
function __goterm_prompt --on-event fish_prompt
    printf '\033]7;file://%s%s\033\\' (hostname) (__goterm_urlencode $PWD)
    printf '\033]133;A\007'
    __goterm_wrap_prompt
end

# __goterm_preexec emits C — the command was accepted, output starts here.
function __goterm_preexec --on-event fish_preexec
    printf '\033]133;C\007'
end

# __goterm_postexec closes the command with its exit status. $status here is
# still the finished command's, so it must be read before anything else runs.
function __goterm_postexec --on-event fish_postexec
    printf '\033]133;D;%s\007' $status
end

# __goterm_wrap_prompt puts B at the very end of the prompt, marking where the
# command line begins. fish has no "prompt finished" event, so the prompt
# function itself is copied aside and replaced with one that calls the
# original and then emits the mark.
#
# The wrap is deferred to the first prompt rather than done at source time
# because config.fish commonly defines fish_prompt *after* sourcing this file;
# waiting means the copy is of the user's real prompt, not the default one it
# would have replaced.
function __goterm_wrap_prompt
    if set -q __goterm_prompt_wrapped
        return 0
    end
    set -g __goterm_prompt_wrapped 1
    if not functions -q fish_prompt
        return 0
    end
    functions --copy fish_prompt __goterm_orig_fish_prompt
    function fish_prompt
        __goterm_orig_fish_prompt
        printf '\033]133;B\007'
    end
end
