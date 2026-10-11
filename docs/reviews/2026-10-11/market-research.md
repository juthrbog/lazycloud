# Market research, 2026-10-11

This file compares LazyCloud with other terminal UIs (TUIs) for AWS and gives a priority order for the roadmap. Star counts and dates come from the GitHub API on 2026-10-11. The research used five web searches. Claims that nobody tested are marked.

## Other tools

| Tool | Language | Scope | Stars and activity | Notes |
|---|---|---|---|---|
| taws | Rust | 30 to 60 services. Its README gives two different counts. | 2,264. Created January 2026. Pushed on 2026-10-11. Latest release v1.3.0-rc.8 (April). | SSO, `aws login`, role chaining, `credential_process`. Read-only mode is opt-in (`--readonly`). Server-side tag filters, pagination with `[` and `]`, SSM connect, endpoint override. The detail view is mostly raw JSON or YAML. S3 shows buckets only. |
| claws | Go, same Charm v2 stack | 70 services, 175 resource types | 161. Created December 2025. v0.18.0 in July 2026. | Parallel queries across accounts and regions. SSO and `aws login` from the profile picker. Side-by-side diff. Bedrock AI chat. 6 themes. Read-only mode is opt-in (`--read-only`). This is the closest competitor. Features come from its README and were not tested. |
| e1s | Go | ECS only | 939. v2.0.0 in September 2026. | ECS Exec and SSM, logs, profile and region switch. |
| stu, ddv | Rust | S3 only, DynamoDB only | 918 and 103. Last push April 2026. | Deep viewers for one service. |
| neboto-tui | Rust | Read-only, all of AWS behind one fuzzy search | 18. Created September 2026. | Detail panes, links across services, live logs, CloudWatch charts. Its pitch overlaps with LazyCloud. |
| sacha, fuziontech/lazyaws, bporter816/aws-tui, a9s | Go | Small | 1 to 24 | Hobby projects. |
| k9s | Go | Kubernetes | 34,800 | The UX model: `:` command, read-only setting, skins. |
| steampipe, cloudquery | Go | Query AWS with SQL | 8,000 and 6,500 | Inventory and queries, not interactive browsing. |
| granted, aws-vault, aws-sso-cli | Go | Credential helpers | 1,800, 9,000, and 664 | The last 99designs aws-vault release was in 2023. The active fork is ByteNess (460 stars, v7.16.2 in October 2026). |
| LazyCloud | Go | 3 services | 0. Last push 2026-04-16. v0.4.0 on 2026-04-07. | |

## What users ask for

The research found no Reddit or Hacker News threads. The evidence comes from the issues with the most reactions in the taws and e1s trackers.

- Credentials are the largest category. taws issues: #14 SSO (the top issue), #13 `role_arn`, #34 `credential_process`, #15, #70, #85 and #89 (SSO cache), #103 `AWS_CONFIG_FILE`, #66 (corporate CA bundle).
- Safety: taws #16 (read-only mode), #73 ("paranoid mode"), #74 ("`s` should require confirmation").
- Large accounts: e1s capped lists at 10 or 100 items (#37, #118, #226). taws did not list all ECS services (#39).
- Basic UX: sorting (taws #111, and e1s #387, the top e1s issue), tag search, S3 object management (taws #110 is open), return to the app after an SSM session (#105), and logs.
- The AWS Console supports 5 signed-in sessions at the same time since January 2025. `aws login` gives the CLI rotating credentials from a console sign-in. A TUI needs to support `login_session` profiles.

## Charm v2 status

bubbletea, lipgloss, and bubbles reached stable v2 on 2026-02-24. The latest versions are bubbletea v2.1.0 (2026-10-08), lipgloss v2.0.6, and bubbles v2.2.1. LazyCloud uses bubbletea v2.0.2, lipgloss v2.0.2, and bubbles v2.1.0.

The newer versions fix these problems:

- A deadlock in the shutdown signal goroutine (bubbletea v2.1.0).
- No fallback for a terminal size of 0x0 (bubbletea v2.1.0).
- A tab-stop and signal leak (bubbletea v2.0.3).
- Emoji rendering (v2.0.8, from a secondary source).

bubbles v2.2.0 adds a tree component, which fits the planned service navigation tree. `teatest/v2` is still an `x/exp` pseudo-version.

## Where LazyCloud leads

1. ReadOnly is the default. taws and claws offer read-only mode only as a flag, and their users asked for it.
2. Each service goes deeper. S3 has versions, presigned URLs, and copy and move. SQS has peek, redrive, and purge. taws shows only S3 buckets.
3. The side panel has tabs and links between resources. Lists have sorting and multi-select with bulk actions.

The safety lead depends on the fixes in the review tracker, especially C2, C3, C4, H1, and H2.

## Where LazyCloud is behind

- Breadth: 3 services against 30 to 70.
- No CloudWatch Logs. Every competitor has logs.
- Credentials: no SSO and no handling of expired tokens. The profile picker lists names only.
- No view across accounts or regions.
- No Homebrew or Scoop package. Issue #41 is still research.
- The README demo GIF is commented out.
- The Charm libraries are out of date.
- The Charm v2 stack is not a lead, because claws uses the same stack.

## Roadmap priority

First, fix the safety findings in the review tracker. Then work on the existing issues in this order:

1. CloudWatch Logs (#119), then live tail (#120). Every competitor has logs. Live tail shows the strength of a TUI. Logs also support Lambda and ECS.
2. Lambda (#122 and #124). It is the most used serverless service, and it links to logs.
3. ECS (#129), then exec (#130). e1s has 939 stars for ECS alone.
4. Secrets Manager and Parameter Store (#125). The API is small, masked values fit the ReadOnly story, and users look up secrets every day.
5. CloudFormation (#138). Stack events explain failed deploys, and stack resources can link to the services that LazyCloud has.

After these, do DynamoDB (#131), IAM (#127), and ELB (#137).

Do these later:

- The 11 EC2 networking issues (#173 to #183). They add depth where LazyCloud already leads.
- The Cloud Pods and seed issues (#148, #150, #153, #196).
- Harmonica (#56).

Do these sooner: Homebrew (#41), and the demo GIF in the README.

## Ideas that are not on the roadmap

These ideas are in order of value:

1. A profile picker that knows SSO. It finds expired tokens and runs `aws sso login` or `aws login`. It supports `login_session` and `credential_process`. This is the largest pain point in competitor trackers. Do it before any new service.
2. An account banner and safety rules per profile. Show the account ID, alias, and role from STS. Lock production profiles to ReadOnly, as k9s does. This builds on the `W` mode switch.
3. Jump to a resource. The user pastes an ID, an ARN, or a name and goes to the resource. This is the main pitch of neboto. Add tag filters with Resource Explorer or the Resource Groups Tagging API.
4. A CloudTrail "who changed this" tab in the detail panel. The research did not find out if claws has this.
5. A view across regions, then across accounts, with parallel queries as in claws. Start with EC2 in all regions.
6. No list stops at a page size without a warning. This is the e1s bug class.
7. Cost hints. claws uses Cost Explorer, Budgets, and Compute Optimizer. This has a lower priority.

## Sources

- https://github.com/huseyinbabal/taws
- https://github.com/huseyinbabal/taws/issues?q=sort%3Areactions-%2B1-desc
- https://github.com/clawscli/claws
- https://www.linuxlinks.com/claws-tui-aws-resource-management/
- https://github.com/keidarcy/e1s
- https://github.com/lusingander/stu
- https://github.com/lusingander/ddv
- https://github.com/neboto/neboto-tui
- https://github.com/Sachamama/sacha
- https://github.com/derailed/k9s
- https://github.com/turbot/steampipe
- https://github.com/cloudquery/cloudquery
- https://github.com/fwdcloudsec/granted
- https://github.com/99designs/aws-vault
- https://github.com/ByteNess/aws-vault
- https://github.com/synfinatic/aws-sso-cli
- https://github.com/charmbracelet/bubbletea/releases
- https://github.com/charmbracelet/bubbles/releases/tag/v2.2.0
- https://raw.githubusercontent.com/charmbracelet/bubbles/main/UPGRADE_GUIDE_V2.md
- https://www.mintlify.com/charmbracelet/bubbletea/migration/v2-upgrade-guide
- https://aws.amazon.com/blogs/security/simplified-developer-access-to-aws-with-aws-login
- https://aws.amazon.com/about-aws/whats-new/2025/01/aws-management-console-simultaneous-sign-in-multiple-accounts
