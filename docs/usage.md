# Token usage

Nodevas keeps what the coding agents and the voice copilot spent, so the owner can see where tokens go and judge whether a plan, a model or a harness is worth it.

In the app it is **Settings → Agents → Advanced → Usage details** (Overview, Sessions, Prices), or **Usage** in the command palette (Ctrl/⌘+K). Both appear once the agent workstation is shown: **Let agents work here** is on, or the workspace already has a runner, a run or a connected agent.

Three sources feed it:

- **Runs.** A runner reports each run's usage with its result (`runs.usage`), including the model the CLI said it ran on when it said (`runnerwire.Usage.Model`). The agent session of an interactive run reports into that run's usage, not into a session of its own.
- **Agent sessions.** An agent its owner runs in their own terminal reports its running totals per model through `nodevas hook`. Each report replaces the totals stored for that session and model; reports never add up. They are kept in `agent_session_usage`, one row per project, session and model.
- **Copilot.** Each voice copilot turn that used tokens records its input and output tokens and its model, as agent `copilot`. The record stays when the conversation is deleted. Copilot turns count in the totals and per agent, model, role and day, but are not listed among the sessions.

## Cost

Every figure is an **API-equivalent estimate**. An agent signed in with a subscription pays nothing per token, and the prices are a vendor's list prices on a date. Use the cost to compare plans and harnesses, not as a bill.

- When an agent says what something cost, that figure is used: Claude Code's `-p` result, and OpenCode. In the report it is `reportedCostUsd`.
- OpenCode 2 reports tokens at the end of each step but not after its last one, so an OpenCode 2 run's tokens and cost leave out the step that wrote the answer.
- Otherwise the cost is estimated from the price table (`estimatedCostUsd`). A run is priced at the first of these that has a price: the model it reported, the model it was dispatched with, its CLI's default (`gpt-5.5` for Codex, `gemini-2.5-pro` for Gemini CLI and Antigravity). A run of a CLI that never reports a cost (Codex, Gemini CLI, Antigravity) always counts as an estimate.
- Tokens of a model with no price are counted in `unpricedTokens` and in no cost.
- A run's cost is worked out once, when the run reports its usage, at the prices of that moment; budgets and Agent performance add up these costs. Agent sessions and copilot turns are priced each time the report is made, so a price change reaches them but not runs already priced.

The built-in prices are Anthropic's first-party list prices as of `usage.PricesAsOf`. Cache writes are priced at the 5-minute rate. Claude Code's aliases (`opus`, `sonnet`, `haiku`) are priced as the models they resolved to on that date, and OpenCode's free models (`opencode/*-free`) cost nothing.

For the CLIs that report no cost, Nodevas adds its own estimates, list prices on the same date (`source: estimate`): `gpt-5.5`, `gpt-5.4-mini`, `gemini-2.5-pro` and `gemini-2.5-flash`. Other models are left unpriced. The owner adds what they pay as custom prices; a custom price also overrides an estimate of the same name. Agent performance notes "Includes estimated costs for CLIs that report $0." when its figures hold estimates.

### How a model finds its price

1. The model name is tried three ways:
   - as reported;
   - after its last `/`, for a provider prefix such as `anthropic/claude-…`;
   - from `claude-` on, for cloud prefixes such as `us.anthropic.claude-…`.
2. An entry matches a name that equals it, or that starts with it followed by a separator. That way date and variant suffixes still match, for example `-20250929`, `@…` or `[1m]`.
3. One `*` in an entry matches anything.
4. When several entries match, the longest one wins. On a tie, a custom price wins over a built-in one.

## API

| Method | Path | Notes |
| --- | --- | --- |
| `POST` | `/api/agents/sessions/:id/usage` | `{agent?, models: [{model, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, costUsd}], actor?}`. Same session binding as `…/changes`. The runner proxy forwards it. Sends `usage-changed` to the project's room. |
| `GET` | `/api/usage?days=N` | `days` is 1–365, default 30, counted in UTC days with today included. The response carries `days`, `since`, `currency`, `pricesAsOf`, `totals`, `byAgent`, `byModel`, `byDay`, `byRole`, `byNode` (the 50 costliest nodes) and `sessions` (newest first, at most 200; `source` is `run` or `session`). Runs are filtered as the runs list filters them; agent sessions are the project's own, as the roster shows them; copilot turns are the project's for an administrator, else the caller's own. |
| `GET` | `/api/usage/prices` | `{prices: [{model, inputPerMTok, outputPerMTok, cacheReadPerMTok, cacheWritePerMTok, source}], asOf}`; `source` is `custom`, `builtin` or `estimate`. Custom prices come first. |
| `PUT` | `/api/usage/prices` | `{prices: [...]}` replaces every custom price. Administrators only. At most 200 entries, each price between 0 and 10000 USD per million tokens. |

## Which day usage counts on

- A run's usage counts on the day the run was created.
- A copilot turn counts on the day it was made.
- A session's totals are cumulative, so they count whole on the day of that model's last report. A session that began before the window and reported inside it shows up entirely inside the window.
