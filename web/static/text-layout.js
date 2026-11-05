'use strict';
(()=>{
 let pending=false;
 const observed=new Set();
 const resize=new ResizeObserver(schedule);
 function schedule(){if(!pending){pending=true;requestAnimationFrame(layout)}}
 function layout(){
  pending=false;
  const banners=Array.from(document.querySelectorAll('body>.banner'));
  document.body.style.setProperty('--banner-space',Math.max(0,...banners.map(b=>b.getBoundingClientRect().height))+12+'px');
  const frames=Array.from(document.querySelectorAll('.dimg,.rvframe,.frame,.zframe,.grid.l .thumb'));
  for(const frame of observed)if(!frames.includes(frame)){resize.unobserve(frame);observed.delete(frame)}
  frames.forEach(frame=>{
   if(!observed.has(frame)){observed.add(frame);resize.observe(frame)}
   const bounds=frame.getBoundingClientRect();if(!bounds.width||!bounds.height)return;
   const origin={x:bounds.left+frame.clientLeft,y:bounds.top+frame.clientTop};
   const obstacles=Array.from(frame.querySelectorAll('.band-controls,.band-label,.zhint,.tier,.cdrtag,.hsenttag,.newtag,.night-badge,.band-badge,.suptag,.formtag,.ov,.sendmark'),element=>{
    const r=element.getBoundingClientRect();return {x:r.left-origin.x,y:r.top-origin.y,w:r.width,h:r.height};
   }).filter(r=>r.w&&r.h);
   const labels=Array.from(frame.querySelectorAll('.box em'));
   labels.forEach(label=>{label.style.fontSize='';label.style.display=''});
   const fonts=labels.map(label=>parseFloat(getComputedStyle(label).fontSize));
   let positions;
   for(const scale of [1,.95,.9,.85,.8,.75]){
    for(const width of [1,.75,.5]){
     labels.forEach((label,i)=>{
      label.style.fontSize=Math.min(fonts[i],Math.max(12,fonts[i]*scale))+'px';
      label.style.maxWidth=Math.max(1,(frame.clientWidth-8)*width)+'px';
      label.style.width='max-content';
     });
     const placed=[...obstacles],attempt=new Map();
     const measured=labels.map(label=>({label,w:label.offsetWidth,h:label.offsetHeight})).sort((a,b)=>b.w*b.h-a.w*a.h);
     for(const {label,w,h}of measured){
      const box=label.parentElement.getBoundingClientRect(),maxX=frame.clientWidth-w-4,maxY=frame.clientHeight-h-4;
      if(maxX<4||maxY<4)break;
      const x=Math.max(4,Math.min(maxX,box.left-origin.x)),y=Math.max(4,Math.min(maxY,box.top-origin.y-h));
      const xs=new Set([x,4,maxX]),ys=new Set([y,4,maxY]);
      placed.forEach(r=>{xs.add(r.x+r.w+3);xs.add(r.x-w-3);ys.add(r.y+r.h+3);ys.add(r.y-h-3)});
      const candidates=[];
      for(const left of xs)for(const top of ys)if(left>=4&&left<=maxX&&top>=4&&top<=maxY)candidates.push({x:left,y:top});
      candidates.sort((a,b)=>Math.abs(a.x-x)+Math.abs(a.y-y)-Math.abs(b.x-x)-Math.abs(b.y-y));
      const target=candidates.find(a=>placed.every(b=>a.x+w+3<=b.x||a.x>=b.x+b.w+3||a.y+h+3<=b.y||a.y>=b.y+b.h+3));
      if(!target)break;
      placed.push({...target,w,h});attempt.set(label,target);
     }
     if(attempt.size===labels.length){positions=attempt;break}
    }
    if(positions)break;
   }
   let legend=frame.querySelector('.detection-labels');
   if(!positions){
    if(!legend){legend=document.createElement('div');legend.className='detection-labels';legend.tabIndex=0;legend.setAttribute('role','region');legend.setAttribute('aria-label','Detection labels');frame.append(legend)}
    const signature=labels.map(label=>label.textContent).join('\n');
    if(legend.dataset.labels!==signature){
     legend.replaceChildren(...labels.map(label=>{const copy=document.createElement('em'),style=getComputedStyle(label);copy.textContent=label.textContent;copy.style.backgroundColor=style.backgroundColor;copy.style.color=style.color;return copy}));
     legend.dataset.labels=signature;
    }
    legend.style.top=Math.max(4,Math.min(frame.clientHeight-28,Math.max(4,...obstacles.filter(r=>r.y<frame.clientHeight/2).map(r=>r.y+r.h+3))))+'px';
    labels.forEach(label=>label.style.display='none');return;
   }
   legend?.remove();
   positions.forEach((target,label)=>{
    const box=label.parentElement.getBoundingClientRect();
    label.style.left=(origin.x+target.x-box.left-(label.parentElement.clientLeft||0))+'px';
    label.style.top=(origin.y+target.y-box.top-(label.parentElement.clientTop||0))+'px';
    label.style.bottom='auto';label.style.right='auto';
   });
  });
 }
 new MutationObserver(schedule).observe(document.body,{childList:true,subtree:true});
 window.addEventListener('resize',schedule);
 document.fonts?.ready.then(schedule);
 window.gsTextLayout=schedule;
 schedule();
})();
