const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');
const source=fs.readFileSync(path.join(__dirname,'static/app.js'),'utf8');
function setup(appSource=source){
 const elements=new Map(),frames=new Map(),timers=new Map();let next=0;
 const grid={children:[],insertBefore(el,before){el.remove();const i=before?this.children.indexOf(before):this.children.length;this.children.splice(i,0,el)}};
 elements.set('grid',grid);
 const context=vm.createContext({
  imageCache:new Map(),
  requestAnimationFrame:fn=>{const id=++next;frames.set(id,fn);return id},cancelAnimationFrame:id=>frames.delete(id),
  setTimeout:(fn,ms)=>{const id=++next;timers.set(id,{fn,ms});return id},clearTimeout:id=>timers.delete(id),
  document:{hidden:false,body:{classList:{add(){},remove(){}}},querySelector:()=>null},
  $:id=>{if(!elements.has(id))elements.set(id,{style:{},classList:{toggle(){}},querySelector:()=>({}),setAttribute(){}});return elements.get(id)},
  card:id=>({dataset:{id},scrollIntoView(){},remove(){const i=grid.children.indexOf(this);if(i>=0)grid.children.splice(i,1)},getBoundingClientRect(){throw Error('large feeds must not read card geometry')}})
 });
 context.window=context;
 vm.runInContext(appSource.replace(/init\(\);\s*$/,''),context);
 vm.runInContext(`
  SRC={sensor:{id:'sensor'}};filters={showFiltered:true,review:'all'};gsSettings={get:()=>({feed:filters})};
  renderDirBar=()=>{};renderTierSeg=()=>{};renderStats=()=>{};renderRp=()=>{};renderDetail=()=>{};renderReview=()=>{};
  updated=[];created=[];updateCard=it=>updated.push(it.id);makeCard=it=>{created.push(it.id);it.el=card(it.id);return it.el};
  function raw(id){return {id,src:'sensor',ts:new Date(Date.parse('2026-11-05T12:00:00Z')-(Number(id)||0)*1000).toISOString(),tier:2,central:.5,img:id+'.jpg',det:[]}}
  function populate(n){items=Array.from({length:n},(_,i)=>normalizeItem(raw(String(i))))}
  streamHandlers={};EventSource=class{addEventListener(type,handler){streamHandlers[type]=handler}};
 `,context);
 return {context,grid,frames,timers,frame(){const [id,fn]=frames.entries().next().value;frames.delete(id);fn()}};
}
test('SSE item/update bursts merge by ID and render once per frame with latest values',()=>{
 const h=setup(),c=h.context;
 vm.runInContext(`
  counts={feed:0,stats:0,rp:0};renderFeed=()=>counts.feed++;renderStats=()=>counts.stats++;renderRp=()=>counts.rp++;
  openStream();
  streamHandlers.item({data:JSON.stringify({item:raw('one'),stats:{cap:1}})});
  for(let sent=1;sent<=50;sent++)streamHandlers.update({data:JSON.stringify({item:{...raw('one'),upload:{sent}},cdr:sent})});
 `,c);
 assert.equal(h.frames.size,1);
 assert.equal(vm.runInContext("items.length===1&&findItem('one').upload.sent===50&&cdr===50&&counts.feed===0",c),true);
 h.frame();
 assert.equal(vm.runInContext('counts.feed===1&&counts.stats===1&&counts.rp===1',c),true);
 assert.equal([...h.timers.values()].filter(t=>t.ms===100).length,0);
});
test('merge removes omitted server fields while retaining identity and client card state',()=>{
 const {context:c}=setup();
 vm.runInContext(`
  mergeItem({...raw('one'),reviewed:true,dismissed:true,escalated:true,sent:true,note:'old',upload:{override:'hold'}},true);
  original=findItem('one');original.el=card('one');originalElement=original.el;rv.cur=original;
  mergeItem({...raw('one'),central:.9},false);
 `,c);
 assert.equal(vm.runInContext("['reviewed','dismissed','escalated','sent','note','upload'].every(key=>!Object.hasOwn(original,key))",c),true);
 assert.equal(vm.runInContext("items.length===1&&items[0]===original&&findItem('one')===original&&itemByID.get('one')===original&&rv.cur===original",c),true);
 assert.equal(vm.runInContext('original.el===originalElement&&original.fresh===false&&original.central===.9',c),true);
 assert.equal(vm.runInContext("Object.keys(original).sort().join(',')===[...Object.keys(normalizeItem(raw('one'))),'el','fresh'].sort().join(',')",c),true);
});
test('hidden-tab timeout flushes once and cancels the pending animation frame',()=>{
 const h=setup(),c=h.context;
 vm.runInContext('document.hidden=true;calls=0;renderFeed=()=>calls++;scheduleFeedFlush();scheduleFeedFlush()',c);
 [...h.timers.values()].find(t=>t.ms===100).fn();
 assert.equal(h.frames.size,0);
 assert.equal(vm.runInContext('calls',c),1);
 vm.runInContext('flushFeed()',c);
 assert.equal(vm.runInContext('calls',c),1);
});
test('indexed merge and reconnect normalization never use linear ID searches',()=>{
 const h=setup(),c=h.context;
 vm.runInContext(`populate(3000);findItem('0');items.find=items.findIndex=()=>{throw Error('linear lookup')};
  mergeItem({...raw('1500'),central:.9},false);
  renderAll=()=>{};renderPolicySummary=()=>{};closeZoom=()=>{};
  applySnapshot({items:[raw('1500'),raw('new')],state:{model:'maritime',directives:[]},stats:{},cdr:0});`,c);
 assert.equal(vm.runInContext("items.length===2&&findItem('new').id==='new'&&!findItem('0')",c),true);
});
test('feed windows cards, updates dirty IDs only, and inserts only new window cards',()=>{
 const h=setup(),c=h.context;
 vm.runInContext('populate(3000);renderFeed(true)',c);
 assert.equal(h.grid.children.length,120);
 assert.equal(vm.runInContext("$('feedMore').textContent",c),'Showing 120 of 3,000 images · Load more');
 vm.runInContext("created=[];updated=[];dirtyItems.add('5');renderFeed()",c);
 assert.equal(vm.runInContext("updated.join(',')",c),'5');
 assert.equal(vm.runInContext('created.length',c),0);
 vm.runInContext('updated=[];growFeedWindow()',c);
 assert.equal(h.grid.children.length,240);
 assert.equal(vm.runInContext('updated.length===120&&created.length===120',c),true);
 vm.runInContext("updated=[];state.sort='new';items[200].ts=new Date('2027-01-01');dirtyItems.add('200');renderFeed()",c);
 assert.equal(h.grid.children[0].dataset.id,'200');
 assert.equal(vm.runInContext("updated.join(',')",c),'200');
});
test('filter resets, empty feeds and off-window dirty items keep a bounded grid',()=>{
 const h=setup(),c=h.context;
 vm.runInContext("populate(10000);renderFeed(true);dirtyItems.add('9999');updated=[];renderFeed()",c);
 assert.equal(h.grid.children.length,120);
 assert.equal(vm.runInContext('updated.length',c),0);
 vm.runInContext('growFeedWindow();resetFeedWindow();renderFeed(true)',c);
 assert.equal(h.grid.children.length,120);
 assert.equal(vm.runInContext('items.filter(it=>it.el).length',c),120);
 vm.runInContext("state.src='missing';renderFeed(true)",c);
 assert.equal(h.grid.children.length,0);
 assert.equal(vm.runInContext("$('feedMore').hidden&&$('empty').style.display==='block'",c),true);
});
test('selection uses cached order and updates only the old and new cards on the next frame',()=>{
 const h=setup(),c=h.context;
 vm.runInContext(`populate(3100);renderFeed(true);state.sel='0';updated=[];details=0;
  renderDetail=()=>details++;visible=()=>{throw Error('selection must use cached order')};
  renderStats=renderRp=()=>{throw Error('selection must not render stats or priorities')};step(1);`,c);
 assert.equal(vm.runInContext("state.sel==='1'&&details===1&&updated.length===0",c),true);
 h.frame();
 assert.equal(vm.runInContext("updated.join(',')",c),'0,1');
 vm.runInContext('updated=[];closeDetail()',c);
 assert.equal(vm.runInContext('updated.length',c),0);
 h.frame();
 assert.equal(vm.runInContext("state.sel===null&&updated.join(',')==='1'&&details===1",c),true);
});
test('reset snapshots remove windowed cards even when the replacement feed is empty',()=>{
 const h=setup(),c=h.context;
 vm.runInContext(`populate(3000);renderFeed(true);growFeedWindow();
  renderAll=()=>renderFeed(true);renderPolicySummary=()=>{};closeZoom=()=>{};closeCardMenu=()=>{};closeDetail=()=>{};
  applySnapshot({reset:true,epoch:1,items:[],state:{model:'maritime',directives:[]},stats:{},cdr:0});`,c);
 assert.equal(h.grid.children.length,0);
 assert.equal(vm.runInContext('feedLimit===FEED_PAGE_SIZE&&feedOrdered.length===0&&dirtyItems.size===0',c),true);
 vm.runInContext("mergeItem(raw('fresh'));renderFeed(true)",c);
 assert.deepEqual(h.grid.children.map(el=>el.dataset.id),['fresh']);
});
test('selection-only batching still flushes concurrent server updates through the full render path',()=>{
 const h=setup(),c=h.context;
 vm.runInContext("populate(3100);renderFeed(true);state.sel='0';step(1);applyItemUpdate({...raw('2'),central:.6})",c);
 h.frame();
 assert.equal(vm.runInContext("feedOrdered[0].id==='2'&&dirtyItems.size===0",c),true);
});
test('skipping animations does not refresh clean cards, but an explicit display refresh does',()=>{
 const h=setup(),c=h.context;
 vm.runInContext('populate(3100);renderFeed(true);updated=[];renderFeed(true)',c);
 assert.equal(vm.runInContext('updated.length',c),0);
 vm.runInContext('renderFeed(true,true)',c);
 assert.equal(vm.runInContext('updated.length',c),120);
});
test('sentinel grows one page and keyboard stepping reveals the next off-window item',()=>{
 const h=setup(),c=h.context;
 c.IntersectionObserver=class{constructor(callback,options){this.callback=callback;assert.equal(options.root,c.$('feed'));c.intersection=callback}observe(){}};
 vm.runInContext('populate(500);renderFeed(true);setupFeedWindow()',c);
 c.intersection([{isIntersecting:true}]);
 assert.equal(h.grid.children.length,240);
 vm.runInContext("state.sel='239';renderDetail=()=>{};cardScrolls=0;const oldMake=makeCard;makeCard=it=>{const el=oldMake(it);el.scrollIntoView=()=>cardScrolls++;return el};step(1)",c);
 assert.equal(h.grid.children.length,360);
 assert.equal(vm.runInContext("state.sel==='240'&&cardScrolls===1",c),true);
});
test('upload and status counts cover all retained items in one pass, not only the window',()=>{
 const h=setup(),c=h.context;
 vm.runInContext(source.slice(source.indexOf('function renderStats('),source.indexOf('/* ---- details screen:')),c);
 vm.runInContext(`populate(3000);stats={cap:3000,keep:3000,drop:0,bytes:0,db:3000};
  items[0].upload={bytes:100,sent:100,done:true};items[1].upload={bytes:100,sent:20,override:'hold'};
  items[2].upload={bytes:100,sent:50,bps:8000,active:true};items[3].dismissed=true;
  items.filter=items.map=()=>{throw Error('extra array pass')};renderStats();`,c);
 assert.equal(c.$('hsImgs').textContent,3000);
 assert.equal(c.$('uploadSummary').textContent,'Shore uploads: 1 uploading · 2996 queued · 1 held · 1 uploaded');
});
test('sidebar mutation observers coalesce geometry work to one frame per panel',()=>{
 const observers=[],frames=[];let queries=0;
 const context=vm.createContext({requestAnimationFrame:fn=>frames.push(fn),innerWidth:1280,
  window:{gsSettings:{get:()=>({bar:'top'})},addEventListener(){}},
  document:{body:{append(){}},createElement:()=>({setAttribute(){},addEventListener(){}}),querySelector:()=>{queries++;return null},addEventListener(){}},
  ResizeObserver:class{observe(){}disconnect(){}},MutationObserver:class{constructor(fn){observers.push(fn)}observe(){}}
 });
 vm.runInContext(fs.readFileSync(path.join(__dirname,'static/resize.js'),'utf8'),context);
 const before=queries;
 for(const callback of observers){callback();callback();callback()}
 assert.equal(queries,before);
 assert.equal(frames.length,observers.length);
 for(const callback of frames)callback();
 assert.equal(queries,before+observers.length);
});
test('bounded source mixing matches the original order on large mixed groups without mutating input',()=>{
 const {context:c}=setup();
 vm.runInContext(`
  function oldMix(l,group){
   const srcOf=i=>i.src?.id??i.src;
   if(new Set(l.map(srcOf)).size<2)return l;
   const rest=l.slice(),out=[],last=new Map();
   while(rest.length){
    const g=group(rest[0]);let pick=0,best=Infinity;
    for(let j=0;j<rest.length&&j<8&&group(rest[j])===g;j++){const seen=last.has(srcOf(rest[j]))?last.get(srcOf(rest[j])):-1;if(seen<best){best=seen;pick=j}}
    const it=rest.splice(pick,1)[0];last.set(srcOf(it),out.length);out.push(it);
   }
   return out;
  }
  mixed=Array.from({length:3200},(_,i)=>({id:i,src:i%2?{id:'s'+(i*17%13)}:'s'+(i*17%13),tier:Math.floor(i/800),minute:Math.floor(i/19)%7}));
 `,c);
 for(const group of ['i=>i.tier','i=>i.minute','()=>1']){
  assert.equal(vm.runInContext(`JSON.stringify(mixSources(mixed,${group}).map(i=>i.id))===JSON.stringify(oldMix(mixed,${group}).map(i=>i.id))`,c),true);
 }
 assert.equal(vm.runInContext('mixed.every((it,i)=>it.id===i)',c),true);
 assert.equal(vm.runInContext('mixSources([],()=>1).length',c),0);
 assert.equal(vm.runInContext("single=mixed.slice(0,1);mixSources(single,()=>1)===single",c),true);
 const started=performance.now();
 assert.equal(vm.runInContext('calls=0;mixSources(mixed,i=>{calls++;return i.tier}).length===3200&&calls===3200',c),true);
 assert.ok(performance.now()-started<1000,'mixing 3,200 items should take under one second');
});
test('repeat pairing indexes originals and preserves missing, repeated and chained duplicate behavior',()=>{
 const {context:c}=setup();
 vm.runInContext(`
  original={id:'a'},repeat={id:'b',filtered:'duplicate',dupOf:'a'},chain={id:'c',filtered:'duplicate',dupOf:'b'},missing={id:'d',filtered:'duplicate',dupOf:'absent'};
  list=[chain,repeat,missing,original];list.find=()=>{throw Error('linear original search')};
 `,c);
 assert.equal(vm.runInContext("pairRepeats(list).map(i=>i.id).join(',')",c),'c,d,a,b');
 assert.equal(vm.runInContext('pairRepeats([]).length',c),0);
});
test('status-only upload checks do not format per-item byte or rate labels',()=>{
 const {context:c}=setup(source.replace('const fmtBytes=','let fmtBytes=').replace('const fmtRate=','let fmtRate='));
 vm.runInContext(`
  upload={upload:{bytes:100,sent:20,bps:8000,active:true}};
  described=uploadState(upload);plain=uploadState(upload,false);
 `,c);
 assert.equal(vm.runInContext("Object.keys(described).every(k=>k==='label'||described[k]===plain[k])&&plain.label===''&&described.label.includes('20 B')",c),true);
 vm.runInContext(`fmtBytes=fmtRate=()=>{throw Error('status checks must not format labels')};
  populate(3200);for(const it of items)it.upload=upload.upload;uploadedCount();filters.hideUploaded=true;visible();sendMark(items[0]);`,c);
 vm.runInContext(source.slice(source.indexOf('function renderStats('),source.indexOf('/* ---- details screen:')),c);
 vm.runInContext('renderSimLink=()=>{};stats={cap:3200,keep:3200,drop:0,bytes:0,db:3200};renderStats()',c);
});
test('freshness expiry updates cards without refiltering the retained collection',()=>{
 const h=setup(),c=h.context;
 vm.runInContext("mergeItem(raw('new'));renderFeed(true);updated=[];visible=()=>{throw Error('freshness does not affect ordering')}",c);
 [...h.timers.values()].find(t=>t.ms===6000).fn();
 h.frame();
 assert.equal(vm.runInContext("updated.join(',')==='new'&&!findItem('new').fresh",c),true);
});
