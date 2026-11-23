// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

// UI tests against the built frontend with a mock backend. They cover the
// flows people use every day and the parts changed recently; the Go tests
// cover the backend behind them.
import { test, expect } from '@playwright/test';
import { readFileSync } from 'node:fs';

const mock = readFileSync(new URL('./mock-backend.js', import.meta.url), 'utf8');

async function open(page, cfg = {}) {
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  await page.addInitScript(c => { window.__mockConfig = c; }, cfg);
  await page.addInitScript(mock);
  await page.goto('/');
  await expect(page.locator('#tree .row').first()).toBeVisible();
  return errors;
}

const calls = (page, method) => page.evaluate(m => window.__calls.filter(c => c.method === m), method);

test('session tree shows groups and sessions and filters them', async ({ page }) => {
  const errors = await open(page);
  await expect(page.locator('#tree .row.group')).toHaveCount(1);
  await expect(page.locator('#tree .row.session')).toHaveCount(3);
  await page.fill('#search', 'core');
  await expect(page.locator('#tree .row.session')).toHaveCount(2);
  await page.fill('#search', 'nothing-like-this');
  await expect(page.locator('#tree')).toContainText('No saved session matches');
  expect(errors).toEqual([]);
});

test('a huge filter result draws 1,000 rows and offers the rest', async ({ page }) => {
  const nodes = [{ id: 'g', parentId: '', name: 'All', isGroup: true, expanded: false, order: 0 }];
  for (let i = 0; i < 3000; i++) nodes.push({ id: `s${i}`, parentId: 'g', name: `sw-${i}`, order: i, session: { protocol: 'ssh', host: `10.0.${i >> 8}.${i & 255}`, port: 22 } });
  const errors = await open(page, { nodes });
  await page.fill('#search', 'sw-');
  await expect(page.locator('#tree .row')).toHaveCount(1000);
  const more = page.locator('.tree-more');
  await expect(more).toContainText('2001 more matches');
  await more.click();
  await expect(page.locator('#tree .row')).toHaveCount(3001);
  expect(errors).toEqual([]);
});

test('Ctrl+click selects several sessions for editing together', async ({ page }) => {
  const errors = await open(page);
  const rows = page.locator('#tree .row.session');
  await rows.nth(0).click();
  await rows.nth(1).click({ modifiers: ['Control'] });
  await expect(page.locator('#tree .row.multi, #tree .row.selected')).toHaveCount(2);
  await rows.nth(1).click({ button: 'right' });
  await page.getByText(/Edit 2 sessions together/).click();
  await expect(page.locator('dialog, .dialog').filter({ hasText: 'Edit 2 sessions together' }).first()).toBeVisible();
  expect(errors).toEqual([]);
});

test('settings are organised in tabs and save the reopen option', async ({ page }) => {
  const errors = await open(page);
  await page.click('#btn-settings');
  const names = ['General', 'Appearance', 'Terminal', 'Connections', 'Highlighting', 'Session logs', 'Security', 'Data and backups', 'Team libraries', 'Integrations', 'About'];
  await expect(page.getByRole('dialog', { name: 'Settings' }).locator('.editor > nav button')).toHaveText(names);
  // General opens first: starting YACM and session names.
  await page.getByText('Reopen the sessions that were open when YACM closed').click();
  await page.getByRole('button', { name: 'Terminal', exact: true }).click();
  await expect(page.getByText('Scrollback lines')).toBeVisible();
  await expect(page.getByText('Copy selected text automatically')).toBeVisible();
  await page.getByRole('button', { name: 'Connections', exact: true }).click();
  await expect(page.getByText('Reconnect automatically after a reload')).toBeVisible();
  await page.getByRole('button', { name: 'Session logs', exact: true }).click();
  await expect(page.getByText('Hide passwords and keys in session logs')).toBeVisible();
  await expect(page.getByText('Log new and imported sessions')).toBeVisible();
  await page.getByRole('button', { name: 'Save settings' }).click();
  const saved = await calls(page, 'SaveSettings');
  expect(saved.at(-1).args[0].reopenSessions).toBe(true);
  expect(saved.at(-1).args[0].logSecretsInClear).toBeFalsy();
  expect(errors).toEqual([]);
});

test('the terminal font weight is saved', async ({ page }) => {
  const errors = await open(page);
  await page.click('#btn-settings');
  await page.getByRole('button', { name: 'Appearance', exact: true }).click();
  const bold = page.locator('.term-preview span');
  await expect(bold).toHaveCSS('font-weight', '700');
  await page.getByRole('radio', { name: 'Medium' }).click();
  await expect(page.locator('.term-preview')).toHaveCSS('font-weight', '500');
  await expect(bold).toHaveCSS('font-weight', '800');
  await page.getByRole('button', { name: 'Save settings' }).click();
  const saved = await calls(page, 'SaveSettings');
  expect(saved.at(-1).args[0].fontWeight).toBe('medium');
  expect(errors).toEqual([]);
});

test('backups are listed and Restore asks first', async ({ page }) => {
  const backups = [{ id: '2026-10-27_21-00-00', created: '2026-10-27T21:00:00Z', reason: 'before import from securecrt', sessions: 120, groups: 8 }];
  const errors = await open(page, { backups });
  await page.click('#btn-settings');
  await page.getByRole('button', { name: 'Data and backups', exact: true }).click();
  await expect(page.getByText('before import from securecrt · 120 sessions · 8 groups')).toBeVisible();
  await page.getByRole('button', { name: 'Restore…' }).click();
  await page.getByRole('button', { name: 'Restore and restart' }).click();
  expect((await calls(page, 'RestoreBackup'))[0].args).toEqual(['2026-10-27_21-00-00']);
  expect(errors).toEqual([]);
});

test('a redirected team session asks before connecting', async ({ page }) => {
  const errors = await open(page);
  await page.evaluate(() => window.__emit('prompt', {
    id: 'p1', kind: 'teamchange', session: 'acc-sw01',
    change: { library: 'DC Zagreb', updatedBy: 'kolega@PC', updated: '2026-10-27 22:25', before: 'ssh 10.0.0.1:22', now: 'ssh 203.0.113.66:22' },
  }));
  const dlg = page.locator('.dialog, dialog').filter({ hasText: 'acc-sw01 has changed since your last connection' }).first();
  await expect(dlg).toContainText('ssh 10.0.0.1:22');
  await expect(dlg).toContainText('ssh 203.0.113.66:22');
  await expect(dlg).toContainText('DC Zagreb');
  await page.getByRole('button', { name: 'Cancel' }).click();
  const ans = await calls(page, 'AnswerPrompt');
  expect(ans[0].args).toEqual(['p1', expect.objectContaining({ accept: false })]);
  expect(errors).toEqual([]);
});

test('a session without a username asks for one and can save it', async ({ page }) => {
  const errors = await open(page);
  await page.evaluate(() => window.__emit('prompt', {
    id: 'p2', kind: 'secret', session: 'cptgwy-stack',
    secret: { kind: 'username', title: 'Username', message: 'Username for 194.152.210.129:22', canSave: true },
  }));
  const dlg = page.locator('.dialog, dialog').filter({ hasText: 'Username for 194.152.210.129:22' }).first();
  await expect(dlg.locator('input[type=text]')).toBeFocused();
  await page.getByRole('button', { name: 'Continue' }).click();
  await expect(dlg).toBeVisible(); // nothing typed yet
  await dlg.locator('input[type=text]').fill('icavar');
  await page.getByRole('button', { name: 'Continue' }).click();
  const ans = await calls(page, 'AnswerPrompt');
  expect(ans.at(-1).args).toEqual(['p2', expect.objectContaining({ accept: true, value: 'icavar', save: true })]);
  expect(errors).toEqual([]);
});

test('library changes are listed', async ({ page }) => {
  const teamChanges = [{ library: 'DC Zagreb', revision: 4, updatedBy: 'kolega@PC', updated: '2026-10-27 22:25',
    moved: [{ name: 'acc-sw01', before: 'ssh 10.0.0.1:22', now: 'ssh 10.0.0.1:2224' }], added: [{ name: 'acc-sw99', now: 'ssh 10.0.0.99:22' }] }];
  const errors = await open(page, { teamChanges });
  await expect(page.getByText('Team library DC Zagreb was updated')).toBeVisible();
  await expect(page.getByText('ssh 10.0.0.1:22  →  ssh 10.0.0.1:2224')).toBeVisible();
  await expect(page.getByText('new: ssh 10.0.0.99:22')).toBeVisible();
  expect(errors).toEqual([]);
});

test("What's new after an update lists the versions since the last one", async ({ page }) => {
  const errors = await open(page, { version: '0.9.3', settings: { lastSeenVersion: '0.9.1' } });
  const dlg = page.getByRole('dialog', { name: "What's new in YACM 0.9.3" });
  await expect(dlg).toBeVisible();
  await expect(dlg.getByText('YACM 0.9.2')).toBeVisible();
  await expect(dlg.getByText('Linux packages')).toBeVisible();
  expect(errors).toEqual([]);
});

test("no What's new before 0.9.1, the guide on a fresh install", async ({ page }) => {
  let errors = await open(page, { version: '0.9.0', settings: { lastSeenVersion: '0.8.11' } });
  await expect(page.locator('#tree .row').first()).toBeVisible();
  await expect(page.getByText("What's new in YACM")).toHaveCount(0);
  expect((await calls(page, 'MarkVersionSeen')).length).toBe(1);
  await page.locator('#btn-settings').click();
  const dlg = page.getByRole('dialog', { name: 'Settings' });
  await dlg.getByText('About', { exact: true }).click();
  await expect(dlg.getByRole('button', { name: 'Licence and terms' })).toBeVisible();
  await expect(dlg.getByRole('button', { name: 'What’s new' })).toHaveCount(0);
  await page.keyboard.press('Escape');
  expect(errors).toEqual([]);

  const fresh = await page.context().newPage();
  errors = [];
  fresh.on('pageerror', e => errors.push(e.message));
  await fresh.addInitScript(c => { window.__mockConfig = c; }, { fresh: true, nodes: [], settings: { lastSeenVersion: '' } });
  await fresh.addInitScript(mock);
  await fresh.goto('/');
  await expect(fresh.getByText('Welcome to YACM')).toBeVisible();
  await expect(fresh.getByRole('button', { name: 'Import sessions…' })).toBeVisible();
  // The demo is a separate download: without it the guide links to the releases.
  await fresh.getByRole('button', { name: 'Get the demo' }).click();
  expect(await fresh.evaluate(() => window.__opened)).toEqual(['https://github.com/yacm-app/yacm/releases']);
  await expect(fresh.getByRole('button', { name: 'Import the demo sessions' })).toHaveCount(0);
  expect(errors).toEqual([]);

  // With the demo download unpacked next to YACM it imports in one click.
  const withDemo = await page.context().newPage();
  withDemo.on('pageerror', e => errors.push(e.message));
  await withDemo.addInitScript(c => { window.__mockConfig = c; }, { fresh: true, nodes: [], settings: { lastSeenVersion: '' }, demoFile: '/home/u/Downloads/yacm-demo-device-0.9.1-linux-amd64/demo.yacm' });
  await withDemo.addInitScript(mock);
  await withDemo.goto('/');
  await expect(withDemo.getByRole('button', { name: 'Import the demo sessions' })).toBeVisible();
  await expect(withDemo.getByRole('button', { name: 'Get the demo' })).toHaveCount(0);
  expect(errors).toEqual([]);
});

test('right click opens a menu instead of pasting', async ({ page }) => {
  const errors = await open(page);
  // Open a tab: the mock accepts Connect, so a terminal appears.
  await page.locator('#tree .row.session').first().dblclick();
  const term = page.locator('.term-body').first();
  await expect(term).toBeVisible();
  await term.click({ button: 'right' });
  await expect(page.getByText('Copy and paste')).toBeVisible();
  expect((await calls(page, 'Write')).length).toBe(0);
  expect(errors).toEqual([]);
});

test('on a Mac the menus show Cmd shortcuts', async ({ page }) => {
  await page.addInitScript(() => Object.defineProperty(Navigator.prototype, 'platform', { get: () => 'MacIntel' }));
  const errors = await open(page);
  await page.locator('#tree .row.session').first().dblclick();
  await page.locator('.term-body').first().click({ button: 'right' });
  await expect(page.locator('.menu, .context-menu').first()).toContainText('⌘V');
  expect(errors).toEqual([]);
});

test('a session of a connection type from a newer YACM is shown but not opened or edited', async ({ page }) => {
  const nodes = [
    { id: 'g1', parentId: '', name: 'DC', isGroup: true, expanded: true, order: 0 },
    { id: 's1', parentId: 'g1', name: 'core-sw1', order: 0, session: { protocol: 'ssh', host: '10.0.0.1', port: 22 } },
    { id: 'w1', parentId: 'g1', name: 'jump-win', order: 1, session: { protocol: 'rdp', host: '10.0.0.50', rdp: { width: 1920 } } },
  ];
  const errors = await open(page, { nodes });
  const row = page.locator('#tree .row.session.newer');
  await expect(row).toHaveCount(1);
  await expect(row).toContainText('newer YACM');
  await row.hover();
  await expect(row).toHaveAttribute('title', /newer version of YACM/);
  // Filtering still works with such a session in the tree.
  await page.fill('#search', 'core');
  await expect(page.locator('#tree .row.session')).toHaveCount(1);
  await page.fill('#search', '');
  await expect(page.locator('#tree .row.session')).toHaveCount(2);
  await row.dblclick();
  await expect(page.locator('.toast')).toContainText('newer version of YACM');
  expect((await calls(page, 'Connect')).length).toBe(0);
  await expect(page.locator('.term-body')).toHaveCount(0);
  await row.click({ button: 'right' });
  await page.getByText(/^Edit/).first().click();
  await expect(page.getByRole('dialog', { name: 'jump-win' })).toContainText('update YACM to open or edit it');
  expect(errors).toEqual([]);
});

// connectTabs opens sessions by name and reports them connected.
async function connectTabs(page, names) {
  for (const name of names) await page.locator('#tree .row.session', { hasText: name }).dblclick();
  const ids = (await calls(page, 'Connect')).map(c => c.args[0].tabId);
  for (const tabId of ids) await page.evaluate(id => window.__emit('tab:status', { tabId: id, state: 'connected', summary: 'curve25519-sha256 · ssh-ed25519 · aes128-gcm' }), tabId);
  return ids;
}

test('Type to many has an All box that follows the session boxes', async ({ page }) => {
  const errors = await open(page);
  await connectTabs(page, ['core-sw1', 'core-sw2']);
  await page.getByRole('button', { name: 'Type to many' }).click();
  const bar = page.locator('#broadcast');
  await expect(bar).toContainText('Typing goes to (2 of 2):');
  const all = bar.getByLabel('All sessions');
  const boxes = bar.locator('label:not(.all) input');
  await expect(boxes).toHaveCount(2);
  // No tags: every connected session starts ticked.
  await expect(all).toBeChecked();
  await boxes.nth(0).uncheck();
  expect(await all.evaluate(el => el.indeterminate)).toBe(true);
  await all.check();
  await expect(boxes.nth(0)).toBeChecked();
  await all.uncheck();
  await expect(boxes.nth(1)).not.toBeChecked();
  await all.check();
  // A session opened later does not join by itself.
  await page.locator('#tree .row.session', { hasText: 'old-router' }).dblclick();
  await expect(boxes).toHaveCount(3);
  await expect(boxes.nth(2)).not.toBeChecked();
  expect(await all.evaluate(el => el.indeterminate)).toBe(true);
  await expect(page.getByRole('button', { name: 'Stop typing to many' })).toBeVisible();
  expect(errors).toEqual([]);
});

test('Type to many: Space after a command from the field pages on, and a ticked box does not catch it', async ({ page }) => {
  const errors = await open(page);
  const ids = await connectTabs(page, ['core-sw1', 'core-sw2']);
  await page.getByRole('button', { name: 'Type to many' }).click();
  const bar = page.locator('#broadcast');
  const line = bar.getByPlaceholder('Send one command to the checked sessions');
  await line.fill('show run');
  await line.press('Enter');
  // --More--: Space (and Enter, Ctrl+C) in the empty field go to the sessions.
  await line.press(' ');
  await line.press('Enter');
  await line.press('Control+c');
  await expect(line).toHaveValue('');
  const sent = (await calls(page, 'Broadcast')).map(c => c.args);
  expect(sent).toEqual([[ids, 'show run\r'], [ids, ' '], [ids, '\r'], [ids, '\x03']]);
  // A command is still typed normally once the field has text.
  await line.fill('show ver');
  await line.press(' ');
  await expect(line).toHaveValue('show ver ');
  // After a click on a session box the keys go back to the terminal, so a
  // Space does not clear the box again.
  const box = bar.locator('label:not(.all) input').nth(1);
  await box.uncheck();
  await page.keyboard.press(' ');
  await expect(box).not.toBeChecked();
  expect(await page.evaluate(() => document.activeElement?.closest('.term') !== null)).toBe(true);
  expect(errors).toEqual([]);
});

test('the session editor previews the font size, and a cleared size goes back to the default', async ({ page }) => {
  const errors = await open(page);
  await page.locator('#tree .row.session', { hasText: 'core-sw1' }).click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Edit' }).click();
  await page.getByRole('button', { name: 'Terminal and logs' }).click();
  const preview = page.locator('.term-preview');
  await expect(preview).toHaveCSS('font-size', '14px');
  const size = page.locator('.field', { hasText: 'Font size' }).locator('input');
  await size.fill('22');
  await expect(preview).toHaveCSS('font-size', '22px');
  await size.fill('');
  await expect(preview).toHaveCSS('font-size', '14px');
  expect(errors).toEqual([]);
});

test('the tab menu offers Clone session instead of font sizes', async ({ page }) => {
  const errors = await open(page);
  const [first] = await connectTabs(page, ['core-sw1', 'core-sw2']);
  await page.locator('.tab', { hasText: 'core-sw1' }).click({ button: 'right' });
  const menu = page.locator('.menu');
  await expect(menu).toContainText('Clone session');
  await expect(menu).not.toContainText('Bigger font');
  await expect(menu).not.toContainText('Keep this font size');
  await menu.getByText('Clone session').click();
  const connects = await calls(page, 'Connect');
  expect(connects.length).toBe(3);
  expect(connects[2].args[0].nodeId).toBe('s1');
  // The clone sits right after the original tab, numbered.
  await expect(page.locator('.tab .title')).toHaveText(['core-sw1', 'core-sw1 (2)', 'core-sw2']);
  await page.locator('.tab', { hasText: 'core-sw1' }).first().click({ button: 'right' });
  await page.locator('.menu').getByText('Clone session').click();
  await expect(page.locator('.tab .title')).toHaveText(['core-sw1', 'core-sw1 (3)', 'core-sw1 (2)', 'core-sw2']);
  await page.locator('.tab', { hasText: 'core-sw1 (3)' }).locator('.close').click();
  const disc = page.getByRole('button', { name: 'Disconnect and close' });
  await page.waitForTimeout(150);
  if (await disc.isVisible()) await disc.click();
  await expect(page.locator('.tab .title')).toHaveText(['core-sw1', 'core-sw1 (2)', 'core-sw2']);
  await page.locator('.tab', { hasText: 'core-sw1' }).first().click();
  expect(first).not.toBe(connects[2].args[0].tabId);
  // Aa in the status bar changes, resets or keeps the font size.
  await page.locator('.term:not([hidden]) .term-body').click();
  await page.keyboard.press('Control+Shift+Equal');
  await page.locator('#statusbar .font-size-btn', { hasText: 'Aa15' }).click();
  await expect(page.locator('.menu')).toContainText('Keep 15 for this session');
  await page.locator('.menu').getByText('Bigger').click();
  await expect(page.locator('#statusbar .font-size-btn')).toHaveText('Aa16');
  // The tab menu: files in one submenu, Send break, no Disconnect in the status bar.
  await page.locator('.tab', { hasText: 'core-sw2' }).click({ button: 'right' });
  await expect(page.locator('.menu').first()).toContainText('Send break');
  await page.locator('.menu').first().getByText('Files and tunnels').hover();
  await expect(page.locator('.menu.submenu')).toContainText('Get file…');
  await expect(page.locator('.menu.submenu')).toContainText('File servers…');
  await page.locator('.menu.submenu').getByText('Get file…').hover();
  await expect(page.locator('.menu.submenu')).toBeVisible();
  await page.keyboard.press('Escape');
  await page.keyboard.press('Escape');
  await expect(page.locator('#statusbar')).not.toContainText('Disconnect');
  // Break is on the status bar of serial consoles only (SSH: the menus).
  await page.locator('.tab', { hasText: 'core-sw2' }).click();
  await expect(page.locator('#statusbar').getByRole('button', { name: 'Break' })).toHaveCount(0);
  expect(errors).toEqual([]);
});

test('Commands sends from a connected session and offers examples', async ({ page }) => {
  const errors = await open(page);
  await page.click('#btn-commands');
  const lib = page.getByRole('dialog', { name: 'Command library' });
  await expect(lib).toBeVisible();
  await lib.getByRole('button', { name: 'Cancel' }).click();
  await expect(lib).toHaveCount(0);
  await connectTabs(page, ['core-sw1']);
  // Bottom left always edits the library; the status bar sends.
  await page.click('#btn-commands');
  await expect(lib).toBeVisible();
  await lib.getByRole('button', { name: 'Cancel' }).click();
  await page.locator('#statusbar').getByRole('button', { name: 'Commands…' }).click();
  const dlg = page.getByRole('dialog', { name: 'Send a command' });
  await expect(dlg).toContainText('No saved commands yet');
  await expect(dlg.getByRole('button', { name: 'Edit this session’s commands…' })).toBeVisible();
  await dlg.getByRole('button', { name: 'Add examples' }).click();
  await expect(dlg).toContainText('Interfaces');
  const saved = await calls(page, 'SaveSettings');
  expect(saved.at(-1).args[0].snippets.length).toBe(5);
  await page.keyboard.press('Escape');
  // The terminal menu has Send saved command and Save as command.
  await page.locator('.term-body').first().click({ button: 'right' });
  await expect(page.locator('.menu')).toContainText('Send saved command…');
  await expect(page.locator('.menu')).toContainText('Save as command…');
  await expect(page.locator('#statusbar')).toContainText('Commands…');
  expect(errors).toEqual([]);
});

test('commands from a team library are marked, so the backend keeps logins out of them', async ({ page }) => {
  const errors = await open(page, {
    settings: { snippets: [{ id: 'm1', name: 'Mine', text: 'show clock', return: true }] },
    teamSnippets: [{ id: 'tnoc-1', name: 'Team backup', folder: 'Team: NOC', text: 'show version', return: true }],
  });
  await connectTabs(page, ['core-sw1']);
  for (const name of ['Team backup', 'Mine']) {
    await page.locator('#statusbar').getByRole('button', { name: 'Commands…' }).click();
    const dlg = page.getByRole('dialog', { name: 'Send a command' });
    await dlg.getByRole('textbox').fill(name);
    await page.keyboard.press('Enter');
  }
  const sent = (await calls(page, 'SendText')).map(c => [c.args[1], c.args[3]]);
  expect(sent).toEqual([['show version', true], ['show clock', false]]);
  expect(errors).toEqual([]);
});

test('secure defaults: export asks for the master password, file servers share their own folder without listing', async ({ page }) => {
  const errors = await open(page);
  await page.locator('#tree .row', { hasText: 'DC Zagreb' }).click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Export group…' }).click();
  const dlg = page.getByRole('dialog', { name: 'Export sessions' });
  await dlg.getByText('Include passwords, encrypted').click();
  await expect(dlg.getByText('Your master password', { exact: true })).toBeVisible();
  await dlg.getByRole('button', { name: 'Choose file and export' }).click();
  await expect(dlg).toContainText('Enter your master password to export passwords.');
  await page.keyboard.press('Escape');
  await page.click('#btn-servers');
  const srv = page.getByRole('dialog', { name: 'File servers and tunnels' });
  await expect(srv.getByLabel('HTTP lists the folder')).not.toBeChecked();
  await expect(srv.getByPlaceholder('Folder the devices copy from and to')).toHaveValue('/home/ivan/Downloads/YACM file servers');
  expect(errors).toEqual([]);
});

test('terminal colours: session, group and Settings default with the apply question', async ({ page }) => {
  const nodes = [
    { id: 'g1', parentId: '', name: 'Production', isGroup: true, expanded: true, order: 0, defaults: { termBackground: '#3a1214' } },
    { id: 's1', parentId: 'g1', name: 'core-sw1', order: 0, session: { protocol: 'ssh', host: '10.0.0.1', port: 22 } },
    { id: 's2', parentId: '', name: 'lab-r1', order: 1, session: { protocol: 'ssh', host: '10.9.0.1', port: 22, termText: '#33ff66' } },
    { id: 's3', parentId: '', name: 'lab-r2', order: 2, session: { protocol: 'telnet', host: '10.9.0.2', port: 23 } },
  ];
  const errors = await open(page, { nodes });
  await page.locator('#tree .row.session', { hasText: 'core-sw1' }).dblclick();
  await expect(page.locator('.term').first()).toHaveCSS('background-color', 'rgb(58, 18, 20)');
  await page.click('#btn-settings');
  await page.getByRole('button', { name: 'Appearance' }).click();
  await page.locator('.term-swatches').nth(1).getByLabel('Dark blue').click();
  await page.getByRole('button', { name: 'Save settings' }).click();
  // core-sw1 takes its colour from the group and lab-r1 sets only text, so
  // two sessions use the default background.
  const q = page.getByRole('dialog', { name: 'Apply the new colours to existing sessions?' });
  await expect(q).toContainText('2 existing sessions use');
  await q.getByRole('button', { name: 'Only new sessions' }).click();
  const bulk = await calls(page, 'BulkUpdate');
  expect(bulk.length).toBe(1);
  expect(bulk[0].args[0].ids.sort()).toEqual(['s2', 's3']);
  expect(bulk[0].args[0].change).toEqual({ termBackground: 'theme' });
  const saved = await calls(page, 'SaveSettings');
  expect(saved.at(-1).args[0].termBackground).toBe('#0f1d33');
  expect(errors).toEqual([]);
});

test('sessions move and swap between split panes', async ({ page }) => {
  const errors = await open(page);
  await connectTabs(page, ['core-sw1', 'core-sw2', 'old-router']);
  await page.click('.layout-btn[data-layout="2v"]');
  const leftName = async () => page.evaluate(() => {
    const vis = [...document.querySelectorAll('.term:not([hidden])')].sort((a, b) => a.getBoundingClientRect().left - b.getBoundingClientRect().left);
    return vis.map(v => v.querySelector('.pane-label .name').textContent);
  });
  const before = await leftName();
  expect(before.length).toBe(2);
  // The name bar shows the session name only (no "null" from a missing icon).
  await expect(page.locator('.term:not([hidden]) .pane-label').first()).not.toContainText('null');
  // Swap from the menu of the left pane's label.
  await page.locator('.term:not([hidden]) .pane-label').first().click({ button: 'right' });
  await page.locator('.menu').getByText(/^Swap with/).click();
  expect(await leftName()).toEqual([before[1], before[0]]);
  // Drag a hidden tab from the tab bar onto the right pane.
  const hidden = ['core-sw1', 'core-sw2', 'old-router'].find(n => !before.includes(n));
  const dt = await page.evaluateHandle(() => new DataTransfer());
  const target = (await leftName())[1];
  const rightEl = page.locator('.term:not([hidden])', { has: page.locator('.pane-label .name', { hasText: target }) });
  await page.locator('.tab', { hasText: hidden }).dispatchEvent('dragstart', { dataTransfer: dt });
  await rightEl.dispatchEvent('dragover', { dataTransfer: dt });
  await rightEl.dispatchEvent('drop', { dataTransfer: dt });
  expect((await leftName())[1]).toBe(hidden);
  // The keyboard moves the focused session to the other pane.
  await page.locator('.term:not([hidden]) .term-body').first().click();
  await page.keyboard.press('Control+Shift+Alt+ArrowRight');
  expect((await leftName())[1]).toBe(before[1]);
  expect(errors).toEqual([]);
});

test('the quick switcher finds sessions among thousands and connects', async ({ page }) => {
  const nodes = [{ id: 'g', parentId: '', name: 'Sites', isGroup: true, expanded: false, order: 0 }];
  for (let i = 0; i < 5000; i++) nodes.push({ id: `s${i}`, parentId: 'g', name: `site${i}-sw`, order: i, session: { protocol: 'ssh', host: `10.${i >> 8}.${i & 255}.1`, port: 22, clusters: i % 100 === 0 ? ['CORE'] : [] } });
  nodes.push({ id: 'fw', parentId: 'g', name: 'edge-fw1', order: 9999, session: { protocol: 'ssh', host: '192.0.2.1', port: 22 } });
  const errors = await open(page, { nodes, recent: ['fw', 's7'] });
  await page.click('#tree');
  await page.keyboard.press('Control+Shift+O');
  const dlg = page.getByRole('dialog', { name: 'Go to session' });
  await expect(dlg).toContainText('Recent');
  await expect(dlg.locator('li[role=option]')).toHaveCount(2);
  await expect(dlg.locator('li[role=option]').first()).toContainText('edge-fw1');
  const input = dlg.getByLabel('Find a session', { exact: true });
  const t0 = Date.now();
  await input.fill('site4242');
  await expect(dlg.locator('li[role=option]').first()).toContainText('site4242-sw');
  expect(Date.now() - t0).toBeLessThan(1500);
  // An address finds the session by host and offers a quick connect too.
  await input.fill('192.0.2.1');
  await expect(dlg.locator('li[role=option]').first()).toContainText('edge-fw1');
  await expect(dlg.locator('li[role=option]').last()).toContainText('Quick connect to 192.0.2.1');
  // Several words narrow it down: a tag and part of a name.
  await input.fill('core site42');
  await expect(dlg.locator('li[role=option]').first()).toContainText('site4200-sw');
  await page.keyboard.press('Enter');
  const c = await calls(page, 'Connect');
  expect(c.at(-1).args[0].nodeId).toBe('s4200');
  // Ctrl+Enter shows the session in the tree instead.
  await page.click('#btn-switcher');
  await page.getByLabel('Find a session', { exact: true }).fill('edge-fw');
  await page.keyboard.press('Control+Enter');
  await expect(page.locator('#tree .row.selected')).toContainText('edge-fw1');
  expect(errors).toEqual([]);
});


test('a change number is asked once for the log file name', async ({ page }) => {
  const nodes = [
    { id: 'g1', parentId: '', name: 'Production', isGroup: true, expanded: true, order: 0, defaults: { askTicket: true } },
    { id: 's1', parentId: 'g1', name: 'core-sw1', order: 0, session: { protocol: 'ssh', host: '10.0.0.1', port: 22, logToFile: true } },
    { id: 's2', parentId: 'g1', name: 'core-sw2', order: 1, session: { protocol: 'ssh', host: '10.0.0.2', port: 22, logToFile: true } },
    { id: 's3', parentId: '', name: 'lab-r1', order: 1, session: { protocol: 'ssh', host: '10.9.0.1', port: 22, logToFile: true } },
  ];
  const errors = await open(page, { nodes, settings: { askTicket: 'groups', logNameTemplateOn: true, logNameTemplate: '{ticket}_{name}' } });
  // Not in a group that asks: connects straight away.
  await page.locator('#tree .row.session', { hasText: 'lab-r1' }).dblclick();
  expect((await calls(page, 'Connect')).length).toBe(1);
  // Opening the whole group asks once and passes the number to both.
  await page.locator('#tree .row.group').click({ button: 'right' });
  await page.getByText('Connect all (2)').click();
  const dlg = page.getByRole('dialog', { name: 'Change or ticket number' });
  await expect(dlg).toContainText('2 sessions');
  await dlg.getByLabel('Change or ticket number').fill('CHG0012345');
  await page.keyboard.press('Enter');
  await expect(page.locator('.tab')).toHaveCount(3);
  const c = await calls(page, 'Connect');
  expect(c.slice(1).map(x => x.args[0].ticket)).toEqual(['CHG0012345', 'CHG0012345']);
  // Settings show the template with a preview.
  await page.click('#btn-settings');
  await page.getByRole('button', { name: 'Session logs', exact: true }).click();
  await expect(page.locator('.log-preview')).toHaveText('CHG0012345_core-sw1');
  expect(errors).toEqual([]);
});

test('after a reload the tab waits for the device and reconnects', async ({ page }) => {
  const errors = await open(page);
  const [id] = await connectTabs(page, ['core-sw1']);
  await page.evaluate(i => window.__emit('tab:status', { tabId: i, state: 'closed', message: 'connection closed' }), id);
  await page.evaluate(i => window.__emit('tab:wait', { tabId: i, state: 'waiting', since: Date.now() - 65000, max: 1800 }), id);
  const overlay = page.locator('.term-overlay.waiting');
  await expect(overlay).toContainText('Waiting for core-sw1 to answer again… 1:05');
  await expect(page.locator('.tab .led.waiting')).toHaveCount(1);
  await page.locator('.tab', { hasText: 'core-sw1' }).click({ button: 'right' });
  await expect(page.locator('.menu')).toContainText('Stop waiting for the device');
  await page.keyboard.press('Escape');
  await page.evaluate(i => window.__emit('tab:wait', { tabId: i, state: 'up', since: Date.now(), max: 1800 }), id);
  await expect(page.locator('.toast')).toContainText('core-sw1 is back');
  const c = await calls(page, 'Connect');
  expect(c.length).toBe(2);
  expect(c[1].args[0].tabId).toBe(id);
  await expect(overlay).toHaveCount(0);
  expect(errors).toEqual([]);
});

test('an alert rule marks a background tab and offers to show it', async ({ page }) => {
  const rules = [
    { id: 'al-link', enabled: true, pattern: '%(?:LINK|LINEPROTO)-\\d-UPDOWN:.*changed state to down', color: 'bright-red', bold: true, alert: true },
    { id: 'hl-bad', enabled: true, pattern: '\\bdown\\b', color: 'red', ignoreCase: true },
  ];
  const errors = await open(page, { settings: { highlightRules: rules } });
  const [a, b] = await connectTabs(page, ['core-sw1', 'core-sw2']);
  await page.locator('.tab', { hasText: 'core-sw2' }).click();
  const send = (id, text) => page.evaluate(([i, t]) => window.__emit('tab:data', { tabId: i, data: btoa(t) }), [id, text]);
  // The tab on screen: no alert.
  await send(b, '%LINEPROTO-5-UPDOWN: Line protocol on Interface Gi1/0/2, changed state to down\r\n');
  // A line that only matches a colour rule: no alert.
  await send(a, 'Gi1/0/9 is administratively down\r\n');
  await expect(page.locator('.tab .alert-dot')).toHaveCount(0);
  await send(a, '*Oct 28 10:12:01: %LINEPROTO-5-UPDOWN: Line protocol on Interface Gi1/0/24, changed state to down\r\n');
  await expect(page.locator('.tab', { hasText: 'core-sw1' }).locator('.alert-dot')).toHaveCount(1);
  const toastEl = page.locator('.toast.alert');
  await expect(toastEl).toContainText('core-sw1');
  await expect(toastEl).toContainText('Gi1/0/24');
  // The same rule again within 30 s: no second alert.
  await send(a, '%LINK-3-UPDOWN: Interface Gi1/0/25, changed state to down\r\n');
  await expect(page.locator('.toast.alert')).toHaveCount(1);
  await toastEl.getByRole('button', { name: 'Show' }).click();
  await expect(page.locator('.tab.active')).toContainText('core-sw1');
  await expect(page.locator('.tab .alert-dot')).toHaveCount(0);
  expect(errors).toEqual([]);
});

test('alerts from several sessions share one message, which closes and turns alerts off', async ({ page }) => {
  const rules = [
    { id: 'al-neighbor', enabled: true, pattern: '%BGP-5-ADJCHANGE:.*Down', color: 'bright-red', bold: true, alert: true },
    { id: 'al-reload', enabled: true, pattern: '%SYS-5-RELOAD', color: 'bright-red', bold: true, alert: true },
  ];
  const errors = await open(page, { settings: { highlightRules: rules } });
  const [a, b, c] = await connectTabs(page, ['core-sw1', 'core-sw2', 'old-router']);
  await page.evaluate(id => window.__emit('tab:status', { tabId: id, state: 'connected', summary: '' }), c);
  const send = (id, text) => page.evaluate(([i, t]) => window.__emit('tab:data', { tabId: i, data: btoa(t) }), [id, text]);
  await send(a, '%BGP-5-ADJCHANGE: neighbor 10.255.0.2 Down\r\n');
  await send(b, '%BGP-5-ADJCHANGE: neighbor 10.255.0.1 Down\r\n');
  await send(a, '%SYS-5-RELOAD: Reload requested\r\n');
  const box = page.locator('.toast.alert');
  await expect(box).toHaveCount(1);
  await expect(box).toContainText('3 alerts from 2 sessions');
  await expect(box).toContainText('core-sw1');
  await expect(box).toContainText('core-sw2');
  // The menu of a shared message covers every session and kind in it.
  await box.getByRole('button', { name: 'Turn off…' }).click();
  await expect(page.getByRole('menuitem', { name: 'For these 2 sessions until their tabs close' })).toBeVisible();
  await expect(page.getByRole('menuitem', { name: 'For messages like these' })).toBeVisible();
  await page.keyboard.press('Escape');
  await box.getByRole('button', { name: 'Close the alert' }).click();
  await expect(box).toHaveCount(0);
  // Turn off for one tab: that tab stays quiet, the others still alert.
  await page.locator('.tab', { hasText: 'old-router' }).click();
  await send(b, '%SYS-5-RELOAD: Reload requested\r\n');
  await box.getByRole('button', { name: 'Turn off…' }).click();
  await page.getByRole('menuitem', { name: 'For core-sw2 until its tab closes' }).click();
  await expect(box).toHaveCount(0);
  await page.evaluate(() => { window.__now = Date.now; Date.now = () => window.__now() + 60000; });
  await send(b, '%BGP-5-ADJCHANGE: neighbor 10.255.0.9 Down\r\n');
  await page.waitForTimeout(200);
  await expect(box).toHaveCount(0);
  // Turn off all alerts: saved in Settings.
  await send(a, '%BGP-5-ADJCHANGE: neighbor 10.255.0.3 Down\r\n');
  await box.getByRole('button', { name: 'Turn off…' }).click();
  await page.getByRole('menuitem', { name: 'All alerts' }).click();
  expect((await calls(page, 'SaveSettings')).at(-1).args[0].noAlerts).toBe(true);
  expect(errors).toEqual([]);
});

test('each connection type has its own icon, also on tabs', async ({ page }) => {
  const nodes = [
    { id: 's1', parentId: '', name: 'a-ssh', order: 0, session: { protocol: 'ssh', host: 'h1' } },
    { id: 's2', parentId: '', name: 'b-telnet', order: 1, session: { protocol: 'telnet', host: 'h2' } },
    { id: 's3', parentId: '', name: 'c-serial', order: 2, session: { protocol: 'serial', serial: { device: 'COM3' } } },
    { id: 's4', parentId: '', name: 'd-rdp', order: 3, session: { protocol: 'rdp', host: 'h4' } },
  ];
  const errors = await open(page, { nodes });
  const refs = await page.locator('#tree .row .port use').evaluateAll(us => us.map(u => u.getAttribute('href')));
  expect(refs).toEqual(['#icon-ssh', '#icon-telnet', '#icon-serial', '#icon-newer']);
  await page.locator('#tree .row', { hasText: 'b-telnet' }).dblclick();
  await expect(page.locator('.tab .port use')).toHaveAttribute('href', '#icon-telnet');
  expect(errors).toEqual([]);
});

test('ad hoc highlighting adds coloured words and can keep them as a rule', async ({ page }) => {
  const errors = await open(page);
  const [id] = await connectTabs(page, ['core-sw1']);
  await page.evaluate(i => window.__emit('tab:data', { tabId: i, data: btoa('Vlan10 is up, mac aabb.cc00.0100\r\n') }), id);
  await page.locator('#statusbar').getByRole('button', { name: 'Highlight' }).click();
  const inp = page.locator('.adhoc-input');
  await inp.fill('aabb.cc00');
  await inp.press('Enter');
  await page.locator('.adhoc-input').fill('/Vlan\\d+/');
  await page.locator('.adhoc-input').press('Enter');
  await page.locator('.adhoc-input').press('Escape');
  await expect(page.locator('.adhoc-chip')).toHaveCount(2);
  await expect(page.locator('.adhoc-chip').first()).toHaveClass(/c-yellow/);
  await expect(page.locator('.adhoc-chip').nth(1)).toHaveClass(/c-cyan/);
  await page.locator('.adhoc-chip').first().locator('.word').click();
  await page.locator('.menu').getByText('Keep as a highlighting rule').click();
  const saved = await calls(page, 'SaveSettings');
  expect(saved.at(-1).args[0].highlightRules[0].pattern).toBe('aabb\\.cc00');
  await expect(page.locator('.adhoc-chip')).toHaveCount(1);
  await page.locator('.adhoc-chip .x').click();
  await expect(page.locator('.adhoc-chip')).toHaveCount(0);
  expect(errors).toEqual([]);
});

test('favourites show at the top of the tree and of Go to session', async ({ page }) => {
  const errors = await open(page, { favorites: ['s3'], recent: ['s1'] });
  await expect(page.locator('#tree .fav-head')).toContainText('Favourites');
  await expect(page.locator('#tree .row.fav')).toHaveCount(1);
  await expect(page.locator('#tree .row.fav')).toContainText('old-router');
  await page.locator('#tree .row.session:not(.fav)', { hasText: 'core-sw2' }).click({ button: 'right' });
  await page.locator('.menu').getByText('Add to favourites').click();
  await expect(page.locator('#tree .row.fav')).toHaveCount(2);
  await page.click('#btn-switcher');
  const dlg = page.getByRole('dialog', { name: 'Go to session' });
  await expect(dlg.locator('li.heading').first()).toHaveText('Favourites');
  await expect(dlg.locator('li[role=option]')).toHaveText([/old-router/, /core-sw2/, /core-sw1/]);
  await page.keyboard.press('Escape');
  await page.locator('#tree .fav-head').click();
  await expect(page.locator('#tree .row.fav')).toHaveCount(0);
  expect(errors).toEqual([]);
});

test('the file browser lists both sides and sends a file with an MD5 check', async ({ page }) => {
  const errors = await open(page);
  await page.locator('#tree .row.session', { hasText: 'core-sw1' }).click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Browse files' }).click();
  const tab = page.locator('.term.files-tab');
  await expect(tab.locator('.files-pane.remote .row')).toHaveCount(3);
  await expect(tab.locator('.files-pane.local .row')).toHaveCount(1);
  await expect(tab.locator('.files-pane.remote .roots')).toBeVisible();
  await expect(page.locator('#tabbar .tab .port.files')).toHaveCount(1);
  await expect(page.locator('#statusbar')).toContainText('SCP + device commands');
  await tab.locator('.files-pane.local .row').dblclick();
  await expect.poll(async () => (await calls(page, 'FilesUpload')).length).toBe(1);
  const up = await calls(page, 'FilesUpload');
  expect(up[0].args).toEqual(['f1', '/home/ivan/Downloads/new-image.bin', 'flash:new-image.bin', true]);
  await page.evaluate(() => window.__emit('xfer:progress', { id: 'x-up', filesId: 'f1', name: 'new-image.bin', dir: 'up', local: '/home/ivan/Downloads/new-image.bin', remote: 'flash:new-image.bin', done: 2048000, total: 2048000, state: 'done', verified: 'ok' }));
  await expect(tab.locator('.xfer.done')).toContainText('done, MD5 OK');
  // Telnet sessions cannot open it.
  await page.locator('#tree .row.session', { hasText: 'old-router' }).click({ button: 'right' });
  await expect(page.getByRole('menuitem', { name: 'Browse files' })).toBeDisabled();
  expect(errors).toEqual([]);
});

test('the file browser copies folders, resumes, and offers sudo where writing is refused', async ({ page }) => {
  const errors = await open(page, { files: { kind: 'sftp', home: '/srv', list: [
    { name: 'configs', path: '/srv/configs', size: 4096, dir: true, mod: '2026-10-20T09:12:33Z' },
    { name: 'notes.txt', path: '/srv/notes.txt', size: 120, dir: false, mod: '2026-10-20T09:12:33Z' },
  ] } });
  await page.locator('#tree .row.session', { hasText: 'core-sw1' }).click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Browse files' }).click();
  const tab = page.locator('.term.files-tab');
  await expect(tab.locator('.files-pane.remote .row')).toHaveCount(2);
  // A folder from the device.
  await tab.locator('.files-pane.remote .row', { hasText: 'configs' }).click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Copy to this computer' }).click();
  await expect.poll(async () => (await calls(page, 'FilesDownloadFolder')).map(c => c.args)).toEqual([['f1', '/srv/configs', '/home/ivan/Downloads/configs']]);
  // A folder to the device.
  await tab.locator('.files-pane.local .row', { hasText: 'new-image.bin' }).click({ button: 'right' });
  await expect(page.getByRole('menuitem', { name: 'Send to the device' })).toBeEnabled();
  await page.keyboard.press('Escape');

  // A download that stopped part-way goes on.
  await page.evaluate(() => window.__emit('xfer:progress', { id: 'x-down', filesId: 'f1', name: 'notes.txt', dir: 'down', local: '/home/ivan/Downloads/notes.txt', remote: '/srv/notes.txt', done: 60, total: 120, state: 'error', error: 'connection lost', resume: true }));
  const row = tab.locator('.xfer', { hasText: 'notes.txt' });
  await row.getByRole('button', { name: 'Resume' }).click();
  await expect.poll(async () => (await calls(page, 'FilesResume')).map(c => c.args)).toEqual([['x-down', 'f1']]);
  await expect(row).toHaveCount(0);

  // An upload the server refused: to /tmp, then the sudo command.
  await page.evaluate(() => window.__emit('xfer:progress', { id: 'x-fail', filesId: 'f1', name: 'app.conf', dir: 'up', local: '/home/ivan/Downloads/app.conf', remote: "/etc/app/it's.conf", done: 0, total: 10, state: 'error', error: 'permission denied', denied: true }));
  await tab.locator('.xfer', { hasText: 'app.conf' }).getByRole('button', { name: 'Send with sudo…' }).click();
  await page.getByRole('button', { name: 'Send to /tmp' }).click();
  await expect.poll(async () => (await calls(page, 'FilesUpload')).length).toBe(1);
  const tmpDir = '/tmp/yacm-0123456789ab';
  expect((await calls(page, 'FilesUpload'))[0].args).toEqual(['f1', '/home/ivan/Downloads/app.conf', `${tmpDir}/app.conf`, false]);
  await page.evaluate(() => window.__emit('xfer:progress', { id: 'x-up', filesId: 'f1', name: 'app.conf', dir: 'up', local: '/home/ivan/Downloads/app.conf', remote: '/tmp/x/app.conf', done: 10, total: 10, state: 'done' }));
  const dlg = page.getByRole('dialog', { name: 'Copy it into place with sudo' });
  await expect(dlg.locator('pre')).toHaveText(`sudo cp -- '${tmpDir}/app.conf' '/etc/app/it'\\''s.conf' && rm -rf -- '${tmpDir}'`);
  await dlg.getByRole('button', { name: 'Close' }).click();
  expect(errors).toEqual([]);
});

test('a new session logs to a file by default', async ({ page }) => {
  const errors = await open(page);
  await page.click('#btn-new-session');
  await page.locator('label.field', { hasText: 'Name' }).first().locator('input').fill('new-sw');
  await page.locator('label.field', { hasText: 'Host' }).first().locator('input').fill('10.9.9.9');
  await page.getByRole('button', { name: 'Create session' }).click();
  const saved = (await calls(page, 'SaveNode')).pop().args[0].session;
  expect(saved.logToFile).toBe(true);
  expect(errors).toEqual([]);
});

test('tunnels and an outbound proxy are saved with the session', async ({ page }) => {
  const errors = await open(page);
  await page.locator('#tree .row.session', { hasText: 'core-sw1' }).click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Edit' }).click();
  await page.getByRole('button', { name: 'Tunnels and proxy' }).click();
  await page.getByRole('button', { name: 'Add local tunnel' }).click();
  const row = page.locator('table.tunnels tr').nth(1);
  await row.locator('input[type=number]').nth(0).fill('8443');
  await row.locator('input[type=text]').nth(0).fill('10.0.0.1');
  await row.locator('input[type=number]').nth(1).fill('443');
  await page.locator('.field', { hasText: 'Outbound proxy' }).locator('select').selectOption('socks5');
  await page.locator('.field', { hasText: 'Proxy host' }).locator('input').fill('proxy.example.net');
  await page.getByRole('button', { name: 'Save session' }).click();
  const saved = (await calls(page, 'SaveNode')).pop().args[0].session;
  expect(saved.tunnels).toEqual([expect.objectContaining({ type: 'local', port: 8443, host: '10.0.0.1', hostPort: 443, off: false })]);
  expect(saved.proxy).toEqual(expect.objectContaining({ type: 'socks5', host: 'proxy.example.net', port: 1080 }));
  expect(errors).toEqual([]);
});

test('the file servers dialog starts a server and builds the device command', async ({ page }) => {
  const errors = await open(page);
  await page.click('#btn-servers');
  await page.locator('.srv-row', { hasText: 'SCP' }).locator('input.switch').check();
  await expect(page.locator('.srv-row.on', { hasText: 'SCP' })).toContainText('Secret123abcd');
  await page.locator('.srv-helper input[type=text]').fill('c8000v.bin');
  await expect(page.locator('.srv-cmd')).toHaveText('copy scp://yacm@10.1.1.5/c8000v.bin flash:');
  await expect(page.locator('.srv-helper .hint')).toContainText('ip ssh source-interface GigabitEthernet0');
  await page.getByRole('radio', { name: 'From the device' }).click();
  await page.getByRole('radio', { name: 'HTTP' }).click();
  await expect(page.locator('.srv-cmd')).toHaveText('copy flash:c8000v.bin http://10.1.1.5:8080/c8000v.bin');
  await page.locator('.srv-helper select').first().selectOption('linux');
  await expect(page.locator('.srv-cmd')).toHaveText('curl -fT c8000v.bin http://10.1.1.5:8080/');
  const platforms = page.locator('.srv-helper select').first().locator('option');
  await expect(platforms).toHaveText(['Cisco IOS / IOS XE', 'Cisco NX-OS', 'Cisco IOS XR', 'Arista EOS', 'Juniper Junos', 'Linux']);
  await page.locator('.srv-helper select').first().selectOption('eos');
  await expect(page.locator('.srv-cmd')).toHaveText('copy flash:c8000v.bin http://10.1.1.5:8080/c8000v.bin');
  await expect(page.locator('.srv-helper .hint')).toContainText('cli vrf MGMT');
  await page.getByRole('radio', { name: 'To the device' }).click();
  await page.getByRole('radio', { name: 'SCP' }).click();
  await expect(page.locator('.srv-cmd')).toHaveText('copy scp://yacm@10.1.1.5/c8000v.bin flash:');
  await page.locator('.srv-helper select').first().selectOption('iosxr');
  await expect(page.locator('.srv-cmd')).toHaveText('scp yacm@10.1.1.5:/c8000v.bin harddisk:/c8000v.bin');
  await page.locator('.srv-helper select').first().selectOption('nxos');
  await expect(page.locator('.srv-cmd')).toHaveText(/^copy scp:\/\/yacm@10\.1\.1\.5(:\d+)?\/c8000v\.bin bootflash: vrf management$/);
  // SFTP runs on the SCP server, with its login.
  await page.getByRole('radio', { name: 'SFTP' }).click();
  await expect(page.locator('.srv-cmd')).toHaveText(/^copy sftp:\/\/yacm@10\.1\.1\.5(:\d+)?\/c8000v\.bin bootflash: vrf management$/);
  await expect(page.locator('.srv-helper .hint')).toContainText('Secret123abcd');
  await page.locator('.srv-helper select').first().selectOption('ios');
  await expect(page.locator('.srv-cmd')).toHaveText('copy sftp://yacm@10.1.1.5/c8000v.bin flash:');
  await page.locator('.srv-helper select').first().selectOption('junos');
  await expect(page.locator('.srv-cmd')).toHaveText(/^file copy scp:\/\/yacm@10\.1\.1\.5(:\d+)?\/c8000v\.bin \/var\/tmp\/$/);
  await expect(page.locator('.srv-helper .hint')).toContainText('Junos copies over SSH with scp');
  expect(errors).toEqual([]);
});

test('one field finds sessions and connects: exact match, only match, address, or choose', async ({ page }) => {
  const nodes = [{ id: 'g', parentId: '', name: 'Sites', isGroup: true, expanded: false, order: 0 }];
  for (let i = 0; i < 3000; i++) nodes.push({ id: `s${i}`, parentId: 'g', name: `site${i}-sw`, order: i, session: { protocol: 'ssh', host: `10.${i >> 8}.${i & 255}.1`, port: 22 } });
  const errors = await open(page, { nodes });
  await expect(page.locator('#quick')).toHaveCount(0);
  const search = page.locator('#search');
  const hint = page.locator('#search-hint');
  // An exact name connects, although site1234 also matches site12340 … none here.
  await search.fill('site1234-sw');
  await expect(hint).toContainText('connects to site1234-sw');
  await page.keyboard.press('Enter');
  expect((await calls(page, 'Connect')).at(-1).args[0].nodeId).toBe('s1234');
  await expect(search).toHaveValue('');
  // An exact address connects to that session, not to 10.4.210.10…
  await search.fill('10.4.210.1');
  await expect(hint).toContainText('connects to site1234-sw');
  // An address nothing saved has: quick connect.
  await search.fill('admin@192.0.2.50:2222');
  await expect(hint).toContainText('quick connect to admin@192.0.2.50:2222');
  await page.keyboard.press('Enter');
  expect((await calls(page, 'Connect')).at(-1).args[0].quick).toBe('admin@192.0.2.50:2222');
  // The new tab takes the keyboard (a moment later on a slow machine).
  await expect(page.locator('.term:not([hidden]) .xterm-helper-textarea')).toBeFocused();
  // Several matches: Enter moves the choice into the tree, Enter there connects.
  await search.fill('site99');
  await expect(hint).toContainText('choose one of 11 sessions');
  await page.keyboard.press('Enter');
  await expect(page.locator('#tree .row.selected')).toContainText('site99-sw');
  await page.keyboard.press('Enter');
  expect((await calls(page, 'Connect')).at(-1).args[0].nodeId).toBe('s99');
  // Escape clears the filter.
  await search.fill('site9');
  await page.keyboard.press('Escape');
  await expect(search).toHaveValue('');
  await expect(hint).toBeHidden();
  expect(errors).toEqual([]);
});

test('right click in a text field offers Paste', async ({ page }) => {
  const errors = await open(page);
  await page.evaluate(() => { window.__clipboard = 'core-sw2'; });
  await page.locator('#search').click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Paste' }).click();
  await expect(page.locator('#search')).toHaveValue('core-sw2');
  await expect(page.locator('#tree .row.session')).toHaveCount(1);
  await page.locator('#search').selectText();
  await page.locator('#search').click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Copy' }).click();
  await expect.poll(() => page.evaluate(() => window.__clipboard)).toBe('core-sw2');
  // In the terminal the terminal's own menu stays.
  await page.locator('#search').fill('');
  await page.locator('#tree .row.session', { hasText: 'core-sw1' }).dblclick();
  await page.locator('.term:not([hidden]) .term-body').click({ button: 'right' });
  await expect(page.locator('.menu')).toContainText('Send saved command…');
  expect(errors).toEqual([]);
});

test('the jump host session is picked by searching', async ({ page }) => {
  const nodes = [{ id: 'g', parentId: '', name: 'DC', isGroup: true, expanded: true, order: 0 }, { id: 'j', parentId: '', name: 'Jump hosts', isGroup: true, expanded: false, order: 1 }];
  for (let i = 0; i < 5000; i++) nodes.push({ id: `b${i}`, parentId: 'j', name: `bastion-${i}`, order: i, session: { protocol: 'ssh', host: `10.250.${i >> 8}.${i & 255}`, port: 22 } });
  nodes.push({ id: 'used', parentId: 'j', name: 'main-jump', order: 9999, session: { protocol: 'ssh', host: '192.0.2.10', port: 22 } });
  nodes.push({ id: 'd1', parentId: 'g', name: 'dist-sw1', order: 0, session: { protocol: 'ssh', host: '10.0.0.11', port: 22, user: 'admin', jumpVia: 'used' } });
  nodes.push({ id: 'd2', parentId: 'g', name: 'dist-sw2', order: 1, session: { protocol: 'ssh', host: '10.0.0.12', port: 22, user: 'admin' } });
  const errors = await open(page, { nodes });
  await page.locator('#tree .row.session', { hasText: 'dist-sw2' }).click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Edit' }).click();
  await page.getByRole('button', { name: 'Jump hosts' }).click();
  await page.locator('.field', { hasText: 'Route' }).locator('select').selectOption('session');
  const picker = page.getByRole('combobox', { name: 'Jump host session' });
  await picker.click();
  const list = page.locator('.picker-list');
  // Before typing: the session already used as a jump host comes first.
  await expect(list.locator('li[role=option]').first()).toContainText('main-jump');
  await expect(list).toContainText('type to find one');
  const t0 = Date.now();
  await picker.fill('bastion-4321');
  await expect(list.locator('li[role=option]').first()).toContainText('bastion-4321');
  expect(Date.now() - t0).toBeLessThan(1500);
  await expect(list.locator('li[role=option]').first()).toContainText('Jump hosts');
  await page.keyboard.press('Enter');
  await expect(picker).toHaveValue('bastion-4321 (10.250.16.225)');
  // Escape closes the list, not the editor.
  await picker.click();
  await expect(list).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(list).toBeHidden();
  await expect(page.getByRole('button', { name: 'Save session' })).toBeVisible();
  await page.getByRole('button', { name: 'Save session' }).click();
  expect((await calls(page, 'SaveNode')).pop().args[0].session.jumpVia).toBe('b4321');
  expect(errors).toEqual([]);
});

test('Copy address copies user@host of one or several sessions', async ({ page }) => {
  const errors = await open(page);
  await page.locator('#tree .row.session', { hasText: 'core-sw1' }).click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Copy address' }).click();
  await expect.poll(() => page.evaluate(() => window.__clipboard)).toBe('admin@10.0.0.1');
  const rows = page.locator('#tree .row.session');
  await rows.nth(0).click();
  await rows.nth(1).click({ modifiers: ['Control'] });
  await rows.nth(1).click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Copy addresses' }).click();
  await expect.poll(() => page.evaluate(() => window.__clipboard)).toBe('admin@10.0.0.1\nadmin@10.0.0.2');
  // The group menu says how many sessions Connect all opens.
  await page.locator('#tree .row.group').click({ button: 'right' });
  await expect(page.getByRole('menuitem', { name: 'Connect all (2)' })).toBeVisible();
  expect(errors).toEqual([]);
});

test('File servers lists the tunnels of all sessions and stops them', async ({ page }) => {
  const tunnels = [{ id: 't1', type: 'local', port: 8443, host: '10.0.0.1', hostPort: 443, listen: '127.0.0.1:8443', state: 'running', conns: 1, total: 3, in: 2048, out: 512 }];
  const errors = await open(page, { tunnels });
  const [tabId] = await connectTabs(page, ['core-sw1']);
  await page.evaluate(id => window.__emit('tab:tunnels', { tabId: id, tunnels: [{ id: 't1', state: 'running' }] }), tabId);
  await expect(page.locator('#btn-servers')).toHaveAttribute('data-tip', /1 tunnel running/);
  await expect(page.locator('#btn-servers')).toHaveClass(/live/);
  await page.click('#btn-servers');
  const dlg = page.getByRole('dialog', { name: 'File servers and tunnels' });
  const row = dlg.locator('.tun-row', { hasText: 'core-sw1' });
  await expect(row).toContainText('127.0.0.1:8443 → 10.0.0.1:443');
  await row.getByRole('button', { name: 'Stop' }).click();
  expect((await calls(page, 'TunnelStop')).at(-1).args).toEqual([tabId, 't1']);
  await row.getByRole('button', { name: 'Go to tab' }).click();
  await expect(dlg).toHaveCount(0);
  expect(errors).toEqual([]);
});

test('Break: the status bar of a serial console, the menus and Ctrl+Break for all', async ({ page }) => {
  const nodes = [
    { id: 's3', parentId: '', name: 'old-router', order: 0, session: { protocol: 'telnet', host: '10.9.9.9', port: 23 } },
    { id: 'c1', parentId: '', name: 'console', order: 1, session: { protocol: 'serial', serial: { device: 'COM3', baud: 9600 } } },
  ];
  const errors = await open(page, { nodes });
  await page.locator('#tree .row.session', { hasText: 'old-router' }).dblclick();
  let [tabId] = (await calls(page, 'Connect')).map(c => c.args[0].tabId);
  await page.evaluate(id => window.__emit('tab:status', { tabId: id, state: 'connected', summary: 'telnet' }), tabId);
  // Telnet (and SSH): not on the status bar, but in the menus and on Ctrl+Break.
  await expect(page.locator('#statusbar').getByRole('button', { name: 'Break' })).toHaveCount(0);
  await page.locator('.term:not([hidden]) .term-body').click();
  await page.keyboard.press('Control+Pause');
  await expect.poll(async () => (await calls(page, 'SendBreak')).length).toBe(1);
  await page.locator('.term:not([hidden]) .term-body').click({ button: 'right' });
  await page.getByRole('menuitem', { name: /Send break/ }).click();
  await expect.poll(async () => (await calls(page, 'SendBreak')).length).toBe(2);
  await page.locator('.tab', { hasText: 'old-router' }).click({ button: 'right' });
  await page.getByRole('menuitem', { name: /Send break/ }).click();
  await expect.poll(async () => (await calls(page, 'SendBreak')).length).toBe(3);
  // A serial console has it on the status bar.
  await page.locator('#tree .row.session', { hasText: 'console' }).dblclick();
  tabId = (await calls(page, 'Connect')).at(-1).args[0].tabId;
  await page.evaluate(id => window.__emit('tab:status', { tabId: id, state: 'connected', summary: 'serial COM3 · 9600 8N1' }), tabId);
  await page.locator('#statusbar').getByRole('button', { name: 'Break' }).click();
  await expect.poll(async () => (await calls(page, 'SendBreak')).length).toBe(4);
  expect(errors).toEqual([]);
});

test('the bar on the left: icons with labels, the session list folds away and back', async ({ page }) => {
  const errors = await open(page);
  const bar = page.locator('#activity');
  await expect(bar.locator('.act-btn')).toHaveCount(6);
  for (const label of ['Sessions', 'Saved commands', 'Credential profiles', 'File servers and tunnels', 'Settings', 'Lock']) {
    await expect(bar.getByRole('button', { name: label, exact: true })).toBeVisible();
  }
  await bar.getByRole('button', { name: 'Settings', exact: true }).click();
  await expect(page.getByRole('dialog', { name: 'Settings' })).toBeVisible();
  await page.getByRole('button', { name: 'Cancel' }).click();
  // Sessions folds the list away (and remembers it), and brings it back.
  await page.locator('#btn-sessions').click();
  await expect(page.locator('#sidebar')).toBeHidden();
  expect((await calls(page, 'SaveSettings')).at(-1).args[0].sidebarHidden).toBe(true);
  await page.locator('#btn-sessions').click();
  await expect(page.locator('#sidebar')).toBeVisible();
  expect((await calls(page, 'SaveSettings')).at(-1).args[0].sidebarHidden).toBe(false);
  // Showing a session from Go to session brings the list back.
  await page.locator('#btn-sessions').click();
  await page.keyboard.press('Control+Shift+O');
  await page.getByLabel('Find a session', { exact: true }).fill('core-sw2');
  await page.keyboard.press('Control+Enter');
  await expect(page.locator('#sidebar')).toBeVisible();
  await expect(page.locator('#tree .row.selected')).toContainText('core-sw2');
  // The vault padlock says what a click does.
  await expect(page.locator('#btn-lock')).toHaveAttribute('data-tip', /unlocked, click to lock it/);
  expect(errors).toEqual([]);
});

test('Save command output goes through the session, with common commands, also for telnet', async ({ page }) => {
  const errors = await open(page);
  await page.locator('#tree .row.session', { hasText: 'old-router' }).dblclick();
  const [tabId] = (await calls(page, 'Connect')).map(c => c.args[0].tabId);
  await page.evaluate(id => window.__emit('tab:status', { tabId: id, state: 'connected', summary: 'telnet' }), tabId);
  await page.locator('#statusbar').getByRole('button', { name: 'Files ▾' }).click();
  await expect(page.locator('.menu')).not.toContainText('Get show tech');
  await page.locator('.menu').getByText('Save command output to file…').click();
  const dlg = page.getByRole('dialog', { name: /Save command output/ });
  await dlg.getByLabel('Common commands', { exact: true }).selectOption('display current-configuration');
  await expect(dlg.locator('input.mono-input')).toHaveValue('display current-configuration');
  await dlg.getByRole('button', { name: 'Save to file…' }).click();
  await expect.poll(async () => (await calls(page, 'SessionCommandToFile')).length).toBe(1);
  const [c] = await calls(page, 'SessionCommandToFile');
  expect(c.args[0]).toBe(tabId);
  expect(c.args[1]).toBe('display current-configuration');
  expect(c.args[2]).toMatch(/current-configuration_old-router_\d{4}-\d{2}-\d{2}\.txt$/);
  expect(errors).toEqual([]);
});

test('File servers types into the session in front, and the device check is not an error', async ({ page }) => {
  const errors = await open(page);
  await connectTabs(page, ['core-sw1']);
  await page.click('#btn-servers');
  const dlg = page.getByRole('dialog', { name: 'File servers and tunnels' });
  const type = dlg.getByRole('button', { name: 'Type into core-sw1' });
  await expect(type).toBeEnabled();
  await expect(dlg.getByLabel('Session to type into')).toHaveValue(/.+/);
  await type.click();
  const sent = await calls(page, 'SendText');
  expect(sent.at(-1).args[1]).toMatch(/^copy /);
  // IOS XE reads part of a file and stops before the real copy: no red message.
  await page.evaluate(() => window.__emit('fileserver:event', { kind: 'tftp', remote: '194.152.210.33', file: 'isr4400.bin', upload: false, state: 'checked', done: 32768 }));
  await expect(page.locator('#toasts')).not.toContainText('stopped');
  await page.click('#btn-servers');
  await expect(page.locator('.srv-ev.checked')).toContainText('stopped by the device');
  expect(errors).toEqual([]);
});

test('publishing warns about sessions that log in without a credential profile', async ({ page }) => {
  const errors = await open(page);
  await page.locator('#tree .row.group[data-id=g1]').click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Publish as team library…' }).click();
  const dlg = page.getByRole('dialog', { name: 'Publish as team library' });
  await expect(dlg.locator('.warning')).toContainText('2 sessions log in without a credential profile (user admin)');
  expect(errors).toEqual([]);
});

test('the transfers panel folds while a transfer runs; profiles and team libraries are easy to find', async ({ page }) => {
  const errors = await open(page);
  const [tabId] = await connectTabs(page, ['core-sw1']);
  await page.locator('#statusbar').getByRole('button', { name: 'Files ▾' }).click();
  await page.locator('.menu').getByText('Save command output to file…').click();
  await page.getByRole('dialog', { name: /Save command output/ }).getByRole('button', { name: 'Save to file…' }).click();
  await page.evaluate(() => window.__emit('xfer:progress', { id: 'x-cmd', filesId: '', name: 'show tech-support', dir: 'cmd', local: '/tmp/t.txt', remote: 'show tech-support', done: 1000, total: 0, state: 'running' }));
  const panel = page.locator('.xfer-panel');
  await expect(panel).toContainText('Transfers: 1 running');
  await panel.getByRole('button', { name: 'Hide' }).click();
  await expect(panel).toHaveClass(/folded/);
  await expect(panel.locator('.rows')).toHaveCount(0);
  await panel.getByRole('button', { name: /Transfers: 1 running/ }).click();
  await expect(panel.locator('.rows')).toBeVisible();
  await page.evaluate(() => window.__emit('xfer:progress', { id: 'x-cmd', filesId: '', name: 'show tech-support', dir: 'cmd', local: '/tmp/t.txt', remote: 'show tech-support', done: 5000, total: 0, state: 'done' }));
  await panel.getByRole('button', { name: 'Close' }).click();
  await expect(panel).toHaveCount(0);
  // New profile is the main button of the profiles window.
  await page.click('#btn-profiles');
  await expect(page.getByRole('button', { name: 'New profile' })).toHaveClass(/primary/);
  await page.keyboard.press('Escape');
  // The import menu leads to team libraries.
  await page.click('#btn-more');
  await page.getByRole('menuitem', { name: 'Team libraries…' }).click();
  await expect(page.getByRole('dialog', { name: 'Settings' })).toContainText('Team libraries');
  expect(errors).toEqual([]);
  void tabId;
});

test('From the device: Choose… lists the files of the device and builds the command with its file system', async ({ page }) => {
  const errors = await open(page);
  await connectTabs(page, ['core-sw1']);
  await page.click('#btn-servers');
  await page.getByRole('radio', { name: 'From the device' }).click();
  await page.getByRole('radio', { name: 'HTTP' }).click();
  await page.locator('.srv-helper').getByRole('button', { name: 'Choose…' }).click();
  const pick = page.getByRole('dialog', { name: 'Choose a file on core-sw1' });
  await pick.locator('.files-pane .row', { hasText: 'vlan.dat' }).dblclick();
  await expect(pick).toHaveCount(0);
  await expect(page.locator('.srv-cmd')).toHaveText('copy flash:vlan.dat http://10.1.1.5:8080/vlan.dat');
  // The address the device reaches is marked in the list (no extra line).
  await expect(page.locator('.srv-helper select').nth(1)).toBeVisible();
  expect(errors).toEqual([]);
});

test('Page Up and Page Down scroll the terminal, except in full-screen programs', async ({ page }) => {
  const errors = await open(page);
  const [tabId] = await connectTabs(page, ['core-sw1']);
  const lines = Array.from({ length: 300 }, (_, i) => `line ${i}`).join('\r\n') + '\r\ncore-sw1#';
  await page.evaluate(([id, d]) => window.__emit('tab:data', { tabId: id, data: btoa(d) }), [tabId, lines]);
  const rows = page.locator('.term:not([hidden]) .xterm-rows');
  await expect(rows).toContainText('core-sw1#');
  await page.locator('.term:not([hidden]) .term-body').click();
  const writes = async () => (await calls(page, 'Write')).map(c => c.args[1]).join('');
  await page.keyboard.press('PageUp');
  await expect(rows).not.toContainText('core-sw1#');
  await page.keyboard.press('PageDown');
  await page.keyboard.press('PageDown');
  await expect(rows).toContainText('core-sw1#');
  expect(await writes()).not.toContain('\x1b[5~');
  // less, vi, top (the alternate screen) get the keys.
  await page.evaluate(id => window.__emit('tab:data', { tabId: id, data: btoa('\x1b[?1049h') }), tabId);
  // xterm takes the switch a moment later: press until the device gets it.
  await expect.poll(async () => { await page.keyboard.press('PageUp'); return writes(); }).toContain('\x1b[5~');
  expect(errors).toEqual([]);
});

test('file names with spaces go to a device with underscores', async ({ page }) => {
  const errors = await open(page, { local: [{ name: 'cbr copy.txt', path: '/home/ivan/Downloads/cbr copy.txt', size: 732, dir: false, mod: '2026-11-03T12:00:00Z' }] });
  await page.locator('#tree .row.session', { hasText: 'core-sw1' }).click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Browse files' }).click();
  const tab = page.locator('.term.files-tab');
  await tab.locator('.files-pane.local .row', { hasText: 'cbr copy.txt' }).dblclick();
  const dlg = page.getByRole('dialog', { name: 'Spaces in file names' });
  await expect(dlg).toContainText('cbr copy.txt → cbr_copy.txt');
  await dlg.getByRole('button', { name: 'Send with underscores' }).click();
  await expect.poll(async () => (await calls(page, 'FilesUpload')).length).toBe(1);
  expect((await calls(page, 'FilesUpload'))[0].args.slice(1, 3)).toEqual(['/home/ivan/Downloads/cbr copy.txt', 'flash:cbr_copy.txt']);
  expect(errors).toEqual([]);
});

test('File servers: port 69 can be allowed on Linux, names with spaces, a tunnel through a direct session', async ({ page }) => {
  const errors = await open(page, { lowPorts: true });
  await connectTabs(page, ['core-sw1']);
  await page.click('#btn-servers');
  await page.locator('.srv-row', { hasText: 'TFTP' }).locator('input.switch').check();
  const ask = page.getByRole('dialog', { name: 'Allow port 69?' });
  await expect(ask).toContainText('other programs do not get it');
  await ask.getByRole('button', { name: 'Allow port 69…' }).click();
  await expect.poll(async () => (await calls(page, 'FileServerAllowPort')).map(c => c.args[0])).toEqual([69]);
  await expect(page.locator('.srv-row.on', { hasText: 'TFTP' })).toHaveCount(1);
  // A name with spaces: the device asks for underscores, the server maps it.
  await page.locator('.srv-helper').getByRole('radio', { name: 'TFTP' }).click();
  await page.locator('.srv-helper input[type=text]').fill('cbr copy.txt');
  await expect(page.locator('.srv-cmd')).toHaveText('copy tftp://10.1.1.5/cbr_copy.txt flash:');
  await expect(page.locator('.srv-helper .hint')).toContainText('asks for cbr_copy.txt');
  // A directly connected SSH session can carry a tunnel (VPN, NAT).
  await page.getByRole('radio', { name: 'HTTP' }).click();
  await page.locator('.srv-row', { hasText: 'HTTP' }).locator('input.switch').check();
  await page.locator('.srv-helper').getByRole('button', { name: 'Open a tunnel through core-sw1' }).click();
  await expect(page.locator('.srv-cmd')).toHaveText('copy http://127.0.0.1:8080/cbr_copy.txt flash:');
  expect(errors).toEqual([]);
});

test('the update check is off by default and never offered at start', async ({ page }) => {
  await page.clock.install();
  const errors = await open(page, { settings: { updateCheck: '' }, update: { current: '0.9.0-rc.1', newer: true, latest: '0.9.0', url: 'https://github.com/yacm-app/yacm/releases/tag/v0.9.0' } });
  await page.clock.fastForward(3 * 3600 * 1000);
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(page.locator('.update-toast')).toHaveCount(0);
  expect(await calls(page, 'CheckForUpdate')).toEqual([]);
  // Settings > General: off, with the note that stores and package managers update YACM.
  await page.locator('#btn-settings').click();
  const dlg = page.getByRole('dialog', { name: 'Settings' });
  const box = dlg.getByLabel('Check for new versions once a day');
  await expect(box).not.toBeChecked();
  await expect(dlg).toContainText('YACM never updates itself');
  expect(errors).toEqual([]);
});

test('when switched on, the update check reports a newer version with a download link', async ({ page }) => {
  await page.clock.install();
  const errors = await open(page, { settings: { updateCheck: 'daily' }, update: { current: '0.9.0-rc.1', newer: true, latest: '0.9.0', url: 'https://github.com/yacm-app/yacm/releases/tag/v0.9.0' } });
  await page.clock.fastForward(21000);
  const note = page.locator('.update-toast');
  await expect(note).toContainText('YACM 0.9.0 is out.');
  await note.getByRole('button', { name: 'Download' }).click();
  expect(await page.evaluate(() => window.__opened)).toEqual(['https://github.com/yacm-app/yacm/releases/tag/v0.9.0']);
  expect(errors).toEqual([]);
});

test('About shows the notices, the licence and terms, support and a manual update check', async ({ page }) => {
  const errors = await open(page);
  await page.click('#btn-settings');
  const dlg = page.getByRole('dialog', { name: 'Settings' });
  await dlg.locator('.editor > nav button', { hasText: 'About' }).click();
  await expect(dlg).toContainText('Copyright © 2026 The YACM Authors');
  await expect(dlg).toContainText('ABSOLUTELY NO WARRANTY');
  await dlg.getByRole('button', { name: 'Licence and terms' }).click();
  const legal = page.getByRole('dialog', { name: 'Licence and terms' });
  await expect(legal.locator('.legal-text')).toContainText('Additional terms under section 7 of the GNU GPL');
  await legal.getByRole('radio', { name: 'GNU GPL version 3' }).click();
  await expect(legal.locator('.legal-text')).toContainText('GNU GENERAL PUBLIC LICENSE');
  await legal.getByRole('button', { name: 'Close' }).click();
  await dlg.getByRole('button', { name: 'Support YACM' }).click();
  expect(await page.evaluate(() => window.__opened)).toEqual(['https://github.com/yacm-app/yacm#support-yacm']);
  await dlg.getByRole('button', { name: 'Check for updates' }).click();
  await expect(page.locator('#toasts')).toContainText('is the newest version');
  expect(errors).toEqual([]);
});

test('Shift+Insert pastes once, and Ctrl+Shift+Tab goes to the previous tab also on Linux', async ({ page }) => {
  const errors = await open(page);
  const ids = await connectTabs(page, ['core-sw1', 'core-sw2']);
  await page.evaluate(() => { window.__clipboard = 'show version'; });
  await page.locator('.term:not([hidden]) .term-body').click();
  const writes = async () => (await calls(page, 'Write')).filter(c => c.args[0] === ids[1]).map(c => c.args[1]).join('');
  // The webview pastes Shift+Insert by itself as well: that paste is skipped.
  await page.keyboard.press('Shift+Insert');
  await page.evaluate(() => {
    const ev = new Event('paste', { bubbles: true, cancelable: true });
    ev.clipboardData = { getData: () => 'show version' };
    document.querySelector('.term:not([hidden]) .xterm-helper-textarea').dispatchEvent(ev);
  });
  await expect.poll(writes).toContain('show version');
  await page.waitForTimeout(300);
  expect((await writes()).split('show version').length - 1).toBe(1);
  // WebKitGTK reports Shift+Tab as ISO_Left_Tab.
  const active = () => page.locator('.tab.active').innerText();
  expect(await active()).toContain('core-sw2');
  await page.evaluate(() => document.querySelector('.term:not([hidden]) .xterm-helper-textarea').dispatchEvent(
    new KeyboardEvent('keydown', { key: 'ISO_Left_Tab', code: 'Tab', ctrlKey: true, shiftKey: true, bubbles: true, cancelable: true })));
  await expect.poll(active).toContain('core-sw1');
  expect(errors).toEqual([]);
});

test('a transfer from a terminal tab goes on when the terminal session ends, with speed and time left', async ({ page }) => {
  const errors = await open(page, { chooseFiles: ['/home/ivan/Downloads/cat9k_iosxe.17.15.06.SPA.bin'] });
  const [tabId] = await connectTabs(page, ['core-sw1']);
  await page.locator('#statusbar').getByRole('button', { name: 'Files ▾' }).click();
  await page.locator('.menu').getByText('Send file…').click();
  await page.getByRole('dialog', { name: /Send cat9k/ }).getByRole('button', { name: 'Send' }).click();
  await expect.poll(async () => (await calls(page, 'FilesUpload')).length).toBe(1);
  const ev = (done, state, extra = {}) => page.evaluate(e => window.__emit('xfer:progress', e), { id: 'x-up', filesId: 'f1', name: 'cat9k_iosxe.17.15.06.SPA.bin', dir: 'up', local: '/home/ivan/Downloads/cat9k_iosxe.17.15.06.SPA.bin', remote: 'flash:cat9k_iosxe.17.15.06.SPA.bin', done, total: 1300000000, state, hint: 'Slow? SCP to a network device is as fast as its SSH window allows', ...extra });
  await ev(0, 'running');
  await page.waitForTimeout(1500);
  await ev(10000000, 'running');
  await page.waitForTimeout(2000);
  await ev(20000000, 'running');
  const panel = page.locator('.xfer-panel');
  await expect(panel).toContainText('/s');
  await expect(panel).toContainText(/min left|h left/);
  await expect(panel).toContainText('SSH window');
  // The device logs the terminal out (exec-timeout): the transfer goes on.
  await page.evaluate(id => window.__emit('tab:status', { tabId: id, state: 'closed' }), tabId);
  await page.waitForTimeout(300);
  expect((await calls(page, 'FilesClose')).length).toBe(0);
  await ev(1300000000, 'done');
  await expect.poll(async () => (await calls(page, 'FilesClose')).length).toBe(1);
  expect(errors).toEqual([]);
});

test('the mouse wheel moves the tab bar, and the active tab stays in view', async ({ page }) => {
  const errors = await open(page);
  await page.setViewportSize({ width: 1000, height: 700 });
  const search = page.locator('#search');
  for (let i = 1; i <= 25; i++) {
    await search.fill(`admin@192.0.2.${i}`);
    await page.keyboard.press('Enter');
    await expect(page.locator('.tab')).toHaveCount(i);
    await expect(page.locator('.term:not([hidden]) .xterm-helper-textarea')).toBeFocused();
  }
  await expect(page.locator('.tab')).toHaveCount(25);
  const bar = page.locator('#tabbar');
  const scroll = () => bar.evaluate(b => b.scrollLeft);
  const inView = sel => page.locator(sel).evaluate(el => { const b = el.parentElement.getBoundingClientRect(), r = el.getBoundingClientRect(); return r.left >= b.left - 1 && r.right <= b.right + 1; });
  // The newest tab is active and in view.
  expect(await scroll()).toBeGreaterThan(0);
  expect(await inView('.tab.active')).toBe(true);
  // The wheel scrolls sideways.
  await bar.hover();
  await page.mouse.wheel(0, -5000);
  await expect.poll(scroll).toBe(0);
  await page.mouse.wheel(0, 300);
  await expect.poll(scroll).toBeGreaterThan(0);
  // Going to the first tab with the keyboard brings it into view.
  await page.mouse.wheel(0, 5000);
  await page.locator('.tab.active').click();
  await page.keyboard.press('Control+Tab');
  await expect.poll(() => inView('.tab.active')).toBe(true);
  expect(await page.locator('.tab.active .title').textContent()).toBe('admin@192.0.2.1');
  expect(errors).toEqual([]);
});

test('a tab not on screen shows new output in its name, not the echo of what was sent', async ({ page }) => {
  const errors = await open(page);
  const [sw1, sw2] = await connectTabs(page, ['core-sw1', 'core-sw2']);
  const send = (id, text) => page.evaluate(([i, t]) => window.__emit('tab:data', { tabId: i, data: btoa(t) }), [id, text]);
  const tab1 = page.locator('.tab', { hasText: 'core-sw1' });
  // The first seconds after connecting (banner, login) do not count.
  await send(sw1, 'Welcome\r\n');
  await expect(tab1).not.toHaveClass(/news/);
  await page.waitForTimeout(3100);
  // Anti-idle's echo does not count either.
  await page.evaluate(i => window.__emit('tab:typed', { tabId: i }), sw1);
  await send(sw1, '!');
  await expect(tab1).not.toHaveClass(/news/);
  await page.waitForTimeout(1600);
  await send(sw1, '%LINK-3-UPDOWN: Interface Gi1/0/1, changed state to down\r\n');
  await expect(tab1).toHaveClass(/news news-red/);
  // The tab on screen is never marked; showing the tab clears it.
  await send(sw2, 'output\r\n');
  await expect(page.locator('.tab', { hasText: 'core-sw2' })).not.toHaveClass(/news/);
  await tab1.click();
  await expect(tab1).not.toHaveClass(/news/);
  // The tab menu turns it off for one tab.
  await page.locator('.tab', { hasText: 'core-sw2' }).click({ button: 'right' });
  await page.locator('.menu').getByText('Do not mark new output in this tab').click();
  await page.locator('.tab', { hasText: 'core-sw2' }).click();
  await tab1.click();
  await send(sw2, 'more output\r\n');
  await expect(page.locator('.tab', { hasText: 'core-sw2' })).not.toHaveClass(/news/);
  expect(errors).toEqual([]);
});

test('the session list has its own size: Ctrl + wheel over it, Ctrl +/-/0 while it has the focus', async ({ page }) => {
  const errors = await open(page);
  const zoom = () => page.locator('#sidebar').evaluate(el => getComputedStyle(el).getPropertyValue('--tree-zoom').trim());
  const rowHeight = () => page.locator('#tree .row').first().evaluate(el => el.getBoundingClientRect().height);
  const before = await rowHeight();
  await page.locator('#tree').hover();
  await page.keyboard.down('Control');
  await page.mouse.wheel(0, -100);
  await page.keyboard.up('Control');
  await expect.poll(zoom).toBe('1.1');
  await page.locator('#tree').focus();
  await page.keyboard.press('Control+Equal');
  await expect.poll(zoom).toBe('1.2');
  expect(await rowHeight()).toBeGreaterThan(before * 1.15);
  await expect.poll(async () => (await calls(page, 'SaveSettings')).at(-1)?.args[0].treeSize).toBe(120);
  await page.keyboard.press('Control+Minus');
  await page.keyboard.press('Control+Minus');
  await page.keyboard.press('Control+Minus');
  await expect.poll(zoom).toBe('0.9');
  await page.keyboard.press('Control+0');
  await expect.poll(zoom).toBe('1');
  await expect.poll(async () => (await calls(page, 'SaveSettings')).at(-1)?.args[0].treeSize).toBe(0);
  // In a terminal the keys still size the terminal.
  const [id] = await connectTabs(page, ['core-sw1']);
  await page.locator('.term:not([hidden]) .term-body').click();
  await page.keyboard.press('Control+Shift+Equal');
  await expect(page.locator('#statusbar .font-size-btn')).toContainText('Aa15');
  await expect.poll(zoom).toBe('1');
  expect(id).toBeTruthy();
  expect(errors).toEqual([]);
});

test('a middle click pastes the last selection or the clipboard, as set, and nothing by default', async ({ page }) => {
  const errors = await open(page);
  const [id] = await connectTabs(page, ['core-sw1']);
  await page.evaluate(i => window.__emit('tab:data', { tabId: i, data: btoa('R1#show clock\r\n12:00:01 CET\r\nR1#') }), id);
  const body = page.locator('.term:not([hidden]) .term-body');
  const writes = async () => (await calls(page, 'Write')).map(c => c.args[1]).join('');
  // Select "show clock" with a double click and a drag.
  const box = await body.boundingBox();
  await page.mouse.move(box.x + 30, box.y + 10);
  await page.mouse.down();
  await page.mouse.move(box.x + 100, box.y + 10, { steps: 5 });
  await page.mouse.up();
  await page.evaluate(() => { window.__clipboard = 'from clipboard'; });
  await page.mouse.click(box.x + 200, box.y + 200, { button: 'middle' });
  await page.waitForTimeout(200);
  expect(await writes()).toBe('');
  for (const [how, want] of [['selection', /how/], ['clipboard', /from clipboard/]]) {
    await page.locator('#btn-settings').click();
    await page.getByRole('button', { name: 'Terminal', exact: true }).click();
    await page.getByRole('radio', { name: how === 'selection' ? 'Paste the selection' : 'Paste the clipboard' }).click();
    await page.getByRole('button', { name: 'Save settings' }).click();
    const n = (await calls(page, 'Write')).length;
    await page.mouse.click(box.x + 200, box.y + 200, { button: 'middle' });
    await expect.poll(async () => (await calls(page, 'Write')).slice(n).map(c => c.args[1]).join('')).toMatch(want);
  }
  expect(errors).toEqual([]);
});

test('cut, copy and paste in the session list: sessions and groups, menus and keys', async ({ page }) => {
  const errors = await open(page);
  const row = name => page.locator('#tree .row', { hasText: name }).first();
  const menu = page.locator('.menu');
  // Copy a session and paste it after old-router (top level).
  await row('core-sw1').click({ button: 'right' });
  await menu.getByText('Copy', { exact: true }).click();
  await row('old-router').click({ button: 'right' });
  await menu.getByText('Paste', { exact: true }).click();
  let c = (await calls(page, 'CopyNodes')).at(-1).args;
  expect(c).toEqual([['s1'], '', 2]);
  await expect(page.locator('#tree .row.session', { hasText: 'core-sw1' })).toHaveCount(2);
  // A group cannot go into itself.
  await row('DC Zagreb').click({ button: 'right' });
  await menu.getByText('Copy', { exact: true }).click();
  await row('core-sw2').click({ button: 'right' });
  await expect(menu.getByRole('menuitem', { name: /^Paste/ }).or(menu.locator('button', { hasText: /^Paste/ })).first()).toBeDisabled();
  await page.keyboard.press('Escape');
  // Cut with the keyboard: dimmed until pasted, Ctrl+V on a group moves it in.
  await row('old-router').click();
  await page.keyboard.press('Control+x');
  await expect(row('old-router')).toHaveClass(/cut/);
  await row('DC Zagreb').click();
  await page.keyboard.press('Control+v');
  await expect.poll(async () => (await calls(page, 'MoveNode')).at(-1)?.args.slice(0, 2)).toEqual(['s3', 'g1']);
  await expect(page.locator('#tree .row.cut')).toHaveCount(0);
  // Copy with the keyboard and paste at the top level from the empty area.
  await row('core-sw2').click();
  await page.keyboard.press('Control+c');
  await page.locator('#tree').click({ button: 'right', position: { x: 50, y: 400 } });
  await menu.getByText('Paste', { exact: true }).click();
  c = (await calls(page, 'CopyNodes')).at(-1).args;
  expect(c).toEqual([['s2'], '', -1]);
  expect(errors).toEqual([]);
});

test('editing sessions together switches telnet to SSH and offers to drop the telnet login rules', async ({ page }) => {
  const login = [{ id: 'u', active: true, expect: 'Username:', send: '<USER>', return: true }, { id: 'p', active: true, expect: 'Password:', send: '<PASS>', return: true }];
  const nodes = [
    { id: 't1', parentId: '', name: 'edge-1', order: 0, session: { protocol: 'telnet', host: '10.0.0.1', port: 23, expect: login } },
    { id: 't2', parentId: '', name: 'edge-2', order: 1, session: { protocol: 'telnet', host: '10.0.0.2', port: 23 } },
    { id: 'c1', parentId: '', name: 'console', order: 2, session: { protocol: 'serial', serial: { device: 'COM1', baud: 9600 } } },
  ];
  const errors = await open(page, { nodes });
  for (const name of ['edge-1', 'edge-2', 'console']) await page.locator('#tree .row', { hasText: name }).click({ modifiers: ['Control'] });
  await page.locator('#tree .row', { hasText: 'edge-1' }).click({ button: 'right' });
  await page.getByText(/Edit 3 sessions together/).click();
  const dlg = page.locator('.dialog').filter({ hasText: 'Edit 3 sessions together' });
  await dlg.getByText('Connection type', { exact: true }).click();
  await expect(dlg).toContainText('2 sessions change to SSH');
  await expect(dlg).toContainText('1 serial session stays');
  await expect(dlg).toContainText('from 1 session: SSH logs in itself');
  await dlg.getByRole('button', { name: 'Apply' }).click();
  const req = (await calls(page, 'BulkUpdate')).at(-1).args[0];
  expect(req.change).toEqual({ protocol: 'ssh', dropLoginRules: true });
  expect(errors).toEqual([]);
});

test('export for a colleague leaves out the logins and no passwords go with it', async ({ page }) => {
  const errors = await open(page);
  await page.locator('#tree .row', { hasText: 'DC Zagreb' }).click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Export group…' }).click();
  const dlg = page.getByRole('dialog', { name: 'Export sessions' });
  await dlg.getByText('Include passwords, encrypted').click();
  await expect(dlg.getByText('Your master password', { exact: true })).toBeVisible();
  await dlg.getByText('Leave out user names and logins').click();
  await expect(dlg.getByText('Your master password', { exact: true })).toBeHidden();
  await expect(dlg.getByLabel('Include passwords, encrypted')).toBeDisabled();
  await dlg.getByRole('button', { name: 'Choose file and export' }).click();
  await expect.poll(async () => (await calls(page, 'ExportSessions')).length).toBe(1);
  const opts = (await calls(page, 'ExportSessions'))[0].args[1];
  expect(opts).toEqual(expect.objectContaining({ leaveOutLogins: true, includeSecrets: false }));
  expect(errors).toEqual([]);
});

test('an import from another program lets you choose what comes in', async ({ page }) => {
  const errors = await open(page, { chooseFile: '/home/user/SecureCRT/Config' });
  await page.locator('#btn-more').click();
  await page.getByText('Import from a file…').click();
  const dlg = page.getByRole('dialog', { name: 'Import from SecureCRT' });
  await expect(dlg).toContainText('What to import');
  for (const t of ['Sessions (3)', 'Credential profiles (2)', 'Saved passwords (2)', 'Logon actions, as automation rules (1)', 'Sessions through jump hosts (1)']) await expect(dlg.getByText(t)).toBeVisible();
  await dlg.getByText('Saved passwords (2)').click();
  await dlg.getByText('Sessions (3)').click();
  await expect(dlg.getByText('Put the sessions into a new group named')).toBeHidden();
  await dlg.getByRole('button', { name: 'Import' }).click();
  await expect.poll(async () => (await calls(page, 'ImportForeign')).length).toBe(1);
  const req = (await calls(page, 'ImportForeign'))[0].args[0];
  expect(req.parts).toEqual({ sessions: false, profiles: true, passwords: false, automation: true, jumpHosts: true });
  await expect(page.getByRole('dialog', { name: /Imported 2 credential profiles/ })).toBeVisible();
  expect(errors).toEqual([]);
});

test('a fresh install offers a button for each program found, in the guide and on the empty list', async ({ page }) => {
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  const sourceCounts = [
    { kind: 'securecrt', label: 'SecureCRT', path: '/home/u/.vandyke/SecureCRT/Config/Sessions', display: '~/.vandyke/SecureCRT/Config/Sessions', sessions: 214 },
    { kind: 'ssh-config', label: 'OpenSSH config', path: '/home/u/.ssh/config', display: '~/.ssh/config', sessions: 0 },
    { kind: 'mremoteng', label: 'mRemoteNG', path: '/x/confCons.xml', display: 'confCons.xml', sessions: -1 },
  ];
  await page.addInitScript(c => { window.__mockConfig = c; }, { fresh: true, nodes: [], settings: { lastSeenVersion: '' }, sourceCounts });
  await page.addInitScript(mock);
  await page.goto('/');
  const welcome = page.getByRole('dialog', { name: 'Welcome to YACM' });
  await expect(welcome.getByRole('button', { name: 'SecureCRT: 214 sessions' })).toBeVisible();
  await expect(welcome.getByRole('button', { name: 'mRemoteNG: import' })).toBeVisible();
  await expect(welcome.getByRole('button', { name: /OpenSSH/ })).toHaveCount(0);
  await welcome.getByRole('button', { name: 'Start with an empty list' }).click();
  // The empty list offers them too.
  await page.locator('#tree').getByRole('button', { name: 'SecureCRT: 214 sessions' }).click();
  await expect(page.getByRole('dialog', { name: 'Import from SecureCRT' })).toBeVisible();
  expect((await calls(page, 'PreviewForeign')).at(-1).args.slice(0, 2)).toEqual(['securecrt', '/home/u/.vandyke/SecureCRT/Config/Sessions']);
  expect(errors).toEqual([]);
});

test('tabs keep their width and a long name is shortened in the middle, so its end stays readable', async ({ page }) => {
  const names = Array.from({ length: 14 }, (_, i) => `cpt-c2960cx-access-floor-${String(i + 1).padStart(2, '0')}`);
  const nodes = names.map((name, i) => ({ id: `s${i}`, parentId: '', name, order: i, session: { protocol: 'ssh', host: `192.0.2.${i + 1}`, port: 22 } }));
  const errors = await open(page, { nodes });
  await page.setViewportSize({ width: 1200, height: 700 });
  await connectTabs(page, names);
  const titles = await page.locator('.tab .title').allTextContents();
  expect(titles).toHaveLength(14);
  // Different ends, so every tab can be told apart.
  expect(new Set(titles.map(t => t.slice(-2))).size).toBe(14);
  expect(titles[0]).toMatch(/^cpt-c2960cx.*….*-01$/);
  expect(await page.locator('.tab .title').first().getAttribute('title')).toBe(names[0]);
  const width = await page.locator('.tab').first().evaluate(el => el.getBoundingClientRect().width);
  expect(width).toBeGreaterThan(200);
  expect(await page.locator('#tabbar').evaluate(b => b.scrollWidth > b.clientWidth)).toBe(true);
  expect(errors).toEqual([]);
});

test('a long name in the session list is shortened in the middle, again when the list gets wider', async ({ page }) => {
  const name = 'cpt-c2960cx-access-floor-building-north-17';
  const errors = await open(page, { nodes: [{ id: 's1', parentId: '', name, order: 0, session: { protocol: 'ssh', host: '192.0.2.1', port: 22 } }] });
  const el = page.locator('#tree .row .name').first();
  await expect(el).toHaveText(/^cpt-c2960cx.*….*north-17$/);
  expect(await el.getAttribute('title')).toBe(name);
  await page.locator('#sidebar').evaluate(s => { s.style.width = '700px'; s.style.maxWidth = '700px'; s.style.flex = '0 0 700px'; });
  await page.evaluate(() => { const r = document.querySelector('.app') || document.body; r.style.gridTemplateColumns = 'auto 700px 1fr'; });
  await expect(el).toHaveText(name);
  expect(errors).toEqual([]);
});

test('the lock screen guards open sessions as the setting says', async ({ page }) => {
  // Stay usable: Continue without passwords, as before.
  let errors = await open(page);
  await connectTabs(page, ['core-sw1']);
  await page.locator('#btn-lock').click();
  const lock = page.getByRole('dialog', { name: 'YACM is locked' });
  await expect(lock).toBeVisible();
  await lock.getByRole('button', { name: 'Continue without passwords' }).click();
  await expect(lock).toHaveCount(0);
  await expect(page.locator('#tabbar .tab')).toHaveCount(1);
  expect(errors).toEqual([]);

  // Need the master password: no way past it, and no keys reach what is behind.
  errors = await open(page, { settings: { lockSessions: 'require' } });
  await connectTabs(page, ['core-sw1']);
  await page.locator('#btn-lock').click();
  await expect(lock).toContainText('enter your master password to go on');
  await expect(lock.getByRole('button', { name: /Continue|Close sessions/ })).toHaveCount(0);
  await page.keyboard.press('Control+Shift+O');
  await expect(page.getByLabel('Find a session', { exact: true })).toHaveCount(0);
  await expect(lock.getByLabel('Master password')).toBeFocused();
  await lock.getByLabel('Master password').fill('master-password-1');
  await page.keyboard.press('Enter');
  await expect(lock).toHaveCount(0);
  expect((await calls(page, 'UnlockVault')).map(c => c.args[0])).toEqual(['master-password-1']);
  await expect(page.locator('#tabbar .tab')).toHaveCount(1);
  expect(errors).toEqual([]);

  // Close: going on without the password closes the sessions, after asking.
  errors = await open(page, { settings: { lockSessions: 'close' } });
  await connectTabs(page, ['core-sw1', 'core-sw2']);
  await page.locator('#btn-lock').click();
  await lock.getByRole('button', { name: 'Close sessions and continue' }).click();
  await lock.getByRole('button', { name: 'Back' }).click();
  await lock.getByRole('button', { name: 'Close sessions and continue' }).click();
  await lock.getByRole('button', { name: 'Close sessions', exact: true }).click();
  await expect(lock).toHaveCount(0);
  await expect(page.locator('#tabbar .tab')).toHaveCount(0);
  expect((await calls(page, 'CloseTab')).length).toBe(2);
  // Without open sessions the screen offers Continue as usual.
  await page.locator('#btn-lock').click();
  await expect(lock.getByRole('button', { name: 'Continue without passwords' })).toBeVisible();
  expect(errors).toEqual([]);
});

test('a .yacm file: all settings only for your own export, your profiles only when allowed', async ({ page }) => {
  const errors = await open(page, { chooseFile: '/home/user/backup.yacm', yacmInfo: {
    app: 'YACM 1.1.0', exportedAt: '2026-11-01T10:00:00Z', groups: 1, sessions: 2, hasSecrets: false, settings: true, knownHosts: 0, existing: 0, localCommands: 0, profiles: 1,
    myProfiles: ['core-r1 (198.51.100.1) with AD'],
  } });
  await page.locator('#btn-more').click();
  await page.getByText('Import from a file…').click();
  const dlg = page.getByRole('dialog', { name: 'Import backup.yacm' });
  await expect(dlg).toContainText('core-r1 (198.51.100.1) with AD');
  await expect(dlg.getByText('It is my own export: take all settings')).toBeHidden();
  await dlg.getByText('Use the app settings from the file').click();
  await dlg.getByText('It is my own export: take all settings').click();
  await dlg.getByRole('button', { name: 'Import' }).click();
  await expect.poll(async () => (await calls(page, 'ImportSessions')).length).toBe(1);
  const opts = (await calls(page, 'ImportSessions'))[0].args[1];
  expect([opts.applySettings, opts.allSettings, opts.useMyProfiles]).toEqual([true, true, false]);
  expect(errors).toEqual([]);
});

test('the tab menu closes all tabs, asking once while sessions are connected', async ({ page }) => {
  const errors = await open(page);
  await connectTabs(page, ['core-sw1', 'core-sw2']);
  await page.locator('#tabbar .tab').first().click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Close all tabs' }).click();
  const dlg = page.getByRole('dialog', { name: 'Close 2 tabs?' });
  await expect(dlg).toContainText('2 sessions are still connected');
  await dlg.getByRole('button', { name: 'Cancel' }).click();
  await expect(page.locator('#tabbar .tab')).toHaveCount(2);
  await page.locator('#tabbar .tab').first().click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Close all tabs' }).click();
  await page.getByRole('dialog', { name: 'Close 2 tabs?' }).getByRole('button', { name: 'Close' }).click();
  await expect(page.locator('#tabbar .tab')).toHaveCount(0);
  expect((await calls(page, 'CloseTab')).length).toBe(2);
  expect(errors).toEqual([]);
});

test('Type to many keeps the command field on its own full-width row, however many sessions', async ({ page }) => {
  const errors = await open(page);
  await page.setViewportSize({ width: 1200, height: 700 });
  const search = page.locator('#search');
  for (let i = 1; i <= 30; i++) {
    await search.fill(`admin@192.0.2.${i}`);
    await page.keyboard.press('Enter');
    await expect(page.locator('.tab')).toHaveCount(i);
  }
  await page.getByRole('button', { name: 'Type to many' }).click();
  const bar = page.locator('#broadcast');
  await expect(bar.locator('label:not(.all)')).toHaveCount(30);
  const field = bar.locator('.inline-input');
  const fb = await field.boundingBox(), bb = await bar.boundingBox(), tb = await bar.locator('.targets').boundingBox();
  // Below every session, and most of the bar wide.
  expect(fb.y).toBeGreaterThanOrEqual(tb.y + tb.height - 1);
  expect(fb.width).toBeGreaterThan(bb.width * 0.7);
  // Many sessions scroll instead of pushing the terminal away.
  expect(tb.height).toBeLessThan(120);
  await expect(bar.locator('.targets strong')).toHaveText('Typing goes to (1 of 30):');
  expect(errors).toEqual([]);
});
