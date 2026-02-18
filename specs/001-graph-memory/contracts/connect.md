# Tool Contract: connect

**Tool name**: `connect`
**Version**: 1.0 (new tool)

## Purpose

Creates a directed, labeled relational edge between two existing memories.
If an edge already exists between the same ordered pair, it is replaced.
Self-referential connections are rejected.

## Input Schema

```json
{
  "type": "object",
  "properties": {
    "from_title": {
      "type": "string",
      "description": "Title of the source memory (the one that updates, extends, or derives)"
    },
    "to_title": {
      "type": "string",
      "description": "Title of the target memory"
    },
    "relation": {
      "type": "string",
      "description": "Label describing the relationship. Recommended: 'updates', 'extends', 'derives'. Any non-empty string is accepted."
    }
  },
  "required": ["from_title", "to_title", "relation"]
}
```

## Output

On success:
```json
{ "output": "connected: <from_title> -[<relation>]-> <to_title>" }
```

On error:
```json
{ "error": "<validation or lookup message>" }
```

## Behavior

1. Validate all three fields are non-empty. Return error if not.
2. Validate `from_title != to_title`. Return error "cannot connect a memory to itself".
3. Look up `from_title` in graph store → get `fromID`. Return error if not found.
4. Look up `to_title` in graph store → get `toID`. Return error if not found.
5. Call `graphStore.Connect(fromID, toID, relation)` — upserts the edge.
6. Return success output.

## Relation Label Semantics (recommended)

| Label | Use when |
|-------|----------|
| `updates` | The source memory contains newer or corrected information about the topic of the target memory |
| `extends` | The source memory adds detail to the target without contradicting it |
| `derives` | The source memory is a conclusion inferred from the target memory |
