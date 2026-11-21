# CloudWatch Logs

Browse CloudWatch Logs log groups, their log streams, and their events. Every call reads, so ReadOnly mode allows all of them. The design is in `docs/design/cwlogs.md`.

Live tail comes in a later PR of that design.

## Views

### Log Groups

Open the list from the CloudWatch tile on the home screen, or with `:logs` (alias `:log`).

| Column | Description |
|--------|-------------|
| Name | The name of the log group |
| Class | Standard, Infrequent or Delivery |
| Retention | The days that AWS keeps the events, or `never` when the events never expire |
| Stored | The stored size |
| Created | The creation date |

A narrow terminal shows only the name and the retention. The retention and the size sort as numbers, and `never` sorts after every number of days.

The list loads at most 20 pages of 50 groups (1,000 groups). When more groups exist, the footer shows `l for more`, and `l` loads the next 1,000. If a later page fails, for example because AWS limits the request rate, the loaded rows stay. The error shows in the status bar, and `l` asks for that page again.

| Key | Action |
|-----|--------|
| `enter` | Open the events of the whole group |
| `v` | Open the streams of the group |
| `d` | Open the detail of the group |
| `y` | Copy the name of the group |
| `l` | Load more groups |
| `/` | Filter the loaded groups |
| `s`, `S` | Sort |
| `r` | Load the list again from the first page |

### Log Group Detail (side panel)

`d` opens a panel with two tabs:

1. Info: the name, the ARN, the class, the retention, the stored size, the creation time, the number of metric filters, the KMS key, the data protection status and the deletion protection.
2. Tags: the tags of the group, from `ListTagsForResource`.

Many read-only roles allow `logs:Describe*` but not `logs:ListTagsForResource`. In that case, the Tags tab shows the AWS error, and the panel still opens.

### Log Streams

The list shows the streams of one group. The stream with the newest event is first.

| Column | Description |
|--------|-------------|
| Name | The name of the stream |
| Last event | The time of the newest event |
| First event | The time of the oldest event |
| Created | The creation time |

AWS updates the last event time late, usually within an hour. So a stream that got events a few minutes ago can show an older time.

The list loads at most 4 pages of 50 streams (200 streams), and `l` loads the next 200. AWS does not take a name prefix together with the order by the last event, so the filter `/` acts only on the loaded streams. While more streams exist, the footer says so.

| Key | Action |
|-----|--------|
| `enter` | Open the events of the stream |
| `d` | Open the detail of the stream: the name, the ARN, the times and the last ingestion time |
| `y` | Copy the name of the stream |
| `l` | Load more streams |
| `/` | Filter the loaded streams |
| `s`, `S` | Sort |
| `r` | Load the list again from the first page |

### Log Events

The viewer shows the events of a whole group, or of one stream. Open it with enter on a group or a stream, or with `:logs NAME` for a group. With `:logs NAME`, the viewer first looks for a group with exactly that name. When no group has it, the viewer says "No log group named NAME".

The viewer shows the newest hour first, in local time. The header shows the range, the time zone and the filter pattern, for example `last 1h · UTC-05:00 · filter: ERROR`. Each line shows the time, the stream (for a group, on a terminal of 70 columns or more) and the first line of the message.

`l` loads older events, at most 1,000 events or 10 pages for each press. When the range has no more events, `l` moves the range back by its length. The walk stops at the oldest time that can hold events, and the footer then says "start of group" or "start of stream". For a group, that time is the latest of these:

- 14 days before the group was made. `PutLogEvents` takes events up to 14 days old.
- Now minus the retention of the group.
- 2024-01-01 UTC. AWS reads newest events first only from that date.

For a stream, the walk also stops at the first event of the stream.

The footer shows the count and the loaded time span, for example `1000 events · 14:02 to 15:02 · l for older`. The viewer holds at most 10,000 lines and 32 MB. When it is full, the footer says so, and `l` stops.

| Key | Action |
|-----|--------|
| `l` | Load older events |
| `f` | Set a filter pattern. AWS applies it, and the viewer loads again. An empty pattern clears it. |
| `/` | Filter the loaded lines. This sends no AWS call. |
| `t` | Pick the range: 5m, 15m, 1h, 3h, 12h, 1d, 3d, 7d, or custom |
| `r` | Load the range again |
| `enter` | Open the event in the side panel: the message (JSON shows formatted), the time, the ingestion time, the stream and the event ID |
| `y` | Copy the message of the line, or of the selected lines |
| `V` | Select lines |
| `w` | Wrap long lines |
| `G` | Go to the end and follow it |
| `j`, `k`, `ctrl+d`, `ctrl+u`, `g` | Move |

A custom range asks for a start time and an end time. Write a time in local time, for example `2026-11-20 14:00`, or in RFC 3339 form with `Z` or an offset. An empty end time is now.

If AWS rejects a filter pattern, the status bar shows the AWS error, and the old lines and the old pattern stay. If AWS limits the request rate, the lines stay, and `l` tries the same page again. A page token expires after 24 hours. When a token is more than 23 hours old, `l` starts a new query at the oldest loaded event.

The side panel opens without the focus, so `l`, `f` and the other keys still go to the viewer. Press `tab` to move the focus to the panel. A message over 64 KB shows cut in the panel. Press `y` in the viewer to copy all of it.

## API calls

| Call | Used for |
|------|----------|
| `DescribeLogGroups` | The group list, 50 groups for each page |
| `ListTagsForResource` | The Tags tab of the group detail. It gets the ARN of the group without `:*`. |
| `DescribeLogStreams` | The stream list, ordered by the last event time, 50 streams for each page |
| `DescribeLogGroups` with a prefix | `:logs NAME`, to find the group with exactly that name |
| `FilterLogEvents` | The events of a group, and of a stream with a filter pattern, newest first |
| `GetLogEvents` | The events of a stream, newest first |

The page tokens of CloudWatch Logs expire after 24 hours. In the group and stream lists, when a kept token is more than 23 hours old, `l` does not send it, and the status bar says to press `r`. The event viewer starts a new query instead (see Log Events).

## IAM permissions

- `logs:DescribeLogGroups`
- `logs:DescribeLogStreams`
- `logs:FilterLogEvents`
- `logs:GetLogEvents`
- `logs:ListTagsForResource` (optional, for the Tags tab)
