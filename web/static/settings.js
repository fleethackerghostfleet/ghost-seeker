'use strict';
// Display settings: feed-control position, theme, and optional UI elements. Saved per browser.
window.gsSettings=(()=>{
 const KEY='gs.settings';
 const BARS=[['top','Top'],['left','Left'],['right','Right'],['bottom','Bottom']];
 const THEMES=[['cic','Dark'],['day','Day'],['red','Night mode (N)']];
 const PARTS=[['hstats','Header counts'],['live','Feed status'],['clock','Clock'],['user','Watch station'],['strip','Pipeline strip'],['dirbar','Active directives'],['ov','Thumbnail source & score'],['cl','Thumbnail captions'],['tbox','Thumbnail detection boxes']];
 const DENSITIES=['m','l'];
 const REVIEWS=['all','reviewed','sent','unreviewed'];
 const TEXT_SIZES=[['compact','Compact'],['default','Default'],['large','Large'],['extra-large','Extra large']];
 const TEXT_SCALES={compact:1,default:1.12,large:1.25,'extra-large':1.4};
 const defaults=()=>({bar:'top',theme:'cic',prev:'cic',hide:[],widths:{},density:'m',feed:cleanFeed(),textSize:'default'});
 function cleanFeed(value){return {showDuplicates:value?.showDuplicates===true,review:REVIEWS.includes(value?.review)?value.review:'all',sensor:typeof value?.sensor==='string'&&/^[A-Za-z0-9_.:/-]{1,128}$/.test(value.sensor)?value.sensor:'ALL',tier:['1','2','3','4'].includes(value?.tier)?value.tier:'all',dupDefault:'off'}}
 // "Sent by a person" is a temporary view; a reload must not leave the main feed stuck on it.
 function startFeed(value){const feed=cleanFeed(value);if(feed.review==='sent')feed.review='all';
  // Reset legacy auto-saved defaults once; marked settings preserve later user choices.
  if(value?.dupDefault!=='off')feed.showDuplicates=false;
  return feed}
 const WIDTHS={controls:[240,480],drawer:[340,780],review:[260,600],priorities:[260,540],briefing:[240,480],commanderQueue:[240,480],commanderBrief:[280,540]};
 function cleanWidths(value){
  const result={};
  for(const [key,[min,max]] of Object.entries(WIDTHS)){
   if(Number.isFinite(value?.[key]))result[key]=Math.round(Math.max(min,Math.min(max,value[key])));
  }
  return result;
 }
 let s=load();
 function load(){
  try{
   const v=JSON.parse(localStorage.getItem(KEY))||{};const d=defaults();
   return {bar:BARS.some(b=>b[0]===v.bar)?v.bar:d.bar,theme:THEMES.some(t=>t[0]===v.theme)?v.theme:d.theme,prev:v.prev==='day'?'day':'cic',hide:Array.isArray(v.hide)?v.hide.filter(h=>PARTS.some(p=>p[0]===h)):[],widths:cleanWidths(v.widths),density:DENSITIES.includes(v.density)?v.density:v.density==='xl'?'l':d.density,feed:startFeed(v.feed),textSize:TEXT_SIZES.some(t=>t[0]===v.textSize)?v.textSize:d.textSize};
  }catch{return defaults()}
 }
 function save(){try{localStorage.setItem(KEY,JSON.stringify(s))}catch{}}
 function apply(){
  const b=document.body;
  document.documentElement.style.setProperty('--text-scale',TEXT_SCALES[s.textSize]);
  window.gsTextLayout?.();
  for(const key of Object.keys(WIDTHS)){
   if(s.widths[key])b.style.setProperty('--width-'+key,s.widths[key]+'px');
   else b.style.removeProperty('--width-'+key);
  }
  [...b.classList].forEach(c=>{if(/^(bar|theme|hide)-/.test(c))b.classList.remove(c)});
  b.classList.add('bar-'+s.bar,'theme-'+s.theme);
  s.hide.forEach(h=>b.classList.add('hide-'+h));
  const grid=document.getElementById('grid');if(grid)grid.className='grid '+s.density;
  document.getElementById('densSeg')?.querySelectorAll('button').forEach(x=>x.classList.toggle('on',x.dataset.d===s.density));
  b.classList.toggle('night',s.theme==='red');
  const nb=document.getElementById('nightBtn');if(nb)nb.textContent=s.theme==='red'?'Exit night mode (N)':'Night mode (N)';
  const dlg=document.getElementById('settings');
  if(!dlg)return;
  dlg.querySelectorAll('.seg[data-k] button').forEach(x=>x.classList.toggle('on',s[x.parentElement.dataset.k]===x.dataset.v));
  dlg.querySelectorAll('input[data-p]').forEach(x=>x.checked=!s.hide.includes(x.dataset.p));
 }
 function set(k,v){if(k==='theme'&&v!=='red')s.prev=v;s[k]=v;save();apply()}
 function toggleRed(){set('theme',s.theme==='red'?s.prev:'red')}
 function build(){
  const dlg=document.getElementById('settings');
  if(!dlg)return;
  const seg=(k,opts)=>`<div class="seg" data-k="${k}">${opts.map(([v,l])=>`<button type="button" data-v="${v}">${l}</button>`).join('')}</div>`;
  dlg.innerHTML=`<h3>Display settings</h3>
  <div class="setsec"><div class="lbl">Feed controls position</div>${seg('bar',BARS)}</div>
  <div class="setsec"><div class="lbl">Text size</div>${seg('textSize',TEXT_SIZES)}<small>Compact uses the original size. Default is 12% larger.</small></div>
  <div class="setsec"><div class="lbl">Theme</div>${seg('theme',THEMES)}<small>Night mode is also available with the N key.</small></div>
  <div class="setsec"><div class="lbl">Show</div><div class="setparts">${PARTS.map(([p,l])=>`<label><input type="checkbox" data-p="${p}"> ${l}</label>`).join('')}</div></div>
  <p class="hint">Classification banners are always shown. Settings are saved in this browser.</p>
  <div class="setfoot"><button type="button" class="btn sm" id="setReset">Reset to defaults</button><span class="spacer"></span><button type="button" class="btn sm primary" id="setDone">Done</button></div>`;
  dlg.onclick=e=>{
   if(e.target===dlg){dlg.close();return}
   const b=e.target.closest('.seg[data-k] button');if(b)set(b.parentElement.dataset.k,b.dataset.v);
  };
  dlg.onchange=e=>{const p=e.target.dataset.p;if(!p)return;s.hide=s.hide.filter(h=>h!==p);if(!e.target.checked)s.hide.push(p);save();apply()};
  document.getElementById('setReset').onclick=()=>{s=defaults();save();apply();window.dispatchEvent(new Event('gs:feed-filters'))};
  document.getElementById('setDone').onclick=()=>dlg.close();
  document.getElementById('setBtn').onclick=()=>dlg.showModal();
 }
 build();apply();
 function resize(key,width){
  if(!WIDTHS[key])return;
  if(width===null)delete s.widths[key];
  else Object.assign(s.widths,cleanWidths({[key]:width}));
  save();apply();
 }
 function setFeed(value){s.feed=cleanFeed({...s.feed,...value});save()}
 function density(value){if(DENSITIES.includes(value))set('density',value)}
 function textSize(value){if(TEXT_SIZES.some(t=>t[0]===value))set('textSize',value)}
 return {toggleRed,resize,setFeed,density,textSize,get:()=>({...s,hide:[...s.hide],widths:{...s.widths},feed:{...s.feed}})};
})();
