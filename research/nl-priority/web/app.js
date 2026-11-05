'use strict';
const $ = id => document.getElementById(id);
let current = null, preview = null, draftContext = null, busy = false;
const number = value => Number(value || 0).toFixed(3);
function el(tag, text, cls) { const n = document.createElement(tag); if (text !== undefined) n.textContent = text; if (cls) n.className = cls; return n; }
function message(text = '', error = false) { $('message').textContent = text; $('message').className = error ? 'error' : ''; }
function controls() { $('preview').disabled = busy; $('apply').disabled = busy || !preview; $('undo').disabled = busy || !current?.historyDepth; $('dataset').disabled = busy; $('engine').disabled = busy; $('instruction').disabled=busy; for(const b of document.querySelectorAll('[data-prompt]'))b.disabled=busy; }
async function api(path, payload) {
  const response = await fetch(path, payload === undefined ? {} : {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify(payload)});
  const text = await response.text(); let data;
  try { data = text ? JSON.parse(text) : {}; } catch { throw new Error(`Server returned ${response.status}. Check the research service logs.`); }
  if (!response.ok) throw new Error(data.error || data.message || `Request failed (${response.status})`);
  return data;
}
async function action(fn) { if (busy) return; busy = true; controls(); message(); try { await fn(); } catch (e) { message(e.message, true); } finally { busy = false; controls(); } }
function renderRules(rules, container) {
  container.replaceChildren();
  if (!rules?.length) { container.append(el('p','No operator interests.','muted')); return; }
  for (const rule of rules) { const row=el('div',undefined,'rule'); row.append(el('span',rule.action,'action '+rule.action),el('span',rule.concept)); if (rule.action==='boost' && rule.weight) row.append(el('span',`${rule.weight}×`,'weight')); container.append(row); }
}
function safeImage(url) { return typeof url === 'string' && (/^data:image\/(jpeg|png|webp);base64,[a-z0-9+/=\s]+$/i.test(url) || /^https?:\/\//i.test(url) || /^\/[^/]/.test(url)); }
function cards(items, target, previous = []) {
  target.replaceChildren(); const positions = new Map(previous.map((it,i)=>[it.id,i]));
  if (!items?.length) { target.append(el('p','No preview yet.','muted')); return; }
  items.slice(0,50).forEach((it,i)=>{
    const card=el('article',undefined,'card'), top=el('div',undefined,'card-top');
    if (safeImage(it.imageUrl)) { const img=el('img'); img.src=it.imageUrl; img.alt=it.label || it.id; img.loading='lazy'; top.append(img); } else top.append(el('span','NO PREVIEW IMAGE','no-image'));
    top.append(el('span',String(i+1).padStart(2,'0'),'rank'));
    if(positions.has(it.id)){ const d=positions.get(it.id)-i; if(d) top.append(el('span',`${d>0?'↑':'↓'} ${Math.abs(d)}`,'movement'+(d<0?' down':''))); }
    const body=el('div',undefined,'card-body'), title=el('div',undefined,'card-title');
    title.append(el('strong',it.label || 'Unclassified'),el('span',number(it.priority),'score')); body.append(title,el('div',`${it.id} · unknown ${number(it.unknownness)}`,'metadata'));
    if(it.subtype) body.append(el('div',`Original subtype: ${it.subtype}`,'metadata'));
    if(it.experimentalLabel) body.append(el('div',`◉ ${it.experimentalLabel} · similarity ${number(it.similarity)}`,'experimental'));
    if(it.labelOverride) body.append(el('div',`Human label: ${it.labelOverride}`,'metadata'));
    if(it.abstention) body.append(el('div',`New label withheld: ${it.abstention}`,'metadata'));
    if(it.suppressed) body.append(el('div','DEPRIORITIZED · metadata retained','suppressed'));
    const reasons=el('details'); reasons.append(el('summary','Why this priority'));
    for(const reason of it.reasons || []) reasons.append(el('p',`${reason.scorer}: ${number(reason.value)}${reason.note?' · '+reason.note:''}`,'reason'));
    body.append(reasons); card.append(top,body); target.append(card);
  });
  if(items.length>50) target.append(el('p',`Showing the first 50 of ${items.length} images.`,'muted'));
}
function delivery(entries,target){ entries=(entries || []).filter(x=>!String(x.form).endsWith('FACT')); target.replaceChildren(); if(!entries?.length){target.append(el('li','No projected payloads.','muted'));return;} entries.slice(0,16).forEach(x=>{const li=el('li',`${x.id} · ${String(x.form).replace('FORM_KIND_','')}`);li.append(el('span',` · ${x.bytes} B · tick ${x.tick}${x.complete?' · complete':''}`));target.append(li);}); }
function showState(state) {
  current=state; $('revision').textContent=String(state.revision ?? 0).padStart(2,'0'); $('engine').value=state.engine || 'edgeml';
  renderRules(state.rules,$('rules')); $('interest-count').textContent=state.rules?.length || 0;
  const items=state.items || []; $('queue-count').textContent=`${items.length} images in the research queue`;
  $('empty').hidden=items.length>0; $('comparison').hidden=items.length===0; cards(items,$('before'));
  if(!preview) cards([],$('after')); controls();
}
function clearPreview(){ preview=null; $('draft').replaceChildren(el('p','Your interpretation and vocabulary changes will appear here.','muted')); $('warnings').replaceChildren(); $('preview-meta').textContent='Nothing changes until you apply.'; $('after-caption').textContent='Waiting for instruction'; cards([],$('after')); delivery([],$('delivery-after')); controls(); }
$('preview').addEventListener('click',()=>action(async()=>{
  const text=$('instruction').value.trim(); if(!text)throw new Error('Enter an instruction first.');
  clearPreview(); message('Compiling intent and comparing the queue…');
  preview=await api('/api/preview',{text,engine:$('engine').value,basePreviewId:draftContext || undefined});
  draftContext=preview.previewId; const draft=preview.draft || {}; $('draft').replaceChildren(el('p',draft.readback || 'Review these proposed interests.')); const rules=el('div'); renderRules(draft.rules,rules); $('draft').append(rules);
  if(draft.vocabulary?.length)$('draft').append(el('p',`Candidate concepts: ${draft.vocabulary.join(', ')}`,'hint'));
  $('warnings').replaceChildren(...(preview.warnings || []).map(w=>el('p',w)));
  cards(preview.before?.items || current?.items || [],$('before')); cards(preview.after?.items || [],$('after'),preview.before?.items || []);
  delivery(preview.before?.delivery,$('delivery-before')); delivery(preview.after?.delivery,$('delivery-after'));
  $('preview-meta').textContent=`${draft.path || 'compiler'} · ${Math.round(preview.elapsedMs || 0)} ms · based on revision ${preview.revision}`;
  $('after-caption').textContent='Proposed policy'; message('Preview ready. Review the interpretation before applying.');
}));
$('apply').addEventListener('click',()=>action(async()=>{if(!preview)return; const state=await api('/api/apply',{previewId:preview.previewId}); draftContext=null;clearPreview();showState(state);message('Applied to the research queue and incoming images.');}));
$('undo').addEventListener('click',()=>{if(busy)return;$('instruction').value='undo';$('preview').click();});
$('dataset').addEventListener('change',()=>action(async()=>{const file=$('dataset').files[0];if(!file)return;if(file.size>50*1024*1024)throw new Error('Queue JSON must be smaller than 50 MB.');let parsed;try{parsed=JSON.parse(await file.text());}catch{throw new Error('This file is not valid JSON. Use the crop ingestion tool to generate queue JSON.');}const payload=Array.isArray(parsed)?{items:parsed}:parsed;if(!Array.isArray(payload.items))throw new Error('Expected an items array.');await api('/api/items',payload);draftContext=null;clearPreview();showState(await api('/api/state'));message(`Imported ${payload.items.length} image records.`);$('dataset').value='';}));
for(const button of document.querySelectorAll('[data-prompt]'))button.addEventListener('click',()=>{$('instruction').value=button.dataset.prompt;clearPreview();$('instruction').focus();});
$('instruction').addEventListener('input',()=>{if(preview)clearPreview();});
$('engine').addEventListener('change',()=>{clearPreview();message('Preview an instruction to evaluate the selected scoring engine.');});
(async()=>{try{const [state,health]=await Promise.all([api('/api/state'),api('/api/health')]);showState(state);$('health').textContent='Research backend connected';}catch(e){$('health').textContent='Service unavailable';message(e.message,true);}})();
