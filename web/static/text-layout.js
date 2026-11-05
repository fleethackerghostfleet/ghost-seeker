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
  const frames=Array.from(document.querySelectorAll('.dimg,.rvframe,.frame,.zframe'));
  for(const frame of observed)if(!frames.includes(frame)){resize.unobserve(frame);observed.delete(frame)}
  frames.forEach(frame=>{
   if(!observed.has(frame)){observed.add(frame);resize.observe(frame)}
   const bounds=frame.getBoundingClientRect();if(!bounds.width||!bounds.height)return;
   const placed=Array.from(frame.querySelectorAll('.band-controls,.band-label,.zhint'),element=>{
    const r=element.getBoundingClientRect();return {x:r.left-bounds.left,y:r.top-bounds.top,w:r.width,h:r.height};
   });
   frame.querySelectorAll('.box em').forEach(label=>{
    const box=label.parentElement.getBoundingClientRect();
    label.style.maxWidth=Math.max(1,frame.clientWidth-8)+'px';
    label.style.width='max-content';
    const w=label.offsetWidth,h=label.offsetHeight,maxX=Math.max(4,frame.clientWidth-w-4),maxY=Math.max(4,frame.clientHeight-h-4);
    const x=Math.max(4,Math.min(maxX,box.left-bounds.left)),y=Math.max(4,Math.min(maxY,box.top-bounds.top-h));
    const candidates=[{x,y}];
    for(let top=4;top<=maxY;top+=h+4)for(const left of [x,4,maxX])candidates.push({x:left,y:top});
    const target=candidates.find(a=>placed.every(b=>a.x+w+3<=b.x||a.x>=b.x+b.w+3||a.y+h+3<=b.y||a.y>=b.y+b.h+3))||{x,y};
    placed.push({...target,w,h});
    label.style.left=(target.x-(box.left-bounds.left)-frame.clientLeft)+'px';
    label.style.top=(target.y-(box.top-bounds.top)-frame.clientTop)+'px';
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
