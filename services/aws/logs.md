# CloudWatch Logs

Browse CloudWatch Logs log groups and their log streams. Every call reads, so ReadOnly mode allows all of them. The design is in `docs/design/cwlogs.md`.

The event viewer and live tail come in later PRs of that design. Until then, enter on a log group opens its streams.

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
| `enter`, `v` | Open the streams of the group |
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
| `enter`, `d` | Open the detail of the stream: the name, the ARN, the times and the last ingestion time |
| `y` | Copy the name of the stream |
| `l` | Load more streams |
| `/` | Filter the loaded streams |
| `s`, `S` | Sort |
| `r` | Load the list again from the first page |

## API calls

| Call | Used for |
|------|----------|
| `DescribeLogGroups` | The group list, 50 groups for each page |
| `ListTagsForResource` | The Tags tab of the group detail. It gets the ARN of the group without `:*`. |
| `DescribeLogStreams` | The stream list, ordered by the last event time, 50 streams for each page |

The page tokens of CloudWatch Logs expire after 24 hours. When a kept token is more than 23 hours old, `l` does not send it, and the status bar says to press `r`.

## IAM permissions

- `logs:DescribeLogGroups`
- `logs:DescribeLogStreams`
- `logs:ListTagsForResource` (optional, for the Tags tab)
