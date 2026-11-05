const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');
const source=fs.readFileSync(path.join(__dirname,'static/app.js'),'utf8');
function setup(){
 const context=vm.createContext({imageCache:new Map(),setTimeout(){},clearTimeout(){}});
 vm.runInContext(`
  const elements=new Map();
  document={activeElement:null,body:{},querySelector:()=>null,addEventListener(type,fn){if(type==='keydown')this.keydown=fn}};
  $=id=>{if(!elements.has(id))elements.set(id,{style:{},setAttribute(){},click(){this.onclick()}});return elements.get(id)};
  window=globalThis;
 `,context);
 vm.runInContext(source.replace(/init\(\);\s*$/,''),context);
 return context;
}
function shortcuts(context){
 vm.runInContext(`
  gsPolicy={isPolicyHotkeyTarget:()=>false};actions=[];
  rvAct=action=>actions.push(action);reviewAskKey=key=>actions.push('ask:'+key);
  state.page='rv';
  function press(key,target,extra={}){let prevented=false;document.keydown({key,target,preventDefault(){prevented=true},...extra});return prevented}
 `,context);
 const start=source.indexOf(" document.addEventListener('keydown',e=>{if(document.querySelector('dialog[open]')");
 const end=source.indexOf(" $('rvKeys').onclick",start);
 assert.ok(start>=0&&end>start);
 vm.runInContext(source.slice(start,end),context);
}
test('review shortcuts leave native button activation and text editing alone, even during prompts',()=>{
 const c=setup();shortcuts(c);
 for(const asking of [false,true]){
  c.asking=asking;
  vm.runInContext(`
   rv.ask=asking?{}:null;
   for(const key of ['Enter',' ']){
    for(const tagName of ['BUTTON','INPUT','TEXTAREA','SELECT']){
     if(press(key,{tagName,closest:()=>null}))throw Error('native key prevented on '+tagName);
    }
    if(press(key,{tagName:'SPAN',closest:()=>({})}))throw Error('nested button prevented');
   }
   for(const key of ['Enter',' ','w','1','n']){
    if(press(key,{tagName:'DIV',isContentEditable:true,closest:()=>null}))throw Error('editing prevented');
    if(press(key,{tagName:'INPUT',closest:()=>null}))throw Error('input prevented');
   }
  `,c);
 }
 assert.equal(vm.runInContext('actions.length',c),0);
 vm.runInContext("rv.ask=null;press('Enter',{tagName:'BODY',closest:()=>null});press(' ',{tagName:'BODY',closest:()=>null})",c);
 assert.equal(vm.runInContext("actions.join(',')",c),'accept,accept');
});
test('Enter in reviewer input saves the trimmed name, without changing Space or IME composition',()=>{
 const c=setup();
 vm.runInContext(`
  saved={};renders=0;localStorage={setItem(k,v){saved[k]=v}};
  refreshReviewData=()=>{};renderReview=()=>renders++;
  setupReviewData();$('reviewerName').value='  Keyboard operator  ';
  let prevented=0;
  $('reviewerName').onkeydown({key:' ',preventDefault(){prevented++}});
  $('reviewerName').onkeydown({key:'Enter',isComposing:true,preventDefault(){prevented++}});
 `,c);
 assert.equal(vm.runInContext('renders',c),0);
 vm.runInContext("$('reviewerName').onkeydown({key:'Enter',preventDefault(){prevented++}})",c);
 assert.equal(vm.runInContext("saved.gsReviewer",c),'Keyboard operator');
 assert.equal(vm.runInContext("$('reviewerStatus').textContent",c),'Reviewing as Keyboard operator');
 assert.equal(vm.runInContext('prevented',c),1);
 vm.runInContext("$('reviewerName').value=' ';$('reviewerName').onkeydown({key:'Enter',preventDefault(){}})",c);
 assert.equal(vm.runInContext("saved.gsReviewer",c),'');
 assert.equal(vm.runInContext("$('reviewerStatus').textContent",c),'Set a name to enable review keys');
});
function feed(){
 const c=setup();
 vm.runInContext(`
  created=0;updated=0;tabWrites=0;opens=[];
  const grid=$('grid');grid.children=[];
  grid.insertBefore=(el,before)=>{el.remove();grid.children.splice(before?grid.children.indexOf(before):grid.children.length,0,el)};
  function card(id){return {dataset:{id},_tab:-1,
   get tabIndex(){return this._tab},set tabIndex(value){tabWrites++;this._tab=value},
   closest(){return grid.children.includes(this)?this:null},
   focus(){document.activeElement=this;grid.onfocusin({target:this})},
   remove(){const i=grid.children.indexOf(this);if(i>=0)grid.children.splice(i,1)},
   getBoundingClientRect(){throw Error('unexpected layout read')}
  }}
  makeCard=it=>{created++;return it.el=card(it.id)};updateCard=()=>updated++;
  visible=()=>({l:items,counts:{}});repeatCount=()=>0;uploadedCount=()=>0;
  renderTierSeg=()=>{};renderDirBar=()=>{};openDetail=id=>opens.push(id);
  setupFeedKeyboard();
  function populate(n){items=Array.from({length:n},(_,i)=>({id:String(i)}))}
  function pressCard(key,target=document.activeElement,extra={}){
   let prevented=false,stopped=false;
   grid.onkeydown({key,target,preventDefault(){prevented=true},stopPropagation(){stopped=true},...extra});
   return prevented&&stopped;
  }
 `,c);
 return c;
}
test('3k feed has one roving tab stop and ordinary flushes never recreate or update unchanged cards',()=>{
 const c=feed();
 vm.runInContext('populate(3000);renderFeed(true)',c);
 assert.equal(vm.runInContext("$('grid').children.length",c),120);
 assert.equal(vm.runInContext("$('grid').children.filter(el=>el.tabIndex===0).length",c),1);
 vm.runInContext("findItem('4').el.focus();created=updated=tabWrites=0;renderFeed(true)",c);
 assert.equal(vm.runInContext('created+updated+tabWrites',c),0);
 assert.equal(vm.runInContext('feedFocusID',c),'4');
 vm.runInContext("dirtyItems.add('6');renderFeed(true)",c);
 assert.equal(vm.runInContext('updated',c),1);
 assert.equal(vm.runInContext('created+tabWrites',c),0);
});
test('delegated arrows move focus without opening details and grow the bounded window only at its edge',()=>{
 const c=feed();
 vm.runInContext("populate(3000);renderFeed(true);findItem('0').el.focus();pressCard('ArrowLeft');pressCard('ArrowUp')",c);
 assert.equal(vm.runInContext('document.activeElement.dataset.id',c),'0');
 vm.runInContext("pressCard('ArrowRight');pressCard('ArrowDown');pressCard('ArrowLeft')",c);
 assert.equal(vm.runInContext('document.activeElement.dataset.id',c),'1');
 assert.equal(vm.runInContext('opens.length',c),0);
 vm.runInContext("findItem('119').el.focus();created=updated=0;pressCard('ArrowRight')",c);
 assert.equal(vm.runInContext('document.activeElement.dataset.id',c),'120');
 assert.equal(vm.runInContext('created',c),120);
 assert.equal(vm.runInContext("$('grid').children.filter(el=>el.tabIndex===0).length",c),1);
});
test('Enter and Space open a focused card once, modifiers and nested controls are left alone',()=>{
 const c=feed();
 vm.runInContext("populate(1);renderFeed(true);findItem('0').el.focus()",c);
 assert.equal(vm.runInContext("pressCard('Enter')&&pressCard(' ')",c),true);
 vm.runInContext("pressCard('Enter',document.activeElement,{repeat:true});pressCard(' ',document.activeElement,{ctrlKey:true});pressCard('Enter',{closest:()=>findItem('0').el})",c);
 assert.equal(vm.runInContext("opens.join(',')",c),'0,0');
 assert.equal(vm.runInContext("pressCard('ArrowDown')",c),true);
 assert.equal(vm.runInContext('document.activeElement.dataset.id',c),'0');
});
test('roving focus survives reorder, falls back on removal, and tolerates empty or stale card events',()=>{
 const c=feed();
 vm.runInContext("populate(3);renderFeed(true);findItem('1').el.focus();items=items.slice().reverse();renderFeed(true)",c);
 assert.equal(vm.runInContext('document.activeElement.dataset.id',c),'1');
 vm.runInContext("items=items.filter(it=>it.id!=='1');renderFeed(true)",c);
 assert.equal(vm.runInContext('document.activeElement.dataset.id',c),'2');
 assert.equal(vm.runInContext("$('grid').children.filter(el=>el.tabIndex===0).length",c),1);
 vm.runInContext("items=[];renderFeed(true);pressCard('ArrowRight',{closest:()=>({dataset:{id:'stale'}})})",c);
 assert.equal(vm.runInContext('feedFocusID',c),null);
 assert.equal(vm.runInContext("$('grid').children.length",c),0);
});
test('cards expose button semantics and theme-aware visible keyboard focus',()=>{
 assert.match(source,/el\.tabIndex=-1;el\.setAttribute\('role','button'\)/);
 assert.match(source,/el\.setAttribute\('aria-label',`P\$\{tier\}.*Open image details`\)/);
 const css=fs.readFileSync(path.join(__dirname,'static/style.css'),'utf8');
 assert.match(css,/\.card:focus-visible\{outline:3px solid var\(--accent\);outline-offset:-4px\}/);
 assert.match(css,/body\.night \.card:focus-visible\{outline-color:#ff6666\}/);
});
