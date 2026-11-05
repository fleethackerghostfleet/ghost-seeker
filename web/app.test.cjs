const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');

const source=fs.readFileSync(path.join(__dirname,'static/app.js'),'utf8');
const css=fs.readFileSync(path.join(__dirname,'static/style.css'),'utf8');
const html=fs.readFileSync(path.join(__dirname,'static/index.html'),'utf8');
const workbenchSource=fs.readFileSync(path.join(__dirname,'static/workbench.js'),'utf8');
const policySource=fs.readFileSync(path.join(__dirname,'static/policy.js'),'utf8');
test('live ocean scenes are P4 feed imagery, not contact review or actions',async()=>{
 const c=setup();
 vm.runInContext(`
  scene=normalizeItem({...raw('ocean',4),background:true,central:0,base:0,edge:0,live:{event:'scene',forms:{}},dets:[],det:[]});
  ship=normalizeItem({...raw('ship',4),live:{event:'open'},dets:[],det:[]});
  items=[scene,ship];state.src='ALL';state.tier='all';state.sort='pri';state.sel='ocean';
  calls=[];request=async(...args)=>{calls.push(args)};
  zulu=()=> '12:00:00Z';largeImage=()=> '<img src="/ocean.jpg">';
 `,c);
 assert.equal(vm.runInContext('visible().l.length',c),2);
 assert.equal(vm.runInContext('rvQueue().length',c),1);
 assert.equal(vm.runInContext('rvQueue()[0].id',c),'ship');
 assert.equal(vm.runInContext('topLine(scene)',c),'Empty water · no contacts');
 assert.equal(vm.runInContext("topLine({...scene,src:{id:'Submarine_surfacing'}})",c),'Empty water · no contacts');
 assert.equal(vm.runInContext('boxesHTML(scene,true)',c),'');
 assert.equal(vm.runInContext('humanSent({...scene,sent:true,fullRes:true})',c),false);
 for(const filter of ['reviewed','unreviewed','sent'])assert.equal(vm.runInContext(`reviewMatch(scene,'${filter}')`,c),false);
 await vm.runInContext("setManual('ocean',1);requestFullRes(scene)",c);
 assert.equal(vm.runInContext('calls.length',c),0);
 vm.runInContext('renderDetailContent()',c);
 const html=c.$('drawer').innerHTML;
 assert.match(html,/Empty water · no contacts/);
 assert.doesNotMatch(html,/id="aCdr"|id="aFull"|data-t=|Detections<|Ship score/);
 vm.runInContext('items=[]',c);
 assert.equal(vm.runInContext('rvQueue().length',c),0);
});
test('live scene FULLs stay on ship until pulled, separately from the upload queue',()=>{
 const c=setup();
 vm.runInContext(`
  ocean={id:'ocean',live:{itemId:'ocean',event:'scene',imageForm:'thumb',forms:{thumb:{state:'received',bytes:10},full:{state:'pending',bytes:1000,received:0}}}};
  contact={id:'contact',upload:{bytes:100,sent:0}};
  items=[ocean,contact];fullRenderStats();
 `,c);
 assert.equal(c.$('uploadSummary').textContent,'Shore uploads: 0 uploading · 1 queued · 0 held · 0 uploaded · 1 kept on ship');
 assert.match(vm.runInContext('uploadHTML(ocean)',c),/Empty water · kept on ship/);
 assert.equal(vm.runInContext('uploadState(ocean).queued',c),false);
 for(const forms of ['{}',"{full:{state:'offered',bytes:1000}}"]){
  assert.equal(vm.runInContext(`uploadState({...ocean,live:{...ocean.live,forms:${forms}}}).background`,c),true);
 }
 vm.runInContext("pulling.add('ocean');fullRenderStats()",c);
 assert.equal(c.$('uploadSummary').textContent,'Shore uploads: 0 uploading · 2 queued · 0 held · 0 uploaded');
 assert.match(vm.runInContext('uploadHTML(ocean)',c),/Queued for shore · Full image/);
 vm.runInContext("pulling.delete('ocean');ocean.live.forms.full={state:'partial',bytes:1000,received:250};fullRenderStats()",c);
 assert.equal(c.$('uploadSummary').textContent,'Shore uploads: 1 uploading · 1 queued · 0 held · 0 uploaded');
 assert.match(vm.runInContext('uploadHTML(ocean)',c),/Uploading to shore · 25% · Full image/);
 vm.runInContext("ocean.live.forms.full={state:'received',bytes:1000,received:1000};ocean.live.imageForm='full';fullRenderStats()",c);
 assert.equal(c.$('uploadSummary').textContent,'Shore uploads: 0 uploading · 1 queued · 0 held · 1 uploaded');
 assert.match(vm.runInContext('uploadHTML(ocean)',c),/✓ Uploaded · Full image/);
});
test('scene repeats never enter scoped repeat counts or disappear with duplicates hidden',()=>{
 const c=feedContext();
 vm.runInContext(`items=[frame('repeat',{tier:4,filtered:'duplicate'}),frame('ocean',{tier:4,filtered:'duplicate',live:{event:'scene'}})];filters.showDuplicates=false`,c);
 assert.equal(vm.runInContext('repeatCount()',c),1);
 assert.equal(vm.runInContext('ids()',c),'ocean');
 vm.runInContext("state.tier='1'",c);
 assert.equal(vm.runInContext('repeatCount()',c),0);
 vm.runInContext("state.tier='4';filters.review='unreviewed'",c);
 assert.equal(vm.runInContext('repeatCount()',c),1);
 vm.runInContext("state.src='IR-U1'",c);
 assert.equal(vm.runInContext('repeatCount()',c),0);
});
test('scene sources interleave inside P4 without moving contact tiers',()=>{
 const c=setup();
 vm.runInContext(`
  items=[normalizeItem(raw('critical',1)),...Array.from({length:12},(_,i)=>normalizeItem({...raw('ship-'+i,4),central:.1})),
   ...Array.from({length:4},(_,i)=>normalizeItem({...raw('ocean-'+i,4),src:'Open_ocean_watch',central:0,live:{event:'scene'}}))];
  state.src='ALL';state.tier='all';state.sort='pri';
 `,c);
 const ids=JSON.parse(vm.runInContext('JSON.stringify(visible().l.map(i=>i.id))',c));
 assert.equal(ids[0],'critical');
 assert.ok(ids.findIndex(id=>id.startsWith('ocean-'))<=8);
 assert.equal(ids.length,17);
});
test('review suggestions alone use the overlay and retain busy buttons',()=>{
 const context=setup(),box=context.$('rvAsk'),classes=new Set();
 box.classList.toggle=(name,on)=>on?classes.add(name):classes.delete(name);
 vm.runInContext("rv.ask={kind:'sug',s:{cls:'vessel',dir:'raise',n:4,of:5}};renderAsk()",context);
 assert.equal(box.hidden,false);
 assert.ok(classes.has('rvask-suggestion'));
 assert.match(box.innerHTML,/Raise vessel priority\?/);
 for(const answer of ['yes','no','later'])assert.ok(box.innerHTML.includes(`data-answer="${answer}"`));
 vm.runInContext('rv.askBusy=true;renderAsk()',context);
 assert.equal((box.innerHTML.match(/disabled/g)||[]).length,3);
 vm.runInContext("rv.ask={kind:'why',id:'missing',classes:['vessel']};renderAsk()",context);
 assert.ok(!classes.has('rvask-suggestion'));
 assert.match(box.innerHTML,/Why did you set/);
 vm.runInContext('rv.ask=null;renderAsk()',context);
 assert.equal(box.hidden,true);
 assert.equal(box.innerHTML,'');
 assert.ok(!classes.has('rvask-suggestion'));
});
test('team proposals render escaped discussion, live own votes, busy buttons and isolated keys',()=>{
 const context=setup();
 vm.runInContext(`teamSync.available=true;teamSync.on=true;reviewerName='Bob';
  proposal={id:'p',cls:'bird',dir:'raise',n:4,of:5,proposedBy:'<Alice>',status:'pending',votes:{Bob:'agree'}};
  receiveTeamProposal(proposal);`,context);
 let html=context.$('rvAsk').innerHTML;
 assert.match(html,/TEAM DISCUSSION — proposed by &lt;Alice&gt;/);
 assert.match(html,/own-vote[^>]*>Bob: agree \(you\)/);
 assert.equal((html.match(/data-ask="team"/g)||[]).length,5);
 vm.runInContext('rv.askBusy=true;renderAsk()',context);
 assert.equal((context.$('rvAsk').innerHTML.match(/disabled/g)||[]).length,5);
 vm.runInContext("rv.askBusy=false;answers=[];answerTeamProposal=a=>answers.push(a);for(const key of ['y','n','a','r','l','escape','1','x'])reviewAskKey(key)",context);
 assert.deepEqual(JSON.parse(vm.runInContext('JSON.stringify(answers)',context)),['agree','disagree','accepted','rejected','later','later']);
 vm.runInContext("rv.askBusy=true;reviewAskKey('a')",context);
 assert.equal(vm.runInContext('answers.length',context),6);
});
test('team resolution closes matching asks and learning, while sync off ignores events',()=>{
 const context=setup();
 vm.runInContext(`teamSync.available=true;teamSync.on=true;messages=[];toast=m=>messages.push(m);resolved=[];
  gsLearn={suggestion:()=>({cls:'bird',dir:'raise'}),resolve:(s,a)=>resolved.push(a)};
  proposal={id:'p',cls:'bird',dir:'raise',n:4,of:5,proposedBy:'Alice',status:'pending',votes:{}};
  receiveTeamProposal(proposal);receiveTeamProposal({...proposal,status:'accepted',resolvedBy:'Alice'});`,context);
 assert.equal(context.$('rvAsk').hidden,true);
 assert.deepEqual(JSON.parse(vm.runInContext('JSON.stringify(resolved)',context)),['yes']);
 assert.match(vm.runInContext('messages[0]',context),/Alice accepted Raise bird/);
 vm.runInContext('receiveTeamProposal(proposal)',context);
 assert.equal(context.$('rvAsk').hidden,true);
 vm.runInContext("teamSync.on=false;receiveTeamProposal({...proposal,id:'other'})",context);
 assert.equal(context.$('rvAsk').hidden,true);
});
test('team sharing posts suggestions, supports local Later, and keeps failed actions open',async()=>{
 const context=setup();
 vm.runInContext(`teamSync.on=true;teamSync.available=true;reviewerName='Alice';calls=[];toast=()=>{};
  gsLearn={suggestion:()=>({cls:'bird',dir:'raise',n:4,of:5}),resolve:()=>{}};
  request=async(path,method,body)=>{calls.push({path,body});return {proposal:{...body,id:'p',status:'pending',proposedBy:'Alice',votes:{}}}};
  checkLearningSuggestion()`,context);
 await new Promise(resolve=>setImmediate(resolve));
 assert.equal(vm.runInContext('calls[0].path',context),'/api/review-sync/proposals');
 assert.equal(vm.runInContext('calls[0].body.reviewer',context),'Alice');
 await vm.runInContext("answerTeamProposal('later')",context);
 assert.equal(context.$('rvAsk').hidden,true);
 vm.runInContext("teamSync.hidden.clear();renderAsk();request=async()=>{throw Error('offline')}",context);
 await vm.runInContext("answerTeamProposal('accepted')",context);
 assert.equal(vm.runInContext('rv.ask.team',context),'p');
 assert.equal(vm.runInContext('rv.askBusy',context),false);
 vm.runInContext("rv.ask=null;teamSync.proposals=[];reviewerName='';checkLearningSuggestion()",context);
 assert.equal(vm.runInContext('!!rv.ask&&!rv.ask.team',context),true);
});
test('team acceptance resolves before adding a directive and votes do not add rules',async()=>{
 const context=setup();
 vm.runInContext(`teamSync.on=true;teamSync.available=true;reviewerName='Alice';calls=[];toast=()=>{};
  proposal={id:'p',cls:'bird',dir:'raise',n:4,of:5,proposedBy:'Alice',status:'pending',votes:{}};
  receiveTeamProposal(proposal);
  request=async(path,method,body)=>{calls.push({path,body});return {proposal:{...proposal,status:body.outcome||'pending',resolvedBy:'Alice'}}};`,context);
 await vm.runInContext("answerTeamProposal('agree')",context);
 assert.equal(vm.runInContext('calls.length',context),1);
 assert.equal(vm.runInContext('calls[0].path',context),'/api/review-sync/proposals/p/vote');
 await vm.runInContext("answerTeamProposal('accepted')",context);
 assert.equal(vm.runInContext('calls[1].path',context),'/api/review-sync/proposals/p/resolve');
 assert.equal(vm.runInContext('calls[2].path',context),'/api/directives');
 assert.equal(context.$('rvAsk').hidden,true);
});
test('two team acceptors add only one directive when the second resolve returns 404',async()=>{
 const contexts=[setup(),setup()];let winner=false,directives=0,resolves=0;
 for(const context of contexts){
  vm.runInContext(`teamSync.on=true;reviewerName='Alice';toast=()=>{};proposal={id:'p',cls:'bird',dir:'raise',n:4,of:5,proposedBy:'Alice',status:'pending',votes:{}};receiveTeamProposal(proposal)`,context);
  context.request=async(path)=>{
   if(path==='/api/review-sync')return {proposals:[]};
   if(path==='/api/directives'){directives++;return {}};
   resolves++;if(winner)throw Object.assign(new Error('pending proposal not found'),{status:404});winner=true;
   return {proposal:{id:'p',cls:'bird',dir:'raise',status:'accepted',resolvedBy:'Alice'}};
  };
 }
 await Promise.all(contexts.map(context=>vm.runInContext("answerTeamProposal('accepted')",context)));
 assert.equal(resolves,2);assert.equal(directives,1);
 for(const context of contexts)assert.equal(context.$('rvAsk').hidden,true);
});
test('failed rule after winning resolve defers learning and shows only the actionable error',async()=>{
 const context=setup();
 vm.runInContext(`teamSync.on=true;reviewerName='Alice';messages=[];toast=m=>messages.push(m);learned=[];
  gsLearn={suggestion:()=>({cls:'bird',dir:'raise'}),resolve:(s,a)=>learned.push(a)};
  proposal={id:'p',cls:'bird',dir:'raise',n:4,of:5,proposedBy:'Alice',status:'pending',votes:{}};
  receiveTeamProposal(proposal);teamSync.local.set('bird:raise',proposal);
  request=async(path)=>{if(path==='/api/directives')throw Error('offline');const resolved={...proposal,status:'accepted',resolvedBy:'Alice'};receiveTeamProposal(resolved);return {proposal:resolved}};`,context);
 await vm.runInContext("answerTeamProposal('accepted')",context);
 vm.runInContext("receiveTeamProposal({...proposal,status:'accepted',resolvedBy:'Alice'})",context);
 assert.deepEqual(JSON.parse(vm.runInContext('JSON.stringify(learned)',context)),['later']);
 assert.equal(vm.runInContext('messages.length',context),1);
 assert.match(vm.runInContext('messages[0]',context),/Accepted, but adding the rule failed: offline — add it on Collection Priorities/);
 assert.equal(context.$('rvAsk').hidden,true);
});
test('enabling team sync promotes an open local suggestion only with a reviewer name',async()=>{
 for(const name of ['Alice','']){
  const context=setup();
  vm.runInContext(`reviewerName=${JSON.stringify(name)};messages=[];toast=m=>messages.push(m);calls=[];
   setupTeamSync(true);rv.ask={kind:'sug',s:{cls:'bird',dir:'raise',n:4,of:5}};
   request=async(path,method,body)=>{calls.push(path);if(path==='/api/review-sync')return {proposals:[]};return {proposal:{...body,id:'p',status:'pending',proposedBy:reviewerName,votes:{}}}};
   $('reviewSync').checked=true;$('reviewSync').onchange();`,context);
  await new Promise(resolve=>setImmediate(resolve));
  if(name){
   assert.equal(vm.runInContext('rv.ask.team',context),'p');
   assert.equal(vm.runInContext("calls.filter(p=>p==='/api/review-sync/proposals').length",context),1);
   assert.match(context.$('rvAsk').innerHTML,/TEAM DISCUSSION/);
  }else{
   assert.equal(vm.runInContext('!!rv.ask&&!rv.ask.team',context),true);
   assert.equal(vm.runInContext("calls.includes('/api/review-sync/proposals')",context),false);
   assert.match(vm.runInContext('messages[0]',context),/Set your reviewer name/);
  }
 }
});
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
test('feed filters default to showing duplicates, persist, validate, and return defensive copies',()=>{
 for(const value of [null,'broken','{}','{"feed":null}','{"feed":{"showDuplicates":"no","review":"invalid"}}']){
  assert.equal(JSON.stringify(settingsContext(value).settings.get().feed),'{"showDuplicates":true,"review":"all"}');
 }
 const {settings,saved}=settingsContext('{"bar":"right","feed":{"showDuplicates":false,"review":"reviewed"}}');
 settings.get().feed.review='unreviewed';assert.equal(settings.get().feed.review,'reviewed');
 settings.setFeed({review:'sent'});
 assert.deepEqual(saved().feed,{showDuplicates:false,review:'sent'});
 assert.equal(saved().bar,'right');
 assert.equal(settingsContext(JSON.stringify(saved())).settings.get().feed.review,'all','a reload must not keep the main feed on sent-only');
 assert.equal(settingsContext(JSON.stringify(saved())).settings.get().feed.showDuplicates,false);
 settings.setFeed({review:'unreviewed'});
 assert.equal(settingsContext(JSON.stringify(saved())).settings.get().feed.review,'unreviewed');
 settings.setFeed({showDuplicates:true,review:'bad'});
 assert.deepEqual(saved().feed,{showDuplicates:true,review:'all'});
});
function feedContext(){
 const context=setup();
 vm.runInContext(`
  filters={showDuplicates:true,review:'all'};gsSettings={get:()=>({feed:filters})};
  function frame(id,extra={}){return {id,src:{id:'EO-P7'},tier:2,central:.7,ts:100,img:'/'+id+'.jpg',det:[],...extra}}
  function ids(){return visible().l.map(i=>i.id).join(',')}
 `,context);
 return context;
}
test('feed shows every pipeline image by default with low-value, repeat and directive-lowered images at P4',()=>{
 const context=feedContext();
 vm.runInContext(`
  items=[frame('original',{tier:1,central:.9}),frame('repeat',{tier:4,filtered:'duplicate',dupOf:'original'}),
   frame('low',{tier:4,filtered:'low-value',central:.1}),frame('directive',{tier:4,det:[{sup:true}]}),
   frame('mixed',{tier:3,det:[{sup:true},{sup:false}]}),frame('dismissed',{dismissed:true})];
  before=JSON.stringify(items);
 `,context);
 assert.equal(vm.runInContext('ids()',context),'original,repeat,mixed,directive,low');
 assert.equal(vm.runInContext('visible().counts[4]',context),3);
 assert.equal(vm.runInContext("feedReasons.get('repeat')",context),'Repeat of an earlier image');
 assert.equal(vm.runInContext("feedReasons.get('low')",context),'Low value');
 assert.equal(vm.runInContext("feedReasons.get('directive')",context),'Lowered by directive');
 vm.runInContext('filters.showDuplicates=false',context);
 assert.equal(vm.runInContext('ids()',context),'original,mixed,directive,low');
 vm.runInContext("filters.showDuplicates=true;state.tier='4'",context);
 assert.equal(vm.runInContext('ids()',context),'repeat,directive,low');
 assert.equal(vm.runInContext('JSON.stringify(items)===before',context),true);
});
test('main feed shows queued, uploading, held, unsent and uploaded images; a stale sent-only setting is cleared on reload',()=>{
 const context=feedContext();
 vm.runInContext(`items=[frame('queued',{upload:{bytes:100,sent:0}}),frame('uploading',{upload:{bytes:100,sent:40}}),
  frame('held',{upload:{bytes:100,sent:0,override:'hold'}}),frame('next',{upload:{bytes:100,sent:10,override:'next'}}),
  frame('done',{upload:{bytes:100,sent:100,done:true}}),frame('cdr',{sent:true,upload:{bytes:100,sent:100,done:true}})]`,context);
 const all='queued,uploading,held,next,done,cdr';
 assert.deepEqual(vm.runInContext('ids()',context).split(',').sort(),all.split(',').sort());
 vm.runInContext("items[1].upload={bytes:100,sent:100,done:true};items[0].upload={bytes:100,sent:60}",context);
 assert.deepEqual(vm.runInContext('ids()',context).split(',').sort(),all.split(',').sort(),'upload progress/completion must not remove or duplicate images');
 vm.runInContext("filters.review='sent'",context);
 assert.equal(vm.runInContext('ids()',context),'cdr');
 const {settings}=settingsContext(JSON.stringify({feed:{showDuplicates:true,review:'sent'}}));
 vm.runInContext(`filters=${JSON.stringify(settings.get().feed)}`,context);
 assert.deepEqual(vm.runInContext('ids()',context).split(',').sort(),all.split(',').sort());
 assert.match(html,/id="sentShowAll"/);
 assert.match(source,/\$\('sentShowAll'\)\.onclick=\(\)=>\{gsSettings\.setFeed\(\{review:'all'\}\)/);
});
test('feed interleaves sensors within a priority tier without changing priorities',()=>{
 const context=feedContext();
 vm.runInContext(`items=[frame('a1',{central:.9}),frame('a2',{central:.8}),frame('a3',{central:.7}),frame('b1',{src:{id:'IR-U1'},central:.6}),
  frame('b2',{src:{id:'IR-U1'},central:.5}),frame('p1',{tier:1,central:.95}),frame('c1',{tier:3,src:{id:'IR-U1'},central:.4}),frame('c2',{tier:3,central:.3})];before=JSON.stringify(items)`,context);
 assert.equal(vm.runInContext('ids()',context),'p1,b1,a1,b2,a2,a3,c1,c2');
 assert.equal(vm.runInContext("visible().l.map(i=>feedTier(i)).join('')",context),'12222233');
 assert.equal(vm.runInContext('JSON.stringify(items)===before',context),true);
 vm.runInContext("state.src='EO-P7'",context);
 assert.equal(vm.runInContext('ids()',context),'p1,a1,a2,a3,c2');
 vm.runInContext("state.src='ALL';state.sort='time';items=[frame('n1',{ts:119000}),frame('n2',{ts:118000}),frame('m1',{src:{id:'IR-U1'},ts:117000}),frame('o1',{ts:59000}),frame('o2',{ts:58000}),frame('o3',{src:{id:'IR-U1'},ts:1000})]",context);
 assert.equal(vm.runInContext('ids()',context),'n1,m1,n2,o3,o1,o2');
 vm.runInContext("items.find(i=>i.id==='o3').ts=-1000",context);
 assert.equal(vm.runInContext('ids()',context),'n1,m1,n2,o1,o2,o3');
});
test('shown repeats sit right after their original across sensors and sorts; hiding removes them',()=>{
 const context=feedContext();
 vm.runInContext(`items=[frame('a',{tier:1,central:.9}),frame('b',{tier:1,src:{id:'IR-U1'},central:.8}),frame('c',{tier:2}),
  frame('ra',{tier:4,filtered:'duplicate',dupOf:'a'}),frame('rb',{tier:4,src:{id:'IR-U1'},filtered:'duplicate',dupOf:'b'}),
  frame('orphan',{tier:4,filtered:'duplicate',dupOf:'gone'}),frame('low',{tier:4,filtered:'low-value',central:.1})];before=JSON.stringify(items)`,context);
 assert.equal(vm.runInContext('ids()',context),'a,ra,b,rb,c,orphan,low');
 assert.equal(vm.runInContext('repeatCount()',context),3);
 vm.runInContext("state.sort='time';items.forEach((i,n)=>i.ts=10000-n*100);before=JSON.stringify(items)",context);
 assert.match(vm.runInContext('ids()',context),/a,ra/);
 assert.match(vm.runInContext('ids()',context),/b,rb/);
 vm.runInContext("state.sort='pri';filters.showDuplicates=false",context);
 assert.equal(vm.runInContext('ids()',context),'a,b,c,low');
 vm.runInContext("state.tier='1'",context);
 assert.equal(vm.runInContext('repeatCount()',context),0);
 vm.runInContext("state.tier='4'",context);
 assert.equal(vm.runInContext('repeatCount()',context),3);
 vm.runInContext("state.tier='all';filters.review='reviewed'",context);
 assert.equal(vm.runInContext('repeatCount()',context),0);
 vm.runInContext("filters.review='all'",context);
 assert.equal(vm.runInContext('JSON.stringify(items)===before',context),true);
});
test('a later boost keeps the stored priority despite an earlier suppression flag',()=>{
 const context=feedContext();
 vm.runInContext("items=[frame('boosted',{tier:2,central:.7998,det:[{sup:true,matched:true}]}),frame('low',{tier:4})]",context);
 assert.equal(vm.runInContext('ids()',context),'boosted,low');
 assert.equal(vm.runInContext('visible().counts[2]',context),1);
 assert.equal(vm.runInContext('feedTier(items[0])',context),2);
 assert.equal(vm.runInContext('filteredReason(items[0])',context),'');
 vm.runInContext("state.tier='4'",context);
 assert.equal(vm.runInContext('ids()',context),'low');
});
test('operator-chosen repeats stay visible at their own priority even with duplicates hidden',()=>{
 const context=feedContext();
 vm.runInContext(`items=[frame('kept',{tier:2,manual:2,filtered:'duplicate'}),frame('sent',{tier:1,sent:true,filtered:'duplicate'}),frame('repeat',{tier:4,filtered:'duplicate'})];filters.showDuplicates=false`,context);
 assert.equal(vm.runInContext('ids()',context),'sent,kept');
 assert.equal(vm.runInContext("feedTier(items[0])",context),2);
});
test('human review filters separate people from automatic escalation and list newest human sends first',()=>{
 const context=feedContext();
 vm.runInContext(`items=[frame('accepted',{reviewed:true}),frame('manual',{manual:2}),frame('dismissed',{dismissed:true}),frame('pending'),
  frame('auto',{tier:1,manual:1,reviewed:true,sent:true,autoSent:true,sentAt:'2026-11-05T10:00:00Z'}),
  frame('sent-old',{reviewed:true,sent:true,sentAt:'2026-11-05T10:01:00Z'}),frame('sent-new',{tier:4,reviewed:true,sent:true,sentAt:'2026-11-05T10:05:00Z'}),
  frame('full',{fullRes:true,fullResAt:'2026-11-05T10:03:00Z'}),frame('other',{src:{id:'IR-U1'},reviewed:true,sent:true,sentAt:'2026-11-05T10:09:00Z'})]`,context);
 vm.runInContext("filters.review='sent'",context);
 assert.equal(vm.runInContext('ids()',context),'other,sent-new,full,sent-old');
 vm.runInContext("state.src='EO-P7'",context);
 assert.equal(vm.runInContext('ids()',context),'sent-new,full,sent-old');
 vm.runInContext("filters.review='reviewed'",context);
 assert.equal(vm.runInContext('ids()',context),'accepted,manual,sent-old,full,dismissed,sent-new');
 vm.runInContext("filters.review='unreviewed'",context);
 assert.equal(vm.runInContext('ids()',context),'auto,pending');
 assert.equal(vm.runInContext("humanSent(items[4])",context),false);
 assert.equal(vm.runInContext("humanSent(items[5])",context),true);
 vm.runInContext("items[4].humanReviewed=true;items[4].manual=2;filters.review='reviewed'",context);
 assert.ok(vm.runInContext('ids().split(",").includes("auto")',context));
 assert.equal(vm.runInContext('humanSent(items[4])',context),false);
});
test('What I sent navigates from review to the filtered imagery feed',()=>{
 const context=feedContext();
 vm.runInContext("location={hash:'#review'};state.page='rv';state.tier='1';gsSettings.setFeed=f=>Object.assign(filters,f);syncFeedFilters=()=>{}",context);
 const start=source.indexOf(" $('rvSent').onclick="),end=source.indexOf('\n',start);
 vm.runInContext(source.slice(start,end),context);
 vm.runInContext("$('rvSent').onclick()",context);
 assert.equal(vm.runInContext('location.hash',context),'#feed');
 assert.equal(vm.runInContext('filters.review',context),'sent');
 assert.equal(vm.runInContext('state.tier',context),'all');
});
test('cards mark human sends purple and automatic sends green',()=>{
 assert.match(source,/<span class="cdrtag">✓ Auto-sent to Commander<\/span><span class="hsenttag">➤ Sent by operator<\/span>/);
 assert.match(source,/\$\{it\.sent&&it\.autoSent\?' sent':''\}\$\{humanSent\(it\)\?' hsent':''\}/);
 assert.match(css,/\.card\.hsent \.hsenttag\{display:block\}/);
 assert.match(css,/--human:#a58bff/);
 assert.match(html,/id="rvSent">➤ What I sent/);
});
test('main strip moves low-level counts to the Details screen',()=>{
 assert.match(css,/#simStrip :is\(#st2,#st4\),#simStrip :is\(#st2,#st4\)\+\.ar,#liveStrip \.lowlevel,#liveStrip \.lowlevel\+\.ar\{display:none\}/);
 assert.match(html,/href="#details">Details: counts &amp; filtered photos ›<\/a>/);
 assert.match(html,/<label class="feed-check"><input type="checkbox" id="showDuplicates" checked> Show duplicates <span class="c" id="dupCount" title="Repeat images in the feed">0<\/span><\/label>/);
 assert.match(html,/<div class="seg" id="densSeg"><button data-d="m" class="on">Small<\/button><button data-d="l">Large<\/button><\/div>/);
});
test('Details screen lists automatically filtered photos by kind with their originals',()=>{
 const context=feedContext();
 vm.runInContext(`body={innerHTML:''};els={detailsBody:body,liveStrip:{innerHTML:''}};$=id=>els[id];imageFor=it=>'/img/'+it.id;zulu=()=>'12:00:00';
  stats={cap:100,keep:20,drop:80,bytes:2048,db:5};
  items=[frame('original',{tier:2}),frame('repeat',{tier:4,filtered:'duplicate',dupOf:'original'}),frame('low',{tier:4,filtered:'low-value'}),
   frame('directive',{tier:4,det:[{sup:true}]}),frame('chosen',{filtered:'duplicate',manual:2}),frame('gone',{dismissed:true,filtered:'low-value'})];
  renderDetails();`,context);
 const page=()=>vm.runInContext('body.innerHTML',context);
 assert.match(page(),/Repeats of earlier images \(1\)/);
 assert.match(page(),/Low value \(1\)/);
 assert.match(page(),/Lowered by directive \(1\)/);
 assert.match(page(),/Low-value and repeat frames kept on the ship \(P4 Low\)<\/td><td class="n">80/);
 assert.match(page(),/Useful images kept<\/td><td class="n">20/);
 assert.doesNotMatch(page(),/Useful images sent to shore/);
 assert.match(page(),/src="\/img\/original"[^]*Original · P2 High[^]*src="\/img\/repeat"/);
 assert.doesNotMatch(page(),/data-id="chosen"/);
 vm.runInContext("state.detKind='low-value';renderDetails()",context);
 assert.match(page(),/data-id="low"/);
 assert.doesNotMatch(page(),/data-id="gone"/);
 vm.runInContext('stats.bytes=1048576;renderDetails()',context);
 assert.match(page(),/Image data sent to shore<\/td><td class="n">1\.0 MB/);
 vm.runInContext('stats.bytes=0;renderDetails()',context);
 assert.match(page(),/Image data sent to shore<\/td><td class="n">0\.0 MB/);
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
test('feed density offers only Small and Large, persists them and maps the old XL to Large',()=>{
 for(const saved of [null,'broken','{}','{"density":"invalid"}','{"density":null}','{"density":"s"}']){
  assert.equal(settingsContext(saved).settings.get().density,'m');
 }
 assert.equal(settingsContext('{"density":"xl"}').settings.get().density,'l');
 const {settings,saved}=settingsContext('{}');
 for(const density of ['m','l']){
  settings.density(density);
  assert.equal(saved().density,density);
  assert.equal(settingsContext(JSON.stringify(saved())).settings.get().density,density);
 }
 settings.density('xl');settings.density('s');settings.density(null);
 assert.equal(settings.get().density,'l');
});
test('feed filters toggle synchronizes expansion and accessibility state',()=>{
 const context=setup();
 vm.runInContext(`
  let expanded=false;
  $('feedHead').classList.toggle=name=>{if(name!=='filters-open')throw Error(name);return expanded=!expanded};
  $('feedFilters').setAttribute=(name,value)=>{if(name!=='aria-expanded')throw Error(name);filterExpanded=value};
  toggleFeedFilters();
 `,context);
 assert.equal(vm.runInContext('filterExpanded',context),'true');
 vm.runInContext('toggleFeedFilters()',context);
 assert.equal(vm.runInContext('filterExpanded',context),'false');
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
test('live review uses durable submit, note, undo and summary endpoints',async()=>{
 const context=setup();
 vm.runInContext(`LIVE={};reviewerName='Alice';calls=[];applyItemUpdate=()=>{};findItem=id=>({id});request=async(...args)=>{calls.push(args);if(args[0].endsWith('/summary'))return {count:0,agreement:0,reviewers:{}};return {record:{id:'live-r'},item:{id:'live-i'},cdr:0}};p={id:'live-i'};`,context);
 await vm.runInContext("submitReview('live-i','accept',{},p)",context);
 await vm.runInContext("saveReviewReason('live-r','wrong_detection','bad box')",context);
 await vm.runInContext("undoReview(p)",context);
 await vm.runInContext("refreshReviewData()",context);
 const calls=JSON.parse(vm.runInContext('JSON.stringify(calls)',context));
 assert.ok(calls.some(c=>c[0]==='/api/items/live-i/review'&&c[1]==='POST'));
 assert.ok(calls.some(c=>c[0]==='/api/reviews/live-r/reason'&&c[2].note==='bad box'));
 assert.ok(calls.some(c=>c[0]==='/api/reviews/live-r/undo'));
 assert.ok(calls.some(c=>c[0]==='/api/reviews/summary'));
 assert.ok(calls.every(c=>c[1]!=='PATCH'));
 vm.runInContext("request=async()=>{throw Error('review logging disabled')}",context);
 await assert.rejects(vm.runInContext("submitReview('live-i','accept',{},p)",context),/review logging disabled/);
 await vm.runInContext("refreshReviewData()",context);
 assert.equal(vm.runInContext("$('reviewData').textContent",context),'review logging disabled');
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
test('shore upload rendering covers queued, partial, completed and invalid payloads',()=>{
 const context=setup();
 assert.match(vm.runInContext('uploadHTML({})',context),/Queued for shore/);
 assert.match(vm.runInContext('uploadHTML({upload:{bytes:40960,sent:12288,bps:256000}})',context),/Uploading to shore: 12.0 KB of 40.0 KB at 0.03 MB\/s/);
 assert.match(vm.runInContext('uploadHTML({upload:{bytes:40,sent:40,done:true}})',context),/✓ Uploaded/);
 assert.equal(vm.runInContext('uploadState({upload:{bytes:-1,sent:-2,done:true}}).done',context),false);
 assert.equal(vm.runInContext('uploadState({upload:{bytes:10,sent:20}}).sent',context),10);
});
test('partial simulation uploads show a link outage in the card and drawer state',()=>{
 const context=setup();
 vm.runInContext('outageItem={upload:{bytes:100,sent:25,bps:0}}',context);
 assert.match(vm.runInContext('uploadState(outageItem).label',context),/^Link outage, paused · 25 B of 100 B$/);
 assert.match(vm.runInContext('uploadHTML(outageItem)',context),/class="upload-label">Link outage, paused<\/span>/);
 assert.match(vm.runInContext('uploadControlsHTML(outageItem)',context),/Link outage, paused/);
 vm.runInContext("outageItem.upload.override='next'",context);
 assert.match(vm.runInContext('uploadHTML(outageItem)',context),/Upload next \(operator\) · Link outage, paused/);
 for(const change of ["override:'hold'",'sent:0','sent:100,done:true','bps:8000','bps:undefined']){
  assert.doesNotMatch(vm.runInContext(`uploadHTML({upload:{bytes:100,sent:25,bps:0,${change}}})`,context),/Link outage/);
 }
});
test('live uploads derive progress from shown form, never from commander escalation',()=>{
 const context=setup();
 assert.equal(vm.runInContext("uploadState({sent:true,live:{imageForm:'crop',forms:{crop:{state:'partial',bytes:100,received:25}}}}).sent",context),25);
 assert.equal(vm.runInContext("uploadState({live:{imageForm:'thumb',forms:{thumb:{state:'received',bytes:100,received:0}}}}).done",context),true);
 assert.equal(vm.runInContext("uploadState({live:{forms:{thumb:{state:'pending',bytes:100}}}}).queued",context),true);
});
test('upload override labels, toggle undo and live/viewer guards',()=>{
 const context=setup();
 vm.runInContext("overrideItem={upload:{bytes:100,sent:20,override:'hold'}}",context);
 assert.match(vm.runInContext('uploadHTML(overrideItem)',context),/upload-held.*Held by operator/);
 assert.match(vm.runInContext('uploadControlsHTML(overrideItem)',context),/Release upload/);
 assert.equal(vm.runInContext('uploadState(overrideItem).queued',context),false);
 vm.runInContext("overrideItem.upload.override='next'",context);
 assert.match(vm.runInContext('uploadHTML(overrideItem)',context),/upload-next.*Upload next \(operator\)/);
 assert.match(vm.runInContext('uploadControlsHTML(overrideItem)',context),/Back to priority order/);
 vm.runInContext("overrideItem.upload.override=''",context);
 assert.match(vm.runInContext('uploadHTML(overrideItem)',context),/Uploading to shore/);
 vm.runInContext("overrideItem.upload.done=true;overrideItem.upload.override='hold'",context);
 assert.match(vm.runInContext('uploadHTML(overrideItem)',context),/✓ Uploaded/);
 assert.match(vm.runInContext('uploadControlsHTML(overrideItem)',context),/already uploaded.*disabled/);
 vm.runInContext("overrideItem.live={forms:{thumb:{state:'pending',bytes:100}}}",context);
 assert.match(vm.runInContext('uploadControlsHTML(overrideItem)',context),/not supported by the live shore connection.*disabled/);
 vm.runInContext("delete overrideItem.live;overrideItem.upload.done=false;gsAuth={viewer:()=>true}",context);
 assert.match(vm.runInContext('uploadControlsHTML(overrideItem)',context),/Viewer role is read-only.*disabled/);
});
test('summary corrections do not reset a received image upload',()=>{
 const context=setup();
 for(const state of ['pending','partial']){
  vm.runInContext(`corrected={live:{imageForm:'thumb',forms:{thumb:{state:'received',bytes:100},fact:{state:'${state}',bytes:200,received:10}}}}`,context);
  assert.equal(vm.runInContext('uploadState(corrected).done',context),true);
  assert.match(vm.runInContext('uploadHTML(corrected)',context),/✓ Uploaded/);
 }
});
test('live active and pending forms take precedence over a received preview',()=>{
 const context=setup();
 vm.runInContext("liveItem={live:{imageForm:'thumb',forms:{thumb:{state:'received',bytes:10},crop:{state:'partial',bytes:100,received:25},full:{state:'pending',bytes:1000}}}}",context);
 assert.equal(vm.runInContext('uploadState(liveItem).sent',context),25);
 assert.equal(vm.runInContext('uploadState(liveItem).done',context),false);
 assert.match(vm.runInContext('uploadHTML(liveItem)',context),/Uploading to shore · 25% · Object close-up/);
 vm.runInContext("liveItem.live.forms.crop.state='received'",context);
 assert.equal(vm.runInContext('uploadState(liveItem).queued',context),true);
 assert.match(vm.runInContext('uploadHTML(liveItem)',context),/Queued for shore · Full image/);
 vm.runInContext("liveItem.live.forms.full.state='received'",context);
 assert.equal(vm.runInContext('uploadState(liveItem).done',context),true);
 assert.match(vm.runInContext('uploadHTML(liveItem)',context),/✓ Uploaded/);
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
  fullRenderStats=renderStats;renderAll=()=>{};updateCard=()=>{};renderFeed=()=>{};renderStats=()=>{};renderRp=()=>{};renderDetail=()=>{};
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
test('bandwidth reads in MB/s and the simulation shows one connection value',()=>{
 const src=fs.readFileSync(path.join(__dirname,'static/app.js'),'utf8');
 const c=vm.createContext({});
 vm.runInContext(src.slice(src.indexOf('const fmtRate='),src.indexOf('const ago=')),c);
 assert.equal(vm.runInContext('fmtRate(4000000)',c),'0.50 MB/s');
 assert.equal(vm.runInContext('fmtRate(16000000)',c),'2.00 MB/s');
 assert.equal(vm.runInContext('fmtRate(0)',c),'0.00 MB/s');
 assert.equal(vm.runInContext('fmtRate(256000)',c),'0.03 MB/s');
 assert.equal(vm.runInContext('fmtRate(73600)',c),'<0.01 MB/s');
 assert.equal(vm.runInContext('fmtRate(8000)',c),'<0.01 MB/s');
 assert.equal(vm.runInContext('fmtRate(1)',c),'<0.01 MB/s');
 assert.equal(vm.runInContext('fmtRate(80000)',c),'0.01 MB/s');
 assert.equal(vm.runInContext('fmtRate(-1)',c),'0.00 MB/s');
 const el={innerHTML:'',classList:{on:new Set(),toggle(k,v){v?this.on.add(k):this.on.delete(k)}}};
 const ctx=vm.createContext({$:id=>id==='stLink'?el:null,esc:s=>String(s),stats:{linkBps:4000000}});
 vm.runInContext(src.slice(src.indexOf('const fmtRate='),src.indexOf('const ago=')),ctx);
 vm.runInContext(src.slice(src.indexOf('function linkPanelHTML('),src.indexOf('const pulling=new Set();')),ctx);
 vm.runInContext('renderSimLink()',ctx);
 assert.match(el.innerHTML,/Ship-to-shore connection<\/span> <b>0.50 MB\/s<\/b>$/);
 assert.doesNotMatch(el.innerHTML,/every|hint/i);
 assert.doesNotMatch(el.innerHTML,/simulated/i);
 assert.doesNotMatch(el.innerHTML,/estimat/i);
 assert.equal(el.classList.on.has('down'),false);
 vm.runInContext('stats.linkBps=0;renderSimLink()',ctx);
 assert.match(el.innerHTML,/<b>0.00 MB\/s<\/b>.*Link down — sending paused/);
 assert.equal(el.classList.on.has('down'),true);
 vm.runInContext('stats.linkBps=160000;stats.linkFixed=true;renderSimLink()',ctx);
 assert.match(el.innerHTML,/Fixed test rate/);assert.doesNotMatch(el.innerHTML,/every 5 s/);
 const html=fs.readFileSync(path.join(__dirname,'static/index.html'),'utf8');
 assert.match(html,/id="simStrip">\s*<div class="st link" id="stLink"/);
 assert.doesNotMatch(html,/type="range"|linkPresets/);
});
test('cards being sent show a blue arrow and sent cards a green check in the top-right corner',()=>{
 const context=setup();
 const mark={},classes=new Set();
 vm.runInContext(source.slice(source.indexOf('function sendMark('),source.indexOf('function uploadControlsHTML(')),context);
 context.card={querySelector:s=>s==='.sendmark'?mark:null,classList:{toggle(k,v){v?classes.add(k):classes.delete(k)}}};
 const apply=u=>{context.it={upload:u};vm.runInContext('applySendMark(card,it)',context)};
 apply({bytes:100,sent:40,bps:8000,active:true});
 assert.equal(mark.className,'sendmark sending');assert.equal(mark.textContent,'↑');assert.ok(classes.has('sending'));
 apply({bytes:100,sent:40,bps:8000,active:false});
 assert.equal(mark.className,'sendmark');assert.equal(classes.size,0);
 apply({bytes:100,sent:40,bps:0,active:true});
 assert.equal(mark.className,'sendmark','outage pauses the sending mark');
 apply({bytes:100,sent:100,done:true});
 assert.equal(mark.className,'sendmark done');assert.equal(mark.textContent,'✓');assert.ok(classes.has('uploaded'));
 context.it={live:{imageForm:'thumb',forms:{thumb:{state:'partial',bytes:100,received:10}}}};
 vm.runInContext("LIVE={link:{controllable:true,bps:4000000},edge:{}}",context);
 vm.runInContext('applySendMark(card,it)',context);
 assert.equal(mark.className,'sendmark sending');
 vm.runInContext('LIVE.link.bps=0;applySendMark(card,it)',context);
 assert.equal(mark.className,'sendmark','live outage: partial form is not on the wire');
 vm.runInContext("LIVE={link:{},edge:{ok:false,estimateBps:0},shore:{estimateBps:64000}};applySendMark(card,it)",context);
 assert.equal(mark.className,'sendmark sending','edge offline: shore estimate keeps the link up, matching the connection panel');
 vm.runInContext("LIVE.shore.linkUp=false;applySendMark(card,it)",context);
 assert.equal(mark.className,'sendmark','edge offline and shore reports link down: stale estimate does not mark sending');
 const css=fs.readFileSync(path.join(__dirname,'static/style.css'),'utf8');
 assert.match(css,/\.sendmark\{[^}]*position:absolute;top:5px;right:5px/);
 assert.match(css,/\.sendmark\.done\{[^}]*background:var\(--good\)/);
});
test('review buttons name their effect and key on screen',()=>{
 const html=fs.readFileSync(path.join(__dirname,'static/index.html'),'utf8');
 for(const [key,label] of [['1','Set P1 Critical &amp; next'],['2','Set P2 High &amp; next'],['3','Set P3 Routine &amp; next'],['4','Set P4 Low &amp; next'],['Space','Agree &amp; next'],['X','Not relevant: hide &amp; next'],['C','Send to Commander now &amp; next'],['Z','Undo last']]){
  assert.ok(new RegExp(`<span class="kb-act"[^>]*>${label}</span><kbd>${key}</kbd>`).test(html),label);
 }
 assert.match(html,/Images left in feed to review:<\/span> <b id="rvLeft">/);
 assert.match(html,/id="rvNext" role="status"/);
});
test('after a re-rank the next-step line confirms the save and names the keys',()=>{
 const context=setup();
 context.$('rvNext').classList={remove(){},add(){}};
 vm.runInContext("reviewerName='Ann';rv.cur={id:'a',centralTier:3};items=[rv.cur];rv.last='';renderNext(rv.cur)",context);
 let html=context.$('rvNext').innerHTML;
 assert.match(html,/<b>Next:<\/b> Press <kbd>Space<\/kbd> to agree with P3 Routine, or <kbd>1<\/kbd>–<kbd>4<\/kbd>/);
 assert.doesNotMatch(html,/rvnext-done/);
 vm.runInContext("setLast('✓ Saved: last image changed P1 Critical → P2 High. Press Z to undo.');renderNext(rv.cur)",context);
 html=context.$('rvNext').innerHTML;
 assert.match(html,/rvnext-done">✓ Saved: last image changed P1 Critical → P2 High\. Press Z to undo\./);
 assert.match(html,/This is the next image\. Press <kbd>Space<\/kbd>/);
 vm.runInContext("rv.ask={kind:'why',classes:['vessel']};renderNext(rv.cur)",context);
 assert.match(context.$('rvNext').innerHTML,/Answer the question above to continue/);
 vm.runInContext("rv.ask=null;reviewerName='';renderNext(rv.cur)",context);
 assert.match(context.$('rvNext').innerHTML,/Enter your reviewer name/);
});
test('suggestion overlay names the exact tier change',()=>{
 const context=setup(),box=context.$('rvAsk');
 vm.runInContext("CLS.submarine={lab:'Submarine'};rv.ask={kind:'sug',s:{cls:'submarine',dir:'lower',n:3,of:3,from:1,to:2,highRisk:true}};renderAsk()",context);
 assert.match(box.innerHTML,/Submarine: <span class="tpill t1">P1 Critical<\/span> → <span class="tpill t2">P2 High<\/span>/);
 assert.match(box.innerHTML,/from P1 Critical to P2 High\. Yes adds a rule that lowers Submarine priority/);
 assert.match(box.innerHTML,/<kbd>Y<\/kbd>Yes, apply change/);
 assert.doesNotMatch(box.innerHTML,/Lower Submarine priority\?/);
});
test('Send now moves a feed image to the top of the send queue',async()=>{
 const context=setup();
 vm.runInContext(`calls=[];messages=[];toast=m=>messages.push(m);
  items=[{id:'img',src:{id:'EO-P7'},upload:{bytes:100,sent:0}}];
  request=async(url,method,body)=>{calls.push([url,method,body]);return{cdr:2,item:{...items[0],upload:{...items[0].upload,override:'next'}}}};
  applyItemUpdate=item=>{items[0]=item};`,context);
 await vm.runInContext("sendNow('img')",context);
 assert.equal(vm.runInContext('JSON.stringify(calls)',context),JSON.stringify([['/api/items/img/upload-override','POST',{override:'next'}]]));
 assert.match(vm.runInContext('messages[0]',context),/top of the send queue/);
 assert.equal(vm.runInContext('sendNowBlocked(items[0])',context),'Already at the top of the send queue');
 await vm.runInContext("sendNow('img')",context);
 assert.equal(vm.runInContext('calls.length',context),1);
 vm.runInContext("items[0]={id:'img',src:{id:'EO-P7'},upload:{bytes:100,sent:100,done:true}}",context);
 assert.equal(vm.runInContext('sendNowBlocked(items[0])',context),'Already uploaded to shore');
});

test('empty water has an obvious kept-on-ship label and explicit upload override',()=>{
 const context=setup();
 context.ocean={id:'ocean',src:'BY-12',ts:new Date(),background:true,dets:null,det:null,upload:{bytes:1000,sent:0,done:false,bps:64000}};
 vm.runInContext('normalizeItem(ocean)',context);
 assert.equal(vm.runInContext('topLine(ocean)',context),'No contacts · empty water');
 assert.match(vm.runInContext('uploadHTML(ocean)',context),/Empty water · kept on ship/);
 assert.equal(vm.runInContext('uploadState(ocean).queued',context),false);
 vm.runInContext("ocean.upload.override='next'",context);
 assert.match(vm.runInContext('uploadHTML(ocean)',context),/Upload next \(operator\)/);
 assert.doesNotMatch(vm.runInContext('uploadHTML(ocean)',context),/kept on ship/);
 assert.equal(vm.runInContext('uploadState(ocean).queued',context),true);
});

test('upload summary counts retained empty water separately from queued and active transfers',()=>{
 const context=setup();
 vm.runInContext(`
  const upload={bytes:1000,sent:0,done:false};
  items=[{id:'ocean',background:true,upload:{...upload}},{id:'contact',upload:{...upload}},{id:'active',upload:{...upload,sent:20}}];
  fullRenderStats();
 `,context);
 assert.equal(context.$('uploadSummary').textContent,'Shore uploads: 1 uploading · 1 queued · 0 held · 0 uploaded · 1 kept on ship');
 vm.runInContext("items[0].upload.override='hold';fullRenderStats()",context);
 assert.equal(context.$('uploadSummary').textContent,'Shore uploads: 1 uploading · 1 queued · 1 held · 0 uploaded');
 vm.runInContext("items[0].upload.override='next';fullRenderStats()",context);
 assert.equal(context.$('uploadSummary').textContent,'Shore uploads: 1 uploading · 2 queued · 0 held · 0 uploaded');
});

test('empty water processing chain explains local retention without claiming shore delivery',()=>{
 const context=setup();
 context.$('drawer').querySelectorAll=()=>[];
 context.briefing={renderDetections(){}};
 context.zulu=()=> '12:00Z';
 vm.runInContext(`
  MODELS={maritime:{name:'Maritime'}};largeImage=()=>'';visible=()=>({l:items});
  items=[normalizeItem({id:'ocean',src:'BY-12',ts:new Date(),tier:4,central:0,base:0,edge:.02,lat:{edge:15,cen:80},background:true,dets:null,det:null,upload:{bytes:1000,sent:0,done:false}})];
  state.sel='ocean';renderDetailContent();
 `,context);
 const html=context.$('drawer').innerHTML;
 assert.match(html,/empty water retained locally for demo · no automatic upload/);
 assert.match(html,/Shore transfer: Empty water · kept on ship/);
 assert.match(html,/Priority scoring \(simulated\)/);
 assert.doesNotMatch(html,/0\.02 ≥ 0\.20|Sent to shore:/);
});
