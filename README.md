# argx

A terminal UI for Argo CD.

argx queries **every Argo CD you are logged into at once**, merged into one
list. It reuses whatever `argocd login` already established — the same
`~/.config/argocd/config` the CLI writes — and can take tokens from `pass` or
any other secret manager instead. The `argocd` binary is not invoked at runtime:
argx talks to the REST API directly.

```
argx                                    # every context with a credential
argx --context prod                     # just one
argx --context prod --context staging   # or a few
argx contexts                           # what argx can connect to, and how
argx list                               # plain listing, for pipes and scripts
argx list --json
```

## Several Argo CDs at once

Servers are queried concurrently and their applications merged into one list,
sorted by name so a fleet reads as one thing rather than as several
concatenated ones. A CONTEXT column names the server each row came from, and
each server gets its own color.

Everything is keyed on **context + name**, never name alone — two servers can
host an application called `web`, and a mark on one must never select the other.
The same goes for actions: each one resolves the client from the application's
own recorded origin rather than from an ambient "current server", so an action
cannot land on a cluster you were not looking at.

`ctx:` narrows the list to one server (`ctx:staging`, `context:argocd.example.com`,
`c:prod`), and ANDs with the rest of the query like every other term. Combined
with `a`, it is the safe way to act on exactly one Argo CD.

An action that spans servers says so before it is confirmed, with its targets
grouped by server:

```
Sync?

5 application(s) across 3 servers

This spans 3 Argo CD servers.

sb-prod  (2)
    web-frontend  →  cluster-sb-prod
    api-gateway  →  cluster-sb-prod
dl-prod  (2)
    ...
```

**Partial failure is partial**, not fatal: a server that is down or whose
session expired is named in the status line (`unreachable: dl-prod (E)`, and `E`
shows why) while the servers that answered are still listed. Only when nothing
answers does argx interrupt — an empty list would otherwise read as "you have no
applications". The header carries the same fact: `3 servers`, or `2/3 servers`.

## Credentials

By default argx uses whatever `argocd login` established for each context. When
a token should not sit in a file on disk — or should outlive the CLI's session —
name a source in `~/.config/argx/config.yaml`.

**A token is always fetched by running a command.** `pass`, `op`, `vault` and
the rest are named shorthands for a command line, not separate features, so a
secret manager argx has never heard of is one entry away.

```yaml
contexts:
  argocd.example.com:
    token: pass:infra/argocd/token     # a named source and its argument

  argocd-eu.example.com:
    token: op:op://vault/argocd/token

  argocd-staging.example.com:
    token: argocd                               # whatever `argocd login` set up

  staging:
    server: staging.example.com
    token:
      argv: [my-secret-tool, fetch, --path, argocd/staging]   # any command
      line: 2                                   # when the secret is not line one

# Optional: which contexts `argx` opens with no --context.
# Omit it and argx opens every context that has a credential.
default: [argocd.example.com, argocd-staging.example.com]
```

Built-in sources — `pass`, `op`, `gopass`, `bw`, `doppler`, `vault`, `keychain`,
`env`, `file`, `aws-ssm`, `gcloud-sm`, and `argocd` (the CLI's own session).
Each is just a command template, and each is reachable by the same code path;
none of them has special handling anywhere in argx.

Teach it a new one, or change how a built-in is called, by naming the command.
`{}` is where the context's argument lands, and may appear more than once:

```yaml
token_sources:
  mysecrets:
    argv: [my-tool, read, --vault, prod, --key, "{}"]
  pass:                                    # replaces the built-in
    argv: [pass, show, --clip=0, "{}"]

contexts:
  prod:
    token: {mysecrets: argocd/prod}
```

A configured source **wins over the CLI's session**, so a `pass` entry keeps
working after `argocd login` expires — and naming the `argocd` source says the
opposite, letting one context keep using the CLI's credential while its
neighbours use a secret manager.

Tokens are fetched once at startup, never at load: `argx contexts` shows where
each credential comes from without running a single secret command, so listing
them never triggers a GPG passphrase prompt. Fetches are sequential for the same
reason — several passphrase prompts racing for the terminal is unusable.

A context named only in argx's config is usable too, as long as it carries a
`server:` — useful for a server the CLI was never logged into.

```
$ argx contexts
CURRENT  NAME                  SERVER                CREDENTIAL
         argocd.example.com     argocd.example.com     pass:infra/argocd/token
         argocd-eu.example.com  argocd-eu.example.com  pass:infra/argocd-eu/token
*        argocd-staging.example.com     argocd-staging.example.com     pass:infra/argocd-staging/token
```

## Layout

A drill-down stack into an application, which then presents three tabs. An Argo
CD session has many applications but a human works on one at a time, and the
three questions you ask about that one application — what is running, what was
deployed before, how is it configured — want the full width each.

```
applications ─Enter→ application view
     │               [ 1 RESOURCES │ 2 HISTORY │ 3 DETAILS ]
     ├─d→ app diff        │             │            │
     └─o→ web UI          │             │            └→ change target revision,
                          │             │               toggle auto-sync / prune /
                          │             │               self-heal, terminate a sync
                          │             └→ past deployments, roll back
                          └→ resource tree → manifest, diff, pod logs
```

`[` / `]` (or `Tab`) cycle the tabs and `1` / `2` / `3` pick one. The
application name stays in the header across all three, and each tab keeps its
own cursor, so switching never loses your place. `Esc` leaves the application
entirely — tabs are not stack levels.

## Multi-select

`Space` marks the row under the cursor and advances, so marking a run is one key
repeated. `a` marks or clears every **filtered** row — with a filter active,
"all" never reaches applications you cannot see.

Actions apply to the marked set when anything is marked, and to the cursor row
otherwise, so `o`, `r`, and `s` behave the same whether or not you bothered to
mark anything. Marks are available on both the application list and the resource
tree; tree marks key on resource UID, so a recreated pod does not inherit the
mark of the one it replaced.

## Keys

Everywhere: `j` `k` `↑` `↓` move, `ctrl+d` / `ctrl+u` half page, `g` / `G` top
and bottom, `/` filter, `?` help.

**`q` and `ctrl+c` quit, from anywhere. `Esc` goes back one screen** — it is the
only key that unwinds, and it never quits. `ctrl+c` works even inside a modal or
mid-search, since it is the terminal's own interrupt; `q` is an ordinary letter,
so it stays typeable while the filter prompt is open.

| key | applications | RESOURCES | HISTORY | DETAILS |
|---|---|---|---|---|
| `Enter` | open the application | live manifest | roll back | change the `*` row |
| `Space` | mark and advance | mark and advance | — | — |
| `a` | mark / clear all filtered | mark / clear all filtered | — | — |
| `o` | **open in browser** | open the application in browser | ← | ← |
| `d` | diff (desired vs live) | diff of the marked resources | diff against live | — |
| `e` | events | — | — | events |
| `l` `L` | — | pod logs | — | — |
| `e` | — | a shell in the container | — | — |
| `n` `N` | next / previous match in a manifest or diff |||
| `M` | show managedFields and other bookkeeping |||
| `r` `R` | refresh / hard refresh | reload | ← | ← |
| `s` | sync | sync the marked resources | — | sync |
| `b` | — | — | roll back | — |
| `[` `]` `Tab` | — | previous / next tab | ← | ← |
| `1` `2` `3` | — | pick a tab | ← | ← |
| `A` | toggle 15s auto-refresh | — | — | — |
| `E` | show unreachable servers | — | — | — |
| `S` | application sets | — | — | — |
| `w` | — | sync windows | ← | ← |

In the log view `/` acts as a grep; in a manifest or diff it does more — see
[Searching a manifest or a diff](#searching-a-manifest-or-a-diff). `Esc` clears
a filter, `Enter` keeps it.

### Resource filter

The RESOURCES tab searches three axes independently — a Deployment named `web`
and a Pod named `web` are different questions:

```
web                       name contains web
kind:pod        k:pod     kind (prefix match; also matches the API group)
status:degraded s:degraded  health — status:none finds unchecked kinds
ns:prod         n:prod    namespace
label:app=web   l:app     a label
kind:pod status:degraded  terms are ANDed
```

Prefixes and values are case-insensitive, so `kind:statefulset` works. Labels
are available only for the kinds Argo CD reports networking for — Pods,
Services, Ingresses — so a label term excludes every other kind rather than
matching it vacuously, which is the reading that makes `l:app=web` mean what it
looks like.

## Actions

- **refresh** (`r`) and **hard refresh** (`R`) only re-read the source and
  recompute status, so they run without a prompt.
- **sync** (`s`) opens an options modal (`p` prune, `d` dry-run) and then a
  confirmation listing exactly what will be synced.
- **target revision** — `Enter` on the DETAILS row opens a picker of the
  repository's actual branches and tags, each labelled with its kind, filtered
  as you type. The confirmation shows the old and new revision, and warns when
  auto-sync is on, because that deploys the new revision immediately.
- **auto-sync / prune / self-heal** — `Enter` toggles. Turning auto-sync *on*
  and turning prune *on* confirm, since both can change a cluster on their own;
  turning either off does not, because making it slow to stop a cluster from
  changing helps nobody. Prune and self-heal are unavailable while auto-sync is
  off — they live inside the automated policy, and offering them would silently
  create one.
- **rollback** (`Enter` or `b` in HISTORY) confirms with the revision, the
  deploy time, and who triggered it. Argo CD refuses a rollback while auto-sync
  is on, and the prompt says so up front rather than letting you confirm just to
  receive an error.
- **terminate** (DETAILS) stops a running sync, and says that the application is
  left partially applied.

## Application sets

`S` toggles between the applications and the ApplicationSets that generate
them. They are peers, not a stack — the same key gets you back.

```
   NAME                                 CONTEXT              GENERATORS           PROJECT  APPS
▸✓ applicationset-addon-base            argocd.example.com   merge(clusters+git)  default  0
 ✗ broken-stacks                        argocd-eu.example…   git                  infra    0
```

This list exists because **a broken generator is invisible from the application
side** — the applications it would have produced simply do not exist, so no row
can be red. The only place that failure surfaces is the ApplicationSet's own
conditions, and `status:error` filters straight to it.

`gen:` filters by generator kind, including inside a nested one, so `gen:git`
finds a `merge(clusters+git)` too. `y` shows the full spec, `o` opens it in the
browser, and `enter` shows the applications it generated.

That last one only works when Argo CD recorded the membership, which is not
guaranteed: the controller's tracking label is absent when the template sets its
own labels, and `status.applicationStatus` is populated only under a
progressive-sync strategy. When neither is available argx says so rather than
showing an unfiltered list that would read as "it generated everything".

## Logs and shells

`l` reads a pod's logs, `e` opens a shell in it. A pod with more than one
container asks which — reading the wrong container's logs is a silent wrong
answer, not an error — and a pod with one does not, because that is not a
choice. The list shows each container's image beside its name, since two
containers called `app` and `sidecar` say little and their images say what they
are.

Init containers are listed for logs, which is what you want when a pod is stuck
in Init, but exec into a finished one is declined with the reason rather than
attempted.

The shell goes **through Argo CD**, not through kubectl: the session inherits
Argo CD's RBAC and lands in its audit log, and argx has no way to map a
destination cluster onto a kubeconfig context anyway. argx suspends while the
shell has the terminal, and reloads the resource tree when it exits.

## Sync windows

`w` from anywhere inside an application lists the schedules that allow or block
syncing — the maintenance windows defined on its AppProject.

```
   KIND   SCHEDULE          DURATION  ZONE            APPLIES TO
▸⟳ allow  1 15 * * *        5h        Asia/Seoul      web-prod*
   deny   3 0 * * *         24h       Asia/Seoul      other-prod*  (does not apply)
```

Only the windows that govern this application are listed — the project's others
govern other applications, and mixing them in makes the reader work out which
lines are about the thing they are looking at. Open windows lead. The verdict on
whether a sync can run right now comes from the server rather than being
recomputed, because the precedence between allow and deny windows is the
server's to define.

`o` opens the project's **windows tab**, which is where they are defined and
edited; `O` opens the application itself.

The selectors and time zone come from a second call, for the project's list —
the per-application payload omits them. These windows are edited by automation,
so a window can be present in one response and absent from the other; when that
happens argx marks the detail unknown rather than showing the default. An empty
selector set legitimately means "the whole project", and an unknown zone read as
UTC would put an Asia/Seoul schedule nine hours off.

A blocked sync is flagged in the status line on **every** tab, and summarized in
DETAILS beside the sync policy — pressing `s` and being rejected is a worse way
to find out. Windows are read-only here: they are defined per project, so a
change reaches every application in it at once, which belongs in the repository
that owns the project.

Spec changes go out as **merge patches**, not a whole-spec PUT: a PUT would
replace the spec with what argx modeled and silently drop every field it does
not know about — `ignoreDifferences`, `info`, `revisionHistoryLimit`,
per-source settings.

Multi-source applications are read-only for the revision edit: a merge patch
cannot address one element of the `sources` array, and guessing an index would
rewrite the wrong source. Resource deletion is absent entirely.

### Testing a branch

The DETAILS tab is enough for the whole loop: turn auto-sync off, point the
target revision at the PR branch (the picker lists it), sync, look at
RESOURCES, then point it back at `HEAD` and turn auto-sync on. argx does not
remember the original revision for you — the picker lists what the repo has, so
`HEAD` and the original branch are both one keystroke away.

## Browser

`o` opens the Argo CD web UI for the marked applications. The opener is
`$ARGX_BROWSER`, then `$BROWSER`, then `open` (macOS) or `xdg-open`. Past five
tabs it asks first — a stray `a` before `o` would otherwise open a hundred.

## Searching a manifest or a diff

`/` in a manifest or diff is not a grep. A line reading `"image": "nginx:1.25"`
says nothing about *which* container it belongs to, and the lines that would
have said are exactly what a grep removes. Each match is labelled with the JSON
path that reaches it and shown with the lines around it:

```
spec.containers[0].image
      "name": "fluent-bit",
      "image": "registry/fluent-bit:3.2.3",
      "imagePullPolicy": "IfNotPresent",
⋯
spec.containers[1].image
      "name": "app",
      "image": "registry/app:1.0.15",
```

`n` and `N` step between matches rather than through the context around them.
Overlapping context windows are merged, so no line is printed twice under a
label describing a different match.

**managedFields is hidden by default**, along with the annotation `kubectl
apply` writes. Measured on a real pod manifest, those are 39% of the document —
enough to bury every real match. `M` shows them, and the status line says how
many lines are hidden, so an absent field is never a mystery. The filter applies
to manifests and diffs only: a log line beginning with `managedFields` is
content, not a field.

## Diff

The diff compares Argo CD's **normalized** live state against the target state —
the same two documents the server itself compares — so argx does not report
drift that Argo CD's normalizations already ignore. Unchanged runs are elided
with `@@ ...` markers, and an oversized diff says so rather than silently
dropping lines.

### Application filter

An unprefixed term searches everything the row shows — name, context, project,
destination, status, revision, repo, path — so anything you can see, you can
find. Prefixes narrow to one axis:

```
web                        anything the row shows
label:env=prod  l:env      a label key and value, or just the key
-l:env                     applications *without* the label
ctx:staging     c:stg      the Argo CD server
proj:platform   ns:web     project, destination namespace
cluster:apne2              destination cluster
sync:outofsync  health:degraded
ctx:stg label:env=prod     terms are ANDed
```

Label keys match on their suffix, so `l:env` finds `example.com/env` without
typing the domain. **Tab completes** the word under the cursor — field names,
then the label keys, values, contexts, projects, namespaces and clusters the
loaded applications actually carry. One press advances as far as is
unambiguous; when that adds nothing, the choices are listed.

While the prompt is open the arrows split by axis: **← →** move the text cursor
inside the query, **↑ ↓** move through the rows it matched. `ctrl+a`/`ctrl+e`
jump to the ends, `ctrl+w` deletes a word, `alt+←`/`alt+→` move by word.

## Terminal

- Minimum 60×14; below that argx says the terminal is too small rather than
  rendering garbage. At 60 columns the project and destination columns are
  dropped, not squeezed — the CONTEXT column survives, because which server a
  row belongs to outranks its project.
- Column widths are sized from what the fields actually hold, measured across a
  real fleet of ~3000 applications: project reaches 12 characters, names 52,
  destinations 104. Project is therefore capped rather than given a proportional
  share of the row.
- Every rendered line fits the terminal exactly. Nothing wraps: a row that
  overruns is wrapped by the terminal, and the wrap shifts every column below
  it. Modals stay inside the frame too, eliding from the middle so their
  controls — "any key to dismiss" — are never the part that gets cut.
- Status is carried by a glyph as well as color, so the UI works in monochrome
  and under `NO_COLOR`. Editable rows in DETAILS carry a marker, so what argx
  can change is visible without moving the cursor over every row.
- Idle at 0 fps. Auto-refresh (`A`) is opt-in.

## Icons

Three glyph sets. **Unicode is the default** — box drawing and letters, legible
in any UTF-8 terminal without a patched font:

```
▸  SH web-frontend         argocd.example.com  default  prod-apne2/web  32b6f40 @main
   H └─ ReplicaSet web-6d9f
```

**Nerd Font** adds icons for Kubernetes kinds, sync and health states, git
branches and tags, clusters, and tabs. It is opt-in, because there is no
reliable way to ask a terminal whether its font has the private-use range, and
guessing wrong renders every icon as a tofu box:

```yaml
# ~/.config/argx/config.yaml
icons: nerd        # unicode (default) | nerd | ascii
```

`ARGX_ICONS=unicode` overrides the file, so one session on a terminal without
the font opts out without editing a config every other session shares.
`ARGX_ASCII=1` still selects the ASCII set.

**ASCII** avoids everything outside 7-bit ASCII, for SSH into minimal images and
terminals that mangle box drawing:

```
>  SH web-frontend
   H `- ReplicaSet web-6d9f
```

Every glyph in every set is exactly one cell. A two-cell icon would push the
column it sits in, and the layout is built on columns starting where the header
says they do — so the sets are interchangeable without the alignment moving.

## Build

```
make build     # ./bin/argx
make install   # ~/.local/bin/argx
make test
```
