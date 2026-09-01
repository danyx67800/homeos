# HomeOS — Phase 4: Packaging, Updates and Deployment

Phases 1–3 produced an appliance that installs, runs and has a dashboard. Phase
4 is what makes it maintainable: a way to ship a new version to a box in
someone's cupboard without breaking it, a containerised stack for development,
and documentation someone else can follow.

---

## 1. The update model

Everything follows from one decision: **program code lives in versioned release
directories, and `current` is a symlink.**

```
/usr/lib/homeos/
├── releases/
│   ├── 1.0.0/{bin,scripts,web}
│   └── 1.1.0/{bin,scripts,web}
├── current  -> releases/1.1.0     ← swapping this is the update
├── previous -> releases/1.0.0     ← swapping it back is the rollback
├── bin      -> current/bin        ← so every documented path still resolves
└── scripts  -> current/scripts
/opt/homeos/web -> /usr/lib/homeos/current/web
```

Replacing a symlink by `rename(2)` is atomic on POSIX. There is no moment when
the appliance is half-updated, and rolling back is one more rename rather than a
restore from backup.

The alternative — unpacking a new version over the running installation — fails
badly in exactly the situation updates matter most: a power cut or a full disk
halfway through leaves a box that runs neither the old version nor the new one.
Here, a download that dies leaves a `.staging` directory nobody is using.

`bin` and `scripts` being symlinks into `current` is what lets phase 1's paths,
the systemd units and `make install` all keep working unchanged.

### The sequence

Staging and applying are separate, and only the second one is dangerous:

| Step | Where | Reversible? |
|---|---|---|
| fetch channel, pick release | daemon | nothing written |
| download, SHA-256, **verify signature** | daemon | nothing written |
| unpack to `<version>.staging` | daemon | delete the directory |
| run the new binary's `-version` | daemon | delete the directory |
| rename staging → `releases/<version>` | daemon | delete the directory |
| **rename `current` symlink** | helper | rename it back |
| restart, health-check 90s | helper | rollback restores the old release |

The applier is a separate process because a daemon cannot supervise its own
replacement. It is started as `homeos-update-apply.service` — and because phase
1's sudoers already permits `systemctl start homeos-*`, over-the-air updates
needed **no new privilege at all**.

The version to apply travels through `/var/lib/homeos/updates/pending` rather
than argv, because a unit started by name carries no arguments. That file is
written by the unprivileged daemon and read by root, so it is treated as
untrusted: the value is validated against `^[A-Za-z0-9._-]+$`, checked for
`..`, and only then used as a path component. Eleven malformed and hostile
inputs are refused in testing, with the symlink untouched in every case.

### Verification

Both a checksum and a signature, always:

- The **SHA-256** proves the bytes arrived intact. A corrupted mirror fails here.
- The **ed25519 signature** proves provenance. An attacker who controls the
  mirror controls the checksum too, so the checksum alone proves nothing about
  who wrote the archive.

The signature covers the digest rather than the archive, so verification does
not need the whole file in memory.

The public key is compiled into the binary (`-X main.UpdatePublicKey=...`), so
an appliance verifies against the key that signed its own release rather than
one it is told about at run time. A missing or malformed key **disables the
updater** rather than being treated as "verification passed" — an updater
without a key could only ever install unverified code.

### Unpacking a downloaded archive

A release archive comes from the network, so the extractor refuses:

- any entry whose path contains a `..` segment — **rejected, not sanitised**.
  `filepath.Clean` would turn `../../etc/sudoers.d/homeos` into
  `<root>/etc/sudoers.d/homeos`, which does not escape but silently writes a
  file nobody asked for. A genuine release never contains such a name.
- symlinks and hard links, which are the same escape in two steps
- devices, FIFOs and sockets
- entries over 256 MB, independently of the archive's compressed size, so a
  small tarball cannot decompress into a full disk

Only the owner-execute bit is honoured from the archive; the rest of the mode is
ours, so a release cannot ship a world-writable or setuid file.

---

## 2. Releases are reproducible

`tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner`, and Go's
`-trimpath`. The same source tree produces byte-identical archives, so a rebuilt
release still verifies against the signature that was published for it — which
is the difference between "we can rebuild this" and "we can prove this is what
we shipped".

`homeos-release verify` re-checks a manifest against archives on disk, so a
release is validated before it is published rather than after an appliance
rejects it.

---

## 3. The containerised stack

Two compose files, because they answer different questions.

`docker-compose.dev.yml` is for working on HomeOS: Vite with hot reload, the
backend built from source, and **Caddy in front with the same routing an
appliance has**. That last part is the point — a bug that only appears behind
the proxy (a websocket upgrade, an SSE buffer, an SPA deep link) reproduces here
rather than in production. A labelled `sample-app` gives the launcher and
`homeos-proxy-sync` something real to show without installing anything.

`docker-compose.demo.yml` serves the built dashboard from disk, as an appliance
does, for checking a release the way a user will see it.

Both mount the host's Docker socket read-write, so container orchestration and
the app store work for real. That is root on the host, and the file says so.

What does not work in a container is stated rather than papered over: storage
needs the host's block devices, Samba needs `smbd`, and mDNS and OTA updates are
both systemd-shaped. The API answers `503` for storage — the same honest
response an LXC install gets, which is why that path was worth fixing properly
in phase 3 rather than special-casing here.

---

## 4. Testing

The updater has 30 assertions across four areas, concentrated where a mistake is
expensive:

| Area | What is asserted |
|---|---|
| signature | a tampered archive **with a matching checksum** is refused; a foreign key is refused; an absent key disables verification rather than passing it |
| archive | nine escape attempts refused, and nothing written outside the destination |
| version | pre-releases sort before their release; `dev` sorts below everything |
| manifest | malformed checksums, signatures and channel documents refused |

The applier — the most dangerous script in the project — is exercised against a
sandbox tree with shimmed `systemctl`, `curl` and symlink operations:

- a successful apply moves `current` forward and records `previous`
- **a build that never becomes healthy is rolled back**
- **a build that will not start at all is rolled back**
- re-applying the current release is a no-op
- eleven hostile version strings are refused with the symlink untouched

The full over-the-air path was also run end to end against a local signed
channel: check → download a 17 MB archive → verify → unpack → sanity-check the
binary → stage. Then the mirror was made hostile — the archive replaced and its
checksum recomputed, the signature left alone — and the daemon refused it with
`release signature does not verify`, leaving nothing on disk.

### Two bugs this found

**Updates could not be turned off.** `config.applyDefaults` filled every empty
string, so setting `channel_url: ""` silently got the shipped default back.
Found by the containerised config, which turns updates off deliberately.
`ChannelURL` is now the one field with no default: empty means off, and
`install.sh` writes the shipped value into `config.yaml` instead.

**A transient rename failed the whole download.** Publishing a staged release
used a single `os.Rename`, which loses a verified 25 MB archive if anything
still holds a handle — an on-access scanner, an `exec` not yet reaped, an
indexer. It now retries briefly.

---

## 5. Deliberate limits of phase 4

- **No delta updates.** A release is a full archive, ~17–25 MB. Binary diffing
  would save bandwidth and cost a great deal of complexity in exactly the code
  path that must never be subtly wrong.
- **No staged rollout.** Every appliance polling a channel sees a release at
  once; the only staggering is the timer's random delay. A percentage rollout
  needs server-side state the channel format deliberately does not have.
- **`/etc/homeos` is merged by hand.** An update ships new unit files and rules,
  but a changed `config.yaml` key needs the operator to add it. Automatic
  config migration is where update systems usually start corrupting things.
- **No signed channel document.** The *artifacts* are signed, which is what
  matters — a hostile channel can withhold an update or offer an old one, but
  cannot make an appliance install code it did not sign. Signing the manifest
  too would close the downgrade window and is the obvious next step.
- **The dev stack cannot exercise updates.** There is no symlink to swap and no
  systemd unit to restart. Testing the apply path means a VM.
