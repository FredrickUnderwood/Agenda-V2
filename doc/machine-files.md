# Files: delivering a credential to a machine

Some applications need a file on disk that cannot live in the repository or in
an environment variable — a signing key, a TLS client certificate, a
service-account JSON. Traditionally someone SSHes in and drops it somewhere, and
from then on nobody is quite sure whether it is still there.

The **Files** tab on an application does that delivery, and keeps a record of
what was delivered so the platform can answer the follow-up question: *is it
still there, and is it still the same file?*

## Two scopes

| | Environment files | Machine upload |
|---|---|---|
| Where | `<workspace_root>/run/<app>/<env>/.files/<name>` on every machine in the environment | any path under that machine's workspace root |
| Mounted into containers | yes, read-only at `/agenda/files` | no |
| Who can | anyone who can edit the app's environment | admin only |
| Use it for | credentials and config an app reads at startup | one-off files unrelated to an app |

Environment files are the scope you almost always want. The path is computed by
the platform, so no caller chooses where the bytes land, and one upload reaches
every machine running that environment — including both halves of a blue/green
pair, which must read the same key.

The directory lives under `run/`, outside the code checkout, for the same reason
instance logs do: a re-clone (including the `rm -rf` fallback in `git.Pull`)
must not be able to delete it.

## How an app reads the file

The deploy pipeline bind-mounts the environment's file directory read-only at
`/agenda/files` in every service it augments, and injects `AGENDA_FILES_DIR`
pointing at it. So an app whose compose file used to carry a hand-provisioned
mount of its own,

```yaml
volumes:
  - /etc/myapp/keys:/etc/myapp/keys:ro    # placed by hand, on every machine
```

drops that mount and points its config at `/agenda/files/<name>` instead.

Read-only is deliberate: the platform is the only writer. A container that
rewrote a file here would make every subsequent verification report tampering.

**Uploads take effect when the instance next starts.** Most applications read a
credential once, at construction. Nothing reloads it under a running process.

## What is stored, and what is not

The file's **contents are never stored by the control plane** — only its path,
size, SHA-256, permissions, and who uploaded it when. A copy of every production
credential in the platform database would be a bigger liability than the
convenience is worth.

The cost of that choice is real and worth stating: **the platform cannot re-send
a file it does not have.** Add a machine to an environment after an upload, and
that machine has no copy. Nothing repairs this automatically. Two mechanisms
exist to keep it from being silent:

- **A deploy-time check.** At the top of `compose_up`, before anything starts,
  the pipeline lists every file the environment has ever been given and reports
  whether each is present on *this* machine with the recorded checksum, as
  `file ok` / `file MISSING` / `file CHANGED` lines in the deploy log. It never
  fails the deploy — the platform cannot fix the gap, and blocking a release on
  it would trade a silent failure for a stuck pipeline — but the gap is named at
  the moment it starts to matter.
- **A background verification pass**, every 15 minutes, over every file that is
  supposed to be on a machine right now. The console's per-row **Check** button
  is the same thing on demand.

## Reading a verification result

| State | Meaning |
|---|---|
| **OK** | Present, and its contents still hash to what was uploaded. |
| **Changed** | Still there, contents differ. Someone edited or replaced it outside agenda. |
| **Missing** | The machine answered, and the file is not at that path. Containers using it will start without it. |
| **Unknown** | The machine could not be reached. This says nothing about the file. |

**Unknown is not Missing.** Collapsing the two would turn every node restart
into a false alarm about a missing credential, and people would learn to ignore
the alarm that matters.

Uploads append rows rather than replacing them, so the tab is a history: who
rotated a credential and when. Only the newest row per (machine, path) describes
what is on disk; older ones are marked *superseded* and are not re-verified.

## How the bytes travel

```
browser ──JWT──▶ control plane ──node token──▶ agenda-node ──▶ file on disk
                               ──ssh────────▶ cat > tmp && mv
```

Uploads go through the same `Runner` abstraction deploys use, so both agent-mode
and SSH machines work. In both cases the bytes land in a temporary file beside
the destination and are renamed into place only once fully written: an app
reading a credential at startup must never observe a half-written one.

The SHA-256 recorded is always the one computed by the machine that now holds
the file, never by the uploader. That is what makes a later check a comparison
of two readings of the same disk.

## Why uploads are confined to the workspace root

Both scopes write inside the machine's workspace root — its own `workspace_root`
when set, otherwise the global one — and a machine upload naming a path outside
it is refused.

This is not tidiness. `agenda-node` commonly runs in a container with only the
workspace root bind-mounted from the host, so a write anywhere else lands in the
node's own container filesystem. That upload *succeeds*: the file is really
written, a verification really finds it, and the checksum really matches — right
up until the node restarts and it is gone, having never been visible to the host
or to any app container. A file that appears to exist and does not is worse than
a refusal, so the console presents the root as a fixed prefix and the control
plane rejects anything outside it.

## Locking the node down

`agenda-node`'s file endpoints are guarded by the same per-machine token as
`/v1/jobs`. That token already grants arbitrary command execution, so file
upload adds no privilege to whoever holds it — but set `file_roots` in
`agenda-node.yaml` anyway:

```yaml
file_roots:
  - "/root/.agenda-v2/workspaces"
max_upload_bytes: 268435456
```

It confines uploads to the tree agenda owns, which is protection against a
control-plane bug rather than against a token thief. The node enforces it
itself, including resolving symlinks, because a rule checked only by the caller
is not a rule.

**A containerized node writes inside its own container.** `install-node.sh` runs
the node in Docker with only the workspace root bind-mounted from the host, so a
path outside it lands in the container's filesystem and disappears on restart.
Keep `file_roots` inside the mounted workspace root — which environment files
already are.
