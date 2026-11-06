const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');
const source=fs.readFileSync(path.join(__dirname,'static/text-layout.js'),'utf8');

function node(){
 const value={style:{},dataset:{},attributes:{},children:[],replacements:0,
  setAttribute(name,value){this.attributes[name]=String(value)},
  replaceChildren(...children){this.children=children;this.replacements++},
  remove(){if(this.frame?.legend===this)this.frame.legend=null},
 };
 Object.defineProperty(value,'innerHTML',{set(){throw Error('detection labels must be text nodes')}});
 return value;
}
function label(text,width=70,height=20){
 const value=Object.assign(node(),{textContent:text,font:16,width,height,backgroundColor:'orange',color:'black'});
 Object.defineProperties(value,{
  offsetWidth:{get(){return Math.min(this.width,parseFloat(this.style.maxWidth)||this.width)}},
  offsetHeight:{get(){return this.height}},
 });
 return value;
}
function frame({width=200,height=140,left=100,top=50,borderLeft=2,borderTop=3,labels=[]}={}){
 const value={clientWidth:width,clientHeight:height,clientLeft:borderLeft,clientTop:borderTop,labels,obstacles:[],legend:null,
  getBoundingClientRect(){return {left,top,width:this.clientWidth?this.clientWidth+borderLeft*2:0,height:this.clientHeight?this.clientHeight+borderTop*2:0}},
  querySelectorAll(selector){return selector==='.box em'?this.labels:this.obstacles},
  querySelector(){return this.legend},append(legend){this.legend=legend;legend.frame=this},
 };
 for(const item of labels)item.parentElement={clientLeft:1,clientTop:2,getBoundingClientRect:()=>({left:left+borderLeft+20,top:top+borderTop+40})};
 return value;
}
function setup({frames=[],banners=[],fonts=true}={}){
 const queue=[],observed=new Set(),observeCalls=new Map(),unobserved=[],windowEvents={},properties={};
 let resize,mutate,fontReady,mutationOptions;
 const body={style:{setProperty(name,value){properties[name]=value}}};
 const document={body,querySelectorAll:selector=>selector==='body>.banner'?banners:frames,createElement:node};
 if(fonts)document.fonts={ready:{then(fn){fontReady=fn}}};
 const window={addEventListener(name,fn,options){windowEvents[name]={fn,options}}};
 vm.runInNewContext(source,{
  document,window,requestAnimationFrame(fn){queue.push(fn)},
  getComputedStyle:el=>({fontSize:el.font+'px',backgroundColor:el.backgroundColor,color:el.color}),
  ResizeObserver:class{constructor(fn){resize=fn}observe(el){observed.add(el);observeCalls.set(el,(observeCalls.get(el)||0)+1)}unobserve(el){observed.delete(el);unobserved.push(el)}},
  MutationObserver:class{constructor(fn){mutate=fn}observe(target,options){assert.equal(target,body);mutationOptions=options}},
 },{filename:'text-layout.js'});
 return {frames,banners,queue,observed,observeCalls,unobserved,properties,windowEvents,window,
  resize:()=>resize(),mutate:()=>mutate(),fontReady:()=>fontReady(),
  mutationOptions:()=>JSON.parse(JSON.stringify(mutationOptions)),
  flush(){assert.equal(queue.length,1);queue.shift()()},
 };
}

test('layout coalesces resize, mutation, load and font-ready signals into one animation frame',()=>{
 const s=setup();
 assert.equal(s.queue.length,1);
 s.resize();s.mutate();s.windowEvents.resize.fn();s.windowEvents.load.fn();s.fontReady();s.window.gsTextLayout();
 assert.equal(s.queue.length,1);
 assert.equal(s.windowEvents.load.options,true);
 assert.deepEqual(s.mutationOptions(),{childList:true,subtree:true});
 s.flush();
 assert.equal(s.properties['--banner-space'],'12px');
 assert.equal(s.queue.length,0);
 s.resize();s.mutate();
 assert.equal(s.queue.length,1);
 s.flush();
});

test('banner space tracks the tallest banner and works without the Font Loading API',()=>{
 const banners=[{getBoundingClientRect:()=>({height:18})},{getBoundingClientRect:()=>({height:40})}];
 const s=setup({banners,fonts:false});
 s.flush();assert.equal(s.properties['--banner-space'],'52px');
 banners.pop();s.mutate();s.flush();assert.equal(s.properties['--banner-space'],'30px');
 banners.length=0;s.mutate();s.flush();assert.equal(s.properties['--banner-space'],'12px');
});

test('removed frames are unobserved and newly inserted frames join layout exactly once',()=>{
 const first=frame(),second=frame();
 const s=setup({frames:[first]});
 s.flush();assert.ok(s.observed.has(first));
 assert.equal(s.observeCalls.get(first),1);
 s.frames.push(second);s.mutate();s.flush();
 assert.equal(s.observed.size,2);
 assert.equal(s.observeCalls.get(first),1);assert.equal(s.observeCalls.get(second),1);
 s.frames.shift();s.mutate();s.flush();
 assert.deepEqual(s.unobserved,[first]);
 assert.equal(s.observed.has(first),false);assert.equal(s.observed.has(second),true);
 s.resize();s.flush();assert.deepEqual(s.unobserved,[first]);
 assert.equal(s.observeCalls.get(first),1);assert.equal(s.observeCalls.get(second),1);
 s.frames.length=0;s.mutate();s.flush();assert.equal(s.observed.size,0);
 assert.deepEqual(s.unobserved,[first,second]);
 s.frames.push(first);s.mutate();s.flush();
 assert.equal(s.observeCalls.get(first),2);assert.equal(s.observeCalls.get(second),1);
 s.resize();s.flush();
 assert.equal(s.observeCalls.get(first),2);assert.equal(s.observeCalls.get(second),1);
});

test('zero-size frames defer label work until visible and positions respect frame and box borders',()=>{
 const item=label('Detection'),image=frame({width:0,height:0,labels:[item]});
 const s=setup({frames:[image]});
 s.flush();assert.ok(s.observed.has(image));assert.equal(item.style.left,undefined);
 assert.equal(item.style.fontSize,undefined);assert.equal(image.legend,null);
 image.clientWidth=200;image.clientHeight=140;s.resize();s.flush();
 assert.equal(item.style.left,'-1px');assert.equal(item.style.top,'-22px');
 assert.equal(item.style.bottom,'auto');assert.equal(item.style.right,'auto');
 assert.equal(item.style.fontSize,'16px');assert.equal(image.legend,null);
 const parent=item.parentElement.getBoundingClientRect(),bounds=image.getBoundingClientRect();
 const x=parent.left+item.parentElement.clientLeft+parseFloat(item.style.left)-bounds.left-image.clientLeft;
 const y=parent.top+item.parentElement.clientTop+parseFloat(item.style.top)-bounds.top-image.clientTop;
 assert.ok(x>=4&&y>=4&&x+item.offsetWidth<=image.clientWidth-4&&y+item.offsetHeight<=image.clientHeight-4);
});

test('crowded frames preserve hostile labels as accessible text and cache unchanged legend content',()=>{
 const hostile=`<img src=x onerror="alert('x')"> & <script>bad()</script>`;
 const labels=[label(hostile),label(`'quoted' & "detection"`)],image=frame({width:100,height:40,labels});
 const s=setup({frames:[image]});
 s.flush();
 const legend=image.legend;
 assert.ok(legend);assert.equal(legend.tabIndex,0);
 assert.deepEqual(legend.attributes,{role:'region','aria-label':'Detection labels'});
 assert.deepEqual(legend.children.map(child=>child.textContent),labels.map(item=>item.textContent));
 assert.equal(legend.children[0].style.backgroundColor,'orange');assert.equal(legend.children[0].style.color,'black');
 assert.ok(labels.every(item=>item.style.display==='none'));
 assert.equal(legend.replacements,1);
 s.mutate();s.flush();assert.equal(image.legend,legend);assert.equal(legend.replacements,1);
 labels[0].textContent='<svg onload="changed">';s.mutate();s.flush();
 assert.equal(legend.replacements,2);assert.equal(legend.children[0].textContent,labels[0].textContent);
 image.clientHeight=180;s.resize();s.flush();
 assert.equal(image.legend,null);assert.ok(labels.every(item=>item.style.display===''));
});

test('removing every detection clears an old legend instead of leaving stale labels',()=>{
 const image=frame({width:100,height:40,labels:[label('one'),label('two')]});
 const s=setup({frames:[image]});
 s.flush();assert.ok(image.legend);
 image.labels.length=0;s.mutate();s.flush();
 assert.equal(image.legend,null);assert.ok(s.observed.has(image));
});
