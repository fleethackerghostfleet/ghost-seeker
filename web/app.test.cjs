const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');

const source=fs.readFileSync(path.join(__dirname,'static/app.js'),'utf8');
const workbenchSource=fs.readFileSync(path.join(__dirname,'static/workbench.js'),'utf8');
const policySource=fs.readFileSync(path.join(__dirname,'static/policy.js'),'utf8');
const settingsSource=fs.readFileSync(path.join(__dirname,'static/settings.js'),'utf8');
function settingsContext(saved){
 const context=vm.createContext({window:{},localStorage:{getItem:()=>saved,setItem:(k,v)=>{saved=v}},document:{body:{style:{setProperty(){},removeProperty(){}},classList:{[Symbol.iterator]:function*(){},add(){},remove(){},toggle(){}}},getElementById:()=>({querySelectorAll:()=>[],dataset:{}})}});
 vm.runInContext(settingsSource,context);
 return {settings:context.window.gsSettings,saved:()=>JSON.parse(saved)};
}
test('sidebar widths default empty, load valid settings, and clamp persisted values',()=>{
 for(const saved of [null,'broken','{}','{"widths":{"drawer":"bad","review":null}}']){
  assert.equal(JSON.stringify(settingsContext(saved).settings.get().widths),'{}');
 }
 const {settings}=settingsContext('{"bar":"right","widths":{"controls":-2,"drawer":9999,"review":360,"unknown":400}}');
 assert.equal(settings.get().bar,'right');
 assert.equal(JSON.stringify(settings.get().widths),'{"controls":240,"drawer":780,"review":360}');
});
test('sidebar resize persists, rejects invalid sizes, supports reset and defensive reads',()=>{
 const {settings,saved}=settingsContext('{}');
 settings.resize('controls',321.8);assert.equal(saved().widths.controls,322);
 settings.resize('controls',NaN);assert.equal(saved().widths.controls,322);
 settings.resize('noSuchPanel',400);assert.equal(saved().widths.noSuchPanel,undefined);
 settings.get().widths.controls=450;assert.equal(settings.get().widths.controls,322);
 settings.resize('controls',null);assert.equal(JSON.stringify(saved().widths),'{}');
});
test('feed filters default safely, persist, validate, and return defensive copies',()=>{
 for(const value of [null,'broken','{}','{"feed":null}','{"feed":{"showFiltered":"true","review":"invalid"}}']){
  assert.equal(JSON.stringify(settingsContext(value).settings.get().feed),'{"showFiltered":false,"review":"all"}');
 }
 const {settings,saved}=settingsContext('{"bar":"right","feed":{"showFiltered":true,"review":"reviewed"}}');
 settings.get().feed.review='unreviewed';assert.equal(settings.get().feed.review,'reviewed');
 settings.setFeed({review:'unreviewed'});
 assert.deepEqual(saved().feed,{showFiltered:true,review:'unreviewed'});
 assert.equal(saved().bar,'right');
 assert.equal(settingsContext(JSON.stringify(saved())).settings.get().feed.review,'unreviewed');
 settings.setFeed({showFiltered:false,review:'bad'});
 assert.deepEqual(saved().feed,{showFiltered:false,review:'all'});
});
function feedContext(){
 const context=setup();
 vm.runInContext(`
  filters={showFiltered:false,review:'all'};gsSettings={get:()=>({feed:filters})};
  function frame(id,extra={}){return {id,src:{id:'EO-P7'},tier:2,central:.7,ts:100,img:'/'+id+'.jpg',det:[],...extra}}
  function ids(){return visible().l.map(i=>i.id).join(',')}
 `,context);
 return context;
}
test('feed checkbox reveals retained filtered and exact duplicate images at P4 without mutating scores',()=>{
 const context=feedContext();
 vm.runInContext(`
  items=[frame('original',{tier:1,img:'/same.jpg',central:.9}),frame('duplicate',{img:'/same.jpg'}),
   frame('dismissed',{dismissed:true}),frame('suppressed',{live:{suppressed:true}}),
   frame('directive',{det:[{sup:true}]}),frame('mixed',{tier:3,det:[{sup:true},{sup:false}]}),frame('low',{tier:4})];
  before=JSON.stringify(items);
 `,context);
 assert.equal(vm.runInContext('ids()',context),'original,mixed,low');
 assert.equal(vm.runInContext('visible().counts.all',context),3);
 vm.runInContext('filters.showFiltered=true',context);
 assert.equal(vm.runInContext('ids()',context),'original,mixed,low,duplicate,dismissed,suppressed,directive');
 assert.equal(vm.runInContext('visible().counts[4]',context),5);
 vm.runInContext("state.tier='4'",context);
 assert.equal(vm.runInContext('ids()',context),'low,duplicate,dismissed,suppressed,directive');
 assert.equal(vm.runInContext('JSON.stringify(items)===before',context),true);
 vm.runInContext("state.tier='all';filters.showFiltered=false;items=items.filter(i=>i.id!=='original')",context);
 assert.equal(vm.runInContext('ids()',context),'duplicate,mixed,low');
});
test('duplicate filtering never folds different sensors, missing imagery, or live sighting placeholders',()=>{
 const context=feedContext();
 vm.runInContext(`items=[frame('one',{img:'/same.jpg'}),frame('other-source',{src:{id:'IR-U1'},img:'/same.jpg'}),
  frame('empty-one',{img:''}),frame('empty-two',{img:''}),frame('live-one',{img:'/placeholder.jpg',live:{}}),frame('live-two',{img:'/placeholder.jpg',live:{}})]`,context);
 assert.equal(vm.runInContext('visible().l.length',context),6);
 vm.runInContext("state.src='IR-U1'",context);
 assert.equal(vm.runInContext('ids()',context),'other-source');
});
test('human review filter includes accepted, manually set and dismissed images and composes with other filters',()=>{
 const context=feedContext();
 vm.runInContext(`items=[frame('accepted',{reviewed:true}),frame('manual',{manual:2}),frame('dismissed',{dismissed:true}),frame('pending'),frame('other',{src:{id:'IR-U1'},reviewed:true})];filters.review='reviewed'`,context);
 assert.equal(vm.runInContext('ids()',context),'accepted,manual,other');
 vm.runInContext("filters.showFiltered=true;state.src='EO-P7'",context);
 assert.equal(vm.runInContext('ids()',context),'accepted,manual,dismissed');
 vm.runInContext("filters.review='unreviewed'",context);
 assert.equal(vm.runInContext('ids()',context),'pending');
 vm.runInContext("items.find(i=>i.id==='pending').reviewed=true",context);
 assert.equal(vm.runInContext('ids()',context),'');
 vm.runInContext("items.find(i=>i.id==='pending').reviewed=false",context);
 assert.equal(vm.runInContext('ids()',context),'pending');
});
test('reviewed-only can show a reviewed representative even when a higher-ranked duplicate is unreviewed',()=>{
 const context=feedContext();
 vm.runInContext(`items=[frame('pending',{tier:1,img:'/same.jpg'}),frame('accepted',{reviewed:true,img:'/same.jpg'})];filters.review='reviewed'`,context);
 assert.equal(vm.runInContext('ids()',context),'accepted');
 vm.runInContext("filters.review='all';filters.showFiltered=true;state.sort='new';items[1].ts=200",context);
 assert.equal(vm.runInContext('ids()',context),'accepted,pending');
});
test('manual priorities and escalations stay visible despite directive suppression or duplicate paths',()=>{
 const context=feedContext();
 vm.runInContext(`items=[frame('original',{tier:1,img:'/same.jpg'}),
  frame('manual',{tier:1,manual:1,det:[{sup:true}]}),frame('escalated',{sent:true,live:{suppressed:true}}),
  frame('manual-copy',{manual:2,img:'/same.jpg'}),frame('sent-copy',{sent:true,img:'/same.jpg'})]`,context);
 assert.equal(vm.runInContext('visible().l.length',context),5);
 vm.runInContext('filters.showFiltered=true;visible()',context);
 assert.equal(vm.runInContext('feedTier(items[1])',context),1);
 assert.equal(vm.runInContext('feedTier(items[3])',context),2);
 vm.runInContext("items.find(i=>i.id==='manual').dismissed=true;filters.showFiltered=false",context);
 assert.equal(vm.runInContext("visible().l.some(i=>i.id==='manual')",context),false);
});
test('shore rank uses all shore records by ship score regardless of review, sensor, tier or sort filters',()=>{
 const context=feedContext();
 vm.runInContext(`items=[frame('highest',{live:{score:30},tier:1}),
  frame('selected',{live:{score:20},reviewed:true}),frame('lowest',{live:{score:10}}),
  frame('suppressed',{live:{score:40,suppressed:true}})];
  filters.review='reviewed';state.sort='new';state.tier='2';state.src='IR-U1';
 `,context);
 assert.match(vm.runInContext("liveWhy(items[1])[0].t",context),/Rank 2 of 3 on shore by ship score \(20\.00\)/);
});
test('durable reviews use server actions, identity, corrections and typing guard',()=>{
 assert.match(source,/\/api\/items\/\$\{encodeURIComponent\(id\)\}\/review/);
 assert.match(source,/reviewer:reviewerName/);
 assert.match(source,/\/api\/reviews\/\$\{encodeURIComponent\(p.record\)\}\/undo/);
 assert.match(source,/e.stopPropagation\(\)/);
 assert.match(source,/wrong_detection/);
});
test('review submission captures record id and never sends model fields',async()=>{
 const context=setup();
 vm.runInContext(`reviewerName='Alice';calls=[];applyItemUpdate=()=>{};findItem=id=>({id});refreshReviewData=()=>{};request=async(...args)=>{calls.push(args);return {record:{id:'r1'},item:{id:'i'},cdr:0}};p={};`,context);
 await vm.runInContext("submitReview('i','2',{manual:2},p)",context);
 const calls=JSON.parse(vm.runInContext('JSON.stringify(calls)',context));
 assert.deepEqual(calls,[['/api/items/i/review','POST',{reviewer:'Alice',action:'tier',tier:2}]]);
 assert.equal(vm.runInContext('p.record',context),'r1');
 await vm.runInContext("saveReviewReason('r1','wrong_detection','note')",context);
 assert.equal(vm.runInContext('lastReview.reason',context),'wrong_detection');
 vm.runInContext("request=async()=>{throw Error('disk full')}",context);
 await assert.rejects(vm.runInContext("submitReview('i','accept',{manual:2},{})",context),/disk full/);
});
test('empty reviewer blocks actions before requests and empty note has no target',()=>{
 const context=setup();
 vm.runInContext("messages=[];toast=m=>messages.push(m);rvAct('accept');openReviewNote()",context);
 assert.deepEqual(JSON.parse(vm.runInContext('JSON.stringify(messages)',context)),['Set your reviewer name first','Review an image first']);
});
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
test('card source identifiers are rendered as escaped text',()=>{
 const context=setup();
 vm.runInContext(`
  imageFor=()=>'/frame.jpg';boxesHTML=()=>'';zulu=()=> '12:00';
  card=cardHTML({id:'item',src:{id:'<style>.card{display:none}</style>'},ts:new Date(),tier:1,central:.9});
 `,context);
 const html=vm.runInContext('card',context);
 assert.ok(html.includes('<span class="sid">&lt;style&gt;.card{display:none}&lt;/style&gt;</span>'));
 assert.doesNotMatch(html,/<style/);
});
test('init source options escape source ids and names',()=>{
 const context=setup();
 const select={innerHTML:''};
 context.$=id=>id==='srcSel'?select:{};
 vm.runInContext(`SOURCES=[{id:'a"><b>x</b>',name:'<b>y</b>'}]`,context);
 const start=source.indexOf(" $('srcSel').innerHTML=SOURCES.map");
 const end=source.indexOf(';',start);
 assert.ok(start>=0&&end>start);
 vm.runInContext(source.slice(start,end+1),context);
 assert.doesNotMatch(select.innerHTML,/<b>/);
 assert.ok(select.innerHTML.includes('a&quot;&gt;&lt;b&gt;x&lt;/b&gt;'));
 assert.ok(select.innerHTML.includes('&lt;b&gt;y&lt;/b&gt;'));
});
test('workbench templates escape item ids and sources and CSS-escape restored focus',async()=>{
 const context=setup();
 const timers=[],elements=new Map(),selectors=[],escaped=[];
 const element=()=>({
  innerHTML:'',textContent:'',value:'',checked:false,hidden:false,disabled:false,dataset:{},
  classList:{toggle(){},add(){},remove(){}},
  querySelector(){return null},querySelectorAll(){return []},
  addEventListener(){},focus(){},setAttribute(){},replaceChildren(){},
 });
 context.$=id=>{if(!elements.has(id))elements.set(id,element());return elements.get(id)};
 context.document.addEventListener=()=>{};
 context.setTimeout=callback=>{timers.push(callback);return timers.length};
 context.clearTimeout=()=>{};
 const id='sighting:"><b>x</b>',droppedID='drop:"><b>d</b>',sourceID='<b>y</b>';
 const data=[
  {id,src:{id:sourceID},ts:Date.now(),tier:1,central:.9,dets:[],det:[],frame:1},
  {id:droppedID,src:{id:sourceID},ts:Date.now(),tier:2,central:.7,dets:[],det:[],frame:2},
  ...Array.from({length:16},(_,i)=>({id:`safe-${i}`,src:{id:sourceID},ts:Date.now(),tier:3,central:.5-i/100,dets:[],det:[],frame:i+3})),
 ];
 context.testItems=data;
 context.CSS={escape:value=>{escaped.push(value);return`ESC(${value})`}};
 context.request=async(_path,_method,body)=>({
  items:data.filter(item=>item.id!==droppedID).map(({id,tier,central})=>({id,tier,central,contrib:[]})),
  counts:[1,0,16,0],
  candidates:[],
 });
 vm.runInContext('items=testItems;imageFor=()=>"/frame.jpg"',context);
 const activeTile={
  dataset:{id},
  closest(selector){return selector==='.wb-tile'?this:null},
  querySelectorAll(){return[]},
  focus(){},
 };
 context.document.activeElement=activeTile;
 const grid=context.$('wbGrid');
 grid.querySelector=selector=>{selectors.push(selector);return null};
 vm.runInContext(workbenchSource,context);
 vm.runInContext('workbench.init();state.page="rp";workbench.activate()',context);
 assert.equal(timers.length,1);
 timers.shift()();
 await new Promise(resolve=>setImmediate(resolve));
 const hoverTarget={closest:()=>({dataset:{id}})};
 grid.onmouseover({target:hoverTarget});
 for(const name of ['wbGrid','wbDropped','wbWhy']){
  const html=context.$(name).innerHTML;
  assert.doesNotMatch(html,/<b>/,`${name} contains unescaped source markup`);
 }
 assert.ok(grid.innerHTML.includes('data-id="sighting:&quot;&gt;&lt;b&gt;x&lt;/b&gt;"'));
 assert.ok(grid.innerHTML.includes('&lt;b&gt;y&lt;/b&gt;'));
 assert.ok(context.$('wbDropped').innerHTML.includes('data-why="drop:&quot;&gt;&lt;b&gt;d&lt;/b&gt;"'));
 assert.ok(context.$('wbWhy').innerHTML.includes('&lt;b&gt;y&lt;/b&gt;'));
 assert.deepEqual(escaped,[id]);
 assert.ok(selectors.includes(`[data-id="ESC(${id})"]`));
});
test('global keyboard shortcuts ignore input while a dialog is open',()=>{
 const context=setup();
 vm.runInContext(`
  handlers={};nightToggles=0;
  document.querySelector=selector=>selector==='dialog[open]'?{}:null;
  document.addEventListener=(type,handler)=>{if(type==='keydown')handlers[type]=handler};
  toggleNight=()=>nightToggles++;
 `,context);
 const start=source.indexOf(" document.addEventListener('keydown',e=>{if(document.querySelector('dialog[open]')||$('settings').open)return;if(gsPolicy.isPolicyHotkeyTarget(e.target))return;");
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
test('live card updates preserve canonical priority labels',()=>{
 const context=setup();
 vm.runInContext(source.slice(source.indexOf('function updateCard('),source.indexOf('function renderFeed(')),context);
 vm.runInContext(`
  badge={};other={setAttribute(){}};
  card={querySelector:selector=>selector==='.tier'?badge:selector==='.thumb'?{insertAdjacentHTML(){}}:selector==='.night-badge'?null:other};
  gsLights={badge:()=>''};boxesHTML=()=>'';topLine=()=>'';
  testItem={id:'test',el:card,tier:1,central:.9,base:.8};updateCard(testItem);
 `,context);
 assert.equal(vm.runInContext('badge.textContent',context),'P1 Critical');
 assert.equal(vm.runInContext('badge.title',context),'Notify Commander');
 vm.runInContext('testItem.tier=4;updateCard(testItem)',context);
 assert.equal(vm.runInContext('badge.textContent',context),'P4 Low');
 assert.equal(vm.runInContext('badge.title',context),'Image priority');
});
test('feed state uses clear active, paused and reconnecting labels',()=>{
 const context=setup();
 vm.runInContext("indicator={};$('live').querySelector=()=>indicator;renderLive()",context);
 assert.equal(vm.runInContext('indicator.textContent',context),'FEED ACTIVE');
 assert.equal(vm.runInContext("$('pauseBtn').textContent",context),'Pause feed');
 vm.runInContext('state.paused=true;renderLive()',context);
 assert.equal(vm.runInContext('indicator.textContent',context),'FEED PAUSED');
 assert.equal(vm.runInContext("$('pauseBtn').textContent",context),'Resume feed');
 vm.runInContext('state.offline=true;renderLive()',context);
 assert.equal(vm.runInContext('indicator.textContent',context),'RECONNECTING');
 assert.equal(vm.runInContext("$('live').title",context),'Reconnecting — click to resume feed');
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
