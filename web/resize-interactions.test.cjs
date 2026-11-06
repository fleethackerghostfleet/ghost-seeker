const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');

function classes(values=[]){const s=new Set(values);return {add:(...v)=>v.forEach(x=>s.add(x)),remove:(...v)=>v.forEach(x=>s.delete(x)),contains:v=>s.has(v),toggle(v,on){if(on)s.add(v);else s.delete(v)},[Symbol.iterator]:()=>s.values()}}
function style(){return {setProperty(k,v){this[k]=v},removeProperty(k){delete this[k]}}}
function setup(bar='left'){
 const handles=[],frames=[],listeners={},panels=new Map(),calls=[],stored=new Map([['gs.settings',JSON.stringify({bar})]]);
 const definitions=[['controls','.fhead',300],['drawer','.drawer',400],['review','.rvside',320],['priorities','.wb-layout > aside',300],['briefing','.brief-controls',300],['commanderQueue','.queue',300],['commanderBrief','.brief',320]];
 const body={style:style(),classList:classes(['drawer-open']),append:el=>handles.push(el)};
 let context;
 for(const [key,selector,width] of definitions){
  panels.set(selector,{visible:true,left:100,top:20,bottom:620,getClientRects(){return this.visible?[{}]:[]},getBoundingClientRect(){const w=context.gsSettings.get().widths[key]||width;return {left:this.left,right:this.left+w,top:this.top,bottom:this.bottom,width:w}}});
 }
 const document={body,documentElement:{style:style()},getElementById:()=>null,querySelector:selector=>panels.get(selector)||null,
  addEventListener(type,fn){listeners['document:'+type]??=[];listeners['document:'+type].push(fn)},
  createElement(){return {style:{},attrs:{},handlers:{},captured:new Set(),focused:0,setAttribute(k,v){this.attrs[k]=String(v)},addEventListener(k,fn){this.handlers[k]=fn},focus(){this.focused++},setPointerCapture(id){this.captured.add(id)},releasePointerCapture(id){this.captured.delete(id);this.released=id}}}
 };
 context=vm.createContext({document,innerWidth:1400,innerHeight:800,localStorage:{getItem:k=>stored.get(k)||null,setItem:(k,v)=>stored.set(k,v)},Event:class{constructor(type){this.type=type}},
  dispatchEvent(){},addEventListener(type,fn){listeners[type]??=[];listeners[type].push(fn)},requestAnimationFrame:fn=>frames.push(fn),ResizeObserver:class{observe(){}disconnect(){}},MutationObserver:class{observe(){}}
 });context.window=context;
 vm.runInContext(fs.readFileSync(path.join(__dirname,'static/settings.js'),'utf8'),context);
 const resize=context.gsSettings.resize;context.gsSettings.resize=(...args)=>{calls.push(args);resize(...args)};
 vm.runInContext(fs.readFileSync(path.join(__dirname,'static/resize.js'),'utf8'),context);
 const handle=key=>handles[definitions.findIndex(d=>d[0]===key)];
 return {context,body,panels,calls,stored,handle,width:key=>context.gsSettings.get().widths[key],
  update(){for(const fn of listeners.resize)fn();while(frames.length)frames.shift()()},
  fire(key,type,extra={}){const e={button:0,pointerId:7,clientX:100,key:'',shiftKey:false,prevented:0,stopped:0,preventDefault(){this.prevented++},stopPropagation(){this.stopped++},...extra};handle(key).handlers[type](e);return e}
 };
}

test('left-panel dragging is anchored at pointerdown, clamps width and ends on release',()=>{
 const h=setup(),handle=h.handle('controls');
 h.fire('controls','pointermove',{clientX:150});
 h.fire('controls','pointerdown',{button:2});
 assert.equal(handle.focused,0);assert.equal(handle.captured.size,0);assert.equal(h.calls.length,0);
 const down=h.fire('controls','pointerdown');
 assert.equal(down.prevented,1);assert.equal(handle.focused,1);assert(handle.captured.has(7));
 h.fire('controls','pointermove',{clientX:130});assert.equal(h.width('controls'),330);
 h.fire('controls','pointermove',{clientX:1000});assert.equal(h.width('controls'),480);
 h.fire('controls','pointermove',{clientX:-1000});assert.equal(h.width('controls'),240);
 h.fire('controls','pointermove',{clientX:150});assert.equal(h.width('controls'),350);
 assert.equal(JSON.parse(h.stored.get('gs.settings')).widths.controls,350);
 h.fire('controls','pointerup');assert.equal(handle.released,7);assert.equal(handle.captured.size,0);
 const count=h.calls.length;h.fire('controls','pointermove',{clientX:200});
 assert.equal(h.calls.length,count);assert.equal(h.width('controls'),350);
});

test('lost pointer capture cancels the drag and a later drag starts from the current width',()=>{
 const h=setup();h.fire('controls','pointerdown');h.fire('controls','pointermove',{clientX:130});
 h.handle('controls').captured.delete(7);h.fire('controls','lostpointercapture');
 const count=h.calls.length;h.fire('controls','pointermove',{clientX:300});
 assert.equal(h.calls.length,count);assert.equal(h.width('controls'),330);
 h.fire('controls','pointerdown',{clientX:200});h.fire('controls','pointermove',{clientX:210});
 assert.equal(h.width('controls'),340);
});

test('right-hand panel drag and keyboard directions are inverted',async t=>{
 for(const key of ['controls','drawer','review','commanderBrief'])await t.test(key,()=>{
  const h=setup('right'),width=h.panels.get({controls:'.fhead',drawer:'.drawer',review:'.rvside',commanderBrief:'.brief'}[key]).getBoundingClientRect().width;
  assert.equal(h.handle(key).hidden,false);assert.equal(h.handle(key).style.left,'95px');
  h.fire(key,'pointerdown');h.fire(key,'pointermove',{clientX:120});assert.equal(h.width(key),width-20);
  h.fire(key,'pointerup');h.fire(key,'keydown',{key:'ArrowRight'});assert.equal(h.width(key),width-30);
  h.fire(key,'keydown',{key:'ArrowLeft',shiftKey:true});assert.equal(h.width(key),width+10);
 });
});

test('keyboard steps, Home and double-click resize/reset only on supported keys',()=>{
 const h=setup();
 for(const key of ['Tab','Enter','ArrowUp']){const e=h.fire('controls','keydown',{key});assert.equal(e.prevented,0);assert.equal(e.stopped,0)}
 assert.equal(h.calls.length,0);
 const e=h.fire('controls','keydown',{key:'ArrowRight'});
 assert.equal(e.prevented,1);assert.equal(e.stopped,1);assert.equal(h.width('controls'),310);
 h.fire('controls','keydown',{key:'ArrowLeft',shiftKey:true});assert.equal(h.width('controls'),270);
 h.fire('controls','keydown',{key:'Home'});assert.equal(h.width('controls'),undefined);
 assert.equal(h.body.style['--width-controls'],undefined);
 h.fire('controls','keydown',{key:'ArrowRight',shiftKey:true});assert.equal(h.width('controls'),340);
 h.fire('controls','dblclick');assert.equal(h.width('controls'),undefined);
 assert.deepEqual(h.calls.slice(-1),[['controls',null]]);
});

test('visibility follows viewport breakpoints, control position, drawer state and mounted geometry',()=>{
 const h=setup('top');assert.equal(h.handle('controls').hidden,true);
 assert.equal(h.handle('priorities').hidden,false);
 h.context.innerWidth=900;h.update();assert(h.handle('priorities').hidden);
 h.context.innerWidth=901;h.update();assert.equal(h.handle('priorities').hidden,false);assert(h.handle('commanderQueue').hidden);
 h.context.innerWidth=1100;h.update();assert(h.handle('commanderQueue').hidden);
 h.context.innerWidth=1101;h.update();assert.equal(h.handle('commanderQueue').hidden,false);
 h.body.classList.remove('drawer-open');h.update();assert(h.handle('drawer').hidden);
 h.body.classList.add('drawer-open');h.update();assert.equal(h.handle('drawer').hidden,false);
 const panel=h.panels.get('.wb-layout > aside');panel.visible=false;h.update();assert(h.handle('priorities').hidden);
 h.panels.delete('.wb-layout > aside');h.update();assert(h.handle('priorities').hidden);
 panel.visible=true;h.panels.set('.wb-layout > aside',panel);h.update();assert.equal(h.handle('priorities').hidden,false);
 for(const side of ['left','right'])assert.equal(setup(side).handle('controls').hidden,false);
 assert.equal(setup('bottom').handle('controls').hidden,true);
});

test('handle geometry clips to the viewport and refreshes after a width change',()=>{
 const h=setup(),panel=h.panels.get('.fhead'),handle=h.handle('controls');
 panel.top=-25;panel.bottom=950;h.context.innerHeight=700;h.update();
 assert.equal(handle.style.left,'395px');assert.equal(handle.style.top,'0px');assert.equal(handle.style.height,'700px');
 h.fire('controls','keydown',{key:'ArrowRight'});assert.equal(handle.style.left,'405px');assert.equal(handle.attrs['aria-valuenow'],'310');
 panel.top=800;h.update();assert.equal(handle.style.height,'0px');
});
