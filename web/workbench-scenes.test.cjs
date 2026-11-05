const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');
const source=fs.readFileSync(path.join(__dirname,'static/workbench.js'),'utf8');

function setup(){
 const elements=new Map(),writes=[],messages=[];
 const element=()=>({value:'',checked:false,dataset:{},classList:{toggle(){}},querySelector:()=>null,querySelectorAll:()=>[],addEventListener(){},focus(){},close(){},setSelectionRange(){}});
 const items=['first','second','ocean'].map((id,i)=>({id,manual:i===0?2:0,tier:4,central:.1,frame:i,src:{id:'camera'},ts:Date.now(),dets:[],det:[],...(id==='ocean'?{live:{event:'scene'}}:{})}));
 const c=vm.createContext({items,findItem:id=>items.find(i=>i.id===id),isScene:i=>i?.live?.event==='scene',
  $:id=>{if(!elements.has(id))elements.set(id,element());return elements.get(id)},
  document:{activeElement:{closest:()=>null},querySelector:()=>null,addEventListener(){}},
  setTimeout:()=>1,clearTimeout(){},CLS:{},ATTR_LAB:{},SOURCES:[],TNAME:['','Critical','High','Routine','Low'],state:{page:'rp'},
  esc:String,tcol:()=>'',ruleLabel:()=>'',imageFor:()=>'/frame.jpg',toast:m=>messages.push(m),
  request:async(url,method,body)=>{if(url.endsWith('/preview'))return {items:items.map(({id,tier,central})=>({id,tier,central})),candidates:[],counts:[0,0,0,3]};writes.push({url,method,body});return {}},
  patchItem:async(id,changes)=>{writes.push({id,...changes});items.find(i=>i.id===id).manual=changes.manual},
 });
 vm.runInContext(source,c);vm.runInContext('workbench.init()',c);
 const pin=(id,value)=>c.$('wbGrid').onchange({target:{classList:{contains:()=>true},closest:()=>({dataset:{id}}),value:String(value)}});
 return {c,items,writes,messages,pin,add:()=>c.$('wbAdd').onclick(),apply:()=>c.$('wbConfirmApply').onclick()};
}
test('workbench never offers scenes as correction or exception targets',async()=>{
 const h=setup();await h.add();
 assert.doesNotMatch(h.c.$('wbGrid').innerHTML,/data-id="ocean"/);
 h.pin('ocean',1);await h.apply();
 assert.equal(h.writes.some(w=>w.id==='ocean'),false);
 assert.equal(h.writes.filter(w=>w.url==='/api/directives').length,1);
});
test('stale scene pins are rejected before any draft writes',async()=>{
 const h=setup();await h.add();h.pin('first',1);h.items[0].live={event:'scene'};
 await h.apply();assert.deepEqual(h.writes,[]);
 assert.match(h.messages.at(-1),/no draft changes were saved/);
});
test('a rejected exception restores earlier pins and does not save directives',async()=>{
 const h=setup();await h.add();h.pin('first',1);h.pin('second',3);
 const patch=h.c.patchItem;h.c.patchItem=async(id,changes)=>{if(id==='second')throw new Error('409 conflict');return patch(id,changes)};
 await h.apply();assert.equal(h.items[0].manual,2);
 assert.deepEqual(h.writes.map(w=>[w.id,w.manual]),[['first',1],['first',2]]);
 assert.match(h.messages.at(-1),/409 conflict.*no draft changes were saved/);
 h.c.patchItem=patch;await h.apply();
 assert.equal(h.items[0].manual,1);assert.equal(h.items[1].manual,3);
 assert.equal(h.writes.at(-1).url,'/api/directives');
});
test('directive rejection restores exceptions and retains the draft for retry',async()=>{
 const h=setup();await h.add();h.pin('first',1);
 const request=h.c.request;h.c.request=async(url,...args)=>{if(url==='/api/directives')throw new Error('policy rejected');return request(url,...args)};
 await h.apply();assert.equal(h.items[0].manual,2);
 h.c.request=request;await h.apply();
 assert.equal(h.items[0].manual,1);assert.equal(h.writes.at(-1).url,'/api/directives');
});
test('failed compensation is reported instead of claiming an atomic rollback',async()=>{
 const h=setup();await h.add();h.pin('first',1);h.pin('second',3);
 const patch=h.c.patchItem;h.c.patchItem=async(id,changes)=>{if(id==='second'||changes.manual===2)throw new Error('offline');return patch(id,changes)};
 await h.apply();assert.match(h.messages.at(-1),/exception rollback failed/);
});
test('empty apply sends no mutations',async()=>{
 const h=setup();await h.apply();assert.deepEqual(h.writes,[]);
});
