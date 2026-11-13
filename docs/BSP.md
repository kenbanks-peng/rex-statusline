# Block Status Protocol (BSP), version 1

BSP supplies information Rex cannot reconstruct. Rex remains authoritative for
block identity, source shell PID, process membership, directory, and geometry.
OSC 7501 is a post-commit invalidation hint only: never JSON or rendered data.

## Channel and file

A channel is `ROOT/SERVER/SESSION/BLOCK/report.json`. Identity components are
literal IDs (including `block:`); 1–200 ASCII letters, digits, colon, underscore
or hyphen. No aliases, path separators, dots or guessed default identities.
The manager and publishers must select the same actual Rex server identity,
session and block. The CLI resolves identity; the store never queries Rex.
Use a private BSP root under the user's runtime/cache directory.
Directories are 0700; reports/locks are 0600, current
effective UID. Root and owned descendants cannot be symlinks. Ancestors of
Root are user-selected/trusted; use a stable absolute root.

One atomic record contains version, server/session/block, host owner PID,
128-bit random epoch, UTC update time, and report:

```json
{"version":1,"server":"server:example","session":"session:example","block":"block:example","owner":1234,"epoch":"0123456789abcdef0123456789abcdef","updated":"2026-11-12T00:00:00Z","report":{"kind":"context","source":"local","epoch":"0123456789abcdef0123456789abcdef","context":{"cwd":"/work","env":{"VIRTUAL_ENV":"/work/.venv"},"jobs":0,"exit_code":0,"duration_ms":12},"line":""}}
```

Maximum record size: 64 KiB. UTF-8 JSON, one complete object; unknown record
fields, trailing JSON, oversized/nonregular/insecure files are rejected.
Readers open owned paths using directory descriptors and O_NOFOLLOW, never
follow a report-supplied pathname. Writers serialize with flock, create a
0600 unique file in the same directory, write/fsync/close, rename and fsync the
directory. Only then may the CLI emit its OSC 7501 idle notification.
Notifications can coalesce or be lost; manager reconciliation must reread.
Readers do not create channels.

## Distinct payload forms

- `kind=context, source=local`: absolute cwd, environment map, job count,
  last exit code and command duration in milliseconds. No rendered line.
  Only the manager renders; the shell never runs OMP or shell segment scripts.
- `kind=line, source=remote|container`: explicitly rendered UTF-8 text and
  zero/absent context. An empty line is a valid pending/clear state. No
  local cwd/env is attached or substituted when remote status is unavailable.
- Local `kind=line` is also supported for explicit application integrations.
  The same source-shell ownership restrictions apply.

The executable, not shell string interpolation, serializes cwd/env. The CLI
collects only permitted environment names (an allowlist, not the full process
environment). Do not publish credentials. A missing field is unknown, not an
invitation to reconstruct guest context from the host. Readers must sanitize
line output to one line and supported SGR; BSP data is not terminal code.

## Ownership, freshness, handoff

`Store.Read(block, Process)` checks record identity, owner liveness and membership
in Rex's supplied process set. Local reports additionally require
owner == Rex source shell PID and Remote == false. Bridge owner must be a
different live **host** PID in the block's process chain, never a guest PID.
The launcher/CLI must select the real forwarding process, not a detached helper.

Local reports expire after 24 hours, refreshed on each shell prompt. There is
no timer daemon or mandatory heartbeat: an idle shell beyond this TTL loses
BSP-only context until its next prompt. Bridge reports expire after two minutes;
forward a fresh guest report at least every minute while connected. Do not
heartbeat by replaying a cached guest line: silence must become unavailable.
Dead owner and expired records wrap os.ErrNotExist. Invalid reports,
ownership failures and superseded tokens have distinct sentinel errors.
A future timestamp over one minute ahead is invalid.

Bridge launch lifecycle:

1. Keep a local supervisor in the source block's process tree. Call
   `Store.Claim(block, hostOwner, "remote"|"container")` once at launch.
   This commits an empty line and returns a fresh random epoch.
2. Keep that token on the host; expose only a dedicated per-launch data
   transport/inbox to the guest, never the host BSP root or Rex socket.
3. Forward each newly received guest line as an explicit line Report using
   `Store.Publish`, retaining its host owner/source and `Report.Epoch`.
   Context reports from guests are not accepted. The guest cannot choose
   identity, host owner or epoch. Ordinary terminal output is not this channel.
4. On exit call `Store.Release(block, hostOwner, epoch)`. This removes only
   the matching record. It never restores an old report. The next local
   prompt publishes current context.
5. A new launch always claims a new epoch. Updates and releases from an old
   bridge cannot overwrite/delete its successor. A local publish cannot
   replace a live bridge. Claims are launcher-only operations, not retries
   performed by update workers.

PID liveness is a bounded freshness check, not process-start-time attestation;
PID reuse is not cryptographically excluded. Rex membership/source-shell
validation, short bridge TTL and fresh launch epochs bound accidental reuse.
BSP isolates accidental cross-channel/late-writer data; it is not a security
boundary against a malicious process with the same UID and filesystem access.

Files need not be deleted to invalidate them. The manager may remove whole
channels only after Rex confirms their blocks closed and publishers stopped.
Do not unlink the per-channel .lock during use (that breaks serialization).
A stale reader must never delete a newer record. No automatic sweeping of
unrelated server/session roots occurs. Closed source frames are removed from the
manager runtime directory; BSP records become invalid immediately when their
owner disappears from Rex, even if their private channel directory remains.

## Public Go API and CLI integration

Package `internal/bsp` provides Context, Report, Process and Store
types, `Read`, `Publish`, and epoch-based bridge handoff:

```go
func (s Store) Claim(block string, owner int, source string) (string, error)
func (s Store) Release(block string, owner int, epoch string) error
```

Publish automatically fills version, identity, timestamp and source. Local
publish creates a fresh epoch; remote/container publish requires the current
claimed epoch. Publish does not validate Rex process membership itself:
the CLI obtains/verifies owner identity and Read independently enforces it.

Integration shell files expect these CLI operations:
- `publish --source local --owner PID --cwd PATH --jobs N --exit-code N --duration-ms N`
  constructs context and JSON-escapes the permitted process environment.
- `claim --source container --owner PID` prints only the token.
- `publish --kind line --source container --owner PID --epoch TOKEN` reads the
  rendered line from stdin (not JSON). The executable constructs the Report.
- `release --source container --owner PID --epoch TOKEN` releases it.
- Use source `remote` for remote-host bridges. Flags selecting host identity,
  source, owner and epoch must override/reject guest-provided values.
- Successful publish/claim/release notify only after commit. Claim stdout is
  machine-readable; notifications belong on the source terminal, not stdout
  captured as a token.

The native `bridge --inbox DIRECTORY --owner PID --source container` supervisor
handles the claim/update/release lifecycle for launchers. The inbox must be a
host-owned 0700 directory, unique per launch. Its report.json is a bounded,
regular nonsymlink file containing:

```json
{"version":1,"kind":"line","line":"rendered guest line","updated":"2026-11-12T00:00:00Z"}
```

Guest updates must be atomically renamed into place. The bridge rejects unknown
fields, stale/future timestamps, and oversized records; it forwards each changed
valid payload once and never heartbeats a cached line. It revalidates host owner
membership every two seconds. The host receipt expires after two minutes
without a newly received valid guest report. Guest timestamps are independently
bounded to two minutes old at receipt. On termination the bridge releases only
its own epoch.

Use `eval "$(rex-statusline init zsh)"` in `.zshrc`, or
`eval "$(rex-statusline init bash)"` in `.bashrc` (Bash 4.4+).
The binary embeds both hooks and binds publication to its
own executable path. Initialization is idempotent. Bash uses PS0 and
PROMPT_COMMAND without changing DEBUG traps; zsh uses preexec/precmd hooks.
Bash 4.4 tracks duration in whole seconds; Bash 5+ and zsh use subsecond clocks.
`integrations/bsp-bridge.zsh` supplies host launcher helpers. It is not a
complete SSH/container transport: launchers must retain the stable owner,
isolate a fresh per-launch inbox, validate incoming lines and forward them.
Launchers and guest publishers must use the same inbox record format.
Install `rex-statusline` on PATH or set `REX_STATUSLINE_EXECUTABLE` for launchers.

`python3 tests/launcher.py` verifies the Linux launcher, native bridge, and guest
publisher with a fake container engine. Set `LINUX_LAUNCHER` and
`GUEST_PUBLISHER` to select their executable/script paths. A real container
round trip remains unverified.
