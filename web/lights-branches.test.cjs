const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

function load(browser = false) {
 const filename = path.join(__dirname, 'static/lights.js');
 const context = browser ? {} : {module: {exports: {}}};
 new vm.Script(fs.readFileSync(filename, 'utf8'), {filename}).runInNewContext(context);
 return browser ? context.gsLights : context.module.exports;
}

function item(dets) { return {night: true, src: {mode: 'eo'}, dets}; }

test('lights: eligibility is strict and missing sensor metadata stays ineligible', () => {
 const lights = load();
 assert.equal(lights.eligible(item([])), true);
 for (const value of [undefined, {}, {night: true}, {night: 1, src: {mode: 'eo'}}, {night: true, src: {mode: 'ir'}}]) {
  assert.ok(!lights.eligible(value));
  assert.equal(lights.section(value), '');
  assert.equal(lights.badge(value), '');
  assert.equal(lights.why(value).length, 0);
 }
});

test('lights: absent detections yield an empty section without badges or explanations', () => {
 const lights = load();
 assert.equal(lights.section(item()), '<section class="dsec night-lights"><h4>Navigation lights (night)</h4></section>');
 assert.equal(lights.badge(item()), '');
 assert.equal(lights.why(item()).length, 0);
 assert.equal(lights.icon(), '');
});

test('lights: icons sort a copy, filter unknown colors and distinguish flashing dots', () => {
 const lights = load();
 const points = [{colour: 'white', y: 3}, {colour: 'black', y: 0}, {colour: 'green', y: 2}, {colour: 'red', y: 1, flashing: true}];
 const before = JSON.stringify(points);
 const html = lights.icon(points);
 assert.match(html, /aria-label="flashing red, green, white"/);
 assert.equal((html.match(/<i /g) || []).length, 3);
 assert.match(html, /<i class="light-dot light-red light-flash"><\/i>/);
 assert.match(html, /<i class="light-dot light-green"><\/i>/);
 assert.doesNotMatch(html, /black/);
 assert.equal(JSON.stringify(points), before);
});

test('lights: missing signals use contact labels and no-pattern fallback without a badge', () => {
 const lights = load();
 const value = item([{cls: '<boat>'}]);
 const html = lights.section(value);
 assert.match(html, /&lt;boat&gt; · contact 1/);
 assert.match(html, /No recognized light pattern/);
 assert.match(html, /auto-detected — verify visually/);
 assert.doesNotMatch(html, /<small>|light-pattern/);
 assert.equal(lights.badge(value), '');
 assert.equal(lights.why(value).length, 0);
});

test('lights: low confidence suppresses even positive boosts and escapes custom uncertainty', () => {
 const lights = load();
 const sig = {severity: 'urgent', line: 'Keep clear', lowConfidence: true, assisted: true, boost: .9, confidenceNote: 'Caution <&"\'>'};
 const value = item([{cls: 'boat', sig}]);
 const html = lights.section(value);
 assert.match(html, /Low confidence · Caution &lt;&amp;&quot;&#39;&gt;/);
 assert.doesNotMatch(html, /priority boost \+|signal-assisted/);
 assert.equal(lights.why(value)[0].c, 'light-low-confidence');
 assert.match(lights.why(value)[0].t, /\(no priority boost\)/);
 sig.lowConfidence = false;
 const supported = lights.section(value);
 assert.match(supported, /signal-assisted · priority boost \+0\.90/);
 assert.doesNotMatch(supported, /Low confidence/);
});

test('lights: only urgent signals become explanations and ordinary signals still get badges', () => {
 const lights = load();
 const ordinary = {cls: 'boat', sig: {severity: 'warning', line: 'Ordinary contact'}};
 const urgent = {cls: 'boat', sig: {severity: 'urgent', line: 'Urgent contact', ref: 'R27'}};
 const value = item([ordinary, {cls: 'boat'}, urgent]);
 const why = lights.why(value);
 assert.equal(why.length, 1);
 assert.equal(why[0].c, 'al');
 assert.match(why[0].t, /Urgent contact/);
 assert.doesNotMatch(why[0].t, /Ordinary contact|no priority boost/);
 assert.match(lights.badge(item([ordinary])), /night-badge/);
 assert.equal(lights.why(item([ordinary])).length, 0);
});

test('lights: class labels and aspect underscores are rendered with explicit fallbacks', () => {
 const lights = load();
 const value = item([{cls: 'boat', sig: {line: '', ref: '<R>', aspect: 'head_on'}}]);
 const html = lights.section(value, {boat: {lab: '<Vessel>'}});
 assert.match(html, /&lt;Vessel&gt; · contact 1/);
 assert.match(html, /&lt;R&gt; · head on/);
 assert.match(html, /No recognized light pattern/);
 value.dets[0].sig.aspect = '';
 assert.match(lights.section(value, {boat: {lab: ''}}), /<b>boat · contact 1<\/b>/);
 assert.match(lights.section(value), /&lt;R&gt; · unknown/);
});

test('lights: browser export exposes the same helpers without CommonJS', () => {
 const browser = load(true);
 const common = load();
 assert.deepEqual(Object.keys(browser).sort(), Object.keys(common).sort());
 assert.equal(browser.badge(item([{sig: {}}])), common.badge(item([{sig: {}}])));
 assert.equal(browser.section(item([])), common.section(item([])));
});
