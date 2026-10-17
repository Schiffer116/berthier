# Berthier

Berthier is the user's chief of staff for coding agents. It runs the Fleet of Agents in Herdr: it starts them on the user's command, keeps them alive, checks on them, and tells the user where things stand. The user talks to Agents directly; Berthier is never in that loop.

## Language

### Who

**Berthier**:
The chief-of-staff agent: whichever coding agent runs in Berthier's own repository, distinguished only by its goal of keeping the Fleet moving and learning how the user works.
_Avoid_: orchestrator, controller, first mate

**Notes**:
Berthier's running record of open threads, commitments, and recent decisions, kept so any new Berthier session continues exactly where the last one stopped. How the user works is kept separately, in Berthier's own instructions.
_Avoid_: memory, journal, log

**Main Loop**:
The deterministic, always-running process that observes the Fleet, Revives gone Agents, and Escalates everything else to Berthier.
_Avoid_: daemon, watcher, supervisor

**Project**:
One Herdr workspace, whatever it contains; it need not be a git repository.
_Avoid_: herdr session, campaign, repo, folder

**Agent**:
One coding-agent session in a Herdr pane inside a Project, whether the user or Berthier started it.
_Avoid_: worker, crewmate, bot

**Main Agent**:
The Agent working a Project on its main branch, in the Project's own workspace; it decides when to split off Tasks.
_Avoid_: lead, primary, project agent

**Fleet**:
Every Agent currently known to Berthier.
_Avoid_: crew, swarm

**Fleet State**:
The Main Loop's latest record of every Project and Agent; only the Main Loop writes it.
_Avoid_: status file, snapshot

### Work

**Task**:
A piece of work a Main Agent splits off to run side by side with the main branch, in its own worktree workspace labelled with the 󱎦 prefix and worked by one Agent at a time; a Revived Agent continues the same Task. The user reviews a finished Task personally.
_Avoid_: job, ticket, assignment, split

**Brief**:
The initial instructions handed to a new Agent (by Berthier to a Main Agent, or by a Main Agent to a Task's Agent): the goal and what done looks like.
_Avoid_: orders, prompt, task description

### Supervision

**Revive**:
Restarting a crashed Agent by resuming its previous session; the only fix the Main Loop makes on its own. An Agent the user exited on purpose is never Revived.
_Avoid_: restart, respawn

**Escalation**:
The Main Loop handing Berthier an Agent problem it does not fix itself, such as a blocked, hung, or stalled Agent, or a failed Revive.
_Avoid_: alert, notification

**Check-in**:
Berthier reading an Agent's recent terminal output to judge whether it is progressing, stuck, or done.
_Avoid_: status report, ping

**Nudge**:
A prompt Berthier sends to a live Agent to get it moving again.
_Avoid_: ping, poke

### Coordination

**Owner**:
The Project responsible for an area (the bar, systemd units, an app); listed in `owners.md`. Only the Owner's Agents change it.
_Avoid_: maintainer

**Ask**:
A request one Agent sends another Project's Agent through Berthier's inbox, delivered when the addressee is ready for input; answered with an Ask back.
_Avoid_: message, ticket, relay

**Report**:
An Agent's own one-line status for the user, written at the end of every turn; "needs you" marks the ones waiting on the user.
_Avoid_: summary, recap
