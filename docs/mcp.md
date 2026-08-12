# MCP integration

Nodevas includes an MCP server over stdio. The MCP process connects to an already running local Nodevas server over HTTP; it does not open the workspace or database itself.

## Start Nodevas

```bash
go run ./cmd/nodevas serve -project ./workspace -port 5666
```

Build the executable used by the MCP client:

```bash
go build -o nodevas ./cmd/nodevas
```

## Configure an MCP client

The same bridge can be used by Claude Code, Codex, or another MCP client:

```json
{
  "mcpServers": {
    "nodevas": {
      "command": "/absolute/path/to/nodevas",
      "args": [
        "mcp",
        "--server",
        "http://127.0.0.1:5666",
        "--project",
        "my-board",
        "--actor",
        "codex"
      ]
    }
  }
}
```

`--server` defaults to `http://127.0.0.1:5666`. `--project` selects the project in the running server; omit it to use the server's current project. `--actor` records attribution for changes and is not authentication.

## Agent workflow

An agent can work the queue safely:

1. Call `get_ready_tasks`.
2. Call `claim_task` before doing the work.
3. Call `get_node` to read the body, metadata, and relationships.
4. Use `update_node_body` with the revision returned by `get_node`, or use `update_node_meta` for fields.
5. Call `set_node_status` with `done`, `failed`, or `skipped`, including a useful note.
6. Call `release_task` if the work cannot be completed.

Available capabilities include project listing, ready-task discovery, node reading and search, graph outlines, graph validation, task claiming and release, status updates, node creation, body and metadata updates, and dependency links. MCP also exposes project and node resources plus the `work_the_queue` prompt. It intentionally has no delete tool.

## Limits

The stdio bridge accepts a loopback Nodevas server only. Remote cloud deployments do not support this bridge; run the MCP process on a trusted machine next to a local Nodevas server.
