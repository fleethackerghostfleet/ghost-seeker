const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');

const source=fs.readFileSync(path.join(__dirname,'static/app.js'),'utf8');
const workbenchSource=fs.readFileSync(path.join(__dirname,'static/workbench.js'),'utf8');
const policySource=fs.readFileSync(path.join(__dirname,'static/policy.js'),'utf8');
test('spectral controls are metadata-driven and preserve the frame and boxes',()=>{
 const context=setup();
 vm.runInContext(`
  imageFor=()=>'/frame.jpg';boxesHTML=()=>'<div class="box">contact</div>';
  renderReview=()=>{};state.page='rv';
  multi={id:'multi',src:{name:'Mast'},bands:['EO','IR']};
  before=largeImage(multi);selectBand(multi,'IR');after=largeImage(multi);
 `,context);
 assert.match(vm.runInContext('before',context),/aria-pressed="true"[^>]*>EO/);
 assert.match(vm.runInContext('after',context),/class="spectrum-ir"/);
 assert.match(vm.runInContext('after',context),/IR · white-hot · simulated LWIR/);
 for(const name of ['before','after']){
  assert.match(vm.runInContext(name,context),/src="\/frame.jpg"/);
  assert.match(vm.runInContext(name,context),/<div class="box">contact<\/div>/);
 }
 vm.runInContext("selectBand(multi,'EO');selectBand(multi,'UV')",context);
 assert.equal(vm.runInContext("bandFor(multi)",context),'EO');
 for(const bands of [undefined,[],['EO'],['IR'],'EO/IR',[null,42,'EO','EO']]){
  context.input=bands;
  assert.equal(vm.runInContext("bandControls({id:'single',bands:input})",context),'');
 }
 assert.equal(vm.runInContext("bandControls({id:'other',src:{id:'EO-P7'}})",context),'');
});
test('global keyboard shortcuts ignore input while a dialog is open',()=>{
 const context=setup();
 vm.runInContext(`
  handlers={};nightToggles=0;
  document.querySelector=selector=>selector==='dialog[open]'?{}:null;
  document.addEventListener=(type,handler)=>{if(type==='keydown')handlers[type]=handler};
  toggleNight=()=>nightToggles++;
 `,context);
 const start=source.indexOf(" document.addEventListener('keydown',e=>{if(document.querySelector('dialog[open]')||$('settings').open||gsPolicy.isPolicyHotkeyTarget(e.target))return;");
 const end=source.indexOf(" $('rvKeys').onclick",start);
 assert.ok(start>=0&&end>start);
 vm.runInContext(source.slice(start,end),context);
 vm.runInContext(`handlers.keydown({key:'n',target:{tagName:'BODY',closest:()=>null}})`,context);
 assert.equal(vm.runInContext('nightToggles',context),0);
 vm.runInContext(`document.querySelector=()=>null;handlers.keydown({key:'n',target:{tagName:'BODY',closest:selector=>selector.includes('#policyAdmin')?{}:null}})`,context);
 assert.equal(vm.runInContext('nightToggles',context),0);
});
test('a blocked lock warning disables workbench confirmation',()=>{
 const context=setup();
 vm.runInContext(`
  confirmButton={disabled:false};
  $=id=>id==='wbConfirmApply'?confirmButton:{};
  compiled={blocked:true,warnings:['Locked by CTF-72 — submarine can\\'t be overridden at ship level']};
  draft={rules:[{type:'suppress',classes:['submarine']}],pins:{}};
 `,context);
 const start=workbenchSource.indexOf("$('wbConfirmApply').disabled=compiled.blocked");
 const end=workbenchSource.indexOf(';',start);
 assert.ok(start>=0&&end>start);
 vm.runInContext(workbenchSource.slice(start,end+1),context);
 assert.equal(vm.runInContext('confirmButton.disabled',context),true);
});
test('band controls keep native Enter and Space activation without accepting review items',()=>{
 const context=setup();
 vm.runInContext(`
  actions=[];prevented=false;insideBands=true;
  document.addEventListener=(type,handler)=>{keydown=handler};
  rvAct=action=>actions.push(action);
  reviewAskKey=key=>actions.push('ask:'+key);
  step=direction=>actions.push('step:'+direction);
  setManual=(id,tier)=>actions.push('tier:'+tier);
  selectBand=(item,band)=>actions.push('band:'+band);
  location={hash:''};
  state.page='rv';state.sel='multi';
  items=[{id:'multi',bands:['EO','IR']}];rv.cur=items[0];
  function press(key){
   prevented=false;
   keydown({key,target:{tagName:'BUTTON',closest:selector=>insideBands&&selector.includes('.band-controls')?{}:null},preventDefault(){prevented=true}});
  }
 `,context);
 const start=source.indexOf(" document.addEventListener('keydown'");
 const end=source.indexOf(" $('rvKeys').onclick",start);
 assert.ok(start>=0&&end>start);
 vm.runInContext(source.slice(start,end),context);
 for(const key of ['Enter',' ']){
  context.key=key;
  for(const asking of [false,true]){
   context.asking=asking;
   vm.runInContext("rv.ask=asking?{}:null;actions=[];press(key)",context);
   assert.equal(vm.runInContext('actions.length',context),0);
   assert.equal(vm.runInContext('prevented',context),false);
  }
  vm.runInContext("rv.ask=null;insideBands=false;actions=[];press(key);insideBands=true",context);
  assert.equal(vm.runInContext("actions.join(',')",context),'accept');
 }
 vm.runInContext("actions=[];press('B');press('1');press('2');press('3');press('4');press('Escape')",context);
 assert.equal(vm.runInContext("actions.join(',')",context),'band:IR,1,2,3,4');
 assert.equal(vm.runInContext('location.hash',context),'#feed');
 vm.runInContext("state.page='feed';actions=[];press('ArrowRight');press('ArrowLeft');press('1')",context);
 assert.equal(vm.runInContext("actions.join(',')",context),'step:1,step:-1,tier:1');
});
test('consecutive Space and Enter and live renders retain band focus without reviewing',()=>{
 const context=setup();
 vm.runInContext(`
  handlers={};
  document.addEventListener=(type,handler)=>{handlers[type]=handler};
  document.activeElement=document.body;
  const surface={
   buttons:[],
   querySelectorAll(){return this.buttons},
   set innerHTML(html){
    if(this.buttons.includes(document.activeElement))document.activeElement=document.body;
    this.buttons=[...html.matchAll(/data-band="([^"]+)" data-band-item="([^"]+)"/g)].map(match=>({
     tagName:'BUTTON',dataset:{band:match[1],bandItem:match[2]},
     closest(selector){return selector.includes('#rvMain')?surface:selector.includes('.band-controls')||selector==='[data-band-item]'?this:null},
     focus(){document.activeElement=this}
    }));
   }
  };
  const oldDollar=$;
  $=id=>id==='rvMain'?surface:oldDollar(id);
  $('rvKeys').children=[];$('rvKeys').querySelectorAll=()=>[];
  rvProgress=()=>{};renderAsk=()=>{};buildWhy=()=>[];zulu=()=> '12:00:00Z';
  imageFor=()=>'/frame.jpg';boxesHTML=()=>'';
  rvAct=()=>{rv.cur.reviewed=true};
  state.page='rv';
  const item={...normalizeItem(raw('multi')),bands:['EO','IR'],src:{id:'EO-P7',name:'Mast'},centralTier:1,edge:.5,dets:[],det:[]};
  SRC['EO-P7']=item.src;
  items=[item];rv.cur=item;
  function press(key){
   let prevented=false;
   handlers.keydown({key,target:document.activeElement,preventDefault(){prevented=true}});
   if(!prevented&&(key===' '||key==='Enter'))handlers.click({target:document.activeElement,stopPropagation(){}});
  }
  renderReview();
  surface.buttons.find(b=>b.dataset.band==='IR').focus();
 `,context);
 const start=source.indexOf(" document.addEventListener('click',e=>{const b=e.target.closest('[data-band-item]')");
 const end=source.indexOf(" $('rvKeys').onclick",start);
 assert.ok(start>=0&&end>start);
 vm.runInContext(source.slice(start,end),context);
 vm.runInContext("originalButton=document.activeElement;press(' ');press('Enter')",context);
 assert.equal(vm.runInContext("!rv.cur.reviewed&&bandFor(rv.cur)==='IR'&&document.activeElement.dataset.band==='IR'&&document.activeElement!==originalButton",context),true);
 vm.runInContext("press('B')",context);
 assert.equal(vm.runInContext("!rv.cur.reviewed&&bandFor(rv.cur)==='EO'&&document.activeElement.dataset.band==='IR'",context),true);
 vm.runInContext("originalButton=document.activeElement;applyItemUpdate({...rv.cur,src:'EO-P7',central:.95})",context);
 assert.equal(vm.runInContext("!rv.cur.reviewed&&document.activeElement.dataset.band==='IR'&&document.activeElement!==originalButton",context),true);
 vm.runInContext("press('Enter')",context);
 assert.equal(vm.runInContext("!rv.cur.reviewed&&bandFor(rv.cur)==='IR'&&!!document.activeElement.closest('.band-controls button')",context),true);
});
function setup(){
 const elements=new Map();
 const context=vm.createContext({
  gsLights:require('./static/lights.js'),
  setTimeout:()=>{},clearTimeout:()=>{},imageCache:new Map(),
  document:{body:{classList:{remove(){}}},querySelector:()=>null},
  $:id=>{if(!elements.has(id))elements.set(id,{classList:{toggle(){}},querySelector:()=>({}),setAttribute(){}});return elements.get(id)},
 });
 context.window=context;
 vm.runInContext(policySource,context);
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

test('unknown contact labels, distinct boxes and persistence explanations',()=>{
 const context=setup();
 vm.runInContext(`
  CLS={unknown:{lab:'UNKNOWN — not classified',g:'unknown'}};
  const W=480,H=300;
  MODELS={maritime:{name:'Maritime'}};
  const d={cls:'unknown',conf:.4,attrs:{},b:{x:200,y:180,w:30,h:20}};
  const sd={s:.7,notes:[]};
  const unknownItem={dets:[d],det:[sd],edge:.4,central:.7};
 `,context);
 assert.match(vm.runInContext('boxesHTML(unknownItem,true)',context),/unknown.*UNKNOWN — not classified/);
 assert.match(vm.runInContext('buildWhy(unknownItem,[[d,sd]])[0].t',context),/persisted across frames — not noise/);
 vm.runInContext('d.attrs.transient=true',context);
 assert.match(vm.runInContext('buildWhy(unknownItem,[[d,sd]])[0].t',context),/Single-frame flicker, treated as noise/);
 assert.equal(vm.runInContext('boxesHTML({dets:[],det:[]},true)',context),'');
 vm.runInContext(`
  unknownItem.night=true;unknownItem.src={mode:'eo'};
  d.sig={severity:'urgent',line:'Vessel not under command',ref:'COLREG'};
 `,context);
 const why=vm.runInContext('buildWhy(unknownItem,[[d,sd]])',context);
 assert.ok(why.some(row=>row.t.includes('Navigation lights')));
 assert.ok(why.some(row=>row.t.includes('Single-frame flicker, treated as noise')));
});

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
 vm.runInContext("toggleHold()",context);
 assert.equal(vm.runInContext("!state.paused&&pendingItems.length===0&&items.length===3&&findItem('queued').tier===1&&!findItem('evicted')&&!findItem('not-in-snapshot')&&!!findItem('snapshot-only')",context),true);
});

test('empty paused snapshot preserves visible items and queue',()=>{
 const context=setup();
 vm.runInContext("state.paused=true;items=[normalizeItem(raw('visible'))];pendingItems=[{item:raw('queued')}];applySnapshot(snapshot([]))",context);
 assert.equal(vm.runInContext("items.length===1&&pendingItems.length===1",context),true);
});

test('toggle hold pauses and resume drains pending items',()=>{
 const context=setup();
 vm.runInContext("pendingItems=[{item:raw('queued')}];toggleHold()",context);
 assert.equal(vm.runInContext("state.paused&&pendingItems.length===1&&items.length===0",context),true);
 vm.runInContext("toggleHold()",context);
 assert.equal(vm.runInContext("!state.paused&&pendingItems.length===0&&items.length===1&&!!findItem('queued')",context),true);
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
 vm.runInContext("toggleHold()",context);
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
 vm.runInContext("toggleHold()",context);
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

test('shipLine formats classified ships and omits absent details',()=>{
 const context=setup();
 assert.equal(vm.runInContext("shipLine({})",context),'');
 assert.equal(vm.runInContext("shipLine({ship:{type:'tanker',label:'Tanker',conf:0.82,aspect:'beam',size:'large'}})",context),'Type: Tanker (82%) · Aspect: beam · Size: large');
 assert.equal(vm.runInContext("shipLine({ship:{type:'unknown',label:'Unknown',conf:0.41}})",context),'Type: Unknown (41%)');
 assert.equal(vm.runInContext("shipLine({ship:{type:'cargo',label:'Cargo',conf:0.7}})",context),'Type: Cargo (70%)');
 assert.equal(vm.runInContext("shipVisibleLine({ship:{features:['radar mast','cranes'],markings:'not visible'}})",context),'Visible: radar mast, cranes · markings not visible');
});
