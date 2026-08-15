# Storage and collaboration

Nodevas can be used as a local workspace or as one shared server for a team.

## Project files

Project data remains inspectable and versionable:

```text
workspace/
├── project-a/
│   ├── graph.yaml
│   ├── nodes/
│   │   └── node-0001.md
│   └── run/
│       ├── state.json
│       └── journal.jsonl
├── project-b/
└── .vised/
    ├── drafts/
    ├── history/
    └── trash/
```

`graph.yaml` stores graph structure, metadata, relationships, layouts, users, and planned work. Node bodies are Markdown files. Run state is rebuilt from the append-only journal. `.vised/` contains drafts, history snapshots, and trash and is excluded from Git by default.

## Shared server

The server watches project files and notifies connected browsers over WebSocket. Shared document sessions use CRDT updates, presence information, and persisted sidecars so multiple people can edit the same document.

For team workflows, deploy one shared Nodevas server so collaborators see the same workspace, task state, live document updates, and audit history. Git remains useful for versioning and reviewing the underlying project files.

The default local server listens on loopback. For a shared deployment, follow the [getting started](./getting-started.md) network guidance or the [OCI deployment guide](../deploy/oci/README.md).
