/* Commander View: final decisions on imagery the watch floor escalated. */
let SRC={},CLS={},MODELS={},ATTR_LAB={},model='maritime';
let items=new Map(),view={tab:'pending',sel:null,busy:false,undo:[]};
const DEC={
 action:{k:'1',lab:'Take action',sub:'Task an asset · intercept / investigate',c:'#ff4d5e'},
 track:{k:'2',lab:'Track',sub:'Keep following this object',c:'#ff9a3c'},
 intel:{k:'3',lab:'Forward to intel',sub:'Analyze the image and prepare a report',c:'#5fb4ff'},
 close:{k:'4',lab:'No further action',sub:'Record a final decision; keep the image stored',c:'#8a99a8'},
 return:{k:'b',lab:'Return to watch floor',sub:'Needs another look',c:'#b48cff'}};
const KEYDEC=Object.fromEntries(Object.entries(DEC).map(([d,v])=>[v.k,d]));
const TN=['','Critical','High','Routine','Low'];
const tcol=t=>['','var(--p1)','var(--p2)','var(--p3)','var(--p4)'][t]||'var(--p4)';
const tierFor=s=>s>=.8?1:s>=.6?2:s>=.35?3:4;
const esc=t=>String(t).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;').replace(/'/g,'&#39;');
const ago=d=>{const s=Math.max(0,(Date.now()-d)/1000);return s<60?Math.round(s)+'s':s<3600?Math.round(s/60)+'m':(s/3600).toFixed(1)+'h'};
const lab=c=>CLS[c]?.lab||c;

function norm(it){
 it.ts=new Date(it.ts);it.src=SRC[typeof it.src==='string'?it.src:it.src.id]||{id:String(it.src),name:String(it.src),scene:'open',mode:'eo'};
 it.sentAt=it.sentAt?new Date(it.sentAt):null;it.decidedAt=it.decidedAt?new Date(it.decidedAt):null;return it;
}
const awaiting=it=>it.sent&&!it.decision;
function upsert(raw){
 const it=norm(raw),had=items.get(it.id);
 if(had&&had.img!==it.img)imageCache.delete(it.id);
 if(it.sent||it.decision)items.set(it.id,it);else items.delete(it.id);
 return{it,isNew:awaiting(it)&&!(had&&awaiting(had))};
}
const pending=()=>[...items.values()].filter(awaiting).sort((a,b)=>a.tier-b.tier||b.central-a.central||a.sentAt-b.sentAt);
const decided=()=>[...items.values()].filter(i=>i.decision).sort((a,b)=>b.decidedAt-a.decidedAt);
const list=()=>view.tab==='pending'?pending():decided();
function ensureSel(){if(!items.has(view.sel)||!list().some(i=>i.id===view.sel))view.sel=list()[0]?.id||null}

function contacts(it){return it.dets.map((d,i)=>[d,it.det[i]||{s:0}]).sort((a,b)=>b[1].s-a[1].s)}
function title(it){const o=contacts(it);return o.length?lab(o[0][0].cls)+(o.length>1?` +${o.length-1}`:''):'No contacts'}
function flags(it){
 const f=[];
 contacts(it).forEach(([d])=>{const g=CLS[d.cls]?.g,l=lab(d.cls);
  if(d.attrs.dark&&g!=='other'&&g!=='clutter')f.push(['al',`${l}: no AIS / transponder correlation`]);
  if(d.attrs.approaching)f.push(['al',`${l}: track inbound toward the sensor`]);
  if(d.attrs.loitering)f.push(['',`${l}: loitering`]);
  if(d.attrs.new)f.push(['',`${l}: not present in previous pass`])});
 if(it.edge<it.central-.15)f.push(['up',`Shore model raised the ship score ${it.edge.toFixed(2)} → ${it.central.toFixed(2)}`]);
 return[...new Map(f.map(x=>[x[1],x])).values()];
}
function boxes(it){
 return it.dets.map((d,i)=>{const s=(it.det[i]||{}).s||0,b=d.b,c=s>=.6?'hi':s>=.3?'md':'lo';if(!b||!(b.w>0))return'';
  return`<div class="box ${c}${b.x+b.w/2>W*.6?' r':''}${b.y<H*.08?' t':''}" style="left:${b.x/W*100}%;top:${b.y/H*100}%;width:${b.w/W*100}%;height:${b.h/H*100}%"><em>${esc(lab(d.cls))} ${Math.round(d.conf*100)}%</em></div>`}).join('');
}

function renderKpis(){
 const p=pending();$('kPend').textContent=p.length;$('kP1').textContent=p.filter(i=>i.tier===1).length;$('kDone').textContent=decided().length;
 const oldest=p.reduce((m,i)=>i.sentAt&&(!m||i.sentAt<m)?i.sentAt:m,null);$('kOld').textContent=oldest?ago(oldest):'—';
 $('tPend').textContent=p.length;$('tDone').textContent=decided().length;
}
function renderLearned(directives){
 const root=$('learned'),prefix='Operator feedback:';
 const learned=(directives||[]).filter(d=>typeof d.text==='string'&&d.text.startsWith(prefix));
 root.hidden=!learned.length;
 root.innerHTML=learned.length?`<b>Learned from operator</b><ul>${learned.map(d=>`<li>${esc(d.text.slice(prefix.length).trim())}</li>`).join('')}</ul>`:'';
}
function renderQueue(){
 const l=list();
 if(!l.length){$('qlist').innerHTML=`<div class="empty">${view.tab==='pending'?'No escalations awaiting decision.<br>New ones appear here as the watch floor sends them up.':'No decisions yet.'}</div>`;return}
 $('qlist').innerHTML=l.map(it=>`<div class="row ${it.id===view.sel?'on':''} ${it.fresh?'fresh':''}" data-id="${esc(it.id)}">
  <div class="th"><img src="${imageFor(it)}" alt=""><span class="tg" style="background:${tcol(it.tier)}">P${it.tier} ${TN[it.tier]}</span></div>
  <div class="rm"><b>${esc(title(it))}</b><span>${esc(it.src.name)}</span>
  <span class="mono">${it.decision?`<i class="dc" style="color:${DEC[it.decision].c}">${DEC[it.decision].lab}</i> · ${ago(it.decidedAt)} ago`:`sent ${ago(it.sentAt)} ago · ${it.central.toFixed(2)}`}</span></div></div>`).join('');
 $('qlist').querySelector('.row.on')?.scrollIntoView({block:'nearest'});
}
function renderStage(){
 const it=items.get(view.sel),old=$('note'),keep=old&&old.dataset.id===view.sel?old.value:'';
 if(!it){
  $('stage').innerHTML=`<div class="idle"><div class="big">✓</div><b>${view.tab==='pending'?'Board is clear':'No decisions yet'}</b><span>${view.tab==='pending'?'Nothing is awaiting the commander’s decision.':''}</span></div>`;$('brief').innerHTML='';return;
 }
 $('stage').innerHTML=`<div class="frame"><img src="${imageFor(it)}" alt=""><div class="bx">${boxes(it)}</div></div>
 <div class="facts"><div><span>Source</span><b>${esc(it.src.name)}</b></div><div><span>Location</span><b>${esc(it.src.loc||'—')}</b></div>
 <div><span>Captured</span><b class="mono">${zulu(it.ts)} · ${ago(it.ts)} ago</b></div><div><span>Record</span><b class="mono">#${it.dbId} · FRM ${it.frame}</b></div></div>
 <table class="dets"><tr><th>Contact</th><th>Behavior</th><th class="n">Conf</th><th class="n">Score</th></tr>${contacts(it).map(([d,sd])=>`<tr><td>${esc(lab(d.cls))}</td><td>${Object.entries(d.attrs).filter(([,v])=>v).map(([k])=>esc(ATTR_LAB[k]||k)).join(', ')||'—'}</td><td class="n">${d.conf.toFixed(2)}</td><td class="n">${sd.s.toFixed(2)}</td></tr>`).join('')}</table>`;
 renderBrief(it);
 if(keep&&$('note'))$('note').value=keep;
}
function renderBrief(it){
 const sug=tierFor(it.central),f=flags(it),d=it.decision&&DEC[it.decision];
 const steps=[['Captured',`${esc(it.src.id)} · ${zulu(it.ts)}`],['Kept on ship',`score ${it.edge.toFixed(2)} · ${esc(it.src.em||'')}`],['Sent to shore',esc(it.src.link||'—')],
  ['Shore model',`${esc(MODELS[model]?.name||model)} · ${it.central.toFixed(2)} → P${sug} ${TN[sug]}`],
  ['Watch officer',it.sentAt?`sent to Commander as P${it.tier} ${TN[it.tier]} · ${zulu(it.sentAt)}`:'returned for another look'],...(d?[['Commander',`${d.lab} · ${zulu(it.decidedAt)}`]]:[])];
 $('brief').innerHTML=`<div class="pri" style="--c:${tcol(it.tier)}"><span class="big">P${it.tier}</span><div><b>${TN[it.tier]}</b><span>Watch-officer priority${it.manual&&it.manual!==sug?` · model suggested P${sug} ${TN[sug]}`:''}</span></div></div>
 <div class="meter"><span title="Command score: importance after shore-side scoring">Shore score</span><div class="bar"><i style="width:${it.central*100}%;background:${tcol(sug)}"></i></div><b class="mono">${it.central.toFixed(2)}</b></div>
 <div class="meter"><span title="Edge score: the initial importance assigned on the ship">Ship score</span><div class="bar"><i style="width:${it.edge*100}%"></i></div><b class="mono">${it.edge.toFixed(2)}</b></div>
 <h4>Why it reached you</h4><ul class="why">${f.map(([c,t])=>`<li class="${c}">${esc(t)}</li>`).join('')||'<li>High command-model score</li>'}</ul>
 <h4 title="Chain of custody: the record of how this image was captured, sent, reviewed and decided">Chain of custody</h4><ol class="chain">${steps.map(([a,b])=>`<li><b>${a}</b><span>${b}</span></li>`).join('')}</ol>
 <h4>Decision</h4>${d?`<div class="decided" style="--c:${d.c}"><b>${d.lab}</b>${it.decisionNote?`<p>“${esc(it.decisionNote)}”</p>`:''}<button class="lnk" id="reopen">Reopen decision (Z)</button></div>`:
 `<textarea id="note" data-id="${esc(it.id)}" maxlength="280" placeholder="Commander's note (optional) · press / to type, Esc to leave"></textarea>
 <div class="decs">${Object.entries(DEC).map(([k,v])=>`<button class="dec" title="${k==='close'?'Record a final decision to take no further action. The image remains stored in Decided.':v.sub}" data-d="${k}" style="--c:${v.c}"><kbd>${v.k.toUpperCase()}</kbd><b>${v.lab}</b><span>${v.sub}</span></button>`).join('')}</div>`}`;
}
function render(){ensureSel();renderKpis();renderQueue();renderStage()}

async function api(path,method='GET',body){
 const r=await fetch(path,{method,headers:body?{'Content-Type':'application/json'}:{},body:body?JSON.stringify(body):undefined});
 const j=await r.json().catch(()=>({}));if(!r.ok)throw new Error(j.error||r.statusText);return j;
}
async function decide(decision,id=view.sel){
 const it=items.get(id);if(!it||view.busy)return;
 const note=decision?($('note')?.value||''):'';view.busy=true;
 try{
  const next=pending().filter(x=>x.id!==id)[0];
  const r=await api(`/api/items/${encodeURIComponent(id)}/decision`,'POST',{decision,note});
  if($('note'))$('note').value='';
  upsert(r.item);
  if(decision){view.undo.push(id);if(view.tab==='pending')view.sel=next?.id||null;toast(`${DEC[decision].lab} · P${it.tier} ${TN[it.tier]} ${title(it)}`)}
  else{setTab('pending');view.sel=id;toast('Decision reopened')}
  render();return true;
 }catch(e){toast(e.message);return false}finally{view.busy=false}
}
async function undo(){
 if(view.busy)return;
 const fromStack=view.undo.length>0,id=fromStack?view.undo.pop():(items.get(view.sel)?.decision?view.sel:null);
 if(!id||!items.get(id)?.decision)return;
 if(!await decide('',id)&&fromStack)view.undo.push(id);
}
function setTab(t){view.tab=t;[...$('tabs').children].forEach(b=>b.classList.toggle('on',b.dataset.t===t))}
function step(dir){const l=list(),i=l.findIndex(x=>x.id===view.sel),n=l[Math.max(0,Math.min(l.length-1,i+dir))];if(n&&n.id!==view.sel){view.sel=n.id;renderQueue();renderStage()}}
function setLive(on){$('live').classList.toggle('off',!on);$('live').querySelector('span').textContent=on?'LIVE':'RECONNECTING'}
let tt;function toast(m){const t=$('toast');t.textContent=m;t.classList.add('on');clearTimeout(tt);tt=setTimeout(()=>t.classList.remove('on'),3200)}

function connect(){
 const es=new EventSource('/api/stream');
 es.onopen=()=>setLive(true);es.onerror=()=>setLive(false);
 es.addEventListener('snapshot',e=>{const m=JSON.parse(e.data);if(m.live){const b=document.querySelector('.banner');if(b)b.textContent='UNCLASSIFIED // FOR TRAINING AND DEMONSTRATION ONLY — LIVE SHORE PICTURE · COMMANDER\'S FINAL REVIEW'}model=m.state.model;renderLearned(m.state.directives);items=new Map();m.items.forEach(upsert);render()});
 es.addEventListener('update',e=>{
  const m=JSON.parse(e.data),prev=view.sel,{it,isNew}=upsert(m.item);
  if(isNew){it.fresh=true;toast(`New escalation · P${it.tier} ${TN[it.tier]} ${title(it)} · ${it.src.name}`);setTimeout(()=>{it.fresh=false;renderQueue()},5000)}
  ensureSel();renderKpis();renderQueue();if(view.sel!==prev||it.id===view.sel)renderStage();
 });
 es.addEventListener('evict',e=>{const{id}=JSON.parse(e.data);if(items.delete(id))render()});
}
async function init(){
 const c=await api('/api/config');
 SRC=Object.fromEntries(c.sources.map(s=>[s.id,s]));CLS=c.classes;MODELS=c.models;ATTR_LAB=c.attrLabels;
 $('tabs').onclick=e=>{const b=e.target.closest('button');if(b){setTab(b.dataset.t);view.sel=null;render()}};
 $('qlist').onclick=e=>{const r=e.target.closest('.row');if(r){view.sel=r.dataset.id;renderQueue();renderStage()}};
 $('brief').onclick=e=>{const b=e.target.closest('.dec');if(b)decide(b.dataset.d);else if(e.target.id==='reopen')decide('')};
 document.addEventListener('keydown',e=>{
  if(e.target.tagName==='TEXTAREA'){if(e.key==='Escape')e.target.blur();return}
  if(e.ctrlKey||e.metaKey||e.altKey)return;
  const k=e.key.toLowerCase();
  if(['arrowdown','arrowup','j','k'].includes(k)){e.preventDefault();step(k==='arrowdown'||k==='j'?1:-1);return}
  if(k==='tab'){e.preventDefault();setTab(view.tab==='pending'?'decided':'pending');view.sel=null;render();return}
  if(k==='/'){e.preventDefault();$('note')?.focus();return}
  if(k==='z'){undo();return}
  const it=items.get(view.sel);if(it&&!it.decision&&KEYDEC[k]){e.preventDefault();if(!e.repeat)decide(KEYDEC[k])}
 });
 const tick=()=>{$('clock').textContent=zulu(new Date());renderKpis()};tick();setInterval(tick,1000);setInterval(renderQueue,15000);
 render();connect();
}
init().catch(e=>toast('Could not load configuration: '+e.message));
