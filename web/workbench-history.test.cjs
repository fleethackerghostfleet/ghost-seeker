const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');
const source=fs.readFileSync(path.join(__dirname,'static/workbench.js'),'utf8');
const tick=()=>new Promise(resolve=>setImmediate(resolve));
function setup(){
 const nodes=new Map(),calls=[],messages=[];
 const node=()=>({value:'',innerHTML:'',textContent:'',children:[],checked:false,disabled:false,hidden:false,open:false,dataset:{},
  classList:{toggle(){},contains:()=>false},querySelector:()=>null,querySelectorAll:()=>[],addEventListener(){},focus(){},setSelectionRange(){},
  replaceChildren(){this.children=[]},append(n){this.children.push(n)},showModal(){this.open=true},close(){this.open=false}});
 const $=id=>{if(!nodes.has(id))nodes.set(id,node());return nodes.get(id)};
 const items=[{id:'a',src:{id:'EO-P7'},ts:Date.now(),tier:3,central:.4,dets:[{cls:'small boat',attrs:{},conf:.9}],frame:1},
  {id:'b',src:{id:'SAR'},ts:Date.now(),tier:2,central:.7,dets:[{cls:'tanker',attrs:{},conf:.8}],frame:2}];
 const context=vm.createContext({renderImageHTML:(root,html)=>{root.innerHTML=html},$,items,CLS:{'small boat':{lab:'Small boat'},tanker:{lab:'Tanker'}},ATTR_LAB:{moving:'Moving',dark:'Dark',approaching:'Approaching'},
  SOURCES:[{id:'EO-P7'},{id:'SAR'}],TNAME:['','Critical','High','Routine','Low'],state:{page:'rp'},gsPolicy:{isPolicyHotkeyTarget:()=>false},
  tcol:()=>'',esc:String,ruleLabel:r=>r.type+' '+r.classes.join(', '),imageFor:()=>'/frame.jpg',findItem:id=>items.find(i=>i.id===id),isScene:()=>false,
  toast:m=>messages.push(m),CSS:{escape:String},setTimeout:()=>1,clearTimeout(){},
  document:{activeElement:{closest:()=>null},querySelector:()=>$('wbConfirm').open?{}:null,addEventListener(){},createElement:node}});
 const score=body=>{
  const results=items.map(i=>({id:i.id,tier:i.id==='a'&&body.rules.length?(body.rules[0].type==='boost'?1:4):i.tier,central:i.central,contrib:[]}));
  return {items:results,counts:[1,2,3,4].map(t=>results.filter(i=>i.tier===t).length),candidates:(body.candidates||[]).map(c=>({key:c.key,counts:[1,1,0,0],changed:1}))};
 };
 context.request=async(url,method,body)=>{
  calls.push({url,method,body:JSON.parse(JSON.stringify(body))});
  assert.equal(url,'/api/directives/preview','draft interactions must never write live directives');
  return score(body);
 };
 vm.runInContext(source+'\nworkbench.init();',context);
 const rules=()=>calls.filter(c=>c.body.rules).at(-1).body.rules;
 const edit=(field,extra={})=>$('wbChips').onchange({target:{dataset:{field},closest:()=>({dataset:{rule:'0'}}),...extra}});
 return {$,context,items,calls,messages,rules,edit,score,run:code=>vm.runInContext(code,context)};
}
test('undo/redo restore independent rule snapshots, clamp at boundaries and discard abandoned redo branches',async()=>{
 const h=setup();await h.$('wbReset').onclick();
 assert.equal(h.$('wbUndo').disabled,true);assert.equal(h.$('wbRedo').disabled,true);
 const baselineCalls=h.calls.length;await h.$('wbUndo').onclick();assert.equal(h.calls.length,baselineCalls);
 await h.$('wbAdd').onclick();assert.equal(h.rules()[0].type,'boost');
 await h.edit('flip');assert.equal(h.rules()[0].type,'suppress');
 await h.$('wbUndo').onclick();assert.equal(h.rules()[0].type,'boost');
 await h.$('wbUndo').onclick();assert.deepEqual(h.rules(),[]);
 await h.$('wbRedo').onclick();assert.equal(h.rules()[0].type,'boost');
 await h.edit('plus');assert.equal(h.rules()[0].weight,1.2);
 assert.equal(h.$('wbRedo').disabled,true);assert.doesNotMatch(h.$('wbHistory').innerHTML,/<strong>v3 ·/);
 assert.match(h.$('wbHistory').innerHTML,/<strong>v4 ·/);
 await h.$('wbUndo').onclick();assert.equal(h.rules()[0].weight,1,'editing the restored draft must not mutate its saved version');
 await h.$('wbRedo').onclick();assert.equal(h.rules()[0].weight,1.2);
});
test('historical comparison uses recorded tiers rather than a changed live baseline and performs no request',async()=>{
 const h=setup();await h.$('wbReset').onclick();await h.$('wbAdd').onclick();
 h.items[0].tier=4;
 const count=h.calls.length;
 await h.$('wbHistory').onclick({target:{dataset:{compare:'0'}}});
 assert.match(h.$('wbCompareLabel').textContent,/Before: v1 recorded results/);
 assert.match(h.$('wbCounts').innerHTML,/P3 Routine<b>1 → 0<\/b>/);
 h.$('wbBaseline').onclick();
 assert.match(h.$('wbCompareLabel').textContent,/Before: current live feed/);
 assert.match(h.$('wbCounts').innerHTML,/P4 Low<b>1 → 0<\/b>/);
 assert.equal(h.calls.length,count);
});
test('specificity suggestions change the draft only and rule weight buttons clamp both bounds',async()=>{
 const h=setup();await h.$('wbReset').onclick();await h.$('wbAdd').onclick();
 const candidates=h.calls.at(-1).body.candidates;
 const dark=candidates.find(c=>c.rules[0].attrs[0]==='dark');assert.ok(dark);
 assert.ok(candidates.some(c=>c.rules[0].sources[0]==='EO-P7'));
 assert.ok(candidates.some(c=>c.rules[0].withinMin===30));
 assert.ok(candidates.some(c=>c.rules[0].attrs[0]==='approaching'));
 await h.$('wbSuggestions').onclick({target:{closest:()=>({dataset:{suggest:dark.key}})}});
 assert.deepEqual(h.rules()[0].attrs,['dark']);
 assert.match(h.$('nlIn').value,/Prioritize small boat Dark/);
 await h.edit('weight',{value:'3'});await h.edit('plus');assert.equal(h.rules()[0].weight,3);
 await h.edit('weight',{value:'.2'});await h.edit('minus');assert.equal(h.rules()[0].weight,.2);
 await h.edit('remove');assert.deepEqual(h.rules(),[]);assert.equal(h.$('wbApply').disabled,true);
});
test('failed previews disable Apply without saving a version, and the next successful edit recovers',async()=>{
 const h=setup();await h.$('wbReset').onclick();
 const history=h.$('wbHistory').innerHTML;
 const request=h.context.request;h.context.request=async()=>{throw Error('preview unavailable')};
 await h.$('wbAdd').onclick();
 assert.equal(h.$('wbApply').disabled,true);assert.match(h.$('wbStatus').textContent,/preview unavailable/);
 assert.equal(h.$('wbHistory').innerHTML,history);
 h.context.request=request;await h.$('wbAdd').onclick();
 assert.equal(h.$('wbApply').disabled,false);assert.equal(h.rules().length,2);
 assert.match(h.$('wbHistory').innerHTML,/<strong>v2 ·/);
});
test('clearItems invalidates an in-flight preview so cleared results cannot reappear',async()=>{
 const h=setup();await h.$('wbReset').onclick();
 let finish;h.context.request=body=>new Promise(resolve=>finish=resolve);
 const preview=h.$('wbAdd').onclick();await tick();assert.ok(finish);
 h.run('workbench.clearItems()');
 finish(h.score({rules:[{type:'boost'}]}));await preview;
 assert.equal(h.$('wbGrid').innerHTML,'');assert.equal(h.$('wbDropped').innerHTML,'');
 assert.equal(h.$('wbApply').disabled,true);assert.equal(h.$('wbProposal').hidden,true);
 assert.doesNotMatch(h.$('wbHistory').innerHTML,/<strong>v2 ·/);
});
