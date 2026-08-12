# Concepts

Nodevas keeps the workspace visible as a graph while preserving each node as an editable document.

## Nodes and relationships

- A node is a document, task, scene, milestone, or other unit of work.
- A node can contain Markdown pages, metadata, tags, an assignee, priority, status, and links.
- A relationship connects nodes visually and can represent an executable prerequisite.
- The same workspace can be viewed as a graph, board, or timeline.

## Dependency gates

Dependency gates are one of Nodevas's core features. They express how prerequisite nodes affect the readiness of a target node.

| Gate | Requirement |
| --- | --- |
| `MUST` | Exactly one input must be complete. |
| `AND` | Every input must be complete. |
| `OR` | At least one input must be complete. |
| `XOR` | Exactly one input must be complete. |
| `NAND` | Not every input may be complete. |
| `NOR` | No input may be complete. |

`AND`, `OR`, `XOR`, `NAND`, and `NOR` require at least two inputs. An incomplete gate remains a visible draft and does not accidentally make a node ready.

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

## More details

- [Timeline and progress](./timeline.md)
- [Storage and collaboration](./collaboration.md)
- [MCP integration](./mcp.md)
