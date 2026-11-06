const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

test('grader shortcuts leave interactive controls alone and ignore held-key repeats', () => {
  const handlers = {};
  const elements = new Map();
  const document = {
    getElementById(id) {
      if (!elements.has(id)) elements.set(id, {});
      return elements.get(id);
    },
    querySelectorAll() { return []; },
    addEventListener(type, handler) { handlers[type] = handler; }
  };
  const context = vm.createContext({document, fetch: () => new Promise(() => {}), calls: []});
  const html = fs.readFileSync(__dirname + '/grade.html', 'utf8');
  vm.runInContext(html.match(/<script>([\s\S]*?)<\/script>/)[1], context);
  vm.runInContext(`
    state = {entries: [{id: 'a', model_tier: 2}]};
    grade = tier => calls.push(['grade', tier]);
    mutate = path => calls.push(['mutate', path]);
    navigate = delta => calls.push(['navigate', delta]);
  `, context);
  for (const control of ['button', 'a', 'input', 'textarea', 'select', 'summary',
    '[contenteditable]:not([contenteditable="false"])', '[role="button"]', '[tabindex]']) {
    for (const key of ['Enter', ' ', '1', '2', '3', '4', 'z', 'ArrowLeft', 'ArrowRight']) {
      let prevented = false;
      handlers.keydown({
        key,
        target: {closest(selector) { return selector.split(',').includes(control) ? {} : null; }},
        preventDefault() { prevented = true; }
      });
      assert.equal(prevented, false, control + ' ' + key);
      assert.equal(context.calls.length, 0);
    }
  }
  for (const key of ['Enter', ' ', '1', '4', 'z', 'ArrowRight']) {
    let prevented = false;
    handlers.keydown({key, target: {closest() { return null; }}, preventDefault() { prevented = true; }});
    assert.equal(prevented, true);
  }
  for (const key of ['Enter', ' ', '1', 'z', 'ArrowRight']) {
    let prevented = false;
    handlers.keydown({key, repeat: true, target: {closest() { return null; }}, preventDefault() { prevented = true; }});
    assert.equal(prevented, false, 'held ' + key);
  }
  assert.deepEqual(JSON.parse(JSON.stringify(context.calls)), [
    ['grade', 2], ['grade', 2], ['grade', 1], ['grade', 4],
    ['mutate', '/api/undo'], ['navigate', 1]
  ]);
});
