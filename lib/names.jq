# Naming shared by lib/tick.jq and the berthier commands (jq -L lib 'include "names"; ...').

def task_prefix: "󱎦 ";
def is_task_label: (. // "") | startswith(task_prefix);

# The name an Agent is addressed by in `berthier ask`: its Corps ("dotfiles"),
# or for a Task "skipli/dark-mode".
def corps_name: (. // "") | ltrimstr(task_prefix) | ltrimstr(".") | ascii_downcase;

# How a workspace label reads aloud: "󱎦 skipli/dark-mode" -> "skipli task dark mode".
def spoken: (. // "an agent")
  | if is_task_label then ltrimstr(task_prefix) | sub("/"; " task ") else . end
  | ltrimstr(".") | gsub("[-_]"; " ");
