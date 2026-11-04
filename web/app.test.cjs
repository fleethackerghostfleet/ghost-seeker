const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');

const source=fs.readFileSync(path.join(__dirname,'static/app.js'),'utf8');
function setup(){
 const elements=new Map();
 const context=vm.createContext({
  setTimeout:()=>{},imageCache:new Map(),
  document:{body:{classList:{remove(){}}}},
  $:id=>{if(!elements.has(id))elements.set(id,{classList:{toggle(){}},querySelector:()=>({})});return elements.get(id)},
 });
 vm.runInContext(source.replace(/init\(\);\s*$/,''),context);
 vm.runInContext(`
  renderAll=()=>{};updateCard=()=>{};renderFeed=()=>{};renderStats=()=>{};renderRp=()=>{};renderDetail=()=>{};
  streamHandlers={};
  EventSource=class{addEventListener(type,handler){streamHandlers[type]=handler}};
  SRC={'EO-P7':{id:'EO-P7'}};
  function raw(id,tier=3){return {id,src:'EO-P7',ts:'2026-11-04T12:00:00Z',tier,central:tier===1?.9:.4}}
  function snapshot(rows){return {items:rows,state:{model:'maritime',directives:[]},stats:{},cdr:0,weights:{}}}
 `,context);
 return context;
}

test('paused snapshots refresh existing and queued scores without exposing captures; resume drains queue',()=>{
 const context=setup();
 vm.runInContext(`
  const card={};
  items=[{...normalizeItem(raw('visible')),el:card}];
  state.sel='visible';state.paused=true;
  pendingItems=[{item:raw('queued')},{item:raw('not-in-snapshot')}];
  evictedIDs.add('evicted');
  applySnapshot(snapshot([raw('visible',1),raw('queued',1),raw('snapshot-only'),raw('evicted')]));
 `,context);
 assert.equal(vm.runInContext("items.length===1&&items[0].id==='visible'&&items[0].tier===1&&items[0].el===card&&state.sel==='visible'",context),true);
 assert.equal(vm.runInContext("pendingItems.length===3&&pendingItems[0].item.tier===1&&pendingItems[1].item.id==='not-in-snapshot'&&pendingItems[2].item.id==='snapshot-only'&&evictedIDs.has('evicted')",context),true);
 const pauseHandler=source.split('\n').find(line=>line.includes("$('pauseBtn').onclick="));
 vm.runInContext(pauseHandler,context);
 vm.runInContext("$('pauseBtn').onclick()",context);
 assert.equal(vm.runInContext("!state.paused&&pendingItems.length===0&&items.length===3&&findItem('queued').tier===1&&!findItem('evicted')&&!findItem('not-in-snapshot')&&!!findItem('snapshot-only')",context),true);
});

test('empty paused snapshot preserves visible items and queue',()=>{
 const context=setup();
 vm.runInContext("state.paused=true;items=[normalizeItem(raw('visible'))];pendingItems=[{item:raw('queued')}];applySnapshot(snapshot([]))",context);
 assert.equal(vm.runInContext("items.length===1&&pendingItems.length===1",context),true);
});

test('resume after a held reconnect drops items the server no longer has but keeps later arrivals',()=>{
 const context=setup();
 vm.runInContext(`
  state.paused=true;items=[normalizeItem(raw('stale')),normalizeItem(raw('kept'))];
  openStream();
  applySnapshot(snapshot([raw('kept')]));
  streamHandlers.item({data:JSON.stringify({item:raw('later'),stats:{}})});
 `,context);
 assert.equal(vm.runInContext("items.length===2",context),true);
 vm.runInContext(source.split('\n').find(line=>line.includes("$('pauseBtn').onclick=")),context);
 vm.runInContext("$('pauseBtn').onclick()",context);
 assert.equal(vm.runInContext("items.length===2&&!!findItem('kept')&&!!findItem('later')&&!findItem('stale')",context),true);
});

test('unpaused snapshot still replaces items and clears pending and eviction state',()=>{
 const context=setup();
 vm.runInContext("items=[normalizeItem(raw('old'))];pendingItems=[{item:raw('queued')}];evictedIDs.add('evicted');applySnapshot(snapshot([raw('new')]))",context);
 assert.equal(vm.runInContext("items.length===1&&items[0].id==='new'&&pendingItems.length===0&&evictedIDs.size===0",context),true);
});

test('paused update events queue unknown IDs, refresh pending records, and reveal latest state on resume',()=>{
 const context=setup();
 vm.runInContext(`
  state.paused=true;items=[normalizeItem(raw('visible'))];
  pendingItems=[{item:raw('captured')}];
  openStream();
  function update(id,tier){streamHandlers.update({data:JSON.stringify({item:{...raw(id,tier),reviewed:true,sent:true},cdr:2})})}
  update('captured',1);update('unknown',3);update('unknown',1);
 `,context);
 assert.equal(vm.runInContext("items.length===1&&pendingItems.length===2&&pendingItems.every(e=>e.item.tier===1&&e.item.reviewed&&e.item.sent)&&cdr===2",context),true);
 vm.runInContext(source.split('\n').find(line=>line.includes("$('pauseBtn').onclick=")),context);
 vm.runInContext("$('pauseBtn').onclick()",context);
 assert.equal(vm.runInContext("!state.paused&&items.length===3&&pendingItems.length===0&&findItem('captured').tier===1&&findItem('unknown').sent",context),true);
});

test('paused updates still refresh visible items without changing their element or queue',()=>{
 const context=setup();
 vm.runInContext(`
  state.paused=true;const card={};items=[{...normalizeItem(raw('visible')),el:card}];
  pendingItems=[{item:raw('queued')}];
  applyItemUpdate({...raw('visible',1),reviewed:true,sent:true});
 `,context);
 assert.equal(vm.runInContext("items.length===1&&items[0].tier===1&&items[0].sent&&items[0].el===card&&pendingItems.length===1",context),true);
});

test('paused updates do not requeue evicted IDs and unpaused updates still add unknown IDs',()=>{
 const context=setup();
 vm.runInContext("state.paused=true;evictedIDs.add('evicted');applyItemUpdate(raw('evicted'))",context);
 assert.equal(vm.runInContext("items.length===0&&pendingItems.length===0",context),true);
 vm.runInContext("state.paused=false;applyItemUpdate(raw('new',1))",context);
 assert.equal(vm.runInContext("items.length===1&&items[0].id==='new'&&items[0].tier===1",context),true);
});
