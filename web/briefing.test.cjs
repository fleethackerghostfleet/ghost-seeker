const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');
const B=require('./static/briefing-model.js');
const classes={boat:{g:'vessel',lab:'Small boat'},bird:{g:'clutter',lab:'Bird'}};
function item(){return {id:'frame-1',src:{id:'EO-1'},ts:new Date('2026-11-04T20:00:00Z'),tier:2,dets:[{cls:'boat',conf:.86,b:{x:20,y:30,w:100,h:50},attrs:{moving:true,dark:false}},{cls:'bird',b:{}}]}}
function draft(n=1){return {created:'2026-11-04T20:01:00Z',title:'Test briefing',assessment:'Review required.',gaps:'Identity unknown.',layout:'combined',records:Array.from({length:n},(_,i)=>({...B.snapshot(item(),0,classes),id:'frame-'+i}))}}

test('snapshot identifies an individual vessel and detaches mutable metadata',()=>{
 const source=item(),record=B.snapshot(source,0,classes);
 source.dets[0].b.x=999;source.dets[0].attrs.moving=false;source.tier=1;
 assert.equal(record.box.x,20);assert.equal(record.priority,2);
 assert.deepEqual(record.observations,['moving']);assert.equal(record.confidence,.86);
 assert.equal(record.key,JSON.stringify(['frame-1',0]));assert.equal(record.image,null);
 assert.throws(()=>B.snapshot(source,1,classes),/vessel/);
 assert.throws(()=>B.snapshot(source,3,classes),/vessel/);
});
test('empty, single, multi and overflow pagination preserve evidence order',()=>{
 assert.deepEqual(B.pages([],'combined'),[]);
 for(const [n,mode,count] of [[1,'combined',1],[4,'combined',1],[5,'combined',2],[9,'combined',3],[5,'individual',5]]){
  const d=draft(n),pages=B.pages(d.records,mode);
  assert.equal(pages.length,count);assert.deepEqual(pages.flat(),d.records);
 }
});
test('scenes retain provenance, uncertainty and marking without inventing identity',()=>{
 const d=draft(5),before=JSON.stringify(d),scenes=B.scenes(d);
 assert.equal(scenes.length,2);assert.equal(JSON.stringify(d),before);
 for(const scene of scenes){
  assert(scene.some(e=>e.value===B.marking));
  assert(scene.some(e=>e.value?.includes('Image unavailable')));
  assert(scene.some(e=>e.value?.includes('Identity / affiliation: unknown')));
  assert(scene.some(e=>e.value?.includes('2026-11-04T20:00:00.000Z')));
  for(const e of scene){assert(e.x>=0&&e.y>=0&&e.x+e.w<=1280&&e.y+e.h<=720)}
 }
});
test('maximum analyst text shrinks rather than truncating the data',()=>{
 const d=draft(4);d.title='W'.repeat(90);d.assessment='W'.repeat(500);d.gaps='W'.repeat(220);
 const scene=B.scenes(d)[0];
 for(const value of [d.title,d.assessment,d.gaps]){
  const e=scene.find(e=>e.value===value);
  assert(e);assert(Math.ceil(value.length/(e.w/e.size))*e.size*1.15<=e.h);
 }
});
test('many short analyst lines reflow without losing notes',()=>{
 const d=draft();d.assessment=Array.from({length:10},(_,i)=>'Note '+i).join('\n');
 const e=B.scenes(d)[0].find(e=>e.value?.startsWith('Note 0'));
 assert.equal(e.value,d.assessment.replace(/\s+/g,' '));
 assert(Math.ceil(e.value.length/(e.w/e.size))*e.size*1.15<=e.h);
});
test('real bundled PowerPoint writer produces editable text and embedded imagery',async()=>{
 const context=vm.createContext({console,setTimeout,clearTimeout,Uint8Array,ArrayBuffer,TextEncoder,TextDecoder});
 vm.runInContext(fs.readFileSync(path.join(__dirname,'static/pptxgen.bundle.js'),'utf8'),context);
 const pptx=vm.runInContext('new PptxGenJS()',context);pptx.layout='LAYOUT_WIDE';
 const d=draft(5);d.records[0].image='data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aX1kAAAAASUVORK5CYII=';
 const ui=fs.readFileSync(path.join(__dirname,'static/briefing.js'),'utf8');
 // Exercise the exact production scene-to-PPTX adapter, not a second implementation.
 const adapter=ui.slice(ui.indexOf('B.scenes(draft).forEach(scene=>{const slide=pptx.addSlide()'),ui.indexOf('   await pptx.writeFile'));
 context.B=B;context.draft=d;context.pptx=pptx;vm.runInContext(adapter,context);
 const bytes=await pptx.write({outputType:'uint8array',compression:true});
 context.bytes=bytes;
 const archive=await vm.runInContext('JSZip.loadAsync(bytes)',context);
 const xml=await archive.file('ppt/slides/slide1.xml').async('string');
 assert.match(xml,/Test briefing/);assert.match(xml,/TRAINING AND DEMONSTRATION ONLY/);
 assert.match(xml,/Identity \/ affiliation: unknown/);assert(archive.file('ppt/slides/slide2.xml'));
 assert(Object.keys(archive.files).some(name=>name.startsWith('ppt/media/')&&name.endsWith('.png')));
});
