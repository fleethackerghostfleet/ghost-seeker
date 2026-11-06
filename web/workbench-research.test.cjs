const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const path=require('node:path');
const source=fs.readFileSync(path.join(__dirname,'static/workbench.js'),'utf8');
const appSource=fs.readFileSync(path.join(__dirname,'static/app.js'),'utf8');
const rule=(cls='fishing vessel')=>({type:'boost',classes:[cls],attrs:[],sources:[],withinMin:0,weight:2});
const tick=()=>new Promise(resolve=>setImmediate(resolve));
function setup(interpret){
 const nodes=new Map(),timers=new Map(),calls=[];let timerID=0;
 const node=()=>({value:'',innerHTML:'',textContent:'',children:[],checked:false,disabled:false,hidden:false,open:false,dataset:{},classList:{toggle(){},contains(){return false}},querySelector(){return null},querySelectorAll(){return[]},addEventListener(){},focus(){},setSelectionRange(){},replaceChildren(){this.children=[]},append(item){this.children.push(item)},showModal(){this.open=true},close(){this.open=false}});
 const $=id=>{if(!nodes.has(id))nodes.set(id,node());return nodes.get(id)};
 const items=[{id:'fish-1',src:{id:'EO-P7'},ts:Date.now(),tier:3,central:.4,dets:[{cls:'fishing vessel',attrs:{},conf:.9}],frame:1}];
 const ctx=vm.createContext({$,items,JSON,Date,console,CLS:{'fishing vessel':{lab:'Fishing vessel'},'cargo ship':{lab:'Cargo ship'}},ATTR_LAB:{moving:'Moving',dark:'Dark',approaching:'Approaching'},SOURCES:[{id:'EO-P7'}],TNAME:['','Urgent','High','Medium','Low'],state:{page:'rp'},gsPolicy:{isPolicyHotkeyTarget:()=>false},tcol:()=>'',esc:String,ruleLabel:r=>r.classes.join(', '),imageFor:()=>'/image',findItem:id=>items.find(i=>i.id===id),toast(){},CSS:{escape:String},setTimeout:cb=>{timers.set(++timerID,cb);return timerID},clearTimeout:id=>timers.delete(id),document:{activeElement:{closest:()=>null},querySelector:()=>$('wbConfirm').open?{}:null,addEventListener(){},createElement:node}});
 ctx.request=async(url,method,body)=>{
  calls.push({url,method,body});
  if(url==='/api/directives/compile'||(url==='/api/directives/preview'&&Object.hasOwn(body,'text')))return interpret(body,url);
  if(url==='/api/directives')return{};
  return{items:items.map(i=>({id:i.id,tier:body.rules.length?1:3,central:body.rules.length?.9:.4,contrib:[]})),counts:[0,0,1,0],candidates:[]};
 };
 ctx.isScene=i=>i?.live?.event==='scene';
 vm.runInContext(appSource.match(/^const esc=.*$/m)[0]+'\n'+appSource.match(/^const clsOf=.*$/m)[0]+'\n'+appSource.match(/function ruleLabel\(r\)\{[\s\S]*?\n\}/)[0],ctx);
 vm.runInContext(source+'\nworkbench.init();',ctx);
 return{$,calls,ctx,timers,async type(text){$('nlIn').value=text;$('nlIn').oninput();},async preview(){const [id,cb]=timers.entries().next().value;timers.delete(id);cb();await tick()}};
}
test('draft chips escape rule labels, attributes and rule types',async()=>{
 const payload='"><img src=x onerror=alert(1)><svg onload=alert(1)>';
 const h=setup(()=>({rules:[{...rule(payload),attrs:['moving'],type:payload}],readback:payload,warnings:[]}));
 h.ctx.ATTR_LAB.moving=payload;
 await h.type(payload);await h.preview();
 const html=h.$('wbChips').innerHTML;
 assert.match(html,/&quot;&gt;&lt;img src=x onerror=alert\(1\)&gt;/);
 assert.match(html,/class="wb-chip &quot;&gt;&lt;img/);
 assert.doesNotMatch(html,/<img|<svg/);
 assert.equal(h.$('wbInterpretationReadback').textContent,payload);
});
test('research interpretation is shown and changes require explicit confirmation',async()=>{
 const h=setup(()=>({rules:[rule()],path:'research/rules+granite-concepts',readback:'Prioritize fishing vessel',warnings:[],unknown:[]}));
 await h.type('look for fishing vessels');assert.equal(h.$('wbApply').disabled,true);
 await h.preview();assert.match(h.$('wbCompiler').textContent,/IBM Granite/);assert.equal(h.$('wbInterpretationReadback').textContent,'Prioritize fishing vessel');assert.equal(h.$('wbApply').disabled,false);
 assert.equal(h.calls.filter(c=>c.url==='/api/directives').length,0);
 await h.$('wbApply').onclick();assert.equal(h.$('wbConfirm').open,true);assert.match(h.$('wbPath').textContent,/IBM Granite/);
 assert.equal(h.calls.filter(c=>c.url==='/api/directives').length,0);
 await h.$('wbConfirmApply').onclick();assert.equal(h.calls.filter(c=>c.url==='/api/directives').length,1);
 assert.equal(h.calls.find(c=>c.url==='/api/directives').body.rules[0].weight,2);
});
test('offline unsupported readback and echoed terms are inert text and cannot apply empty rules',async()=>{
 const payload='<img src=x onerror=alert(1)>';
 const readback=`Not supported: "${payload}" — this sensor can detect: small boat, person.`;
 const h=setup(()=>({rules:[],unknown:[payload],path:'rules',readback,warnings:[]}));
 await h.type('report '+payload);await h.preview();
 assert.equal(h.$('wbInterpretationReadback').textContent,readback);
 assert.equal(h.$('wbInterpretationReadback').innerHTML,'');
 assert.match(h.$('wbUnknown').innerHTML,/&lt;img src=x onerror=alert\(1\)&gt;/);
 assert.doesNotMatch(h.$('wbUnknown').innerHTML,/<img/);
 assert.equal(h.$('wbApply').disabled,true);
 assert.equal(h.calls.some(c=>c.url==='/api/directives'),false);
});
test('Granite preview and Apply cannot offer scenes as exception targets',async()=>{
 const h=setup(()=>({rules:[rule()],path:'research/granite',readback:'Prioritize fishing vessel',warnings:[],unknown:[]}));
 vm.runInContext("items.push({...items[0],id:'scene',live:{event:'scene'},dets:[]})",h.ctx);
 await h.type('look for fishing vessels');await h.preview();
 assert.doesNotMatch(h.$('wbGrid').innerHTML,/data-id="scene"/);
 await h.$('wbApply').onclick();await h.$('wbConfirmApply').onclick();
 assert.equal(h.calls.filter(c=>c.url==='/api/directives').length,1);
 assert.equal(h.calls.some(c=>c.method==='PATCH'),false);
});
test('unsupported qualifier blocks both Apply paths and shows clarification',async()=>{
 const h=setup(()=>({rules:[],unknown:['big ship'],path:'research/granite',readback:'Clarify the requested category',blocked:true,warnings:['Blocked: big ship is not a supported demo category.']}));
 await h.type('look for big ships');await h.preview();assert.equal(h.$('wbApply').disabled,true);assert.match(h.$('wbInterpretationWarnings').children[0].textContent,/big ship/);
 await h.$('wbApply').onclick();await h.$('wbConfirmApply').onclick();assert.equal(h.calls.filter(c=>c.url==='/api/directives'||c.url==='/api/directives/compile').length,0);
});
test('model failure invalidates previous success and does not change active state',async()=>{
 let fail=false;const h=setup(()=>{if(fail)throw Error('model timed out');return{rules:[rule()],path:'research/granite',readback:'Fishing vessels',warnings:[]}});
 await h.type('look for fishing vessels');await h.preview();assert.equal(h.$('wbApply').disabled,false);
 fail=true;await h.type('look for big ships');await h.preview();assert.equal(h.$('wbApply').disabled,true);assert.match(h.$('wbInterpretationWarnings').children[0].textContent,/timed out/);
 await h.$('wbApply').onclick();assert.equal(h.calls.filter(c=>c.url==='/api/directives').length,0);
});
test('stale language response cannot replace a newer blocked request',async()=>{
 let finish;const old=new Promise(resolve=>finish=resolve);const h=setup(body=>body.text==='old'?old:{rules:[],blocked:true,path:'research/granite',readback:'Clarify big ship',warnings:['Unsupported category']});
 await h.type('old');await h.preview();await h.type('new');await h.preview();finish({rules:[rule()],path:'research/granite',readback:'Old interpretation',warnings:[]});await tick();
 assert.equal(h.$('wbInterpretationReadback').textContent,'Clarify big ship');assert.equal(h.$('wbApply').disabled,true);
});
test('no-match warning describes future behavior without inventing a current change',async()=>{
 const h=setup(()=>({rules:[rule('cargo ship')],path:'research/granite',readback:'Cargo ships',warnings:[]}));
 await h.type('look for cargo ships');await h.preview();assert.match(h.$('wbInterpretationWarnings').children[0].textContent,/No stored images match/);assert.equal(h.$('wbApply').disabled,false);
});
test('a text change after confirmation prevents applying that stale interpretation',async()=>{
 const h=setup(()=>({rules:[rule()],path:'research/granite',readback:'Fishing vessels',warnings:[]}));
 await h.type('look for fishing vessels');await h.preview();await h.$('wbApply').onclick();
 await h.type('look for big ships');await h.$('wbConfirmApply').onclick();assert.equal(h.calls.filter(c=>c.url==='/api/directives').length,0);
});

test('Apply pauses scheduled live previews and prevents duplicate compiles until confirmation',async()=>{
 const compiled={rules:[rule()],path:'research/granite',readback:'Fishing vessels',warnings:[]};
 let finish;const pending=new Promise(resolve=>finish=resolve);
 const h=setup((_body,url)=>url==='/api/directives/compile'?pending:compiled);
 await h.type('look for fishing vessels');await h.preview();
 vm.runInContext('workbench.live()',h.ctx);const scheduled=[...h.timers.values()][0];assert.ok(scheduled);
 const apply=h.$('wbApply').onclick();await tick();assert.equal(h.$('wbApply').disabled,true);assert.equal(h.timers.size,0);
 const count=h.calls.length;scheduled();vm.runInContext('workbench.live()',h.ctx);await h.$('wbApply').onclick();await tick();
 assert.equal(h.calls.length,count);assert.equal(h.timers.size,0);
 finish(compiled);await apply;assert.equal(h.$('wbConfirm').open,true);assert.equal(h.calls.filter(c=>c.url==='/api/directives/compile').length,1);
});
test('editing during compile releases the guard and ignores the obsolete compile',async()=>{
 const compiled={rules:[rule()],path:'research/granite',readback:'Fishing vessels',warnings:[]};
 let finish;const pending=new Promise(resolve=>finish=resolve);let count=0;
 const h=setup((_body,url)=>url==='/api/directives/compile'&&count++===0?pending:compiled);
 await h.type('look for fishing vessels');await h.preview();const old=h.$('wbApply').onclick();await tick();
 await h.type('prioritize fishing vessels');await h.preview();assert.equal(h.$('wbApply').disabled,false);
 await h.$('wbApply').onclick();assert.equal(h.$('wbConfirm').open,true);
 finish({...compiled,readback:'Obsolete interpretation'});await old;assert.equal(h.$('wbReadback').textContent,'Fishing vessels');
});
test('compile error releases guard so a corrected request can open confirmation',async()=>{
 const compiled={rules:[rule()],path:'research/granite',readback:'Fishing vessels',warnings:[]};let fail=true;
 const h=setup((_body,url)=>{if(url==='/api/directives/compile'&&fail)throw Error('timeout');return compiled});
 await h.type('look for fishing vessels');await h.preview();await h.$('wbApply').onclick();assert.equal(h.$('wbConfirm').open,false);
 fail=false;await h.type('prioritize fishing vessels');await h.preview();await h.$('wbApply').onclick();assert.equal(h.$('wbConfirm').open,true);
});
