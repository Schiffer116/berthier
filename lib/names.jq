# Naming shared by lib/tick.jq and the berthier commands (jq -L lib 'include "names"; ...').

# A Division is labelled plainly "<name>"; its Corps recorded in
# state/divisions.json is what makes it a Division.

# The name an Agent is addressed by in `berthier ask`: its Corps ("dotfiles")
# or its Division ("dark-mode").
def corps_name: (. // "") | ltrimstr(".") | ascii_downcase;
def is_division($divisions): $divisions[corps_name] != null;

# How a workspace label reads aloud: "dark-mode" -> "dark mode".
def spoken: (. // "an agent") | ltrimstr(".") | gsub("[-_]"; " ");
