# Naming shared by lib/tick.jq and the berthier commands (jq -L lib 'include "names"; ...').

def task_prefix: "󱎦 ";
def is_task_label: (. // "") | startswith(task_prefix);
# A Division is a workspace a Corps adopted: labelled "<corps>/<division>".
def is_division_label: (. // "") as $l | ($l | is_task_label | not) and ($l | contains("/"));
def parent_corps: split("/")[0];

# The name an Agent is addressed by in `berthier ask`: its Corps ("dotfiles"),
# or for a Task "skipli/dark-mode".
def corps_name: (. // "") | ltrimstr(task_prefix) | ltrimstr(".") | ascii_downcase;

# How a workspace label reads aloud: "󱎦 skipli/dark-mode" -> "skipli task dark mode",
# "learning/tailscale" -> "tailscale in learning".
def spoken: (. // "an agent")
  | if is_task_label then ltrimstr(task_prefix) | sub("/"; " task ")
    elif is_division_label then split("/") | "\(.[1]) in \(.[0])"
    else . end
  | ltrimstr(".") | gsub("[-_]"; " ");
