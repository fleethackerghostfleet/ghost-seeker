const workbench=(()=>{
 const copy=x=>JSON.parse(JSON.stringify(x));
 const empty=()=>({text:'',rules:[],pins:{}});
 let draft=empty(),unknown=[],after=[],candidates=[],proposal=null,comparison=null,pendingDuration=0,parsedSnapshot=JSON.stringify(draft.rules),closeFocus='apply';
 let interpretation=null,interpretationPending=false,pendingParse=null,previewReady=false,confirmedDraft=null;
 let versions=[],cursor=-1,serial=0,selected=null,textTimer=null,weightTimer=null,liveTimer=null,lastPreview=0,sequence=0,busy=false,initialized=false,renderDeferred=false;
 const sorted=rows=>[...rows].sort((a,b)=>a.tier-b.tier||b.central-a.central||a.id.localeCompare(b.id));
 const counts=rows=>[1,2,3,4].map(t=>rows.filter(i=>i.tier===t).length);
 const delta=(before,next)=>next.map((n,i)=>`${n-before[i]>=0?'+':'−'}${Math.abs(n-before[i])} P${i+1} ${TNAME[i+1]}`).join(' · ');
 const changes=(before,next)=>{const map=new Map(before.map(i=>[i.id,i.tier]));return next.filter(i=>map.has(i.id)&&map.get(i.id)!==i.tier).length};
 const normalize=r=>({...r,classes:r.classes||[],attrs:r.attrs||[],sources:r.sources||[],withinMin:r.withinMin||0,weight:r.weight||1});
 const rows=()=>sorted(after.map(result=>{const item=findItem(result.id),pin=draft.pins[result.id];return item?{...item,...result,tier:pin===0?(result.central>=.8?1:result.central>=.6?2:result.central>=.35?3:4):pin??result.tier}:null}).filter(Boolean));
 const compilerLabel=path=>(path==='research'||path?.startsWith('research/'))?'IBM Granite · research language compiler':({llm:'On-board language assistant',rules:'Built-in language rules',edited:'Your edited rules'}[path]||'');
 const draftKey=()=>JSON.stringify(draft);
 function updateApply(){
  $('wbApply').disabled=busy||interpretationPending||!previewReady||!!interpretation?.blocked||(!draft.rules.length&&!Object.keys(draft.pins).length);
 }
 function renderInterpretation(){
  const box=$('wbInterpretation');box.hidden=!interpretation&&!interpretationPending;
  $('wbCompiler').textContent=interpretationPending?'Interpreting your request…':compilerLabel(interpretation?.path);
  $('wbInterpretationReadback').textContent=interpretationPending?'Preview is updating. Active priorities have not changed.':interpretation?.readback||'';
  const warnings=[...(interpretation?.warnings||[])];
  if(!interpretationPending&&!interpretation?.blocked&&draft.rules.length&&items.length&&!draft.rules.some(r=>items.some(it=>(!r.sources.length||r.sources.includes(it.src.id))&&(!r.withinMin||Date.now()-it.ts<=r.withinMin*60000)&&it.dets.some(d=>(!r.classes.length||r.classes.includes(d.cls))&&(!r.attrs.length||r.attrs.some(a=>d.attrs[a]))))))warnings.push('No stored images match these rules. Applying them would affect future matching images.');
  const list=$('wbInterpretationWarnings');list.replaceChildren();
  for(const warning of warnings){const item=document.createElement('li');item.textContent=warning;if(interpretation?.blocked||warning.startsWith('Blocked'))item.className='blocked';list.append(item)}
  list.hidden=interpretationPending||!warnings.length;
  updateApply();
 }
 function markEdited(){interpretation={path:'edited',readback:draft.rules.map(ruleText).join('; '),warnings:[],blocked:false};confirmedDraft=null;previewReady=false;renderInterpretation()}
 function ruleText(r){return `${r.type==='boost'?'Prioritize':'Suppress'} ${r.only?'only ':''}${r.classes.join(' or ')||'any object'}${r.attrs.length?' '+r.attrs.map(a=>ATTR_LAB[a]).join(' or '):''}${r.sources.length?' from '+r.sources.join(' or '):''}${r.withinMin?' last '+r.withinMin+' min':''}`}
 function rewrite(){draft.text=draft.rules.map(ruleText).join('; ').slice(0,500);$('nlIn').value=draft.text}
 function save(label){
  if(cursor>=0&&JSON.stringify(versions[cursor].draft)===JSON.stringify(draft))return;
  const result=rows(),previous=versions[cursor]?.result||items;
  versions=versions.slice(0,cursor+1);versions.push({n:++serial,label,draft:copy(draft),interpretation:copy(interpretation),result:result.map(i=>({id:i.id,tier:i.tier,central:i.central})),delta:`${changes(previous,result)} priority changes · ${delta(counts(previous),counts(result))}`});cursor=versions.length-1;renderHistory();
 }
 function buildCandidates(){
  candidates=[];
  function offer(label,edit){if(candidates.length>=30)return;const rules=copy(draft.rules);edit(rules);candidates.push({key:String(candidates.length),label,rules})}
  draft.rules.forEach((r,i)=>{
   if(r.classes.includes('small boat')&&/\bboats\b/i.test(draft.text)&&!r.attrs.length){
    for(const [label,classes,attrs] of [['Small boats',['small boat'],[]],['Fast boats',['small boat'],['moving']],['Fishing vessels',['fishing vessel'],[]]])offer('Specify boats → '+label,rs=>{rs[i]={...rs[i],classes,attrs,edited:true}});
   }
   if(r.classes.length>=3&&r.classes.includes('helicopter'))for(const [label,classes] of [['Helicopters',['helicopter']],['Drones',['drone']],['Fixed-wing',['military aircraft','civil aircraft']]])offer('Specify aircraft → '+label,rs=>{rs[i].classes=classes;rs[i].edited=true});
   if(!r.attrs.length)for(const a of ['moving','dark'])offer('Add condition → '+ATTR_LAB[a],rs=>{rs[i].attrs=[a];rs[i].edited=true});
   if(!r.sources.length)offer('Scope rule '+(i+1)+' → EO-P7',rs=>{rs[i].sources=['EO-P7'];rs[i].edited=true});
   if(!r.withinMin)offer('Rule '+(i+1)+' → last 30 min',rs=>{rs[i].withinMin=30;rs[i].edited=true});
   const matched=items.filter(it=>(!r.sources.length||r.sources.includes(it.src.id))&&(!r.withinMin||Date.now()-it.ts<=r.withinMin*60000)&&it.dets.some(d=>(!r.classes.length||r.classes.includes(d.cls))&&(!r.attrs.length||r.attrs.some(a=>d.attrs[a])))).length;
   if(items.length&&matched/items.length>.45)offer(`Broad match: ${Math.round(100*matched/items.length)}% → approaching only`,rs=>{rs[i].attrs=['approaching'];rs[i].edited=true});
   if(draft.rules.some((other,j)=>j!==i&&other.type!==r.type&&(!r.classes.length||!other.classes.length||r.classes.some(c=>other.classes.includes(c)))))offer('Conflicting boost/suppress → remove rule '+(i+1),rs=>rs.splice(i,1));
  });
  if(proposal)candidates.push({key:'correction',label:proposal.label,rules:proposal.rules});
 }
 async function preview({parse=false,label=null,background=false}={}){
  if(typeof gsAuth!=='undefined'&&gsAuth.viewer()){$('wbStatus').textContent='Read-only · collection priority editing is unavailable';return false}
  if(document.querySelector('dialog[open]'))return false;
  clearTimeout(liveTimer);liveTimer=null;
  const token=++sequence;lastPreview=Date.now();previewReady=false;confirmedDraft=null;
  if(parse)interpretationPending=true;renderInterpretation();
  $('wbStatus').textContent='Scoring preview…';
  try{
   if(parse){
    const text=$('nlIn').value;
    const parsed=await request('/api/directives/preview','POST',{text});
    if(token!==sequence)return;
    draft.text=text;draft.rules=parsed.rules.map(normalize);parsedSnapshot=JSON.stringify(draft.rules);unknown=parsed.unknown||[];interpretation={path:parsed.path||'rules',readback:parsed.readback||draft.rules.map(ruleText).join('; '),warnings:parsed.warnings||[],blocked:!!parsed.blocked};proposal=null;renderChips();
   }
   buildCandidates();
   const submitted=candidates.map(c=>({key:c.key,rules:c.rules}));
   const result=await request('/api/directives/preview','POST',{rules:draft.rules,candidates:submitted});
   if(token!==sequence)return;
   after=result.items;previewReady=true;interpretationPending=false;renderInterpretation();
   for(const c of candidates){const effect=result.candidates.find(x=>x.key===c.key);if(effect)c.effect=delta(result.counts,effect.counts)+` · ${effect.changed} priority changes`}
   if(label)save(label);else if(!versions.length&&items.length)save('Live starting point');
   $('wbStatus').textContent=interpretation?.blocked?'Clarification needed · active priorities unchanged':`Live · ${result.items.length} stored images · preview updated ${new Date().toLocaleTimeString()}`;
   renderResults({background});renderSuggestions();renderProposal();renderHistory();
   return true;
  }catch(error){if(token===sequence){interpretationPending=false;previewReady=false;interpretation={path:interpretation?.path,readback:'Preview unavailable. Active priorities have not changed.',warnings:[error.message],blocked:true};renderInterpretation();$('wbStatus').textContent='Preview unavailable: '+error.message;toast(error.message)}return false}
 }
 function textChanged(){
  clearTimeout(textTimer);proposal=null;++sequence;interpretationPending=true;previewReady=false;confirmedDraft=null;renderInterpretation();
  textTimer=setTimeout(()=>{textTimer=null;pendingParse=preview({parse:true,label:'Directive edited'})},250);
 }
 async function flush(){if(textTimer){clearTimeout(textTimer);textTimer=null;pendingParse=preview({parse:true,label:'Directive edited'})}if(interpretationPending&&pendingParse)return pendingParse;return true}
 function live(){
  if(!initialized||state.page!=='rp'||textTimer||interpretationPending||busy||document.querySelector('dialog[open]'))return;
  clearTimeout(liveTimer);liveTimer=setTimeout(()=>{liveTimer=null;if(state.page==='rp'&&!textTimer&&!interpretationPending&&!busy&&!document.querySelector('dialog[open]'))preview({background:true})},Math.max(0,3000-(Date.now()-lastPreview)));
 }
 function activate(){if($('nlIn').value!==draft.text)textChanged();else live()}
 function opts(entries,values){return entries.map(([v,l])=>`<option value="${esc(String(v))}" ${values.includes(String(v))?'selected':''}>${esc(l)}</option>`).join('')}
 function renderChips(){
  $('wbUnknown').innerHTML=interpretation?.blocked?'Clarification needed — request has not been applied.':unknown.length?'Not understood: '+unknown.map(w=>`<mark>${esc(w)}</mark>`).join(' ')+'<br>Try an object type, condition, sensor name or “last 30 min”.':'✓ Recognized terms only';
  $('wbChips').innerHTML=draft.rules.map((r,i)=>`<div class="wb-chip ${r.type}" data-rule="${i}"><div class="wb-row"><strong>${ruleLabel(r)}</strong>${r.edited?'<span class="edited">edited</span>':''}</div><div class="wb-row"><button data-field="flip">${r.type==='boost'?'▲ Boost':'▼ Suppress'} ⇄</button><button data-field="remove" aria-label="Remove rule ${i+1}">× Remove</button></div><label>Object types · Ctrl+click to select several<select data-field="classes" multiple size="3">${opts(Object.entries(CLS).map(([k,v])=>[k,v.lab]),r.classes)}</select></label><label>Conditions (any match)<select data-field="attrs" multiple size="2">${opts(Object.entries(ATTR_LAB),r.attrs)}</select></label><label>Sensors · leave blank to use all<select data-field="sources" multiple size="2">${opts(SOURCES.filter(s=>s.id!=='ALL').map(s=>[s.id,s.id]),r.sources)}</select></label><label>Time window<select data-field="withinMin">${opts([['0','Any time'],['15','Last 15 min'],['30','Last 30 min'],['60','Last hour'],...(![0,15,30,60].includes(r.withinMin)?[[String(r.withinMin),'Last '+r.withinMin+' min']]:[])],[String(r.withinMin)])}</select></label><label title="Weight: how strongly this rule changes the score">Rule strength <output>${r.weight.toFixed(1)}×</output><div class="wb-row"><button data-field="minus" aria-label="Decrease weight">−</button><input data-field="weight" aria-label="Rule weight" type="range" min=".2" max="3" step=".2" value="${r.weight}"><button data-field="plus" aria-label="Increase weight">+</button></div></label></div>`).join('')||'<p class="hint">No draft rules. The preview matches the current feed.</p>';
 }
 async function chipEdit(target){
  clearTimeout(weightTimer);
  await flush();const root=target.closest('[data-rule]');if(!root)return;const i=+root.dataset.rule,r=draft.rules[i],field=target.dataset.field;if(!r||!field)return;
  proposal=null;
  if(field==='remove')draft.rules.splice(i,1);
  else{r.edited=true;if(field==='flip')r.type=r.type==='boost'?'suppress':'boost';else if(field==='minus'||field==='plus')r.weight=+Math.max(.2,Math.min(3,r.weight+(field==='plus'?.2:-.2))).toFixed(1);else if(['classes','attrs','sources'].includes(field))r[field]=[...target.selectedOptions].map(o=>o.value);else r[field]=+target.value}
  markEdited();renderChips();await preview({label:'Rule edited'});
 }
 function renderSuggestions(){
  $('wbSuggestions').innerHTML=candidates.filter(c=>c.key!=='correction').map(c=>`<button class="wb-suggestion" data-suggest="${c.key}">${esc(c.label)}<small>${esc(c.effect||'Scoring…')}</small></button>`).join('')||'<p class="hint">Try “prioritize boats” for guided specificity suggestions.</p>';
 }
 function renderResults({background=false}={}){
  const active=document.activeElement,tile=active.closest('.wb-tile'),control=active.closest('select,input,button,textarea');
  if(background&&tile&&control){renderDeferred=true;return}
  renderDeferred=false;
  const focused=tile?.dataset.id,controlIndex=tile&&control?[...tile.querySelectorAll('select,input,button,textarea')].indexOf(control):-1;
  const current=rows(),live=sorted(items),before=sorted(comparison?items.map(it=>({...it,...comparison.result.find(r=>r.id===it.id)})):items),bm=new Map(before.map((i,index)=>[i.id,{...i,rank:index+1}])),ac=counts(current),bc=counts(before);
  $('wbCompareLabel').textContent=`Before: ${comparison?'v'+comparison.n+' recorded results':'current live feed'} → After: draft · ${changes(before,current)} images change priority`;
  $('wbCounts').innerHTML=ac.map((n,i)=>`<div class="wb-count" style="--tier:${tcol(i+1)}">P${i+1} ${TNAME[i+1]}<b>${bc[i]} → ${n}</b><span class="hint">${n-bc[i]>=0?'+':''}${n-bc[i]} images</span><div class="wb-track"><i style="width:${before.length?bc[i]/before.length*100:0}%;opacity:.4"></i></div><div class="wb-track"><i style="width:${current.length?n/current.length*100:0}%"></i></div></div>`).join('');
  const top=current.slice(0,16),display=$('wbDiff').checked?current.filter(i=>bm.get(i.id)?.tier!==i.tier):[...top];
  if(focused&&!display.some(i=>i.id===focused)){const retained=current.find(i=>i.id===focused);if(retained)display.push(retained)}
  $('wbResultLabel').textContent=$('wbDiff').checked?`After · ${display.length} images with changed priority`:'After · Top 16 images';
  $('wbGrid').innerHTML=display.map(it=>{const old=bm.get(it.id)||{tier:it.tier,rank:live.length},rank=current.indexOf(it)+1,diff=old.rank-rank,badge=old.rank>16&&rank<=16?'NEW in top':diff>0?'↑'+diff:diff<0?'↓'+Math.abs(diff):'— same rank';return `<article class="wb-tile ${selected===it.id?'selected':''}" tabindex="0" data-id="${esc(it.id)}" aria-label="Rank ${rank}, frame ${it.frame}, P${it.tier} ${TNAME[it.tier]}"><div class="wb-photo"><img src="${imageFor(it)}" alt="Synthetic ${esc(it.dets.map(d=>CLS[d.cls].lab).join(', '))}"><span class="wb-badge">#${rank} · ${badge}</span></div><div class="wb-tile-body"><div class="wb-tile-title"><span>${esc(it.src.id)} · ${Math.max(0,Math.round((Date.now()-it.ts)/60000))}m</span><b style="color:${tcol(it.tier)}">P${old.tier} ${TNAME[old.tier]} → P${it.tier} ${TNAME[it.tier]}</b></div><div class="wb-actions"><button data-correct="higher" title="Should be higher (↑)">↑ Higher</button><button data-correct="lower" title="Should be lower (↓)">↓ Lower</button><button data-correct="irrelevant" title="Propose a rule to lower the priority of similar images. Review and apply it to change the feed (X).">Not relevant</button></div><select class="wb-pin" aria-label="Set priority for image ${it.frame}"><option value="">${draft.pins[it.id]!==undefined?'Remove pending pin':'Set image priority…'}</option>${[1,2,3,4].map(t=>`<option value="${t}" ${draft.pins[it.id]===t?'selected':''}>Set P${t} ${TNAME[t]} exception</option>`).join('')}<option value="0" ${draft.pins[it.id]===0?'selected':''}>Clear current operator-set priority</option></select></div></article>`}).join('')||'<div class="wb-empty">No priority changes or no stored images. Refine a rule or turn off the priority-change filter.</div>';
  const dropped=before.slice(0,16).filter(it=>!top.some(x=>x.id===it.id));$('wbDropLabel').textContent='No longer in the top 16 images · '+dropped.length;
  $('wbDropped').innerHTML=dropped.map(it=>`<button data-why="${esc(it.id)}"><img src="${imageFor(it)}" alt="Dropped synthetic frame ${it.frame}">Frame ${it.frame}</button>`).join('')||'<p class="hint">No images dropped out.</p>';
  if(selected)why(selected);
  if(focused){const restored=$('wbGrid').querySelector(`[data-id="${CSS.escape(focused)}"]`);(controlIndex>=0?restored?.querySelectorAll('select,input,button,textarea')[controlIndex]:restored)?.focus({preventScroll:true})}
 }
 function why(id){
  const it=rows().find(i=>i.id===id);if(!it)return;selected=id;
  $('wbWhy').innerHTML=`<div class="wb-row"><h3>Why / Frame ${it.frame}</h3><div class="spacer"></div><span class="hint">${esc(it.src.id)}</span></div><p class="hint">${it.dets.map(d=>esc(CLS[d.cls].lab)+' ('+Math.round(d.conf*100)+'%) '+Object.keys(d.attrs).filter(a=>d.attrs[a]).map(a=>ATTR_LAB[a]).join(', ')).join(' · ')}</p><ul>${(it.contrib||[]).map(c=>`<li>${draft.rules[c.ruleIndex]?ruleLabel(draft.rules[c.ruleIndex]):'Rule'}: <b>${c.delta>=0?'+':''}${(100*c.delta).toFixed(1)} score points</b>${Math.abs(c.delta)<.00001?' (no effect)':''}</li>`).join('')}<li>Final score: ${(100*it.central).toFixed(1)} → P${it.tier} ${TNAME[it.tier]}${draft.pins[id]!==undefined?' · draft image-specific exception':it.manual?' · current operator-set priority':''}</li></ul><div class="hint">Each number shows how much that rule changes the score, alongside active directives. Rules can overlap, so the numbers may not add up. Prioritizing one type of object can lower others.</div>`;
  $('wbGrid').querySelectorAll('.wb-tile').forEach(el=>el.classList.toggle('selected',el.dataset.id===id));
 }
 async function correct(id,action){
  await flush();const it=findItem(id);if(!it)return;const score=(d,i)=>it.det?.[i]?.s??MODELS[state.model].w[d.cls]*d.conf,det=it.dets.map((d,i)=>({d,s:score(d,i)})).sort((a,b)=>b.s-a.s)[0]?.d;if(!det)return;
  const attr=['dark','approaching','moving','loitering','new'].find(a=>det.attrs[a]),rule=normalize({type:action==='higher'?'boost':'suppress',classes:[det.cls],attrs:attr?[attr]:[],sources:[it.src.id],weight:action==='irrelevant'?2:1,edited:true});
  proposal={id,label:ruleText(rule),rules:[...copy(draft.rules),rule]};await preview();$('wbProposal').scrollIntoView({block:'nearest',behavior:'smooth'});
 }
 function renderProposal(){
  $('wbProposal').hidden=!proposal;if(!proposal)return;
  const effect=candidates.find(c=>c.key==='correction')?.effect||'Scoring…';
  $('wbProposal').innerHTML=`<div class="wb-eyebrow">Proposed correction · not yet active</div><h3>${esc(proposal.label)}</h3><p class="hint">Based on this image’s object type, condition and sensor. Changes all matching images; set an image-specific priority to change only this one.</p><p style="color:var(--good)">${esc(effect)}</p><div class="wb-row"><button id="wbAcceptFix" class="primary">Accept fix</button><button id="wbRejectFix" title="Cancel this proposed correction without changing your draft rules or the feed.">Cancel correction</button></div>`;
  $('wbAcceptFix').onclick=async()=>{draft.rules=proposal.rules;rewrite();proposal=null;markEdited();renderChips();await preview({label:'Correction accepted'})};
  $('wbRejectFix').onclick=()=>{proposal=null;renderProposal()};
 }
 function renderHistory(){
  $('wbHistory').innerHTML=versions.map((v,i)=>`<div class="wb-version ${i===cursor?'active':''}"><strong>v${v.n} · ${esc(v.label)}</strong><p class="hint">${esc(v.delta)}</p><div class="wb-row"><button data-compare="${i}">Compare vs now</button><button data-revert="${i}">Revert</button></div></div>`).reverse().join('');
  $('wbUndo').disabled=cursor<=0;$('wbRedo').disabled=cursor>=versions.length-1;
 }
 async function travel(change){await flush();const index=cursor+change;if(index<0||index>=versions.length)return;cursor=index;draft=copy(versions[index].draft);$('nlIn').value=draft.text;unknown=[];proposal=null;interpretation=copy(versions[index].interpretation);parsedSnapshot=JSON.stringify(draft.rules);renderChips();await preview()}
 async function reset(label='Reset draft'){++sequence;clearTimeout(textTimer);textTimer=null;pendingParse=null;interpretationPending=false;interpretation=null;confirmedDraft=null;draft=empty();parsedSnapshot=JSON.stringify(draft.rules);$('nlIn').value='';unknown=[];proposal=null;comparison=null;renderChips();await preview({label})}
 function init(){
  initialized=true;renderChips();renderInterpretation();
  $('nlIn').oninput=textChanged;
  $('wbAdd').onclick=async()=>{await flush();draft.rules.push(normalize({type:'boost',classes:['small boat'],edited:true}));proposal=null;markEdited();renderChips();await preview({label:'Manual rule added'})};
  $('wbChips').onclick=e=>{const b=e.target.closest('button');if(b)chipEdit(b)};
  $('wbChips').onchange=e=>chipEdit(e.target);
  $('wbChips').oninput=e=>{if(e.target.dataset.field==='weight'){const root=e.target.closest('.wb-chip'),r=draft.rules[+root.dataset.rule];r.weight=+e.target.value;r.edited=true;markEdited();root.querySelector('output').textContent=r.weight.toFixed(1)+'×';clearTimeout(weightTimer);weightTimer=setTimeout(()=>preview(),120)}};
  $('wbSuggestions').onclick=async e=>{const b=e.target.closest('[data-suggest]');if(!b)return;const c=candidates.find(c=>c.key===b.dataset.suggest);if(!c)return;draft.rules=copy(c.rules);rewrite();proposal=null;unknown=[];markEdited();renderChips();await preview({label:'Suggestion: '+c.label})};
  $('wbGrid').onmouseover=e=>{const t=e.target.closest('.wb-tile');if(t&&selected!==t.dataset.id)why(t.dataset.id)};
  $('wbGrid').onfocusin=e=>{const t=e.target.closest('.wb-tile');if(t)why(t.dataset.id)};
  $('wbGrid').onfocusout=()=>setTimeout(()=>{if(renderDeferred)renderResults({background:true})},0);
  $('wbGrid').onclick=e=>{const t=e.target.closest('.wb-tile');if(!t)return;why(t.dataset.id);const b=e.target.closest('[data-correct]');if(b)correct(t.dataset.id,b.dataset.correct)};
  $('wbGrid').onchange=e=>{if(!e.target.classList.contains('wb-pin'))return;const id=e.target.closest('.wb-tile').dataset.id;if(e.target.value==='')delete draft.pins[id];else draft.pins[id]=+e.target.value;selected=id;confirmedDraft=null;renderResults();updateApply();save('Tier exception updated')};
  $('wbDropped').onclick=e=>{const b=e.target.closest('[data-why]');if(b)why(b.dataset.why)};
  $('wbDiff').onchange=renderResults;$('wbBaseline').onclick=()=>{comparison=null;renderResults()};
  $('wbHistory').onclick=async e=>{if(e.target.dataset.compare!==undefined){comparison=versions[+e.target.dataset.compare];renderResults()}if(e.target.dataset.revert!==undefined){await flush();draft=copy(versions[+e.target.dataset.revert].draft);$('nlIn').value=draft.text;proposal=null;unknown=[];interpretation=copy(versions[+e.target.dataset.revert].interpretation);parsedSnapshot=JSON.stringify(draft.rules);renderChips();await preview({label:'Version restored'})}};
  $('wbUndo').onclick=()=>travel(-1);$('wbRedo').onclick=()=>travel(1);$('wbReset').onclick=()=>reset();
  document.addEventListener('keydown',e=>{
   if(typeof gsAuth!=='undefined'&&gsAuth.viewer())return;
   if(document.querySelector('dialog[open]')||gsPolicy.isPolicyHotkeyTarget(e.target)||state.page!=='rp'||busy||$('settings').open||/TEXTAREA|INPUT|SELECT/.test(e.target.tagName)||e.altKey)return;
   const k=e.key.toLowerCase();if((e.ctrlKey||e.metaKey)&&k==='z'){e.preventDefault();travel(e.shiftKey?1:-1);return}if(e.ctrlKey||e.metaKey)return;
   if(e.target.closest('select,input,button'))return;
   const tile=e.target.closest('.wb-tile');if(!tile)return;
   if(['arrowup','arrowdown','x'].includes(k)){e.preventDefault();if(!e.repeat)correct(tile.dataset.id,k==='arrowup'?'higher':k==='arrowdown'?'lower':'irrelevant')}
   if(['arrowleft','arrowright','j','k'].includes(k)){e.preventDefault();const tiles=[...$('wbGrid').querySelectorAll('.wb-tile')],index=tiles.indexOf(tile),offset=k==='arrowleft'||k==='k'?-1:1;tiles[Math.max(0,Math.min(tiles.length-1,index+offset))]?.focus()}
  });
  $('wbApply').onclick=async()=>{
   if(!await flush()||interpretation?.blocked||!previewReady)return;
   const token=sequence;
   $('wbApply').disabled=true;
   const edited=draft.rules.some(r=>r.edited)||JSON.stringify(draft.rules)!==parsedSnapshot,payload=edited||!draft.text.trim()?{text:draft.text,rules:draft.rules}:{text:draft.text};
   let compiled;
   try{compiled=await request('/api/directives/compile','POST',payload)}catch(error){if(token===sequence){interpretation={path:interpretation?.path,readback:'Apply stopped. Active priorities have not changed.',warnings:[error.message],blocked:true};renderInterpretation();toast(error.message)}return}
   if(token!==sequence)return;
   interpretation={path:compiled.path,readback:compiled.readback,warnings:compiled.warnings||[],blocked:!!compiled.blocked};renderInterpretation();
   if(compiled.blocked)return;
   if(!edited&&(compiled.path==='llm'||(compiled.path==='research'||compiled.path?.startsWith('research/')))){draft.rules=compiled.rules.map(normalize);parsedSnapshot=JSON.stringify(draft.rules);renderChips()}
   pendingDuration=compiled.durationMin||0;
   if(!await preview())return;
   $('wbReadback').textContent=compiled.readback;
   $('wbPath').textContent=compilerLabel(compiled.path);
   const list=$('wbWarnings');list.replaceChildren();
   for(const warning of compiled.warnings||[]){const item=document.createElement('li');item.textContent=warning;if(warning.startsWith('Blocked'))item.className='blocked';list.append(item)}
   list.hidden=!compiled.warnings?.length;
   $('wbConfirmText').textContent=`${changes(items,rows())} images change priority. Add ${draft.rules.length} draft rules and save ${Object.keys(draft.pins).length} tier exceptions.`;
   $('wbConfirmApply').disabled=compiled.blocked||(!draft.rules.length&&!Object.keys(draft.pins).length);
   confirmedDraft=draftKey();closeFocus='apply';$('wbConfirm').showModal();
   ($('wbConfirmApply').disabled?$('wbEditApply'):$('wbConfirmApply')).focus();
  };
  $('wbConfirm').addEventListener('close',()=>{if(closeFocus==='apply')$('wbApply').focus();else if(closeFocus==='edit')focusInput();closeFocus='apply'});
  $('wbCancelApply').onclick=()=>{closeFocus='apply';$('wbConfirm').close()};
  $('wbEditApply').onclick=()=>{closeFocus='edit';$('wbConfirm').close();focusInput()};
  $('wbConfirmApply').onclick=async()=>{
   if(busy||interpretationPending||interpretation?.blocked||!previewReady||confirmedDraft!==draftKey())return;
   busy=true;updateApply();$('wbConfirmApply').disabled=true;
   try{
    let conflicts=[];
    if(draft.rules.length){const added=await request('/api/directives','POST',{text:draft.text||draft.rules.map(ruleText).join('; ').slice(0,500),rules:draft.rules,durationMin:pendingDuration});conflicts=added.conflicts||[];draft.rules=[]}
    for(const [id,manual]of Object.entries(draft.pins)){if(findItem(id))await patchItem(id,{manual});delete draft.pins[id]}
    closeFocus='confirm';$('wbConfirm').close();await reset('Applied to live feed');$('nlIn').focus();toast(conflicts.length?gsPolicy.conflictToast(conflicts):'Collection priorities applied to live feed');
   }catch(error){renderChips();closeFocus='apply';$('wbConfirm').close();if(!error.policyConflicts)toast('Apply stopped: '+error.message+'; successful changes are already live.')}finally{busy=false;confirmedDraft=null;updateApply();live()}
  };
 }
 function focusInput(){const input=$('nlIn');input.focus();input.setSelectionRange(input.value.length,input.value.length)}
 return {init,live,activate,textChanged};
})();
