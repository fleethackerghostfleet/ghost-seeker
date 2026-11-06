const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');
const source=fs.readFileSync(path.join(__dirname,'static/cdr.js'),'utf8');
function setup(){
 const nodes=new Map(),handlers={},calls=[];
 const context=vm.createContext({imageCache:new Map(),imageFor:()=>'/frame.jpg',zulu:()=> '12:00:00Z',setInterval(){},setTimeout(){},clearTimeout(){},
  document:{querySelector:()=>null,addEventListener:(type,fn)=>handlers[type]=fn},
  EventSource:class{addEventListener(){}},
 });
 context.$=id=>{
  if(!nodes.has(id))nodes.set(id,{innerHTML:'',textContent:'',value:'',dataset:{},children:[],scrollTop:0,
   classList:{toggle(){},add(){},remove(){}},querySelector:()=>null,focus(){this.focused=true},blur(){this.blurred=true}});
  return nodes.get(id);
 };
 context.fetch=async(url,options)=>{calls.push({url,options});return {ok:true,json:async()=>({sources:[{id:'EO',name:'Mast'}],classes:{},models:{},attrLabels:{}})}};
 vm.runInContext(source.replace(/init\(\)\.catch\(e=>toast\('Could not load configuration: '\+e\.message\)\);\s*$/,''),context);
 vm.runInContext(`
  function item(id,extra={}){return {id,src:'EO',ts:'2026-01-01T12:00:00Z',sentAt:'2026-01-01T11:00:00Z',tier:2,central:.7,edge:.5,dets:[],det:[],sent:true,upload:{done:true},...extra}}
  function seed(rows){SRC={EO:{id:'EO',name:'Mast'}};items.clear();rows.forEach(upsert)}
 `,context);
 return {context,nodes,calls,handlers,run:code=>vm.runInContext(code,context)};
}
test('Commander bucket ordering uses all priority, score, report-time and receipt-time tie-breaks',()=>{
 const h=setup();
 h.run(`seed([
  item('lower-tier',{tier:3,central:.99,ts:'2026-01-01T15:00:00Z'}),
  item('lower-score',{tier:1,central:.8,ts:'2026-01-01T14:00:00Z'}),
  item('later-report',{tier:1,central:.9,sentAt:'2026-01-01T11:30:00Z',ts:'2026-01-01T13:00:00Z'}),
  item('earlier-report',{tier:1,central:.9}),
  item('awareness',{sent:false,ts:'2026-01-01T16:00:00Z'}),
  item('older-decision',{decision:'track',decidedAt:'2026-01-01T12:00:00Z'}),
  item('newer-decision',{decision:'close',decidedAt:'2026-01-01T13:00:00Z'})
 ])`);
 assert.equal(h.run("pending().map(i=>i.id).join(',')"),'earlier-report,later-report,lower-score,lower-tier');
 assert.equal(h.run("received().slice(0,4).map(i=>i.id).join(',')"),'awareness,lower-tier,lower-score,later-report');
 assert.equal(h.run("decided().map(i=>i.id).join(',')"),'newer-decision,older-decision');
 h.run("items.clear();renderKpis();ensureSel()");
 assert.equal(h.run('view.sel'),null);
 for(const id of ['kReceived','kPend','kP1','kDone'])assert.equal(h.context.$(id).textContent,0);
 assert.equal(h.context.$('kOld').textContent,'—');
});
test('selection stays within the active bucket and stepping clamps at both ends',()=>{
 const h=setup();h.run("seed([item('a',{tier:1}),item('b'),item('done',{decision:'track'})]);view.tab='pending';view.sel='done';ensureSel();renderQueue=()=>{};renderStage=()=>{}");
 assert.equal(h.run('view.sel'),'a');
 h.run('step(-1)');assert.equal(h.run('view.sel'),'a');
 h.run('step(1);step(1)');assert.equal(h.run('view.sel'),'b');
 h.run("ensureSel()");assert.equal(h.run('view.sel'),'b');
 h.run("setTab('decided');ensureSel()");assert.equal(h.run('view.sel'),'done');
 h.run("items.clear();ensureSel();step(1)");assert.equal(h.run('view.sel'),null);
});
test('Commander keys navigate without decisions and respect text editing, modifiers and read-only roles',async()=>{
 const h=setup();await h.run('init()');
 h.run("seed([item('a',{tier:1}),item('b')]);view.tab='pending';view.sel='a';renderQueue=()=>{};renderStage=()=>{};decisions=[];decide=d=>decisions.push(d);undos=0;undo=()=>undos++");
 const press=(key,extra={},target={tagName:'BODY'})=>{
  let prevented=false;h.handlers.keydown({key,target,preventDefault(){prevented=true},...extra});return prevented;
 };
 assert.equal(press('ArrowDown'),true);assert.equal(h.run('view.sel'),'b');
 press('k');assert.equal(h.run('view.sel'),'a');
 press('1',{repeat:true});assert.equal(h.run('decisions.length'),0);
 press('1');press('b');assert.equal(h.run("decisions.join(',')"),'action,return');
 press('/');assert.equal(h.context.$('note').focused,true);
 press('z');assert.equal(h.run('undos'),1);
 const input={tagName:'TEXTAREA',blur(){this.blurred=true}};
 assert.equal(press('1',{},input),false);press('Escape',{},input);assert.equal(input.blurred,true);
 for(const modifier of ['ctrlKey','metaKey','altKey'])assert.equal(press('1',{[modifier]:true}),false);
 for(const role of ['viewer','reviewer']){
  h.context.gsAuth={viewer:()=>role==='viewer',reviewer:()=>role==='reviewer'};
  assert.equal(press('ArrowDown'),false);assert.equal(press('1'),false);
 }
 assert.equal(h.run("decisions.join(',')"),'action,return');
});
test('decision rejection preserves the note and undo stack; failed undo is retryable',async()=>{
 const h=setup();h.run("seed([item('a')]);view.tab='pending';view.sel='a';$('note').value='Keep this draft'");
 const requests=[];
 h.context.fetch=async(url,options)=>{requests.push({url,body:JSON.parse(options.body)});return {ok:false,statusText:'Forbidden',json:async()=>({message:'shore rejected decision'})}};
 assert.equal(await h.run("decide('track')"),false);
 assert.deepEqual(requests[0],{url:'/api/items/a/decision',body:{decision:'track',note:'Keep this draft'}});
 assert.equal(h.context.$('note').value,'Keep this draft');
 assert.equal(h.run('view.busy'),false);assert.equal(h.run('view.undo.length'),0);
 assert.match(h.context.$('toast').textContent,/shore rejected decision/);
 h.run("items.get('a').decision='track';view.undo=['a']");
 await h.run('undo()');assert.equal(h.run("view.undo.join(',')"),'a');
 h.context.fetch=async()=>({ok:true,json:async()=>({item:h.run("item('a')")})});
 await h.run('undo()');
 assert.equal(h.run('view.undo.length'),0);assert.equal(h.run("items.get('a').decision"),undefined);
 assert.equal(h.run('view.tab'),'pending');assert.equal(h.run('view.sel'),'a');assert.equal(h.run('view.busy'),false);
});
test('a decision response from before a reset cannot resurrect an evicted image',async()=>{
 const h=setup();h.run("seed([item('a')]);view.sel='a'");
 let finish;h.context.fetch=()=>new Promise(resolve=>finish=resolve);
 const pending=h.run("decide('track')");
 assert.equal(h.run('view.busy'),true);
 h.run("applySnapshot({reset:true,epoch:2,state:{model:'maritime',directives:[]},items:[]})");
 finish({ok:true,json:async()=>({item:h.run("item('a',{decision:'track'})")})});
 assert.equal(await pending,false);
 assert.equal(h.run('items.size'),0);assert.equal(h.run('view.sel'),null);
 assert.equal(h.run('view.undo.length'),0);assert.equal(h.run('view.busy'),false);
});
