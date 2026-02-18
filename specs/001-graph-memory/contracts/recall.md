# Tool Contract: recall

**Tool name**: `recall`
**Version**: 2.0 (adds 1-hop graph expansion)

## Purpose

Searches memories by semantic similarity to the query. Returns the top-K
matching memories plus all memories directly connected to each result
(1-hop neighbors in both directions). Also updates recall state for
episode memories (extends expiration, promotes if threshold reached).

## Input Schema

```json
{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "Natural language query to find related memories"
    }
  },
  "required": ["query"]
}
```

## Output

```json
{
  "output": "{\"memories\":[{\"title\":\"...\",\"content\":\"...\",\"type\":\"...\",\"source\":\"search|neighbor\"}]}"
}
```

Each memory object:

| Field | Type | Description |
|-------|------|-------------|
| `title` | string | Memory title |
| `content` | string | Memory content |
| `type` | string | `fact`, `preference`, or `episode` |
| `source` | string | `search` (top-K hit) or `neighbor` (graph expansion) |

Returns `{"memories":[]}` when no matches found (no error).

## Behavior

1. Validate `query` is non-empty. Return error if not.
2. Generate query embedding via `embedding.Generate(ctx, query)`.
3. Call `graphStore.TopK(queryEmbedding, topK)` → candidate memories.
4. For each candidate memory:
   a. Call `graphStore.Neighbors(id)` → neighbors in both directions.
   b. If memory type is `episode`:
      - If `recall_count + 1 >= PromotionThreshold`: call `graphStore.Promote(id)`.
      - Else: call `graphStore.ExtendExpiration(id, ExpirationExtension)`.
      - Call `graphStore.IncrementRecall(id)`.
5. Build result set: union of top-K hits (source=`search`) and all unique
   neighbors (source=`neighbor`), deduplicating by title.
6. Return JSON-encoded result.

If embedding is unavailable, return `{"memories":[]}` rather than erroring.
The file-listing fallback is removed in v2.0.
