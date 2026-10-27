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

The default local server listens on loopback. For a shared deployment, follow [Getting started: Team server](./getting-started.md#team-server) and [Allow network access](./operations.md#allow-network-access).

## Invite people

On a server with accounts, an administrator adds people from the browser: *Settings → Server accounts*. The part is shown only to server administrators, and only on a networked server; a loopback server has no accounts. The project *Members* tab links to it ("Person not found? Invite them to this server").

1. Type the person's email address, optionally an account name (it defaults to the part before the `@`), and choose **Member** or **Administrator**.
2. Click **Invite**. The first change in a while asks you to verify it's you (a step-up: a fresh passcode mailed to you, or your authenticator code).
3. Nodevas creates the account and mails the person the server's address with how to sign in: open the address, enter that email, type the one-time passcode mailed to them. There is no password and no sign-in link; the mailbox is the credential, as for every account.

If the invitation cannot be mailed, because the relay refused it or the server has no `smtp` configured, the account is still created and the panel shows the message to pass on yourself. Without outgoing mail nobody can be sent a passcode, so the person can sign in only once mail is configured.

The same panel lists every account with its email, role and whether two-factor authentication is on, changes an account between member and administrator, and removes accounts. Each change asks for a fresh step-up. You cannot remove or demote your own account (ask another administrator), and the last administrator can never be removed or demoted. A role change or removal ends that account's sessions on their next request. `--max-active-users` limits how many people are signed in at once, not how many accounts exist: an invitation past the limit succeeds, and the person is turned away at sign-in while the server is full.

The panel uses the same account rules as `nodevas user add|role|remove` on the server host (unique names and addresses, the same name format), and every change is written to the audit trail as `account.invite`, `account.role` or `account.remove`.

## Project members

On a server with accounts, every project starts **open**: everyone who can sign in can see and change it, as before. To limit a project to a team, open *Settings → Statuses, milestones and people → Members* and add people by account name or sign-in email. The first member added restricts the project, and whoever added them becomes its first **owner**.

| Role | Can |
| --- | --- |
| Viewer | Read the board, its documents, runs and agent roster. Changes are refused with `403 PROJECT_READ_ONLY`; that includes their agents' heartbeats and messages. |
| Editor | Everything a viewer can, plus change the board, dispatch runs and message agents. |
| Owner | Everything an editor can, plus manage the member list. |

Server administrators reach every project as owners. Agents act as the account they run for: an MCP session as its user, a runner's proxied calls as the runner's owner. Agents never manage members.

Someone who is not a member of a restricted project gets `403 PROJECT_ACCESS_DENIED` from its routes. The project also disappears from their project list, search, link picker and exports, and its realtime events (graph changes, presence, document sessions) never reach their sockets. Removing a member moves their live connection out of the project at once.

A project cannot be left without an owner while it has other members. *Open to everyone* removes every member and reopens the project. When an administrator moves a project, its members go with it. A copy gets the same members as the original. Deleting a project from disk forgets its members; detaching it keeps them in case it is added back.

Membership is stored in the `project_members` table and keyed by the project's directory. If you rename a project directory outside Nodevas, it loses its members and becomes open. Use the project menu's move or rename instead.

Membership only applies to servers with accounts. On a loopback server, the local user owns everything.

Each account has its own current project, saved across restarts and never one it can't open: switching moves only that account's tabs, MCP sessions and runner calls, and `?project=` still names a project explicitly.

What membership does not cover yet:
- Hiding a restricted project hides only that project. Projects nested inside it are restricted separately, and a hidden project's folder name can still appear as the parent of a nested project in the tree.
