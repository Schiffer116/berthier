# Naming shared by lib/tick.jq and the berthier commands (jq -L lib 'include "names"; ...').

def division_prefix: "󱎦 ";
# A Division is labelled "󱎦 <name>"; its Corps is recorded in state/divisions.json
# by `berthier division-new` or `berthier adopt`.
def is_division_label: (. // "") | startswith(division_prefix);

# The name an Agent is addressed by in `berthier ask`: its Corps ("dotfiles")
# or its Division ("dark-mode").
def corps_name: (. // "") | ltrimstr(division_prefix) | ltrimstr(".") | ascii_downcase;

# How a workspace label reads aloud: "󱎦 dark-mode" -> "dark mode".
def spoken: (. // "an agent") | ltrimstr(division_prefix)
  | ltrimstr(".") | gsub("[-_]"; " ");
