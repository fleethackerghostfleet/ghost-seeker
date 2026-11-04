function createLearner(){
 const records=[],cooldowns=new Map();
 const highRisk=new Set(['warship','military aircraft','submarine','person']);
 const cooldownKey=(cls,dir)=>`${cls}\u0000${dir}`;
 function noteArrival(cls){
  for(const[key,cooldown]of cooldowns){
   if(cooldown.cls!==cls)continue;
   cooldown.remaining--;
   if(cooldown.remaining<=0)cooldowns.delete(key);
  }
 }
 function record(item){
  if(!item?.dets?.length)return null;
  const contacts=item.dets.map((d,i)=>({cls:d.cls,s:Number(item.det?.[i]?.s)||0})).sort((a,b)=>b.s-a.s);
  const top=contacts[0];if(!top)return null;
  const dir=Math.sign(item.tier-item.sug);
  let ambiguous=null;
  if(dir!==0){
   const threshold=top.s*.85,classes=[];
   for(const contact of contacts){
    if(contact.s<threshold||classes.includes(contact.cls))continue;
    classes.push(contact.cls);
   }
   if(classes.length>1)ambiguous=classes.slice(0,3);
  }
  const index=records.findIndex(r=>r.id===item.id),previous=index<0?null:records.splice(index,1)[0];
  const cls=ambiguous?null:top.cls,entry={id:item.id,cls,src:item.src,dir};
  records.push(entry);
  if(cls&&previous?.cls!==cls)noteArrival(cls);
  if(records.length>200)records.shift();
  return{ambiguous};
 }
 function attribute(id,choice){
  const entry=records.find(r=>r.id===id);if(!entry)return false;
  if(choice==='wrong'){
   entry.cls=null;entry.wrong=true;return true;
  }
  if(choice===null){
   entry.cls=null;delete entry.wrong;return true;
  }
  if(entry.cls!==choice)noteArrival(choice);
  entry.cls=choice;delete entry.wrong;return true;
 }
 function forget(id){
  const index=records.findIndex(r=>r.id===id);
  if(index<0)return false;
  records.splice(index,1);return true;
 }
 function suggestion(){
  const classes=[...new Set(records.filter(r=>r.cls&&!r.wrong).map(r=>r.cls))];
  for(const cls of classes){
   const window=records.filter(r=>r.cls===cls&&!r.wrong).slice(-8);
   for(const[dir,predicate]of [['lower',r=>r.dir>0],['raise',r=>r.dir<0]]){
    const lowered=window.filter(predicate);
    if(lowered.length<5||lowered.length/window.length<.7||new Set(lowered.map(r=>r.src)).size<2||cooldowns.has(cooldownKey(cls,dir)))continue;
    return{cls,dir,n:lowered.length,of:window.length,highRisk:highRisk.has(cls)};
   }
  }
  return null;
 }
 function resolve(s,answer){
  if(!s?.cls||!['lower','raise'].includes(s.dir))return;
  if(answer==='yes'){
   for(let i=records.length-1;i>=0;i--)if(records[i].cls===s.cls)records.splice(i,1);
   for(const[key,cooldown]of cooldowns)if(cooldown.cls===s.cls)cooldowns.delete(key);
  }else if(answer==='no'||answer==='later'){
   cooldowns.set(cooldownKey(s.cls,s.dir),{cls:s.cls,remaining:answer==='no'?8:3});
  }
 }
 function wrongCount(){return records.filter(r=>r.wrong).length}
 return{record,attribute,forget,suggestion,resolve,wrongCount};
}

if(typeof window!=='undefined')window.gsLearn=createLearner();
