const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');

const source=fs.readFileSync(path.join(__dirname,'static/render.js'),'utf8');

test('marine mammal draws a body in side and top views',()=>{
 const {context,canvases}=setup();
 context.document.createElement('canvas');
 const ctx=canvases[0].ctx;
 context.drawSide(ctx,'marine mammal',{x:10,y:20,w:48,h:19},'eo',{});
 assert.ok(canvases[0].calls.some(c=>c[0]==='ellipse'));
 canvases[0].calls.length=0;
 context.drawTop(ctx,'marine mammal',{cx:100,cy:100,L:48,B:19,a:0},'eo',{});
 assert.ok(canvases[0].calls.some(c=>c[0]==='ellipse'));
 assert.ok(canvases[0].calls.some(c=>c[0]==='fill'));
});

function setup(){
 const canvases=[];
 const math=Object.create(Math);
 const context=vm.createContext({Math:math,document:{
  getElementById:id=>id==='present'?{id}:null,
  createElement(tag){
   assert.equal(tag,'canvas');
   const calls=[];
   const pixels=new Uint8ClampedArray([100,150,200,255]);
   const gradient={addColorStop(...args){calls.push(['addColorStop',...args])}};
   const ctx=new Proxy({
    getImageData(){return {data:pixels}},
    createLinearGradient(){return gradient},
    createRadialGradient(){return gradient},
   },{get(target,key){return key in target?target[key]:(...args)=>calls.push([key,...args])}});
   const canvas={calls,pixels,ctx,getContext(kind){assert.equal(kind,'2d');return ctx},
    toDataURL(...args){calls.push(['toDataURL',...args]);return `data:${args[0]};base64,frame${canvases.length}`}};
   canvases.push(canvas);
   return canvas;
  },
 }});
 vm.runInContext(source,context);
 const item=()=>({id:'one',seed:42,src:{id:'EO',scene:'open',mode:'eo'},hz:.4,ts:new Date('2026-01-02T03:04:05Z'),frame:7,dets:[]});
 return {context,canvases,math,item};
}

test('seeded random helpers are reproducible and stay in bounds',()=>{
 const {context,math}=setup();
 const a=context.mulberry32(42),b=context.mulberry32(42),c=context.mulberry32(43);
 const samples=Array.from({length:100},()=>a());
 assert.deepEqual(samples,Array.from({length:100},()=>b()));
 assert.notEqual(samples[0],c());
 assert.ok(samples.every(n=>n>=0&&n<1));
 math.random=()=>0;
 assert.equal(vm.runInContext('rand(-4,8)',context),-4);
 assert.equal(vm.runInContext('pick(["first","last"])',context),'first');
 assert.equal(vm.runInContext('pick([])',context),undefined);
 assert.equal(vm.runInContext('chance(0)',context),false);
 assert.equal(vm.runInContext('chance(1)',context),true);
 math.random=()=>.999;
 assert.equal(vm.runInContext('pick(["first","last"])',context),'last');
 assert.equal(vm.runInContext('zulu(new Date("2026-01-02T03:04:05Z"))',context),'03:04:05Z');
 assert.equal(vm.runInContext('$("absent")',context),null);
});

test('empty detections render JPEG with dimensions and untrusted metadata as canvas text, not HTML',()=>{
 const {context,canvases,item,math}=setup();
 const row=item(),payload='"><img src=x onerror=alert(1)>&\' 🚢';
 row.src.id=payload;
 const original=math.random;
 assert.match(context.renderImage(row),/^data:image\/jpeg;/);
 const canvas=canvases[0];
 assert.equal(canvas.width,480);
 assert.equal(canvas.height,300);
 assert.ok(canvas.calls.some(c=>c[0]==='fillText'&&c[1]===`${payload}  2026-01-02 03:04:05Z`));
 assert.ok(canvas.calls.some(c=>c[0]==='fillText'&&c[1]==='FRM 000007  EO'));
 assert.deepEqual(canvas.calls.at(-1),['toDataURL','image/jpeg',.82]);
 assert.equal(math.random,original);
});

test('rendering is seeded per item and restores random even on null/invalid inputs and canvas errors',()=>{
 const {context,canvases,item,math}=setup();
 const original=math.random;
 context.renderImage(item());
 context.renderImage(item());
 assert.deepEqual(canvases[0].calls,canvases[1].calls);
 for(const row of [null,{...item(),dets:null},{...item(),src:null}]){
  assert.throws(()=>context.renderImage(row));
  assert.equal(math.random,original);
 }
 context.document.createElement=()=>{throw new Error('canvas unavailable')};
 assert.throws(()=>context.renderImage(item()),/canvas unavailable/);
 assert.equal(math.random,original);
});

test('IR and SAR convert pixels to grayscale while retaining alpha',()=>{
 for(const mode of ['ir','sar']){
  const {context,canvases,item,math}=setup();
  context.grain=()=>{};
  const row=item();row.src.mode=mode;
  const original=math.random;
  context.renderImage(row);
  const [r,g,b,a]=canvases[0].pixels;
  assert.ok(Math.abs(r-140.5)<=.5);
  assert.equal(r,g);
  assert.equal(g,b);
  assert.equal(a,255);
  assert.equal(math.random,original);
 }
});

test('grain alters RGB with clamping but leaves alpha untouched; zero amplitude is unchanged',()=>{
 const {context,math}=setup();
 const pixels=new Uint8ClampedArray([250,5,100,17]);
 let written;
 const ctx={getImageData:()=>({data:pixels}),putImageData:d=>{written=d.data}};
 math.random=()=>1;
 context.grain(ctx,40);
 assert.equal(written,pixels);
 assert.deepEqual(Array.from(pixels),[255,25,120,17]);
 context.grain(ctx,0);
 assert.deepEqual(Array.from(pixels),[255,25,120,17]);
 math.random=()=>0;
 context.grain(ctx,100);
 assert.deepEqual(Array.from(pixels),[205,0,70,17]);
});

test('placeholder is PNG summary-only artwork and imageFor caches each image source by ID',()=>{
 const {context,canvases,item}=setup();
 assert.match(context.factPlaceholder(),/^data:image\/png;/);
 assert.ok(canvases[0].calls.some(c=>c[0]==='fillText'&&c[1]==='SUMMARY ONLY'));
 assert.ok(canvases[0].calls.some(c=>c[0]==='fillText'&&c[1]==='no image has crossed the link yet'));
 const row=item();row.img='imagery/frame.jpg';
 assert.equal(context.imageFor(row),'/imagery/frame.jpg');
 row.img='changed.jpg';
 assert.equal(context.imageFor(row),'/imagery/frame.jpg');
 assert.equal(canvases.length,1);
 const live={id:'live',live:true,img:null};
 assert.match(context.imageFor(live),/^data:image\/png;/);
 assert.equal(context.imageFor(live),context.imageFor(live));
 assert.equal(canvases.length,2);
 const synthetic=item();synthetic.id='synthetic';synthetic.img='';
 assert.match(context.imageFor(synthetic),/^data:image\/jpeg;/);
 context.imageFor(synthetic);
 assert.equal(canvases.length,3);
 assert.throws(()=>context.imageFor(null));
});

test('detections are painted in depth order without mutating the input array',()=>{
 const {context,item}=setup(),row=item(),order=[];
 const near={cls:'near',b:{y:10,h:5}},far={cls:'far',b:{y:1,h:2}},top={cls:'top',b:{y:5,h:1,top:true}};
 row.dets=[near,far,top];
 context.drawSide=(_ctx,cls)=>order.push(cls);
 context.drawTop=(_ctx,cls)=>order.push(cls);
 context.renderImage(row);
 assert.deepEqual(order,['far','top','near']);
 assert.deepEqual(row.dets,[near,far,top]);
});
