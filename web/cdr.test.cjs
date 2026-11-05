const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');

const source=fs.readFileSync(path.join(__dirname,'static/cdr.js'),'utf8');

function setup(){
 const elements=new Map();
 const context=vm.createContext({
  imageFor:()=>'/frame.jpg',
  zulu:()=> '12:00:00Z',
  setInterval:()=>0,
  document:{querySelector:()=>null,addEventListener(){}},
 });
 context.$=id=>{
  if(!elements.has(id))elements.set(id,{
   innerHTML:'',textContent:'',value:'',dataset:{},hidden:false,disabled:false,
   classList:{toggle(){},add(){},remove(){}},
   querySelector:()=>null,querySelectorAll:()=>[],scrollIntoView(){},addEventListener(){},
  });
  return elements.get(id);
 };
 vm.runInContext(source.replace(/init\(\)\.catch\(e=>toast\('Could not load configuration: '\+e\.message\)\);\s*$/,''),context);
 return {elements,context};
}

test('commander queue and note templates escape item ids',()=>{
 const {elements,context}=setup();

 const id='sighting:x"><style>.row{display:none}</style>';
 context.testItem={
  id,src:{id:'EO-P7',name:'EO-P7',loc:'',em:'',link:''},sent:true,tier:1,central:.9,edge:.8,
  sentAt:new Date(),ts:new Date(),dets:[],det:[],frame:1,dbId:1,upload:{done:true},
 };
 vm.runInContext('items=new Map([[testItem.id,testItem]]);view.sel=testItem.id;renderQueue();renderStage()',context);

 const expected='data-id="sighting:x&quot;&gt;&lt;style&gt;';
 assert.ok(elements.get('qlist').innerHTML.includes(expected));
 assert.ok(elements.get('brief').innerHTML.includes(expected));
 assert.doesNotMatch(elements.get('qlist').innerHTML,/<style/);
 assert.doesNotMatch(elements.get('brief').innerHTML,/<style/);
 context.testItem={...context.testItem,id:'negative',detectorNegative:true,dets:null,det:null};
 vm.runInContext('upsert(testItem);view.sel=testItem.id;renderQueue();renderStage()',context);
 assert.match(elements.get('qlist').innerHTML,/No contacts/);
 assert.ok(Array.isArray(context.testItem.dets));
 assert.ok(Array.isArray(context.testItem.det));
});

test('shore view excludes ship backlog even after escalation and includes completed uploads',()=>{
 const {elements,context}=setup();
 context.rows=[
  {id:'backlog',src:'BY-12',ts:new Date(),sent:true,upload:{done:false,sent:12}},
  {id:'received',src:'BY-12',ts:new Date(),upload:{done:true}},
  {id:'report',src:'BY-12',ts:new Date(),sentAt:new Date(),tier:1,central:.9,sent:true,upload:{done:true}},
 ];
 vm.runInContext('rows.forEach(upsert);renderKpis()',context);
 assert.equal(vm.runInContext('received().length',context),2);
 assert.equal(vm.runInContext('pending().length',context),1);
 assert.equal(vm.runInContext("items.has('backlog')",context),false);
 assert.equal(elements.get('kReceived').textContent,2);
 assert.equal(vm.runInContext('view.tab',context),'received');
 context.rows[1].upload.done=false;
 vm.runInContext('upsert(rows[1])',context);
 assert.equal(vm.runInContext('received().length',context),1);
});

test('live shore receipt distinguishes summary from images and ignores partial-only and scene items',()=>{
 const {elements,context}=setup();
 context.rows=[
  {id:'summary',src:'new-camera',ts:new Date(),tier:2,central:.7,edge:.6,dets:[],det:[],live:{forms:{fact:{state:'received'},thumb:{state:'partial',received:10,bytes:50}}}},
  {id:'partial',src:'new-camera',ts:new Date(),live:{forms:{thumb:{state:'partial'}}}},
  {id:'scene',src:'new-camera',ts:new Date(),live:{event:'scene',forms:{thumb:{state:'received'}}}},
 ];
 vm.runInContext('rows.forEach(upsert);view.sel="summary";renderQueue();renderStage()',context);
 assert.equal(vm.runInContext('received().length',context),1);
 assert.match(elements.get('stage').innerHTML,/Summary only · image not received/);
 assert.match(elements.get('stage').innerHTML,/Preview: partial/);
 assert.doesNotMatch(elements.get('brief').innerHTML,/<textarea|class="dec"/);
 assert.match(elements.get('brief').innerHTML,/when the watch floor sends a report/);
 context.rows[0].live.imageForm='thumb';
 context.rows[0].live.forms.thumb.state='received';
 vm.runInContext('upsert(rows[0]);renderStage()',context);
 assert.match(elements.get('stage').innerHTML,/Image received/);
 assert.doesNotMatch(elements.get('stage').innerHTML,/Summary only/);
});

test('awareness-only receipts cannot use command decision API',async()=>{
 const {context}=setup();let calls=0;
 context.fetch=async()=>{calls++;throw new Error('unexpected request')};
 context.row={id:'awareness',src:'BY-12',ts:new Date(),upload:{done:true}};
 vm.runInContext('upsert(row);view.sel=row.id',context);
 await vm.runInContext("decide('action')",context);
 assert.equal(calls,0);
});

test('live snapshots register shore sources and labels before rendering received items',()=>{
 const {elements,context}=setup();
 const handlers={};
 context.EventSource=function(){this.addEventListener=(name,fn)=>{handlers[name]=fn}};
 context.message={
  live:{sources:[{id:'live-camera',name:'Bridge camera',loc:'At sea'}],classes:{contact:{lab:'Vessel',g:'vessel'}}},
  state:{model:'maritime',directives:[]},
  items:[{id:'live-receipt',src:'live-camera',ts:new Date(),tier:2,central:.7,edge:.6,dets:[{cls:'contact',conf:.9,attrs:{}}],det:[],live:{forms:{fact:{state:'received'}}}}],
 };
 vm.runInContext('connect()',context);
 handlers.snapshot({data:JSON.stringify(context.message)});
 assert.match(elements.get('qlist').innerHTML,/Bridge camera/);
 assert.match(elements.get('qlist').innerHTML,/Vessel/);
 assert.match(elements.get('stage').innerHTML,/At sea/);
});

test('received report moves through pending and decided without changing receipt count',()=>{
 const {elements,context}=setup();
 context.row={id:'report',src:'BY-12',ts:new Date(),tier:4,central:0,edge:.03,dets:null,det:null,upload:{done:true}};
 vm.runInContext('upsert(row);row.sent=true;row.sentAt=new Date();upsert(row);renderKpis()',context);
 assert.equal(elements.get('kPend').textContent,1);
 vm.runInContext("row.decision='track';row.decidedAt=new Date();upsert(row);renderKpis();view.tab='decided';ensureSel();renderStage()",context);
 assert.equal(elements.get('kPend').textContent,0);
 assert.equal(elements.get('kDone').textContent,1);
 assert.equal(elements.get('kReceived').textContent,1);
 assert.match(elements.get('brief').innerHTML,/Commander/);
 assert.match(elements.get('brief').innerHTML,/Track/);
});

test('full-size transfer preserves previous receipts and decisions across snapshots',()=>{
 const {elements,context}=setup();
 context.rows=['awareness','pending','decided'].map((id,i)=>({
  id,src:'BY-12',ts:new Date(),tier:2,central:.7,edge:.6,dets:[],det:[],
  sent:i>0,sentAt:i>0?new Date():null,decision:i===2?'track':null,decidedAt:i===2?new Date():null,
  fullRes:true,upload:{done:false,sent:100,received:true},
 }));
 vm.runInContext('items=new Map();rows.forEach(upsert);renderKpis();view.sel="decided";renderStage()',context);
 assert.equal(elements.get('kReceived').textContent,3);
 assert.equal(elements.get('kPend').textContent,1);
 assert.equal(elements.get('kDone').textContent,1);
 assert.match(elements.get('stage').innerHTML,/Image received · full-size transfer pending/);
 assert.match(elements.get('brief').innerHTML,/Track/);
 context.unreceived={...context.rows[0],id:'unreceived',upload:{done:false,received:false}};
 vm.runInContext('upsert(unreceived);renderKpis()',context);
 assert.equal(elements.get('kReceived').textContent,3);
});
