const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const M=require('./static/oprep-model.js');
const now=new Date('2026-11-06T13:14:00Z');
const classes={fish:{lab:'Fish'},mm:{lab:'Marine mammal'},orca:{lab:'Orca'}};
const item={id:'image-1',ts:now.getTime(),src:{id:'EO1',name:'Bow camera',loc:'10N 20W'},dets:[{cls:'fish',conf:.99},{cls:'mm',conf:.7},{cls:'orca',conf:.91}]};
test('UTC DTG has zero padding, month and year and rejects invalid dates',()=>{
 assert.equal(M.dtg(now),'061314ZNOV26');assert.equal(M.dtg('2026-01-02T03:04:00Z'),'020304ZJAN26');
 for(const value of ['',null,undefined,'invalid'])assert.equal(M.dtg(value),'UNK');
});
test('defaults select highest-confidence mammal and capture evidence and position',()=>{
 const f=M.defaults(item,classes,[],now);
 assert.equal(f.incidentTime,now.toISOString());assert.equal(f.species,'UNIDENTIFIED WHALE');assert.equal(f.position,'10N 20W');
 assert.match(f.evidence,/image-1.*EO1.*Bow camera.*2026-11-06T13:14:00.000Z.*Orca 91%/);
 assert.equal(M.defaults({...item,src:'EO1'},classes,[item.src],now).position,'10N 20W');
});
test('defaults handle absent detections, source and empty image',()=>{
 const f=M.defaults({...item,src:null,dets:[]},classes,[],now);
 assert.equal(f.species,'UNIDENTIFIED MARINE MAMMAL');assert.equal(f.position,'POSITION UNKNOWN - ENTER LAT/LONG');
 assert.match(f.evidence,/no marine mammal detection in selected image/);
 assert.equal(M.defaults({},classes,[],now).incidentTime,now.toISOString());
 assert.equal(M.defaults({...item,ts:'invalid'},classes).incidentTime,'UNK');
});
test('message sanitizes field separators, newlines and empty values',()=>{
 const output=M.message({originator:'uss / test\nship',to:'\n / ',actions:'one\r\ntwo/three'},now);
 assert.match(output,/FM USS - TEST SHIP\nTO -\nINFO UNK/);assert.match(output,/GENTEXT\/ACTIONS TAKEN\/ONE TWO-THREE\/\//);
 assert.equal(output,output.toUpperCase());assert.doesNotMatch(output,/undefined|null|\r|\/\/\//i);
 const empty=M.message({},now);assert.match(empty,/TIMELOC\/UNK\/UNK\/\//);assert.match(empty,/MSGID\/OPREP-3\/UNK\/UNK\/\//);
 assert(empty.split('\n').every(line=>line.trim()));
});
test('message maintains required line order, flagword and evidence caution',()=>{
 const lines=M.message(M.defaults(item,classes),now).split('\n');
 assert.equal(lines[0],'UNCLASSIFIED//FOR TRAINING AND DEMONSTRATION ONLY');
 assert.match(lines[1],/^DRAFT - NOT RELEASED/);assert.equal(lines[2],'IMMEDIATE');assert.equal(lines[3],'061314ZNOV26');
 const starts=['FM ','TO ','INFO ','BT','UNCLAS','MSGID/','FLAGWORD/NAVY BLUE//','SUBJ/','TIMELOC/','GENTEXT/INCIDENT IDENTIFICATION AND DETAILS/','GENTEXT/SHIP STATUS/','GENTEXT/ACTIONS TAKEN/','GENTEXT/PUBLIC AFFAIRS/','GENTEXT/COMMANDERS ASSESSMENT/','GENTEXT/REMARKS/','BT'];
 starts.forEach((prefix,i)=>assert(lines[i+4].startsWith(prefix),prefix));
 assert.match(lines.at(-2),/MACHINE DETECTION IS NOT CONFIRMATION OF STRIKE/);
});
test('watch floor loads local scripts and dialog controls have labels',()=>{
 const html=fs.readFileSync(path.join(__dirname,'static/index.html'),'utf8');
 for(const file of ['oprep-model.js','oprep.js'])assert(html.includes(`<script src="/${file}" defer></script>`));
 assert.match(html,/<dialog id="oprepDialog" aria-labelledby="oprepTitle" aria-describedby="oprepHint">/);
 const form=html.slice(html.indexOf('<form id="oprepForm">'),html.indexOf('</form>',html.indexOf('<form id="oprepForm">')));
 const controls=[...form.matchAll(/<(?:input|textarea|select) id="([^"]+)" name="([^"]+)"/g)];
 assert.equal(controls.length,Object.keys(M.defaults()).length);
 for(const [,id] of controls)assert(form.includes(`<label for="${id}">`));
 assert.match(html,/id="oprepPreview" tabindex="0" aria-label="Read-only OPREP message preview"/);
});
