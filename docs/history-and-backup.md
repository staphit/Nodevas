# Version history and backup

Traditional Chinese: [history-and-backup.zh-TW.md](./history-and-backup.zh-TW.md)

## Version history: a git repository per project

Each project keeps its history in its own git repository, at `<project>/.vised/git`, with the project folder as its work tree. It is never your own repository. A project folder can sit inside a code checkout: Nodevas commits nothing there, and it writes a `.vised/.gitignore` so that checkout ignores Nodevas's own state, this repository included. The project's own files are still yours to commit or not.

It needs git 2.32 or newer, the first that lets Nodevas ignore your machine's git config. With an older git, Nodevas keeps file copies and says why in its log.

- **What is kept:** `graph.yaml`, every node's document and its subpages. Nodevas's own state (`.vised/`) and the run journal (`run/`) are not.
- **When it commits:**
  - When a file is first changed in an editing session, the version it replaces is committed right away. That is the version you would want back.
  - Later saves are grouped into one commit per five minutes per project.
  - A restore, a delete or a page conversion commits at once. So does shutting the server down.
- **Who it credits:** each commit's author is the person or agent who saved. Anyone else who saved in the same five minutes is added as `Co-authored-by`. Edits made outside Nodevas (another editor, a sync tool) are committed as `Nodevas`.
- **Where you see it:** the node panel's History lists the versions with their time and author. You can preview one and restore it. A restore is a new commit, so it can be undone too.
- **Upgrading:** the first time a project opens with git, its older copies under `.vised/history/` are imported as commits in their original order and with their original times. The old copies are left in place.
- **Without git:**
  - If git is not installed, Nodevas keeps file copies under `.vised/history/` as before.
  - The Docker image includes git.
  - `NODEVAS_HISTORY=files` forces file copies; `NODEVAS_HISTORY=git` requires git. Unset or `auto` uses git when it is there.

You can read a project's history with plain git:

```bash
git --git-dir=<project>/.vised/git log --stat
```

Nodevas runs its git with none of your machine's git settings: no global config, hooks, signing or credential helpers.

The trash still keeps its own copies under `.vised/trash/`.

## Backup to a git remote

In **Settings → Backup and history**, an admin can choose **Git remote**. Each project's history is then pushed to its own branch of one repository, for example a private GitHub repository: `nodevas/<project>`.

- **Address:** HTTPS only. Use a token with write access to that repository. The username defaults to `x-access-token`, which works for GitHub tokens.
- **The token:**
  - Stored encrypted on the server, outside the workspace, and never shown again.
  - Sent to git only through its environment, never on a command line or in a URL.
  - Removed from error messages.
- **Destinations:** a server that is not loopback-only refuses private, loopback and link-local addresses. The address is checked again before each push. With git 2.37 or newer, git is also pinned to the addresses that were checked.
- **Schedule:** pushes run on the backup schedule and from **Back up now**. They are never forced.
- **When the remote branch has moved on** (someone pushed to it), that project is held back and shown as diverged. The others still push. **Keep local** pushes your history to a side branch instead.
- **Test connection** checks the address and token before the first backup.

The folder backup and `.veproj` import and export work as before.
