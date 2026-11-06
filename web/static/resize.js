'use strict';
(()=>{
 const panels=[['controls','.fhead','Feed controls',240,480],['drawer','.drawer','Image details',340,780],['review','.rvside','Review details',260,600],['priorities','.wb-layout > aside','Collection priorities',260,540],['briefing','.brief-controls','Briefing controls',240,480],['commanderQueue','.queue','Commander queue',240,480],['commanderBrief','.brief','Commander details',280,540]];
 for(const [key,selector,label,min,max] of panels){
  const handle=document.createElement('div');
  handle.className='sidebar-resizer';handle.tabIndex=0;
  handle.setAttribute('role','separator');handle.setAttribute('aria-orientation','vertical');
  handle.setAttribute('aria-label',label+' width');
  handle.setAttribute('aria-valuemin',min);handle.setAttribute('aria-valuemax',max);
  handle.title='Drag or use arrow keys to resize. Double-click or Home to reset.';
  (document.getElementById?.('panelResizers')||document.body).append(handle);
  let panel,right=false,drag=null,observed=null,scheduled=false;
  const schedule=()=>{if(scheduled)return;scheduled=true;requestAnimationFrame(()=>{scheduled=false;update()})};
  const sizes=new ResizeObserver(schedule);
  const update=()=>{
   panel=document.querySelector(selector);
   if(panel!==observed){sizes.disconnect();if(panel)sizes.observe(panel);observed=panel}
   const side=window.gsSettings.get().bar;
   const visible=panel&&panel.getClientRects().length&&innerWidth>(key.startsWith('commander')?1100:900)&&(key!=='controls'||side==='left'||side==='right')&&(key!=='drawer'||document.body.classList.contains('drawer-open'));
   handle.hidden=!visible;if(!visible)return;
   right=key==='drawer'||key==='review'||key==='commanderBrief'||(key==='controls'&&side==='right');
   const r=panel.getBoundingClientRect();
   handle.style.left=((right?r.left:r.right)-5)+'px';
   handle.style.top=Math.max(0,r.top)+'px';handle.style.height=Math.max(0,Math.min(innerHeight,r.bottom)-Math.max(0,r.top))+'px';
   handle.setAttribute('aria-valuenow',Math.round(r.width));
  };
  handle.addEventListener('pointerdown',e=>{
   if(e.button!==0)return;
   e.preventDefault();handle.focus();drag={x:e.clientX,width:panel.getBoundingClientRect().width};
   handle.setPointerCapture(e.pointerId);
  });
  handle.addEventListener('pointermove',e=>{
   if(!drag)return;
   window.gsSettings.resize(key,drag.width+(e.clientX-drag.x)*(right?-1:1));update();
  });
  handle.addEventListener('lostpointercapture',()=>{drag=null});
  handle.addEventListener('pointerup',e=>{drag=null;handle.releasePointerCapture(e.pointerId)});
  handle.addEventListener('dblclick',()=>{window.gsSettings.resize(key,null);update()});
  handle.addEventListener('keydown',e=>{
   if(!['ArrowLeft','ArrowRight','Home'].includes(e.key))return;
   e.preventDefault();e.stopPropagation();
   window.gsSettings.resize(key,e.key==='Home'?null:panel.getBoundingClientRect().width+(e.key==='ArrowRight'?1:-1)*(right?-1:1)*(e.shiftKey?40:10));update();
  });
  new MutationObserver(schedule).observe(document.body,{subtree:true,childList:true,attributes:true,attributeFilter:['class']});
  new ResizeObserver(schedule).observe(document.body);
  window.addEventListener('resize',schedule);document.addEventListener('scroll',schedule,true);
  document.addEventListener('transitionend',schedule,true);
  update();
 }
})();
