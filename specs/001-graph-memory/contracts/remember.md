# Tool Contract: remember

**Tool name**: `remember`
**Version**: 2.0 (replaces subject+content with title+content+type)

## Purpose

Saves a typed memory node to the graph store. If a memory with the same title
already exists, its content is updated in place (upsert). A vector embedding
of the title is stored for semantic recall.

## Input Schema

```json
{
  "type": "object",
  "properties": {
    "title": {
      "type": "string",
      "description": "Short, unique topic label for the memory (used as the lookup key)"
    },
    "content": {
      "type": "string",
      "description": "The information to remember"
    },
    "type": {
      "type": "string",
      "enum": ["fact", "preference", "episode"],
      "description": "Memory type: fact (permanent truth), preference (behavioral pattern), episode (time-bound event)"
    }
  },
  "required": ["title", "content", "type"]
}
```

## Output

On success:
```json
{ "output": "remembered: <title>" }
```

On error (missing/invalid fields):
```json
{ "error": "<validation message>" }
```

## Behavior

1. Validate `title`, `content`, and `type` are non-empty; validate `type` is one
   of `fact`, `preference`, `episode`. Return error if not.
2. Compute default `expires_at`:
   - `fact` → nil (no expiration)
   - `preference` → nil (no expiration)
   - `episode` → `NOW + 7 days`
3. Call `graphStore.Save(title, content, type, expiresAt)` — upserts the node.
4. Generate title embedding via `embedding.Generate(ctx, title)`.
5. Call `graphStore.SaveEmbedding(id, embedding)` — upserts into vec_memories.
6. Return success output.

If embedding or index fails, the memory is still saved and success is returned
(degraded mode — search will not return this entry until embedding succeeds).

## Edge Types (recommended labels for agents)

When the agent later calls `connect`, these labels are recommended:
- `updates` — new memory contradicts or supersedes the connected memory
- `extends` — new memory adds detail without replacing the connected memory
- `derives` — this memory was inferred from the connected memory
