# SQS

Browse SQS queues, peek at messages, and manage queue operations.

## Views

### Queue List

Lists all SQS queues with attributes fetched in parallel after listing.

| Column | Description |
|--------|-------------|
| Queue Name | Queue name (parsed from URL) |
| Type | Standard or FIFO |
| Messages | Approximate message count |
| In-Flight | Messages currently being processed |
| Delayed | Messages waiting for delay period |
| DLQ | Whether this queue is a dead-letter queue |
| Created | Creation date |

DLQ detection works by cross-referencing `RedrivePolicy.DeadLetterTargetArn` across all queues, not just checking `RedriveAllowPolicy` (which is optional).

### Queue Detail (side panel)

Pressing `enter` or `d` fetches queue attributes and DLQ source queues. Displays 6 tabs:

1. **Info** — ARN, URL, type, created/modified timestamps
2. **Configuration** — visibility timeout, delay, max message size, retention, receive wait time
3. **Messages** — approximate counts (navigable link to message peek view)
4. **DLQ Config** — redrive policy, allow policy, source queues (navigable links)
5. **Encryption** — SSE-SQS or KMS settings
6. **FIFO Settings** — content-based dedup, dedup scope, throughput limit (FIFO only)

### Message Browser

Opt-in message peek view — navigating here shows a prompt; press `l` to consciously trigger `ReceiveMessage`. This avoids unintended `ApproximateReceiveCount` increments that could trigger DLQ redrive policies.

Messages are peeked with `VisibilityTimeout=0` so they remain visible to other consumers. A warning banner is always shown.

| Column | Description |
|--------|-------------|
| Message ID | Truncated message identifier |
| Sent | Sent timestamp |
| Receives | Approximate receive count (updates on re-peek) |
| Group ID | Message group ID (FIFO only) |
| Body | Truncated body preview |

Pressing `enter` on a message opens the full body in the content viewer with JSON formatting if valid JSON.

Messages are deduplicated by MessageID across load-more calls. Subsequent peeks update existing messages with fresh attributes (e.g., ReceiveCount).

## Keybindings

### Queue List

| Key | Action | Mode |
|-----|--------|------|
| `enter` / `d` | Queue detail panel | Any |
| `p` | Peek at messages | Any |
| `m` | Manage (send, purge, delete) | ReadWrite |
| `y` | Copy queue URL | Any |
| `space` | Multi-select | Any |
| `s` / `S` | Sort / reverse sort | Any |
| `/` | Filter | Any |
| `r` | Refresh | Any |

### Message Browser

| Key | Action | Mode |
|-----|--------|------|
| `enter` | View message body | Any |
| `l` | Load / load more messages | Any |
| `x` | Delete message(s) | ReadWrite |
| `m` | DLQ actions (redrive) | ReadWrite |
| `y` | Copy message ID | Any |
| `space` | Multi-select | Any |
| `r` | Refresh (clear and re-peek) | Any |

## Mutations (ReadWrite mode)

### Send Message

Accessed via `m` → "Send Message" on the queue list. Opens a form with fields adapted per queue type:

- **Standard queues**: Message body (required), delay seconds (optional, 0-900)
- **FIFO queues**: Message body (required), message group ID (required, max 128 chars), deduplication ID (required or optional based on ContentBasedDeduplication setting)

Per-message delay is not available for FIFO queues (SQS API limitation).

### Purge Queue

Removes all messages. Triggers a targeted attribute refresh for the purged queue only (not a full list reload). Note: SQS enforces a 60-second cooldown between purge operations.

### Delete Queue

Deletes the queue and all its messages. Requires confirmation.

## LocalStack Notes

LocalStack may ignore `VisibilityTimeout=0` in `ReceiveMessage` requests and fall back to the queue's default visibility timeout. The seed program sets `VisibilityTimeout=0` on all seeded queues as a workaround. On real AWS, the per-request `VisibilityTimeout=0` works as documented.
