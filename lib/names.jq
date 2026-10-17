# Naming shared by lib/tick.jq and the berthier commands (jq -L lib 'include "names"; ...').

def task_prefix: "󱎦 ";
# Tasks and Divisions both carry the prefix: a Task is "󱎦 <corps>/<task>", a
# Division (a workspace a Corps adopted) is "󱎦 <division>", its Corps recorded
# by `berthier adopt` in state/divisions.json.
def is_task_label: (. // "") | startswith(task_prefix) and contains("/");
def is_division_label: (. // "") | startswith(task_prefix) and (contains("/") | not);

# The name an Agent is addressed by in `berthier ask`: its Corps ("dotfiles"),
# or for a Task "skipli/dark-mode".
def corps_name: (. // "") | ltrimstr(task_prefix) | ltrimstr(".") | ascii_downcase;

# How a workspace label reads aloud: "󱎦 skipli/dark-mode" -> "skipli task dark mode",
# "󱎦 tailscale" -> "tailscale".
def spoken: (. // "an agent")
  | if is_task_label then ltrimstr(task_prefix) | sub("/"; " task ")
    else ltrimstr(task_prefix) end
  | ltrimstr(".") | gsub("[-_]"; " ");
