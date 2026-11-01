# Concepts

Nodevas keeps the workspace visible as a graph while preserving each node as an editable document.

## Nodes and relationships

- Every node has a type: Task, Goal, Scene, Event, Review, Decision, Start, End or Note, or one of the project's own types (**Settings → This project → Node types**). The type decides whether agents may work the node, who may mark it done, whether it has a status at all, and its card's default shape and colour.
- A node can contain Markdown pages, metadata, tags, an assignee, priority, status, and links.
- A relationship connects nodes visually and can represent an executable prerequisite.
- The **Board** shows a project as a dependency graph and on a timeline.

## Dependency gates

Most of the time you only need "B depends on A": Alt+drag from A onto B, or choose **Add dependency…** in B's context menu, which opens B's node panel on its **Dependencies** tab where you pick A. B is blocked until A is done, and A shows up under B's **Depends on** list while B shows up under A's **Blocks** list.

When B depends on several nodes, it waits for **all** of them. That is the default, so the editor shows these connections as plain wires with no gate badge. A single dependency needs no gate either.

### Advanced conditions (gates)

When "wait for all of them" is not what you mean, open **Advanced conditions** in the node panel's Dependencies tab or in the connection's menu. A node that already uses one of these keeps that section open and shows a badge on the canvas, so a stored condition is never hidden.

| Gate | Requirement |
| --- | --- |
| `AND` | Every input must be complete (the default for several dependencies). |
| `OR` | At least one input must be complete. |
| `XOR` | Exactly one input must be complete. |
| `NAND` | Not every input may be complete. |
| `NOR` | No input may be complete. |
| `MUST` | A standalone gate with one input that must be complete. |

Connections can also be marked *optional* (does not block) or *deprecated* (kept as a line, not a prerequisite). Standalone logic gates, created from the canvas context menu under **Advanced conditions**, take over a node's whole condition; `AND`, `OR`, `XOR`, `NAND`, and `NOR` gates need at least two inputs. **Convert to logic gate** in a connection's menu turns optional or deprecated connections into one. An incomplete gate remains a visible draft and does not accidentally make a node ready.

The same conditions can be written in a node's `requires` expression in `graph.yaml`; the Dependencies tab shows it read-only as **Condition expression**. Expressions support parentheses, `and`, `or`, `xor`, `nand`, `nor`, `not`, `all(…)`, `any(…)`, node references, and flags, including comparisons such as `flag(karma >= 3)`. The expression is the executable condition; canvas wires are its visual projection. An expression nested more than 256 levels deep is refused.

Example:

```text
design and (review or flag(approved))
```

The target becomes ready only when the expression is satisfied. If it is blocked, Nodevas reports the node references that explain the condition.

A node can also use Notes as context (**Dependencies → References**). A reference never blocks.

## Ready work

A task is ready when its dependency condition is satisfied and it has not already reached a settled status. Filter the board by the **Ready** status to see the work that can start now; agents get the same list from `get_ready_tasks`.

## Node information

Clicking a card opens the node panel:

- a top row with the status, the assignee and, once agents are on, **Dispatch to agent**;
- **Content**: the page, its subpages and the acceptance criteria;
- **Dependencies**: Depends on, Blocks, References, Advanced conditions and document links;
- **Activity**: agent runs, planned milestones and actual status changes, and file versions;
- **More properties**: type, entry, priority, tags, links, appearance, write access and delete.

Metadata is saved as fields change. A status change is staged until you press **Apply status** or Ctrl/⌘+S, because it represents an actual event in the run history.

## Also included

- **Project explorer** (left of the board): **Nodes**; **Files** (the project folder, without projects nested in it, and your runners' repositories, read-only); **Git** (this project's uncommitted changes and version history, agents' branches, and uncommitted changes in your runners' repositories); **Agent** (running, external and recently finished agent sessions).
- Multiple projects and subprojects in one workspace tree.
- Ctrl/⌘+K searches the nodes and pages of every project you can open, subpages included, and runs commands.
- **Problems** (board header) lists validation warnings, schedule issues such as overdue nodes, and hints.
- Markdown, plain text, HTML, and Word document pages. Previews drop anything that could pose as the app (image maps, dialogs, `background` attributes, class names of their own), Markdown nested more than 64 levels deep shows as plain text past that depth, and in a Word document tables nested more than 32 deep become text and list levels past 32 join level 32.
- Imports from Markdown, JSON Canvas, and `.veproj` or `.zip` project bundles; exports to `.veproj` (a project or the whole workspace), JSON Canvas, and, for documents, Word, plain text, HTML, Markdown, and PDF.
- Filters for status, assignee, tag, priority and text, and saved views per project.
- Drafts, history, trash, conflict detection, and filesystem change notifications.
- Inspectable project files that remain useful with Git and ordinary text tools.
- English and Traditional Chinese (`zh-TW`) interface, following the browser's language until you pick one; light and dark mode; text size 90-150% (**Settings → General**).

## More details

- [Timeline and progress](./timeline.md)
- [Storage and collaboration](./collaboration.md)
- [MCP integration](./mcp.md)
