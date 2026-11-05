let SOURCES=[],SRC={},CLS={},MODELS={},ATTR_LAB={},EXAMPLES=[];
let state={src:'ALL',model:'maritime',sort:'pri',tier:'all',paused:false,sel:null,directives:[],policy:null};
let items=[],stats={cap:0,keep:0,drop:0,bytes:0,db:0},cdr=0,weightData={},pendingItems=[],evictedIDs=new Set(),heldSnapshotIDs=null,policyAuditTimer=null,policyRawTouched=false,policyRawLoaded=false;
/* LIVE is the shore block of the snapshot when the console runs on a shore node (-shore); null on the simulation. */
let LIVE=null;
let feedReasons=new Map();
const clsOf=k=>CLS[k]||{lab:String(k||'unknown'),g:'other'};
const fmtBytes=n=>n<1024?`${n|0} B`:n<1048576?`${(n/1024).toFixed(1)} KB`:`${(n/1048576).toFixed(2)} MB`;
const fmtRate=bps=>bps>=1e6?`${(bps/1e6).toFixed(1)} Mbps`:`${(bps/1000).toFixed(bps<10000?1:0)} kbps`;
const ago=ms=>{const s=Math.max(0,(Date.now()-ms)/1000);return s<60?`${s|0}s ago`:s<3600?`${(s/60)|0}m ago`:`${(s/3600)|0}h ago`};
const FORMS=['fact','thumb','crop','full'];
const FORM_NAME={fact:'Summary',thumb:'Preview',crop:'Object close-up',full:'Full image'};
const FORM_HELP={fact:'FACT: a small summary of the object and capture time (about 200 bytes). Sent before images.',thumb:'THUMB: a small preview of the whole image.',crop:'CROP: a close-up of the detected object at its original resolution.',full:'FULL: the complete, full-quality image. Camera overview images are sent only when requested.'};
function formChipsHTML(it){
 const f=it.live?.forms;if(!f)return'';
 return FORMS.map(k=>{const st=f[k]||{state:'none'};const title=st.state==='received'?`${FORM_NAME[k]}: received (${fmtBytes(st.bytes)})`:st.state==='partial'?`${FORM_NAME[k]}: ${fmtBytes(st.received)} of ${fmtBytes(st.bytes)} — streaming over the link`:st.state==='pending'?`${FORM_NAME[k]}: waiting on the ship (${fmtBytes(st.bytes)})`:st.state==='offered'?`${FORM_NAME[k]}: available on the ship — request it to send it`:`${FORM_NAME[k]}: not available`;
  return`<span class="fchip ${st.state}${it.live.imageForm===k?' shown':''}" title="${esc(FORM_HELP[k]+' · '+title)}">${FORM_NAME[k]}${st.state==='partial'?` ${Math.round(100*st.received/Math.max(1,st.bytes))}%`:''}</span>`}).join('');
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
 return(r.type==='boost'?'▲ ':'▼ ')+cl+at;
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
function largeImage(it){return `<img class="${bandsFor(it).length>1&&bandFor(it)==='IR'?'spectrum-ir':''}" src="${imageFor(it)}" alt="${esc(it.src.name)}${bandsFor(it).length>1?' · '+esc(bandFor(it)):''}">${boxesHTML(it,true)}${bandControls(it)}`}
function preserveBandFocus(render){
 const button=document.activeElement?.closest?.('.band-controls button');
 const surface=button?.closest('#rvMain, #drawer, #zoom');
 const item=button?.dataset.bandItem,band=button?.dataset.band;
 render();
 if(surface)[...surface.querySelectorAll('[data-band-item]')].find(b=>b.dataset.bandItem===item&&b.dataset.band===band)?.focus({preventScroll:true});
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
 const sid=typeof item.src==='string'?item.src:item.src.id;
 item.ts=new Date(item.ts);item.src=SRC[sid]||{id:sid,name:String(sid).replace(/_/g,' '),scene:'open',mode:'eo',hw:'edge node',em:'edge detector',link:'ship→shore link'};
 const prev=findItem(item.id);if(prev&&prev.img!==item.img)imageCache.delete(item.id);
 return item;
}
function findItem(id){return items.find(item=>item.id===id)}
function renderAll(){
 renderFeed(true);renderStats();renderRp();if(state.sel)renderDetail();if(state.page==='rv')renderReview();
}
function mergeItem(raw,fresh=true){
 const item=normalizeItem(raw);if(evictedIDs.has(item.id))return;
 const index=items.findIndex(existing=>existing.id===item.id);
 if(index>=0)item.el=items[index].el;
 item.fresh=fresh;
 if(index<0)items.push(item);else items[index]=item;
 if(rv.cur?.id===item.id)rv.cur=item;
 if(fresh){['st1','st2','st3','st4','st5'].forEach((id,index)=>setTimeout(()=>flash(id),index*140));setTimeout(()=>{const current=findItem(item.id);if(current){current.fresh=false;updateCard(current)}},6000)}
}
function flash(id){const element=$(id);if(!element)return;element.classList.add('flash');setTimeout(()=>element.classList.remove('flash'),350)}
function applySnapshot(data){
 const selection=state.sel;
 const elements=new Map(items.map(item=>[item.id,item.el]));
 if(state.paused){
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
 state.model=data.state.model;state.directives=data.state.directives;
 applyLive(data.live);
 renderPolicySummary(data.policy);
 stats=data.stats;cdr=data.cdr;weightData=data.weights||{};
 state.sel=items.some(item=>item.id===selection)?selection:null;
 if(!state.sel){document.body.classList.remove('drawer-open');$('drawer').innerHTML='';closeZoom()}
 rv.cur=rv.cur&&items.find(item=>item.id===rv.cur.id)||null;
 renderAll();
}
function applyLive(live){
 LIVE=live||null;const cl=document.body?.classList;if(cl?.toggle)cl.toggle('live',!!LIVE);
 if(!LIVE)return;
 mergeLiveConfig(LIVE);
 const banner=document.querySelector?.('.banner');if(banner)banner.textContent='UNCLASSIFIED // FOR TRAINING AND DEMONSTRATION ONLY — LIVE SHORE PICTURE';
}
function applyItemUpdate(raw){
 if(state.paused&&!findItem(raw.id)){
  if(evictedIDs.has(raw.id))return;
  heldSnapshotIDs?.add(raw.id);
  const pending=pendingItems.find(event=>event.item.id===raw.id);
  if(pending)pending.item=raw;else pendingItems.push({item:raw});
  renderStats();return;
 }
 const item=normalizeItem(raw);mergeItem(item,false);
 if(state.sel===item.id)renderDetail();
 renderFeed();renderStats();renderRp(true);if(state.page==='rv'&&!rv.busy)renderReview();
}
async function request(path,method='GET',body){
 if(typeof gsAuth!=='undefined')gsAuth.requireWrite(method);
 const response=await fetch(path,{method,headers:body?{'Content-Type':'application/json'}:{},body:body?JSON.stringify(body):undefined});
 const data=response.status===204?null:await response.json();
 if(!response.ok){const error=new Error(data?.message||data?.error||`Request failed (${response.status})`);error.status=response.status;if(data?.conflicts?.length){error.policyConflicts=data.conflicts;toast(gsPolicy.conflictToast(data.conflicts))}throw error}
 return data;
}
async function patchItem(id,changes){
 const result=await request(`/api/items/${encodeURIComponent(id)}`,'PATCH',changes);
 cdr=result.cdr;applyItemUpdate(result.item);return findItem(id);
}
function renderLive(){
 const live=$('live');live.classList.toggle('off',!!state.offline);live.classList.toggle('paused',!state.offline&&state.paused);
 live.querySelector('span').textContent=state.offline?'RECONNECTING':state.paused?'FEED PAUSED':'FEED ACTIVE';
 const action=state.paused?'resume feed':'pause feed';
 live.title=state.offline?`Reconnecting — click to ${action}`:state.paused?'Feed paused — click to resume feed':'Feed active — click to pause feed';
 live.setAttribute('aria-label',live.title);live.setAttribute('aria-pressed',String(!!state.paused));
 $('pauseBtn').textContent=state.paused?'Resume feed':'Pause feed';$('wbHeld').hidden=!state.paused;
}
function toggleHold(){
 state.paused=!state.paused;renderLive();
 if(!state.paused){if(heldSnapshotIDs){const keep=heldSnapshotIDs;for(const item of items)if(!keep.has(item.id)){imageCache.delete(item.id);if(state.sel===item.id)closeDetail();if(rv.cur?.id===item.id){rv.cur=null;rv.shown=null}}items=items.filter(item=>keep.has(item.id));pendingItems=pendingItems.filter(event=>keep.has(event.item.id));heldSnapshotIDs=null}for(const event of pendingItems)mergeItem(event.item);pendingItems=[];renderAll()}
}
function openStream(){
 const stream=new EventSource('/api/stream');
 stream.onopen=()=>{state.offline=false;renderLive()};
 stream.onerror=()=>{state.offline=true;renderLive()};
 stream.addEventListener('snapshot',event=>{applySnapshot(JSON.parse(event.data));refreshTeamProposals()});
 stream.addEventListener('review-sync',event=>receiveTeamProposal(JSON.parse(event.data).proposal));
 stream.addEventListener('item',event=>{
  const data=JSON.parse(event.data);
  stats=data.stats;if(state.paused){pendingItems.push(data);heldSnapshotIDs?.add(data.item.id);renderStats()}else{mergeItem(data.item);renderFeed();renderStats();renderRp(true);if(state.page==='rv')renderReview()}
 });
 stream.addEventListener('stats',event=>{
  stats=JSON.parse(event.data).stats;renderStats();
 });
 stream.addEventListener('update',event=>{
  const data=JSON.parse(event.data);cdr=data.cdr;applyItemUpdate(data.item);
 });
 stream.addEventListener('live',event=>{
  applyLive(JSON.parse(event.data));renderStats();if(state.page==='feed')renderCameras();
 });
 stream.addEventListener('evict',event=>{
  const {id}=JSON.parse(event.data);evictedIDs.add(id);imageCache.delete(id);pendingItems=pendingItems.filter(data=>data.item.id!==id);
  items=items.filter(item=>item.id!==id);
  if(state.sel===id)closeDetail();
  if(rv.cur?.id===id){rv.cur=null;rv.shown=null}
  renderAll();
 });
}
async function init(){
 if(typeof gsAuth!=='undefined')await gsAuth.init();
 const config=await request('/api/config');
 setupTeamSync(!!config.reviewSync);
 SOURCES=[{id:'ALL',name:'All sensors (combined)'},...config.sources];
 SRC=Object.fromEntries(SOURCES.map(source=>[source.id,source]));
 CLS=config.classes;MODELS=config.models;ATTR_LAB=config.attrLabels;EXAMPLES=config.examples;
 if(config.live&&!MODELS[state.model])state.model=Object.keys(MODELS)[0]||state.model;
 $('srcSel').innerHTML=SOURCES.map(s=>`<option value="${esc(s.id)}">${esc(s.name)}</option>`).join('');
 $('modelSel').innerHTML=Object.entries(MODELS).map(([k,m])=>`<option value="${k}">${m.name}</option>`).join('');
 $('examples').innerHTML=EXAMPLES.map(e=>`<span class="ex">${e}</span>`).join('');
 $('examples').onclick=e=>{if(e.target.classList.contains('ex')){$('nlIn').value=e.target.textContent;workbench.textChanged()}};
 $('srcSel').onchange=e=>{state.src=e.target.value;closeDetail();renderFeed(true);renderStats();renderRp()};
 const syncFeedFilters=()=>{const f=gsSettings.get().feed;$('showFiltered').checked=f.showFiltered;$('reviewFilter').value=f.review;closeDetail();renderFeed(true)};
 $('showFiltered').onchange=e=>{gsSettings.setFeed({showFiltered:e.target.checked});syncFeedFilters()};
 $('reviewFilter').onchange=e=>{gsSettings.setFeed({review:e.target.value});syncFeedFilters()};
 window.addEventListener('gs:feed-filters',syncFeedFilters);syncFeedFilters();
 $('modelSel').onchange=async e=>{try{await request('/api/model','PUT',{id:e.target.value});toast(`Base model set to ${MODELS[e.target.value].name}`)}catch(error){toast(error.message);e.target.value=state.model}};
 workbench.init();
 briefing.init();
 $('clearDir').onclick=async()=>{if(!state.directives.length)return;try{await request('/api/directives','DELETE');toast('Directives cleared — base model restored')}catch(error){toast(error.message)}};
 $('sortSeg').onclick=e=>{const b=e.target.closest('button');if(!b)return;state.sort=b.dataset.s;[...$('sortSeg').children].forEach(x=>x.classList.toggle('on',x===b));renderFeed()};
 $('densSeg').onclick=e=>{const b=e.target.closest('button');if(!b)return;state.dens=b.dataset.d;gsSettings.density(state.dens)};
 $('tierSeg').onclick=e=>{const b=e.target.closest('button');if(!b)return;state.tier=b.dataset.t;renderFeed()};
 $('pauseBtn').onclick=toggleHold;$('live').onclick=toggleHold;$('wbResume').onclick=toggleHold;
 $('emconControl').onclick=async e=>{const button=e.target.closest('#emconSeg [data-mode]');if(!button||button.disabled)return;try{const result=await request('/api/policy/emcon','POST',{mode:button.dataset.mode});if(result.conflicts?.length)toast(gsPolicy.conflictToast(result.conflicts))}catch(error){if(!error.policyConflicts)toast(error.message)}};
 $('polRaw').oninput=()=>{policyRawTouched=true};
 $('polRawValidate').onclick=()=>rawPolicyAction('validate');$('polRawApply').onclick=()=>rawPolicyAction('apply');$('polRawReload').onclick=()=>{policyRawTouched=false;loadRawPolicy(true)};
 $('grid').onclick=e=>{const c=e.target.closest('.card');if(c)openDetail(c.dataset.id)};
 document.addEventListener('click',e=>{const b=e.target.closest('[data-band-item]');if(!b)return;e.stopPropagation();selectBand(findItem(b.dataset.bandItem),b.dataset.band)},true);
 document.addEventListener('keydown',e=>{if(document.querySelector('dialog[open]')||$('settings').open)return;if(gsPolicy.isPolicyHotkeyTarget(e.target))return;if(typeof gsAuth!=='undefined'&&gsAuth.viewer())return;const tg=e.target.tagName;if(tg==='TEXTAREA'||tg==='INPUT'||tg==='SELECT'||e.ctrlKey||e.metaKey||e.altKey)return;
  const k=e.key.toLowerCase();
  if((k==='enter'||k===' ')&&e.target.closest?.('header, .band-controls'))return;
  if(state.page==='rv'&&k==='w'){e.preventDefault();openReviewNote();return}
  if(state.page==='rv'&&rv.ask){e.preventDefault();if(!e.repeat)reviewAskKey(k);return}
  if(k==='n'){toggleNight();return}
  if(k==='b'&&(state.page==='rv'||state.page==='feed')){const it=state.page==='rv'?rv.cur:findItem(state.sel);const bands=it?bandsFor(it):[];if(bands.length>1){e.preventDefault();if(!e.repeat)selectBand(it,bands[(bands.indexOf(bandFor(it))+1)%bands.length])}return}
  if(state.page==='rv'){if(k==='escape'){location.hash='#feed';return}const a=RVKEYS[k];if(a){e.preventDefault();if(!e.repeat)rvAct(a)}return}
  if(state.page!=='feed')return;
  if(k==='r'){location.hash='#review';return}
  if(k==='escape'){if(state.zoom)closeZoom();else closeDetail();return}
  if(k==='f'&&state.sel){state.zoom?closeZoom():openZoom();return}
  if(state.sel&&['1','2','3','4'].includes(k))setManual(state.sel,+k);
  if(state.sel&&['arrowright','arrowdown','arrowleft','arrowup'].includes(k)){e.preventDefault();step(k==='arrowright'||k==='arrowdown'?1:-1)}});
 $('rvKeys').onclick=e=>{const b=e.target.closest('.kb');if(b&&(!rv.ask||b.dataset.a==='undo')){b.blur();rvAct(b.dataset.a)}};
 $('rvAsk').onclick=e=>{const b=e.target.closest('[data-ask]');if(!b)return;if(b.dataset.ask==='why')answerWhy(b.dataset.choice);else if(b.dataset.ask==='team')answerTeamProposal(b.dataset.answer);else answerSuggestion(b.dataset.answer)};
 $('rvOrder').onclick=e=>{const b=e.target.closest('button');if(!b)return;rv.order=b.dataset.o;[...$('rvOrder').children].forEach(x=>x.classList.toggle('on',x===b));rv.cur=null;renderReview()};
 $('nightBtn').onclick=toggleNight;
 window.addEventListener('hashchange',route);
 setInterval(()=>$('clock').textContent=zulu(new Date()),1000);$('clock').textContent=zulu(new Date());
 route();renderFeed(true);renderStats();renderRp();openStream();
}
function route(){
 state.page=location.hash==='#briefing'?'brief':location.hash==='#reprioritize'?'rp':location.hash==='#review'?'rv':'feed';
 $('pgBrief').classList.toggle('on',state.page==='brief');
 $('pgFeed').classList.toggle('on',state.page==='feed');$('pgRp').classList.toggle('on',state.page==='rp');$('pgRv').classList.toggle('on',state.page==='rv');$('navRv').classList.toggle('on',state.page==='rv');
 $('navFeed').classList.toggle('on',state.page==='feed');$('navRp').classList.toggle('on',state.page==='rp');
 if(state.page!=='rv'&&rv.ask){rv.ask=null;rv.askBusy=false;renderAsk()}
 if(state.page==='feed')renderFeed(true);else if(state.page==='brief'){closeDetail();briefing.render()}else if(state.page==='rv'){closeDetail();rv.cur=null;rv.shown=null;renderReview()}else{closeDetail();renderRp();workbench.activate();setTimeout(()=>$('nlIn').focus(),50)}
}

function humanReviewed(it){return !!(it.reviewed||it.manual||it.dismissed)}
function filteredReason(it){return it.dismissed?'Not relevant':it.manual||it.sent?'':it.live?.suppressed||it.det?.length&&it.det.every(d=>d.sup)?'Filtered by directive':''}
function feedTier(it){return feedReasons.has(it.id)?4:it.tier}
function visible(){
 const f=typeof gsSettings!=='undefined'?gsSettings.get().feed:{showFiltered:false,review:'all'};
 let l=items.filter(i=>(state.src==='ALL'||i.src.id===state.src)&&(f.review==='all'||humanReviewed(i)===(f.review==='reviewed')));
 feedReasons=new Map();const originals=new Map();
 for(const it of [...l].sort((a,b)=>a.tier-b.tier||b.central-a.central||b.ts-a.ts||String(a.id).localeCompare(String(b.id)))){
  const reason=filteredReason(it);if(reason){feedReasons.set(it.id,reason);continue}
  // Match only actual image paths, never placeholder URLs, seeds or sighting reports.
  if(!it.img||it.live)continue;
  const key=JSON.stringify([it.src.id,it.img]);
  if(originals.has(key)){if(!it.manual&&!it.sent)feedReasons.set(it.id,'Duplicate image')}else originals.set(key,it.id);
 }
 l=l.filter(i=>f.showFiltered||!feedReasons.has(i.id));
 const counts={all:l.length,1:0,2:0,3:0,4:0};l.forEach(i=>counts[feedTier(i)]++);
 if(state.tier!=='all')l=l.filter(i=>String(feedTier(i))===state.tier);
 l.sort(state.sort==='pri'?(a,b)=>feedTier(a)-feedTier(b)||Number(feedReasons.has(a.id))-Number(feedReasons.has(b.id))||b.central-a.central||b.ts-a.ts:(a,b)=>b.ts-a.ts);
 return{l,counts};
}
function renderTierSeg(counts){
 const T=[['all','All'],['1','P1 Critical'],['2','P2 High'],['3','P3 Routine'],['4','P4 Low']];
 $('tierSeg').innerHTML=T.map(([k,l])=>`<button data-t="${k}" class="${state.tier===k?'on':''}">${k!=='all'?`<span class="tchip" style="background:${tcol(+k)}"></span>`:''}${l}<span class="c">${counts[k]}</span></button>`).join('');
}
function boxesHTML(it,labels){
 return it.dets.map((d,i)=>{const sd=it.det[i];if(!d.b||!(d.b.w>0))return'';const c=sd.sup?'sup':sd.matched?'match':sd.s>=.6?'hi':sd.s>=.3?'md':'lo';const b=d.b;
  return`<div class="box ${c}${d.cls==='unknown'?' unknown':''}${b.x+b.w/2>W*.6?' r':''}${b.y<H*.08?' t':''}" style="left:${b.x/W*100}%;top:${b.y/H*100}%;width:${b.w/W*100}%;height:${b.h/H*100}%">${labels?`<em>${esc(clsOf(d.cls).lab)} ${Math.round(d.conf*100)}%</em>`:''}</div>`}).join('');
}
function topLine(it){
 const r=it.dets.map((d,i)=>[d,it.det[i]]).sort((a,b)=>b[1].s-a[1].s);if(!r.length)return'—';
 if(it.live){const l=it.live,[d,sd]=r[0];const det=l.label&&l.label.toLowerCase()!==d.cls?` <span style="opacity:.6">· computer label: ${esc(l.label)}</span>`:'';const frames=l.sightingId?(l.framesKnown?` · ${l.frames} frames`:` · ${l.reports} report${l.reports===1?'':'s'}`):'';
  return`<span class="${l.suppressed?'s':sd.matched?'m':l.unknown?'a':''}">${esc(clsOf(d.cls).lab)}${l.unknown?' (unknown)':''}</span>${det}<span style="opacity:.6">${frames}</span>`}
 const[d,sd]=r[0];const tag=d.attrs.dark&&clsOf(d.cls).g!=='other'&&clsOf(d.cls).g!=='clutter'?' · no AIS':d.attrs.approaching?' · inbound':d.attrs.new?' · new':'';
 return`<span class="${sd.matched?'m':tag&&sd.s>.4?'a':''}">${esc(clsOf(d.cls).lab)}${tag}</span>${r.length>1?` <span style="opacity:.6">+${r.length-1}</span>`:''}`;
}
function cardHTML(it,mini){
 return`<div class="thumb"><img src="${imageFor(it)}" alt=""><div class="bx">${mini?boxesHTML(it,false):''}</div><span class="tier t${it.tier}" title="${it.tier===1?'Notify Commander':'Image priority'}">P${it.tier} ${TNAME[it.tier]}</span><span class="cdrtag">✓ Sent to Commander</span><span class="newtag">NEW</span><div class="ov"><span class="sid">${esc(it.src.id)}</span><span class="t mono">${zulu(it.ts).slice(0,5)}Z</span><span class="delta"></span><span class="rvk" style="margin-left:auto">✓</span><b class="cs" style="margin-left:4px">${mini?it.central.toFixed(2):''}</b></div><span class="formtag"></span><span class="suptag">DEPRIORITIZED</span></div><div class="cl"></div><div class="fchips"></div>`;
}
function makeCard(it){const el=document.createElement('div');el.dataset.id=it.id;el.innerHTML=cardHTML(it);if(bandsFor(it).length>1)el.querySelector('.thumb').insertAdjacentHTML('beforeend',`<span class="band-badge">${bandsFor(it).map(esc).join('/')}</span>`);it.el=el;return el}
function updateCard(it){
 const el=it.el;if(!el)return;const tier=feedTier(it),reason=feedReasons.get(it.id);el.className=`card p${tier}${reason?' sup':''}${it.fresh?' fresh':''}${it.sent?' sent':''}${humanReviewed(it)?' rev':''}${it.manual?' man':''}${state.sel===it.id?' sel':''}`;
 const reviewMark=el.querySelector('.rvk');reviewMark.title=humanReviewed(it)?'Human reviewed':'Not reviewed';reviewMark.setAttribute('aria-label',reviewMark.title);
 const status=el.querySelector('.suptag');status.textContent=reason||'';status.title=reason?`${reason} — shown at P4 Low in this feed only; stored model score and transfer priority are unchanged`:'';
 el.querySelector('.night-badge')?.remove();el.querySelector('.thumb').insertAdjacentHTML('beforeend',gsLights.badge(it));
 const t=el.querySelector('.tier');t.className='tier t'+tier;t.textContent=`P${tier} ${TNAME[tier]}`;t.title=reason?status.title:it.tier===1?'Notify Commander':'Image priority';
 el.querySelector('.bx').innerHTML=boxesHTML(it,false);el.querySelector('.cl').innerHTML=topLine(it);
 el.querySelector('.cs').textContent=it.central.toFixed(2);
 if(it.live){const ft=el.querySelector('.formtag');ft.textContent=it.live.imageForm?FORM_NAME[it.live.imageForm]:'Summary only';ft.title=it.live.imageForm?`Image shown is the ${FORM_NAME[it.live.imageForm]} (${fmtBytes(it.live.forms[it.live.imageForm]?.bytes||0)}). Hover the image-version labels below for transfer status.`:'Only the object summary has arrived. Images follow as bandwidth becomes available.';el.querySelector('.fchips').innerHTML=formChipsHTML(it)}
 const dl=it.central-it.base,de=el.querySelector('.delta');de.className='delta '+(dl>.04?'up':dl<-.04?'dn':'');de.textContent=dl>.04?`▲${dl.toFixed(2).slice(1)}`:dl<-.04?`▼${(-dl).toFixed(2).slice(1)}`:'';
}
function renderFeed(noAnim){
 const{l,counts}=visible();renderTierSeg(counts);$('empty').style.display=l.length?'none':'block';
 if(state.page!=='feed')noAnim=true;
 const grid=$('grid'),first=new Map();
 if(!noAnim)[...grid.children].forEach(c=>first.set(c.dataset.id,c.getBoundingClientRect()));
 const keep=new Set(l.map(i=>i.id));[...grid.children].forEach(c=>{if(!keep.has(c.dataset.id))c.remove()});
 l.forEach((it,idx)=>{const el=it.el||makeCard(it);updateCard(it);if(grid.children[idx]!==el)grid.insertBefore(el,grid.children[idx]||null)});
 if(!noAnim){const vh=$('feed').clientHeight+200;l.forEach(it=>{const el=it.el,f=first.get(it.id),r=el.getBoundingClientRect();if(r.top>vh&&(!f||f.top>vh))return;
  if(f){const dx=f.left-r.left,dy=f.top-r.top;if(Math.abs(dx)>1||Math.abs(dy)>1){el.style.transition='none';el.style.transform=`translate(${dx}px,${dy}px)`;el.offsetWidth;el.style.transition='transform .5s cubic-bezier(.2,.8,.2,1)';el.style.transform=''}}
  else if(it.fresh){el.classList.remove('enter');el.offsetWidth;el.classList.add('enter')}})}
 renderDirBar();if(LIVE)renderCameras();
}
function renderDirBar(){
 const d=state.directives;$('dirCount').textContent=d.length||'';
 if(LIVE){const p=LIVE.policy||{};$('dirBar').innerHTML=d.length?`<span>Standing directive:</span><span class="pill" title="${esc(p.text||'')}">${esc(p.text||p.readback||'')}</span><span class="readback" title="What your words mean: the priorities the ship will apply">→ ${esc(p.readback||'')}</span><a href="#reprioritize">Edit</a>`:`<span>No standing directive — ranked by ${esc(LIVE.prioritization?.label||'the ship scoring model')}</span><a href="#reprioritize">Set priorities →</a>`;return}
 $('dirBar').innerHTML=d.length?`<span>Directives:</span>${d.slice(0,2).map(x=>`<span class="pill" title="${esc(x.text)}">${esc(x.text)}</span>`).join('')}${d.length>2?`<span>+${d.length-2}</span>`:''}<a href="#reprioritize">Edit</a>`:`<span>Base model only</span><a href="#reprioritize">Set priorities →</a>`;
}
function renderStats(){
 const vis=items.filter(i=>state.src==='ALL'||i.src.id===state.src);
 $('hsImgs').textContent=vis.length;$('hsP1').textContent=vis.filter(i=>i.tier===1).length;$('hsCdr').textContent=cdr;
 const unrevLive=vis.filter(i=>!i.reviewed&&!i.dismissed).length;
 if(LIVE){renderLiveStrip();$('rvCount').textContent=unrevLive||'';if(state.page==='rv')rvProgress();return}
 $('cCap').textContent=stats.cap.toLocaleString();$('cKeep').textContent=stats.keep;$('cDrop').textContent=stats.drop.toLocaleString();
 $('cKB').textContent=(stats.bytes/1024).toFixed(1);$('cDb').textContent=(stats.db+180000).toLocaleString();$('cP1').textContent=items.filter(i=>i.tier===1).length;
 $('cModel').textContent=(MODELS[state.model]?.name||'').replace(' (default)','')+(state.directives.length?` + ${state.directives.length} directive${state.directives.length>1?'s':''}`:'');
 const unrev=vis.filter(i=>!i.reviewed&&!i.dismissed).length;$('rvCount').textContent=unrev||'';if(state.page==='rv')rvProgress();
 $('cSaved').textContent=Math.round(stats.drop/Math.max(1,stats.cap)*100)+'%';
}

/* ---- live shore strip + cameras ---- */
function renderLiveStrip(){
 const L=LIVE;if(!L)return;const sim=$('simStrip'),el=$('liveStrip');if(sim)sim.hidden=true;if(!el)return;el.hidden=false;
 const {shore,edge,link,counts,policy,prioritization}=L;
 const rate=link.controllable?link.bps:(edge.ok?edge.estimateBps:shore.estimateBps)||0;
 const est=edge.ok?edge.estimateBps:shore.estimateBps;
 const relearn=edge.ok&&edge.consecutiveFailures>0?` · estimating speed again: ${edge.consecutiveFailures} failed transfer${edge.consecutiveFailures===1?'':'s'}`:'';
 const d=link.distribution;
 const distribution=d?`<span class="hint">Automatic · normal · ${fmtRate(d.minBps)}–${fmtRate(d.maxBps)} · every ${d.intervalMs/1000}s</span>`:'';
 const linkTitle=d?`Simulated bandwidth follows a normal distribution: mean ${fmtRate(d.meanBps)}, standard deviation ${fmtRate(d.stdDevBps)}, clipped to ${fmtRate(d.minBps)}–${fmtRate(d.maxBps)}. A new rate is applied to the actual ship-to-shore link every ${d.intervalMs/1000} seconds. The ship's measured estimate may take several transfers to catch up.`:'Ship-to-shore speed is controlled outside this console. Showing the measured estimate.';
 const sightingMode=counts.grouped>0;
 const objsShown=sightingMode?counts.sightings:counts.objects,objsImg=sightingMode?counts.sightingsWithImage:counts.objectsWithImage;
 const preTrack=sightingMode?Math.max(0,counts.objects-counts.sightings):0;
 const dropPct=edge.ok&&(edge.itemsTotal+edge.itemsDeduped)>0?Math.round(100*edge.itemsDeduped/(edge.itemsTotal+edge.itemsDeduped)):0;
 el.innerHTML=`
  <div class="st link" title="${esc(linkTitle)}">Ship-to-shore connection <b>${fmtRate(rate)}</b>${distribution}${link.controllable&&est?`<span class="hint">ship estimates ${fmtRate(est)}${relearn}</span>`:''}</div><span class="ar">›</span>
  <div class="st" title="Ship records include detected objects and camera overview updates. Repeated views of an already reported object stay on the ship to save bandwidth. Waiting records and bytes have not yet been sent.">On ship ${edge.ok?`<b>${edge.itemsTotal}</b> records · <b>${edge.itemsDeduped||0}</b> repeated views kept on ship · waiting <b>${edge.queuedItems}</b> (${fmtBytes(edge.queuedBytes||0)})`:`<b>${edge.error?'n/a':'—'}</b>`}</div><span class="ar">›</span>
  <div class="st" title="Records received on shore include object summaries and camera overview updates. Each tracked object has one card. Cards without an image show only a summary until image data arrives.${preTrack?` ${preTrack} earlier record${preTrack===1?'':'s'} of the same objects are combined.`:''}">On shore <b>${shore.ok?shore.itemsTotal:'—'}</b> records · <b>${objsShown}</b> detected objects (<b>${objsImg}</b> with image) · <b>${counts.scenes||0}</b> camera updates${shore.ok?'':` <span class="bad">${esc(shore.error||'unreachable')}</span>`}</div><span class="ar">›</span>
  <div class="st" title="Image and summary data sent over the connection${link.controllable?` (shore itself counted ${fmtBytes(shore.bytes||0)})`:''}. Compare with what ${edge.itemsDeduped||0} repeated views would have cost to send.">Data transferred <b>${fmtBytes(link.controllable?link.bytes:shore.bytes||0)}</b></div><span class="ar">›</span>
  <div class="st" title="${esc(policy.text?`Operator wrote: “${policy.text}”. The shore read it back as: ${policy.readback}`:policy.readback||'')}">Directive <b class="rb">${esc(policy.text?policy.readback:'none')}</b>${policy.emcon&&policy.emcon.mode&&policy.emcon.mode!=='NORMAL'?` · Transmission mode <b>${esc(policy.emcon.mode)}</b>`:''}</div><span class="ar">›</span>
  <div class="st" id="prioMode" title="Scoring model used on the ship. P1 Critical, P2 High, P3 Routine and P4 Low divide the current shore feed into priority groups (top 10%, next 25%, next 35%, then the rest). Deprioritized objects are P4 Low.">Scoring model <b>${esc(prioritization?.label||'Ship score')}</b>${prioritization?.source==='edge'?'':' <span class="hint">(built-in)</span>'}</div>
  <span class="sv" title="Share of all camera views that stayed on the ship because they repeated an object already reported. This saves bandwidth.">${dropPct}% of camera views stayed on the ship (repeats) to save bandwidth</span>`;
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
  return`<tr class="${f.state}${l.imageForm===k?' shown':''}"><td>${FORM_NAME[k]}${l.imageForm===k?' <small>· shown</small>':''}</td><td>${esc(st)}</td></tr>`}).join('');
 return`<div class="dsec live-ladder"><h4>Image versions · shore record ${esc(l.itemID||l.itemId||'')}</h4>
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
function openDetail(id){const prev=items.find(i=>i.id===state.sel);state.sel=id;document.body.classList.add('drawer-open');prev&&updateCard(prev);const it=items.find(i=>i.id===id);it&&updateCard(it);renderDetail();$('drawer').scrollTop=0;it&&it.el&&it.el.scrollIntoView({block:'nearest',behavior:'smooth'})}
function openZoom(){document.activeElement?.blur();state.zoom=true;renderZoom()}
function closeZoom(){state.zoom=false;const z=$('zoom');z.hidden=true;z.innerHTML=''}
function renderZoom(){preserveBandFocus(renderZoomContent)}
function renderZoomContent(){const it=items.find(i=>i.id===state.sel);if(!it){closeZoom();return}const z=$('zoom');z.hidden=false;
 z.innerHTML=`<div class="zbar"><span class="tier t${it.tier}" title="${it.tier===1?'Notify Commander':'Image priority'}">P${it.tier} ${TNAME[it.tier]}</span><b>${esc(it.src.name)}</b><span class="mono">${zulu(it.ts)}</span><span>Stored image #${it.dbId}</span><span class="zk">← → step · 1–4 set priority · Esc / click to close</span></div><div class="zframe">${largeImage(it)}</div>`;z.onclick=closeZoom}
function closeDetail(){closeZoom();const prev=items.find(i=>i.id===state.sel);state.sel=null;document.body.classList.remove('drawer-open');prev&&updateCard(prev)}
function step(dir){const l=visible().l;const i=l.findIndex(x=>x.id===state.sel);const n=l[Math.max(0,Math.min(l.length-1,i+dir))];if(n)openDetail(n.id)}
function buildWhy(it,rows){
 const why=[...liveWhy(it),...gsLights.why(it)];const top=rows[0];
 rows.filter(([d])=>d.cls==='unknown').forEach(([d])=>why.push({c:d.attrs.transient?'dn':'al',t:d.attrs.transient?'Single-frame flicker, treated as noise':'Unclassified contact persisted across frames — not noise; prioritized as potentially important'+(d.attrs.approaching?' · approaching, urgent':d.attrs.dark?' · no AIS / transponder correlation, urgent':'')}));
 if(top)why.push({c:top[1].s>.6?'al':'',t:`Main object: <b>${esc(clsOf(top[0].cls).lab)}</b> (confidence ${top[0].conf.toFixed(2)}) contributes ${top[1].s.toFixed(2)} under ${MODELS[state.model].name}`});
 rows.forEach(([d])=>{if(d.attrs.dark&&!['other','clutter'].includes(clsOf(d.cls).g))why.push({c:'al',t:`${esc(clsOf(d.cls).lab)} has no AIS / transponder correlation`});if(d.attrs.approaching)why.push({c:'al',t:`${esc(clsOf(d.cls).lab)} track is inbound toward the sensor`});if(d.attrs.new)why.push({c:'',t:`Change detection: ${esc(clsOf(d.cls).lab)} not present in previous pass`})});
 const seen=new Set();rows.forEach(([d,sd])=>sd.notes.forEach(r=>{const k=ruleLabel(r);if(seen.has(k))return;seen.add(k);const dir=state.directives.find(x=>x.id===r.directiveId);why.push({c:r.type==='boost'?'up':'dn',t:`${r.type==='boost'?'Boosted':'Suppressed'} by directive “${dir?esc(dir.text):''}” (${k.slice(2)})`})}));
 if(rows.some(([,sd])=>sd.focus))why.push({c:'dn',t:'Objects outside your active directives receive lower scores (focus mode)'});
 if(!it.live&&it.edge<it.central-.15)why.push({c:'up',t:`Shore scoring raised the ship’s priority estimate (${it.edge.toFixed(2)} → ${it.central.toFixed(2)})`});
 const _w=new Set();for(let i=why.length-1;i>=0;i--){if(_w.has(why[i].t))why.splice(i,1);else _w.add(why[i].t)}
 return why;
}
function renderDetail(){preserveBandFocus(renderDetailContent)}
function renderDetailContent(){
 const it=items.find(i=>i.id===state.sel);const D=$('drawer');
 if(!it){D.innerHTML='';return}
 const rows=it.dets.map((d,i)=>[d,it.det[i]]).sort((a,b)=>b[1].s-a[1].s);
 const why=buildWhy(it,rows);
 const fmtMs=ms=>ms>=1000?(ms/1000).toFixed(1)+' s':ms+' ms';const dl=it.central-it.base;
 const l=visible().l,idx=l.findIndex(x=>x.id===it.id);
 D.innerHTML=`<div class="dhead"><div><h2><span class="tier t${it.tier}" title="${it.tier===1?'Notify Commander':'Image priority'}">P${it.tier} ${TNAME[it.tier]}</span></h2><small>${esc(it.src.name)} · <span class="mono" title="UTC time (Zulu)">${zulu(it.ts)}</span> · Stored image #${it.dbId}${idx>=0?` · ${idx+1} of ${l.length}`:''}</small></div>
  <div class="dnav"><button id="dPrev" title="Previous (←)">‹</button><button id="dNext" title="Next (→)">›</button><button id="dClose" title="Close (Esc)">×</button></div></div>
 <div class="dimg" id="dImg" title="Enlarge (F)">${largeImage(it)}<span class="zhint">⤢ Enlarge · F</span></div>
 <div class="dbody">
  <div class="lbl" style="margin-bottom:6px">Set priority · keys 1–4${it.manual?' · <span style="color:var(--accent);text-transform:none;letter-spacing:0">operator override</span>':''}</div><div class="tierbtns">${[1,2,3,4].map(t=>`<button class="btn tb${t} ${it.tier===t?'on':''}" data-t="${t}">P${t} ${TNAME[t]}</button>`).join('')}<button class="btn" id="dReset" title="Reset to model" ${it.manual?'':'disabled style="opacity:.4"'}>↺</button></div>
  <div class="scores"><div class="sc" title="${it.live?'Ship score: importance relative to the highest score on shore':'Edge score: the ship’s initial estimate of importance'}"><span>Ship score</span><b>${it.edge.toFixed(2)}</b></div><div class="sc" title="${it.live?'Shore score: relative importance after applying your directive':'Command score: importance after shore scoring and directives'}"><span>Shore score</span><b style="color:${tcol(it.tier)}">${it.central.toFixed(2)}</b></div><div class="sc"><span title="How much active directives changed the base score">Change from directives</span><b style="color:${dl>.04?'var(--accent)':dl<-.04?'#8d99a5':'var(--text)'}">${dl>=0?'+':''}${dl.toFixed(2)}</b></div></div>
  <div class="dsec"><h4>Detections</h4><table><tr><th>Object</th><th>Behavior</th><th class="n" title="Confidence: how sure the computer is about the object label">Confidence</th><th class="n">Score</th></tr>${rows.map(([d,sd])=>{const ship=shipLine(d),visible=shipVisibleLine(d);return`<tr><td>${sd.matched?'<span style="color:var(--accent)">●</span> ':sd.sup?'<span style="color:#8d99a5">○</span> ':''}${esc(clsOf(d.cls).lab)}${ship?`<small class="shipline">${ship}</small>`:''}${visible?`<small class="shipfeatures">${visible}</small>`:''}</td><td style="color:var(--muted)">${Object.keys(ATTR_LAB).filter(a=>d.attrs[a]&&!(a==='dark'&&['other','clutter'].includes(clsOf(d.cls).g))).map(a=>ATTR_LAB[a]).join(', ')||'—'}</td><td class="n">${d.conf.toFixed(2)}</td><td class="n" style="color:${sd.s>=.6?'var(--p1)':sd.s>=.3?'var(--p2)':'var(--muted)'}">${sd.s.toFixed(2)}</td></tr>`}).join('')}</table></div>
  ${gsLights.section(it,CLS)}
  <div class="dsec" id="briefDetections"></div>
  <div class="dsec"><h4>Why this priority</h4><ul class="why">${why.slice(0,7).map(w=>`<li class="${w.c}">${w.t}</li>`).join('')}</ul></div>
  ${it.live?liveLadderHTML(it):`<div class="dsec"><h4>Processing chain</h4><ul class="tl">
   <li class="edge"><i></i><div>Captured &amp; filtered on the ship<br><small>${it.src.hw} · ${it.src.em} · score ${it.edge.toFixed(2)} ≥ 0.20 → keep</small></div><span class="ms">${fmtMs(it.lat.edge)}</span></li>
   <li><i></i><div>Sent to shore<br><small>${it.src.link} · ${it.fullRes?`original, full size, no compression · operator override by ${esc(it.fullResBy||'')}`:`${it.kb} KB JPEG + metadata`}</small></div><span class="ms">${fmtMs(it.lat.up)}</span></li>
   <li><i></i><div>Stored in imagery database<br><small>record ${it.dbId} · frame ${it.frame}</small></div><span class="ms">12 ms</span></li>
   <li><i></i><div>Shore priority scoring<br><small>${MODELS[state.model].name}${state.directives.length?` + ${state.directives.length} directive${state.directives.length>1?'s':''}`:''}</small></div><span class="ms">${fmtMs(it.lat.cen)}</span></li>
  </ul></div>`}
  <div class="actions"><button class="btn cdr ${it.sent?'done':''}" id="aCdr">${it.sent?'✓ Sent to Commander':'Send to Commander'}</button><button class="btn ${it.fullRes?'done':''}" id="aFull" title="Operator override: send the original image at full size, no compression">${it.fullRes?'✓ Full size requested':'Send full size'}</button><button class="btn" id="aSim">Prioritize similar</button><button class="btn" id="aDis" title="Hide this image from the feed and mark it reviewed. The image remains stored.">Not relevant</button></div>
  <div class="kbd"><kbd>←</kbd> <kbd>→</kbd> step through feed · <kbd>Esc</kbd> close</div>
 </div>`;
 D.querySelectorAll('.tierbtns [data-t]').forEach(b=>b.onclick=()=>setManual(it.id,+b.dataset.t));$('dReset').onclick=async()=>{try{await patchItem(it.id,{manual:0,reviewed:false})}catch(error){toast(error.message)}};$('dPrev').onclick=()=>step(-1);$('dNext').onclick=()=>step(1);$('dClose').onclick=closeDetail;$('dImg').onclick=openZoom;if(state.zoom)renderZoom();
 briefing.renderDetections(it);
 $('aCdr').onclick=async()=>{if(it.sent)return;try{await patchItem(it.id,{sent:true});toast('Image and detections pushed to Commander queue')}catch(error){toast(error.message)}};
 $('aFull').onclick=()=>requestFullRes(it);
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
 if(it.fullRes)return;
 const by=prompt('Operator override: send this image at FULL SIZE with NO compression.\nThis uses much more link bandwidth than the usual thumbnail/crop.\n\nEnter your name or operator ID to confirm:');
 if(by===null)return;
 if(!by.trim()){toast('Not sent: enter your name or operator ID in the confirmation box');return}
 try{await request(`/api/items/${encodeURIComponent(it.id)}/full-res`,'POST',{by:by.trim()});toast(`Full-size send requested by ${by.trim()}`)}catch(error){toast(error.message)}
}
async function setManual(id,t){if(!findItem(id))return;try{await patchItem(id,{manual:t,reviewed:true});toast(`Operator set P${t} ${TNAME[t]} — logged as model feedback`)}catch(error){toast(error.message)}}
function toggleNight(){if(window.gsSettings)gsSettings.toggleRed();else document.body.classList.toggle('night')}
const TNAME=['','Critical','High','Routine','Low'];
const RVKEYS={'1':'1','2':'2','3':'3','4':'4',' ':'accept','enter':'accept','x':'dismiss','delete':'dismiss','c':'cdr','arrowright':'skip','s':'skip','z':'undo','backspace':'undo'};
let rv={cur:null,shown:null,order:'pri',done:0,agree:0,undo:[],busy:false,skipped:{},ask:null,askBusy:false};
const teamSync={available:false,on:false,proposals:[],hidden:new Set(),resolved:new Set(),local:new Map(),accepting:new Map(),revision:0,posting:false};
function setupTeamSync(available){
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
 try{const data=await request('/api/review-sync/proposals','POST',{cls:s.cls,dir:s.dir,n:s.n,of:s.of,highRisk:!!s.highRisk,reviewer:reviewerName});receiveTeamProposal(data.proposal)}
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
function rvQueue(){const l=items.filter(i=>(state.src==='ALL'||i.src.id===state.src)&&!i.reviewed&&!i.dismissed);l.sort((a,b)=>(rv.skipped[a.id]||0)-(rv.skipped[b.id]||0)||(rv.order==='pri'?a.tier-b.tier||b.central-a.central:b.ts-a.ts));return l}
function rvProgress(){const left=rvQueue().length;$('rvDone').textContent=rv.done;$('rvLeft').textContent=left;$('rvBarI').style.width=(rv.done/Math.max(1,rv.done+left)*100)+'%';$('rvSrc').textContent='· '+(state.src==='ALL'?'all sources':SRC[state.src].name)+(rv.done?` · agreed with model on ${Math.round(rv.agree/rv.done*100)}%`:'');if(!rv.cur&&left&&!rv.busy)renderReview()}
function renderAsk(){
 const box=$('rvAsk');if(!box)return;
 if(!rv.ask&&teamSync.on){const p=teamSync.proposals.find(p=>!teamSync.hidden.has(p.id));if(p)rv.ask={kind:'sug',s:p,team:p.id}}
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
 box.innerHTML=`<div class="rvask-copy"><b>You ${verb} ${s.n} of the last ${s.of} images where ${esc(label)} was the top contact. ${action} ${esc(label)} priority?</b>${s.highRisk?`<span class="rvask-warning">${esc(label)} is normally high priority — confirm this change.</span>`:''}</div><div class="rvask-actions"><button class="kb rvask-btn" data-ask="sug" data-answer="yes" ${rv.askBusy?'disabled':''}><kbd>Y</kbd>Yes</button><button class="kb rvask-btn" data-ask="sug" data-answer="no" ${rv.askBusy?'disabled':''}><kbd>N</kbd>No</button><button class="kb rvask-btn rvask-skip" data-ask="sug" data-answer="later" ${rv.askBusy?'disabled':''}><kbd>L</kbd>Later</button></div>`;
 if(rv.ask.team){
  const p=teamSync.proposals.find(p=>p.id===rv.ask.team)||s;
  box.innerHTML=`<div class="rvask-team-banner">TEAM DISCUSSION — proposed by ${esc(p.proposedBy)}. Discuss with the team before accepting.</div><div class="rvask-copy"><b>${action} ${esc(label)} priority?</b><span>${esc(p.proposedBy)} ${verb} ${s.n} of the last ${s.of} images where ${esc(label)} was the top contact.</span>${s.highRisk?'<span class="rvask-warning">Normally high priority — confirm this change.</span>':''}</div><ul class="rvask-votes">${Object.entries(p.votes).map(([name,vote])=>`<li class="${name===reviewerName?'own-vote':''}">${esc(name)}: ${esc(vote)}${name===reviewerName?' (you)':''}</li>`).join('')||'<li>No votes yet — discuss with your team.</li>'}</ul><div class="rvask-actions">${[['Y','Agree','agree'],['N','Disagree','disagree'],['A','Accept for team','accepted'],['R','Reject for team','rejected'],['L','Later','later']].map(([key,label,answer])=>`<button class="kb rvask-btn" data-ask="team" data-answer="${answer}" ${rv.askBusy?'disabled':''}><kbd>${key}</kbd>${label}</button>`).join('')}</div>`;
 }
}
function checkLearningSuggestion(){
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
 return request('/api/directives','POST',{text:`Operator feedback: ${s.dir==='lower'?'lower':'raise'} ${label}`,rules:[{type:s.dir==='lower'?'suppress':'boost',classes:[s.cls],attrs:[],only:false}]});
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
function renderReview(){preserveBandFocus(renderReviewContent)}
function renderReviewContent(){
 setupReviewData();
 let it=rv.cur;if(!it||it.reviewed||it.dismissed||!items.includes(it))it=rv.cur=rvQueue()[0]||null;
 rvProgress();renderAsk();
 [...$('rvKeys').children].forEach(b=>{if(b.dataset.a)b.disabled=!reviewerName||(!it&&b.dataset.a!=='undo')});
 if(!it){rv.shown=null;$('rvMain').innerHTML=`<div class="rvdone"><div class="ck">✓</div><b>All caught up</b><div>New images will appear here as sensors report.</div></div><div class="rvflash" id="rvFlash"></div>`;return}
 const key=it.id+':'+it.centralTier+':'+it.central;if(rv.shown===key)return;rv.shown=key;
 const sug=it.centralTier,rows=it.dets.map((d,i)=>[d,it.det[i]]).sort((a,b)=>b[1].s-a[1].s),why=buildWhy(it,rows).slice(0,4);
 const age=Math.max(0,Math.round((Date.now()-it.ts)/60000));
 const classifications=rows.filter(([d])=>d.ship).slice(0,3).map(([d])=>shipLine(d));
 [...$('rvKeys').querySelectorAll('[data-a]')].forEach(b=>b.classList.toggle('sugg',b.dataset.a===String(sug)));
 $('accLbl').textContent=`Agree with suggested priority · P${sug} ${TNAME[sug]}`;
 $('rvMain').innerHTML=`<div class="rvimg"><div class="rvframe p${sug}">${largeImage(it)}</div></div>
 <div class="rvside">
  <div class="rvcard"><div class="lbl">Model suggests</div><div class="rvsug"><span class="tier t${sug}">P${sug} ${TNAME[sug]}</span></div><div class="rvsub" title="Command score is the shore-side priority estimate; edge score is the ship-side estimate.">Shore score ${it.central.toFixed(2)} · ship score ${it.edge.toFixed(2)}${state.directives.length?` · ${state.directives.length} directive${state.directives.length>1?'s':''} active`:''}</div></div>
  <div class="rvcard"><div class="lbl" style="margin-bottom:4px">Why</div><ul class="why">${why.map(w=>`<li class="${w.c}">${w.t}</li>`).join('')}</ul></div>
  <div class="rvcard rvmeta"><span>Source</span><b>${esc(it.src.name)}</b><span>Captured</span><b class="mono">${zulu(it.ts)} <span style="color:var(--muted);font-family:inherit;font-weight:500">(${age} min ago)</span></b><span>Objects</span><b>${rows.map(([d])=>esc(clsOf(d.cls).lab)).join(', ')||'—'}</b>${classifications.length?`<span>Classification</span><b class="rvclassification">${classifications.join('<br>')}</b>`:''}</div>
 </div><div class="rvflash" id="rvFlash"></div>`;
}
function rvFlash(msg,col){const f=$('rvFlash');if(!f)return;f.textContent=msg;f.style.color=col;f.classList.add('on');setTimeout(()=>f.classList.remove('on'),300)}
function rvPress(a){const b=$('rvKeys').querySelector(`[data-a="${a}"]`);if(!b)return;b.classList.add('pressed');setTimeout(()=>b.classList.remove('pressed'),140)}
function rvAct(a){
 if(!reviewerName){toast('Set your reviewer name first');return}
 if(rv.busy)return;rvPress(a);
 if(a==='undo'){const p=rv.undo[rv.undo.length-1];if(!p){toast('Nothing to undo');return}rv.busy=true;undoReview(p).then(it=>{rv.undo.pop();rv.done--;if(p.agreed)rv.agree--;if(typeof gsLearn!=='undefined')gsLearn.forget(p.id);rv.ask=null;rv.askBusy=false;renderAsk();rv.cur=it;rv.shown=null;rv.busy=false;renderReview();rvFlash('↶ Undone','#c3d1dd')}).catch(error=>{rv.busy=false;toast(error.message)});return}
 const it=rv.cur;if(!it)return;
 if(a==='skip'){rv.skipped[it.id]=Date.now();rv.cur=null;rv.shown=null;renderReview();return}
 const sug=it.centralTier,p={id:it.id,manual:it.manual,reviewed:it.reviewed,dismissed:it.dismissed,sent:it.sent,agreed:false};
 let msg,col;
 let changes={reviewed:true};
 if('1234'.includes(a)){changes.manual=+a;msg=`P${a} ${TNAME[+a]}`;col=tcol(+a);p.agreed=+a===sug}
 else if(a==='accept'){changes.manual=sug;msg=`✓ P${sug} ${TNAME[sug]}`;col=tcol(sug);p.agreed=true}
 else if(a==='dismiss'){changes.manual=4;changes.dismissed=true;msg='✕ Not relevant';col='#9aa7b3'}
 else if(a==='cdr'){changes.manual=1;changes.sent=true;msg='➤ Sent to Commander';col='var(--good)'}
 else return;
 rv.busy=true;
 submitReview(it.id,a,changes,p).then(updated=>{rv.done++;if(p.agreed)rv.agree++;rv.undo.push(p);if(rv.undo.length>50)rv.undo.shift();if(typeof gsLearn!=='undefined'){const result=gsLearn.record({id:it.id,src:it.src.id,dets:it.dets,det:it.det,sug,tier:changes.manual});if(result?.ambiguous){rv.ask={kind:'why',id:it.id,record:p.record,classes:result.ambiguous};rv.askBusy=false;renderAsk()}else checkLearningSuggestion()}rvFlash(msg,col);setTimeout(()=>{rv.busy=false;rv.cur=null;renderReview()},280)}).catch(error=>{rv.busy=false;toast(error.message)});
}
let tt;function toast(m){const t=$('toast');t.textContent=m;t.classList.add('on');clearTimeout(tt);tt=setTimeout(()=>t.classList.remove('on'),3200)}
let reviewerName='';try{reviewerName=(localStorage.getItem('gsReviewer')||'').trim().slice(0,80)}catch{}
let reviewDataReady=false,lastReview=null;
function refreshReviewData(){if(LIVE){$('reviewData').textContent='Durable review logging is unavailable in live shore mode.';return Promise.resolve()}return request('/api/reviews/summary').then(s=>{$('reviewData').textContent=`${s.count} reviews · ${Math.round(s.agreement*100)}% agreement · `+Object.entries(s.reviewers).map(([n,v])=>`${n}: ${v.count}`).join(', ')}).catch(error=>{$('reviewData').textContent=error.message})}
function setupReviewData(){if(reviewDataReady)return;reviewDataReady=true;$('reviewerName').value=reviewerName;$('reviewerStatus').textContent=reviewerName?`Reviewing as ${reviewerName}`:'Set a name to enable review keys';$('reviewerSave').onclick=()=>{reviewerName=$('reviewerName').value.trim().slice(0,80);try{localStorage.setItem('gsReviewer',reviewerName)}catch{}$('reviewerStatus').textContent=reviewerName?`Reviewing as ${reviewerName}`:'Set a name to enable review keys';renderReview()};$('reviewNoteOpen').onclick=openReviewNote;$('reviewNote').onkeydown=async e=>{e.stopPropagation();if(e.key==='Escape'){$('reviewNoteBox').hidden=true;return}if(e.key!=='Enter')return;e.preventDefault();try{await saveReviewReason(lastReview.id,lastReview.reason||'',$('reviewNote').value);lastReview.note=$('reviewNote').value;$('reviewNoteBox').hidden=true;toast('Note saved')}catch(error){toast(error.message)}};refreshReviewData()}
function openReviewNote(){if(!lastReview){toast('Review an image first');return}$('reviewNoteBox').hidden=false;$('reviewNote').value=lastReview.note||'';$('reviewNote').focus()}
async function submitReview(id,action,changes,p){if(LIVE)return patchItem(id,changes);const r=await request(`/api/items/${encodeURIComponent(id)}/review`,'POST',{reviewer:reviewerName,action:'1234'.includes(action)?'tier':action,tier:changes.manual});p.record=r.record.id;lastReview=r.record;cdr=r.cdr;applyItemUpdate(r.item);refreshReviewData();return findItem(id)}
async function undoReview(p){if(LIVE)return patchItem(p.id,{manual:p.manual,reviewed:p.reviewed,dismissed:p.dismissed,sent:p.sent});const r=await request(`/api/reviews/${encodeURIComponent(p.record)}/undo`,'POST',{reviewer:reviewerName});if(r.item){cdr=r.cdr;applyItemUpdate(r.item)}if(lastReview?.id===p.record)lastReview=null;refreshReviewData();return findItem(p.id)}
async function saveReviewReason(id,reason,note){if(note===undefined)note=lastReview?.id===id?lastReview.note||'':'';await request(`/api/reviews/${encodeURIComponent(id)}/reason`,'POST',{reviewer:reviewerName,reason,note});if(lastReview?.id===id){lastReview.reason=reason;lastReview.note=note}}
init();
