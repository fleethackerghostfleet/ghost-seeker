const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');
const source=fs.readFileSync(path.join(__dirname,'static/view-tabs.js'),'utf8');

function setup(width=300){
 const handlers=new Map(),windowHandlers={};
 const element=()=>({hidden:true,disabled:false,addEventListener(type,fn,options){handlers.set(this,{...handlers.get(this),[type]:{fn,options}})}});
 const left=element(),right=element(),strip=element();
 const root={clientWidth:width,querySelector:s=>s==='nav'?strip:s.includes('left')?left:right};
 let selected=0,position=0;
 const tabs=Array.from({length:4},(_,i)=>({width:100,closest:()=>tabs[i],getBoundingClientRect(){const start=tabs.slice(0,i).reduce((sum,t)=>sum+t.width,0)-position;return {left:start,right:start+this.width}}}));
 strip.children=tabs;
 strip.contains=tab=>tabs.includes(tab);
 strip.querySelector=()=>tabs[selected];
 strip.getBoundingClientRect=()=>({left:0,right:strip.clientWidth});
 const fire=(el,type,event={})=>handlers.get(el)?.[type]?.fn(event);
 Object.defineProperties(strip,{
  clientWidth:{get:()=>root.clientWidth-(left.hidden?0:28)-(right.hidden?0:28)},
  scrollWidth:{get:()=>Math.max(strip.clientWidth,tabs.reduce((sum,t)=>sum+t.width,0))},
  scrollLeft:{get:()=>Math.min(position,strip.scrollWidth-strip.clientWidth),set:v=>{position=Math.max(0,Math.min(v,strip.scrollWidth-strip.clientWidth))}},
 });
 strip.scrollBy=({left:delta})=>{strip.scrollLeft=strip.scrollLeft+delta;fire(strip,'scroll')};
 let resize,mutate;
 const observed=new Set();
 vm.runInNewContext(source,{
  document:{querySelectorAll:()=>[root]},window:{addEventListener:(type,fn)=>windowHandlers[type]=fn},
  ResizeObserver:class{constructor(fn){resize=fn}observe(el){observed.add(el)}unobserve(el){observed.delete(el)}},
  MutationObserver:class{constructor(fn){mutate=fn}observe(){}},
 });
 return {root,strip,left,right,tabs,resize,mutate,observed,fire,windowHandlers,handlers,
  select:i=>{selected=i;mutate()},selected:()=>selected,
  click:el=>{if(!el.disabled&&!el.hidden)fire(el,'click')},
 };
}

test('no overflow hides both arrows, including the exact-fit threshold',()=>{
 for(const width of [400,401,800]){
  const s=setup(width);
  assert.equal(s.left.hidden,true);assert.equal(s.right.hidden,true);
  assert.equal(s.strip.scrollLeft,0);
 }
});

test('overflow arrows disable only the boundary direction and never select a tab',()=>{
 const s=setup();
 assert.equal(s.left.hidden,false);assert.equal(s.right.hidden,false);
 assert.equal(s.left.disabled,true);assert.equal(s.right.disabled,false);
 s.click(s.right);
 assert.ok(s.strip.scrollLeft>0);
 assert.equal(s.selected(),0);
 assert.equal(s.left.disabled,false);
 while(!s.right.disabled)s.click(s.right);
 assert.equal(s.strip.scrollLeft,s.strip.scrollWidth-s.strip.clientWidth);
 assert.equal(s.left.disabled,false);assert.equal(s.right.disabled,true);
 s.click(s.left);
 assert.equal(s.right.disabled,false);
 assert.equal(s.selected(),0);
 while(!s.left.disabled)s.click(s.left);
 assert.equal(s.strip.scrollLeft,0);
});

test('native scroll events update both directions, including a middle position',()=>{
 const s=setup();
 s.strip.scrollLeft=50;s.fire(s.strip,'scroll');
 assert.equal(s.left.disabled,false);assert.equal(s.right.disabled,false);
 s.strip.scrollLeft=1000;s.fire(s.strip,'scroll');
 assert.equal(s.right.disabled,true);
 s.strip.scrollLeft=0;s.fire(s.strip,'scroll');
 assert.equal(s.left.disabled,true);assert.equal(s.right.disabled,false);
 assert.equal(s.handlers.get(s.strip).scroll.options.passive,true);
});

test('resize recalculates overflow and keeps the selected tab visible',()=>{
 const s=setup(800);
 s.select(3);
 s.root.clientWidth=300;s.resize();
 assert.equal(s.right.hidden,false);
 assert.equal(s.tabs[3].getBoundingClientRect().right,s.strip.clientWidth);
 assert.equal(s.right.disabled,true);
 s.root.clientWidth=800;s.windowHandlers.resize();
 assert.equal(s.left.hidden,true);assert.equal(s.right.hidden,true);
});

test('content/badge growth and removed tabs recalculate overflow without resetting manual scrolling',()=>{
 const s=setup(450);
 s.tabs[1].width=200;s.mutate();
 assert.equal(s.right.hidden,false);
 s.click(s.right);const position=s.strip.scrollLeft;
 s.mutate();assert.equal(s.strip.scrollLeft,position);
 const removed=s.tabs.pop();s.mutate();
 assert.equal(s.observed.has(removed),false);
 assert.equal(s.left.hidden,true);assert.equal(s.right.hidden,true);
 const added={width:150,getBoundingClientRect:()=>({left:400,right:550})};
 s.tabs.push(added);s.mutate();
 assert.equal(s.observed.has(added),true);
 assert.equal(s.right.hidden,false);
});

test('active-tab navigation and keyboard focus reveal offscreen tabs without activation',()=>{
 const s=setup();
 s.select(2);
 assert.equal(s.tabs[2].getBoundingClientRect().right,s.strip.clientWidth);
 s.fire(s.strip,'focusin',{target:s.tabs[3]});
 assert.equal(s.tabs[3].getBoundingClientRect().right,s.strip.clientWidth);
 assert.equal(s.selected(),2);
 s.select(0);
 assert.equal(s.strip.scrollLeft,0);
});

test('view-strip controls are labelled buttons, not navigation links, and are loaded by the app',()=>{
 const html=fs.readFileSync(path.join(__dirname,'static/index.html'),'utf8');
 assert.match(html,/<nav id="viewTabs" aria-label="Application views">/);
 for(const direction of ['left','right'])assert.match(html,new RegExp(`<button type="button"[^>]*data-scroll="${direction}"[^>]*aria-label="Scroll views ${direction}"[^>]*aria-controls="viewTabs"`));
 assert.match(html,/<script src="\/view-tabs.js" defer><\/script>/);
});
