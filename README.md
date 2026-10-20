# whatnow

*Executive function for the rest of us.*

whatnow is a day planner for ADHD brains. Rather than an hourly schedule, it holds your day loosely:
what you must do, what you should do, and what you could do if there's room. Dump everything on
your plate in whatever words come out. An LLM sorts it into tasks and events, notices how you feel
about them, and plans a day that fits your energy. When you're stuck, ask it *what now?*

It runs locally: one Go binary with a web UI and a terminal UI, and your data in SQLite on your
machine. You bring your own model API key.

![Today: the day strip, tasks in must, should, and could tiers, and the chat pane](docs/screenshots/today.png)

For a feel of a whole day, from the morning brain dump to the wrap-up, read [demo.md](demo.md).

## What it does

- **Good morning:** reconcile yesterday's leftovers, dump what's on your mind, say how much you've
  got in you today, and get a plan in three tiers.
- **What now?:** one pick for right now, with a reason. Pass on it and say why, and it picks again.
- **Chat:** "push the dentist to Thursday", "break this report into tiny steps". The assistant acts
  directly, and every change can be undone.
- **Pace:** five levels, from *running on fumes* to *go hard*. Change it any time and the plan
  follows.
- **Nudges:** before events, when they end, and as the day winds down, in the web UI.
- **Wrap up:** see what you got done, triage what's left, and leave a note for tomorrow.
- **Integrations:** pull today's meetings from Google Calendar (by each calendar's secret iCal
  address, no OAuth), and pick tasks from Jira tickets (by your own JQL search) and from the PRs
  a running [Review Queue](https://github.com/oreillymedia-ai-transformation/review-queue) ranks
  for your review. Pulls happen only when you ask, from the morning brain dump or from settings.

You can shape it in settings. *Assistant style* sets its voice ("playful and friendly, with the
odd koan"). *About me* tells the planner how you work ("my focus peaks before lunch", "meetings
drain me"). *Theme* swaps the look: the default sticker style, plain 90s, Neon Nights, Terminal,
Paper, or Vaporwave.

## A day, in pictures

All of these come from one made-up Thursday run against a live model, with the *About me* above
set to "focus peaks before lunch, cooked after 3pm, meetings drain me".

| | |
|---|---|
| ![The morning brain dump](docs/screenshots/morning-dump.png) **Good morning:** dump it all, messy is fine. | ![The morning plan in three tiers](docs/screenshots/morning-plan.png) **The plan:** the dreaded report goes first, while focus is high. |
| ![A what-now pick with its reason](docs/screenshots/what-now.png) ***What now?*** 11 minutes before standup, so not the report. | ![Chat breaking the report into tasks](docs/screenshots/chat.png) **Chat:** break the report into steps, then add them as tasks, with undo. |
| ![Wrap-up triage of the leftovers](docs/screenshots/wrap-triage.png) **Wrap up:** each leftover comes with a suggestion. | ![The goodnight reflection](docs/screenshots/goodnight.png) **Goodnight:** a reflection that reads your note for tomorrow. |

![The six themes side by side](docs/screenshots/themes.png)

## Install

You'll need an API key for Anthropic, OpenAI, or an OpenAI-compatible provider, or a signed-in Claude
Code or GitHub Copilot, to run it (see [Models](#models)).

On a Mac, with [Homebrew](https://brew.sh):

```sh
brew install jameynakama/tap/whatnow
```

### Without Homebrew

Download the latest release:

```sh
curl -fLo whatnow https://github.com/jameynakama/whatnow/releases/latest/download/whatnow-darwin-arm64
chmod +x whatnow && sudo mv whatnow /usr/local/bin/
```

On an Intel Mac, swap `arm64` for `amd64`. The binary isn't signed, so downloading it through a
browser gets it blocked by Gatekeeper. `curl` doesn't, and `xattr -d com.apple.quarantine whatnow`
undoes a browser download.

### From source

You need [Go](https://go.dev/dl/) 1.27+, Node 24, and [just](https://github.com/casey/just).

```sh
git clone https://github.com/jameynakama/whatnow
cd whatnow
just install   # builds the web UI and puts whatnow in your GOBIN
```

## Models

whatnow works with Anthropic (the default), any OpenAI-compatible API (OpenAI, OpenRouter, Ollama,
LM Studio), or a plan you already have:

- **Claude Code** runs on your Claude plan through the `claude` CLI you're signed in to. whatnow
  starts it with no tools of its own, none of your settings, and nothing saved, and hands it
  only whatnow's planner tools. This is for your own use on your own machine: Anthropic doesn't
  allow third-party apps to offer Claude plan sign-in to other people.
- **GitHub Copilot** runs on your Copilot plan through the Copilot CLI (`brew install
  copilot-cli`), signed in with `gh auth login`. Each message counts toward your Copilot usage.

Set it up on the first run, or later in settings. API keys are stored in your OS keychain, never in
the database. `ANTHROPIC_API_KEY` or `OPENAI_API_KEY` in the environment works too.

## Usage

```sh
whatnow          # the terminal UI, which also serves the web UI at http://127.0.0.1:7777
whatnow serve    # the web UI only
```

Installed with Homebrew, `brew services start whatnow` keeps the web UI running in the background
and starts it at login. Its log is in `$(brew --prefix)/var/log/whatnow.log`. `whatnow` then opens the
terminal UI against that server.

Both take `-addr` (default `127.0.0.1:7777`) and `-db` (default
`<user config dir>/whatnow/whatnow.db`, which on macOS is
`~/Library/Application Support/whatnow/whatnow.db`).

The server only answers this machine. To reach it from your phone over
[Tailscale](https://tailscale.com), keep it on loopback and run `tailscale serve --bg 7777`, then
add the machine's Tailscale name (such as `my-mac.tail1234.ts.net`) under *Remote access* in
settings. Anyone who can reach an allowed host gets full access, so don't put it behind a Funnel.

Open the web UI once to set up a model. After that, use either UI, or both at once, since they
stay in sync. In the terminal, press `?` for the keys.

### The terminal UI

The TUI is a companion meant to sit in a spare pane all day, not a full replacement. It does the
morning ritual (including calendar, Jira, and Review Queue pulls), today's list, *what now?*, pace, undo, quick
add, and chat. These are web-only:

- first-run setup and all settings, including integrations and themes
- the wrap-up ritual (the TUI shows the day's-done card once you've wrapped)
- nudges
- editing a task's details, and the all-tasks list

## Development

```sh
just dev     # API plus the Vite dev server, at http://localhost:5173
just test    # Go and web unit tests
just lint
just e2e     # Playwright, against a fake model
just demo    # a throwaway database on a clock you move with `just clock 13:45`
```

## License

MIT. See [LICENSE](LICENSE).
