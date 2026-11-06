let SOURCES=[],SRC={},CLS={},MODELS={},ATTR_LAB={},EXAMPLES=[];
let DATASET_MODE=false;
let state={src:'ALL',model:'maritime',sort:'pri',tier:'all',paused:false,sel:null,directives:[],policy:null};
let items=[],stats={cap:0,keep:0,drop:0,bytes:0,db:0},cdr=0,weightData={},pendingItems=[],evictedIDs=new Set(),heldSnapshotIDs=null,policyAuditTimer=null,policyRawTouched=false,policyRawLoaded=false;
let indexedItems=null,itemByID=new Map(),indexedPending=null,pendingByID=new Map();
const dirtyItems=new Set();
let feedFrame=null,feedTimer=null,feedScheduled=false,feedNeedsRender=false;
const FEED_PAGE_SIZE=120;
let feedLimit=FEED_PAGE_SIZE,feedOrdered=[],renderedFeedIDs=[],renderedFeedReasons=new Map();
let feedFocusID=null;
/* LIVE is the shore block of the snapshot when the console runs on a shore node (-shore); null on the simulation. */
let LIVE=null;
let snapshotEpoch=null,resetRevision=0;
let feedReasons=new Map();
const metadataCache=new Map();
let metadataExpanded=false;
let metadataEMCON='NORMAL';
const clsOf=k=>CLS[k]||{lab:String(k||'unknown'),g:'other'};
const fmtBytes=n=>n<1024?`${n|0} B`:n<1048576?`${(n/1024).toFixed(1)} KB`:`${(n/1048576).toFixed(2)} MB`;
const fmtRate=bps=>bps>0&&bps<8e4?'<0.01 MB/s':`${(Math.max(0,bps)/8e6).toFixed(2)} MB/s`;
const fmtMs=ms=>ms>=1000?(ms/1000).toFixed(1)+' s':ms+' ms';
const ago=ms=>{const s=Math.max(0,(Date.now()-ms)/1000);return s<60?`${s|0}s ago`:s<3600?`${(s/60)|0}m ago`:`${(s/3600)|0}h ago`};
const FORMS=['fact','thumb','crop','full'];
const FORM_NAME={fact:'Summary',thumb:'Preview',crop:'Object close-up',full:'Full image'};
const FORM_HELP={fact:'FACT: a small summary of the object and capture time (about 200 bytes). Sent before images.',thumb:'THUMB: a small preview of the whole image.',crop:'CROP: a close-up of the detected object at its original resolution.',full:'FULL: the complete, full-quality image. Camera overview images are sent only when requested.'};
function formChipsHTML(it){
 const f=it.live?.forms;if(!f)return'';
 return FORMS.map(k=>{const st=f[k]||{state:'none'};const title=st.state==='received'?`${FORM_NAME[k]}: received (${fmtBytes(st.bytes)})`:st.state==='partial'?`${FORM_NAME[k]}: ${fmtBytes(st.received)} of ${fmtBytes(st.bytes)} — streaming over the link`:st.state==='pending'?`${FORM_NAME[k]}: waiting on the ship (${fmtBytes(st.bytes)})`:st.state==='offered'?`${FORM_NAME[k]}: available on the ship — request it to send it`:`${FORM_NAME[k]}: not available`;
  return`<span class="fchip ${esc(st.state)}${it.live.imageForm===k?' shown':''}" title="${esc(FORM_HELP[k]+' · '+title)}">${FORM_NAME[k]}${st.state==='partial'?` ${Math.round(100*st.received/Math.max(1,st.bytes))}%`:''}</span>`}).join('');
}
function uploadState(it,describe=true){
 let u=it.upload,form='';
 const liveForms=it.live?.forms||{};
 if(it.live){
  const forms=liveForms;
  const imageForms=['thumb','crop','full'];
  const key=imageForms.find(k=>forms[k]?.state==='partial')||imageForms.find(k=>forms[k]?.state==='pending')||(imageForms.includes(it.live.imageForm)?it.live.imageForm:null)||['full','crop','thumb'].find(k=>forms[k]?.state==='received')||imageForms.find(k=>forms[k]?.state==='offered');
  form=FORM_NAME[key]||'';
  const f=forms[key]||{};
  u={bytes:f.bytes||0,sent:f.state==='received'?f.bytes:f.received||0,done:f.state==='received',active:f.state==='partial'&&(!LIVE||!!(LIVE?.link?.controllable?LIVE.link.bps>0:LIVE?.edge?.ok?LIVE.edge.estimateBps:LIVE?.shore?.linkUp!==false&&LIVE?.shore?.estimateBps)),bps:LIVE?.link?.bps||LIVE?.edge?.estimateBps||0};
 }
 const bytes=Math.max(0,Number(u?.bytes)||0),sent=Math.min(bytes,Math.max(0,Number(u?.sent)||0));
 const full=it.live?.forms?.full;
 const sceneHeld=isScene(it)&&!['partial','received'].includes(full?.state)&&!(full?.received>0)&&!pulling.has(it.live.itemId);
 const done=!sceneHeld&&!!u?.done&&bytes>0,held=!it.live&&!done&&u?.override==='hold',next=!it.live&&!done&&u?.override==='next';
 const metadataDone=it.live?formsStateDone(it.live,'fact'):!!u?.metadataDone;
 const background=sceneHeld||!it.live&&it.background&&!done&&!held&&!it.manual&&!it.sent&&!it.fullRes&&!next;
 const paused=!it.live&&!done&&!held&&sent>0&&u?.bps===0;
 const sending=!done&&!held&&!paused&&!background&&!!u?.active;
 const queued=!done&&!held&&!background&&!sending;
 const metadataOnly=metadataDone&&!held&&!background&&(it.live?!['thumb','crop','full'].some(k=>formsStateDone(it.live,k)):!done);
 const transfer=describe?`${paused?'Link outage, paused · ':''}${fmtBytes(sent)} of ${fmtBytes(bytes)}${u?.bps?` at ${fmtRate(u.bps)}`:''}`:'';
 const label=describe?(done?'✓ Uploaded':metadataOnly?'Metadata on shore · image queued':held?'Held by operator':next?`Upload next (operator) · ${transfer}`:background?'Empty water · kept on ship':paused?transfer:queued?`Queued for shore: ${transfer}`:`Uploading to shore: ${transfer}`)+(form&&!metadataOnly?` · ${form}`:''):'';
 return{bytes,sent,done,queued,label,form,held,next,paused,background,sending,metadataDone,metadataOnly};
}
function formsStateDone(live,name){return live?.forms?.[name]?.state==='received'}
function uploadHTML(it){
 const u=uploadState(it);
 return`<div class="upload-info${u.done?' uploaded':u.metadataOnly?' metadata-only':u.held?' upload-held':u.next?' upload-next':''}" title="${esc(u.label)}"><progress class="upload-bar" max="${Math.max(1,u.bytes)}" value="${u.sent}" aria-label="${esc(u.label)}"></progress><span class="upload-label">${esc(u.done||u.metadataOnly?u.label:u.held?'Held by operator':u.next?`Upload next (operator)${u.paused?' · Link outage, paused':''}`:u.background?'Empty water · kept on ship':u.paused?'Link outage, paused':(`${u.queued?'Queued for shore':'Uploading to shore'}${!u.queued||u.sent>0?` · ${Math.round(100*u.sent/Math.max(1,u.bytes))}%`:''}`)+(u.form?` · ${u.form}`:''))}</span></div>`;
}
function sendMark(it){
 const u=uploadState(it,false);
 return u.done?{cls:'sendmark done',text:'✓',title:'Sent to shore'}:u.sending?{cls:'sendmark sending',text:'↑',title:'Sending to shore now'}:{cls:'sendmark',text:'',title:''};
}
function applySendMark(el,it){
 const m=el.querySelector('.sendmark');if(!m)return;const k=sendMark(it);m.className=k.cls;m.textContent=k.text;m.title=k.title;
 el.classList?.toggle('sending',k.cls.endsWith('sending'));el.classList?.toggle('uploaded',k.cls.endsWith('done'));
}
function uploadControlsHTML(it){
 const u=uploadState(it),live=!!(LIVE||it.live),viewer=typeof gsAuth!=='undefined'&&gsAuth.viewer();
 const disabled=live||viewer||u.done,reason=live?'Transfer overrides are not supported by the live shore connection':viewer?'Viewer role is read-only':u.done?'Image is already uploaded':'Default: priority tier, then score. Operator overrides affect uploads only.';
 return`<div class="dsec upload-controls"><h3>Shore upload queue</h3>${uploadHTML(it)}<div class="actions"><button class="btn ${u.next?'done':''}" id="aUploadNext" aria-pressed="${u.next}" title="${esc(reason)}" ${disabled?'disabled':''}>${u.next?'Back to priority order':'Upload next'}</button><button class="btn ${u.held?'done':''}" id="aUploadHold" aria-pressed="${u.held}" title="${esc(reason)}" ${disabled?'disabled':''}>${u.held?'Release upload':'Hold upload'}</button></div></div>`;
}
function mergeLiveConfig(live){
 if(!live)return;
 for(const [k,v]of Object.entries(live.classes||{}))if(!CLS[k])CLS[k]=v;
 let added=false;for(const src of live.sources||[]){if(!SRC[src.id]){SRC[src.id]=src;SOURCES.push(src);added=true}}
 if(added){const sel=$('srcSel');if(sel&&sel.innerHTML!==undefined){const cur=state.src;sel.innerHTML=SOURCES.map(s=>`<option value="${esc(s.id)}">${esc(s.name)}</option>`).join('');sel.value=cur}}
}
function liveSuppressReason(it){const l=it.live;if(!l?.suppressed)return'';return`Deprioritized by the standing directive${l.suppressedBy?` (“ignore ${esc(l.suppressedBy)}”)`:''} — ranked P4 Low, still in the feed, nothing deleted`}

function ruleLabel(r){
 const cl=r.classes.length?(r.classes.length>3?`${r.classes.slice(0,2).map(k=>clsOf(k).lab).join(' / ')} +${r.classes.length-2}`:r.classes.map(k=>clsOf(k).lab).join(' / ')):'Any object';
 const at=r.attrs.length?' · '+r.attrs.map(a=>ATTR_LAB[a]).join(' or '):'';
 return esc((r.type==='boost'?'▲ ':'▼ ')+cl+at+(r.tier?` → P${r.tier} ${TNAME[r.tier]}`:''));
}

/* ---------------- UI ---------------- */
state.dens=window.gsSettings?.get().density||'m';state.page='feed';
const tcol=t=>['','var(--p1)','var(--p2)','var(--p3)','var(--p4)'][t];
const esc=t=>String(t).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;').replace(/'/g,'&#39;');
function shipLine(d){
 const ship=d.ship;if(!ship)return'';
 const label=ship.label||String(ship.type||'unknown').replace(/_/g,' ').replace(/\b\w/g,c=>c.toUpperCase());
 return`Type: ${esc(label)} (${Math.round(Number(ship.conf)*100)}%)${ship.aspect?` · Aspect: ${esc(ship.aspect)}`:''}${ship.size?` · Size: ${esc(ship.size)}`:''}`;
}
function shipVisibleLine(d){
 const ship=d.ship;if(!ship)return'';
 const features=(ship.features||[]).map(esc).join(', ');
 const markings=ship.markings?`markings ${esc(ship.markings)}`:'';
 const visible=[features,markings].filter(Boolean).join(' · ');
 return visible?`Visible: ${visible}`:'';
}
const selectedBands=new Map();
function bandsFor(it){return Array.isArray(it.bands)?[...new Set(it.bands.filter(b=>typeof b==='string'&&b.trim()))]:[]}
function bandFor(it){const bands=bandsFor(it);return bands.includes(selectedBands.get(it.id))?selectedBands.get(it.id):bands[0]}
function bandControls(it){
 const bands=bandsFor(it);if(bands.length<2)return '';
 const band=bandFor(it);
 return `<div class="band-controls" role="group" title="Spectral band: choose visible light or infrared (heat) imagery" aria-label="Spectral band">${bands.map(b=>`<button type="button" data-band="${esc(b)}" data-band-item="${esc(it.id)}" aria-pressed="${b===band}" title="${b==='IR'?'Simulated long-wave infrared (LWIR): warm objects appear white':b==='EO'?'Electro-optical (EO): visible-light camera':esc(b)}">${esc(b)}</button>`).join('')}<span>B: change image band</span></div><span class="band-label">${esc(band)}${band==='IR'?' · white-hot · simulated LWIR':''}</span>`;
}
function largeImage(it){return `<img class="${bandsFor(it).length>1&&bandFor(it)==='IR'?'spectrum-ir':''}" src="${esc(imageFor(it))}" alt="${esc(it.src.name)}${bandsFor(it).length>1?' · '+esc(bandFor(it)):''}">${boxesHTML(it,true)}${bandControls(it)}`}
function preserveBandFocus(render){
 const button=document.activeElement?.closest?.('.band-controls button');
 const metadata=document.activeElement?.matches?.('[data-meta-summary]')?'[data-meta-summary]':document.activeElement?.id==='metadataCopy'?'#metadataCopy':null;
 const surface=button?.closest('#rvMain, #drawer, #zoom');
 const item=button?.dataset.bandItem,band=button?.dataset.band;
 render();
 if(surface)[...surface.querySelectorAll('[data-band-item]')].find(b=>b.dataset.bandItem===item&&b.dataset.band===band)?.focus({preventScroll:true});
 if(metadata)document.querySelector(metadata)?.focus({preventScroll:true});
}
function renderPolicyControls(summary){
 const root=$('emconControl'),active=document.activeElement?.closest?.('#emconSeg button'),mode=active?.dataset.mode;
 root.innerHTML=gsPolicy.emconHTML(summary);
 if(mode){const button=root.querySelector(`#emconSeg [data-mode="${mode}"]`);(button&&!button.disabled?button:root.querySelector('#emconSeg'))?.focus({preventScroll:true})}
}
function renderPolicyPanels(summary){
 const admin=$('policyAdmin');admin.hidden=!summary?.admin;
 $('policyConflicts').innerHTML=gsPolicy.conflictsHTML(summary?.conflicts||[]);
 if(summary?.admin&&!policyRawLoaded){policyRawLoaded=true;loadRawPolicy(false)}
 clearTimeout(policyAuditTimer);
 policyAuditTimer=setTimeout(async()=>{
  try{const response=await fetch('/api/policy/audit?limit=50');const data=await response.json();$('policyAuditRows').innerHTML=response.ok?gsPolicy.auditRowsHTML(data.entries||[]):'<tr><td colspan="7">Super admin required to view audit.</td></tr>'}
  catch{$('policyAuditRows').innerHTML='<tr><td colspan="7">Audit unavailable.</td></tr>'}
 },500);
}
function renderPolicySummary(summary){
 state.policy=summary||null;renderPolicyControls(summary||{});renderPolicyPanels(summary||{});
 refreshMetadataEMCON(summary?.emcon?.mode);
}
async function loadRawPolicy(force){
 if(policyRawTouched&&!force)return;
 try{
  const response=await fetch('/api/policy'),data=await response.json();
  if(!response.ok)throw new Error(data.message||data.error||`Request failed (${response.status})`);
  if(force||!policyRawTouched)$('polRaw').value=JSON.stringify(data.policy,null,2);
  $('polRawMsg').textContent='';$('polRawMsg').className='';
 }catch(error){$('polRawMsg').textContent=error.message;$('polRawMsg').className='policy-error'}
}
let adminToken='';// super-admin re-auth: operator token, kept in memory only (never stored)
async function rawPolicyAction(action){
 if(typeof gsAuth!=='undefined'&&gsAuth.viewer()){toast('read-only role');return}
 const msg=$('polRawMsg');msg.textContent='';msg.className='';
 try{
  if(!adminToken){adminToken=(window.prompt('Super admin: paste the operator token to authorize this policy edit')||'').trim();if(!adminToken)throw new Error('Operator token required for super admin edits')}
  const response=await fetch(`/api/policy${action==='validate'?'/validate':''}`,{method:action==='validate'?'POST':'PUT',headers:{'Content-Type':'application/json',Authorization:`Bearer ${adminToken}`},body:$('polRaw').value});
  const data=await response.json();
  if(response.status===401){adminToken='';throw new Error('Operator token rejected; try again')}
  if(!response.ok)throw new Error(data.message||data.error||`Request failed (${response.status})`);
  msg.className='policy-valid';msg.textContent=`Valid · ${(data.conflicts||[]).length} conflicts`;
  msg.insertAdjacentHTML('beforeend',gsPolicy.conflictsHTML(data.conflicts||[]));
  if(action==='apply')policyRawTouched=false;
 }catch(error){msg.textContent=error.message;msg.className='policy-error';toast(error.message)}
}
function selectBand(it,band){
 const bands=it?bandsFor(it):[];if(bands.length<2||!bands.includes(band))return;
 selectedBands.set(it.id,band);
 if(state.page==='rv'){rv.shown=null;renderReview()}else if(state.sel===it.id)renderDetail();
}
function normalizeItem(item){
 item.dets=Array.isArray(item.dets)?item.dets:[];item.det=Array.isArray(item.det)?item.det:[];
 const sid=typeof item.src==='string'?item.src:item.src.id;
 item.ts=new Date(item.ts);item.src=SRC[sid]||{id:sid,name:String(sid).replace(/_/g,' '),scene:'open',mode:'eo',hw:'edge node',em:'edge detector',link:'ship→shore link'};
 const prev=findItem(item.id);if(prev&&prev.img!==item.img)imageCache.delete(item.id);
 return item;
}
function findItem(id){
 if(indexedItems!==items){itemByID=new Map(items.map(item=>[item.id,item]));indexedItems=items}
 return itemByID.get(id);
}
function queuePending(data){
 if(indexedPending!==pendingItems){pendingByID=new Map(pendingItems.map(event=>[event.item.id,event]));indexedPending=pendingItems}
 const previous=pendingByID.get(data.item.id);
 if(previous)Object.assign(previous,data);else{pendingItems.push(data);pendingByID.set(data.item.id,data)}
}
function scheduleFeedFlush(selectionOnly=false){
 if(!selectionOnly)feedNeedsRender=true;
 if(feedScheduled)return;
 feedScheduled=true;
 if(typeof requestAnimationFrame==='function')feedFrame=requestAnimationFrame(flushFeed);
 feedTimer=setTimeout(flushFeed,100);
}
function flushFeed(){
 if(!feedScheduled)return;
 feedScheduled=false;
 if(feedFrame!==null&&typeof cancelAnimationFrame==='function')cancelAnimationFrame(feedFrame);
 clearTimeout(feedTimer);feedFrame=feedTimer=null;
 if(!feedNeedsRender){
  for(const id of dirtyItems){const it=findItem(id);if(it?.el)updateCard(it)}
  dirtyItems.clear();return;
 }
 feedNeedsRender=false;
 const selectedDirty=dirtyItems.has(state.sel);
 renderFeed();renderStats();renderRp(true);
 if(selectedDirty)renderDetail();
 if(state.page==='rv'&&!rv.busy)renderReview();
}
function renderAll(){
 renderFeed(true);renderStats();renderRp();if(state.sel)renderDetail();if(state.page==='rv')renderReview();
}
function mergeItem(raw,fresh=true){
 const item=normalizeItem(raw);if(evictedIDs.has(item.id))return;
 const previous=findItem(item.id);
 invalidateMetadata(item,previous);
 if(previous){
  const el=previous.el;
  for(const key of Object.keys(previous))if(!Object.hasOwn(item,key))delete previous[key];
  Object.assign(previous,item,{el,fresh});
 }else{item.fresh=fresh;items.push(item);itemByID.set(item.id,item)}
 dirtyItems.add(item.id);
 if(rv.cur?.id===item.id)rv.cur=findItem(item.id);
 if(fresh){['st1','st2','st3','st4','st5'].forEach((id,index)=>setTimeout(()=>flash(id),index*140));setTimeout(()=>{const current=findItem(item.id);if(current){current.fresh=false;dirtyItems.add(current.id);scheduleFeedFlush(true)}},6000)}
}
function flash(id){const element=$(id);if(!element)return;element.classList.add('flash');setTimeout(()=>element.classList.remove('flash'),350)}
function applySnapshot(data){
 const reset=data.reset===true||(snapshotEpoch!==null&&data.epoch!==undefined&&data.epoch!==snapshotEpoch);
 if(data.epoch!==undefined)snapshotEpoch=data.epoch;
 if(reset)clearDemoItems();
 data.items=data.items.map(normalizeItem);
 const selection=state.sel;
 for(const item of data.items){dirtyItems.add(item.id);invalidateMetadata(item,findItem(item.id))}
 const elements=new Map(items.map(item=>[item.id,item.el]));
 if(state.paused&&!reset){
  const snapshot=new Map(data.items.map(item=>[item.id,item]));
  heldSnapshotIDs=new Set(snapshot.keys());
  items=items.map(item=>{const raw=snapshot.get(item.id);return raw?{...normalizeItem(raw),el:item.el}:item});
  pendingItems=pendingItems.map(event=>({...event,item:snapshot.get(event.item.id)||event.item}));
  const queued=new Set(pendingItems.map(event=>event.item.id));
  for(const item of data.items)if(!elements.has(item.id)&&!queued.has(item.id)&&!evictedIDs.has(item.id))pendingItems.push({item});
 }else{
  items=data.items.map(item=>{item=normalizeItem(item);item.el=elements.get(item.id);return item});
  pendingItems=[];evictedIDs.clear();heldSnapshotIDs=null;
 }
 const retained=new Set(items.map(item=>item.id));for(const id of imageCache.keys())if(!retained.has(id))imageCache.delete(id);
 for(const id of metadataCache.keys())if(!retained.has(id))metadataCache.delete(id);
 state.model=data.state.model;state.directives=data.state.directives;
 applyLive(data.live);
 renderPolicySummary(data.policy);
 stats=data.stats;cdr=data.cdr;weightData=data.weights||{};
 state.sel=findItem(selection)?selection:null;
 if(!state.sel){document.body.classList.remove('drawer-open');$('drawer').innerHTML='';closeZoom()}
 rv.cur=rv.cur&&(findItem(rv.cur.id)||rv.cur);
 renderAll();
}
function clearDemoItems(){
 ++resetRevision;items=[];pendingItems=[];evictedIDs.clear();heldSnapshotIDs=null;
 dirtyItems.clear();feedOrdered=[];renderedFeedReasons.clear();resetFeedWindow();
 imageCache.clear();metadataCache.clear();selectedBands.clear();feedReasons.clear();pulling.clear();
 closeDetail();closeCardMenu();$('drawer').innerHTML='';
 rv={cur:null,shown:null,displayed:null,navigation:rv.navigation+1,order:rv.order,done:0,agree:0,undo:[],busy:false,skipped:{},ask:null,askBusy:false,last:''};
 lastReview=null;$('reviewNoteBox').hidden=true;
 if(typeof workbench!=='undefined')workbench.clearItems();
}
// Server-reported capabilities (/api/config "features"). Demo-only controls
// exist only when the server says so; production (-tags prod) builds report
// them all false, and a missing block is treated the same way.
function gsFeatures(config){
 const f=config&&typeof config.features==='object'&&config.features||{};
 return {profile:String(f.profile||''),simulation:f.simulation===true,demoReset:f.demoReset===true,dataset:f.dataset===true,demoLinkControl:f.demoLinkControl===true};
}
function applyFeatures(features){
 document.body.classList.toggle('no-sim',!features.simulation);
 document.body.classList.toggle('prod',features.profile==='prod');
 if(!features.dataset){const el=$('datasetStrip');if(el){el.hidden=true;el.remove?.()}}
 if(!features.simulation){
  $('simStrip').hidden=true;
  for(const el of document.querySelectorAll('.sim-only'))el.hidden=true;
 }
}
function setupDemoReset(enabled){
 if(!enabled){for(const id of ['resetDemoBtn','resetDemoDialog']){const el=$(id);if(el){el.hidden=true;el.remove?.()}}return}
 $('resetDemoBtn').hidden=!enabled||(typeof gsAuth!=='undefined'&&gsAuth.viewer());
 $('resetDemoBtn').onclick=()=>{$('resetDemoDialog').showModal();$('resetDemoCancel').focus()};
 $('resetDemoCancel').onclick=()=>$('resetDemoDialog').close();
 $('resetDemoConfirm').onclick=async()=>{
  const button=$('resetDemoConfirm');button.disabled=true;
  try{await request('/api/demo/reset','POST',{confirm:'reset'});$('resetDemoDialog').close();toast('Demo reset — new captures keep arriving')}
  catch(error){toast(error.message)}finally{button.disabled=false}
 };
}
function applyLive(live){
 LIVE=live||null;const cl=document.body?.classList;if(cl?.toggle)cl.toggle('live',!!LIVE);
 renderLive();
 if(!LIVE)return;
 refreshMetadataEMCON(LIVE.policy?.emcon?.mode);
 mergeLiveConfig(LIVE);
 const banner=document.querySelector?.('.banner');if(banner)banner.textContent='UNCLASSIFIED // FOR TRAINING AND DEMONSTRATION ONLY — LIVE SHORE PICTURE';
}
function applyItemUpdate(raw){
 raw=normalizeItem(raw);
 if(state.paused&&!findItem(raw.id)){
  if(evictedIDs.has(raw.id))return;
  heldSnapshotIDs?.add(raw.id);
  queuePending({item:raw});
  scheduleFeedFlush();return;
 }
 mergeItem(raw,false);scheduleFeedFlush();
}
async function request(path,method='GET',body){
 const revision=resetRevision,itemWrite=method!=='GET'&&/^\/api\/(items|reviews)\//.test(path);
 if(typeof gsAuth!=='undefined')gsAuth.requireWrite(method,path);
 const response=await fetch(path,{method,headers:body?{'Content-Type':'application/json'}:{},body:body?JSON.stringify(body):undefined});
 const data=response.status===204?null:await response.json();
 if(!response.ok){const error=new Error(data?.message||data?.error||`Request failed (${response.status})`);error.status=response.status;if(data?.conflicts?.length){error.policyConflicts=data.conflicts;toast(gsPolicy.conflictToast(data.conflicts))}throw error}
 if(itemWrite&&(revision!==resetRevision||(data?.item&&!findItem(data.item.id))))throw new Error('Image is no longer available — the feed changed');
 return data;
}
async function patchItem(id,changes){
 const result=await request(`/api/items/${encodeURIComponent(id)}`,'PATCH',changes);
 cdr=result.cdr;applyItemUpdate(result.item);return findItem(id);
}
function renderLive(){
 const shoreUnavailable=!!LIVE?.shore?.error;
 const warning=$('shoreWarning');if(warning)warning.hidden=!shoreUnavailable;
 const live=$('live');live.classList.toggle('off',!!state.offline||shoreUnavailable);live.classList.toggle('paused',!state.offline&&!shoreUnavailable&&state.paused);
 live.querySelector('span').textContent=shoreUnavailable?'SHORE UNREACHABLE':state.offline?'RECONNECTING':state.paused?'FEED PAUSED':'FEED ACTIVE';
 const action=state.paused?'resume feed':'pause feed';
 live.title=shoreUnavailable?`Shore unreachable — showing last-known data; click to ${action}`:state.offline?`Reconnecting — click to ${action}`:state.paused?'Feed paused — click to resume feed':'Feed active — click to pause feed';
 live.setAttribute('aria-label',live.title);live.setAttribute('aria-pressed',String(!!state.paused));
 $('pauseBtn').textContent=state.paused?'Resume feed':'Pause feed';$('wbHeld').hidden=!state.paused;
}
function toggleHold(){
 state.paused=!state.paused;renderLive();
 if(!state.paused){if(heldSnapshotIDs){const keep=heldSnapshotIDs;for(const item of items)if(!keep.has(item.id)){imageCache.delete(item.id);if(state.sel===item.id)closeDetail()}items=items.filter(item=>keep.has(item.id));pendingItems=pendingItems.filter(event=>keep.has(event.item.id));heldSnapshotIDs=null}for(const event of pendingItems)mergeItem(event.item);pendingItems=[];renderAll()}
}
function openStream(){
 const stream=new EventSource('/api/stream');
 stream.onopen=()=>{state.offline=false;renderLive()};
 stream.onerror=()=>{state.offline=true;renderLive()};
 stream.addEventListener('snapshot',event=>{applySnapshot(JSON.parse(event.data));refreshTeamProposals();if(reviewDataReady)refreshReviewData()});
 stream.addEventListener('review-data',event=>{reviewDataRevision++;renderReviewData(JSON.parse(event.data))});
 stream.addEventListener('review-sync',event=>receiveTeamProposal(JSON.parse(event.data).proposal));
 stream.addEventListener('item',event=>{
  const data=JSON.parse(event.data);
  data.item=normalizeItem(data.item);
  stats=data.stats;if(state.paused){queuePending(data);heldSnapshotIDs?.add(data.item.id)}else mergeItem(data.item);
  scheduleFeedFlush();
 });
 stream.addEventListener('stats',event=>{
  stats=JSON.parse(event.data).stats;scheduleFeedFlush();
 });
 stream.addEventListener('update',event=>{
  const data=JSON.parse(event.data);cdr=data.cdr;applyItemUpdate(data.item);
 });
 stream.addEventListener('live',event=>{
  applyLive(JSON.parse(event.data));renderStats();if(state.page==='feed')renderCameras();
 });
 stream.addEventListener('evict',event=>{
  const {id}=JSON.parse(event.data);evictedIDs.add(id);imageCache.delete(id);metadataCache.delete(id);pendingItems=pendingItems.filter(data=>data.item.id!==id);
  items=items.filter(item=>item.id!==id);
  if(state.sel===id)closeDetail();
  scheduleFeedFlush();
 });
}
async function init(){
 if(typeof gsAuth!=='undefined')await gsAuth.init();
 const config=await request('/api/config');
 const features=gsFeatures(config);
 applyFeatures(features);
 setupDemoReset(features.demoReset);
 setupTeamSync(!!config.reviewSync);
 DATASET_MODE=features.dataset;
 SOURCES=[{id:'ALL',name:DATASET_MODE?'All sources (combined)':'All sensors (combined)'},...config.sources];
 SRC=Object.fromEntries(SOURCES.map(source=>[source.id,source]));
 CLS=config.classes;MODELS=config.models;ATTR_LAB=config.attrLabels;EXAMPLES=config.examples;
 if(DATASET_MODE){$('classificationBanner').textContent='UNCLASSIFIED // FOR TRAINING AND DEMONSTRATION ONLY · REAL IMAGERY · clean v2 dev';$('simStrip').hidden=true;$('datasetStrip').hidden=false;$('sourceLabel').textContent='Source';}
 if(config.live&&!MODELS[state.model])state.model=Object.keys(MODELS)[0]||state.model;
 $('srcSel').innerHTML=SOURCES.map(s=>`<option value="${esc(s.id)}">${esc(s.name)}</option>`).join('');
 $('modelSel').innerHTML=Object.entries(MODELS).map(([k,m])=>`<option value="${esc(k)}">${esc(m.name)}</option>`).join('');
 $('examples').innerHTML=EXAMPLES.map(e=>`<span class="ex">${esc(e)}</span>`).join('');
 $('examples').onclick=e=>{if(e.target.classList.contains('ex')){$('nlIn').value=e.target.textContent;workbench.textChanged()}};
 $('srcSel').onchange=e=>{gsSettings.setFeed({sensor:e.target.value});syncFeedFilters()};
 $('showDuplicates').onchange=e=>{const on=e.target.checked,n=repeatCount();gsSettings.setFeed({showDuplicates:on});syncFeedFilters();toast(n?on?`Showing ${n} repeat image${n===1?'':'s'}, each placed right after its original (kept at P4 Low)`:`Hid ${n} repeat image${n===1?'':'s'}. Originals stay in the feed.`:'No repeat images to hide right now')};
 $('hideUploaded').onchange=e=>{const on=e.target.checked,n=uploadedCount();gsSettings.setFeed({hideUploaded:on});syncFeedFilters();toast(on?n?`Hid ${n} image${n===1?'':'s'} already uploaded to shore. Queued and uploading images stay.`:'No uploaded images to hide yet. Images leave the feed once they reach shore.':`Showing ${n} uploaded image${n===1?'':'s'} again`)};
 $('rvSent').onclick=()=>{gsSettings.setFeed({review:'sent',tier:'all'});syncFeedFilters();location.hash='#feed'};
 $('reviewFilter').onchange=e=>{gsSettings.setFeed({review:e.target.value});syncFeedFilters()};
 $('clearFeedFilters').onclick=resetFeedFilters;
 $('sentShowAll').onclick=()=>{gsSettings.setFeed({review:'all'});syncFeedFilters()};
 window.addEventListener('gs:feed-filters',syncFeedFilters);syncFeedFilters();
 $('modelSel').onchange=async e=>{try{await request('/api/model','PUT',{id:e.target.value});toast(`Base model set to ${MODELS[e.target.value].name}`)}catch(error){toast(error.message);e.target.value=state.model}};
 workbench.init();
 briefing.init();
 $('clearDir').onclick=async()=>{if(!state.directives.length)return;try{await request('/api/directives','DELETE');toast('Directives cleared — base model restored')}catch(error){toast(error.message)}};
 $('sortSeg').onclick=e=>{const b=e.target.closest('button');if(!b)return;state.sort=b.dataset.s;[...$('sortSeg').children].forEach(x=>x.classList.toggle('on',x===b));resetFeedWindow();renderFeed(true,true)};
 $('densSeg').onclick=e=>{const b=e.target.closest('button');if(!b)return;state.dens=b.dataset.d;gsSettings.density(state.dens);renderFeed(true,true)};
 $('feedFilters').onclick=toggleFeedFilters;
 $('tierSeg').onclick=e=>{const b=e.target.closest('button');if(!b)return;gsSettings.setFeed({tier:b.dataset.t});syncFeedFilters()};
 $('pauseBtn').onclick=toggleHold;$('live').onclick=toggleHold;$('wbResume').onclick=toggleHold;
 $('emconControl').onclick=async e=>{const button=e.target.closest('#emconSeg [data-mode]');if(!button||button.disabled)return;try{const result=await request('/api/policy/emcon','POST',{mode:button.dataset.mode});if(result.conflicts?.length)toast(gsPolicy.conflictToast(result.conflicts))}catch(error){if(!error.policyConflicts)toast(error.message)}};
 $('polRaw').oninput=()=>{policyRawTouched=true};
 $('polRawValidate').onclick=()=>rawPolicyAction('validate');$('polRawApply').onclick=()=>rawPolicyAction('apply');$('polRawReload').onclick=()=>{policyRawTouched=false;loadRawPolicy(true)};
 $('grid').onclick=e=>{const c=e.target.closest('.card');if(c)openDetail(c.dataset.id)};
 setupFeedKeyboard();
 $('grid').oncontextmenu=openCardMenu;$('cmSendNow').onclick=()=>sendNow($('cardMenu').dataset.id);$('cmOpen').onclick=()=>{const id=$('cardMenu').dataset.id;closeCardMenu();openDetail(id)};
 document.addEventListener('pointerdown',e=>{if(!e.target.closest('#cardMenu'))closeCardMenu()},true);document.addEventListener('keydown',e=>{if(e.key==='Escape'&&!$('cardMenu').hidden){e.stopImmediatePropagation();closeCardMenu()}},true);window.addEventListener('blur',closeCardMenu);window.addEventListener('hashchange',closeCardMenu);$('feed').addEventListener('scroll',closeCardMenu);
 document.addEventListener('click',e=>{const b=e.target.closest('[data-band-item]');if(!b)return;e.stopPropagation();selectBand(findItem(b.dataset.bandItem),b.dataset.band)},true);
 document.addEventListener('keydown',e=>{if(document.querySelector('dialog[open]')||$('settings').open)return;if(gsPolicy.isPolicyHotkeyTarget(e.target))return;if(typeof gsAuth!=='undefined'&&gsAuth.viewer())return;const tg=e.target.tagName;if(tg==='TEXTAREA'||tg==='INPUT'||tg==='SELECT'||e.ctrlKey||e.metaKey||e.altKey)return;
  const k=e.key.toLowerCase();
  if(e.target.closest?.('.metadata-panel'))return;
  if(e.defaultPrevented||e.target.isContentEditable)return;
  if((k==='enter'||k===' ')&&(tg==='BUTTON'||e.target.closest?.('button, a[href], summary, header, .band-controls')))return;
  if(state.page==='rv'&&k==='w'){e.preventDefault();openReviewNote();return}
  if(state.page==='rv'&&rv.ask){e.preventDefault();if(!e.repeat)reviewAskKey(k);return}
  if(k==='n'){toggleNight();return}
  if(k==='b'&&(state.page==='rv'||state.page==='feed')){const it=state.page==='rv'?rv.cur:findItem(state.sel);const bands=it?bandsFor(it):[];if(bands.length>1){e.preventDefault();if(!e.repeat)selectBand(it,bands[(bands.indexOf(bandFor(it))+1)%bands.length])}return}
  if(state.page==='rv'){if(k==='escape'){location.hash='#feed';return}const a=reviewKeyAction(k);if(a){e.preventDefault();if(!e.repeat)rvAct(a)}return}
  if(state.page!=='feed')return;
  if(k==='r'){location.hash='#review';return}
  if(k==='escape'){if(state.zoom)closeZoom();else closeDetail();return}
  if(k==='f'&&state.sel){state.zoom?closeZoom():openZoom();return}
  if(state.sel&&['1','2','3','4'].includes(k)&&!(typeof gsAuth!=='undefined'&&gsAuth.reviewer()))setManual(state.sel,+k);
  if(state.sel&&['arrowright','arrowdown','arrowleft','arrowup'].includes(k)){e.preventDefault();step(k==='arrowright'||k==='arrowdown'?1:-1)}});
 $('rvKeys').onclick=e=>{const b=e.target.closest('.kb');if(b&&(!rv.ask||b.dataset.a==='undo')){b.blur();rvAct(b.dataset.a)}};
 $('rvAsk').onclick=e=>{const b=e.target.closest('[data-ask]');if(!b)return;if(b.dataset.ask==='why')answerWhy(b.dataset.choice);else if(b.dataset.ask==='team')answerTeamProposal(b.dataset.answer);else answerSuggestion(b.dataset.answer)};
 $('rvOrder').onclick=e=>{const b=e.target.closest('button');if(!b||rv.busy)return;rv.order=b.dataset.o;[...$('rvOrder').children].forEach(x=>x.classList.toggle('on',x===b));rv.cur=null;rv.shown=null;renderReview()};
 $('nightBtn').onclick=toggleNight;
 window.addEventListener('hashchange',route);
 window.addEventListener('gs:display-settings',()=>renderFeed(true,true));
 setupFeedWindow();
 setInterval(()=>$('clock').textContent=zulu(new Date()),1000);$('clock').textContent=zulu(new Date());
 route();renderFeed(true);renderStats();renderRp();openStream();
}
function route(){
 state.page=location.hash==='#details'?'details':location.hash==='#briefing'?'brief':location.hash==='#reprioritize'?'rp':location.hash==='#review'?'rv':'feed';
 $('pgBrief').classList.toggle('on',state.page==='brief');
 $('pgFeed').classList.toggle('on',state.page==='feed');$('pgRp').classList.toggle('on',state.page==='rp');$('pgRv').classList.toggle('on',state.page==='rv');$('navRv').classList.toggle('on',state.page==='rv');
 $('pgDetails').classList.toggle('on',state.page==='details');
 $('navFeed').classList.toggle('on',state.page==='feed');$('navRp').classList.toggle('on',state.page==='rp');
 if(state.page!=='rv'&&rv.ask){rv.ask=null;rv.askBusy=false;renderAsk()}
 if(state.page==='feed')renderFeed(true);else if(state.page==='details'){closeDetail();renderDetails()}else if(state.page==='brief'){closeDetail();briefing.render()}else if(state.page==='rv'){closeDetail();rv.shown=null;renderReview()}else{closeDetail();renderRp();workbench.activate();setTimeout(()=>$('nlIn').focus(),50)}
}

function syncFeedFilters(){
 const f=gsSettings.get().feed;
 const previousSource=state.src;
 state.src=f.sensor&&SRC[f.sensor]?f.sensor:'ALL';state.tier=f.tier||'all';
 if(state.src!==previousSource){rv.navigation++;rv.cur=null;rv.shown=null;rv.displayed=null}
 if(f.sensor&&state.src!==f.sensor)gsSettings.setFeed({sensor:state.src});
 $('srcSel').value=state.src;$('showDuplicates').checked=f.showDuplicates;$('hideUploaded').checked=f.hideUploaded;$('reviewFilter').value=f.review;$('sentOrder').hidden=f.review!=='sent';
 $('clearFeedFilters').hidden=state.src==='ALL'&&state.tier==='all'&&f.review==='all'&&!f.showDuplicates&&!f.hideUploaded;
 closeDetail();resetFeedWindow();renderFeed(true,true);renderStats();renderRp();
}
function resetFeedFilters(){gsSettings.setFeed({sensor:'ALL',tier:'all',review:'all',showDuplicates:false,hideUploaded:false});syncFeedFilters()}
function humanReviewed(it){return !isScene(it)&&!!(it.humanReviewed||!it.autoSent&&(it.reviewed||it.manual||it.dismissed))}
function humanSent(it){return !isScene(it)&&!!(it.sent&&!it.autoSent||it.fullRes)}
function sendTime(it){return Math.max(it.sent&&!it.autoSent&&it.sentAt?Date.parse(it.sentAt):0,it.fullRes&&it.fullResAt?Date.parse(it.fullResAt):0)}
const FILTERED_NAME={duplicate:'Repeat of an earlier image','low-value':'Low value'};
function filteredReason(it){return it.dismissed?'Not relevant':it.manual||it.sent?'':FILTERED_NAME[it.filtered]||(it.tier===4&&(it.live?.suppressed||it.det?.length&&it.det.every(d=>d.sup))?'Lowered by directive':'')}
function feedTier(it){return it.tier}
function reviewMatch(it,review){return isScene(it)?review==='all':review==='sent'?humanSent(it):review==='reviewed'?humanReviewed(it):review==='unreviewed'?!humanReviewed(it):!it.dismissed}
function visible(){
 const f=typeof gsSettings!=='undefined'?gsSettings.get().feed:{showDuplicates:false,review:'all'};
 let l=items.filter(i=>(state.src==='ALL'||i.src.id===state.src)&&reviewMatch(i,f.review)&&(f.showDuplicates||!hideableRepeat(i))&&!(f.hideUploaded&&uploadState(i,false).done));
 feedReasons=new Map();l.forEach(i=>{const reason=filteredReason(i);if(reason)feedReasons.set(i.id,reason)});
 const counts={all:l.length,1:0,2:0,3:0,4:0};l.forEach(i=>counts[feedTier(i)]++);
 if(state.tier!=='all')l=l.filter(i=>String(feedTier(i))===state.tier);
 l.sort(f.review==='sent'?(a,b)=>sendTime(b)-sendTime(a)||b.ts-a.ts:state.sort==='pri'?(a,b)=>feedTier(a)-feedTier(b)||Number(feedReasons.has(a.id))-Number(feedReasons.has(b.id))||b.central-a.central||b.ts-a.ts:(a,b)=>b.ts-a.ts);
 if(state.src==='ALL'&&f.review!=='sent')l=mixSources(l,state.sort==='pri'?i=>feedTier(i)+(feedReasons.has(i.id)?'f':''):i=>Math.floor(i.ts/60000));
 if(f.showDuplicates&&f.review!=='sent')l=pairRepeats(l);
 return{l,counts};
}
function hideableRepeat(it){return !isScene(it)&&it.filtered==='duplicate'&&!humanReviewed(it)&&!humanSent(it)}
function uploadedCount(){const f=typeof gsSettings!=='undefined'?gsSettings.get().feed:{showDuplicates:true,review:'all'};return items.filter(i=>(state.src==='ALL'||i.src.id===state.src)&&(state.tier==='all'||String(feedTier(i))===state.tier)&&reviewMatch(i,f.review)&&(f.showDuplicates||!hideableRepeat(i))&&uploadState(i,false).done).length}
function repeatCount(){const f=typeof gsSettings!=='undefined'?gsSettings.get().feed:{review:'all'};return items.filter(i=>(state.src==='ALL'||i.src.id===state.src)&&(state.tier==='all'||String(feedTier(i))===state.tier)&&reviewMatch(i,f.review)&&hideableRepeat(i)&&!(f.hideUploaded&&uploadState(i,false).done)).length}
/* Display-only: show each repeat right after its original so the duplicate filter is visible at the top of the feed. */
function pairRepeats(l){
 const originals=new Map(),after=new Map();
 for(const i of l)if(!originals.has(i.id))originals.set(i.id,i);
 for(const i of l)if(hideableRepeat(i)&&i.dupOf){const o=originals.get(i.dupOf);if(o&&!hideableRepeat(o)){if(!after.has(o.id))after.set(o.id,[]);after.get(o.id).push(i)}}
 if(!after.size)return l;
 const moved=new Set([...after.values()].flat().map(i=>i.id)),out=[];
 for(const i of l){if(moved.has(i.id))continue;out.push(i);if(after.has(i.id))out.push(...after.get(i.id))}
 return out;
}
/* Display-only: interleave sensors so the feed shows several camera feeds at once.
   Items only move within a run sharing the same group key (priority tier, or capture minute when sorted by time)
   and at most SOURCE_MIX_WINDOW places, so priorities and visible ordering cues are unchanged. */
const SOURCE_MIX_WINDOW=8;
function mixSources(l,group){
 const srcOf=i=>i.src?.id??i.src;
 if(new Set(l.map(srcOf)).size<2)return l;
 const window=[],out=[],last=new Map();let next=0;
 while(next<l.length||window.length){
  while(next<l.length&&window.length<SOURCE_MIX_WINDOW){const it=l[next++];window.push({it,src:srcOf(it),group:group(it)})}
  const g=window[0].group;let pick=0,best=Infinity;
  for(let j=0;j<window.length&&window[j].group===g;j++){const seen=last.has(window[j].src)?last.get(window[j].src):-1;if(seen<best){best=seen;pick=j}}
  const {it,src}=window.splice(pick,1)[0];last.set(src,out.length);out.push(it);
 }
 return out;
}
function renderTierSeg(counts){
 const T=[['all','All'],['1','P1 Critical'],['2','P2 High'],['3','P3 Routine'],['4','P4 Low']];
 $('tierSeg').innerHTML=T.map(([k,l])=>`<button data-t="${k}" class="${state.tier===k?'on':''}">${k!=='all'?`<span class="tchip" style="background:${tcol(+k)}"></span>`:''}${l}<span class="c">${counts[k]}</span></button>`).join('');
}
function boxesHTML(it,labels){
 return it.dets.map((d,i)=>{const sd=it.det[i];if(!d.b||!(d.b.w>0))return'';const c=sd.sup?'sup':sd.matched?'match':sd.s>=.6?'hi':sd.s>=.3?'md':'lo';const b=d.b;
  return`<div class="box ${c}${d.cls==='unknown'?' unknown':''}${b.x+b.w/2>W*.6?' r':''}${b.y<H*.08?' t':''}" style="left:${b.x/W*100}%;top:${b.y/H*100}%;width:${b.w/W*100}%;height:${b.h/H*100}%">${labels?`<em>${esc(clsOf(d.cls).lab)} ${Math.round(d.conf*100)}%</em>`:''}</div>`}).join('');
}
function isScene(it){return it?.live?.event==='scene'}
function isEmptyWater(it){return isScene(it)||!!(it&&!it.live&&it.background)}
function topLine(it){
	if(isScene(it))return 'Empty water · no contacts';
 const r=it.dets.map((d,i)=>[d,it.det[i]]).sort((a,b)=>b[1].s-a[1].s);if(!r.length)return it.background?'No contacts · empty water':'—';
 if(it.live){const l=it.live,[d,sd]=r[0];const det=l.label&&l.label.toLowerCase()!==d.cls?` <span style="opacity:.6">· computer label: ${esc(l.label)}</span>`:'';const frames=l.sightingId?(l.framesKnown?` · ${l.frames} frames`:` · ${l.reports} report${l.reports===1?'':'s'}`):'';
  return`<span class="${l.suppressed?'s':sd.matched?'m':l.unknown?'a':''}">${esc(clsOf(d.cls).lab)}${l.unknown?' (unknown)':''}</span>${det}<span style="opacity:.6">${frames}</span>`}
 const[d,sd]=r[0];const tag=d.attrs.dark&&clsOf(d.cls).g!=='other'&&clsOf(d.cls).g!=='clutter'?' · no AIS':d.attrs.approaching?' · inbound':d.attrs.new?' · new':'';
 return`<span class="${sd.matched?'m':tag&&sd.s>.4?'a':''}">${esc(clsOf(d.cls).lab)}${tag}</span>${r.length>1?` <span style="opacity:.6">+${r.length-1}</span>`:''}`;
}
function detectorNegativeLabel(it){return it?.detectorNegative?'Detector found nothing — check for missed objects':''}
function detectorNegativeHTML(it){const label=detectorNegativeLabel(it);return label?`<div class="detector-negative">${label}</div>`:''}
function reviewAcceptLabel(it){return it.detectorNegative?'Nothing here & next':`Agree (P${it.centralTier} ${TNAME[it.centralTier]}) & next`}
function reviewKeyAction(key){return RVKEYS[key]}
function cardHTML(it,mini){
 return`${detectorNegativeHTML(it)}<div class="thumb"><div class="thumb-image"><img src="${esc(imageFor(it))}" alt=""><div class="bx">${mini?boxesHTML(it,false):''}</div></div><span class="tier t${it.tier}" title="${it.tier===1?'Notify Commander':'Image priority'}">P${it.tier} ${TNAME[it.tier]}</span><span class="cdrtag">✓ Auto-sent to Commander</span><span class="hsenttag">➤ Sent by operator</span><span class="newtag">NEW</span><div class="ov"><span class="sid">${esc(it.src.id)}</span><span class="t mono">${zulu(it.ts).slice(0,5)}Z</span><span class="delta"></span><span class="rvk" style="margin-left:auto">✓</span><b class="cs" style="margin-left:4px">${mini?it.central.toFixed(2):''}</b></div><span class="formtag"></span><span class="suptag">DEPRIORITIZED</span></div><div class="cl"></div><div class="fchips"></div>`;
}
function sendNowBlocked(it){const u=uploadState(it);return it.live||LIVE?'Not available on the live shore connection':typeof gsAuth!=='undefined'&&gsAuth.viewer()?'Viewer role is read-only':u.done?'Already uploaded to shore':u.next?'Already at the top of the send queue':''}
function openCardMenu(e){
 const c=e.target.closest('.card'),it=c&&findItem(c.dataset.id);if(!it)return;
	if(isScene(it)){e.preventDefault();return}
 e.preventDefault();
 const m=$('cardMenu'),blocked=sendNowBlocked(it);
 m.dataset.id=it.id;$('cmSendNow').disabled=!!blocked;$('cmSendNowSub').textContent=blocked||'Moves this image to the top of the send queue';
 m.hidden=false;
 const r=m.getBoundingClientRect();
 m.style.left=Math.max(4,Math.min(e.clientX,innerWidth-r.width-4))+'px';m.style.top=Math.max(4,Math.min(e.clientY,innerHeight-r.height-4))+'px';
 $('cmSendNow').focus();
}
function closeCardMenu(){const m=$('cardMenu');if(m&&!m.hidden)m.hidden=true}
async function sendNow(id){
 closeCardMenu();
 const it=findItem(id);if(!it)return;
 const blocked=sendNowBlocked(it);if(blocked){toast(blocked);return}
 try{const result=await request(`/api/items/${encodeURIComponent(it.id)}/upload-override`,'POST',{override:'next'});cdr=result.cdr;applyItemUpdate(result.item);toast(`Sending now: ${it.src.id} image moved to the top of the send queue`)}
 catch(error){toast(error.message)}
}
function setupFeedKeyboard(){
 const grid=$('grid');
 grid.onfocusin=e=>{const card=e.target.closest('.card');if(!card)return;const previous=findItem(feedFocusID)?.el;if(previous&&previous!==card)previous.tabIndex=-1;feedFocusID=card.dataset.id;card.tabIndex=0};
 grid.onkeydown=e=>{
  const card=e.target.closest('.card');if(!card||e.target!==card||e.ctrlKey||e.metaKey||e.altKey)return;
  const k=e.key.toLowerCase();
  if(k==='enter'||k===' '){e.preventDefault();e.stopPropagation();if(!e.repeat)openDetail(card.dataset.id);return}
  if(!['arrowleft','arrowup','arrowright','arrowdown'].includes(k))return;
  e.preventDefault();e.stopPropagation();
  if(state.sel){step(k==='arrowleft'||k==='arrowup'?-1:1);findItem(state.sel)?.el?.focus();return}
  const index=feedOrdered.findIndex(it=>it.id===card.dataset.id),direction=k==='arrowleft'||k==='arrowup'?-1:1;
  const next=feedOrdered[Math.max(0,Math.min(feedOrdered.length-1,index+direction))];if(!next)return;
  if(revealFeedItem(next.id))renderFeed(true);
  next.el?.focus();
 };
}
function makeCard(it){const el=document.createElement('div');el.dataset.id=it.id;el.tabIndex=-1;el.setAttribute('role','button');el.innerHTML='<span class="card-context sr-only"></span>'+cardHTML(it);el.querySelector('.thumb').insertAdjacentHTML('beforeend','<span class="sendmark"></span>');el.querySelector('.thumb').insertAdjacentHTML('afterend',`<div class="upload-slot">${uploadHTML(it)}</div>`);applySendMark(el,it);if(bandsFor(it).length>1)el.querySelector('.thumb').insertAdjacentHTML('beforeend',`<span class="band-badge">${bandsFor(it).map(esc).join('/')}</span>`);it.el=el;return el}
function updateCard(it){
 const el=it.el;if(!el)return;const tier=feedTier(it),reason=feedReasons.get(it.id);el.className=`card p${tier}${reason?' sup':''}${it.fresh?' fresh':''}${it.sent&&it.autoSent?' sent':''}${humanSent(it)?' hsent':''}${humanReviewed(it)?' rev':''}${it.manual?' man':''}${state.sel===it.id?' sel':''}`;
 el.querySelector('.card-context').textContent=`P${tier} ${TNAME[tier]} image from ${it.src.name||it.src.id}. Open image details`;
 const reviewMark=el.querySelector('.rvk');reviewMark.title=humanReviewed(it)?'Human reviewed':'Not reviewed';reviewMark.setAttribute('aria-label',reviewMark.title);
 const status=el.querySelector('.suptag');status.textContent=reason||'';status.title=reason?`${reason} — shown at P4 Low`:'';
 el.querySelector('.night-badge')?.remove();if(!DATASET_MODE)el.querySelector('.thumb').insertAdjacentHTML('beforeend',gsLights.badge(it));
 const t=el.querySelector('.tier');t.className='tier t'+tier;t.textContent=`P${tier} ${TNAME[tier]}`;t.title=reason?status.title:it.tier===1?'Notify Commander':'Image priority';
 el.querySelector('.bx').innerHTML=boxesHTML(it,false);el.querySelector('.cl').innerHTML=topLine(it);
 el.querySelector('.cs').textContent=isScene(it)?'':it.central.toFixed(2);
 const upload=el.querySelector('.upload-slot');if(upload)upload.innerHTML=uploadHTML(it);applySendMark(el,it);
 if(it.live){const ft=el.querySelector('.formtag');ft.textContent=it.live.imageForm?FORM_NAME[it.live.imageForm]:'Summary only';ft.title=it.live.imageForm?`Image shown is the ${FORM_NAME[it.live.imageForm]} (${fmtBytes(it.live.forms[it.live.imageForm]?.bytes||0)}). Hover the image-version labels below for transfer status.`:'Only the object summary has arrived. Images follow as bandwidth becomes available.';el.querySelector('.fchips').innerHTML=formChipsHTML(it)}
 const dl=it.central-it.base,de=el.querySelector('.delta');de.className='delta '+(dl>.04?'up':dl<-.04?'dn':'');de.textContent=dl>.04?`▲${dl.toFixed(2).slice(1)}`:dl<-.04?`▼${(-dl).toFixed(2).slice(1)}`:'';
}
function toggleFeedFilters(){
 const expanded=$('feedHead').classList.toggle('filters-open');
 $('feedFilters').setAttribute('aria-expanded',String(expanded));
}
function resetFeedWindow(){feedLimit=FEED_PAGE_SIZE;$('feed').scrollTop=0}
function growFeedWindow(){
 if(feedLimit>=feedOrdered.length)return;
 feedLimit=Math.min(feedOrdered.length,feedLimit+FEED_PAGE_SIZE);renderFeed();
}
function revealFeedItem(id){
 const index=feedOrdered.findIndex(it=>it.id===id);
 if(index<feedLimit)return false;
 feedLimit=Math.ceil((index+1)/FEED_PAGE_SIZE)*FEED_PAGE_SIZE;
 return true;
}
function setupFeedWindow(){
 const more=$('feedMore');more.onclick=growFeedWindow;
 if(typeof IntersectionObserver==='function'){
  const observer=new IntersectionObserver(entries=>{if(entries.some(e=>e.isIntersecting)&&!more.hidden)growFeedWindow()},{root:$('feed'),rootMargin:'400px'});
  observer.observe(more);
 }
}
function renderFeed(noAnim,refreshCards=false){
 const{l,counts}=visible();feedOrdered=l;renderTierSeg(counts);const dc=$('dupCount');if(dc)dc.textContent=String(repeatCount());const uc=$('uploadedCount');if(uc)uc.textContent=String(uploadedCount());$('empty').style.display=l.length?'none':'block';
 const windowed=l.slice(0,feedLimit),ids=windowed.map(it=>it.id),grid=$('grid');
 const focusedCard=document.activeElement?.closest?.('#grid .card');
 if(!ids.includes(feedFocusID))feedFocusID=ids[0]||null;
 const changed=ids.length!==renderedFeedIDs.length||ids.some((id,i)=>id!==renderedFeedIDs[i]);
 const animate=changed&&!noAnim&&state.page==='feed'&&windowed.length<=60&&dirtyItems.size<=12;
 const first=new Map();
 if(animate)for(const el of grid.children)first.set(el.dataset.id,el.getBoundingClientRect());
 if(changed){
  const keep=new Set(ids);
  for(const el of [...grid.children])if(!keep.has(el.dataset.id)){el.remove();const it=findItem(el.dataset.id);if(it)it.el=null}
 }
 windowed.forEach((it,idx)=>{
  const fresh=!it.el,el=it.el||makeCard(it),reason=feedReasons.get(it.id);
  if(fresh||refreshCards||dirtyItems.has(it.id)||renderedFeedReasons.get(it.id)!==reason)updateCard(it);
  const tabIndex=it.id===feedFocusID?0:-1;if(el.tabIndex!==tabIndex)el.tabIndex=tabIndex;
  if(changed&&grid.children[idx]!==el)grid.insertBefore(el,grid.children[idx]||null);
 });
 if(focusedCard&&document.activeElement!==findItem(feedFocusID)?.el)findItem(feedFocusID)?.el?.focus({preventScroll:true});
 if(animate){
  const positions=windowed.map(it=>({it,rect:it.el.getBoundingClientRect()})),vh=$('feed').getBoundingClientRect().bottom+200;
  for(const {it,rect:r} of positions){
   const f=first.get(it.id),el=it.el;if(r.top>vh&&(!f||f.top>vh))continue;
   if(f){const dx=f.left-r.left,dy=f.top-r.top;if((Math.abs(dx)>1||Math.abs(dy)>1)&&el.animate)el.animate([{transform:`translate(${dx}px,${dy}px)`},{transform:'none'}],{duration:500,easing:'cubic-bezier(.2,.8,.2,1)'})}
   else if(it.fresh&&el.animate)el.animate([{opacity:0,transform:'translateY(12px)'},{opacity:1,transform:'none'}],{duration:350});
  }
 }
 renderedFeedIDs=ids;renderedFeedReasons=new Map(windowed.map(it=>[it.id,feedReasons.get(it.id)]));dirtyItems.clear();
 const more=$('feedMore');more.hidden=windowed.length>=l.length;
 const message=`Showing ${windowed.length.toLocaleString()} of ${l.length.toLocaleString()} images · Load more`;
 if(more.textContent!==message)more.textContent=message;
 renderDirBar();if(LIVE)renderCameras();
}
function renderDirBar(){
 const d=state.directives;$('dirCount').textContent=d.length||'';
 if(LIVE){const p=LIVE.policy||{};$('dirBar').innerHTML=d.length?`<span>Standing directive:</span><span class="pill" title="${esc(p.text||'')}">${esc(p.text||p.readback||'')}</span><span class="readback" title="What your words mean: the priorities the ship will apply">→ ${esc(p.readback||'')}</span><a href="#reprioritize">Edit</a>`:`<span>No standing directive — ranked by ${esc(LIVE.prioritization?.label||'the ship scoring model')}</span><a href="#reprioritize">Set priorities →</a>`;return}
 $('dirBar').innerHTML=d.length?`<span>Directives:</span>${d.slice(0,2).map(x=>`<span class="pill" title="${esc(x.text)}">${esc(x.text)}</span>`).join('')}${d.length>2?`<span>+${d.length-2}</span>`:''}<a href="#reprioritize">Edit</a>`:`<span>Base model only</span><a href="#reprioritize">Set priorities →</a>`;
}
function renderStats(){
 renderMetadataMode();
 let metadata=0,total=0,p1=0,allP1=0,unrev=0,sending=0,done=0,queued=0,held=0,kept=0,humanSentCount=0;
 for(const it of items){
  if(it.tier===1)allP1++;
  if(state.src!=='ALL'&&it.src.id!==state.src)continue;
  total++;if(it.tier===1)p1++;if(!isEmptyWater(it)&&!it.reviewed&&!it.dismissed)unrev++;if(humanSent(it))humanSentCount++;
  if(!it.dismissed){const u=uploadState(it,false);if(u.done)done++;if(u.queued&&!u.metadataOnly)queued++;if(u.metadataOnly)metadata++;if(u.held)held++;if(u.background)kept++;if(u.sending)sending++}
 }
 const summary=$('uploadSummary');if(summary)summary.textContent=`Shore uploads: ${sending} uploading · ${queued} queued · ${held} held · ${done} uploaded${metadata?` · ${metadata} metadata only`:''}${kept?` · ${kept} kept on ship`:''}`;
 $('hsImgs').textContent=total;$('rvSentN').textContent=humanSentCount;$('hsP1').textContent=p1;$('hsCdr').textContent=cdr;
 $('rvCount').textContent=unrev||'';if(state.page==='rv')rvProgress();
 if(LIVE){renderLiveStrip();if(state.page==='details')renderDetails();return}
 renderSimLink();
 $('cCap').textContent=stats.cap.toLocaleString();$('cKeep').textContent=stats.keep;$('cDrop').textContent=stats.drop.toLocaleString();
 $('cKB').textContent=(stats.bytes/1048576).toFixed(1);$('cDb').textContent=stats.db.toLocaleString();$('cP1').textContent=allP1;
 $('cModel').textContent=(MODELS[state.model]?.name||'').replace(' (default)','')+(state.directives.length?` + ${state.directives.length} directive${state.directives.length>1?'s':''}`:'');
 if(state.page==='details')renderDetails();
}

/* ---- details screen: low-level counts and automatically filtered photos ---- */
const DETAIL_KINDS=[['duplicate','Repeats of earlier images'],['low-value','Low value'],['directive','Lowered by directive']];
state.detKind='duplicate';
function filteredKind(it){return it.dismissed||it.manual||it.sent?'':it.filtered||(filteredReason(it)?'directive':'')}
function renderDetails(){
 const el=$('detailsBody');if(!el)return;
 const vis=items.filter(i=>state.src==='ALL'||i.src.id===state.src),groups={duplicate:[],'low-value':[],directive:[]};
 vis.forEach(i=>groups[filteredKind(i)]?.push(i));
 const n=v=>Number(v).toLocaleString();
 const counts=LIVE?`<div class="strip det-strip">${$('liveStrip').innerHTML}</div>`:`<table class="det-counts">${[
  ['Frames captured by sensors',n(stats.cap)],['Useful images kept',n(stats.keep)],['Frames filtered on ship (cumulative, including unretained frames)',n(stats.drop)],
  ['Share of captured frames filtered on ship (cumulative)',Math.round(stats.drop/Math.max(1,stats.cap)*100)+'%'],['Image data sent to shore',(stats.bytes/1048576).toFixed(1)+' MB'],['Records in imagery database',n(stats.db+180000)],
  ['Images in this feed',n(vis.length)],['Sent to Commander by a person',n(vis.filter(i=>i.sent&&!i.autoSent).length)],['Sent to Commander automatically',n(vis.filter(i=>i.sent&&i.autoSent).length)]
 ].map(([k,v])=>`<tr><td>${k}</td><td class="n">${v}</td></tr>`).join('')}</table>`;
 const list=groups[state.detKind].sort((a,b)=>b.ts-a.ts).slice(0,60);
 el.innerHTML=`<section class="panel"><h3>Pipeline counts</h3>${counts}</section>
 <section class="panel"><h3>Photos filtered automatically</h3><p class="hint">Current feed only. Cumulative counts include frames without retained images and images no longer in this feed. Up to 60 photos shown per category. Select a photo to open it.</p>
 <div class="seg" id="detKind">${DETAIL_KINDS.map(([k,l])=>`<button data-k="${k}" class="${state.detKind===k?'on':''}">${l} (${groups[k].length})</button>`).join('')}</div>
 <div class="det-grid">${list.map(it=>{const orig=it.dupOf&&findItem(it.dupOf);return`<button class="det-item" data-id="${esc(it.id)}">${orig?`<span class="det-pair"><img src="${esc(imageFor(orig))}" alt=""><small>Original · P${orig.tier} ${TNAME[orig.tier]}</small></span>`:''}<span class="det-pair"><img src="${esc(imageFor(it))}" alt=""><small>${esc(filteredReason(it))} · P4 Low</small></span><span class="det-meta">${esc(it.src.id)} · ${zulu(it.ts).slice(0,5)}Z</span></button>`}).join('')||'<p class="hint">None right now.</p>'}</div></section>`;
 el.onclick=e=>{const k=e.target.closest('[data-k]');if(k){state.detKind=k.dataset.k;renderDetails();return}const b=e.target.closest('.det-item');if(b){const id=b.dataset.id;location.hash='#feed';setTimeout(()=>openDetail(id),60)}};
}

/* ---- live shore strip + cameras ---- */
function renderLiveStrip(){
 const L=LIVE;if(!L)return;const sim=$('simStrip'),el=$('liveStrip');if(sim)sim.hidden=true;if(!el)return;el.hidden=false;
 const {shore,edge,link,counts,policy,prioritization}=L;
 const shoreDown=!link.controllable&&!edge.ok&&shore.linkUp===false;
 const rate=link.controllable?link.bps:shoreDown?0:(edge.ok?edge.estimateBps:shore.estimateBps)||0;
 const d=link.distribution;
 const measuring=!link.controllable&&!rate&&!shoreDown;
 const linkNote=d?'':measuring?'Measuring link speed…':'Measured by the ship';
 const linkTitle=d?`Simulated bandwidth follows a normal distribution: mean ${fmtRate(d.meanBps)}, standard deviation ${fmtRate(d.stdDevBps)}, clipped to ${fmtRate(d.minBps)}–${fmtRate(d.maxBps)}. A new rate is applied to the actual ship-to-shore link every ${d.intervalMs/1000} seconds.`:'Ship-to-shore speed is controlled outside this console.';
 const sightingMode=counts.grouped>0;
 const objsShown=sightingMode?counts.sightings:counts.objects,objsImg=sightingMode?counts.sightingsWithImage:counts.objectsWithImage;
 const preTrack=sightingMode?Math.max(0,counts.objects-counts.sightings):0;
 const dropPct=edge.ok&&(edge.itemsTotal+edge.itemsDeduped)>0?Math.round(100*edge.itemsDeduped/(edge.itemsTotal+edge.itemsDeduped)):0;
 el.innerHTML=`
  <div class="st link${rate>0||measuring?'':' down'}" title="${esc(linkTitle)}">${linkPanelHTML(rate,linkNote,measuring)}</div><span class="ar">›</span>
  <div class="st lowlevel" title="Ship records include detected objects and camera overview updates. Repeated views of an already reported object stay on the ship to save bandwidth. Waiting records and bytes have not yet been sent.">On ship ${edge.ok?`<b>${edge.itemsTotal}</b> records · <b>${edge.itemsDeduped||0}</b> repeated views kept on ship · waiting <b>${edge.queuedItems}</b> (${fmtBytes(edge.queuedBytes||0)})`:`<b>${edge.error?'n/a':'—'}</b>`}</div><span class="ar">›</span>
  <div class="st lowlevel" title="Records received on shore include object summaries and camera overview updates. Each tracked object has one card. Cards without an image show only a summary until image data arrives.${preTrack?` ${preTrack} earlier record${preTrack===1?'':'s'} of the same objects are combined.`:''}">On shore <b>${shore.ok?shore.itemsTotal:'—'}</b> records · <b>${objsShown}</b> detected objects (<b>${objsImg}</b> with image) · <b>${counts.scenes||0}</b> camera updates${shore.ok?'':` <span class="bad">${esc(shore.error||'unreachable')}</span>`}</div><span class="ar">›</span>
  <div class="st" title="Image and summary data sent over the connection${link.controllable?` (shore itself counted ${fmtBytes(shore.bytes||0)})`:''}. Compare with what ${edge.itemsDeduped||0} repeated views would have cost to send.">Data transferred <b>${fmtBytes(link.controllable?link.bytes:shore.bytes||0)}</b></div><span class="ar">›</span>
  <div class="st" title="${esc(policy.text?`Operator wrote: “${policy.text}”. The shore read it back as: ${policy.readback}`:policy.readback||'')}">Directive <b class="rb">${esc(policy.text?policy.readback:'none')}</b>${policy.emcon&&policy.emcon.mode&&policy.emcon.mode!=='NORMAL'?` · Transmission mode <b>${esc(policy.emcon.mode)}</b>`:''}</div><span class="ar">›</span>
  <div class="st" id="prioMode" title="Scoring model used on the ship. P1 Critical, P2 High, P3 Routine and P4 Low divide the current shore feed into priority groups (top 10%, next 25%, next 35%, then the rest). Deprioritized objects are P4 Low.">Scoring model <b>${esc(prioritization?.label||'Ship score')}</b>${prioritization?.source==='edge'?'':' <span class="hint">(built-in)</span>'}</div>
  <span class="sv lowlevel" title="Share of all camera views that stayed on the ship because they repeated an object already reported. This saves bandwidth.">${dropPct}% of camera views stayed on the ship (repeats) to save bandwidth</span><a class="btn sm details-link" href="#details">Details: counts &amp; filtered photos ›</a>`;
}
function linkPanelHTML(bps,note,measuring){
 const hint=bps>0||measuring?note:'Link down — sending paused';
 return`<span class="lk">Ship-to-shore connection</span> <b>${measuring?'—':fmtRate(bps)}</b>${hint?`<span class="hint">${esc(hint)}</span>`:''}`;
}
function renderSimLink(){
 const el=$('stLink');if(!el)return;const bps=Number(stats.linkBps)||0;
 el.classList.toggle('down',bps<=0);el.innerHTML=linkPanelHTML(bps,'');
}
function metaOnlyChip(data,bps){if(!data?.metaOnly)return'';const reason=data.metaOnlyReason==='emcon'?'EMCON burst':`link ${Math.round((bps||data.linkBps||0)/1000)} kbps`;return`<span class="meta-only-chip" title="Link below 8 kbps for 2 min — only metadata is sent; images resume after 2 min above 16 kbps">Metadata only · ${reason}</span>`}
function renderMetadataMode(){
 let root=document.querySelector('header .metadata-mode');
 if(!root){const anchor=document.querySelector('header .hstats');if(!anchor)return;anchor.insertAdjacentHTML('beforebegin','<span class="metadata-mode"></span>');root=document.querySelector('header .metadata-mode')}
 const data=LIVE?LIVE.edge:stats,rate=LIVE?(LIVE.link?.controllable?LIVE.link.bps:LIVE.edge?.estimateBps||LIVE.shore?.estimateBps):stats.linkBps;
 root.innerHTML=metaOnlyChip(data,rate);root.hidden=!data?.metaOnly;
}
const pulling=new Set();
function renderCameras(){
 const el=$('cameras');if(!el)return;const cams=LIVE?.cameras||[];el.hidden=!LIVE||!cams.length;if(el.hidden){el.innerHTML='';return}
 el.innerHTML=`<div class="camhead" title="One view per ship camera. Camera overview updates show scenes without detected objects (about 2 KB per minute). Full images stay on the ship until requested. Until an overview arrives, the latest detected object is shown instead. Overview updates do not enter the object review queue.">Cameras · ${cams.length} · overview updates</div>`+cams.map(c=>{const v=c.view,p=v?.pull;const busy=v&&pulling.has(v.itemId);
  return`<div class="cam ${c.heartbeat?'hb':'contact'}" title="${c.heartbeat?'Latest camera overview with no detected objects. Not included in the review queue.':'No camera overview yet — showing this camera’s latest detected object.'}">${v?.image?`<img src="${esc(v.image)}" alt="">`:'<div class="noimg">no view yet</div>'}<div class="meta"><b>${esc(c.name||c.source)}</b><span>seen ${ago(c.lastMs)} · ${c.contacts} detected object${c.contacts===1?'':'s'}${v?` · view: ${esc(FORM_NAME[v.imageForm||'fact'])} ${ago(v.lastMs)}`:''}</span>${p?`<button class="btn sm" data-pull="${esc(v.itemId)}" ${p.enabled&&!busy?'':'disabled'} title="${esc(p.reason)}">${busy?'requested…':esc(p.label)}</button>`:''}</div></div>`}).join('');
 el.onclick=async e=>{const b=e.target.closest('button[data-pull]');if(!b||b.disabled)return;const id=b.dataset.pull;pulling.add(id);b.disabled=true;b.textContent='requested…';try{await request('/api/shore/pull','POST',{itemId:id,form:'full'});toast('Full image requested from the ship — its status changes from waiting to received')}catch(error){pulling.delete(id);toast(error.message)}};
}
function liveLadderHTML(it){
 const l=it.live;if(!l)return'';const p=l.pull||{};
 const rows=FORMS.map(k=>{const f=l.forms[k]||{state:'none'};const st=f.state==='received'?`received · ${fmtBytes(f.bytes)}`:f.state==='partial'?`${fmtBytes(f.received)} of ${fmtBytes(f.bytes)} streaming`:f.state==='pending'?`waiting on the ship · ${fmtBytes(f.bytes)}`:f.state==='offered'?`on the ship, not sent · ${fmtBytes(f.bytes)}`:'not available';
  return`<tr class="${esc(f.state)}${l.imageForm===k?' shown':''}"><td>${FORM_NAME[k]}${l.imageForm===k?' <small>· shown</small>':''}</td><td>${esc(st)}</td></tr>`}).join('');
 return`<div class="dsec live-ladder"><h3>Image versions · shore record ${esc(l.itemID||l.itemId||'')}</h3>
  <div class="hint" style="margin-bottom:6px">Image shown: <b>${l.imageForm?FORM_NAME[l.imageForm]:'none — summary only'}</b>${l.sightingId?` · Tracked object <span class="mono">${esc(l.sightingId)}</span>`:' · object not yet tracked'}${l.framesKnown?` · ${l.frames} frames seen on the ship, ${l.reports} report${l.reports===1?'':'s'} sent`:` · ${l.reports} report${l.reports===1?'':'s'} so far`}${l.event?` · ${esc(l.event)}`:''}</div>
  <table class="ladder">${rows}</table>
  <div class="pullrow"><button class="btn ${p.enabled?'primary':''}" id="aPull" ${p.enabled&&!pulling.has(l.itemId)?'':'disabled'} title="${esc(p.reason||'')}">${pulling.has(l.itemId)?'requested…':esc(p.label||'Request full image')}</button><span class="hint">${esc(p.reason||'')}</span></div>
 </div>`;
}
function liveWhy(it){
 const l=it.live,why=[];if(!l)return why;
 const rank=items.filter(x=>x.live&&!x.live.suppressed).sort((a,b)=>b.live.score-a.live.score);const pos=rank.findIndex(x=>x.id===it.id);
 if(l.suppressed)why.push({c:'dn',t:liveSuppressReason(it)});
 else if(pos>=0)why.push({c:it.tier===1?'al':'',t:`Rank ${pos+1} of ${rank.length} on shore by ship score (${l.score.toFixed(2)}) → P${it.centralTier} ${TNAME[it.centralTier]}`});
 if(l.unknown)why.push({c:'al',t:`The ship marked this object <b>unknown</b> (unfamiliarity score ${l.unknownness.toFixed(2)}) — unknown objects are never deprioritized by a directive`});
 if(l.subtype)why.push({c:'',t:`Suggested object type <b>${esc(l.subtype)}</b>${l.label&&l.label!==l.subtype?` (computer label: ${esc(l.label)})`:''}`});
 if(l.override)why.push({c:'up',t:`Operator changed the label of this object “${esc(l.override)}” on shore`});
 if(it.det[0]?.matched)why.push({c:'up',t:'Boosted by the standing directive'});
 if(!l.imageForm)why.push({c:'',t:'Only the object summary has arrived — its image appears when the connection delivers a preview or close-up'});
 return why;
}

/* ---- detail drawer ---- */
function selectFeedItem(id){
 if(state.sel)dirtyItems.add(state.sel);
 state.sel=id;if(id)dirtyItems.add(id);
 if(id&&revealFeedItem(id))renderFeed(true);else scheduleFeedFlush(true);
}
function openDetail(id){const it=findItem(id);if(!it){closeDetail();toast('This image is no longer in the feed.');return}document.body.classList.add('drawer-open');selectFeedItem(id);renderDetail();$('drawer').scrollTop=0;it.el&&it.el.scrollIntoView({block:'nearest',behavior:'smooth'})}
function openZoom(){document.activeElement?.blur();state.zoom=true;renderZoom()}
function closeZoom(){state.zoom=false;const z=$('zoom');z.hidden=true;z.innerHTML=''}
function renderZoom(){preserveBandFocus(renderZoomContent)}
function renderZoomContent(){const it=findItem(state.sel);if(!it){closeZoom();return}const z=$('zoom');z.hidden=false;
 z.innerHTML=`<div class="zbar"><span class="tier t${it.tier}" title="${it.tier===1?'Notify Commander':'Image priority'}">P${it.tier} ${TNAME[it.tier]}</span><b>${esc(it.src.name)}</b><span class="mono">${zulu(it.ts)}</span><span>Stored image #${it.dbId}</span><span class="zk">← → step · 1–4 set priority · Esc / click to close</span></div><div class="zframe">${largeImage(it)}</div>`;z.onclick=closeZoom}
function closeDetail(){closeZoom();selectFeedItem(null);document.body.classList.remove('drawer-open')}
function step(dir){const l=feedOrdered;const i=l.findIndex(x=>x.id===state.sel);const n=l[Math.max(0,Math.min(l.length-1,i+dir))];if(n)openDetail(n.id)}
function metadataHTML(record){
 if(!record||!Object.keys(record).length)return'<p class="hint">Metadata unavailable.</p>';
 const c=record.capture||{},m=record.model||{},s=record.scoring||{},o=record.operator||{},t=record.transfer||{};
 const synthetic=[...(record.synthetic||[]),...(c.synthetic||[])];
 const marked=keys=>keys.some(key=>synthetic.some(k=>k===key||k===key.split('.').pop()));
 const row=(label,value,keys=[],title='')=>value===undefined||value===null||value===''?'':`<tr><th>${esc(label)}</th><td${title?` title="${esc(title)}"`:''}>${esc(value)}${marked(keys)?'<sup class="synthetic-marker" title="Synthetic value">sim</sup>':''}</td></tr>`;
 const group=(name,rows)=>rows?`<section class="metadata-group"><h5>${name}</h5><table>${rows}</table></section>`:'';
 const number=(v,d=2)=>typeof v==='number'&&Number.isFinite(v)?v.toFixed(d):'';
 const name=v=>String(v||'').replace(/_/g,' ').replace(/^\w/,x=>x.toUpperCase());
 const sensor={...(SRC[c.source]||{}),...(c.sensor||{})};
 const position=c.lat!==undefined&&c.lon!==undefined?`${number(c.lat,4)}, ${number(c.lon,4)}${c.headingDeg!==undefined?` · ${number(c.headingDeg,0)}°`:''}`:'';
 const hash=c.contentHash||'';
 const image=[c.imageSize>0?`${number(c.imageSize/(1024*1024))} MB`:'',c.widthPx>0&&c.heightPx>0?`${c.widthPx} × ${c.heightPx}`:''].filter(Boolean).join(' · ');
 const capture=row('Time (UTC Z)',c.time)+row('Sensor',[sensor.name||c.source,sensor.hardware||sensor.hw,sensor.emcon].filter(Boolean).join(' · '))+row('Position',position,['capture.lat','capture.lon','capture.headingDeg'])+row('Altitude',c.altM?`${number(c.altM,0)} m`:'',['capture.altM'])+row('Frame / record',[c.frame?`Frame ${c.frame}`:'',c.record?`#${c.record}`:''].filter(Boolean).join(' / '))+row('Content hash',hash.length>24?hash.slice(0,24)+'…':hash,['capture.contentHash'],hash)+row('Image size',image,['capture.widthPx','capture.heightPx'])+row('Field of view',c.fovDeg?`${number(c.fovDeg,0)}°`:'',['capture.fovDeg']);
 const dets=m.detections||[];
 const detections=dets.map((d,n)=>{const b=d.box||{},box=['x','y','w','h'].every(k=>typeof b[k]==='number')?` · box ${['x','y','w','h'].map(k=>Math.round(b[k])).join(',')}`:'';return row(`Detection ${n+1}`,`${clsOf(d.class).lab} · conf ${number(d.confidence)}${box}`)}).join('');
 const ships=dets.map(d=>d.shipClass).filter(Boolean);
 const shipText=ships.map(ship=>[ship.label||name(ship.type),ship.aspect,ship.size,ship.markings?`markings ${ship.markings}`:'',(ship.features||[]).join(', '),ship.model].filter(Boolean).join(' · ')).join('; ');
 const behavior=[...new Set(dets.flatMap(d=>Object.entries(d.attrs||{}).filter(([,v])=>v===true).map(([key])=>ATTR_LAB[key]||name(key))))].join(' · ');
 const lights=dets.map(d=>d.signal?[d.signal.line||d.signal.meaning,d.signal.ref,d.signal.aspect].filter(Boolean).join(' · '):(d.lights||[]).map(light=>`${name(light.colour)}${light.flashing?' flashing':''} · conf ${number(light.conf)}`).join(', ')).filter(Boolean).join('; ');
 const model=row('Model',m.name||m.id)+detections+row('Subtype (zero-shot)',m.subtype?`${name(m.subtype)}${m.subtypeConf?` · ${number(m.subtypeConf)}`:''}`:'',['model.subtype','model.subtypeConf'])+row('Ship class',shipText)+row('Behavior',behavior||'None')+row('Night lights / COLREGS',lights||'None')+row('Unknownness',number(m.unknownness),['model.unknownness'])+row('Tracked object',m.trackId?`${m.trackId}${m.trackFrames?` · ${m.trackFrames} frames`:''}`:'',['model.trackId','model.trackFrames']);
 const scoring=row('Ship score',number(s.edge,3))+row('Shore score',number(s.central,3))+row('Tier',s.tier?`P${s.tier} · ${TNAME[s.tier]||''}`:'')+row('Top reasons',(s.topReasons||[]).map(v=>ATTR_LAB[v]||clsOf(v).lab||name(v)).join(' · ')||'None')+row('Filter',s.filtered?`${name(s.filtered)}${s.dupOf?` · repeat of ${s.dupOf}`:''}`:'None');
 const operator=row('Label override',o.labelOverride?name(o.labelOverride):'None')+row('Manual tier',o.manualTier?`P${o.manualTier} · ${TNAME[o.manualTier]||''}`:'None (model priority)')+row('Reviewed',o.humanReviewed?'Human reviewed':o.reviewed?'Reviewed':'Not reviewed')+row('Commander decision',o.decision?name(o.decision):'Pending');
 const transfer=(label,value)=>{if(!value)return'';const done=value.done||value.state==='received',bytes=value.bytes||0,sent=value.sent??value.received??0;return row(label,[done?'on shore':value.state||'queued',bytes?fmtBytes(bytes):'',done&&value.doneAt?String(value.doneAt).replace(/^.*T/,'').replace(/\.\d+Z$/,'Z'):!done&&bytes?`${Math.round(100*sent/bytes)}%`:''].filter(Boolean).join(' · '))};
 return group('Capture',capture)+group('Model classification',model)+group('Scoring',scoring)+group('Operator',operator)+group('Transfer',transfer('Metadata',t.metadata)+transfer('Image',t.image));
}
function refreshMetadataEMCON(mode){
 if(!mode||mode===metadataEMCON)return;
 metadataEMCON=mode;
 for(const [id,cached] of metadataCache)metadataCache.set(id,{data:cached.data,stale:true});
 const it=findItem(state.sel);if(it)loadMetadata(it,$('metadataPanel'));
}
function metadataVersion(it){
 const version={};
 for(const key of ['src','ts','frame','dbId','capture','dets','det','edge','central','tier','manual','reviewed','humanReviewed','decision','filtered','dupOf'])version[key]=it[key];
 if(it.live){
  const {itemId,subtype,label,override,unknownness,imageForm}=it.live;
  const forms=Object.fromEntries(Object.entries(it.live.forms||{}).map(([key,{state,bytes}])=>[key,{state,bytes}]));
  version.live={itemId,subtype,label,override,unknownness,imageForm,forms};
 }else{
  version.kb=it.kb;
  const {metadataBytes,metadataDone,metadataDoneAt,bytes,done}=it.upload||{};
  version.upload={metadataBytes,metadataDone,metadataDoneAt,bytes,done};
 }
 return JSON.stringify(version);
}
function invalidateMetadata(it,previous){
 const cached=metadataCache.get(it.id);
 if(cached&&(!previous||metadataVersion(it)!==metadataVersion(previous)))metadataCache.set(it.id,{data:cached.data,stale:true});
}
async function loadMetadata(it,panel){
 if(!panel||!panel.open)return;
 const cached=metadataCache.get(it.id);
 if(cached?.data){const body=panel.querySelector('.metadata-content');if(body)body.innerHTML=metadataHTML(cached.data);if(!cached.stale)return}
 if(cached?.loading)return;
 const entry={data:cached?.data,loading:true};metadataCache.set(it.id,entry);
 const body=panel.querySelector('.metadata-content');if(body&&!entry.data)body.innerHTML='<p class="hint">Loading metadata…</p>';
 const currentPanel=()=>state.sel===it.id?$('metadataPanel'):panel.isConnected?panel:null;
 try{const data=await request(`/api/items/${encodeURIComponent(it.id)}/metadata`);if(metadataCache.get(it.id)!==entry)return;metadataCache.set(it.id,{data});const current=currentPanel();if(current?.isConnected&&current.open){const target=current.querySelector('.metadata-content');if(target)target.innerHTML=metadataHTML(data)}}catch(error){if(metadataCache.get(it.id)!==entry)return;if(entry.data)metadataCache.set(it.id,{data:entry.data,stale:true});else metadataCache.delete(it.id);const current=currentPanel();if(current?.isConnected){const target=current.querySelector('.metadata-content');if(target)target.innerHTML=`${entry.data?metadataHTML(entry.data):''}<p class="hint">${esc(error.message)}</p>`}}
}
function metadataPanelHTML(it){
 const cached=metadataCache.get(it.id),size=cached?.data?.transfer?.metadata?.bytes||it.upload?.metadataBytes||it.live?.forms?.fact?.bytes||1024;
 return`<details class="metadata-panel" id="metadataPanel" ${metadataExpanded?'open':''}><summary data-meta-summary><span class="metadata-caret">${metadataExpanded?'▾':'▸'}</span> Metadata · ${fmtBytes(size)}</summary><div class="metadata-content">${cached?.data?metadataHTML(cached.data):'<p class="hint">Open to load metadata.</p>'}</div><button type="button" class="btn sm metadata-copy" id="metadataCopy">Copy JSON</button></details>`;
}
function bindMetadataPanel(it){
 const panel=$('metadataPanel');if(!panel)return;
 panel.addEventListener?.('toggle',()=>{if(!panel.isConnected)return;metadataExpanded=panel.open;const caret=panel.querySelector('.metadata-caret');if(caret)caret.textContent=panel.open?'▾':'▸';if(panel.open)loadMetadata(it,panel)});
 $('metadataCopy')?.addEventListener?.('click',async e=>{e.stopPropagation();const data=metadataCache.get(it.id)?.data;if(!data)return;try{await navigator.clipboard.writeText(JSON.stringify(data,null,2));toast('Metadata JSON copied')}catch{toast('Could not copy metadata JSON')}});
 if(panel.open)loadMetadata(it,panel);
}
function buildWhy(it,rows){
 const why=[...liveWhy(it),...gsLights.why(it)];const top=rows[0];
 rows.filter(([d])=>d.cls==='unknown').forEach(([d])=>why.push({c:d.attrs.transient?'dn':'al',t:d.attrs.transient?'Single-frame flicker, treated as noise':'Unclassified contact persisted across frames — not noise; prioritized as potentially important'+(d.attrs.approaching?' · approaching, urgent':d.attrs.dark?' · no AIS / transponder correlation, urgent':'')}));
 if(top)why.push({c:top[1].s>.6?'al':'',t:`Main object: <b>${esc(clsOf(top[0].cls).lab)}</b> (confidence ${top[0].conf.toFixed(2)}) contributes ${top[1].s.toFixed(2)} under ${esc(MODELS[state.model].name)}`});
 rows.forEach(([d])=>{if(d.attrs.dark&&!['other','clutter'].includes(clsOf(d.cls).g))why.push({c:'al',t:`${esc(clsOf(d.cls).lab)} has no AIS / transponder correlation`});if(d.attrs.approaching)why.push({c:'al',t:`${esc(clsOf(d.cls).lab)} track is inbound toward the sensor`});if(d.attrs.new)why.push({c:'',t:`Change detection: ${esc(clsOf(d.cls).lab)} not present in previous pass`})});
 const seen=new Set();rows.forEach(([d,sd])=>sd.notes.forEach(r=>{const k=ruleLabel(r);if(seen.has(k))return;seen.add(k);const dir=state.directives.find(x=>x.id===r.directiveId);why.push({c:r.type==='boost'?'up':'dn',t:`${r.type==='boost'?'Boosted':'Suppressed'} by directive “${dir?esc(dir.text):''}” (${k.slice(2)})`})}));
 if(rows.some(([,sd])=>sd.focus))why.push({c:'dn',t:'Objects outside your active directives receive lower scores (focus mode)'});
 if(!it.live&&it.edge<it.central-.15)why.push({c:'up',t:`${DATASET_MODE?'Model scoring raised the priority estimate':'Shore scoring raised the ship’s priority estimate'} (${it.edge.toFixed(2)} → ${it.central.toFixed(2)})`});
 const _w=new Set();for(let i=why.length-1;i>=0;i--){if(_w.has(why[i].t))why.splice(i,1);else _w.add(why[i].t)}
 return why;
}
function processingHTML(it){
 if(DATASET_MODE)return `${detectorNegativeHTML(it)}<div class="dsec"><h3>Processing</h3><ul class="tl">
  <li class="edge"><i></i><div>Clean v2 dev image<br><small>Real detector output · score ${it.edge.toFixed(2)} ${it.detectorNegative?'<':'≥'} 0.20</small></div></li>
  <li><i></i><div>Loaded for Operator Review<br><small>${esc(it.dataset_id)} · ${esc(it.image_id)}</small></div></li>
  <li><i></i><div>Priority scoring<br><small>${esc(MODELS[state.model].name)}${state.directives.length?` + ${state.directives.length} directive${state.directives.length>1?'s':''}`:''}</small></div></li>
 </ul></div>`;
 return `<div class="dsec"><h3>Processing chain</h3><ul class="tl">
  <li class="edge"><i></i><div>Captured &amp; filtered on the ship<br><small>${esc(it.src.hw)} · ${esc(it.src.em)} · score ${it.edge.toFixed(2)} ≥ 0.20 → keep</small></div><span class="ms">${fmtMs(it.lat.edge)}</span></li>
  <li><i></i><div>Sent to shore<br><small>${esc(it.src.link)} · ${it.fullRes?`original, full size, no compression · operator override by ${esc(it.fullResBy||'')}`:`${it.kb} KB JPEG + metadata`}</small></div><span class="ms">${fmtMs(it.lat.up)}</span></li>
  <li><i></i><div>Stored in imagery database<br><small>record ${it.dbId} · frame ${it.frame}</small></div><span class="ms">12 ms</span></li>
  <li><i></i><div>Shore priority scoring<br><small>${esc(MODELS[state.model].name)}${state.directives.length?` + ${state.directives.length} directive${state.directives.length>1?'s':''}`:''}</small></div><span class="ms">${fmtMs(it.lat.cen)}</span></li>
 </ul></div>`;
}
function captureHTML(it){
 const c=it.capture;if(!c)return '';
 const keys=['lat','lon','headingDeg','widthPx','heightPx','contentHash','subtype','subtypeConf','unknownness','trackId','trackFrames',...['altM','fovDeg'].filter(k=>c[k]!=null)];
 const synthetic=new Set(Array.isArray(c.synthetic)?c.synthetic:[]),all=keys.every(k=>synthetic.has(k));
 const tag='<small class="capture-synthetic" title="This value is simulated, not measured by a sensor.">synthetic</small>';
 const value=(key,text,title)=>`<span${title==null?'':` title="${esc(title)}"`}>${esc(text??'—')}</span>${!all&&synthetic.has(key)?tag:''}`;
 const num=(key,dp=0)=>Number.isFinite(c[key])?c[key].toFixed(dp):c[key]??'—';
 const pct=key=>Number.isFinite(c[key])?`${(c[key]*100).toFixed(0)}%`:c[key]??'—';
 const row=(label,body)=>`<dt>${esc(label)}</dt><dd>${body}</dd>`;
 return `<section class="dsec capture"><h3>Capture</h3>${all?'<p class="capture-note" title="These values are simulated, not measured by a sensor.">All capture values shown are synthetic.</p>':''}<dl class="capture-grid">${
  row('Position',`${value('lat',num('lat',5))}, ${value('lon',num('lon',5))} · ${value('headingDeg',`${num('headingDeg',1)}°`)}`)+
  (c.altM!=null?row('Altitude',value('altM',`${num('altM',1)} m`)):'')+
  (c.fovDeg!=null?row('Field of view',value('fovDeg',`${num('fovDeg',1)}°`)):'')+
  row('Resolution',`${value('widthPx',c.widthPx)} × ${value('heightPx',c.heightPx)} px`)+
  row('Content hash',value('contentHash',String(c.contentHash??'').slice(0,12),c.contentHash))+
  row('Subtype',`${value('subtype',c.subtype)} · ${value('subtypeConf',pct('subtypeConf'))}`)+
  row('Unknownness',value('unknownness',pct('unknownness')))+
  row('Track',`${value('trackId',c.trackId)} · ${value('trackFrames',c.trackFrames)} frames`)
 }</dl></section>`;
}
function renderDetail(){preserveBandFocus(renderDetailContent)}
function renderDetailContent(){
 const it=findItem(state.sel);const D=$('drawer');
 if(!it){D.innerHTML='';closeDetail();return}
	if(isScene(it)){
	 D.innerHTML=`<div class="dhead"><div><h2><span class="tier t4">P4 Low</span> Empty water · no contacts</h2><small>${esc(it.src.name)} · ${zulu(it.ts)}</small></div><button id="dClose" class="btn" aria-label="Close image details">×</button></div><div class="dimg" id="dImg">${largeImage(it)}</div><div class="dbody">${captureHTML(it)}<p>Object-free camera overview. No contact review, alerts or escalation.</p>${uploadHTML(it)}${formChipsHTML(it)}${uploadState(it).background?'<p class="hint">FULL stays on ship until explicitly pulled from Cameras.</p>':''}${metadataPanelHTML(it)}</div>`;
	 $('dClose').onclick=closeDetail;$('dImg').onclick=openZoom;bindMetadataPanel(it);return;
	}
 const rows=it.dets.map((d,i)=>[d,it.det[i]]).sort((a,b)=>b[1].s-a[1].s);
 const why=buildWhy(it,rows);
 const dl=it.central-it.base;
 const l=feedOrdered,idx=l.findIndex(x=>x.id===it.id);
 D.innerHTML=`<div class="dhead"><div><h2><span class="tier t${it.tier}" title="${it.tier===1?'Notify Commander':'Image priority'}">P${it.tier} ${TNAME[it.tier]}</span></h2><small>${esc(it.src.name)} · <span class="mono" title="UTC time (Zulu)">${zulu(it.ts)}</span> · Stored image #${it.dbId}${idx>=0?` · ${idx+1} of ${l.length}`:''}</small></div>
  <div class="dnav"><button id="dPrev" title="Previous (←)" aria-label="Previous image">‹</button><button id="dNext" title="Next (→)" aria-label="Next image">›</button><button id="dClose" title="Close (Esc)" aria-label="Close image details">×</button></div></div>
 <div class="dimg" id="dImg" title="Enlarge (F)">${largeImage(it)}<span class="zhint">⤢ Enlarge · F</span></div>
 <div class="dbody">
  ${captureHTML(it)}
  <div class="lbl" style="margin-bottom:6px">Set priority · keys 1–4${it.manual?' · <span style="color:var(--accent);text-transform:none;letter-spacing:0">operator override</span>':''}</div><div class="tierbtns">${[1,2,3,4].map(t=>`<button class="btn tb${t} ${it.tier===t?'on':''}" data-t="${t}">P${t} ${TNAME[t]}</button>`).join('')}<button class="btn" id="dReset" title="Reset to model" aria-label="Reset priority to model" ${it.manual?'':'disabled style="opacity:.4"'}>↺</button></div>
  <div class="scores"><div class="sc" title="${DATASET_MODE?'Detector triage score from real detections':it.live?'Ship score: importance relative to the highest score on shore':'Edge score: the ship’s initial estimate of importance'}"><span>${DATASET_MODE?'Detector score':'Ship score'}</span><b>${it.edge.toFixed(2)}</b></div><div class="sc" title="${DATASET_MODE?'Command-model score after active directives':it.live?'Shore score: relative importance after applying your directive':'Command score: importance after shore scoring and directives'}"><span>${DATASET_MODE?'Model score':'Shore score'}</span><b style="color:${tcol(it.tier)}">${it.central.toFixed(2)}</b></div><div class="sc"><span title="How much active directives changed the base score">Change from directives</span><b style="color:${dl>.04?'var(--accent)':dl<-.04?'#8d99a5':'var(--text)'}">${dl>=0?'+':''}${dl.toFixed(2)}</b></div></div>
  <div class="dsec"><h3>Detections</h3><table><tr><th>Object</th><th>Behavior</th><th class="n" title="Confidence: how sure the computer is about the object label">Confidence</th><th class="n">Score</th></tr>${rows.map(([d,sd])=>{const ship=shipLine(d),visible=shipVisibleLine(d);return`<tr><td>${sd.matched?'<span style="color:var(--accent)">●</span> ':sd.sup?'<span style="color:#8d99a5">○</span> ':''}${esc(clsOf(d.cls).lab)}${ship?`<small class="shipline">${ship}</small>`:''}${visible?`<small class="shipfeatures">${visible}</small>`:''}</td><td style="color:var(--muted)">${Object.keys(ATTR_LAB).filter(a=>d.attrs[a]&&!(a==='dark'&&['other','clutter'].includes(clsOf(d.cls).g))).map(a=>esc(ATTR_LAB[a])).join(', ')||'—'}</td><td class="n">${d.conf.toFixed(2)}</td><td class="n" style="color:${sd.s>=.6?'var(--p1)':sd.s>=.3?'var(--p2)':'var(--muted)'}">${sd.s.toFixed(2)}</td></tr>`}).join('')}</table></div>
  ${gsLights.section(it,CLS)}
  <div class="dsec" id="briefDetections"></div>
  <div class="dsec"><h3>Why this priority</h3><ul class="why">${why.slice(0,7).map(w=>`<li class="${w.c}">${w.t}</li>`).join('')}</ul></div>
  ${it.live?liveLadderHTML(it):DATASET_MODE?processingHTML(it):`<div class="dsec"><h3>Processing chain</h3><ul class="tl">
   <li class="edge"><i></i><div>Captured &amp; filtered on the ship<br><small>${esc(it.src.hw)} · ${esc(it.src.em)} · ${it.background?'empty water retained locally for demo · no automatic upload':`score ${it.edge.toFixed(2)} ${it.filtered==='duplicate'?'· repeat of an earlier image → kept on ship at P4 Low':it.filtered?'< 0.20 → kept on ship at P4 Low':'≥ 0.20 → keep'}`}</small></div><span class="ms">${fmtMs(it.lat.edge)}</span></li>
   <li><i></i><div>Shore transfer: ${esc(uploadState(it).label)}<br><small>${it.fullRes?`original, full size, no compression · operator override by ${esc(it.fullResBy||'')}`:`${it.kb} KB JPEG + metadata`}</small></div></li>
   <li><i></i><div>Stored on ship<br><small>record ${it.dbId} · frame ${it.frame}</small></div><span class="ms">12 ms</span></li>
   <li><i></i><div>Priority scoring (simulated)<br><small>${esc(MODELS[state.model].name)}${state.directives.length?` + ${state.directives.length} directive${state.directives.length>1?'s':''}`:''}</small></div><span class="ms">${fmtMs(it.lat.cen)}</span></li>
  </ul></div>`}
  ${DATASET_MODE?'':uploadControlsHTML(it)}
  <div class="actions"><button class="btn cdr ${it.sent?'done':''}${it.sent&&!it.autoSent?' human':''}" id="aCdr">${it.sent?it.autoSent?'✓ Auto-sent to Commander':'➤ Sent to Commander by operator':'Send to Commander'}</button><button class="btn ${it.fullRes?'done':''}" id="aFull" title="Operator override: send the original image at full size, no compression">${it.fullRes?'✓ Full size requested':'Send full size'}</button><button class="btn" id="aSim">Prioritize similar</button><button class="btn" id="aDis" title="Hide this image from the feed and mark it reviewed. The image remains stored.">Not relevant</button></div>
  ${metadataPanelHTML(it)}
  <div class="kbd"><kbd>←</kbd> <kbd>→</kbd> step through feed · <kbd>Esc</kbd> close</div>
 </div>`;
 D.querySelectorAll('.tierbtns [data-t]').forEach(b=>b.onclick=()=>setManual(it.id,+b.dataset.t));$('dReset').onclick=async()=>{try{await patchItem(it.id,{manual:0,reviewed:false})}catch(error){toast(error.message)}};$('dPrev').onclick=()=>step(-1);$('dNext').onclick=()=>step(1);$('dClose').onclick=closeDetail;$('dImg').onclick=openZoom;if(state.zoom)renderZoom();
 bindMetadataPanel(it);briefing.renderDetections(it);
 $('aCdr').onclick=async()=>{if(it.sent)return;try{await patchItem(it.id,{sent:true});toast('Image and detections pushed to Commander queue')}catch(error){toast(error.message)}};
 $('aFull').onclick=()=>requestFullRes(it);
 for(const [id,override] of [['aUploadNext','next'],['aUploadHold','hold']]){const b=$(id);if(b)b.onclick=async()=>{if(it.live||LIVE||uploadState(it).done)return;try{const result=await request(`/api/items/${encodeURIComponent(it.id)}/upload-override`,'POST',{override:it.upload?.override===override?'':override});cdr=result.cdr;applyItemUpdate(result.item)}catch(error){toast(error.message)}}}
 const aPull=$('aPull');if(aPull)aPull.onclick=async()=>{const id=it.live.itemId;pulling.add(id);aPull.disabled=true;aPull.textContent='requested…';try{await request(`/api/items/${encodeURIComponent(it.id)}/pull`,'POST',{form:'full'});toast('Full image requested from the ship — its status changes from waiting to transferring to received')}catch(error){pulling.delete(id);toast(error.message)}};
 $('aSim').onclick=()=>{const k=rows[0]&&rows[0][0].cls;if(!k)return;$('nlIn').value=`Prioritize ${clsOf(k).lab.toLowerCase()}s`;location.hash='#reprioritize';toast('Drafted a directive from this image — review and apply')};
 $('aDis').onclick=async()=>{const l=visible().l,i=l.indexOf(it),nx=l[i+1]||l[i-1];try{await patchItem(it.id,{dismissed:true,reviewed:true});nx&&findItem(nx.id)?openDetail(nx.id):closeDetail();toast(it.live?'Image hidden from this console (still stored on shore)':'Image hidden from feed (still stored)')}catch(error){toast(error.message)}};
}

/* ---- reprioritize page ---- */
function renderRp(light){
 workbench.live();
 if(light)return;
 renderWeights();renderDirectives();if(MODELS[state.model]){$('modelSel').value=state.model;$('modelDesc').textContent=MODELS[state.model].desc}
}
function renderWeights(){
 const rows=Object.entries(weightData).map(([k,weight])=>({k,base:weight.base,eff:weight.effective,cond:weight.conditional})).sort((a,b)=>b.eff-a.eff);
 $('wNote').textContent=state.directives.length?'Highlighted rows changed.':'';
 $('weights').innerHTML=rows.map(r=>{const c=r.eff>r.base+.02?'up':r.eff<r.base-.02?'down':'';return`<div class="wrow ${c}"><span>${esc(clsOf(r.k).lab)}${r.cond?' <span style="color:var(--accent)">*</span>':''}</span><div class="bar"><i style="width:${r.eff*100}%"></i></div><span class="v">${r.eff.toFixed(2)}</span></div>`}).join('')+(rows.some(r=>r.cond)?'<div class="hint">* boosted further when its condition is met (e.g. approaching, no AIS)</div>':'');
}
function renderDirectives(){
 const d=state.directives;$('clearDir').style.display=d.length?'':'none';
 $('directives').innerHTML=d.length?d.map((x,i)=>`<div class="dir ${x.blocked?'policy-blocked':''}"><span class="n">${i+1}</span><div class="body"><q>${esc(x.text)}</q><div>${gsPolicy.directiveBadge(x)}</div><div class="rules">${x.rules.map(r=>`<span class="rule ${r.type==='suppress'?'sup':''}">${ruleLabel(r)}</span>`).join('')}${x.rules.some(r=>r.only)?'<span class="rule focus">Focus: others ×0.3</span>':''}</div></div>${x.source?'':`<button class="x" data-id="${esc(x.id)}" title="Remove directive">×</button>`}</div>`).join(''):`<div class="nodir">${LIVE?'No standing directive — the edge priority score ranks the live feed.':'No directives — feed is ranked by the base model only.'}</div>`;
 $('directives').querySelectorAll('.x').forEach(b=>b.onclick=async()=>{try{await request(`/api/directives/${encodeURIComponent(b.dataset.id)}`,'DELETE');toast('Directive removed')}catch(error){toast(error.message)}});
}
async function requestFullRes(it){
	if(isScene(it))return;
 if(it.fullRes)return;
 const by=prompt('Operator override: send this image at FULL SIZE with NO compression.\nThis uses much more link bandwidth than the usual thumbnail/crop.\n\nEnter your name or operator ID to confirm:');
 if(by===null)return;
 if(!by.trim()){toast('Not sent: enter your name or operator ID in the confirmation box');return}
 try{await request(`/api/items/${encodeURIComponent(it.id)}/full-res`,'POST',{by:by.trim()});toast(`Full-size send requested by ${by.trim()}`)}catch(error){toast(error.message)}
}
async function setManual(id,t){if(!findItem(id)||isScene(findItem(id)))return;try{await patchItem(id,{manual:t,reviewed:true});toast(`Operator set P${t} ${TNAME[t]} — logged as model feedback`)}catch(error){toast(error.message)}}
function toggleNight(){if(window.gsSettings)gsSettings.toggleRed();else document.body.classList.toggle('night')}
const TNAME=['','Critical','High','Routine','Low'];
const RVKEYS={'1':'1','2':'2','3':'3','4':'4',' ':'accept','enter':'accept','x':'dismiss','delete':'dismiss','c':'cdr','arrowright':'skip','s':'skip','z':'undo','backspace':'undo'};
let rv={cur:null,shown:null,displayed:null,navigation:0,order:'pri',done:0,agree:0,undo:[],busy:false,skipped:{},ask:null,askBusy:false,last:''};
const teamSync={available:false,on:false,proposals:[],hidden:new Set(),resolved:new Set(),local:new Map(),accepting:new Map(),revision:0,posting:false};
function setupTeamSync(available){
 if(typeof gsAuth!=='undefined'&&gsAuth.reviewer())available=false;
 teamSync.available=available;
 try{teamSync.on=available&&localStorage.getItem('gs.reviewSync')==='true'}catch{teamSync.on=false}
 $('reviewSyncLabel').hidden=!available;$('reviewSync').checked=teamSync.on;
 $('reviewSync').onchange=()=>{
  teamSync.on=available&&$('reviewSync').checked;
  try{localStorage.setItem('gs.reviewSync',String(teamSync.on))}catch{}
  if(!teamSync.on&&rv.ask?.team){rv.ask=null;rv.askBusy=false}
  if(teamSync.on&&rv.ask?.kind==='sug'&&!rv.ask.team){
   if(reviewerName){const s=rv.ask.s;rv.ask=null;rv.askBusy=false;proposeTeamSuggestion(s)}
   else toast('Set your reviewer name to share suggestions with the team');
  }
  renderAsk();if(teamSync.on)refreshTeamProposals();
 };
}
async function refreshTeamProposals(){
 if(!teamSync.on)return;
 const revision=teamSync.revision;
 try{const data=await request('/api/review-sync');if(!teamSync.on||revision!==teamSync.revision)return;teamSync.proposals=data.proposals.filter(p=>!teamSync.resolved.has(p.id));if(rv.ask?.team&&!teamSync.proposals.some(p=>p.id===rv.ask.team)){rv.ask=null;rv.askBusy=false}renderAsk()}
 catch(error){toast('Team sync: '+error.message)}
}
function receiveTeamProposal(p,finish=false){
 if(!teamSync.on)return;
 teamSync.revision++;
 const index=teamSync.proposals.findIndex(x=>x.id===p.id);
 if(p.status==='pending'){
  if(!teamSync.resolved.has(p.id)){if(index<0)teamSync.proposals.push(p);else teamSync.proposals[index]=p}
 }else{
  teamSync.proposals=teamSync.proposals.filter(x=>x.id!==p.id);
  const first=!teamSync.resolved.has(p.id);teamSync.resolved.add(p.id);
  const accepting=teamSync.accepting.has(p.id);if(accepting)teamSync.accepting.set(p.id,p);
  const key=p.cls+':'+p.dir,local=teamSync.local.get(key);
  const current=typeof gsLearn!=='undefined'?gsLearn.suggestion():null;
  if(!accepting&&(first||finish)){if(typeof gsLearn!=='undefined'&&(local||(current?.cls===p.cls&&current?.dir===p.dir)))gsLearn.resolve(local||current,p.status==='accepted'?'yes':'no');teamSync.local.delete(key)}
  if(rv.ask?.team===p.id||(rv.ask?.kind==='sug'&&rv.ask.s.cls===p.cls&&rv.ask.s.dir===p.dir)){rv.ask=null;rv.askBusy=false}
  if(teamSync.on&&first&&!accepting)toast(`${p.resolvedBy} ${p.status} ${p.dir==='lower'?'Lower':'Raise'} ${CLS[p.cls]?.lab||p.cls}`);
 }
 if(teamSync.on)renderAsk();
}
async function proposeTeamSuggestion(s){
 teamSync.posting=true;teamSync.local.set(s.cls+':'+s.dir,s);
 try{const data=await request('/api/review-sync/proposals','POST',{cls:s.cls,dir:s.dir,n:s.n,of:s.of,highRisk:!!s.highRisk,from:s.from||0,to:s.to||0,reviewer:reviewerName});receiveTeamProposal(data.proposal)}
 catch(error){toast('Team sync: '+error.message);if(!rv.ask){rv.ask={kind:'sug',s};renderAsk()}}
 finally{teamSync.posting=false}
}
async function answerTeamProposal(answer){
 const ask=rv.ask;if(!ask?.team||rv.askBusy)return;
 if(answer==='later'){teamSync.hidden.add(ask.team);rv.ask=null;renderAsk();return}
 if(!reviewerName){toast('Set your reviewer name first');return}
 rv.askBusy=true;renderAsk();
 const base='/api/review-sync/proposals/'+encodeURIComponent(ask.team);
 let won=false;
 if(answer==='accepted')teamSync.accepting.set(ask.team,null);
 try{
  if(answer==='agree'||answer==='disagree'){
   const data=await request(base+'/vote','POST',{reviewer:reviewerName,vote:answer});receiveTeamProposal(data.proposal);
  }else if(answer==='accepted'||answer==='rejected'){
   const data=await request(base+'/resolve','POST',{reviewer:reviewerName,outcome:answer});receiveTeamProposal(data.proposal);
   if(answer==='accepted'){
    won=true;
    try{await addSuggestionDirective(ask.s);if(typeof gsLearn!=='undefined')gsLearn.resolve(ask.s,'yes');toast(`${reviewerName} accepted ${ask.s.dir==='lower'?'Lower':'Raise'} ${CLS[ask.s.cls]?.lab||ask.s.cls} — rule added`)}
    catch(error){if(typeof gsLearn!=='undefined')gsLearn.resolve(ask.s,'later');toast(`Accepted, but adding the rule failed: ${error.message} — add it on Collection Priorities`)}
    teamSync.local.delete(ask.s.cls+':'+ask.s.dir);
   }
  }
 }catch(error){if(error.status!==404)toast('Team sync: '+error.message);refreshTeamProposals()}
 finally{const deferred=teamSync.accepting.get(ask.team);teamSync.accepting.delete(ask.team);if(!won&&deferred)receiveTeamProposal(deferred,true);if(rv.ask===ask)rv.askBusy=false;renderAsk()}
}
function rvQueue(){const l=items.filter(i=>!isEmptyWater(i)&&(state.src==='ALL'||i.src.id===state.src)&&!i.reviewed&&!i.dismissed);l.sort((a,b)=>(rv.skipped[a.id]||0)-(rv.skipped[b.id]||0)||(rv.order==='pri'?a.tier-b.tier||b.central-a.central:b.ts-a.ts));return l}
function rvProgress(){const left=rvQueue().length;$('rvDone').textContent=rv.done;$('rvLeft').textContent=left;$('rvBarI').style.width=(rv.done/Math.max(1,rv.done+left)*100)+'%';$('rvSrc').textContent='· '+(state.src==='ALL'?'all sources':SRC[state.src].name)+(rv.done?` · agreed with model on ${Math.round(rv.agree/rv.done*100)}%`:'');if(!rv.cur&&left&&!rv.busy)renderReview()}
function renderAsk(){
 const box=$('rvAsk');if(!box)return;
 if(!rv.ask&&teamSync.on){const p=teamSync.proposals.find(p=>!teamSync.hidden.has(p.id));if(p)rv.ask={kind:'sug',s:p,team:p.id}}
 renderNext(rv.cur);
 box.classList.toggle('rvask-suggestion',rv.ask?.kind==='sug');
 box.classList.toggle('rvask-team',!!rv.ask?.team);
 if(!rv.ask){box.hidden=true;box.innerHTML='';return}
 box.hidden=false;
 if(rv.ask.kind==='why'){
  const tier=findItem(rv.ask.id)?.tier||'?';
  box.innerHTML=`<div class="rvask-copy"><b>Why did you set the last image to P${tier} ${TNAME[tier]}?</b><span>Two contacts scored close. Pick the one that drove your call.</span></div><div class="rvask-actions">${rv.ask.classes.map((cls,i)=>`<button class="kb rvask-btn" data-ask="why" data-choice="${esc(cls)}" ${rv.askBusy?'disabled':''}><kbd>${i+1}</kbd>${esc(CLS[cls]?.lab||cls)}</button>`).join('')}<button class="kb rvask-btn" data-ask="why" title="The computer drew a detection box on the wrong thing. This keeps the wrong detection out of future priority-learning suggestions." data-choice="wrong" ${rv.askBusy?'disabled':''}><kbd>${rv.ask.classes.length+1}</kbd>Wrong detection</button><button class="kb rvask-btn rvask-skip" data-ask="why" data-choice="skip" ${rv.askBusy?'disabled':''}><kbd>Esc</kbd>Skip</button></div>`;
  return;
 }
 const s=rv.ask.s,label=CLS[s.cls]?.lab||s.cls,verb=s.dir==='lower'?'lowered':'raised',action=s.dir==='lower'?'Lower':'Raise';
 const change=tierChange(s);
 box.innerHTML=`<div class="rvask-copy"><span class="rvask-kicker">Suggested priority change</span><b class="rvask-change">${change?`${esc(label)}: <span class="tpill t${change.from}">P${change.from} ${TNAME[change.from]}</span> → <span class="tpill t${change.to}">P${change.to} ${TNAME[change.to]}</span>`:`${action} ${esc(label)} priority?`}</b><span>You ${verb} ${s.n} of the last ${s.of} images where ${esc(label)} was the top contact${change?` from P${change.from} ${TNAME[change.from]} to P${change.to} ${TNAME[change.to]}`:''}. Yes adds a rule that ${s.dir==='lower'?'lowers':'raises'} ${esc(label)} priority for new images.</span>${s.highRisk?`<span class="rvask-warning">${esc(label)} is normally high priority — confirm this change.</span>`:''}</div><div class="rvask-actions"><button class="kb rvask-btn" data-ask="sug" data-answer="yes" ${rv.askBusy?'disabled':''}><kbd>Y</kbd>Yes, apply change</button><button class="kb rvask-btn" data-ask="sug" data-answer="no" ${rv.askBusy?'disabled':''}><kbd>N</kbd>No, keep as is</button><button class="kb rvask-btn rvask-skip" data-ask="sug" data-answer="later" ${rv.askBusy?'disabled':''}><kbd>L</kbd>Ask me later</button></div>`;
 if(rv.ask.team){
  const p=teamSync.proposals.find(p=>p.id===rv.ask.team)||s;
  box.innerHTML=`<div class="rvask-team-banner">TEAM DISCUSSION — proposed by ${esc(p.proposedBy)}. Discuss with the team before accepting.</div><div class="rvask-copy"><b class="rvask-change">${change?`${esc(label)}: <span class="tpill t${change.from}">P${change.from} ${TNAME[change.from]}</span> → <span class="tpill t${change.to}">P${change.to} ${TNAME[change.to]}</span>`:`${action} ${esc(label)} priority?`}</b><span>${esc(p.proposedBy)} ${verb} ${s.n} of the last ${s.of} images where ${esc(label)} was the top contact.</span>${s.highRisk?'<span class="rvask-warning">Normally high priority — confirm this change.</span>':''}</div><ul class="rvask-votes">${Object.entries(p.votes).map(([name,vote])=>`<li class="${name===reviewerName?'own-vote':''}">${esc(name)}: ${esc(vote)}${name===reviewerName?' (you)':''}</li>`).join('')||'<li>No votes yet — discuss with your team.</li>'}</ul><div class="rvask-actions">${[['Y','Agree','agree'],['N','Disagree','disagree'],['A','Accept for team','accepted'],['R','Reject for team','rejected'],['L','Later','later']].map(([key,label,answer])=>`<button class="kb rvask-btn" data-ask="team" data-answer="${answer}" ${rv.askBusy?'disabled':''}><kbd>${key}</kbd>${label}</button>`).join('')}</div>`;
 }
}
function checkLearningSuggestion(){
 if(typeof gsAuth!=='undefined'&&gsAuth.reviewer())return;
 if(rv.ask||teamSync.posting||typeof gsLearn==='undefined')return;
 const s=gsLearn.suggestion();if(s){
  if(teamSync.on&&reviewerName){proposeTeamSuggestion(s);return}
  if(teamSync.on)toast('Set your reviewer name to share suggestions with the team');
  rv.ask={kind:'sug',s};rv.askBusy=false;renderAsk();
 }
}
async function answerWhy(choice){
 const ask=rv.ask;if(!ask||ask.kind!=='why'||rv.askBusy||typeof gsLearn==='undefined')return;
 if(choice!=='skip'&&ask.record){rv.askBusy=true;renderAsk();try{await saveReviewReason(ask.record,choice==='wrong'?'wrong_detection':choice)}catch(error){rv.askBusy=false;renderAsk();toast(error.message);return}}
 if(choice==='wrong'){gsLearn.attribute(ask.id,'wrong');toast('Marked as wrong detection — not used for rule suggestions')}
 else gsLearn.attribute(ask.id,choice==='skip'?null:choice);
 rv.ask=null;rv.askBusy=false;renderAsk();checkLearningSuggestion();
}
function addSuggestionDirective(s){
 const label=CLS[s.cls]?.lab||s.cls;
 const tier=Number.isInteger(s.to)&&s.to>=1&&s.to<=4?s.to:0;
 return request('/api/directives','POST',{text:`Operator feedback: ${s.dir==='lower'?'lower':'raise'} ${label}${tier?` to P${tier} ${TNAME[tier]}`:''}`,rules:[{type:s.dir==='lower'?'suppress':'boost',classes:[s.cls],attrs:[],only:false,...(tier?{tier}:{})}]});
}
function answerSuggestion(answer){
 const ask=rv.ask;if(!ask||ask.kind!=='sug'||rv.askBusy||typeof gsLearn==='undefined')return;
 const s=ask.s;
 if(answer!=='yes'){gsLearn.resolve(s,answer);rv.ask=null;rv.askBusy=false;renderAsk();return}
 rv.askBusy=true;renderAsk();
 addSuggestionDirective(s)
  .then(result=>{gsLearn.resolve(s,'yes');toast(result.conflicts?.length?gsPolicy.conflictToast(result.conflicts):'Rule added — review it on Collection Priorities')})
  .catch(error=>{gsLearn.resolve(s,'later');if(!error.policyConflicts)toast(error.message)})
  .finally(()=>{rv.askBusy=false;if(rv.ask===ask)rv.ask=null;renderAsk()});
}
function reviewAskKey(k){
 if(!rv.ask||rv.askBusy)return;
 if(rv.ask.team){const answer={y:'agree',n:'disagree',a:'accepted',r:'rejected',l:'later',escape:'later'}[k];if(answer)answerTeamProposal(answer);return}
 if(rv.ask.kind==='why'){
  if(k==='escape'){answerWhy('skip');return}
  const n=Number(k);if(n>=1&&n<=rv.ask.classes.length)answerWhy(rv.ask.classes[n-1]);else if(n===rv.ask.classes.length+1)answerWhy('wrong');
  return;
 }
 if(k==='y')answerSuggestion('yes');else if(k==='n')answerSuggestion('no');else if(k==='l'||k==='escape')answerSuggestion('later');
}
function tierChange(s){const from=Number(s?.from),to=Number(s?.to);return from>=1&&from<=4&&to>=1&&to<=4&&from!==to?{from,to}:null}
function renderNext(it){
 const box=$('rvNext');if(!box)return;
 let step;
 if(!reviewerName)step='Enter your reviewer name above to start reviewing.';
 else if(rv.busy)step='Saving review… Please wait before reviewing another image.';
 else if(it&&!findItem(it.id))step='This image is unavailable. Press <kbd>S</kbd> to skip, or <kbd>Z</kbd> to undo your last decision.';
 else if(rv.ask?.kind==='why')step='Answer the question above to continue: press the number of your choice, or <kbd>Esc</kbd> to skip.';
 else if(rv.ask)step='Answer the yellow question to continue.';
 else if(!it)step='No images left to review. New images will appear here.';
 else step=`${rv.last?'This is the next image. ':''}Press <kbd>Space</kbd> to agree with P${it.centralTier} ${TNAME[it.centralTier]}, or <kbd>1</kbd>–<kbd>4</kbd> to set a different priority. Each choice saves and moves on.`;
 box.innerHTML=(rv.last?`<span class="rvnext-done">${esc(rv.last)}</span>`:'')+`<span class="rvnext-step"><b>Next:</b> ${step}</span>`;
}
function setLast(msg){rv.last=msg;const box=$('rvNext');if(!box?.classList)return;box.classList.remove('flash');void box.offsetWidth;box.classList.add('flash')}
function renderReview(){preserveBandFocus(renderReviewContent)}
function renderReviewContent(){
 setupReviewData();
 if(isEmptyWater(rv.cur)){rv.cur=null;rv.shown=null;rv.displayed=null}
 const current=rv.cur?findItem(rv.cur.id):rv.busy?null:rvQueue()[0];
 if(current)rv.cur=current;
 const it=rv.cur,unavailable=!!it&&!current;
 rvProgress();renderAsk();
 [...$('rvKeys').children].forEach(b=>{if(b.dataset.a)b.disabled=!reviewerName||rv.busy||(!it&&b.dataset.a!=='undo')||(unavailable&&!['skip','undo'].includes(b.dataset.a))});
 if(!it){rv.shown=null;rv.displayed=null;$('rvMain').innerHTML=`<div class="rvdone"><div class="ck">✓</div><b>${rv.busy?'Saving review…':'All caught up'}</b><div>${rv.busy?'The next image will appear when the save completes.':'New images will appear here as sensors report.'}</div></div><div class="rvflash" id="rvFlash"></div>`;return}
 const {el,fresh,...displayed}=it;
 const key=JSON.stringify([displayed,unavailable]);if(rv.shown===key)return;rv.shown=key;rv.displayed=displayed;
 const sug=it.centralTier,rows=it.dets.map((d,i)=>[d,it.det[i]]).sort((a,b)=>b[1].s-a[1].s),why=buildWhy(it,rows).slice(0,4);
 const age=Math.max(0,Math.round((Date.now()-it.ts)/60000));
 const classifications=rows.filter(([d])=>d.ship).slice(0,3).map(([d])=>shipLine(d));
 [...$('rvKeys').querySelectorAll('[data-a]')].forEach(b=>b.classList.toggle('sugg',b.dataset.a===String(sug)));
 $('accLbl').textContent=reviewAcceptLabel(it);
 $('rvMain').innerHTML=`<div class="rvimg"><div class="rvframe p${sug}">${largeImage(it)}</div></div>
 <div class="rvside">${unavailable?'<div class="rvcard" role="status"><b>This image is no longer available on the server.</b><p>Review paused for this image. Press Skip (S) to move to the next image, or Undo (Z) to return to your last decision. The live feed is still updating.</p></div>':''}${detectorNegativeHTML(it)}
  <div class="rvcard"><div class="lbl">Model suggests</div><div class="rvsug"><span class="tier t${sug}">P${sug} ${TNAME[sug]}</span></div><div class="rvsub" title="${DATASET_MODE?'Command-model score and detector triage score':'Command score is the shore-side priority estimate; edge score is the ship-side estimate.'}">${DATASET_MODE?'Model score':'Shore score'} ${it.central.toFixed(2)} · ${DATASET_MODE?'detector score':'ship score'} ${it.edge.toFixed(2)}${state.directives.length?` · ${state.directives.length} directive${state.directives.length>1?'s':''} active`:''}</div></div>
  <div class="rvcard"><div class="lbl" style="margin-bottom:4px">Why</div><ul class="why">${why.map(w=>`<li class="${w.c}">${w.t}</li>`).join('')}</ul></div>
  <div class="rvcard rvmeta"><span>Source</span><b>${esc(it.src.name)}</b><span>Captured</span><b class="mono">${zulu(it.ts)} <span style="color:var(--muted);font-family:inherit;font-weight:500">(${age} min ago)</span></b><span>Objects</span><b>${rows.map(([d])=>esc(clsOf(d.cls).lab)).join(', ')||'—'}</b>${classifications.length?`<span>Classification</span><b class="rvclassification">${classifications.join('<br>')}</b>`:''}</div>
 </div><div class="rvflash" id="rvFlash"></div>`;
}
function rvFlash(msg,col){const f=$('rvFlash');if(!f)return;f.textContent=msg;f.style.color=col;f.classList.add('on');setTimeout(()=>f.classList.remove('on'),300)}
function rvPress(a){const b=$('rvKeys').querySelector(`[data-a="${a}"]`);if(!b)return;b.classList.add('pressed');setTimeout(()=>b.classList.remove('pressed'),140)}
function rvAct(a){
 const revision=resetRevision;
 if(!reviewerName){toast('Set your reviewer name first');return}
 if(rv.busy)return;rvPress(a);
 if(a==='undo'){const p=rv.undo[rv.undo.length-1];if(!p){toast('Nothing to undo');return}const navigation=rv.navigation;rv.busy=true;undoReview(p).then(it=>{if(revision!==resetRevision)return;rv.undo.pop();rv.done--;if(p.agreed)rv.agree--;if(typeof gsLearn!=='undefined')gsLearn.forget(p.id);rv.ask=null;rv.askBusy=false;renderAsk();if(rv.navigation===navigation)rv.cur=it||p.item;rv.shown=null;rv.busy=false;setLast(rv.navigation===navigation?'↶ Undone: you are back on that image.':'↶ Undone: previous review was restored.');renderReview();rvFlash('↶ Undone','#c3d1dd')}).catch(error=>{if(revision!==resetRevision)return;rv.busy=false;renderReview();toast(error.message)});return}
 if(!rv.cur||isEmptyWater(rv.cur))return;
 if(a==='skip'){rv.skipped[rv.cur.id]=Date.now();rv.cur=null;rv.shown=null;setLast('Skipped: that image moved to the end of the list.');renderReview();return}
 const it=rv.displayed;if(!it||isEmptyWater(it)||it.id!==rv.cur.id)return;
 if(!findItem(it.id)){renderReview();toast('This image is no longer available. Skip to continue.');return}
 const sug=it.centralTier,p={id:it.id,item:it,manual:it.manual,reviewed:it.reviewed,dismissed:it.dismissed,sent:it.sent,agreed:false};
 let msg,col,receipt;
 let changes={reviewed:true};
 if('1234'.includes(a)){changes.manual=+a;msg=`P${a} ${TNAME[+a]}`;col=tcol(+a);p.agreed=+a===sug;receipt=p.agreed?`✓ Saved: last image kept at P${a} ${TNAME[+a]}.`:`✓ Saved: last image changed P${sug} ${TNAME[sug]} → P${a} ${TNAME[+a]}.`}
 else if(a==='accept'){changes.manual=sug;msg=`✓ P${sug} ${TNAME[sug]}`;col=tcol(sug);p.agreed=true;receipt=`✓ Saved: you agreed with P${sug} ${TNAME[sug]} on the last image.`}
 else if(a==='dismiss'){changes.manual=4;changes.dismissed=true;msg='✕ Not relevant';col='#9aa7b3';receipt='✓ Saved: last image marked not relevant and hidden from the feed.'}
 else if(a==='cdr'){changes.manual=1;changes.sent=true;msg='➤ Sent to Commander';col='var(--human)';receipt='✓ Saved: last image sent to the Commander.'}
 else return;
 rv.busy=true;
 submitReview(it.id,a,changes,p).then(updated=>{if(revision!==resetRevision)return;rv.done++;if(p.agreed)rv.agree++;rv.undo.push(p);if(rv.undo.length>50)rv.undo.shift();if(!it.detectorNegative&&typeof gsLearn!=='undefined'){const result=gsLearn.record({id:it.id,src:it.src.id,dets:it.dets,det:it.det,sug,tier:changes.manual});if(result?.ambiguous){rv.ask={kind:'why',id:it.id,record:p.record,classes:result.ambiguous};rv.askBusy=false;renderAsk()}else checkLearningSuggestion()}setLast(receipt+' Press Z to undo.');rvFlash(msg,col);setTimeout(()=>{if(revision!==resetRevision)return;rv.busy=false;if(rv.cur?.id===it.id){rv.cur=null;rv.shown=null}renderReview()},280)}).catch(error=>{if(revision!==resetRevision)return;rv.busy=false;renderReview();toast(error.message)});
}
let tt;function toast(m){const t=$('toast');t.textContent=m;t.classList.add('on');clearTimeout(tt);tt=setTimeout(()=>t.classList.remove('on'),3200)}
let reviewerName='';try{reviewerName=(localStorage.getItem('gsReviewer')||'').trim().slice(0,80)}catch{}
let reviewDataReady=false,lastReview=null,reviewDataRevision=0;
function renderReviewData(s){$('reviewData').textContent=`${s.count} reviews · ${Math.round(s.agreement*100)}% agreement · `+Object.entries(s.reviewers).map(([n,v])=>`${n}: ${v.count}`).join(', ')}
function refreshReviewData(){const revision=++reviewDataRevision;return request('/api/reviews/summary').then(s=>{if(revision===reviewDataRevision)renderReviewData(s)}).catch(error=>{if(revision===reviewDataRevision)$('reviewData').textContent=error.message})}
function setupReviewData(){if(reviewDataReady)return;reviewDataReady=true;$('reviewerName').value=reviewerName;$('reviewerStatus').textContent=reviewerName?`Reviewing as ${reviewerName}`:'Set a name to enable review keys';$('reviewerSave').onclick=()=>{reviewerName=$('reviewerName').value.trim().slice(0,80);try{localStorage.setItem('gsReviewer',reviewerName)}catch{}$('reviewerStatus').textContent=reviewerName?`Reviewing as ${reviewerName}`:'Set a name to enable review keys';renderReview()};$('reviewerName').onkeydown=e=>{if(e.key==='Enter'&&!e.isComposing){e.preventDefault();$('reviewerSave').click()}};$('reviewNoteOpen').onclick=openReviewNote;$('reviewNote').onkeydown=async e=>{e.stopPropagation();if(e.key==='Escape'){$('reviewNoteBox').hidden=true;return}if(e.key!=='Enter')return;e.preventDefault();try{await saveReviewReason(lastReview.id,lastReview.reason||'',$('reviewNote').value);lastReview.note=$('reviewNote').value;$('reviewNoteBox').hidden=true;toast('Note saved')}catch(error){toast(error.message)}};refreshReviewData()}
function openReviewNote(){if(!lastReview){toast('Review an image first');return}$('reviewNoteBox').hidden=false;$('reviewNote').value=lastReview.note||'';$('reviewNote').focus()}
async function submitReview(id,action,changes,p){const r=await request(`/api/items/${encodeURIComponent(id)}/review`,'POST',{reviewer:reviewerName,action:'1234'.includes(action)?'tier':action,tier:changes.manual});p.record=r.record.id;lastReview=r.record;cdr=r.cdr;applyItemUpdate(r.item);refreshReviewData();return findItem(id)}
async function undoReview(p){const r=await request(`/api/reviews/${encodeURIComponent(p.record)}/undo`,'POST',{reviewer:reviewerName});if(r.item){cdr=r.cdr;applyItemUpdate(r.item)}if(lastReview?.id===p.record)lastReview=null;refreshReviewData();return findItem(p.id)}
async function saveReviewReason(id,reason,note){if(note===undefined)note=lastReview?.id===id?lastReview.note||'':'';await request(`/api/reviews/${encodeURIComponent(id)}/reason`,'POST',{reviewer:reviewerName,reason,note});if(lastReview?.id===id){lastReview.reason=reason;lastReview.note=note}}
init();
