# Issue tracker: GitHub

Issues and specs for this repo live as GitHub issues in `jaishankarh/system-telemtery-oracle`.

Use the **REST API via `gh api`** for every operation. The `gh issue …` subcommands go through GraphQL, which is blocked in Claude Code cloud sessions; `gh api` works both there and locally. Locally you may use `gh issue …` if you prefer.

`R=repos/jaishankarh/system-telemtery-oracle`

## Conventions

- **Create an issue**: `gh api $R/issues -f title="..." -f body="$(cat <<'EOF' ... EOF)" -f 'labels[]=...'`
- **Read an issue**: `gh api $R/issues/<n> --jq '{number,title,body,state,labels:[.labels[].name]}'` and comments with `gh api $R/issues/<n>/comments --jq '[.[].body]'`
- **List issues**: `gh api "$R/issues?state=open&per_page=100" --jq '[.[] | select(.pull_request|not) | {number,title,labels:[.labels[].name]}]'`, adding `&labels=<label>` to filter
- **Comment on an issue**: `gh api $R/issues/<n>/comments -f body="..."`
- **Apply a label**: `gh api $R/issues/<n>/labels -f 'labels[]=<label>'`
- **Remove a label**: `gh api --method DELETE $R/issues/<n>/labels/<label>`
- **Close**: comment first, then `gh api --method PATCH $R/issues/<n> -f state=closed`

## Pull requests as a triage surface

**PRs as a request surface: no.** _(Set to `yes` if this repo treats external PRs as feature requests; `/triage` reads this flag.)_

When set to `yes`, PRs run through the same labels and states as issues: read with `gh api $R/pulls/<n>` and `gh api $R/pulls/<n>/files`; list external PRs with `gh api "$R/pulls?state=open"` keeping only `author_association` of `CONTRIBUTOR`, `FIRST_TIME_CONTRIBUTOR` or `NONE`. Labels and comments on a PR use the same `issues/<n>` endpoints as above.

GitHub shares one number space across issues and PRs, so a bare `#42` may be either: an issue response with a `pull_request` field is a PR.

## When a skill says "publish to the issue tracker"

Create a GitHub issue.

## When a skill says "fetch the relevant ticket"

Read the issue and its comments as above.

## Wayfinding operations

Used by `/wayfinder`. The **map** is a single issue with **child** issues as tickets.

- **Map**: a single issue labelled `wayfinder:map`, holding the Notes / Decisions-so-far / Fog body.
- **Child ticket**: an issue linked to the map as a GitHub sub-issue: `gh api --method POST $R/issues/<map>/sub_issues -F sub_issue_id=<child-db-id>`. Where sub-issues aren't available, add the child to a task list in the map body and put `Part of #<map>` at the top of the child body. Labels: `wayfinder:<type>` (`research`/`prototype`/`grilling`/`task`). Once claimed, the ticket is assigned to the driving dev.
- **Blocking**: GitHub's native issue dependencies: `gh api --method POST $R/issues/<child>/dependencies/blocked_by -F issue_id=<blocker-db-id>`, where `<blocker-db-id>` is the numeric database id (`gh api $R/issues/<n> --jq .id`, not the `#number` or `node_id`). `issue_dependencies_summary.blocked_by` counts open blockers. Where dependencies aren't available, fall back to a `Blocked by: #<n>, #<n>` line at the top of the child body. A ticket is unblocked when every blocker is closed.
- **Frontier query**: list the map's open children, drop any with an open blocker or an assignee; first in map order wins.
- **Claim**: `gh api $R/issues/<n>/assignees -f 'assignees[]=jaishankarh'`, the session's first write.
- **Resolve**: comment the answer, close the issue, then append a context pointer (gist + link) to the map's Decisions-so-far.
