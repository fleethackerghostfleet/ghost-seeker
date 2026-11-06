const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');
const common=require('./static/briefing-model.js');
const filename=path.join(__dirname,'static/briefing-model.js');
const script=new vm.Script(fs.readFileSync(filename,'utf8'),{filename});
function browser(){const context=vm.createContext({});script.runInContext(context);return context.BriefingModel}
const classes={boat:{g:'vessel',lab:'Small boat'},bird:{g:'clutter',lab:'Bird'}};
function item(){return {id:'sensor/frame',src:{id:'EO-1'},ts:'2026-11-04T20:00:00Z',tier:2,
 dets:[{cls:'boat',conf:.75,b:{x:10,y:20,w:30,h:40},attrs:{moving:true,dark:false}}]}}
function draft(B,count=1){return {created:'2026-11-04T20:01:00Z',title:'',assessment:'',gaps:'',layout:'combined',
 records:Array.from({length:count},(_,i)=>({...B.snapshot({...item(),id:'frame-'+i},0,classes),image:i%2?'data:image/png;base64,fake':null}))}}
const plain=value=>JSON.parse(JSON.stringify(value));

test('browser-global and CommonJS briefing models agree on snapshots, pagination and scenes',()=>{
 const B=browser();assert.equal(B.marking,common.marking);
 const record=B.snapshot(item(),0,classes);
 assert.deepEqual(plain(record),common.snapshot(item(),0,classes));
 const d=draft(common,5);
 assert.deepEqual(plain(B.pages(d.records,'combined')),common.pages(d.records,'combined'));
 assert.deepEqual(plain(B.scenes(d)),common.scenes(d));
});

test('snapshot requires a vessel at the exact detection index and preserves empty attributes',()=>{
 const B=browser(),source=item();
 source.dets.push({...source.dets[0],cls:'bird'});
 for(const index of [-1,1,2])assert.throws(()=>B.snapshot(source,index,classes),/Select a vessel detection/);
 assert.throws(()=>B.snapshot(source,0,{}),/Select a vessel detection/);
 delete source.dets[0].attrs;
 const record=B.snapshot(source,0,classes);
 assert.deepEqual(Array.from(record.observations),[]);
 assert.equal(record.id,'sensor/frame');assert.equal(record.source,'EO-1');assert.equal(record.index,0);
 assert.equal(record.time,'2026-11-04T20:00:00.000Z');assert.equal(record.image,null);
});

test('separate snapshots detach geometry and observations in both directions',()=>{
 const B=browser(),source=item(),first=B.snapshot(source,0,classes),second=B.snapshot(source,0,classes);
 first.box.x=900;first.observations.push('analyst-only');
 assert.equal(source.dets[0].b.x,10);assert.equal(second.box.x,10);
 assert.deepEqual(Array.from(second.observations),['moving']);
 source.dets[0].b.w=800;source.dets[0].attrs.dark=true;
 assert.equal(second.box.w,30);assert.deepEqual(Array.from(second.observations),['moving']);
});

test('empty browser drafts produce no phantom slides or pages in either layout',()=>{
 const B=browser();
 for(const layout of ['combined','individual']){
  const d={...draft(B,0),layout};
  assert.deepEqual(Array.from(B.pages(d.records,layout)),[]);assert.deepEqual(Array.from(B.scenes(d)),[]);
 }
});

test('pagination detaches page arrays, preserves record order and defaults to four per page',()=>{
 const B=browser(),d=draft(B,9),before=JSON.stringify(d);
 const pages=B.pages(d.records,'unrecognized');
 assert.deepEqual(Array.from(pages,p=>p.length),[4,4,1]);
 assert.deepEqual(Array.from(pages.flat(),r=>r.id),d.records.map(r=>r.id));
 pages[0].pop();pages.push([]);
 assert.equal(JSON.stringify(d),before);
 assert.deepEqual(Array.from(B.pages(d.records,'individual'),p=>p.length),Array(9).fill(1));
});

test('single-observation scene retains uncertainty and uses model defaults without inventing attributes',()=>{
 const B=browser(),d=draft(B);d.records[0].observations=[];
 const before=JSON.stringify(d),scene=B.scenes(d)[0],texts=Array.from(scene.filter(e=>e.type==='text'),e=>e.value);
 assert.ok(texts.includes('Maritime intelligence briefing'));
 assert.ok(texts.includes('Not entered. Analyst review required.'));
 assert.ok(texts.includes('Image unavailable at snapshot'));
 assert.ok(texts.some(t=>t.includes('Model attributes: none')));
 assert.ok(texts.some(t=>t.includes('Identity / affiliation: unknown')));
 assert.ok(texts.some(t=>t.includes('Position / course / speed: unavailable')));
 assert.ok(texts.includes(B.marking));assert.equal(texts.at(-1),'1');
 assert.equal(JSON.stringify(d),before);
});

test('images, placeholders and literal analyst text stay attached to the correct paginated evidence',()=>{
 const B=browser(),d=draft(B,5);d.title='<img src=x>';d.assessment='A & B';d.gaps='Unknown <identity>';
 const scenes=B.scenes(d);
 assert.deepEqual(Array.from(scenes,s=>s.filter(e=>e.type==='image').length),[2,0]);
 assert.deepEqual(Array.from(scenes,s=>s.filter(e=>e.value==='Image unavailable at snapshot').length),[2,1]);
 for(let i=0;i<scenes.length;i++){
  const scene=scenes[i];
  for(const text of [d.title,d.assessment,d.gaps])assert.ok(scene.some(e=>e.type==='text'&&e.value===text));
  assert.equal(scene.at(-1).value,String(i+1));
  for(const e of scene){assert.ok(Number.isFinite(e.x)&&Number.isFinite(e.y));assert.ok(e.x>=0&&e.y>=0&&e.x+e.w<=1280&&e.y+e.h<=720)}
 }
 assert.ok(scenes[1].some(e=>e.value?.includes('Image frame-4 / detection 1')));
 assert.ok(!scenes[0].some(e=>e.value?.includes('Image frame-4 / detection 1')));
});

test('rendering a different draft never mutates previously returned scene geometry',()=>{
 const B=browser(),first=B.scenes(draft(B,4)),before=JSON.stringify(first);
 B.scenes({...draft(B,1),layout:'individual',title:'Different title'});
 assert.equal(JSON.stringify(first),before);
 first[0][0].w=1;
 assert.equal(B.scenes(draft(B,4))[0][0].w,1280);
});
