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

The count columns sort as numbers. When LazyCloud cannot read the settings of a queue, for example because access is denied, the queue shows `?` in the count columns and `unknown` as its type. A toast names these queues with the AWS error code, and the event log has the full error.

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

SQS has no read-only peek. A peek is a real `ReceiveMessage` call. Right after the receive, LazyCloud calls `ChangeMessageVisibilityBatch` with a visibility timeout of 0, so the messages are visible to other consumers again at once. SQS still adds 1 to the `ApproximateReceiveCount` of each message, and nothing can undo that.

On a queue with a redrive policy, enough receives move a message to the dead-letter queue (DLQ) without processing. For this reason, the peek rules depend on the mode:

- In ReadOnly mode, `l` and `r` peek only when the queue settings are loaded and show no redrive policy. On a queue with a DLQ, or when the settings cannot be read, the peek is blocked. Press `W` to switch to ReadWrite mode.
- In ReadWrite mode, a peek is always allowed. After the peek, a warning shows the number of messages that reached `maxReceiveCount`. The next receive by a consumer moves these messages to the DLQ.
- If the visibility reset fails, for example because the role has no `sqs:ChangeMessageVisibility` permission, the messages are still shown. A warning says how long they stay hidden from consumers.

A banner above the table always describes what a peek does to the current queue.

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
| `l` | Load / load more messages | Any (ReadOnly: only queues without a DLQ) |
| `x` | Delete message(s) | ReadWrite |
| `m` | DLQ actions (redrive) | ReadWrite |
| `y` | Copy message ID | Any |
| `space` | Multi-select | Any |
| `r` | Refresh (clear and re-peek) | Any (ReadOnly: only queues without a DLQ) |

## Mutations (ReadWrite mode)

The actions in the `m` menu of the queue list apply to the selected queues, or to the queue under the cursor when no queue is selected.

### Send Message

Accessed via `m` → "Send Message" on the queue list. The menu offers it only when the action applies to one queue: one selected queue, or the cursor queue. Opens a form with fields adapted per queue type:

- **Standard queues**: Message body (required), delay seconds (optional, 0-900)
- **FIFO queues**: Message body (required), message group ID (required, max 128 chars), deduplication ID (required or optional based on ContentBasedDeduplication setting)

Per-message delay is not available for FIFO queues (SQS API limitation).

### Purge Queue

Removes all messages. Triggers a targeted attribute refresh for the purged queues only (not a full list reload). Note: SQS enforces a 60-second cooldown between purge operations.

### Delete Queue

Deletes the queue and all its messages. Requires confirmation.

### Actions on Several Queues

Purge and delete try each queue, also after one fails. Only the queues that changed are refreshed or removed. The toast says how many succeeded and names the failed queues with their AWS error codes, for example "Deleted 2 of 3 queue(s). Failed: orders (AccessDenied)".

### Delete Messages

`x` in the message view deletes the selected messages, or the message under the cursor. The rows stay until SQS answers. Then only the deleted messages leave the list. A message that SQS did not delete stays, and the toast names it with the error code.

SQS deletes a message with the receipt handle of a receive. A peek is a receive, and the handle of a message changes each time that a consumer receives it:

- On a FIFO queue, SQS rejects an old handle with `ReceiptHandleIsInvalid`. The message stays in the list.
- On a standard queue, SQS accepts the delete of an old handle, but the message stays in the queue. SQS gives no way to find out. For this reason, the confirm prompt says so, and the toast says "SQS accepted the delete of N message(s)". To delete with fresh handles, press `r` to peek again first.

### Redrive

`m` in the message view starts a redrive, which moves the messages of a dead-letter queue back to their source queues. It is available when another queue sends its failed messages to this queue, as `ListDeadLetterSourceQueues` reports. A `RedriveAllowPolicy` is not necessary.

A redrive moves every message of the queue, so it asks first with a y/n prompt. Press `y` to start it. `n`, `enter`, or `esc` cancels.

## LocalStack Notes

The seed program creates queues with the default visibility timeout of 30 seconds, so local queues behave like real ones. Older versions of the seed program set the queue default to 0. If your LocalStack data comes from such a version, wipe it and seed again.

A redrive of the seeded `order-processing-dlq` fails in LocalStack with the reason `AttributeError`, and no message moves. The seed program sends those messages straight to the DLQ, so SQS has no source queue for them. A message that reaches a DLQ through a redrive policy goes back to its source queue without a problem. To test a redrive locally, create a queue with a redrive policy and a `maxReceiveCount` of 1, send messages to it, and receive them until SQS moves them to the DLQ.

Do not set `VisibilityTimeout` to 0 in `ReceiveMessageInput` and expect it to work. The AWS SDK for Go omits fields that have the value 0, so the queue default applies. LazyCloud resets the visibility with `ChangeMessageVisibilityBatch`, where the SDK always sends the value.
