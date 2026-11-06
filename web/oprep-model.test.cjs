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
test('latLong formats hemispheres, zero, rounding carry and invalid positions',()=>{
 assert.equal(M.latLong(32+41.2/60,-117-14.8/60),'32-41.20N 117-14.80W');
 assert.equal(M.latLong(-32.5,117.5),'32-30.00S 117-30.00E');
 assert.equal(M.latLong(0,0),'00-00.00N 000-00.00E');
 assert.equal(M.latLong(-12.999999,179.999999),'13-00.00S 180-00.00E');
 assert.equal(M.latLong(90,-180),'90-00.00N 180-00.00W');
 for(const [lat,lon] of [[91,0],[0,-181],[null,0],[0,NaN],['32',0]])assert.match(M.latLong(lat,lon),/POSITION UNKNOWN/);
});
test('metadata record takes precedence and carries provenance evidence without claiming ship course',()=>{
 const metadata={capture:{time:'2026-01-02T03:04:00Z',lat:32.5,lon:-117.5,headingDeg:270,source:'META',sensor:{name:'Metadata camera',hardware:'EO turret'},frame:7,record:42,contentHash:'abcdefghijklmnopqrstuvwxyz012345',synthetic:['capture.lat','capture.lon']},model:{subtype:'Blue whale',subtypeConf:.83,trackId:'sighting-3',trackFrames:12}};
 const f=M.defaults({...item,capture:{lat:1,lon:2,subtype:'Dolphin'}},classes,[],now,metadata);
 assert.equal(f.position,'32-30.00N 117-30.00W');assert.equal(f.incidentTime,'2026-01-02T03:04:00.000Z');
 assert.equal(f.species,'Blue whale');assert.equal(f.sensorHeading,'270 DEG TRUE');assert.match(f.course,/UNKNOWN/);
 assert.match(f.evidence,/META.*Metadata camera.*EO turret.*frame 7, record 42/);
 assert.match(f.evidence,/content hash abcdefghijklmnopqrstuvwx;/);assert.doesNotMatch(f.evidence,/yz012345/);
 assert.match(f.evidence,/Blue whale \(83%\).*sighting track sighting-3, 12 frames/);
 assert.match(f.evidence,/CAPTURE METADATA IS SYNTHETIC \(fields: capture.lat, capture.lon\)/);
 const message=M.message(f,now);assert.match(message,/TIMELOC\/020304ZJAN26\/32-30.00N 117-30.00W/);
 assert.match(message,/SENSOR HEADING AT CAPTURE: 270 DEG TRUE \(NOT SHIP COURSE\)/);
});
test('embedded item.capture fallback supports zeros and does not invent metadata',()=>{
 const f=M.defaults({...item,capture:{lat:0,lon:0,headingDeg:0,subtype:'Orca',subtypeConf:.9,trackId:'t1',trackFrames:2,synthetic:['headingDeg']}},classes,[],now);
 assert.equal(f.position,'00-00.00N 000-00.00E');assert.equal(f.sensorHeading,'0 DEG TRUE');assert.equal(f.species,'Orca');
 assert.match(f.evidence,/sighting track t1, 2 frames/);assert.match(f.evidence,/CAPTURE METADATA IS SYNTHETIC/);
 const absent=M.defaults(item,classes,[],now);assert.equal(absent.sensorHeading,'UNKNOWN');assert.doesNotMatch(absent.evidence,/SYNTHETIC|sighting track|content hash/);
 assert.equal(M.defaults({...item,capture:{subtype:'Cargo ship'}},classes).species,'UNIDENTIFIED WHALE');
});
test('generic mammal or dolphin detections are not called whales',()=>{
 assert.equal(M.defaults({...item,dets:[{cls:'mm',conf:.7}]},classes,[],now).species,'UNIDENTIFIED MARINE MAMMAL');
 assert.equal(M.defaults({...item,dets:[{cls:'dolphin',conf:.8}]},{dolphin:{lab:'Dolphin'}},[],now).species,'UNIDENTIFIED MARINE MAMMAL');
});
