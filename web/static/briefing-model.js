/* Shared slide geometry for browser preview, print and editable PowerPoint. */
(function(root){
 'use strict';
 const marking='UNCLASSIFIED // FOR TRAINING AND DEMONSTRATION ONLY';
 function snapshot(item,index,classes){
  const d=item.dets[index];
  if(!d||classes[d.cls]?.g!=='vessel')throw new Error('Select a vessel detection.');
  return {key:JSON.stringify([item.id,index]),id:item.id,index,label:classes[d.cls].lab,
   source:item.src.id,time:new Date(item.ts).toISOString(),priority:item.tier,
   confidence:d.conf,box:JSON.parse(JSON.stringify(d.b)),
   observations:Object.entries(d.attrs||{}).filter(([,v])=>v).map(([k])=>k),image:null};
 }
 function pages(records,layout){
  const size=layout==='individual'?1:4,out=[];
  for(let i=0;i<records.length;i+=size)out.push(records.slice(i,i+size));
  return out;
 }
 function scenes(draft){
  return pages(draft.records,draft.layout).map((records,page)=>{
   const elements=[];
   const text=(value,x,y,w,h,size=16,color='172E40',bold=false)=>{
    value=String(value);
    if(value.split('\n').reduce((n,line)=>n+Math.max(1,Math.ceil(line.length/(w/9))),0)*9*1.15>h)value=value.replace(/\s+/g,' ').trim();
    // Conservative character width keeps long analyst text inside the shared geometry.
    while(size>9&&value.split('\n').reduce((n,line)=>n+Math.max(1,Math.ceil(line.length/(w/size))),0)*size*1.15>h)size--;
    elements.push({type:'text',value,x,y,w,h,size,color,bold});
   };
   const rect=(x,y,w,h,color)=>elements.push({type:'rect',x,y,w,h,color});
   rect(0,0,1280,32,'007A33');text(marking,30,7,1210,22,14,'FFFFFF',true);
   text(draft.title||'Maritime intelligence briefing',32,48,1216,72,30,'172E40',true);
   text(`J2 / MARITIME  |  SNAPSHOT ${draft.created}  |  ANALYST DRAFT`,32,124,1216,28,13,'526672');
   const gap=20,width=(1216-gap*(records.length-1))/records.length;
   records.forEach((r,i)=>{
    const x=32+i*(width+gap),imageH=records.length===1?260:190;
    const imageW=records.length===1?640:width;
    rect(x,166,imageW,imageH,'E9EEF0');
    if(r.image)elements.push({type:'image',data:r.image,x,y:166,w:imageW,h:imageH});
    else text('Image unavailable at snapshot',x+12,220,imageW-24,50,16,'526672');
    const tx=records.length===1?710:x,ty=records.length===1?170:368,tw=records.length===1?538:width;
    text(`${i+1}. ${r.label} / P${r.priority}`,tx,ty,tw,50,records.length>2?18:22,'172E40',true);
    text(`${r.source} | ${r.time}\nImage ${r.id} / detection ${r.index+1}`,tx,ty+56,tw,57,12,'526672');
    text(`Model confidence: ${Math.round(r.confidence*100)}%\nIdentity / affiliation: unknown`,tx,ty+117,tw,50,14);
    if(records.length===1)text('Position / course / speed: unavailable\nBox highlights the selected detection.\nModel attributes: '+(r.observations.join(', ')||'none'),tx,ty+174,tw,80,14,'526672');
   });
   rect(32,550,1216,1,'CCD7DB');
   text('ANALYST ASSESSMENT',32,565,770,24,13,'006C72',true);
   text(draft.assessment||'Not entered. Analyst review required.',32,595,770,83,16);
   text('INTELLIGENCE GAPS',840,565,408,24,13,'9D5200',true);
   text(draft.gaps,840,595,408,83,14,'526672');
   text(`SYNTHETIC / DEMO DATA | Priority is not threat or intent | Observations may show the same ship`,32,695,1150,18,10,'526672');
   text(String(page+1),1210,695,38,18,10,'526672');
   return elements;
  });
 }
 const api={marking,snapshot,pages,scenes};
 if(typeof module!=='undefined'&&module.exports)module.exports=api;
 else root.BriefingModel=api;
})(typeof globalThis!=='undefined'?globalThis:this);
