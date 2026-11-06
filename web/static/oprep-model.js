(function(root){
 'use strict';
 const clean=value=>String(value??'').replace(/\//g,'-').replace(/\s+/g,' ').trim().toUpperCase();
 function dtg(date){
  if(date===null||date===undefined||date==='')return '';
  const d=new Date(date);if(!Number.isFinite(d.getTime()))return '';
  const pad=n=>String(n).padStart(2,'0');
  return pad(d.getUTCDate())+pad(d.getUTCHours())+pad(d.getUTCMinutes())+'Z'+['JAN','FEB','MAR','APR','MAY','JUN','JUL','AUG','SEP','OCT','NOV','DEC'][d.getUTCMonth()]+pad(d.getUTCFullYear()%100);
 }
 function latLong(lat,lon){
  if(!Number.isFinite(lat)||!Number.isFinite(lon)||Math.abs(lat)>90||Math.abs(lon)>180)return '';
  const coordinate=(value,width,positive,negative)=>{
   const hundredths=Math.round(Math.abs(value)*6000),degrees=Math.floor(hundredths/6000),minutes=(hundredths%6000/100).toFixed(2).padStart(5,'0');
   return String(degrees).padStart(width,'0')+'-'+minutes+(value<0?negative:positive);
  };
  return coordinate(lat,2,'N','S')+' '+coordinate(lon,3,'E','W');
 }
 function defaults(item={},classes={},sources=[],now=new Date(),metadata={}){
  const source=(typeof item.src==='object'?item.src:null)||(Array.isArray(sources)?sources.find(s=>s.id===item.src):sources[item.src])||{id:item.src};
  const mammal=(item.dets||[]).filter(d=>/whale|mammal|dolphin|orca|seal/i.test(String(d.cls)+' '+(classes[d.cls]?.lab||classes[d.cls]?.label||''))).sort((a,b)=>b.conf-a.conf)[0];
  const embedded=item.capture||{},c={...embedded,...metadata.capture},m={...embedded,...metadata.model};
  const capture=c.time??item.ts,time=capture!==undefined&&capture!==null&&capture!==''&&Number.isFinite(new Date(capture).getTime())?new Date(capture).toISOString():'';
  const detection=mammal?`${classes[mammal.cls]?.lab||classes[mammal.cls]?.label||mammal.cls} ${Number.isFinite(mammal.conf)?Math.round(mammal.conf*100)+'%':''}`:'no marine mammal detection in selected image';
  const subtype=/whale|mammal|dolphin|orca|seal/i.test(m.subtype||'')?m.subtype:null;
  const evidence=[`ghost-seeker image ${item.id??''}, source ${c.source||source.id||''} (${c.sensor?.name||source.name||''}; hardware ${c.sensor?.hardware||source.hw||''}), frame ${c.frame??item.frame??''}, record ${c.record??item.dbId??''}, capture time ${time}, ${detection}`];
  if(c.contentHash)evidence.push('content hash '+String(c.contentHash).slice(0,24));
  if(subtype)evidence.push(`model subtype ${subtype} (${Number.isFinite(m.subtypeConf)?Math.round(m.subtypeConf*100)+'%':''})`);
  if(m.trackId)evidence.push(`sighting track ${m.trackId}, ${m.trackFrames??''} frames`);
  if(c.synthetic?.length)evidence.push(`CAPTURE METADATA IS SYNTHETIC (fields: ${c.synthetic.join(', ')})`);
  const label=mammal?(classes[mammal.cls]?.lab||classes[mammal.cls]?.label||mammal.cls):'';
  const generic=/^(marine mammal|mammal)$/i.test(label);
  const corresponds=subtype&&label&&subtype.toLowerCase().includes(label.toLowerCase());
  const species=mammal?(corresponds?subtype:generic?'':label):subtype||'';
  return {originator:'',to:'ISIC / OPERATIONAL COMMANDER',info:'CNO WASHINGTON DC',serial:'001',incidentTime:time,position:Number.isFinite(c.lat)&&Number.isFinite(c.lon)?latLong(c.lat,c.lon):source.loc||'',sensorHeading:Number.isFinite(c.headingDeg)?c.headingDeg+' DEG TRUE':'',course:'',speed:'',species,size:'',condition:'',damage:'',injuries:'',actions:'',media:'',assessment:'',evidence:evidence.join('; ')};
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
   `GENTEXT/REMARKS/SENSOR EVIDENCE: ${f.evidence}. SENSOR HEADING AT CAPTURE: ${f.sensorHeading} (NOT SHIP COURSE). MACHINE DETECTION IS NOT CONFIRMATION OF STRIKE.//`,'BT'
  ].join('\n');
 }
 const api={defaults,dtg,message,latLong};
 if(typeof module!=='undefined'&&module.exports)module.exports=api;
 else root.OprepModel=api;
})(typeof globalThis!=='undefined'?globalThis:this);
