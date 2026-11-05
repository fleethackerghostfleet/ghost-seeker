const briefing=(()=>{
 const selected=new Map();
 let draft=null,busy=false;
 const B=BriefingModel;
 function status(message){$('briefStatus').textContent=message}
 function renderDetections(item){
  const area=$('briefDetections');area.replaceChildren();
  const heading=document.createElement('h4');heading.textContent='Select ships for a briefing';area.append(heading);
  item.dets.forEach((d,index)=>{
   if(CLS[d.cls]?.g!=='vessel')return;
   const key=JSON.stringify([item.id,index]),button=document.createElement('button');
   button.className='btn brief-detection';button.type='button';button.setAttribute('aria-pressed',selected.has(key));
   button.textContent=`${selected.has(key)?'Remove':'Add'} detection ${index+1}: ${CLS[d.cls].lab} (${Math.round(d.conf*100)}%)`;
   button.onclick=()=>{
    if(selected.has(key))selected.delete(key);
    else {const record=B.snapshot(item,index,CLS);record.url=imageFor(item);selected.set(key,record)}
    renderDetections(item);renderSelection();
   };area.append(button);
  });
  if(!area.querySelector('button')){const p=document.createElement('p');p.className='hint';p.textContent='No vessel detections in this image.';area.append(p)}
  const link=document.createElement('a');link.className='btn';link.href='#briefing';link.textContent='Open briefing';area.append(link);
 }
 function renderSelection(){
  $('briefCount').textContent=selected.size;
  $('briefCreate').textContent=`Create briefing (${selected.size})`;
  $('briefCreate').title=selected.size?'Build a briefing from the selected ship images':'Select ships in image details first';
  $('briefCreate').disabled=busy||!selected.size;$('briefRebuild').disabled=busy||!selected.size;
  $('briefClear').disabled=busy||!selected.size;
  const area=$('briefSelection');area.replaceChildren();
  selected.forEach((r,key)=>{
   const row=document.createElement('div');row.className='brief-selected';
   const label=document.createElement('div');label.textContent=r.label;
   const sub=document.createElement('small');sub.textContent=`${r.source} / ${r.id} / detection ${r.index+1}`;label.append(sub);
   const remove=document.createElement('button');remove.className='btn sm';remove.textContent='Remove';remove.setAttribute('aria-label',`Remove ${r.label} detection ${r.index+1} from image ${r.id}`);
   remove.onclick=()=>{selected.delete(key);renderSelection();if(state.sel)renderDetail()};row.append(label,remove);area.append(row);
  });
  if(!selected.size)area.textContent='Open an image and select its vessel detections.';
 }
 function capture(record){
  return new Promise(resolve=>{
   const img=new Image();const timer=setTimeout(()=>resolve(null),8000);
   img.onload=()=>{clearTimeout(timer);try{
    const canvas=document.createElement('canvas');canvas.width=960;canvas.height=600;
    const ctx=canvas.getContext('2d');ctx.drawImage(img,0,0,960,600);
    const b=record.box;ctx.strokeStyle='#ff8c1a';ctx.lineWidth=4;ctx.strokeRect(b.x/480*960,b.y/300*600,b.w/480*960,b.h/300*600);
    resolve(canvas.toDataURL('image/jpeg',.9));
   }catch{resolve(null)}};
   img.onerror=()=>{clearTimeout(timer);resolve(null)};img.src=record.url;
  });
 }
 async function create(){
  if(busy||!selected.size)return;
  if(draft&&!window.confirm('Replace the frozen evidence with the current selection? Your title and assessment will be kept.'))return;
  busy=true;renderSelection();render();status('Freezing selected evidence and locally available images…');
  const records=Array.from(selected.values(),r=>JSON.parse(JSON.stringify(r)));
  const created=new Date().toISOString();
  await Promise.all(records.map(async r=>{r.image=await capture(r);delete r.url}));
  draft={created,records,title:$('briefTitle').value,layout:$('briefLayout').value,assessment:$('briefAssessment').value,gaps:$('briefGaps').value};
  busy=false;location.hash='#briefing';renderSelection();render();
  const missing=records.filter(r=>!r.image).length;
  status(`Frozen ${records.length} vessel observation(s) at ${created}.${missing?' '+missing+' image(s) unavailable; placeholders will be exported.':''} Review the slide before export.`);
 }
 function slideDOM(scene,doc=document){
  const slide=doc.createElement('div');slide.className='brief-slide';
  scene.forEach(e=>{
   const el=doc.createElement('div');el.className='brief-element';
   Object.assign(el.style,{left:e.x+'px',top:e.y+'px',width:e.w+'px',height:e.h+'px'});
   if(e.type==='rect')el.style.background='#'+e.color;
   else if(e.type==='image'){const img=doc.createElement('img');img.src=e.data;img.alt='Snapshot with selected vessel detection highlighted';el.append(img)}
   else {el.textContent=e.value;Object.assign(el.style,{fontSize:e.size+'px',color:'#'+e.color,fontWeight:e.bold?'700':'400'})}
   slide.append(el);
  });return slide;
 }
 function resize(){document.querySelectorAll('#briefPreview .brief-slide').forEach(el=>{el.style.transform=`scale(${el.parentElement.clientWidth/1280})`})}
 function render(){
  const area=$('briefPreview');area.replaceChildren();
  $('briefPPTX').disabled=busy||!draft;$('briefPDF').disabled=busy||!draft;
  if(!draft){const empty=document.createElement('div');empty.className='brief-empty';empty.textContent='Select vessel detections in the imagery feed, then choose Create briefing. No briefing snapshot yet.';area.append(empty);return}
  B.scenes(draft).forEach((scene,i)=>{const label=document.createElement('p');label.className='hint';label.textContent=`Slide ${i+1} / ${B.pages(draft.records,draft.layout).length} · 16:9 · Analyst draft`;const wrap=document.createElement('div');wrap.className='brief-slide-wrap';wrap.append(slideDOM(scene));area.append(label,wrap)});resize();
 }
 async function powerpoint(){
  if(!draft||busy)return;busy=true;render();
  try{
   const pptx=new PptxGenJS();pptx.layout='LAYOUT_WIDE';pptx.author='Ghost Seeker';pptx.subject=B.marking;pptx.title=draft.title;pptx.lang='en-US';
   B.scenes(draft).forEach(scene=>{const slide=pptx.addSlide();scene.forEach(e=>{
    const opts={x:e.x/96,y:e.y/96,w:e.w/96,h:e.h/96};
    if(e.type==='rect')slide.addShape(pptx.ShapeType.rect,{...opts,fill:{color:e.color},line:{color:e.color,transparency:100}});
    else if(e.type==='image'){
     const w=Math.min(opts.w,opts.h*1.6),h=w/1.6;
     slide.addImage({data:e.data,x:opts.x+(opts.w-w)/2,y:opts.y+(opts.h-h)/2,w,h});
    }
    else slide.addText(e.value,{...opts,fontFace:'Arial',fontSize:e.size*.75,color:e.color,bold:e.bold,margin:0,breakLine:false,valign:'top',fit:'shrink'});
   })});
   await pptx.writeFile({fileName:'ghost-seeker-j2-'+draft.created.replace(/[:.]/g,'-')+'.pptx',compression:true});status('PowerPoint exported. Text remains editable; imagery and source captions are embedded.');
  }catch(error){status('PowerPoint export failed: '+error.message)}finally{busy=false;renderSelection();render()}
 }
 function print(){
  if(!draft||busy)return;
  const popup=window.open('','_blank');if(!popup){status('Allow popups for this site to print / save PDF.');return}
  const doc=popup.document;doc.title='Ghost Seeker Open briefing';
  const style=doc.createElement('style');style.textContent='@page{size:13.333333in 7.5in;margin:0}body{margin:0;font-family:Arial,sans-serif}.brief-slide{position:relative;width:1280px;height:720px;background:white;break-after:page;page-break-after:always}.brief-slide:last-child{break-after:auto}.brief-element{position:absolute;white-space:pre-wrap;overflow-wrap:anywhere;overflow:hidden;line-height:1.15}.brief-element img{width:100%;height:100%;object-fit:contain}*{box-sizing:border-box;print-color-adjust:exact;-webkit-print-color-adjust:exact}@media print{.print-actions{display:none}}';doc.head.append(style);
  const actions=doc.createElement('div');actions.className='print-actions';const button=doc.createElement('button');button.textContent='Print / Save as PDF';button.onclick=()=>popup.print();actions.append(button);doc.body.append(actions);
  B.scenes(draft).forEach(scene=>doc.body.append(slideDOM(scene,doc)));
  status('Print preview opened. Choose Save as PDF in the browser print dialog (landscape, no margins).');
 }
 function init(){
  $('briefCreate').onclick=create;$('briefRebuild').onclick=create;$('briefPPTX').onclick=powerpoint;$('briefPDF').onclick=print;
  $('briefClear').onclick=()=>{selected.clear();renderSelection();if(state.sel)renderDetail()};
  for(const [id,key] of [['briefTitle','title'],['briefLayout','layout'],['briefAssessment','assessment'],['briefGaps','gaps']])$(id).addEventListener('input',e=>{if(draft){draft[key]=e.target.value;render()}});
  window.addEventListener('resize',resize);renderSelection();render();
 }
 return {init,render,renderDetections};
})();
