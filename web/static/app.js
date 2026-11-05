let SOURCES=[],SRC={},CLS={},MODELS={},ATTR_LAB={},EXAMPLES=[];
let state={src:'ALL',model:'maritime',sort:'pri',tier:'all',paused:false,sel:null,directives:[],policy:null};
let items=[],stats={cap:0,keep:0,drop:0,bytes:0,db:0},cdr=0,weightData={},pendingItems=[],evictedIDs=new Set(),heldSnapshotIDs=null,policyAuditTimer=null,policyRawTouched=false,policyRawLoaded=false;

function ruleLabel(r){
 const cl=r.classes.length?(r.classes.length>3?`${r.classes.slice(0,2).map(k=>CLS[k].lab).join(' / ')} +${r.classes.length-2}`:r.classes.map(k=>CLS[k].lab).join(' / ')):'Any object';
 const at=r.attrs.length?' · '+r.attrs.map(a=>ATTR_LAB[a]).join(' or '):'';
 return(r.type==='boost'?'▲ ':'▼ ')+cl+at;
}

/* ---------------- UI ---------------- */
state.dens='m';state.page='feed';
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
 return `<div class="band-controls" role="group" aria-label="Spectral band">${bands.map(b=>`<button type="button" data-band="${esc(b)}" data-band-item="${esc(it.id)}" aria-pressed="${b===band}" title="${b==='IR'?'Simulated LWIR · white-hot':esc(b)}">${esc(b)}</button>`).join('')}<span>B · cycle</span></div><span class="band-label">${esc(band)}${band==='IR'?' · white-hot · simulated LWIR':''}</span>`;
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
  if(!response.ok)throw new Error(data.error||`Request failed (${response.status})`);
  if(force||!policyRawTouched)$('polRaw').value=JSON.stringify(data.policy,null,2);
  $('polRawMsg').textContent='';$('polRawMsg').className='';
 }catch(error){$('polRawMsg').textContent=error.message;$('polRawMsg').className='policy-error'}
}
let adminToken='';// super-admin re-auth: operator token, kept in memory only (never stored)
async function rawPolicyAction(action){
 const msg=$('polRawMsg');msg.textContent='';msg.className='';
 try{
  if(!adminToken){adminToken=(window.prompt('Super admin: paste the operator token to authorize this policy edit')||'').trim();if(!adminToken)throw new Error('Operator token required for super admin edits')}
  const response=await fetch(`/api/policy${action==='validate'?'/validate':''}`,{method:action==='validate'?'POST':'PUT',headers:{'Content-Type':'application/json',Authorization:`Bearer ${adminToken}`},body:$('polRaw').value});
  const data=await response.json();
  if(response.status===401){adminToken='';throw new Error('Operator token rejected; try again')}
  if(!response.ok)throw new Error(data.error||`Request failed (${response.status})`);
  msg.className='policy-valid';msg.textContent=`Valid · ${(data.conflicts||[]).length} conflicts`;
  msg.insertAdjacentHTML('beforeend',gsPolicy.conflictsHTML(data.conflicts||[]));
  if(action==='apply')policyRawTouched=false;
 }catch(error){msg.textContent=error.message;msg.className='policy-error'}
}
function selectBand(it,band){
 const bands=it?bandsFor(it):[];if(bands.length<2||!bands.includes(band))return;
 selectedBands.set(it.id,band);
 if(state.page==='rv'){rv.shown=null;renderReview()}else if(state.sel===it.id)renderDetail();
}
function normalizeItem(item){
 item.ts=new Date(item.ts);item.src=SRC[typeof item.src==='string'?item.src:item.src.id];
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
 renderPolicySummary(data.policy);
 stats=data.stats;cdr=data.cdr;weightData=data.weights||{};
 state.sel=items.some(item=>item.id===selection)?selection:null;
 if(!state.sel){document.body.classList.remove('drawer-open');$('drawer').innerHTML='';closeZoom()}
 rv.cur=rv.cur&&items.find(item=>item.id===rv.cur.id)||null;
 renderAll();
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
 const response=await fetch(path,{method,headers:body?{'Content-Type':'application/json'}:{},body:body?JSON.stringify(body):undefined});
 const data=response.status===204?null:await response.json();
 if(!response.ok){const error=new Error(data?.error||`Request failed (${response.status})`);if(data?.conflicts?.length){error.policyConflicts=data.conflicts;toast(gsPolicy.conflictToast(data.conflicts))}throw error}
 return data;
}
async function patchItem(id,changes){
 const result=await request(`/api/items/${encodeURIComponent(id)}`,'PATCH',changes);
 cdr=result.cdr;applyItemUpdate(result.item);return findItem(id);
}
function renderLive(){
 const live=$('live');live.classList.toggle('off',!!state.offline);live.classList.toggle('paused',!state.offline&&state.paused);
 live.querySelector('span').textContent=state.offline?'RECONNECTING':state.paused?'FEED HELD':'FEED ACTIVE';
 const action=state.paused?'resume':'hold';
 live.title=state.offline?`Reconnecting — click to ${action}`:state.paused?'Feed held — click to resume':'Feed active — click to hold';
 live.setAttribute('aria-label',live.title);live.setAttribute('aria-pressed',String(!!state.paused));
 $('pauseBtn').textContent=state.paused?'Resume':'Hold';$('wbHeld').hidden=!state.paused;
}
function toggleHold(){
 state.paused=!state.paused;renderLive();
 if(!state.paused){if(heldSnapshotIDs){const keep=heldSnapshotIDs;for(const item of items)if(!keep.has(item.id)){imageCache.delete(item.id);if(state.sel===item.id)closeDetail();if(rv.cur?.id===item.id){rv.cur=null;rv.shown=null}}items=items.filter(item=>keep.has(item.id));pendingItems=pendingItems.filter(event=>keep.has(event.item.id));heldSnapshotIDs=null}for(const event of pendingItems)mergeItem(event.item);pendingItems=[];renderAll()}
}
function openStream(){
 const stream=new EventSource('/api/stream');
 stream.onopen=()=>{state.offline=false;renderLive()};
 stream.onerror=()=>{state.offline=true;renderLive()};
 stream.addEventListener('snapshot',event=>applySnapshot(JSON.parse(event.data)));
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
 stream.addEventListener('evict',event=>{
  const {id}=JSON.parse(event.data);evictedIDs.add(id);imageCache.delete(id);pendingItems=pendingItems.filter(data=>data.item.id!==id);
  items=items.filter(item=>item.id!==id);
  if(state.sel===id)closeDetail();
  if(rv.cur?.id===id){rv.cur=null;rv.shown=null}
  renderAll();
 });
}
async function init(){
 const config=await request('/api/config');
 SOURCES=[{id:'ALL',name:'All Sources (fused)'},...config.sources];
 SRC=Object.fromEntries(SOURCES.map(source=>[source.id,source]));
 CLS=config.classes;MODELS=config.models;ATTR_LAB=config.attrLabels;EXAMPLES=config.examples;
 $('srcSel').innerHTML=SOURCES.map(s=>`<option value="${s.id}">${s.name}</option>`).join('');
 $('modelSel').innerHTML=Object.entries(MODELS).map(([k,m])=>`<option value="${k}">${m.name}</option>`).join('');
 $('examples').innerHTML=EXAMPLES.map(e=>`<span class="ex">${e}</span>`).join('');
 $('examples').onclick=e=>{if(e.target.classList.contains('ex')){$('nlIn').value=e.target.textContent;workbench.textChanged()}};
 $('srcSel').onchange=e=>{state.src=e.target.value;closeDetail();renderFeed(true);renderStats();renderRp()};
 $('modelSel').onchange=async e=>{try{await request('/api/model','PUT',{id:e.target.value});toast(`Base model set to ${MODELS[e.target.value].name}`)}catch(error){toast(error.message);e.target.value=state.model}};
 workbench.init();
 briefing.init();
 $('clearDir').onclick=async()=>{if(!state.directives.length)return;try{await request('/api/directives','DELETE');toast('Directives cleared — base model restored')}catch(error){toast(error.message)}};
 $('sortSeg').onclick=e=>{const b=e.target.closest('button');if(!b)return;state.sort=b.dataset.s;[...$('sortSeg').children].forEach(x=>x.classList.toggle('on',x===b));renderFeed()};
 $('densSeg').onclick=e=>{const b=e.target.closest('button');if(!b)return;state.dens=b.dataset.d;[...$('densSeg').children].forEach(x=>x.classList.toggle('on',x===b));$('grid').className='grid '+state.dens};
 $('tierSeg').onclick=e=>{const b=e.target.closest('button');if(!b)return;state.tier=b.dataset.t;renderFeed()};
 $('pauseBtn').onclick=toggleHold;$('live').onclick=toggleHold;$('wbResume').onclick=toggleHold;
 $('emconControl').onclick=async e=>{const button=e.target.closest('#emconSeg [data-mode]');if(!button||button.disabled)return;try{const result=await request('/api/policy/emcon','POST',{mode:button.dataset.mode});if(result.conflicts?.length)toast(gsPolicy.conflictToast(result.conflicts))}catch(error){if(!error.policyConflicts)toast(error.message)}};
 $('polRaw').oninput=()=>{policyRawTouched=true};
 $('polRawValidate').onclick=()=>rawPolicyAction('validate');$('polRawApply').onclick=()=>rawPolicyAction('apply');$('polRawReload').onclick=()=>{policyRawTouched=false;loadRawPolicy(true)};
 $('grid').onclick=e=>{const c=e.target.closest('.card');if(c)openDetail(c.dataset.id)};
 document.addEventListener('click',e=>{const b=e.target.closest('[data-band-item]');if(!b)return;e.stopPropagation();selectBand(findItem(b.dataset.bandItem),b.dataset.band)},true);
 document.addEventListener('keydown',e=>{if(document.querySelector('dialog[open]')||$('settings').open)return;if(gsPolicy.isPolicyHotkeyTarget(e.target))return;const tg=e.target.tagName;if(tg==='TEXTAREA'||tg==='INPUT'||tg==='SELECT'||e.ctrlKey||e.metaKey||e.altKey)return;
  const k=e.key.toLowerCase();
  if((k==='enter'||k===' ')&&e.target.closest?.('header, .band-controls'))return;
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
 $('rvAsk').onclick=e=>{const b=e.target.closest('[data-ask]');if(!b)return;if(b.dataset.ask==='why')answerWhy(b.dataset.choice);else answerSuggestion(b.dataset.answer)};
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

function visible(){
 let l=items.filter(i=>(state.src==='ALL'||i.src.id===state.src)&&!i.dismissed);
 const counts={all:l.length,1:0,2:0,3:0,4:0};l.forEach(i=>counts[i.tier]++);
 if(state.tier!=='all')l=l.filter(i=>String(i.tier)===state.tier);
 l.sort(state.sort==='pri'?(a,b)=>a.tier-b.tier||b.central-a.central||b.ts-a.ts:(a,b)=>b.ts-a.ts);
 return{l,counts};
}
function renderTierSeg(counts){
 const T=[['all','All'],['1','P1'],['2','P2'],['3','P3'],['4','P4']];
 $('tierSeg').innerHTML=T.map(([k,l])=>`<button data-t="${k}" class="${state.tier===k?'on':''}">${k!=='all'?`<span class="tchip" style="background:${tcol(+k)}"></span>`:''}${l}<span class="c">${counts[k]}</span></button>`).join('');
}
function boxesHTML(it,labels){
 return it.dets.map((d,i)=>{const sd=it.det[i];const c=sd.sup?'sup':sd.matched?'match':sd.s>=.6?'hi':sd.s>=.3?'md':'lo';const b=d.b;
  return`<div class="box ${c}${d.cls==='unknown'?' unknown':''}${b.x+b.w/2>W*.6?' r':''}${b.y<H*.08?' t':''}" style="left:${b.x/W*100}%;top:${b.y/H*100}%;width:${b.w/W*100}%;height:${b.h/H*100}%">${labels?`<em>${CLS[d.cls].lab} ${Math.round(d.conf*100)}%</em>`:''}</div>`}).join('');
}
function topLine(it){
 const r=it.dets.map((d,i)=>[d,it.det[i]]).sort((a,b)=>b[1].s-a[1].s);if(!r.length)return'—';
 const[d,sd]=r[0];const tag=d.attrs.dark&&CLS[d.cls].g!=='other'&&CLS[d.cls].g!=='clutter'?' · no AIS':d.attrs.approaching?' · inbound':d.attrs.new?' · new':'';
 return`<span class="${sd.matched?'m':tag&&sd.s>.4?'a':''}">${CLS[d.cls].lab}${tag}</span>${r.length>1?` <span style="opacity:.6">+${r.length-1}</span>`:''}`;
}
function cardHTML(it,mini){
 return`<div class="thumb"><img src="${imageFor(it)}" alt=""><div class="bx">${mini?boxesHTML(it,false):''}</div><span class="tier t${it.tier}">P${it.tier}</span><span class="cdrtag">✓ FWD</span><span class="newtag">NEW</span><div class="ov"><span class="sid">${it.src.id}</span><span class="t mono">${zulu(it.ts).slice(0,5)}Z</span><span class="delta"></span><span class="rvk" style="margin-left:auto">✓</span><b class="cs" style="margin-left:4px">${mini?it.central.toFixed(2):''}</b></div></div><div class="cl"></div>`;
}
function makeCard(it){const el=document.createElement('div');el.dataset.id=it.id;el.innerHTML=cardHTML(it);if(bandsFor(it).length>1)el.querySelector('.thumb').insertAdjacentHTML('beforeend',`<span class="band-badge">${bandsFor(it).map(esc).join('/')}</span>`);it.el=el;return el}
function updateCard(it){
 const el=it.el;if(!el)return;el.className=`card p${it.tier}${it.fresh?' fresh':''}${it.sent?' sent':''}${it.reviewed?' rev':''}${it.manual?' man':''}${state.sel===it.id?' sel':''}`;
 el.querySelector('.night-badge')?.remove();el.querySelector('.thumb').insertAdjacentHTML('beforeend',gsLights.badge(it));
 const t=el.querySelector('.tier');t.className='tier t'+it.tier;t.textContent='P'+it.tier;
 el.querySelector('.bx').innerHTML=boxesHTML(it,false);el.querySelector('.cl').innerHTML=topLine(it);
 el.querySelector('.cs').textContent=it.central.toFixed(2);
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
 renderDirBar();
}
function renderDirBar(){
 const d=state.directives;$('dirCount').textContent=d.length||'';
 $('dirBar').innerHTML=d.length?`<span>Directives:</span>${d.slice(0,2).map(x=>`<span class="pill" title="${esc(x.text)}">${esc(x.text)}</span>`).join('')}${d.length>2?`<span>+${d.length-2}</span>`:''}<a href="#reprioritize">Edit</a>`:`<span>Base model only</span><a href="#reprioritize">Set priorities →</a>`;
}
function renderStats(){
 const vis=items.filter(i=>state.src==='ALL'||i.src.id===state.src);
 $('hsImgs').textContent=vis.length;$('hsP1').textContent=vis.filter(i=>i.tier===1).length;$('hsCdr').textContent=cdr;
 $('cCap').textContent=stats.cap.toLocaleString();$('cKeep').textContent=stats.keep;$('cDrop').textContent=stats.drop.toLocaleString();
 $('cKB').textContent=(stats.bytes/1024).toFixed(1);$('cDb').textContent=(stats.db+180000).toLocaleString();$('cP1').textContent=items.filter(i=>i.tier===1).length;
 $('cModel').textContent=MODELS[state.model].name.replace(' (default)','')+(state.directives.length?` + ${state.directives.length} directive${state.directives.length>1?'s':''}`:'');
 const unrev=vis.filter(i=>!i.reviewed&&!i.dismissed).length;$('rvCount').textContent=unrev||'';if(state.page==='rv')rvProgress();
 $('cSaved').textContent=Math.round(stats.drop/Math.max(1,stats.cap)*100)+'%';
}

/* ---- detail drawer ---- */
function openDetail(id){const prev=items.find(i=>i.id===state.sel);state.sel=id;document.body.classList.add('drawer-open');prev&&updateCard(prev);const it=items.find(i=>i.id===id);it&&updateCard(it);renderDetail();$('drawer').scrollTop=0;it&&it.el&&it.el.scrollIntoView({block:'nearest',behavior:'smooth'})}
function openZoom(){document.activeElement?.blur();state.zoom=true;renderZoom()}
function closeZoom(){state.zoom=false;const z=$('zoom');z.hidden=true;z.innerHTML=''}
function renderZoom(){preserveBandFocus(renderZoomContent)}
function renderZoomContent(){const it=items.find(i=>i.id===state.sel);if(!it){closeZoom();return}const z=$('zoom');z.hidden=false;
 z.innerHTML=`<div class="zbar"><span class="tier t${it.tier}">P${it.tier}</span><b>${it.src.name}</b><span class="mono">${zulu(it.ts)}</span><span>DB #${it.dbId}</span><span class="zk">← → step · 1–4 set priority · Esc / click to close</span></div><div class="zframe">${largeImage(it)}</div>`;z.onclick=closeZoom}
function closeDetail(){closeZoom();const prev=items.find(i=>i.id===state.sel);state.sel=null;document.body.classList.remove('drawer-open');prev&&updateCard(prev)}
function step(dir){const l=visible().l;const i=l.findIndex(x=>x.id===state.sel);const n=l[Math.max(0,Math.min(l.length-1,i+dir))];if(n)openDetail(n.id)}
function buildWhy(it,rows){
 const why=gsLights.why(it);const top=rows[0];
 rows.filter(([d])=>d.cls==='unknown').forEach(([d])=>why.push({c:d.attrs.transient?'dn':'al',t:d.attrs.transient?'Single-frame flicker, treated as noise':'Unclassified contact persisted across frames — not noise; prioritized as potentially important'+(d.attrs.approaching?' · approaching, urgent':d.attrs.dark?' · no AIS / transponder correlation, urgent':'')}));
 if(top)why.push({c:top[1].s>.6?'al':'',t:`Top contact: <b>${CLS[top[0].cls].lab}</b> (conf ${top[0].conf.toFixed(2)}) contributes ${top[1].s.toFixed(2)} under ${MODELS[state.model].name}`});
 rows.forEach(([d])=>{if(d.attrs.dark&&!['other','clutter'].includes(CLS[d.cls].g))why.push({c:'al',t:`${CLS[d.cls].lab} has no AIS / transponder correlation`});if(d.attrs.approaching)why.push({c:'al',t:`${CLS[d.cls].lab} track is inbound toward the sensor`});if(d.attrs.new)why.push({c:'',t:`Change detection: ${CLS[d.cls].lab} not present in previous pass`})});
 const seen=new Set();rows.forEach(([d,sd])=>sd.notes.forEach(r=>{const k=ruleLabel(r);if(seen.has(k))return;seen.add(k);const dir=state.directives.find(x=>x.id===r.directiveId);why.push({c:r.type==='boost'?'up':'dn',t:`${r.type==='boost'?'Boosted':'Suppressed'} by directive “${dir?esc(dir.text):''}” (${k.slice(2)})`})}));
 if(rows.some(([,sd])=>sd.focus))why.push({c:'dn',t:'Contacts not matching active directives are de-weighted (focus mode)'});
 if(it.edge<it.central-.15)why.push({c:'up',t:`Command model ranked this higher than edge triage (${it.edge.toFixed(2)} → ${it.central.toFixed(2)})`});
 const _w=new Set();for(let i=why.length-1;i>=0;i--){if(_w.has(why[i].t))why.splice(i,1);else _w.add(why[i].t)}
 return why;
}
function renderDetail(){preserveBandFocus(renderDetailContent)}
function renderDetailContent(){
 const it=items.find(i=>i.id===state.sel);const D=$('drawer');
 if(!it){D.innerHTML='';return}
 const rows=it.dets.map((d,i)=>[d,it.det[i]]).sort((a,b)=>b[1].s-a[1].s);
 const why=buildWhy(it,rows);
 const tierName=['','Critical — notify commander','High','Routine','Low'][it.tier];
 const fmtMs=ms=>ms>=1000?(ms/1000).toFixed(1)+' s':ms+' ms';const dl=it.central-it.base;
 const l=visible().l,idx=l.findIndex(x=>x.id===it.id);
 D.innerHTML=`<div class="dhead"><div><h2><span class="tier t${it.tier}">P${it.tier}</span>${tierName}</h2><small>${it.src.name} · <span class="mono">${zulu(it.ts)}</span> · DB #${it.dbId}${idx>=0?` · ${idx+1} of ${l.length}`:''}</small></div>
  <div class="dnav"><button id="dPrev" title="Previous (←)">‹</button><button id="dNext" title="Next (→)">›</button><button id="dClose" title="Close (Esc)">×</button></div></div>
 <div class="dimg" id="dImg" title="Enlarge (F)">${largeImage(it)}<span class="zhint">⤢ Enlarge · F</span></div>
 <div class="dbody">
  <div class="lbl" style="margin-bottom:6px">Set priority · keys 1–4${it.manual?' · <span style="color:var(--accent);text-transform:none;letter-spacing:0">operator override</span>':''}</div><div class="tierbtns">${[1,2,3,4].map(t=>`<button class="btn tb${t} ${it.tier===t?'on':''}" data-t="${t}">${t} · P${t}</button>`).join('')}<button class="btn" id="dReset" title="Reset to model" ${it.manual?'':'disabled style="opacity:.4"'}>↺</button></div>
  <div class="scores"><div class="sc"><span>Edge triage</span><b>${it.edge.toFixed(2)}</b></div><div class="sc"><span>Command score</span><b style="color:${tcol(it.tier)}">${it.central.toFixed(2)}</b></div><div class="sc"><span>Directive Δ</span><b style="color:${dl>.04?'var(--accent)':dl<-.04?'#8d99a5':'var(--text)'}">${dl>=0?'+':''}${dl.toFixed(2)}</b></div></div>
  <div class="dsec"><h4>Detections</h4><table><tr><th>Object</th><th>Behavior</th><th class="n">Conf</th><th class="n">Score</th></tr>${rows.map(([d,sd])=>{const ship=shipLine(d),visible=shipVisibleLine(d);return`<tr><td>${sd.matched?'<span style="color:var(--accent)">●</span> ':sd.sup?'<span style="color:#8d99a5">○</span> ':''}${CLS[d.cls].lab}${ship?`<small class="shipline">${ship}</small>`:''}${visible?`<small class="shipfeatures">${visible}</small>`:''}</td><td style="color:var(--muted)">${Object.keys(ATTR_LAB).filter(a=>d.attrs[a]&&!(a==='dark'&&['other','clutter'].includes(CLS[d.cls].g))).map(a=>ATTR_LAB[a]).join(', ')||'—'}</td><td class="n">${d.conf.toFixed(2)}</td><td class="n" style="color:${sd.s>=.6?'var(--p1)':sd.s>=.3?'var(--p2)':'var(--muted)'}">${sd.s.toFixed(2)}</td></tr>`}).join('')}</table></div>
  ${gsLights.section(it,CLS)}
  <div class="dsec" id="briefDetections"></div>
  <div class="dsec"><h4>Why this priority</h4><ul class="why">${why.slice(0,7).map(w=>`<li class="${w.c}">${w.t}</li>`).join('')}</ul></div>
  <div class="dsec"><h4>Processing chain</h4><ul class="tl">
   <li class="edge"><i></i><div>Captured &amp; triaged on sensor<br><small>${it.src.hw} · ${it.src.em} · score ${it.edge.toFixed(2)} ≥ 0.20 → keep</small></div><span class="ms">${fmtMs(it.lat.edge)}</span></li>
   <li><i></i><div>Uplinked to central<br><small>${it.src.link} · ${it.fullRes?`original, full size, no compression · operator override by ${esc(it.fullResBy||'')}`:`${it.kb} KB JPEG + metadata`}</small></div><span class="ms">${fmtMs(it.lat.up)}</span></li>
   <li><i></i><div>Stored in imagery database<br><small>record ${it.dbId} · frame ${it.frame}</small></div><span class="ms">12 ms</span></li>
   <li><i></i><div>Command reprioritization<br><small>${MODELS[state.model].name}${state.directives.length?` + ${state.directives.length} directive${state.directives.length>1?'s':''}`:''}</small></div><span class="ms">${fmtMs(it.lat.cen)}</span></li>
  </ul></div>
  <div class="actions"><button class="btn cdr ${it.sent?'done':''}" id="aCdr">${it.sent?'✓ Sent to Commander':'Send to Commander'}</button><button class="btn ${it.fullRes?'done':''}" id="aFull" title="Operator override: send the original image at full size, no compression">${it.fullRes?'✓ Full size requested':'Send full size'}</button><button class="btn" id="aSim">Prioritize similar</button><button class="btn" id="aDis">Dismiss</button></div>
  <div class="kbd"><kbd>←</kbd> <kbd>→</kbd> step through feed · <kbd>Esc</kbd> close</div>
 </div>`;
 D.querySelectorAll('.tierbtns [data-t]').forEach(b=>b.onclick=()=>setManual(it.id,+b.dataset.t));$('dReset').onclick=async()=>{try{await patchItem(it.id,{manual:0,reviewed:false})}catch(error){toast(error.message)}};$('dPrev').onclick=()=>step(-1);$('dNext').onclick=()=>step(1);$('dClose').onclick=closeDetail;$('dImg').onclick=openZoom;if(state.zoom)renderZoom();
 briefing.renderDetections(it);
 $('aCdr').onclick=async()=>{if(it.sent)return;try{await patchItem(it.id,{sent:true});toast('Image and detections pushed to Commander queue')}catch(error){toast(error.message)}};
 $('aFull').onclick=()=>requestFullRes(it);
 $('aSim').onclick=()=>{const k=rows[0]&&rows[0][0].cls;if(!k)return;$('nlIn').value=`Prioritize ${CLS[k].lab.toLowerCase()}s`;location.hash='#reprioritize';toast('Drafted a directive from this image — review and apply')};
 $('aDis').onclick=async()=>{const l=visible().l,i=l.indexOf(it),nx=l[i+1]||l[i-1];try{await patchItem(it.id,{dismissed:true,reviewed:true});nx&&findItem(nx.id)?openDetail(nx.id):closeDetail();toast('Image dismissed from feed (retained in DB)')}catch(error){toast(error.message)}};
}

/* ---- reprioritize page ---- */
function renderRp(light){
 workbench.live();
 if(light)return;
 renderWeights();renderDirectives();$('modelSel').value=state.model;$('modelDesc').textContent=MODELS[state.model].desc;
}
function renderWeights(){
 const rows=Object.entries(weightData).map(([k,weight])=>({k,base:weight.base,eff:weight.effective,cond:weight.conditional})).sort((a,b)=>b.eff-a.eff);
 $('wNote').textContent=state.directives.length?'Highlighted rows changed.':'';
 $('weights').innerHTML=rows.map(r=>{const c=r.eff>r.base+.02?'up':r.eff<r.base-.02?'down':'';return`<div class="wrow ${c}"><span>${CLS[r.k].lab}${r.cond?' <span style="color:var(--accent)">*</span>':''}</span><div class="bar"><i style="width:${r.eff*100}%"></i></div><span class="v">${r.eff.toFixed(2)}</span></div>`}).join('')+(rows.some(r=>r.cond)?'<div class="hint">* boosted further when its condition is met (e.g. approaching, no AIS)</div>':'');
}
function renderDirectives(){
 const d=state.directives;$('clearDir').style.display=d.length?'':'none';
 $('directives').innerHTML=d.length?d.map((x,i)=>`<div class="dir ${x.blocked?'policy-blocked':''}"><span class="n">${i+1}</span><div class="body"><q>${esc(x.text)}</q><div>${gsPolicy.directiveBadge(x)}</div><div class="rules">${x.rules.map(r=>`<span class="rule ${r.type==='suppress'?'sup':''}">${ruleLabel(r)}</span>`).join('')}${x.rules.some(r=>r.only)?'<span class="rule focus">Focus: others ×0.3</span>':''}</div></div>${x.source?'':`<button class="x" data-id="${esc(x.id)}" title="Remove directive">×</button>`}</div>`).join(''):'<div class="nodir">No directives — feed is ranked by the base model only.</div>';
 $('directives').querySelectorAll('.x').forEach(b=>b.onclick=async()=>{try{await request(`/api/directives/${encodeURIComponent(b.dataset.id)}`,'DELETE');toast('Directive removed')}catch(error){toast(error.message)}});
}
async function requestFullRes(it){
 if(it.fullRes)return;
 const by=prompt('Operator override: send this image at FULL SIZE with NO compression.\nThis uses much more link bandwidth than the usual thumbnail/crop.\n\nEnter your name or operator ID to confirm:');
 if(by===null)return;
 if(!by.trim()){toast('Not sent: operator name is required');return}
 try{await request(`/api/items/${encodeURIComponent(it.id)}/full-res`,'POST',{by:by.trim()});toast(`Full-size send requested by ${by.trim()}`)}catch(error){toast(error.message)}
}
async function setManual(id,t){if(!findItem(id))return;try{await patchItem(id,{manual:t,reviewed:true});toast(`Operator set P${t} — logged as model feedback`)}catch(error){toast(error.message)}}
function toggleNight(){if(window.gsSettings)gsSettings.toggleRed();else document.body.classList.toggle('night')}
const TNAME=['','Critical','High','Routine','Low'];
const RVKEYS={'1':'1','2':'2','3':'3','4':'4',' ':'accept','enter':'accept','x':'dismiss','delete':'dismiss','c':'cdr','arrowright':'skip','s':'skip','z':'undo','backspace':'undo'};
let rv={cur:null,shown:null,order:'pri',done:0,agree:0,undo:[],busy:false,skipped:{},ask:null,askBusy:false};
function rvQueue(){const l=items.filter(i=>(state.src==='ALL'||i.src.id===state.src)&&!i.reviewed&&!i.dismissed);l.sort((a,b)=>(rv.skipped[a.id]||0)-(rv.skipped[b.id]||0)||(rv.order==='pri'?a.tier-b.tier||b.central-a.central:b.ts-a.ts));return l}
function rvProgress(){const left=rvQueue().length;$('rvDone').textContent=rv.done;$('rvLeft').textContent=left;$('rvBarI').style.width=(rv.done/Math.max(1,rv.done+left)*100)+'%';$('rvSrc').textContent='· '+(state.src==='ALL'?'all sources':SRC[state.src].name)+(rv.done?` · agreed with model on ${Math.round(rv.agree/rv.done*100)}%`:'');if(!rv.cur&&left&&!rv.busy)renderReview()}
function renderAsk(){
 const box=$('rvAsk');if(!box)return;
 if(!rv.ask){box.hidden=true;box.innerHTML='';return}
 box.hidden=false;
 if(rv.ask.kind==='why'){
  const tier=findItem(rv.ask.id)?.tier||'?';
  box.innerHTML=`<div class="rvask-copy"><b>Why did you set the last image to P${tier}?</b><span>Two contacts scored close. Pick the one that drove your call.</span></div><div class="rvask-actions">${rv.ask.classes.map((cls,i)=>`<button class="kb rvask-btn" data-ask="why" data-choice="${esc(cls)}" ${rv.askBusy?'disabled':''}><kbd>${i+1}</kbd>${esc(CLS[cls]?.lab||cls)}</button>`).join('')}<button class="kb rvask-btn" data-ask="why" data-choice="wrong" ${rv.askBusy?'disabled':''}><kbd>${rv.ask.classes.length+1}</kbd>Wrong detection</button><button class="kb rvask-btn rvask-skip" data-ask="why" data-choice="skip" ${rv.askBusy?'disabled':''}><kbd>Esc</kbd>Skip</button></div>`;
  return;
 }
 const s=rv.ask.s,label=CLS[s.cls]?.lab||s.cls,verb=s.dir==='lower'?'lowered':'raised',action=s.dir==='lower'?'Lower':'Raise';
 box.innerHTML=`<div class="rvask-copy"><b>You ${verb} ${s.n} of the last ${s.of} images where ${esc(label)} was the top contact. ${action} ${esc(label)} priority?</b>${s.highRisk?`<span class="rvask-warning">${esc(label)} is normally high priority — confirm this change.</span>`:''}</div><div class="rvask-actions"><button class="kb rvask-btn" data-ask="sug" data-answer="yes" ${rv.askBusy?'disabled':''}><kbd>Y</kbd>Yes</button><button class="kb rvask-btn" data-ask="sug" data-answer="no" ${rv.askBusy?'disabled':''}><kbd>N</kbd>No</button><button class="kb rvask-btn rvask-skip" data-ask="sug" data-answer="later" ${rv.askBusy?'disabled':''}><kbd>L</kbd>Later</button></div>`;
}
function checkLearningSuggestion(){
 if(rv.ask||typeof gsLearn==='undefined')return;
 const s=gsLearn.suggestion();if(s){rv.ask={kind:'sug',s};rv.askBusy=false;renderAsk()}
}
function answerWhy(choice){
 const ask=rv.ask;if(!ask||ask.kind!=='why'||rv.askBusy||typeof gsLearn==='undefined')return;
 if(choice==='wrong'){gsLearn.attribute(ask.id,'wrong');toast('Marked as wrong detection — not used for rule suggestions')}
 else gsLearn.attribute(ask.id,choice==='skip'?null:choice);
 rv.ask=null;rv.askBusy=false;renderAsk();checkLearningSuggestion();
}
function answerSuggestion(answer){
 const ask=rv.ask;if(!ask||ask.kind!=='sug'||rv.askBusy||typeof gsLearn==='undefined')return;
 const s=ask.s;
 if(answer!=='yes'){gsLearn.resolve(s,answer);rv.ask=null;rv.askBusy=false;renderAsk();return}
 rv.askBusy=true;renderAsk();
 const label=CLS[s.cls]?.lab||s.cls;
 request('/api/directives','POST',{text:`Operator feedback: ${s.dir==='lower'?'lower':'raise'} ${label}`,rules:[{type:s.dir==='lower'?'suppress':'boost',classes:[s.cls],attrs:[],only:false}]})
  .then(result=>{gsLearn.resolve(s,'yes');toast(result.conflicts?.length?gsPolicy.conflictToast(result.conflicts):'Rule added — review it on Collection Priorities')})
  .catch(error=>{gsLearn.resolve(s,'later');if(!error.policyConflicts)toast(error.message)})
  .finally(()=>{rv.askBusy=false;if(rv.ask===ask)rv.ask=null;renderAsk()});
}
function reviewAskKey(k){
 if(!rv.ask||rv.askBusy)return;
 if(rv.ask.kind==='why'){
  if(k==='escape'){answerWhy('skip');return}
  const n=Number(k);if(n>=1&&n<=rv.ask.classes.length)answerWhy(rv.ask.classes[n-1]);else if(n===rv.ask.classes.length+1)answerWhy('wrong');
  return;
 }
 if(k==='y')answerSuggestion('yes');else if(k==='n')answerSuggestion('no');else if(k==='l'||k==='escape')answerSuggestion('later');
}
function renderReview(){preserveBandFocus(renderReviewContent)}
function renderReviewContent(){
 let it=rv.cur;if(!it||it.reviewed||it.dismissed||!items.includes(it))it=rv.cur=rvQueue()[0]||null;
 rvProgress();renderAsk();
 [...$('rvKeys').children].forEach(b=>{if(b.dataset.a&&b.dataset.a!=='undo')b.disabled=!it});
 if(!it){rv.shown=null;$('rvMain').innerHTML=`<div class="rvdone"><div class="ck">✓</div><b>All caught up</b><div>New images will appear here as sensors report.</div></div><div class="rvflash" id="rvFlash"></div>`;return}
 const key=it.id+':'+it.centralTier+':'+it.central;if(rv.shown===key)return;rv.shown=key;
 const sug=it.centralTier,rows=it.dets.map((d,i)=>[d,it.det[i]]).sort((a,b)=>b[1].s-a[1].s),why=buildWhy(it,rows).slice(0,4);
 const age=Math.max(0,Math.round((Date.now()-it.ts)/60000));
 const classifications=rows.filter(([d])=>d.ship).slice(0,3).map(([d])=>shipLine(d));
 [...$('rvKeys').querySelectorAll('[data-a]')].forEach(b=>b.classList.toggle('sugg',b.dataset.a===String(sug)));
 $('accLbl').textContent=`Agree · P${sug}`;
 $('rvMain').innerHTML=`<div class="rvimg"><div class="rvframe p${sug}">${largeImage(it)}</div></div>
 <div class="rvside">
  <div class="rvcard"><div class="lbl">Model suggests</div><div class="rvsug"><span class="tier t${sug}">P${sug}</span><b>${TNAME[sug]}</b></div><div class="rvsub">Command score ${it.central.toFixed(2)} · edge ${it.edge.toFixed(2)}${state.directives.length?` · ${state.directives.length} directive${state.directives.length>1?'s':''} active`:''}</div></div>
  <div class="rvcard"><div class="lbl" style="margin-bottom:4px">Why</div><ul class="why">${why.map(w=>`<li class="${w.c}">${w.t}</li>`).join('')}</ul></div>
  <div class="rvcard rvmeta"><span>Source</span><b>${it.src.name}</b><span>Captured</span><b class="mono">${zulu(it.ts)} <span style="color:var(--muted);font-family:inherit;font-weight:500">(${age} min ago)</span></b><span>Objects</span><b>${rows.map(([d])=>CLS[d.cls].lab).join(', ')||'—'}</b>${classifications.length?`<span>Classification</span><b class="rvclassification">${classifications.join('<br>')}</b>`:''}</div>
 </div><div class="rvflash" id="rvFlash"></div>`;
}
function rvFlash(msg,col){const f=$('rvFlash');if(!f)return;f.textContent=msg;f.style.color=col;f.classList.add('on');setTimeout(()=>f.classList.remove('on'),300)}
function rvPress(a){const b=$('rvKeys').querySelector(`[data-a="${a}"]`);if(!b)return;b.classList.add('pressed');setTimeout(()=>b.classList.remove('pressed'),140)}
function rvAct(a){
 if(rv.busy)return;rvPress(a);
 if(a==='undo'){const p=rv.undo[rv.undo.length-1];if(!p){toast('Nothing to undo');return}rv.busy=true;patchItem(p.id,{manual:p.manual,reviewed:p.reviewed,dismissed:p.dismissed,sent:p.sent}).then(it=>{rv.undo.pop();rv.done--;if(p.agreed)rv.agree--;if(typeof gsLearn!=='undefined')gsLearn.forget(p.id);rv.ask=null;rv.askBusy=false;renderAsk();rv.cur=it;rv.shown=null;rv.busy=false;renderReview();rvFlash('↶ Undone','#c3d1dd')}).catch(error=>{rv.busy=false;toast(error.message)});return}
 const it=rv.cur;if(!it)return;
 if(a==='skip'){rv.skipped[it.id]=Date.now();rv.cur=null;rv.shown=null;renderReview();return}
 const sug=it.centralTier,p={id:it.id,manual:it.manual,reviewed:it.reviewed,dismissed:it.dismissed,sent:it.sent,agreed:false};
 let msg,col;
 let changes={reviewed:true};
 if('1234'.includes(a)){changes.manual=+a;msg=`P${a} ${TNAME[+a]}`;col=tcol(+a);p.agreed=+a===sug}
 else if(a==='accept'){changes.manual=sug;msg=`✓ P${sug}`;col=tcol(sug);p.agreed=true}
 else if(a==='dismiss'){changes.manual=4;changes.dismissed=true;msg='✕ Not relevant';col='#9aa7b3'}
 else if(a==='cdr'){changes.manual=1;changes.sent=true;msg='➤ Forwarded to CO';col='var(--good)'}
 else return;
 rv.busy=true;
 patchItem(it.id,changes).then(updated=>{rv.done++;if(p.agreed)rv.agree++;rv.undo.push(p);if(rv.undo.length>50)rv.undo.shift();if(typeof gsLearn!=='undefined'){const result=gsLearn.record({id:it.id,src:it.src.id,dets:it.dets,det:it.det,sug,tier:changes.manual});if(result?.ambiguous){rv.ask={kind:'why',id:it.id,classes:result.ambiguous};rv.askBusy=false;renderAsk()}else checkLearningSuggestion()}rvFlash(msg,col);setTimeout(()=>{rv.busy=false;rv.cur=null;renderReview()},280)}).catch(error=>{rv.busy=false;toast(error.message)});
}
let tt;function toast(m){const t=$('toast');t.textContent=m;t.classList.add('on');clearTimeout(tt);tt=setTimeout(()=>t.classList.remove('on'),3200)}
init();
