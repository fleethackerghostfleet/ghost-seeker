(function(root){
 'use strict';
 const escape=value=>String(value??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
 const colors=new Set(['red','green','white','yellow','blue']);
 function eligible(item){return item?.night===true&&item.src?.mode==='eo'}
 function icon(lights=[]){
  const dots=lights.slice().sort((a,b)=>a.y-b.y).filter(p=>colors.has(p.colour));
  if(!dots.length)return '';
  return `<span class="light-pattern" role="img" aria-label="${escape(dots.map(p=>(p.flashing?'flashing ':'')+p.colour).join(', '))}">${dots.map(p=>`<i class="light-dot light-${p.colour}${p.flashing?' light-flash':''}"></i>`).join('')}</span>`;
 }
 function caveat(signal){return signal?.lowConfidence?`Low confidence · ${signal.confidenceNote||'extra unexplained lights — verify, no priority boost'}`:(signal?.assisted?'signal-assisted':'auto-detected — verify visually')+(signal?.boost>0?` · priority boost +${signal.boost.toFixed(2)}`:'')}
 function badge(item){return eligible(item)&&item.dets?.some(d=>d.sig)?'<span class="night-badge" title="Navigation-light interpretation — verify visually">☾ Lights</span>':''}
 function section(item,classes={}){
  if(!eligible(item))return '';
  return `<section class="dsec night-lights"><h3>Navigation lights (night)</h3>${(item.dets||[]).map((d,i)=>`<div class="light-contact${d.sig?.lowConfidence?' light-low-confidence':''}">${icon(d.lights)}<div><b>${escape(classes[d.cls]?.lab||d.cls)} · contact ${i+1}</b><p>${escape(d.sig?.line||'No recognized light pattern')}</p>${d.sig?`<small>${escape(d.sig.ref)} · ${escape(d.sig.aspect?.replaceAll('_',' ')||'unknown')}</small>`:''}<div><span class="light-caveat">${escape(caveat(d.sig))}</span></div></div></div>`).join('')}</section>`;
 }
 function why(item){
  if(!eligible(item))return [];
  return (item.dets||[]).filter(d=>d.sig?.severity==='urgent').map(d=>({c:d.sig.lowConfidence?'light-low-confidence':'al',t:`${icon(d.lights)}<strong>Navigation lights${d.sig.lowConfidence?' (no priority boost)':''}:</strong> ${escape(d.sig.line)} <small>(${escape(d.sig.ref)} · ${escape(caveat(d.sig))})</small>`}));
 }
 const api={icon,badge,section,why,eligible};
 if(typeof module==='object'&&module.exports)module.exports=api;else root.gsLights=api;
})(typeof globalThis==='object'?globalThis:this);
