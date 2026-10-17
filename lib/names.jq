# Naming shared by lib/tick.jq and the berthier commands (jq -L lib 'include "names"; ...').

# An adopted Division is labelled "󱎦 <name>"; a spawned (worktree) one just
# "<name>", since Herdr already draws it under its Corps. Either way its Corps
# is recorded in state/divisions.json, which is what makes it a Division.
def division_prefix: "󱎦 ";

# The name an Agent is addressed by in `berthier ask`: its Corps ("dotfiles")
# or its Division ("dark-mode").
def corps_name: (. // "") | ltrimstr(division_prefix) | ltrimstr(".") | ascii_downcase;
def is_division($divisions): $divisions[corps_name] != null;

# How a workspace label reads aloud: "󱎦 dark-mode" -> "dark mode".
def spoken: (. // "an agent") | ltrimstr(division_prefix)
  | ltrimstr(".") | gsub("[-_]"; " ");
