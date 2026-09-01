import { chromium } from 'playwright';

const BASE = 'http://127.0.0.1:8791';
const PASSPHRASE = 'a-long-enough-passphrase';
const shot = (p, n) => p.screenshot({ path: `shots/${n}.png`, fullPage: false });
const ok = (m) => console.log('  PASS ' + m);
const bad = (m) => { console.log('  FAIL ' + m); process.exitCode = 1; };

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });

const errors = [];
page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });
page.on('pageerror', (e) => errors.push(String(e)));

// 1. Sign in, running the first-run wizard when the appliance is fresh
await page.goto(BASE, { waitUntil: 'networkidle' });
await page.waitForSelector('h1', { timeout: 10000 });
const h1 = await page.textContent('h1');
const fresh = h1 === 'Set up HomeOS';
ok(fresh ? 'first-run wizard shown' : 'login shown (account already exists)');
await shot(page, '1-signin');

const disabledBefore = await page.isDisabled('button[type=submit]');
disabledBefore ? ok('submit disabled on an empty form') : bad('submit enabled too early');

await page.fill('input[autocomplete=username]', 'marco');

if (fresh) {
  await page.fill('input[autocomplete=new-password] >> nth=0', 'short');
  const stillDisabled = await page.isDisabled('button[type=submit]');
  stillDisabled ? ok('submit disabled for a short password') : bad('short password accepted');
  await page.fill('input[autocomplete=new-password] >> nth=0', PASSPHRASE);
  await page.fill('input[autocomplete=new-password] >> nth=1', PASSPHRASE);
} else {
  await page.fill('input[autocomplete=current-password]', 'wrong-passphrase');
  await page.click('button[type=submit]');
  await page.waitForSelector('[role=alert]', { timeout: 8000 })
    .then(() => ok('wrong password shows an error')).catch(() => bad('no error shown'));
  await page.fill('input[autocomplete=current-password]', PASSPHRASE);
}
await page.click('button[type=submit]');

// 2. Dashboard renders with live telemetry
await page.waitForSelector('text=Apps', { timeout: 15000 });
ok('signed in and dashboard rendered');

await page.waitForFunction(
  () => document.body.innerText.includes('Live'), null, { timeout: 15000 },
).then(() => ok('telemetry stream reports Live'))
 .catch(() => bad('stream never reached Live'));

// The vitals strip must show real numbers, not placeholders.
await page.waitForTimeout(2500);
const vitals = await page.evaluate(() =>
  [...document.querySelectorAll('.label')].map((l) => {
    const val = l.parentElement?.querySelector('.readout');
    return { label: l.textContent.trim(), value: val?.textContent.trim() ?? '' };
  }).filter((v) => v.value));

const cpu = vitals.find((v) => /CPU/i.test(v.label));
/^[0-9]+(\.[0-9]+)?%$/.test(cpu?.value ?? '')
  ? ok(`CPU reading live (${cpu.value})`)
  : bad(`CPU reading = "${cpu?.value}" from ${JSON.stringify(vitals)}`);

vitals.length >= 4
  ? ok(`vitals strip shows ${vitals.length} readings`)
  : bad(`only ${vitals.length} readings in the strip`);

// Apps sit above the vitals. That reordering is the point of the redesign, so
// it is worth failing a build over if it ever moves back.
const appsFirst = await page.evaluate(() => {
  const apps = [...document.querySelectorAll('h2')].find((h) => /^Apps/.test(h.textContent.trim()));
  const strip = document.querySelector('.label');
  if (!apps || !strip) return null;
  return apps.getBoundingClientRect().top < strip.getBoundingClientRect().top;
});
appsFirst === true
  ? ok('the app grid sits above the vitals')
  : bad('the vitals are above the apps again');

const bodyText = await page.innerText('body');
/\d+(\.\d+)?\s?(GB|MB)/.test(bodyText) ? ok('memory rendered in real units') : bad('no memory figure');
await shot(page, '2-dashboard');

// 3. Navigation across every view
for (const [tab, route, marker] of [
  ['Store', 'store', 'input[placeholder^="Search apps"]'],
  ['Storage', 'storage', 'button:has-text("Rescan")'],
  ['Shares', 'shares', 'button:has-text("New share")'],
  ['Settings', 'settings', 'h2:has-text("Appearance")'],
]) {
  await page.click(`nav button:has-text("${tab}")`);
  await page.waitForFunction(
    (r) => location.hash.startsWith('#/' + r), route, { timeout: 8000 },
  ).catch(() => bad(`${tab} did not change the route`));
  await page.waitForSelector(marker, { timeout: 8000 })
    .then(() => ok(`navigated to ${tab}`))
    .catch(() => bad(`${tab} did not render its own content`));

  // Exactly one tab must claim aria-current, and it must be this one.
  const current = await page.evaluate(() =>
    [...document.querySelectorAll('nav button[aria-current="page"]')]
      .map((e) => e.textContent.trim()));
  current.length === 2 && current.every((t) => t === tab)
    ? ok(`${tab} marked as the current tab`)
    : bad(`aria-current = [${current}] while on ${tab}`);

  await page.waitForTimeout(250);
  await shot(page, `3-${tab.toLowerCase()}`);
}

// 4. Theme toggle
await page.click('nav button:has-text("Dashboard")');
await page.waitForTimeout(400);
const darkBefore = await page.evaluate(() => document.documentElement.classList.contains('dark'));
await page.click('button[aria-label*="theme"]');
await page.waitForTimeout(400);
const darkAfter = await page.evaluate(() => document.documentElement.classList.contains('dark'));
darkBefore !== darkAfter ? ok('theme toggles') : bad('theme did not change');
await shot(page, '4-light');
await page.click('button[aria-label*="theme"]');

// 5. Power dialog
await page.click('button[aria-label="Power options"]');
await page.waitForSelector('dialog[open]', { timeout: 5000 })
  .then(() => ok('power dialog opens')).catch(() => bad('power dialog missing'));
await page.keyboard.press('Escape');
await page.waitForTimeout(300);
const stillOpen = await page.locator('dialog[open]').count();
stillOpen === 0 ? ok('Escape closes the dialog') : bad('dialog stayed open');

// 6. Mobile layout
await page.setViewportSize({ width: 390, height: 844 });
await page.waitForTimeout(500);
// Tailwind class names contain a colon, which the CSS engine will not parse
// in a selector, so the check is done in the page instead.
const bottomNav = await page.evaluate(() => {
  const navs = [...document.querySelectorAll('nav')];
  // A fixed-position element has a null offsetParent, so visibility has to
  // come from its box instead.
  return navs.some((n) => n.className.includes('md:hidden')
    && n.getBoundingClientRect().height > 0);
});
bottomNav ? ok('bottom navigation appears on mobile') : bad('no mobile navigation');
const scrollX = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
scrollX <= 1 ? ok('no horizontal overflow at 390px') : bad(`overflows by ${scrollX}px`);
await shot(page, '5-mobile');

// 7. No console errors anywhere in that flow
// HTTP status logs are expected here: this environment has no Docker and no
// lsblk, and the dashboard surfaces both conditions in the UI. Only uncaught
// exceptions and framework errors indicate a bug in the dashboard itself.
const real = errors.filter((e) => !/favicon|Failed to load resource/i.test(e));
real.length === 0 ? ok('no console errors') : bad('console errors: ' + real.slice(0, 3).join(' | '));

await browser.close();
console.log(process.exitCode ? '\nRESULT: failures above' : '\nRESULT: all checks passed');
