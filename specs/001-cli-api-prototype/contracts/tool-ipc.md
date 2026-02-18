# Tool IPC Contract

The containerized agent communicates with the daemon via HTTP over a Unix domain socket mounted into the container at `/run/blueclaw/ipc.sock`.

The daemon exposes tool endpoints on this socket. The agent (or the LLM tool execution layer inside the container) calls these endpoints when the LLM requests a tool.

## Endpoints

### POST /tools/remember

Save a memory.

**Request**:
```json
{
  "subject": "project deadlines",
  "content": "The MVP is due March 15. Beta launch is April 1."
}
```

**Response** (200):
```json
{
  "status": "saved",
  "filePath": "short-term-memory/project-deadlines.md"
}
```

### POST /tools/recall

Search memories by semantic similarity.

**Request**:
```json
{
  "query": "When is the MVP due?",
  "topK": 5
}
```

**Response** (200):
```json
{
  "memories": [
    {
      "subject": "project deadlines",
      "content": "The MVP is due March 15. Beta launch is April 1.",
      "distance": 0.123,
      "storage": "short-term"
    }
  ]
}
```

### POST /tools/schedule

Create a recurring scheduled job.

**Request**:
```json
{
  "cronExpression": "0 9 * * 1",
  "prompt": "Summarize my weekly goals and check for upcoming deadlines."
}
```

**Response** (200):
```json
{
  "status": "scheduled",
  "jobID": "abc-123",
  "nextRunAt": "2026-02-24T09:00:00Z"
}
```

## Error Responses

All endpoints return errors in this format:

```json
{
  "error": "embedding server unavailable",
  "code": "EMBEDDING_UNAVAILABLE"
}
```

| Code | Description |
|------|-------------|
| EMBEDDING_UNAVAILABLE | llama-server sidecar is down |
| STORAGE_FULL | Disk space below threshold |
| INVALID_CRON | Cron expression could not be parsed |

## Transport

- Socket path inside container: `/run/blueclaw/ipc.sock`
- Socket path on host: `~/.blueclaw/ipc.sock`
- The host bind-mounts the socket's parent directory into the container
- Content-Type: `application/json` for all requests and responses
