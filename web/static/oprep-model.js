(function(root){
 'use strict';
 const clean=value=>String(value??'').replace(/\//g,'-').replace(/\s+/g,' ').trim().toUpperCase()||'UNK';
 function dtg(date){
  if(date===null||date===undefined||date==='')return 'UNK';
  const d=new Date(date);if(!Number.isFinite(d.getTime()))return 'UNK';
  const pad=n=>String(n).padStart(2,'0');
  return pad(d.getUTCDate())+pad(d.getUTCHours())+pad(d.getUTCMinutes())+'Z'+['JAN','FEB','MAR','APR','MAY','JUN','JUL','AUG','SEP','OCT','NOV','DEC'][d.getUTCMonth()]+pad(d.getUTCFullYear()%100);
 }
 function defaults(item={},classes={},sources=[],now=new Date()){
  const source=(typeof item.src==='object'?item.src:null)||(Array.isArray(sources)?sources.find(s=>s.id===item.src):sources[item.src])||{id:item.src};
  const mammal=(item.dets||[]).filter(d=>/whale|mammal|dolphin|orca/i.test(String(d.cls)+' '+(classes[d.cls]?.lab||classes[d.cls]?.label||''))).sort((a,b)=>b.conf-a.conf)[0];
  const capture=new Date(item.ts??now),time=Number.isFinite(capture.getTime())?capture.toISOString():'UNK';
  const detection=mammal?`${classes[mammal.cls]?.lab||classes[mammal.cls]?.label||mammal.cls} ${Number.isFinite(mammal.conf)?Math.round(mammal.conf*100)+'%':'UNK CONFIDENCE'}`:'no marine mammal detection in selected image';
  return {originator:'USS UNKNOWN',to:'ISIC / OPERATIONAL COMMANDER',info:'CNO WASHINGTON DC; ENTER FLEET COMMANDER',serial:'001',incidentTime:time,position:source.loc||'POSITION UNKNOWN - ENTER LAT/LONG',course:'UNKNOWN - ENTER COURSE',speed:'UNKNOWN - ENTER SPEED',species:mammal?'UNIDENTIFIED WHALE':'UNIDENTIFIED MARINE MAMMAL',size:'UNKNOWN - ENTER SIZE ESTIMATE',condition:'UNKNOWN',damage:'UNKNOWN - ASSESS SHIP DAMAGE',injuries:'UNKNOWN - ASSESS PERSONNEL INJURIES',actions:'MANEUVERED/STOPPED - VERIFY; LOOKOUTS POSTED - VERIFY; NOTIFICATION OF FLEET ENVIRONMENTAL OFFICE AND NMFS REGIONAL STRANDING NETWORK PENDING',media:'UNKNOWN - ENTER MEDIA/PUBLIC INTEREST',assessment:'PENDING COMMANDER REVIEW',evidence:`ghost-seeker image ${item.id??'UNK'}, source ${source.id||'UNK'} (${source.name||'UNK'}), capture time ${time}, ${detection}`};
 }
 function message(fields={},now=new Date()){
  const f=new Proxy(fields,{get:(target,key)=>clean(target[key])}),incident=dtg(fields.incidentTime);
  return [
   'UNCLASSIFIED//FOR TRAINING AND DEMONSTRATION ONLY',
   'DRAFT - NOT RELEASED - VERIFY FORMAT, ADDRESSEES AND TIMELINES AGAINST OPNAVINST 3100.6K BEFORE TRANSMISSION',
   'IMMEDIATE',dtg(now),`FM ${f.originator}`,`TO ${f.to}`,`INFO ${f.info}`,'BT','UNCLAS',
   `MSGID/OPREP-3/${f.originator}/${f.serial}//`,'FLAGWORD/NAVY BLUE//',
   `SUBJ/OPREP-3 NAVY BLUE/MARINE MAMMAL STRIKE BY ${f.originator}//`,
   `TIMELOC/${incident}/${f.position}//`,
   `GENTEXT/INCIDENT IDENTIFICATION AND DETAILS/1. WHO/WHAT ${f.originator} REPORTS MARINE MAMMAL STRIKE. 2. WHEN ${incident}. 3. WHERE ${f.position}, COURSE ${f.course}, SPEED ${f.speed}. 4. SPECIES / ESTIMATED SIZE ${f.species}, ${f.size}. 5. ANIMAL CONDITION AFTER STRIKE ${f.condition}//`,
   `GENTEXT/SHIP STATUS/DAMAGE: ${f.damage}; PERSONNEL INJURIES: ${f.injuries}//`,
   `GENTEXT/ACTIONS TAKEN/${f.actions}//`,
   `GENTEXT/PUBLIC AFFAIRS/${f.media}//`,
   `GENTEXT/COMMANDERS ASSESSMENT/${f.assessment}//`,
   `GENTEXT/REMARKS/SENSOR EVIDENCE: ${f.evidence}. MACHINE DETECTION IS NOT CONFIRMATION OF STRIKE.//`,'BT'
  ].join('\n');
 }
 const api={defaults,dtg,message};
 if(typeof module!=='undefined'&&module.exports)module.exports=api;
 else root.OprepModel=api;
})(typeof globalThis!=='undefined'?globalThis:this);
