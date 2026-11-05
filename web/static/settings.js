'use strict';
// Display settings: feed-control position, theme, and optional UI elements. Saved per browser.
window.gsSettings=(()=>{
 const KEY='gs.settings';
 const BARS=[['top','Top'],['left','Left'],['right','Right'],['bottom','Bottom']];
 const THEMES=[['cic','Dark'],['day','Day'],['red','Night mode (N)']];
 const PARTS=[['hstats','Header counts'],['live','Feed status'],['clock','Clock'],['user','Watch station'],['strip','Pipeline strip'],['dirbar','Active directives'],['ov','Thumbnail source & score'],['cl','Thumbnail captions'],['tbox','Thumbnail detection boxes']];
 const defaults=()=>({bar:'top',theme:'cic',prev:'cic',hide:[]});
 let s=load();
 function load(){
  try{
   const v=JSON.parse(localStorage.getItem(KEY))||{};const d=defaults();
   return {bar:BARS.some(b=>b[0]===v.bar)?v.bar:d.bar,theme:THEMES.some(t=>t[0]===v.theme)?v.theme:d.theme,prev:v.prev==='day'?'day':'cic',hide:Array.isArray(v.hide)?v.hide.filter(h=>PARTS.some(p=>p[0]===h)):[]};
  }catch{return defaults()}
 }
 function save(){try{localStorage.setItem(KEY,JSON.stringify(s))}catch{}}
 function apply(){
  const b=document.body;
  [...b.classList].forEach(c=>{if(/^(bar|theme|hide)-/.test(c))b.classList.remove(c)});
  b.classList.add('bar-'+s.bar,'theme-'+s.theme);
  s.hide.forEach(h=>b.classList.add('hide-'+h));
  b.classList.toggle('night',s.theme==='red');
  const nb=document.getElementById('nightBtn');if(nb)nb.textContent=s.theme==='red'?'Exit night mode (N)':'Night mode (N)';
  const dlg=document.getElementById('settings');
  dlg.querySelectorAll('.seg[data-k] button').forEach(x=>x.classList.toggle('on',s[x.parentElement.dataset.k]===x.dataset.v));
  dlg.querySelectorAll('input[data-p]').forEach(x=>x.checked=!s.hide.includes(x.dataset.p));
 }
 function set(k,v){if(k==='theme'&&v!=='red')s.prev=v;s[k]=v;save();apply()}
 function toggleRed(){set('theme',s.theme==='red'?s.prev:'red')}
 function build(){
  const dlg=document.getElementById('settings');
  const seg=(k,opts)=>`<div class="seg" data-k="${k}">${opts.map(([v,l])=>`<button type="button" data-v="${v}">${l}</button>`).join('')}</div>`;
  dlg.innerHTML=`<h3>Display settings</h3>
  <div class="setsec"><div class="lbl">Feed controls position</div>${seg('bar',BARS)}</div>
  <div class="setsec"><div class="lbl">Theme</div>${seg('theme',THEMES)}<small>Night mode is also available with the N key.</small></div>
  <div class="setsec"><div class="lbl">Show</div><div class="setparts">${PARTS.map(([p,l])=>`<label><input type="checkbox" data-p="${p}"> ${l}</label>`).join('')}</div></div>
  <p class="hint">Classification banners are always shown. Settings are saved in this browser.</p>
  <div class="setfoot"><button type="button" class="btn sm" id="setReset">Reset to defaults</button><span class="spacer"></span><button type="button" class="btn sm primary" id="setDone">Done</button></div>`;
  dlg.onclick=e=>{
   if(e.target===dlg){dlg.close();return}
   const b=e.target.closest('.seg[data-k] button');if(b)set(b.parentElement.dataset.k,b.dataset.v);
  };
  dlg.onchange=e=>{const p=e.target.dataset.p;if(!p)return;s.hide=s.hide.filter(h=>h!==p);if(!e.target.checked)s.hide.push(p);save();apply()};
  document.getElementById('setReset').onclick=()=>{s=defaults();save();apply()};
  document.getElementById('setDone').onclick=()=>dlg.close();
  document.getElementById('setBtn').onclick=()=>dlg.showModal();
 }
 build();apply();
 return {toggleRed,get:()=>({...s,hide:[...s.hide]})};
})();
