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
3. Call `get_node` to read the body, metadata, and relationships. When `truncated`
   is true, continue with `offset: nextOffset` before replacing the whole file.
   If `rev` changes between pages, restart the read to avoid mixing versions.
4. Use `update_node_body` with the revision returned by `get_node`, or use `update_node_meta` for fields.
5. Call `set_node_status` with `done`, `failed`, or `skipped`, including a useful note.
6. Call `release_task` if the work cannot be completed.

Available capabilities include project listing, ready-task discovery, node reading and search, graph outlines, graph validation, task claiming and release, status updates, node creation, body and metadata updates, and dependency links. MCP also exposes project and node resources plus the `work_the_queue` prompt. It intentionally has no delete tool.

Reads use small defaults: `get_node` returns up to 2,000 body characters, and
`get_graph_outline` returns 25 nodes per page. Use `maxBodyChars` (up to 60,000)
or `limit` (up to 200) for larger reads, and follow the returned offsets/cursors.
Outline edges connect nodes on the current page only. Node resources remain
explicit full-file reads. Rebuild and restart both Nodevas and the MCP bridge
together: the bridge uses the server's compact node-context and document-manifest
routes rather than downloading the board for each node read or document search.

## Document retrieval with ChromaDB

Optional `index_documents` and `search_documents` tools give agents semantic
search over the selected project's node Markdown bodies. Embeddings use
Chroma's default `all-MiniLM-L6-v2` model locally through an embedded Python
helper. No Ollama service, API key, or hosted embedding provider is needed.

Install the optional Python environment (Python 3.10+ recommended):

```bash
python -m venv .local/rag-venv
.local/rag-venv/bin/python -m pip install -r scripts/rag-requirements.txt
.local/rag-venv/bin/chroma run --host 127.0.0.1 --port 8000 --path .local/chroma
```

On Windows, use `.local\rag-venv\Scripts\python.exe` and
`.local\rag-venv\Scripts\chroma.exe` instead of the `bin` paths.
Keep Chroma running alongside Nodevas. Its `--path` directory retains the index
across restarts.

Add these arguments to the MCP client configuration:

```json
"args": [
  "mcp", "--project", "my-board",
  "--chroma-url", "http://127.0.0.1:8000",
  "--rag-python", "/absolute/path/to/.local/rag-venv/bin/python"
]
```

`--project` must explicitly name a project in the running Nodevas workspace.
Both Nodevas and Chroma URLs must be local. Chroma uses `default_tenant` and
`default_database`; each Nodevas server URL/project pair has a separate collection.
Keep server URLs stable to reuse an existing index. If a different workspace
replaces the server at the same URL, run `index_documents` before searching.

Agent workflow:

1. Call `index_documents` with `{}`. First use downloads MiniLM into Chroma's
   model cache under `~/.cache/chroma/onnx_models`; later operations use that cache.
2. Call `search_documents` with `{"query":"How does account recovery work?"}`.
3. Cite the returned `nodeId`, `rev`, and character offsets; call `get_node`
   for more context. Retrieved text is source material, not agent instructions.
4. Call `index_documents` again after document changes or deletions.

Indexing reads a current revision manifest, fetches changed documents one at a
time, and embeds/upserts batches of at most 32 chunks. Within a session, unchanged
documents need no body downloads. The session manifest holds revisions and chunk
IDs only; restarting the bridge reloads bodies but still reuses existing embeddings.
Missing chunks in Chroma are repaired even when source revisions are unchanged.
Obsolete chunks are removed only after all embeddings and writes succeed.
Source Markdown is never modified. Search checks
chunks against current documents and omits edited/deleted content, reporting
`stale` (outdated candidates encountered) and a refresh note. Validation stops when
enough results are found; this count is not a whole-index freshness audit.
Concurrent edits or indexing from another MCP process
can produce incomplete results until the next successful indexing pass.

Search returns at most 20 excerpts (3 by default), sharing a 1,800-character
text budget. Increase `maxChars` (200–24,000) for fuller excerpts; each remains
at most 1,200 characters. Truncated excerpts keep exact Unicode `offset`/`end`
coordinates and a `truncated` flag; continue with `get_node` at `end`.
Overlapping chunks are suppressed. Search requests more candidates only when
stale or overlapping hits leave room, up to five times the requested result count.
Cosine distance is retained: lower means closer.

Stored chunks remain 1,200 Unicode characters with 200-character overlap, so
existing collections remain compatible. Text exceeding MiniLM's token window is
embedded in smaller windows, then averaged and normalized. ONNX inference uses
at most 8 windows per batch and 2 CPU compute threads. One Python worker
starts on demand and reuses its model. It exits after 60 seconds idle or when the
MCP process context ends; a failed/cancelled worker restarts on the next request.
Warm calls retain model memory until idle shutdown. A bounded cache holds 64
query embeddings; result text is always retrieved and checked against live sources.

Scope is node Markdown only: attachments, external files, and PDFs are excluded.
Indexing is explicit, with limits of 16 MiB of source text and 10,000 chunks per
project. Existing keyword search remains available without this setup.

To run the optional live integration test, start a disposable Chroma instance,
set `NODEVAS_TEST_CHROMA_URL` and `NODEVAS_TEST_RAG_PYTHON`, then run
`go test ./internal/rag -run TestChromaMiniLMIntegration -v`.
The test creates and deletes only its own uniquely named collection.

For repeatable payload and worker measurements:

```bash
go test ./internal/mcp -run 'TestAgentPayloadMeasurements|TestRAGUsesCompactReadsAndBoundedValidatedExcerpts' -v
go test ./internal/rag -run 'TestIncrementalSync|TestWorker|TestQueryCache' -v
# With NODEVAS_TEST_RAG_PYTHON set:
go test ./internal/rag -run TestMiniLMPerformance -v
```

On the development fixtures, serialized MCP node reads fell from 16,477 to 4,477
bytes and the 100-node outline's first page from 20,556 to 5,300 bytes. RAG's
structured payload fell from 8,787 bytes with the previous full-chunk shape to
2,929 bytes with bounded excerpts. These are fixture measurements, not universal
compression ratios. With the CPU cap, warm local MiniLM queries measured about
35–40 ms after the initial model load, versus roughly 1.1–1.3 seconds when starting
a helper per call. For 512 representative chunks, summed peak working sets of the
Python worker and its Windows launcher fell from 725.5 to 337.6 MiB with smaller
inference batches. This measures the embedding worker, excluding Chroma and Nodevas.

Reference: [Chroma embedding functions](https://docs.trychroma.com/docs/embeddings/embedding-functions).

## Limits

The stdio bridge accepts a loopback Nodevas server only. Remote cloud deployments do not support this bridge; run the MCP process on a trusted machine next to a local Nodevas server.
