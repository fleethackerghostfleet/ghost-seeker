const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const {test}=require('node:test');
const read=file=>fs.readFileSync(path.join(__dirname,'static',file),'utf8');
const html=read('index.html'),commander=read('cdr.html'),app=read('app.js');
const tag=(source,id)=>{
 const match=source.match(new RegExp(`<[^>]+\\bid="${id}"[^>]*>`));
 assert.ok(match,`missing ${id}`);return match[0];
};

test('sensor, model and raw policy controls have persistent accessible names',()=>{
 assert.match(tag(html,'srcSel'),/aria-labelledby="sourceLabel"/);
 assert.match(html,/<span[^>]*id="sourceLabel"[^>]*>Sensor<\/span>/);
 assert.match(tag(html,'modelSel'),/aria-label="Base prioritization model"/);
 assert.match(tag(html,'polRaw'),/aria-label="Raw policy \(protojson\)"/);
});
test('dialogs and detail icon buttons have descriptive names',()=>{
 assert.match(tag(html,'settings'),/aria-label="Display settings"/);
 assert.match(tag(html,'wbConfirm'),/aria-label="Read back before applying"/);
 for(const [id,name] of [['dPrev','Previous image'],['dNext','Next image'],['dReset','Reset priority to model']]){
  assert.match(tag(app,id),new RegExp(`aria-label="${name}"`));
 }
 const closeButtons=[...app.matchAll(/<button[^>]*id="dClose"[^>]*>/g)];
 assert.equal(closeButtons.length,2);
 for(const [button] of closeButtons)assert.match(button,/aria-label="Close image details"/);
});
test('feed card names come from visible contents, with extra context as a description',()=>{
 const update=app.slice(app.indexOf('function updateCard('),app.indexOf('function toggleFeedFilters('));
 assert.doesNotMatch(update,/el\.setAttribute\('aria-label'/);
 assert.match(update,/el\.setAttribute\('aria-description'/);
 assert.match(app,/el\.setAttribute\('role','button'\)/);
});
test('Commander scroll panes are named and reachable by native keyboard focus',()=>{
 for(const [id,name] of [['qlist','Received imagery queue'],['stage','Selected image details'],['brief','Command assessment and decisions']]){
  const pane=tag(commander,id);
  assert.match(pane,/tabindex="0"/);
  assert.match(pane,new RegExp(`aria-label="${name}"`));
 }
 assert.match(tag(commander,'qlist'),/role="region"/);
 assert.match(commander,/<aside class="queue" aria-label="Received imagery">/);
});
