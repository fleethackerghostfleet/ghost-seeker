const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');
const source=fs.readFileSync(path.join(__dirname,'static/app.js'),'utf8');

function setup(){
 const elements=new Map(),timers=new Map();let next=0;
 const context=vm.createContext({
  imageCache:new Map(),
  setTimeout:(fn,ms)=>{const id=++next;timers.set(id,{fn,ms});return id},clearTimeout:id=>timers.delete(id),
  document:{body:{classList:{remove(){},toggle(){}}},querySelector:()=>null},
  $:id=>{if(!elements.has(id))elements.set(id,{style:{},children:[],classList:{toggle(){},add(){},remove(){}},querySelector:()=>null,querySelectorAll:()=>[],setAttribute(){}});return elements.get(id)},
 });
 context.window=context;
 vm.runInContext(source.replace(/init\(\);\s*$/,''),context);
 vm.runInContext(`
  SRC={sensor:{id:'sensor',name:'Sensor'}};
  state.page='rv';reviewerName='Reviewer';reviewDataReady=true;
  renderFeed=()=>{};renderStats=()=>{};renderRp=()=>{};renderDetail=()=>{};renderPolicySummary=()=>{};renderAsk=()=>{};renderLive=()=>{};
  buildWhy=()=>[];zulu=()=> '12:00:00Z';largeImage=it=>'<img data-review-id="'+it.id+'" src="'+it.img+'">';
  streamHandlers={};EventSource=class{addEventListener(type,handler){streamHandlers[type]=handler}};openStream();
  $('rvKeys').children=['accept','1','2','3','4','cdr','dismiss','skip','undo'].map(a=>({dataset:{a}}));
  function raw(id,tier=2){return {id,src:'sensor',img:id+'.jpg',ts:'2026-11-05T12:00:00Z',tier,centralTier:tier,central:tier===1?.95:.6,edge:.5,dets:[],det:[]}}
  function snapshot(rows){return {items:rows,state:{model:'maritime',directives:[]},stats:{},cdr:0,weights:{}}}
  function emit(type,data){streamHandlers[type]({data:JSON.stringify(data)})}
  function start(){applySnapshot(snapshot([raw('active'),raw('next',3)]))}
 `,context);
 return {context,run:code=>vm.runInContext(code,context),finish(ms){const entries=[...timers].filter(([,t])=>t.ms===ms);for(const [id,t]of entries){timers.delete(id);t.fn()}}};
}
function active(h,id='active'){
 assert.equal(h.run('rv.cur?.id'),id);
 assert.match(h.context.$('rvMain').innerHTML,new RegExp(`data-review-id="${id}"`));
}

test('scene arrivals do not replace stable Review selection or become its next item',()=>{
 const h=setup();h.run(`start();emit('item',{item:{...raw('ocean',4),live:{event:'scene'}},stats:{}})`);active(h);
 assert.equal(h.run('rvQueue().some(isScene)'),false);
 h.run("rvAct('skip')");active(h,'next');
 h.run("rvAct('skip')");assert.equal(h.run('isScene(rv.cur)'),false);
 h.run("items=items.filter(isScene);rv.cur=null;renderReview()");
 assert.equal(h.run('rv.cur'),null);
 assert.match(h.context.$('rvMain').innerHTML,/All caught up/);
});
test('a stale pinned or displayed scene cannot be reviewed',()=>{
 const h=setup();h.run(`start();scene={...findItem('active'),live:{event:'scene'}};rv.cur=scene;rv.displayed=scene;
  requests=[];submitReview=(...args)=>{requests.push(args);return Promise.resolve()};rvAct('accept')`);
 assert.equal(h.run('requests.length'),0);
 h.run('renderReview()');active(h);
 assert.equal(h.run('isScene(rv.cur)'),false);
 h.run('rv.displayed=scene;rvAct("accept")');
 assert.equal(h.run('requests.length'),0);
});
test('new high-priority arrivals and score reordering never replace the active image',()=>{
 const h=setup();h.run('start()');active(h);
 h.run("emit('item',{item:raw('urgent',1),stats:{cap:3}})");active(h);
 h.run("emit('update',{item:{...raw('next',1),central:1},cdr:0})");active(h);
 assert.equal(h.run("rvQueue()[0].id"),'next');
 assert.equal(h.run('state.paused'),false);
});
test('the default simulated watch officer review update does not advance review',()=>{
 const h=setup();h.run('start()');active(h);
 h.run("emit('update',{item:{...raw('active'),manual:2,reviewed:true,sent:true},cdr:1})");
 active(h);
 assert.equal(h.run("findItem('active').reviewed&&findItem('active').sent&&cdr===1"),true);
});
test('upload progress refreshes the active record and feed without selecting a new photo',()=>{
 const h=setup();h.run('start();feedRenders=0;renderFeed=()=>feedRenders++');
 h.run("emit('update',{item:{...raw('active'),upload:{bytes:100,sent:70}},cdr:0})");
 active(h);assert.equal(h.run('rv.cur.upload.sent'),70);assert.equal(h.run('feedRenders'),1);
 h.run("emit('stats',{stats:{bytes:70}})");active(h);
 assert.equal(h.run('stats.bytes'),70);
});
test('full reconnect and time-rescore snapshots preserve selection while replacing objects',()=>{
 const h=setup();h.run('start();original=rv.cur');
 h.run("applySnapshot(snapshot([raw('urgent',1),{...raw('active',4),central:.1},raw('next',1)]))");
 active(h);assert.equal(h.run('rv.cur!==original&&rv.cur.centralTier===4'),true);
 assert.match(h.context.$('rvMain').innerHTML,/P4/);
 h.run("applySnapshot(snapshot([{...raw('active',4),reviewed:true,dismissed:true},raw('urgent',1)]))");active(h);
});
for(const removal of ['eviction','snapshot','held reconnect']){
 test(`${removal} pauses on an unavailable active image until explicit skip`,()=>{
  const h=setup();h.run('start()');
  if(removal==='eviction')h.run("emit('evict',{id:'active'})");
  else if(removal==='snapshot')h.run("applySnapshot(snapshot([raw('next',3)]))");
  else h.run("state.paused=true;applySnapshot(snapshot([raw('next',3)]));toggleHold()");
  active(h);assert.match(h.context.$('rvMain').innerHTML,/no longer available/i);
  assert.equal(h.run("$('rvKeys').children.find(b=>b.dataset.a==='accept').disabled"),true);
  assert.equal(h.run("$('rvKeys').children.find(b=>b.dataset.a==='skip').disabled"),false);
  h.run("emit('item',{item:raw('urgent',1),stats:{}});submitted=[];submitReview=(...args)=>submitted.push(args);rvAct('accept')");
  active(h);assert.equal(h.run('submitted.length'),0);
  h.run("rvAct('skip')");active(h,'urgent');
 });
}
test('an eviction racing input cannot attach that decision to the next queued image',()=>{
 const h=setup();h.run("start();submitted=[];submitReview=(...args)=>submitted.push(args);emit('evict',{id:'active'});rvAct('2')");
 assert.equal(h.run('submitted.length'),0);active(h);
});
for(const action of ['accept','1','2','3','4','dismiss','cdr']){
 test(`explicit ${action} submits the displayed ID once and advances only after completion`,async()=>{
  const h=setup();h.run(`start();submitted=[];submitReview=(id,action,changes,p)=>{submitted.push({id,action,changes});return new Promise(resolve=>complete=()=>{applyItemUpdate({...raw(id),...changes});resolve(findItem(id))})};rvAct('${action}');rvAct('${action}')`);
  assert.equal(h.run('submitted.length'),1);assert.equal(h.run('submitted[0].id'),'active');
  h.run("applySnapshot(snapshot([raw('urgent',1),{...raw('active'),reviewed:true},raw('next',3)]))");active(h);
  h.run('complete()');await new Promise(setImmediate);
  assert.match(h.run('rv.last'),/Saved:.*Press Z to undo\./);
  h.run("applySnapshot(snapshot([raw('urgent',1),{...raw('active'),reviewed:true},raw('next',3)]))");active(h);
  h.finish(280);active(h,'urgent');
  active(h,'urgent');assert.equal(h.run('rv.done'),1);
 });
}
test('input after a synchronous rescore uses the newly displayed priority',async()=>{
 const h=setup();h.run("start();submitted=[];submitReview=(id,action,changes)=>{submitted.push({id,changes});return Promise.resolve()};emit('update',{item:raw('active',1),cdr:0});rvAct('accept')");
 await new Promise(setImmediate);
 assert.equal(h.run('submitted[0].id'),'active');assert.equal(h.run('submitted[0].changes.manual'),1);
});
test('skip postpones one item, and undo returns to the original image even across updates',async()=>{
 const h=setup();h.run("start();rvAct('skip')");active(h,'next');
 h.run("submitReview=(id,action,changes)=>{applyItemUpdate({...raw(id,3),...changes});return Promise.resolve()};rvAct('accept')");
 await new Promise(setImmediate);h.finish(280);active(h);
 h.run("undoReview=p=>{applyItemUpdate(raw(p.id,3));return Promise.resolve(findItem(p.id))};rvAct('undo')");
 await new Promise(setImmediate);active(h,'next');assert.equal(h.run('rv.done'),0);assert.equal(h.run('rv.undo.length'),0);
 h.run("emit('update',{item:{...raw('next',3),reviewed:true},cdr:0})");active(h,'next');
});
test('undo after retention eviction shows the missing original image instead of a different one',async()=>{
 const h=setup();h.run("start();submitReview=(id,action,changes)=>{applyItemUpdate({...raw(id),...changes});return Promise.resolve()};rvAct('accept')");
 await new Promise(setImmediate);h.finish(280);active(h,'next');
 h.run("emit('evict',{id:'active'});undoReview=()=>Promise.resolve(undefined);rvAct('undo')");
 await new Promise(setImmediate);active(h);assert.match(h.context.$('rvMain').innerHTML,/no longer available/i);
});
test('failed review stays on the active image without recording or advancing',async()=>{
 const h=setup();h.run("start();submitReview=()=>Promise.reject(new Error('save failed'));rvAct('accept')");
 await new Promise(setImmediate);h.finish(280);active(h);assert.equal(h.run('rv.done'),0);assert.equal(h.run('rv.busy'),false);
});
test('an initially empty review queue can show its first arrival without a feed pause',()=>{
 const h=setup();h.run('applySnapshot(snapshot([]))');assert.equal(h.run('rv.cur'),null);
 h.run("emit('item',{item:raw('active'),stats:{}})");active(h);assert.equal(h.run('state.paused'),false);
});
test('explicit review order navigation selects once and is ignored while a save is pending',async()=>{
 const h=setup();
 const start=source.indexOf(" $('rvOrder').onclick=");
 assert.ok(start>=0);
 vm.runInContext(source.slice(start,source.indexOf('\n',start)),h.context);
 h.run(`start();emit('item',{item:raw('urgent',1),stats:{}});
  orderButton={dataset:{o:'pri'},classList:{toggle(){}}};$('rvOrder').children=[orderButton];
  orderEvent={target:{closest:()=>orderButton}};
  originalRender=renderReview;orderRenders=0;renderReview=()=>{orderRenders++;originalRender()};
  $('rvOrder').onclick(orderEvent)`);
 active(h,'urgent');assert.equal(h.run('orderRenders'),1);
 h.run("submitReview=(id,action,changes)=>new Promise(resolve=>complete=()=>{applyItemUpdate({...raw(id,1),...changes});resolve(findItem(id))});rvAct('accept');$('rvOrder').onclick(orderEvent)");
 active(h,'urgent');assert.equal(h.run('orderRenders'),1);
 h.run('complete()');await new Promise(setImmediate);h.finish(280);active(h);
 assert.equal(h.run('rv.done'),1);
});
test('leaving and returning during a review save never opens an unrelated image',async()=>{
 const h=setup();h.run("start();location={hash:'#feed'};submitReview=(id,action,changes)=>new Promise(resolve=>complete=()=>{applyItemUpdate({...raw(id),...changes});resolve(findItem(id))});rvAct('accept');route();emit('item',{item:raw('urgent',1),stats:{}});location.hash='#review';route()");
 active(h);h.run('complete()');await new Promise(setImmediate);active(h);
 h.finish(280);active(h,'urgent');assert.equal(h.run('rv.done'),1);
});
test('an explicit sensor switch releases the pinned image and reviews only the chosen source',async()=>{
 const h=setup();
 const start=source.indexOf(" $('srcSel').onchange=");
 assert.ok(start>=0);
 vm.runInContext(source.slice(start,source.indexOf('\n',start)),h.context);
 h.run(`start();SRC.other={id:'other',name:'Other sensor'};
  applyItemUpdate({...raw('other-image',1),src:'other'});
  state.page='feed';$('srcSel').onchange({target:{value:'other'}});location={hash:'#review'};route()`);
 active(h,'other-image');assert.equal(h.run("rv.cur.src.id===state.src"),true);
 h.run("submitted=[];submitReview=(id,action,changes)=>{submitted.push({id,changes});return Promise.resolve()};rvAct('accept')");
 await new Promise(setImmediate);assert.equal(h.run('submitted[0].id'),'other-image');
});
test('a sensor switch during a save waits, then selects once within the new source',async()=>{
 const h=setup();
 const start=source.indexOf(" $('srcSel').onchange=");
 vm.runInContext(source.slice(start,source.indexOf('\n',start)),h.context);
 h.run(`start();SRC.other={id:'other',name:'Other sensor'};
  applyItemUpdate({...raw('other-image',1),src:'other'});
  submitReview=(id,action,changes)=>new Promise(resolve=>complete=()=>{applyItemUpdate({...raw(id),...changes});resolve(findItem(id))});rvAct('accept');
  state.page='feed';$('srcSel').onchange({target:{value:'other'}});location={hash:'#review'};route()`);
 assert.equal(h.run('rv.cur'),null);assert.match(h.context.$('rvMain').innerHTML,/Saving review/);
 h.run('complete()');await new Promise(setImmediate);h.finish(280);active(h,'other-image');
 active(h,'other-image');assert.equal(h.run('rv.done'),1);
});
for(const empty of [false,true]){
 test(`a sensor switch during undo does not restore the previous source (${empty?'empty':'nonempty'} queue)`,async()=>{
  const h=setup();
  const start=source.indexOf(" $('srcSel').onchange=");
  vm.runInContext(source.slice(start,source.indexOf('\n',start)),h.context);
  h.run(`state.src='sensor';start();SRC.other={id:'other',name:'Other sensor'};
   applyItemUpdate({...raw('other-image',1),src:'other'});
   if(${empty})applyItemUpdate({...raw('next',3),reviewed:true});
   submitReview=(id,action,changes)=>{applyItemUpdate({...raw(id),...changes});return Promise.resolve(findItem(id))};rvAct('accept')`);
  await new Promise(setImmediate);h.finish(280);
  h.run(`undoReview=()=>new Promise(resolve=>completeUndo=()=>{applyItemUpdate(raw('active'));resolve(findItem('active'))});rvAct('undo');
   state.page='feed';$('srcSel').onchange({target:{value:'other'}});location={hash:'#review'};route()`);
  assert.equal(h.run('rv.cur'),null);
  h.run('completeUndo()');await new Promise(setImmediate);active(h,'other-image');
  assert.equal(h.run('rv.done'),0);assert.equal(h.run('rv.undo.length'),0);
 });
}
test('a stale accept sends the displayed tier, then refreshes the same image on server rejection',async()=>{
 const h=setup();h.run(`start();requests=[];
  request=(url,method,body)=>{requests.push({url,body});applyItemUpdate(raw('active',1));return Promise.reject(new Error('model priority changed'))};
  rvAct('accept')`);
 await new Promise(setImmediate);active(h);
 assert.equal(h.run('requests.length'),1);assert.equal(h.run('requests[0].url'),'/api/items/active/review');
 assert.equal(h.run('requests[0].body.tier'),2);assert.equal(h.run('requests[0].body.action'),'accept');
 assert.equal(h.run('rv.displayed.centralTier'),1);assert.equal(h.run('rv.done'),0);assert.equal(h.run('rv.undo.length'),0);
 assert.match(h.context.$('toast').textContent,/model priority changed/);
});
test('demo next-step guidance explains saving and unavailable states without replacing the image',()=>{
 const h=setup();h.run('start();renderNext(rv.cur)');
 assert.match(h.context.$('rvNext').innerHTML,/Space.*agree with P2 High/);
 h.run("emit('evict',{id:'active'});renderNext(rv.cur)");active(h);
 assert.match(h.context.$('rvNext').innerHTML,/unavailable.*<kbd>S<\/kbd>.*<kbd>Z<\/kbd>/);
 h.run('rv.busy=true;renderNext(rv.cur)');
 assert.match(h.context.$('rvNext').innerHTML,/Saving review/);
});
