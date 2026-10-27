# Concepts

Nodevas keeps the workspace visible as a graph while preserving each node as an editable document.

## Nodes and relationships

- A node is a document, task, scene, milestone, or other unit of work.
- A node can contain Markdown pages, metadata, tags, an assignee, priority, status, and links.
- A relationship connects nodes visually and can represent an executable prerequisite.
- The same workspace can be viewed as a graph, board, or timeline.

## Dependency gates

Most of the time you only need "B depends on A": draw a connection from A to B (Alt+drag, or **Add dependency** in B's context menu). B is blocked until A is done, and A shows up under B's **Depends on** list while B shows up under A's **Blocks** list.

When B depends on several nodes, it waits for **all** of them. That is the default, so the editor shows these connections as plain wires with no gate badge. A single dependency needs no gate either.

### Advanced conditions (gates)

When "wait for all of them" is not what you mean, open **Advanced conditions** in the node or connection menu. A node that already uses one of these keeps that section open and shows a badge on the canvas, so a stored condition is never hidden.

| Gate | Requirement |
| --- | --- |
| `AND` | Every input must be complete (the default for several dependencies). |
| `OR` | At least one input must be complete. |
| `XOR` | Exactly one input must be complete. |
| `NAND` | Not every input may be complete. |
| `NOR` | No input may be complete. |
| `MUST` | A standalone gate with one input that must be complete. |

Connections can also be marked *optional* (does not block) or *deprecated* (kept as a line, not a prerequisite). Standalone logic gates, created from the canvas context menu under **Advanced conditions**, take over a node's whole condition; `AND`, `OR`, `XOR`, `NAND`, and `NOR` gates need at least two inputs. An incomplete gate remains a visible draft and does not accidentally make a node ready.

The same conditions can be written in a node's `requires` expression. Expressions support parentheses, `not`, node references, and flags. The expression is the executable condition; canvas wires are its visual projection.

Example:

```text
design and (review or flag(approved))
```

The target becomes ready only when the expression is satisfied. If it is blocked, Nodevas reports the node references that explain the condition.

## Ready work

A task is ready when its dependency condition is satisfied and it has not already reached a settled status. The ready view is useful to both people and agents because it turns a large graph into the work that can start now.

## Node information

Selecting a node opens the information panel. It brings together:

- title, node kind, priority, assignee, and tags;
- entry behavior and linked nodes;
- planned milestones and actual history;
- lifecycle status and status note;
- Markdown content, outline, appearance, and node history.

Metadata is saved as fields change. A lifecycle status update is staged until the user applies it, because it represents an actual event in the run history.

## Also included

- Multiple projects and subprojects in one workspace tree.
- Markdown, plain text, HTML, and Word document pages.
- Imports from Markdown, JSON Canvas, and `.veproj`; exports to Word, plain text, HTML, Markdown, and PDF.
- Filters and saved views for status, assignee, label, priority, and project.
- Drafts, history, trash, conflict detection, and filesystem change notifications.
- Inspectable project files that remain useful with Git and ordinary text tools.
- English and Traditional Chinese (`zh-TW`) interface, light and dark mode.

## More details

- [Timeline and progress](./timeline.md)
- [Storage and collaboration](./collaboration.md)
- [MCP integration](./mcp.md)
