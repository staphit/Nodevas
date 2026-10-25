# Token usage

Nodevas keeps what the coding agents spent, so the owner can see where tokens go and judge whether a plan, a model or a harness is worth it.

Two sources feed it:

- **Runs.** A runner reports each run's usage with its result (`runs.usage`), including the model the CLI said it ran on when it said (`runnerwire.Usage.Model`).
- **Agent sessions.** An agent its owner runs in their own terminal reports its running totals per model through `nodevas hook`. Each report replaces the totals stored for that session and model; reports never add up. They are kept in `agent_session_usage`, one row per project, session and model.

## Cost

Every figure is an **API-equivalent estimate**. An agent signed in with a subscription pays nothing per token, and the prices are a vendor's list prices on a date. Use the cost to compare plans and harnesses, not as a bill.

- When an agent says what something cost, that figure is used: Claude Code's `-p` result, and OpenCode. In the report it is `reportedCostUsd`.
- Otherwise the cost is estimated from the price table (`estimatedCostUsd`).
- Tokens of a model with no price are counted in `unpricedTokens` and in no cost.

The built-in prices are Anthropic's first-party list prices as of `usage.PricesAsOf`. Cache writes are priced at the 5-minute rate. Claude Code's aliases (`opus`, `sonnet`, `haiku`) are priced as the models they resolved to on that date, and OpenCode's free models (`opencode/*-free`) cost nothing.

Other vendors' models (`gpt-*`, `gemini-*`, …) are deliberately left unpriced. The owner adds what they pay as custom prices.

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
| `GET` | `/api/usage?days=N` | `days` is 1–365, default 30, counted in UTC days with today included. The response carries `totals`, `byAgent`, `byModel`, `byDay` and `sessions` (newest first, at most 200). Runs are filtered as the runs list filters them; agent sessions are the project's own, as the roster shows them. |
| `GET` | `/api/usage/prices` | `{prices: [{model, inputPerMTok, outputPerMTok, cacheReadPerMTok, cacheWritePerMTok, source}], asOf}`. Custom prices come first. |
| `PUT` | `/api/usage/prices` | `{prices: [...]}` replaces every custom price. Administrators only. At most 200 entries, each price between 0 and 10000 USD per million tokens. |

## Which day usage counts on

- A run's usage counts on the day the run was created.
- A session's totals are cumulative, so they count whole on the day of that model's last report. A session that began before the window and reported inside it shows up entirely inside the window.
