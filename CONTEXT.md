# Berthier

Berthier is the user's chief of staff for coding agents. It runs the Grand Army of Agents in Herdr: it starts them on the user's command, keeps them alive, checks on them, and tells the user where things stand. The user talks to Agents directly; Berthier is never in that loop.

## Language

### Who

**Berthier**:
The chief-of-staff agent: whichever coding agent runs in Berthier's own repository, distinguished only by its goal of keeping the Grand Army moving and learning how the user works.
_Avoid_: orchestrator, controller, first mate

**Notes**:
Berthier's running record of open threads, commitments, and recent decisions, kept so any new Berthier session continues exactly where the last one stopped. How the user works is kept separately, in Berthier's own instructions.
_Avoid_: memory, journal, log

**Main Loop**:
The deterministic, always-running process that observes the Grand Army, Revives gone Agents, and Escalates everything else to Berthier.
_Avoid_: daemon, watcher, supervisor

**Corps**:
One Herdr workspace, whatever it contains; it need not be a git repository. Same word singular and plural, as in Napoleon's Grande Armée.
_Avoid_: project, herdr session, campaign, repo, folder

**Division**:
A sub-unit of a Corps, with its own Agent; Berthier records which Corps it belongs to. Herdr nests a spawned one under its Corps. It is either a worktree the Main Agent spawns for a side feature, which ends once the user has reviewed it, or an existing workspace the Corps adopted, which lasts. A spawned one runs on a model its Corps picks (`division-new --model`, default `BERTHIER_DIVISION_MODEL`), kept across Revives. The Corps's Main Agent commands it: assigns and reviews its work, and receives its Escalations.
_Avoid_: task, sub-project, child, team

**Agent**:
One coding-agent session in a Herdr pane inside a Corps, whether the user or Berthier started it.
_Avoid_: worker, crewmate, bot

**Main Agent**:
The Agent working a Corps on its main branch, in the Corps's own workspace; it decides when to spawn Divisions.
_Avoid_: lead, primary

**Grand Army**:
Every Agent currently known to Berthier, after Napoleon's Grande Armée, whose chief of staff was Berthier.
_Avoid_: Army, fleet, crew, swarm

**Grand Army State**:
The Main Loop's latest record of every Corps and Agent; only the Main Loop writes it.
_Avoid_: status file, snapshot

### Work

**Brief**:
The initial instructions handed to a new Agent (by Berthier to a Main Agent, or by a Main Agent to a Division's Agent): the goal and what done looks like.
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
The Corps responsible for an area (the bar, systemd units, an app); listed in `owners.md`. Only the Owner's Agents change it.
_Avoid_: maintainer

**Ask**:
A request one Agent sends another Corps's Agent through Berthier's inbox, delivered when the addressee is ready for input; answered with an Ask back.
_Avoid_: message, ticket, relay

**Report**:
An Agent's own one-line status for the user, written at the end of every turn; "needs you" marks the ones waiting on the user.
_Avoid_: summary, recap
