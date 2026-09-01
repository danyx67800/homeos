# HomeOS — Phase 3: Web Dashboard

Phase 2 left an API that had only ever been driven with `curl`. Phase 3 is the
interface: a single-page dashboard built with Svelte 5, Vite and Tailwind CSS 4,
served as static files from `/opt/homeos/web`.

---

## 1. Why Svelte

The dashboard re-renders telemetry every two seconds and may sit open on a wall
display for weeks, often served from a Raspberry Pi. That shaped the choice more
than familiarity did:

- **No virtual DOM.** Svelte compiles reactivity to direct DOM updates. A gauge
  whose number changes does not diff a tree; it writes one text node.
- **46 KB gzipped** for the whole application — router, charts, dialogs and all.
  React with a chart library would be several times that before any of our code.
- **Runes** (`$state`, `$derived`, `$effect`) make the streaming store plain:
  one module holds the state, the stream writes to it, every view reads it. No
  subscriptions to tear down and no context plumbing.

Tailwind 4's CSS-first config means the theme is a `@theme` block and a handful
of custom properties, with no JavaScript config file to keep in step.

---

## 2. Streaming, and being honest about it

`TelemetryStream` opens a WebSocket, falls back to SSE, and owns its own retry
policy. Three details are what make it survive a real network:

**A stale-data timer.** A TCP connection to a box that has been unplugged can
stay open for minutes. If no sample arrives within four sampling intervals the
stream is torn down and reconnected, rather than leaving the dashboard showing
old numbers as though they were live.

**Backoff with jitter.** Exponential to 30 s, multiplied by 0.7–1.3. Without the
jitter, every open tab hits a restarting daemon at the same instant.

**A sticky SSE fallback.** After two WebSocket failures the transport switches to
SSE permanently for that session. A proxy that mangles upgrades would otherwise
cost a failed attempt on every single reconnect, forever.

The connection state is on screen at all times as a coloured dot in the header.
Every number in the interface silently depends on the stream being alive, so
"are these figures live?" deserves an honest answer rather than an assumption.

The token travels in the query string, because neither `EventSource` nor the
WebSocket constructor lets a browser set an `Authorization` header. That is
acceptable only because the URL terminates on loopback — see
[phase2-backend.md §7](phase2-backend.md#7-authentication).

---

## 3. Design

Dark-first glassmorphism over a slow-drifting gradient. Decisions worth naming:

**The theme resolves before first paint**, in an inline script in `index.html`.
Doing it in the app would flash white on every load, which on a display someone
leaves running is genuinely unpleasant.

**`backdrop-filter` only on containers.** It is expensive on a Pi's GPU. The
`.glass` class is applied to panels, never to the dozens of small elements
inside them.

**One accent ramp, three status colours.** The colour budget is spent on meaning
— ok / warn / bad — rather than on decoration. `severity()` lives in one module
so a number never reads green in a gauge and amber in the card beside it.

**`prefers-reduced-motion` stops the drift.** The gradient stays; it just holds
still. A wall dashboard that animates for a week is a reasonable thing to want
switched off.

**Base-1000 byte formatting.** A 4 TB disk shows as 4.0 TB, matching the label
on the drive. Base-1024 would report 3.6 TB and generate support questions
forever.

### Gauges

SVG, not canvas: it scales to any DPI for free. The value animates through a CSS
transition on `stroke-dashoffset`, so the browser interpolates it rather than us
re-rendering on a timer. The arc spans 270° with the gap at the bottom, which
reads as a dial rather than a pie and leaves room for the label.

---

## 4. Layout

Two navigation systems: inline tabs from `md` up, a fixed bottom bar below —
where the top of a tall phone is out of thumb reach. Both mark the same tab with
`aria-current`, which the end-to-end test asserts.

The vitals row is `grid-cols-[auto_1fr]`: the gauge panel takes only the width
it needs, and the stat cards fill whatever is left in an `auto-fit` grid. That
matters because two of the cards are conditional — temperature and fan speed
exist on a mini PC and not on most SBCs — and a fixed column count left visible
holes on the machines that report less.

Routing is hash-based. Phase 1's Caddyfile already falls back to `index.html`
so the History API would work, but a hash keeps deep links correct even when
the bundle is served from a sub-path, and there are five destinations, not fifty.

---

## 5. The five surfaces

| View | What it does |
|---|---|
| **Dashboard** | gauges for CPU, memory and temperature; stat cards with sparklines; filesystem bars; the app launcher |
| **Store** | category filter, search, detail modal with the install form, background install |
| **Storage** | disks and partitions, SMART detail, format, mount, unmount |
| **Shares** | SMB share editor |
| **Settings** | system facts, theme, password change |

### Launcher

A tile is a **link**, not a button. Opening the app is the overwhelmingly common
action, so it should middle-click into a new tab and show its URL on hover like
any other link. Management lives behind a menu button that appears on hover.

The status dot distinguishes running, stopped, starting, unhealthy and
installing. During an install the progress bar sits inside the tile's existing
footprint, so the grid does not reflow while an app is being pulled.

Container state is the one thing not on the telemetry stream, so the launcher
refreshes every 20 seconds and immediately after an action.

### Store

The install form shows only the fields a person must actually decide on;
anything marked `advanced` in the manifest is behind a disclosure. Fields the
backend generates show "Leave blank to generate a strong value" rather than
demanding one.

Install returns `202` and the modal closes. Progress arrives on the telemetry
stream as `type: "install"` and is rendered by the launcher tile — one
connection, not a second polling loop.

Manifests the backend rejected are shown at the bottom of the store with their
parse errors. A catalogue author should be able to see why their app is missing
instead of guessing.

### Storage

Formatting asks the user to type the device path, matching what the API already
requires. It is the one action that destroys every byte on a disk, and a
mistyped device in a form should not be enough to trigger it.

The SMART dialog puts **warnings above the raw attribute table**, with an
explicit note that SMART may still report the drive as passing. That is the
whole point of the backend's predictive-attribute logic: a drive with twelve
reallocated sectors reports `passed: true`, and a dashboard that shows a green
tick there is worse than useless.

A drive behind a USB bridge that does not pass SMART through is shown as
*unknown*, never as unhealthy. Training people to ignore a red indicator is how
the real failure gets missed.

### Shares

The API replaces the whole share set in one `PUT`, matching how the file is
written on disk, so the editor works on a local copy and saves it whole. A
rejected set leaves the server exactly as it was.

`Public` is a checkbox that says "Allow anyone on the network (no password)"
and, when ticked, spells out that every device on the LAN can read the folder.
The backend refuses to infer it from an empty user list; the UI refuses to
disguise it.

---

## 6. Testing

`npm run e2e` drives a real Chromium against a running `homeos-core` and
asserts 21 behaviours: first-run wizard and login (including a wrong password),
live telemetry reaching the gauges, navigation to all five views with
`aria-current` on exactly the right tab, theme toggling, dialog focus and
Escape, the mobile bottom bar, absence of horizontal overflow at 390 px, and no
uncaught console errors. Screenshots are written to `web/shots/`.

Three defects it caught that reading the code would not have:

1. **A false-positive navigation check of my own.** The Store assertion matched
   the text "App Store" — which is a *button on the dashboard*. The check passed
   while the screenshot showed the dashboard. Markers are now view-specific and
   the route is asserted separately.
2. **Two gauges overflowed a 390 px viewport by two pixels** and wrapped, making
   the vitals take two screens to scroll past on a phone.
3. **Content read through the mobile bottom bar.** At the shared panel opacity,
   text scrolling underneath was legible through it.

A fourth came from the backend side: `/storage/disks` returned **500** when
`lsblk` was absent. A missing tool is not an internal error — it is subsystem
unavailability, the same class as an unreachable Docker. It is now a `503` with
a message the dashboard renders as "Storage management is unavailable on this
system", which matters on the container-based installs phase 1's preflight
already warns about.

---

## 7. Deliberate limits

- **No app "Settings" pane yet.** The context menu entry opens the store detail.
  Reconfiguring an installed app means changing its environment and recreating
  the stack, which belongs with the update path in phase 4.
- **Logs are polled, not streamed.** The telemetry stream is a broadcast to
  every connected dashboard; per-container log following does not belong on it.
  A 3-second poll while the modal is open is cheap and cannot outlive the dialog.
- **No partition editor.** Format creates one partition filling the disk, which
  is what a NAS wants. Multi-partition layouts are a shell job.
- **No i18n.** The interface is English; dates, times and relative times use the
  browser's locale through `Intl`.
- **Samba users are not managed here.** The share editor accepts usernames but
  creating them is still `smbpasswd -a`, which the field says explicitly.
