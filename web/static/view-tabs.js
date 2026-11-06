'use strict';
(()=>{
 for(const root of document.querySelectorAll('[data-view-tabs]')){
  const strip=root.querySelector('nav');
  const left=root.querySelector('[data-scroll="left"]');
  const right=root.querySelector('[data-scroll="right"]');
  let active=null;
  const update=()=>{
   const overflow=strip.scrollWidth>root.clientWidth+1;
   left.hidden=right.hidden=!overflow;
   left.disabled=strip.scrollLeft<=1;
   right.disabled=strip.scrollLeft>=strip.scrollWidth-strip.clientWidth-1;
  };
  const reveal=link=>{
   if(!link)return;
   const bounds=strip.getBoundingClientRect(),tab=link.getBoundingClientRect();
   if(tab.left<bounds.left)strip.scrollBy({left:tab.left-bounds.left});
   else if(tab.right>bounds.right)strip.scrollBy({left:Math.min(tab.right-bounds.right,tab.left-bounds.left)});
   update();
  };
  const refresh=(resized=false)=>{
   update();
   const selected=strip.querySelector('a.on');
   if(resized||selected!==active)reveal(selected);
   active=selected;
  };
  left.addEventListener('click',()=>strip.scrollBy({left:-strip.clientWidth*.8}));
  right.addEventListener('click',()=>strip.scrollBy({left:strip.clientWidth*.8}));
  strip.addEventListener('scroll',update,{passive:true});
  strip.addEventListener('focusin',e=>reveal(e.target.closest('a')));
  const sizes=new ResizeObserver(()=>refresh(true));
  sizes.observe(root);sizes.observe(strip);
  const observed=new Set();
  const observeTabs=()=>{
   for(const tab of strip.children)if(!observed.has(tab)){sizes.observe(tab);observed.add(tab)}
   for(const tab of observed)if(!strip.contains(tab)){sizes.unobserve(tab);observed.delete(tab)}
  };
  new MutationObserver(()=>{observeTabs();refresh()}).observe(strip,{subtree:true,childList:true,characterData:true,attributes:true,attributeFilter:['class','hidden']});
  window.addEventListener('resize',()=>refresh(true));
  observeTabs();refresh();
 }
})();
