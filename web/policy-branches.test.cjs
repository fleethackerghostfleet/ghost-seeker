const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

function load(browser = false) {
 const filename = path.join(__dirname, 'static/policy.js');
 const context = browser ? {window: {}} : {module: {exports: {}}};
 new vm.Script(fs.readFileSync(filename, 'utf8'), {filename}).runInNewContext(context);
 return browser ? context.window.gsPolicy : context.module.exports;
}

test('policy: default and lowercase modes select exactly one unlocked EMCON button', () => {
 const policy = load();
 for (const [summary, selected] of [[undefined, 'NORMAL'], [{emcon: {mode: 'burst'}}, 'BURST']]) {
  const html = policy.emconHTML(summary);
  assert.match(html, new RegExp(`data-mode="${selected}" class="on" aria-pressed="true"`));
  assert.equal((html.match(/aria-pressed="true"/g) || []).length, 1);
  assert.doesNotMatch(html, /disabled|EMCON locked by/);
 }
});

test('policy: lock ownership falls back to setter and then higher echelon', () => {
 const policy = load();
 const setter = policy.emconHTML({emcon: {locked: true, setBy: '<Fleet & "A">'}});
 assert.match(setter, /EMCON locked by &lt;Fleet &amp; &quot;A&quot;&gt;/);
 assert.equal((setter.match(/ disabled/g) || []).length, 3);
 const fallback = policy.emconHTML({emcon: {locked: true}});
 assert.match(fallback, /EMCON locked by higher echelon/);
 assert.equal((fallback.match(/ disabled/g) || []).length, 3);
});

test('policy: source locks override local blocked and expiry badges', () => {
 const policy = load();
 const badge = policy.directiveBadge({source: '<Fleet>', locked: true, blocked: 'local conflict', expiresAtUnixMs: 1});
 assert.equal(badge, '<span class="policy-badge locked">🔒 Locked by &lt;Fleet&gt;</span>');
 assert.doesNotMatch(badge, /Blocked|Until/);
 assert.equal(policy.directiveBadge({source: '<Fleet>', locked: false}), '<span class="policy-badge delegated">From &lt;Fleet&gt; · delegated</span>');
});

test('policy: local blocking overrides expiry while unblocked expiry uses UTC', () => {
 const policy = load();
 const expiresAtUnixMs = Date.UTC(2026, 0, 1, 0, 7);
 assert.match(policy.directiveBadge({expiresAtUnixMs}), /Until 00:07Z/);
 assert.equal(policy.directiveBadge({blocked: 'higher & newer', expiresAtUnixMs}), '<span class="policy-badge blocked">Blocked — higher &amp; newer</span>');
 assert.equal(policy.directiveBadge(), '');
 assert.equal(policy.directiveBadge({expiresAtUnixMs: 0}), '');
});

test('policy: empty conflicts stay empty and missing conflict fields get explicit defaults', () => {
 const policy = load();
 assert.equal(policy.conflictsHTML(), '');
 assert.equal(policy.conflictsHTML([{}]), '<ul class="policy-conflicts"><li><b>policy</b> — Conflict <small>(unknown)</small></li></ul>');
 assert.equal(policy.conflictToast(), '');
 assert.equal(policy.conflictToast([{}]), '1 rule dropped — policy conflict');
 assert.equal(policy.conflictToast([{reason: 'locked'}, {}]), '2 rules dropped — locked');
});

test('policy: audit defaults do not stringify missing fields and preserve version zero', () => {
 const policy = load();
 assert.equal(policy.auditRowsHTML(), '');
 assert.equal(policy.auditRowsHTML([{}]), '<tr><td>00:00:00Z</td><td></td><td></td><td></td><td title=""></td><td class="audit-"></td><td></td></tr>');
 assert.match(policy.auditRowsHTML([{version: 0, atUnixMs: '1000'}]), /<td>00:00:01Z<\/td><td><\/td><td><\/td><td>0<\/td>/);
});

test('policy: audit text keeps full escaped title but truncates visible text', () => {
 const policy = load();
 const prefix = 'x'.repeat(80);
 const row = policy.auditRowsHTML([{text: prefix + '<tail>', who: 'A&B', result: 'reject"', reason: "reviewer's note"}]);
 assert.ok(row.includes(`<td title="${prefix}&lt;tail&gt;">${prefix}</td>`));
 assert.match(row, /A&amp;B/);
 assert.match(row, /class="audit-reject&quot;"/);
 assert.match(row, /reviewer&#39;s note/);
});

test('policy: hotkeys require a matching ancestor and tolerate absent DOM methods', () => {
 const policy = load();
 assert.equal(policy.isPolicyHotkeyTarget(null), false);
 assert.equal(policy.isPolicyHotkeyTarget({}), false);
 assert.equal(policy.isPolicyHotkeyTarget({closest: () => null}), false);
 let selector;
 assert.equal(policy.isPolicyHotkeyTarget({closest: value => {selector = value; return {};}}), true);
 assert.equal(selector, '#emconSeg, #policyAdmin');
});

test('policy: browser export exposes the same helper behavior as CommonJS', () => {
 const browser = load(true);
 const common = load();
 assert.deepEqual(Object.keys(browser).sort(), Object.keys(common).sort());
 assert.equal(typeof browser.conflictToast, 'function');
 assert.equal(browser.conflictToast([{}, {}]), '2 rules dropped — policy conflict');
 assert.equal(browser.emconHTML(), common.emconHTML());
});
