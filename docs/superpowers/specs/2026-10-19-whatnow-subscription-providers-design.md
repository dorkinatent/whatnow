# whatnow providers: Claude Code and GitHub Copilot

Today whatnow needs an API key: Anthropic, OpenAI, or an OpenAI-compatible server. This adds two
providers that run on a plan the user already pays for, through that vendor's own CLI:

- **Claude Code** (`claudecode`): the `claude` CLI the user is signed in to, driven headless.
- **GitHub Copilot** (`copilot`): the Copilot runtime, driven by the Copilot Go SDK.

Neither needs a key in whatnow. Each uses the user's own sign-in on their own machine.

## What the research found (2026-10-19)

**Claude Code / Agent SDK**
- The Agent SDK is TypeScript (`@anthropic-ai/claude-agent-sdk` 0.3.303) and Python only. For
  other languages the docs say to run the CLI as a subprocess with `-p`. whatnow does that; there
  is no official Go SDK, and the community one isn't worth a dependency for one subprocess.
- Headless flags (CLI 2.1.303, checked against `claude --help`): `-p`, `--output-format
  json|stream-json`, `--verbose`, `--include-partial-messages`, `--system-prompt`, `--model`,
  `--max-turns`, `--json-schema`, `--tools ""` (no built-in tools), `--mcp-config`,
  `--strict-mcp-config`, `--allowedTools`, `--permission-mode dontAsk`, `--setting-sources`,
  `--no-session-persistence`.
- `--bare` would skip every customization but **never reads OAuth credentials**, so it can't use
  a subscription. `--safe-mode` keeps auth but also disables MCP servers, which the chat needs.
  whatnow instead lists exactly what it allows (below).
- With `--json-schema`, the result's `structured_output` holds the reply. It's validated and
  retried by the CLI, not guaranteed: the result subtype `error_max_structured_output_retries`
  reports failure.
- `ANTHROPIC_API_KEY` in the environment always wins in `-p` mode, so whatnow removes it (and
  `ANTHROPIC_AUTH_TOKEN`) from the child's environment. Otherwise "use my plan" would silently
  bill an API key.
- **Policy.** Anthropic's Agent SDK overview: "Unless previously approved, Anthropic does not
  allow third party developers to offer claude.ai login or rate limits for their products,
  including agents built on the Claude Agent SDK." The legal page adds that it doesn't "prevent an
  end user from signing in to the unmodified Claude Code binary with their own Claude
  subscription", and that plan limits "assume ordinary, individual usage". whatnow never handles
  credentials: it runs the user's own unmodified `claude`, which signs itself in. The provider is
  framed as personal use, and the UI says so. **If whatnow is distributed with this provider
  shown by default, ask Anthropic first.** That decision belongs to the maintainer, not this plan.

**GitHub Copilot SDK**
- `github.com/github/copilot-sdk/go`, GA since 2026-06-02, latest stable `v1.0.22`. It spawns the
  Copilot runtime (`copilot --headless --stdio`) and talks JSON-RPC to it.
- The runtime path comes from `StdioConnection.Path`, `COPILOT_CLI_PATH`, or a binary embedded
  with `go tool bundler`. It does **no PATH lookup**, despite its README. whatnow looks up
  `copilot` itself and does not embed it, to keep the release binaries small.
- Auth, in order: an explicit token, `COPILOT_GITHUB_TOKEN`/`GH_TOKEN`/`GITHUB_TOKEN`, the stored
  `copilot` login, `gh auth`. Classic `ghp_` tokens don't work.
- `Mode: ModeEmpty` turns off built-in tools, environment context and custom instructions, and
  needs a `BaseDirectory`. ModeEmpty may block the stored keychain login
  (`COPILOT_DISABLE_KEYTAR=1`), so whatnow passes `gh auth token` explicitly when no token is in
  the environment.
- Custom tools are Go handlers (`copilot.Tool{Handler}`), and **calls run in parallel**. Tools
  limited by `AvailableTools: ["custom:<name>"]`. A nil permission handler leaves requests
  pending forever, so whatnow rejects every permission request; its own tools set
  `SkipPermission`.
- Structured output (`ResponseSchema`) is experimental and only unmarshals, without validating.
  whatnow asks for JSON in the prompt instead, and checks it as it does now.
- There's no way to hand a session earlier assistant turns. History is folded into the prompt.
- Billing: each message counts against the user's Copilot allowance (AI Credits since 2026-06-01).
  Known issues to design around: `SendAndWait` defaults to a 60 s timeout, cancelling doesn't stop
  the agent (`Abort` does), and nothing restarts a runtime that died.

## The seam: who runs the tool loop

whatnow's chat runs its own tool loop (`agent.turn.run`): one `Chat` call per round, tools run in
between, up to 10 rounds. Both SDKs run the loop themselves and call tools back. So:

- **`llm.Looper`**, an optional interface a `Client` may also implement:

  ```go
  Loop(ctx, req ChatRequest, maxRounds int, run RunTool, onText func(string)) (string, error)
  ```

  It runs the whole turn: tools through `run`, text through `onText`, separate model messages
  joined with a blank line. It returns everything said, even alongside an error.
- **The agent uses `Loop` when the client has it**, and its own loop otherwise. Anthropic and
  OpenAI are untouched. A looper's tool calls may arrive in parallel (Copilot), but a turn's
  writes share one batch, so the agent runs them one at a time.
- **Round limit:** `llm.ErrTooManyRounds` from `Loop` becomes the same "(I stopped after 10
  rounds…)" text the agent's own loop gives.
- **A turn is bounded by 5 minutes** for a looper, against 2 minutes per call for the others, since
  one `Loop` covers every round.
- **History** is folded into one prompt for both backends: earlier messages as a tagged record,
  then the new message.

`Complete` maps directly: one prompt, no tools, the reply's text or JSON. `Fallback`, `Cache` and
`MaxTokens` are Claude-API features. Both backends ignore them.

## Claude Code backend (`llm/claudecode.go`)

- **One `claude` process per call**, run in an empty working directory of whatnow's own
  (`<user cache dir>/whatnow/claudecode`), so no project `CLAUDE.md` or settings apply.
- **Every call passes:** `-p --model <m> --system-prompt <s> --tools "" --setting-sources ""
  --strict-mcp-config --no-session-persistence --permission-mode dontAsk`. User text arrives on
  stdin. The environment loses `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN`, and gains
  `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1`.
- **Complete** adds `--output-format json`, and `--json-schema <schema>` when there's a schema.
  The reply is `structured_output` re-marshaled, or `result`.
- **Loop** adds `--output-format stream-json --verbose --include-partial-messages --max-turns
  <maxRounds+1> --mcp-config <json> --allowedTools mcp__whatnow__*`. The MCP config points at a
  loopback MCP server whatnow starts for the turn (below). Text deltas are the `stream_event`s
  whose `event.delta.type` is `text_delta`. A `message_start` after any text means the next text
  needs a blank line first.
- **The turn's MCP server:** the official Go MCP SDK (`github.com/modelcontextprotocol/go-sdk`
  v1.8.0), streamable HTTP, on `127.0.0.1:0`, with a random bearer token in the config's headers.
  It lists the request's tools and answers each call through `run`, using the turn's context. It
  closes when the turn ends. It never touches whatnow's main HTTP server or its allowed hosts.
- **Errors**, from the final `result` event:
  - `error_max_turns` → `ErrTooManyRounds`
  - text containing "Not logged in" or "/login" → "Claude Code is not signed in. Run `claude`
    in a terminal and sign in"
  - any other `is_error`, or a subtype other than `success` → `claude code: <result text or
    errors, or the subtype>`
  - no result event, or a process that fails before printing one → `claude code: <last line of
    stderr>`
  - a schema request whose result has no `structured_output` → `claude code: no structured
    output`

## Copilot backend (`llm/copilot.go`)

- **One runtime per whatnow process**, started on first use and stopped when the app closes.
  `ModeEmpty`, `BaseDirectory` `<user cache dir>/whatnow/copilot`, runtime path from
  `COPILOT_CLI_PATH` or `copilot` on `PATH`. If the runtime dies, the next call starts a new one.
- **Token:** none from whatnow when `COPILOT_GITHUB_TOKEN`, `GH_TOKEN` or `GITHUB_TOKEN` is set.
  Otherwise the output of `gh auth token` when `gh` is installed and signed in. Otherwise the
  SDK's defaults.
- **One session per call**, deleted afterwards: `SystemMessage` mode `replace`, `Streaming`,
  `SkipCustomInstructions`, `EnableConfigDiscovery: false`, `EnableSessionStore: false`, the base
  directory as its working directory, and a permission handler that rejects everything.
- **Complete:** no tools. With a schema, the system prompt ends with the schema and "Reply with
  only a JSON object that matches it, with no other text." The reply is trimmed of code fences.
- **Loop:** each request tool becomes a `copilot.Tool` with `SkipPermission`, its schema as
  `Parameters`, and a handler that calls `run`. `AvailableTools` names only them. A round is one
  top-level assistant message with tool requests. Past `maxRounds`, the session is aborted and
  `Loop` returns `ErrTooManyRounds`.
- **Errors:**
  - `session.error` of type `authentication` or `authorization` → "GitHub Copilot is not signed
    in. Run `gh auth login`"
  - type `quota` → "GitHub Copilot's usage limit is reached"
  - any other → `copilot: <message>`
  - a runtime that can't start → `copilot: <error>`, and the next call tries again
- **Tests:** the adapter talks to the SDK through two small interfaces (`copilotRuntime`,
  `copilotSession`), so unit tests drive it with a scripted fake. One live test runs only with
  `WHATNOW_LIVE=1`.

## Settings

- **Providers:** `claudecode` and `copilot` join `anthropic` and `openai`, each with its own
  `ProviderConfig` (`claudecode`, `copilot` in the JSON). `BaseURL` is unused for them.
- **Default models:** Claude Code `sonnet` and `haiku` (the CLI's aliases); Copilot
  `claude-sonnet-4.5` and `gpt-5-mini`, confirmed against `ListModels` before release.
- **No key:** they never take a key. `PUT /settings/keys/{claudecode,copilot}` is a 400. Their
  `KeySource` is `login` when the CLI is found (`claude` on `PATH`; `copilot` per the runtime rule
  above), else empty. That's enough for `configured()`. The connection test is what checks the
  sign-in.
- **`NewClient`** builds them without a key, and returns `ErrNotConfigured` when the CLI isn't
  found.

## Screens

- **Web settings:** the provider select gains "Claude Code (your Claude plan)" and "GitHub
  Copilot". For them the key field is replaced by a note:
  - Claude Code: "Uses the Claude Code you're signed in to, so your Claude plan's limits apply.
    For your own use on your own machine." Then either "Found `claude`." or "Install Claude Code
    first: https://code.claude.com".
  - Copilot: "Uses your GitHub Copilot plan. Each message counts toward your Copilot usage."
    Then either "Found the Copilot CLI." or "Install the Copilot CLI first: `brew install
    copilot-cli`".
- **Welcome:** two more provider choices. The key step shows the same note, with Next enabled
  once the CLI is found.
- **TUI:** no change. Provider setup is web-only.

## Not in scope

- Picking Copilot models from a list. The model fields stay free text.
- Embedding the Copilot runtime in the binary.
- Long-lived `claude` processes. The per-call spawn costs about a second; revisit if it shows.
- Prompt caching, refusal fallback and token limits on these providers.

## Checked on 2026-10-19

- claude 2.1.303, signed in (claude.ai, Max): every flag accepted as listed; `--setting-sources ""`
  accepted. A Complete call returns a `result` with `subtype` `success` and `result` `ok`; with
  `--json-schema` it returns `structured_output` `{"tier":"must"}`.
- stream-json with `--verbose --include-partial-messages`: `system`/`init` (tools `[]`, no MCP
  servers), then `stream_event`s `message_start`, `content_block_start`, `content_block_delta`
  (`text_delta`), `content_block_stop`, `message_delta`, `message_stop`, with an `assistant` and a
  `rate_limit_event` between, and a final `result`. `parent_tool_use_id` is `null`.
- Signed out (a fresh `CLAUDE_CONFIG_DIR`): `result` with `subtype` `success`, `is_error` `true`,
  `result` "Not logged in · Please run /login".
- Copilot CLI: not installed or checked; the user didn't approve installing it or Copilot calls.
  The Copilot default models and the install command are unverified.
