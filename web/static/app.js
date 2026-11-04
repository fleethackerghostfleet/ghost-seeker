const W=480,H=300;
const rand=(a,b)=>a+Math.random()*(b-a);
const pick=a=>a[Math.floor(Math.random()*a.length)];
const chance=p=>Math.random()<p;
const $=id=>document.getElementById(id);
const zulu=d=>d.toISOString().slice(11,19)+'Z';
let SOURCES=[],SRC={},CLS={},MODELS={},ATTR_LAB={},EXAMPLES=[];
let state={src:'ALL',model:'maritime',sort:'pri',tier:'all',paused:false,sel:null,directives:[]};
let items=[],stats={cap:0,keep:0,drop:0,bytes:0,db:0},cdr=0,weightData={},pendingItems=[],evictedIDs=new Set();
const imageCache=new Map();

/* ---------------- synthetic imagery ---------------- */
function grain(ctx,amp){const d=ctx.getImageData(0,0,W,H),p=d.data;for(let i=0;i<p.length;i+=4){const n=(Math.random()-.5)*amp;p[i]+=n;p[i+1]+=n;p[i+2]+=n}ctx.putImageData(d,0,0)}
function lg(ctx,y0,y1,stops){const g=ctx.createLinearGradient(0,y0,0,y1);stops.forEach((c,i)=>g.addColorStop(i/(stops.length-1),c));return g}
function poly(ctx,pts,fill){ctx.beginPath();pts.forEach(([x,y],i)=>i?ctx.lineTo(x,y):ctx.moveTo(x,y));ctx.closePath();ctx.fillStyle=fill;ctx.fill()}

function drawBackground(ctx,scene,mode,hz){
 const ir=mode==='ir';
 if(scene==='harbor'||scene==='open'){
  const tod=pick(['day','day','overcast','dusk']);
  const sky=ir?['#1c1c1c','#383838']:tod==='dusk'?['#41506e','#d99a6c']:tod==='overcast'?['#8e9ba6','#c9d0d5']:['#7fa7c9','#d6e3ec'];
  ctx.fillStyle=lg(ctx,0,hz*H,sky);ctx.fillRect(0,0,W,hz*H);
  if(scene==='harbor'){let x=-10;ctx.fillStyle=ir?'#4b4b4b':tod==='dusk'?'#2f3442':'#68757f';
   while(x<W){const bw=rand(12,38),bh=rand(6,34);ctx.fillRect(x,hz*H-bh,bw,bh);x+=bw+rand(0,6)}
   for(let i=0;i<2;i++){const cx=rand(40,W-40);ctx.fillRect(cx,hz*H-58,3,58);ctx.fillRect(cx-22,hz*H-58,46,3);ctx.fillRect(cx+18,hz*H-58,2,20)}}
  else{ctx.fillStyle=ir?'rgba(90,90,90,.4)':'rgba(255,255,255,.25)';ctx.fillRect(0,hz*H-3,W,4)}
  const sea=ir?['#2a2a2a','#121212']:tod==='dusk'?['#3d4a60','#141b26']:tod==='overcast'?['#5b6b77','#27343d']:['#3f6a87','#18324a'];
  ctx.fillStyle=lg(ctx,hz*H,H,sea);ctx.fillRect(0,hz*H,W,H-hz*H);
  ctx.strokeStyle=ir?'rgba(120,120,120,.25)':'rgba(255,255,255,.13)';ctx.lineWidth=1;
  for(let i=0;i<170;i++){const y=rand(hz*H+2,H),s=(y-hz*H)/(H-hz*H),x=rand(0,W),l=4+s*22;ctx.beginPath();ctx.moveTo(x,y);ctx.quadraticCurveTo(x+l/2,y-1-s*2,x+l,y);ctx.stroke()}
 }else if(scene==='sky'){
  ctx.fillStyle=lg(ctx,0,H,['#4f88c0','#a9cbe8','#d7e6f1']);ctx.fillRect(0,0,W,H);
  ctx.fillStyle='rgba(255,255,255,.55)';for(let c=0;c<5;c++){const cx=rand(0,W),cy=rand(20,H*.7);for(let k=0;k<6;k++){ctx.beginPath();ctx.ellipse(cx+rand(-40,40),cy+rand(-8,8),rand(20,45),rand(8,16),0,0,7);ctx.fill()}}
  ctx.fillStyle='#2f4030';ctx.fillRect(0,hz*H,W,H);ctx.fillStyle='#253425';for(let x=0;x<W;x+=6){ctx.beginPath();ctx.arc(x,hz*H,rand(3,8),Math.PI,0);ctx.fill()}
  ctx.fillStyle='#ffd27a';for(let x=20;x<W;x+=34)ctx.fillRect(x,H-6,3,2);
 }else if(scene==='aerial'){
  ctx.fillStyle='#1f4a63';ctx.fillRect(0,0,W,H);
  ctx.strokeStyle='rgba(255,255,255,.07)';for(let i=0;i<260;i++){const x=rand(0,W),y=rand(0,H);ctx.beginPath();ctx.moveTo(x,y);ctx.lineTo(x+rand(6,16),y+rand(-2,2));ctx.stroke()}
  if(chance(.5)){const side=chance(.5);ctx.beginPath();ctx.moveTo(side?0:W,0);let y=0,x=side?rand(40,110):W-rand(40,110);while(y<H){ctx.lineTo(x,y);y+=20;x+=rand(-14,14)}ctx.lineTo(x,H);ctx.lineTo(side?0:W,H);ctx.closePath();ctx.fillStyle='#8a8466';ctx.fill();ctx.strokeStyle='rgba(240,240,220,.5)';ctx.lineWidth=3;ctx.stroke();ctx.lineWidth=1}
 }else if(scene==='sat'){
  ctx.fillStyle='#0b0b0b';ctx.fillRect(0,0,W,H);
  if(chance(.6)){ctx.fillStyle='#3a3a3a';ctx.beginPath();ctx.moveTo(0,0);let x=0,y=rand(30,90);ctx.lineTo(0,y);while(x<W){x+=24;y+=rand(-12,12);ctx.lineTo(x,y)}ctx.lineTo(W,0);ctx.closePath();ctx.fill()}
 }
}

function drawSide(ctx,cls,b,mode,att){
 const mono=mode==='ir';const C=c=>mono?c==='hot'?'#f4f4f4':'#a9a9a9':c;
 const dir=chance(.5)?1:-1,w=b.w,h=b.h;
 ctx.save();ctx.translate(dir>0?b.x:b.x+w,b.y);ctx.scale(dir,1);
 if(mono){ctx.shadowColor='rgba(255,255,255,.55)';ctx.shadowBlur=6}
 const hull=(top,col,bow=.96)=>poly(ctx,[[0,h*top],[w,h*top-h*.04],[w*bow,h],[w*.04,h]],col);
 if((cls==='small boat'||cls==='fishing vessel'||cls==='warship')&&att.moving){ctx.fillStyle=mono?'rgba(200,200,200,.4)':'rgba(255,255,255,.55)';ctx.beginPath();ctx.ellipse(-w*.15,h*.95,w*.45,h*.09,0,0,7);ctx.fill();ctx.beginPath();ctx.ellipse(w*.92,h*.93,w*.08,h*.08,0,0,7);ctx.fill()}
 switch(cls){
  case 'cargo ship':{hull(.58,C('#2a2f37'));ctx.fillStyle=C('#8b2a2a');ctx.fillRect(w*.04,h*.88,w*.9,h*.12);
   const cols=['#c0392b','#2e86c1','#d68910','#1e8449','#7d3c98','#ba4a00','#5d6d7e'];for(let x=w*.2;x<w*.86;x+=w*.055)for(let r=0;r<2;r++){ctx.fillStyle=C(pick(cols));ctx.fillRect(x,h*.58-(r+1)*h*.15,w*.05,h*.14)}
   ctx.fillStyle=C('#e8e8e8');ctx.fillRect(w*.05,h*.05,w*.11,h*.53);ctx.fillStyle=C('#1b2633');ctx.fillRect(w*.06,h*.12,w*.09,h*.06);ctx.fillStyle=C('hot');ctx.fillRect(w*.12,0,w*.03,h*.1);break}
  case 'tanker':{hull(.55,C('#3a2b2b'));ctx.fillStyle=C('#7a2222');ctx.fillRect(w*.04,h*.85,w*.9,h*.15);ctx.strokeStyle=C('#b5b5b5');ctx.lineWidth=1;ctx.beginPath();ctx.moveTo(w*.2,h*.5);ctx.lineTo(w*.9,h*.48);ctx.stroke();
   ctx.fillStyle=C('#efefef');ctx.fillRect(w*.04,h*.08,w*.1,h*.45);ctx.fillStyle=C('hot');ctx.fillRect(w*.06,0,w*.03,h*.1);break}
  case 'warship':{poly(ctx,[[0,h*.55],[w*.99,h*.48],[w*.86,h],[w*.03,h]],C('#5c6670'));ctx.fillStyle=C('#6d7782');ctx.fillRect(w*.28,h*.25,w*.3,h*.32);ctx.fillRect(w*.36,h*.08,w*.12,h*.2);
   ctx.strokeStyle=C('#4a525a');ctx.lineWidth=Math.max(1,w*.012);ctx.beginPath();ctx.moveTo(w*.42,h*.08);ctx.lineTo(w*.42,-h*.25);ctx.moveTo(w*.36,-h*.1);ctx.lineTo(w*.48,-h*.1);ctx.stroke();
   ctx.fillStyle=C('#4f5861');ctx.fillRect(w*.72,h*.42,w*.06,h*.1);ctx.fillRect(w*.75,h*.44,w*.12,h*.03);ctx.fillStyle=C('hot');ctx.fillRect(w*.2,h*.3,w*.05,h*.2);ctx.fillStyle=C('#ffffff');ctx.font=`bold ${Math.max(6,h*.22)}px sans-serif`;ctx.fillText(String(Math.floor(rand(50,99))),w*.82,h*.8);break}
  case 'fishing vessel':{hull(.55,C('#e9edf0'));ctx.fillStyle=C('#2364a4');ctx.fillRect(w*.04,h*.82,w*.9,h*.18);ctx.fillStyle=C('#f3f3f3');ctx.fillRect(w*.15,h*.22,w*.3,h*.34);ctx.fillStyle=C('#24323f');ctx.fillRect(w*.18,h*.28,w*.24,h*.1);
   ctx.strokeStyle=C('#3b3b3b');ctx.lineWidth=1;ctx.beginPath();ctx.moveTo(w*.62,h*.55);ctx.lineTo(w*.62,0);ctx.lineTo(w*.95,h*.5);ctx.moveTo(w*.62,0);ctx.lineTo(w*.3,h*.2);ctx.stroke();break}
  case 'small boat':{poly(ctx,[[0,h*.45],[w,h*.5],[w*.8,h],[w*.06,h]],C(pick(['#f2f2f2','#d9d9d9','#3a3f46'])));ctx.fillStyle=C('#29323b');ctx.fillRect(w*.3,h*.1,w*.25,h*.35);ctx.fillStyle=C('hot');ctx.fillRect(w*.02,h*.4,w*.08,h*.25);break}
  case 'submarine':{ctx.fillStyle=C('#151a1f');ctx.beginPath();ctx.ellipse(w*.5,h*.92,w*.5,h*.14,0,Math.PI,0);ctx.fill();ctx.fillRect(w*.42,h*.15,w*.16,h*.75);ctx.fillRect(w*.5,-h*.15,w*.02,h*.32);break}
  case 'military aircraft':{ctx.fillStyle=C('#6b737b');ctx.beginPath();ctx.ellipse(w*.5,h*.55,w*.5,h*.13,0,0,7);ctx.fill();poly(ctx,[[w*.35,h*.55],[w*.62,h*.55],[w*.42,h*.98]],C('#59616a'));poly(ctx,[[w*.02,h*.5],[w*.14,h*.5],[w*.06,h*.05]],C('#59616a'));ctx.fillStyle=C('#22303b');ctx.beginPath();ctx.ellipse(w*.8,h*.45,w*.08,h*.08,0,0,7);ctx.fill();if(mono){ctx.fillStyle='#fff';ctx.fillRect(0,h*.48,w*.05,h*.14)}break}
  case 'civil aircraft':{ctx.fillStyle=C('#f4f4f4');ctx.beginPath();ctx.ellipse(w*.5,h*.55,w*.5,h*.11,0,0,7);ctx.fill();poly(ctx,[[w*.4,h*.58],[w*.56,h*.58],[w*.36,h]],C('#9aa3ab'));poly(ctx,[[w*.02,h*.5],[w*.12,h*.5],[w*.04,h*.0]],C('#1f5fa8'));ctx.fillStyle=C('#8c949b');ctx.fillRect(w*.4,h*.68,w*.08,h*.08);ctx.fillStyle=C('#3d4b57');for(let x=w*.2;x<w*.88;x+=w*.04)ctx.fillRect(x,h*.5,w*.015,h*.04);break}
  case 'helicopter':{ctx.fillStyle=C('#3f4a3a');ctx.beginPath();ctx.ellipse(w*.62,h*.6,w*.24,h*.24,0,0,7);ctx.fill();ctx.fillRect(w*.08,h*.52,w*.4,h*.08);ctx.fillRect(w*.04,h*.36,w*.06,h*.22);ctx.strokeStyle=C('#222');ctx.lineWidth=1.5;ctx.beginPath();ctx.moveTo(w*.15,h*.2);ctx.lineTo(w*1,h*.2);ctx.moveTo(w*.62,h*.2);ctx.lineTo(w*.62,h*.36);ctx.moveTo(w*.45,h*.95);ctx.lineTo(w*.82,h*.95);ctx.stroke();ctx.fillStyle=C('#9fc3d6');ctx.beginPath();ctx.ellipse(w*.76,h*.55,w*.08,h*.13,0,0,7);ctx.fill();break}
  case 'drone':{ctx.strokeStyle=C('#222');ctx.lineWidth=Math.max(1,w*.06);ctx.beginPath();ctx.moveTo(w*.1,h*.2);ctx.lineTo(w*.9,h*.8);ctx.moveTo(w*.9,h*.2);ctx.lineTo(w*.1,h*.8);ctx.stroke();ctx.fillStyle=C('#333');ctx.fillRect(w*.35,h*.35,w*.3,h*.3);ctx.strokeStyle=C('rgba(40,40,40,.6)');ctx.lineWidth=1;[[.1,.2],[.9,.2],[.1,.8],[.9,.8]].forEach(([x,y])=>{ctx.beginPath();ctx.ellipse(w*x,h*y,w*.14,h*.08,0,0,7);ctx.stroke()});break}
  case 'person':{ctx.fillStyle=mono?'rgba(150,150,150,.4)':'rgba(255,255,255,.35)';ctx.beginPath();ctx.ellipse(w*.5,h*.85,w*.5,h*.15,0,0,7);ctx.fill();ctx.fillStyle=C('#ff7a1a');ctx.fillRect(w*.25,h*.55,w*.5,h*.3);ctx.fillStyle=C('hot');ctx.beginPath();ctx.arc(w*.5,h*.38,w*.2,0,7);ctx.fill();break}
  case 'bird':{ctx.strokeStyle=C('#1d1d1d');ctx.lineWidth=1.5;ctx.beginPath();ctx.moveTo(0,h*.3);ctx.quadraticCurveTo(w*.25,0,w*.5,h*.6);ctx.quadraticCurveTo(w*.75,0,w,h*.3);ctx.stroke();break}
  case 'buoy':{poly(ctx,[[w*.5,0],[w,h*.85],[0,h*.85]],C(pick(['#c0392b','#1e8449'])));ctx.fillStyle=C('hot');ctx.beginPath();ctx.arc(w*.5,h*.05,w*.15,0,7);ctx.fill();break}
  case 'debris':{for(let i=0;i<4;i++)poly(ctx,[[rand(0,w*.5),rand(h*.4,h)],[rand(w*.3,w),rand(h*.4,h)],[rand(w*.3,w),rand(h*.7,h)]],C(pick(['#6b4f2f','#8a6a3f','#e07b2b'])));break}
 }
 ctx.restore();
}
function drawTop(ctx,cls,b,mode,att){
 const sar=mode==='sar';ctx.save();ctx.translate(b.cx,b.cy);ctx.rotate(b.a);const L=b.L,B=b.B;
 if(sar){ctx.shadowColor='#fff';ctx.shadowBlur=8;ctx.fillStyle='#f0f0f0';ctx.beginPath();ctx.ellipse(0,0,L/2,B/2,0,0,7);ctx.fill();ctx.globalAlpha=.35;ctx.fillRect(-L*.9,-.5,L*1.8,1.2);ctx.fillRect(-.5,-L*.5,1.2,L);ctx.restore();return}
 if(att.moving&&cls!=='person'&&cls!=='debris'){ctx.fillStyle='rgba(255,255,255,.45)';ctx.beginPath();ctx.moveTo(-L/2,0);ctx.lineTo(-L*1.8,-B*1.6);ctx.lineTo(-L*1.8,B*1.6);ctx.closePath();ctx.fill();ctx.fillStyle='rgba(255,255,255,.7)';ctx.fillRect(-L*1.1,-B*.25,L*.6,B*.5)}
 const hullPath=()=>{ctx.beginPath();ctx.moveTo(-L/2,-B/2);ctx.lineTo(L*.3,-B/2);ctx.lineTo(L/2,0);ctx.lineTo(L*.3,B/2);ctx.lineTo(-L/2,B/2);ctx.closePath()};
 switch(cls){
  case 'submarine':ctx.globalAlpha=.7;ctx.fillStyle='#0d1b24';ctx.beginPath();ctx.ellipse(0,0,L/2,B/2.4,0,0,7);ctx.fill();ctx.globalAlpha=1;ctx.fillStyle='#091218';ctx.fillRect(-L*.05,-B*.15,L*.12,B*.3);break;
  case 'person':ctx.fillStyle='rgba(255,255,255,.35)';ctx.beginPath();ctx.arc(0,0,L*.6,0,7);ctx.fill();ctx.fillStyle='#ff7a1a';ctx.beginPath();ctx.arc(0,0,L*.32,0,7);ctx.fill();break;
  case 'debris':for(let i=0;i<5;i++)poly(ctx,[[rand(-L/2,L/2),rand(-B,B)],[rand(-L/2,L/2),rand(-B,B)],[rand(-L/2,L/2),rand(-B,B)]],pick(['#6b4f2f','#8a6a3f','#e07b2b']));break;
  default:{const col={'warship':'#6a737c','cargo ship':'#30353c','tanker':'#5a2a2a','fishing vessel':'#e8ecef','small boat':'#f4f4f4'}[cls];hullPath();ctx.fillStyle=col;ctx.fill();
   if(cls==='cargo ship'){const cs=['#c0392b','#2e86c1','#d68910','#1e8449'];for(let x=-L*.3;x<L*.25;x+=L*.07){ctx.fillStyle=pick(cs);ctx.fillRect(x,-B*.38,L*.06,B*.76)}ctx.fillStyle='#eee';ctx.fillRect(-L*.45,-B*.4,L*.1,B*.8)}
   if(cls==='tanker'){ctx.fillStyle='#8a8a8a';ctx.fillRect(-L*.3,-1,L*.6,2);ctx.fillStyle='#eee';ctx.fillRect(-L*.46,-B*.4,L*.1,B*.8)}
   if(cls==='warship'){ctx.fillStyle='#7f8892';ctx.fillRect(-L*.15,-B*.3,L*.25,B*.6);ctx.fillStyle='#555';ctx.beginPath();ctx.arc(L*.3,0,B*.15,0,7);ctx.fill()}
   if(cls==='fishing vessel'||cls==='small boat'){ctx.fillStyle='#2a3743';ctx.fillRect(-L*.15,-B*.3,L*.25,B*.6)}}
 }
 ctx.restore();
}

function mulberry32(seed){
 return function(){let t=seed+=0x6D2B79F5;t=Math.imul(t^t>>>15,t|1);t^=t+Math.imul(t^t>>>7,t|61);return((t^t>>>14)>>>0)/4294967296}
}
function renderImage(item){
 const originalRandom=Math.random;
 Math.random=mulberry32(item.seed);
 try{
  const src=item.src,cv=document.createElement('canvas');cv.width=W;cv.height=H;const ctx=cv.getContext('2d');
  drawBackground(ctx,src.scene,src.mode,item.hz);
  [...item.dets].sort((a,b)=>(a.b.y+a.b.h)-(b.b.y+b.b.h)).forEach(d=>d.b.top?drawTop(ctx,d.cls,d.b,src.mode,d.attrs):drawSide(ctx,d.cls,d.b,src.mode,d.attrs));
  if(src.mode==='ir'||src.mode==='sar'){const d=ctx.getImageData(0,0,W,H),p=d.data;for(let i=0;i<p.length;i+=4){const l=.3*p[i]+.59*p[i+1]+.11*p[i+2];p[i]=p[i+1]=p[i+2]=l}ctx.putImageData(d,0,0)}
  grain(ctx,src.mode==='sar'?46:src.mode==='ir'?16:9);
  const vg=ctx.createRadialGradient(W/2,H/2,H*.4,W/2,H/2,H*.9);vg.addColorStop(0,'rgba(0,0,0,0)');vg.addColorStop(1,'rgba(0,0,0,.35)');ctx.fillStyle=vg;ctx.fillRect(0,0,W,H);
  ctx.font='10px ui-monospace,Menlo,monospace';ctx.fillStyle='rgba(0,0,0,.45)';ctx.fillRect(0,0,W,15);ctx.fillStyle='#e8f1f7';
  ctx.fillText(`${src.id}  ${item.ts.toISOString().slice(0,19).replace('T',' ')}Z`,6,11);ctx.textAlign='right';ctx.fillText(`FRM ${String(item.frame).padStart(6,'0')}  ${src.mode.toUpperCase()}`,W-6,11);
  return cv.toDataURL('image/jpeg',.82);
 }finally{
  Math.random=originalRandom;
 }
}
function imageFor(item){
 if(!imageCache.has(item.id))imageCache.set(item.id,renderImage(item));
 return imageCache.get(item.id);
}

function ruleLabel(r){
 const cl=r.classes.length?(r.classes.length>3?`${r.classes.slice(0,2).map(k=>CLS[k].lab).join(' / ')} +${r.classes.length-2}`:r.classes.map(k=>CLS[k].lab).join(' / ')):'Any object';
 const at=r.attrs.length?' · '+r.attrs.map(a=>ATTR_LAB[a]).join(' or '):'';
 return(r.type==='boost'?'▲ ':'▼ ')+cl+at;
}

/* ---------------- UI ---------------- */
state.dens='m';state.page='feed';
const tcol=t=>['','var(--p1)','var(--p2)','var(--p3)','var(--p4)'][t];
const esc=t=>t.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/"/g,'&quot;');
function normalizeItem(item){
 item.ts=new Date(item.ts);item.src=SRC[typeof item.src==='string'?item.src:item.src.id];
 return item;
}
function findItem(id){return items.find(item=>item.id===id)}
function renderAll(){
 renderFeed(true);renderStats();renderRp();if(state.sel)renderDetail();if(state.page==='rv')renderReview();
}
function mergeItem(raw,fresh=true){
 const item=normalizeItem(raw);if(evictedIDs.has(item.id))return;
 const index=items.findIndex(existing=>existing.id===item.id);
 if(index>=0)item.el=items[index].el;
 item.fresh=fresh;
 if(index<0)items.push(item);else items[index]=item;
 if(rv.cur?.id===item.id)rv.cur=item;
 if(fresh){['st1','st2','st3','st4','st5'].forEach((id,index)=>setTimeout(()=>flash(id),index*140));setTimeout(()=>{const current=findItem(item.id);if(current){current.fresh=false;updateCard(current)}},6000)}
}
function flash(id){const element=$(id);if(!element)return;element.classList.add('flash');setTimeout(()=>element.classList.remove('flash'),350)}
function applySnapshot(data){
 const selection=state.sel;
 const elements=new Map(items.map(item=>[item.id,item.el]));
 items=data.items.map(item=>{item=normalizeItem(item);item.el=elements.get(item.id);return item});
 const retained=new Set(items.map(item=>item.id));for(const id of imageCache.keys())if(!retained.has(id))imageCache.delete(id);
 pendingItems=[];evictedIDs.clear();state.model=data.state.model;state.directives=data.state.directives;
 stats=data.stats;cdr=data.cdr;weightData=data.weights||{};
 state.sel=items.some(item=>item.id===selection)?selection:null;
 if(!state.sel){document.body.classList.remove('drawer-open');$('drawer').innerHTML=''}
 rv.cur=rv.cur&&items.find(item=>item.id===rv.cur.id)||null;
 renderAll();
}
function applyItemUpdate(raw){
 const item=normalizeItem(raw);mergeItem(item,false);
 if(state.sel===item.id)renderDetail();
 renderFeed();renderStats();renderRp(true);if(state.page==='rv'&&!rv.busy)renderReview();
}
async function request(path,method='GET',body){
 const response=await fetch(path,{method,headers:body?{'Content-Type':'application/json'}:{},body:body?JSON.stringify(body):undefined});
 const data=response.status===204?null:await response.json();
 if(!response.ok)throw new Error(data?.error||`Request failed (${response.status})`);
 return data;
}
async function patchItem(id,changes){
 const result=await request(`/api/items/${encodeURIComponent(id)}`,'PATCH',changes);
 cdr=result.cdr;applyItemUpdate(result.item);return findItem(id);
}
function openStream(){
 const stream=new EventSource('/api/stream');
 stream.addEventListener('snapshot',event=>applySnapshot(JSON.parse(event.data)));
 stream.addEventListener('item',event=>{
  const data=JSON.parse(event.data);
  if(state.paused)pendingItems.push(data);else{mergeItem(data.item);stats=data.stats;renderFeed();renderStats();renderRp(true);if(state.page==='rv')renderReview()}
 });
 stream.addEventListener('stats',event=>{
  stats=JSON.parse(event.data).stats;renderStats();
 });
 stream.addEventListener('update',event=>{
  const data=JSON.parse(event.data);cdr=data.cdr;applyItemUpdate(data.item);
 });
 stream.addEventListener('evict',event=>{
  const {id}=JSON.parse(event.data);evictedIDs.add(id);imageCache.delete(id);pendingItems=pendingItems.filter(data=>data.item.id!==id);
  items=items.filter(item=>item.id!==id);
  if(state.sel===id)closeDetail();
  if(rv.cur?.id===id){rv.cur=null;rv.shown=null}
  renderAll();
 });
}
async function init(){
 const config=await request('/api/config');
 SOURCES=[{id:'ALL',name:'All Sources (fused)'},...config.sources];
 SRC=Object.fromEntries(SOURCES.map(source=>[source.id,source]));
 CLS=config.classes;MODELS=config.models;ATTR_LAB=config.attrLabels;EXAMPLES=config.examples;
 $('srcSel').innerHTML=SOURCES.map(s=>`<option value="${s.id}">${s.name}</option>`).join('');
 $('modelSel').innerHTML=Object.entries(MODELS).map(([k,m])=>`<option value="${k}">${m.name}</option>`).join('');
 $('examples').innerHTML=EXAMPLES.map(e=>`<span class="ex">${e}</span>`).join('');
 $('examples').onclick=e=>{if(e.target.classList.contains('ex')){$('nlIn').value=e.target.textContent;applyDirective()}};
 $('srcSel').onchange=e=>{state.src=e.target.value;closeDetail();renderFeed(true);renderStats();renderRp()};
 $('modelSel').onchange=async e=>{try{await request('/api/model','PUT',{id:e.target.value});toast(`Base model set to ${MODELS[e.target.value].name}`)}catch(error){toast(error.message);e.target.value=state.model}};
 $('nlGo').onclick=applyDirective;$('nlIn').onkeydown=e=>{if(e.key==='Enter'&&!e.shiftKey){e.preventDefault();applyDirective()}};
 $('clearDir').onclick=async()=>{if(!state.directives.length)return;try{await request('/api/directives','DELETE');toast('Directives cleared — base model restored')}catch(error){toast(error.message)}};
 $('sortSeg').onclick=e=>{const b=e.target.closest('button');if(!b)return;state.sort=b.dataset.s;[...$('sortSeg').children].forEach(x=>x.classList.toggle('on',x===b));renderFeed()};
 $('densSeg').onclick=e=>{const b=e.target.closest('button');if(!b)return;state.dens=b.dataset.d;[...$('densSeg').children].forEach(x=>x.classList.toggle('on',x===b));$('grid').className='grid '+state.dens};
 $('tierSeg').onclick=e=>{const b=e.target.closest('button');if(!b)return;state.tier=b.dataset.t;renderFeed()};
 $('pauseBtn').onclick=()=>{state.paused=!state.paused;$('pauseBtn').textContent=state.paused?'Resume':'Pause';$('live').classList.toggle('paused',state.paused);$('live').querySelector('span').textContent=state.paused?'PAUSED':'LIVE';if(!state.paused){for(const event of pendingItems){mergeItem(event.item);stats=event.stats}pendingItems=[];renderAll()}};
 $('grid').onclick=e=>{const c=e.target.closest('.card');if(c)openDetail(c.dataset.id)};
 $('topGrid').onclick=e=>{const c=e.target.closest('.card');if(c){location.hash='#feed';openDetail(c.dataset.id)}};
 document.addEventListener('keydown',e=>{const tg=e.target.tagName;if(tg==='TEXTAREA'||tg==='INPUT'||tg==='SELECT'||e.ctrlKey||e.metaKey||e.altKey)return;
  const k=e.key.toLowerCase();
  if(k==='n'){toggleNight();return}
  if(state.page==='rv'){if(k==='escape'){location.hash='#feed';return}const a=RVKEYS[k];if(a){e.preventDefault();if(!e.repeat)rvAct(a)}return}
  if(state.page!=='feed')return;
  if(k==='r'){location.hash='#review';return}
  if(k==='escape')closeDetail();
  if(state.sel&&['1','2','3','4'].includes(k))setManual(state.sel,+k);
  if(state.sel&&['arrowright','arrowdown','arrowleft','arrowup'].includes(k)){e.preventDefault();step(k==='arrowright'||k==='arrowdown'?1:-1)}});
 $('rvKeys').onclick=e=>{const b=e.target.closest('.kb');if(b){b.blur();rvAct(b.dataset.a)}};
 $('rvOrder').onclick=e=>{const b=e.target.closest('button');if(!b)return;rv.order=b.dataset.o;[...$('rvOrder').children].forEach(x=>x.classList.toggle('on',x===b));rv.cur=null;renderReview()};
 $('nightBtn').onclick=toggleNight;
 window.addEventListener('hashchange',route);
 setInterval(()=>$('clock').textContent=zulu(new Date()),1000);$('clock').textContent=zulu(new Date());
 route();renderFeed(true);renderStats();renderRp();openStream();
}
function route(){
 state.page=location.hash==='#reprioritize'?'rp':location.hash==='#review'?'rv':'feed';
 $('pgFeed').classList.toggle('on',state.page==='feed');$('pgRp').classList.toggle('on',state.page==='rp');$('pgRv').classList.toggle('on',state.page==='rv');$('navRv').classList.toggle('on',state.page==='rv');
 $('navFeed').classList.toggle('on',state.page==='feed');$('navRp').classList.toggle('on',state.page==='rp');
 if(state.page==='feed')renderFeed(true);else if(state.page==='rv'){closeDetail();rv.cur=null;rv.shown=null;renderReview()}else{renderRp();setTimeout(()=>$('nlIn').focus(),50)}
}

function visible(){
 let l=items.filter(i=>(state.src==='ALL'||i.src.id===state.src)&&!i.dismissed);
 const counts={all:l.length,1:0,2:0,3:0,4:0};l.forEach(i=>counts[i.tier]++);
 if(state.tier!=='all')l=l.filter(i=>String(i.tier)===state.tier);
 l.sort(state.sort==='pri'?(a,b)=>a.tier-b.tier||b.central-a.central||b.ts-a.ts:(a,b)=>b.ts-a.ts);
 return{l,counts};
}
function renderTierSeg(counts){
 const T=[['all','All'],['1','P1'],['2','P2'],['3','P3'],['4','P4']];
 $('tierSeg').innerHTML=T.map(([k,l])=>`<button data-t="${k}" class="${state.tier===k?'on':''}">${k!=='all'?`<span class="tchip" style="background:${tcol(+k)}"></span>`:''}${l}<span class="c">${counts[k]}</span></button>`).join('');
}
function boxesHTML(it,labels){
 return it.dets.map((d,i)=>{const sd=it.det[i];const c=sd.sup?'sup':sd.matched?'match':sd.s>=.6?'hi':sd.s>=.3?'md':'lo';const b=d.b;
  return`<div class="box ${c}" style="left:${b.x/W*100}%;top:${b.y/H*100}%;width:${b.w/W*100}%;height:${b.h/H*100}%">${labels?`<em>${CLS[d.cls].lab} ${Math.round(d.conf*100)}%</em>`:''}</div>`}).join('');
}
function topLine(it){
 const r=it.dets.map((d,i)=>[d,it.det[i]]).sort((a,b)=>b[1].s-a[1].s);if(!r.length)return'—';
 const[d,sd]=r[0];const tag=d.attrs.dark&&CLS[d.cls].g!=='other'&&CLS[d.cls].g!=='clutter'?' · no AIS':d.attrs.approaching?' · inbound':d.attrs.new?' · new':'';
 return`<span class="${sd.matched?'m':tag&&sd.s>.4?'a':''}">${CLS[d.cls].lab}${tag}</span>${r.length>1?` <span style="opacity:.6">+${r.length-1}</span>`:''}`;
}
function cardHTML(it,mini){
 return`<div class="thumb"><img src="${imageFor(it)}" alt=""><div class="bx">${mini?boxesHTML(it,false):''}</div><span class="tier t${it.tier}">P${it.tier}</span><span class="cdrtag">✓ CDR</span><span class="newtag">NEW</span><div class="ov"><span class="sid">${it.src.id}</span><span class="t mono">${zulu(it.ts).slice(0,5)}Z</span><span class="delta"></span><span class="rvk" style="margin-left:auto">✓</span><b class="cs" style="margin-left:4px">${mini?it.central.toFixed(2):''}</b></div></div><div class="cl"></div>`;
}
function makeCard(it){const el=document.createElement('div');el.dataset.id=it.id;el.innerHTML=cardHTML(it);it.el=el;return el}
function updateCard(it){
 const el=it.el;if(!el)return;el.className=`card p${it.tier}${it.fresh?' fresh':''}${it.sent?' sent':''}${it.reviewed?' rev':''}${it.manual?' man':''}${state.sel===it.id?' sel':''}`;
 const t=el.querySelector('.tier');t.className='tier t'+it.tier;t.textContent='P'+it.tier;
 el.querySelector('.bx').innerHTML=boxesHTML(it,false);el.querySelector('.cl').innerHTML=topLine(it);
 el.querySelector('.cs').textContent=it.central.toFixed(2);
 const dl=it.central-it.base,de=el.querySelector('.delta');de.className='delta '+(dl>.04?'up':dl<-.04?'dn':'');de.textContent=dl>.04?`▲${dl.toFixed(2).slice(1)}`:dl<-.04?`▼${(-dl).toFixed(2).slice(1)}`:'';
}
function renderFeed(noAnim){
 const{l,counts}=visible();renderTierSeg(counts);$('empty').style.display=l.length?'none':'block';
 if(state.page!=='feed')noAnim=true;
 const grid=$('grid'),first=new Map();
 if(!noAnim)[...grid.children].forEach(c=>first.set(c.dataset.id,c.getBoundingClientRect()));
 const keep=new Set(l.map(i=>i.id));[...grid.children].forEach(c=>{if(!keep.has(c.dataset.id))c.remove()});
 l.forEach((it,idx)=>{const el=it.el||makeCard(it);updateCard(it);if(grid.children[idx]!==el)grid.insertBefore(el,grid.children[idx]||null)});
 if(!noAnim){const vh=$('feed').clientHeight+200;l.forEach(it=>{const el=it.el,f=first.get(it.id),r=el.getBoundingClientRect();if(r.top>vh&&(!f||f.top>vh))return;
  if(f){const dx=f.left-r.left,dy=f.top-r.top;if(Math.abs(dx)>1||Math.abs(dy)>1){el.style.transition='none';el.style.transform=`translate(${dx}px,${dy}px)`;el.offsetWidth;el.style.transition='transform .5s cubic-bezier(.2,.8,.2,1)';el.style.transform=''}}
  else if(it.fresh){el.classList.remove('enter');el.offsetWidth;el.classList.add('enter')}})}
 renderDirBar();
}
function renderDirBar(){
 const d=state.directives;$('dirCount').textContent=d.length||'';
 $('dirBar').innerHTML=d.length?`<span>Directives:</span>${d.slice(0,2).map(x=>`<span class="pill" title="${esc(x.text)}">${esc(x.text)}</span>`).join('')}${d.length>2?`<span>+${d.length-2}</span>`:''}<a href="#reprioritize">Edit</a>`:`<span>Base model only</span><a href="#reprioritize">Reprioritize →</a>`;
}
function renderStats(){
 const vis=items.filter(i=>state.src==='ALL'||i.src.id===state.src);
 $('hsImgs').textContent=vis.length;$('hsP1').textContent=vis.filter(i=>i.tier===1).length;$('hsCdr').textContent=cdr;
 $('cCap').textContent=stats.cap.toLocaleString();$('cKeep').textContent=stats.keep;$('cDrop').textContent=stats.drop.toLocaleString();
 $('cKB').textContent=(stats.bytes/1024).toFixed(1);$('cDb').textContent=(stats.db+180000).toLocaleString();$('cP1').textContent=items.filter(i=>i.tier===1).length;
 $('cModel').textContent=MODELS[state.model].name.replace(' (default)','')+(state.directives.length?` + ${state.directives.length} directive${state.directives.length>1?'s':''}`:'');
 const unrev=vis.filter(i=>!i.reviewed&&!i.dismissed).length;$('rvCount').textContent=unrev||'';if(state.page==='rv')rvProgress();
 $('cSaved').textContent=Math.round(stats.drop/Math.max(1,stats.cap)*100)+'%';
}

/* ---- detail drawer ---- */
function openDetail(id){const prev=items.find(i=>i.id===state.sel);state.sel=id;document.body.classList.add('drawer-open');prev&&updateCard(prev);const it=items.find(i=>i.id===id);it&&updateCard(it);renderDetail();$('drawer').scrollTop=0;it&&it.el&&it.el.scrollIntoView({block:'nearest',behavior:'smooth'})}
function closeDetail(){const prev=items.find(i=>i.id===state.sel);state.sel=null;document.body.classList.remove('drawer-open');prev&&updateCard(prev)}
function step(dir){const l=visible().l;const i=l.findIndex(x=>x.id===state.sel);const n=l[Math.max(0,Math.min(l.length-1,i+dir))];if(n)openDetail(n.id)}
function buildWhy(it,rows){
 const why=[];const top=rows[0];
 if(top)why.push({c:top[1].s>.6?'al':'',t:`Top contact: <b>${CLS[top[0].cls].lab}</b> (conf ${top[0].conf.toFixed(2)}) contributes ${top[1].s.toFixed(2)} under ${MODELS[state.model].name}`});
 rows.forEach(([d])=>{if(d.attrs.dark&&!['other','clutter'].includes(CLS[d.cls].g))why.push({c:'al',t:`${CLS[d.cls].lab} has no AIS / transponder correlation`});if(d.attrs.approaching)why.push({c:'al',t:`${CLS[d.cls].lab} track is inbound toward the sensor`});if(d.attrs.new)why.push({c:'',t:`Change detection: ${CLS[d.cls].lab} not present in previous pass`})});
 const seen=new Set();rows.forEach(([d,sd])=>sd.notes.forEach(r=>{const k=ruleLabel(r);if(seen.has(k))return;seen.add(k);const dir=state.directives.find(x=>x.id===r.directiveId);why.push({c:r.type==='boost'?'up':'dn',t:`${r.type==='boost'?'Boosted':'Suppressed'} by directive “${dir?esc(dir.text):''}” (${k.slice(2)})`})}));
 if(rows.some(([,sd])=>sd.focus))why.push({c:'dn',t:'Contacts not matching active directives are de-weighted (focus mode)'});
 if(it.edge<it.central-.15)why.push({c:'up',t:`Command model ranked this higher than edge triage (${it.edge.toFixed(2)} → ${it.central.toFixed(2)})`});
 const _w=new Set();for(let i=why.length-1;i>=0;i--){if(_w.has(why[i].t))why.splice(i,1);else _w.add(why[i].t)}
 return why;
}
function renderDetail(){
 const it=items.find(i=>i.id===state.sel);const D=$('drawer');
 if(!it){D.innerHTML='';return}
 const rows=it.dets.map((d,i)=>[d,it.det[i]]).sort((a,b)=>b[1].s-a[1].s);
 const why=buildWhy(it,rows);
 const tierName=['','Critical — notify commander','High','Routine','Low'][it.tier];
 const fmtMs=ms=>ms>=1000?(ms/1000).toFixed(1)+' s':ms+' ms';const dl=it.central-it.base;
 const l=visible().l,idx=l.findIndex(x=>x.id===it.id);
 D.innerHTML=`<div class="dhead"><div><h2><span class="tier t${it.tier}">P${it.tier}</span>${tierName}</h2><small>${it.src.name} · <span class="mono">${zulu(it.ts)}</span> · DB #${it.dbId}${idx>=0?` · ${idx+1} of ${l.length}`:''}</small></div>
  <div class="dnav"><button id="dPrev" title="Previous (←)">‹</button><button id="dNext" title="Next (→)">›</button><button id="dClose" title="Close (Esc)">×</button></div></div>
 <div class="dimg"><img src="${imageFor(it)}">${boxesHTML(it,true)}</div>
 <div class="dbody">
  <div class="lbl" style="margin-bottom:6px">Set priority · keys 1–4${it.manual?' · <span style="color:var(--accent);text-transform:none;letter-spacing:0">operator override</span>':''}</div><div class="tierbtns">${[1,2,3,4].map(t=>`<button class="btn tb${t} ${it.tier===t?'on':''}" data-t="${t}">${t} · P${t}</button>`).join('')}<button class="btn" id="dReset" title="Reset to model" ${it.manual?'':'disabled style="opacity:.4"'}>↺</button></div>
  <div class="scores"><div class="sc"><span>Edge triage</span><b>${it.edge.toFixed(2)}</b></div><div class="sc"><span>Command score</span><b style="color:${tcol(it.tier)}">${it.central.toFixed(2)}</b></div><div class="sc"><span>Directive Δ</span><b style="color:${dl>.04?'var(--accent)':dl<-.04?'#8d99a5':'var(--text)'}">${dl>=0?'+':''}${dl.toFixed(2)}</b></div></div>
  <div class="dsec"><h4>Detections</h4><table><tr><th>Object</th><th>Behavior</th><th class="n">Conf</th><th class="n">Score</th></tr>${rows.map(([d,sd])=>`<tr><td>${sd.matched?'<span style="color:var(--accent)">●</span> ':sd.sup?'<span style="color:#8d99a5">○</span> ':''}${CLS[d.cls].lab}</td><td style="color:var(--muted)">${Object.keys(ATTR_LAB).filter(a=>d.attrs[a]&&!(a==='dark'&&['other','clutter'].includes(CLS[d.cls].g))).map(a=>ATTR_LAB[a]).join(', ')||'—'}</td><td class="n">${d.conf.toFixed(2)}</td><td class="n" style="color:${sd.s>=.6?'var(--p1)':sd.s>=.3?'var(--p2)':'var(--muted)'}">${sd.s.toFixed(2)}</td></tr>`).join('')}</table></div>
  <div class="dsec"><h4>Why this priority</h4><ul class="why">${why.slice(0,7).map(w=>`<li class="${w.c}">${w.t}</li>`).join('')}</ul></div>
  <div class="dsec"><h4>Processing chain</h4><ul class="tl">
   <li class="edge"><i></i><div>Captured &amp; triaged on sensor<br><small>${it.src.hw} · ${it.src.em} · score ${it.edge.toFixed(2)} ≥ 0.20 → keep</small></div><span class="ms">${fmtMs(it.lat.edge)}</span></li>
   <li><i></i><div>Uplinked to central<br><small>${it.src.link} · ${it.kb} KB JPEG + metadata</small></div><span class="ms">${fmtMs(it.lat.up)}</span></li>
   <li><i></i><div>Stored in imagery database<br><small>record ${it.dbId} · frame ${it.frame}</small></div><span class="ms">12 ms</span></li>
   <li><i></i><div>Command reprioritization<br><small>${MODELS[state.model].name}${state.directives.length?` + ${state.directives.length} directive${state.directives.length>1?'s':''}`:''}</small></div><span class="ms">${fmtMs(it.lat.cen)}</span></li>
  </ul></div>
  <div class="actions"><button class="btn cdr ${it.sent?'done':''}" id="aCdr">${it.sent?'✓ Sent to Commander':'Send to Commander'}</button><button class="btn" id="aSim">Prioritize similar</button><button class="btn" id="aDis">Dismiss</button></div>
  <div class="kbd"><kbd>←</kbd> <kbd>→</kbd> step through feed · <kbd>Esc</kbd> close</div>
 </div>`;
 D.querySelectorAll('.tierbtns [data-t]').forEach(b=>b.onclick=()=>setManual(it.id,+b.dataset.t));$('dReset').onclick=async()=>{try{await patchItem(it.id,{manual:0,reviewed:false})}catch(error){toast(error.message)}};$('dPrev').onclick=()=>step(-1);$('dNext').onclick=()=>step(1);$('dClose').onclick=closeDetail;
 $('aCdr').onclick=async()=>{if(it.sent)return;try{await patchItem(it.id,{sent:true});toast('Image and detections pushed to Commander queue')}catch(error){toast(error.message)}};
 $('aSim').onclick=()=>{const k=rows[0]&&rows[0][0].cls;if(!k)return;$('nlIn').value=`Prioritize ${CLS[k].lab.toLowerCase()}s`;location.hash='#reprioritize';toast('Drafted a directive from this image — review and apply')};
 $('aDis').onclick=async()=>{const l=visible().l,i=l.indexOf(it),nx=l[i+1]||l[i-1];try{await patchItem(it.id,{dismissed:true,reviewed:true});nx&&findItem(nx.id)?openDetail(nx.id):closeDetail();toast('Image dismissed from feed (retained in DB)')}catch(error){toast(error.message)}};
}

/* ---- reprioritize page ---- */
function renderRp(light){
 const vis=items.filter(i=>(state.src==='ALL'||i.src.id===state.src)&&!i.dismissed);
 const now=[0,0,0,0,0],base=[0,0,0,0,0];vis.forEach(i=>{now[i.centralTier]++;base[i.baseTier]++});
 $('impact').innerHTML=[1,2,3,4].map(t=>{const d=now[t]-base[t];return`<div class="imp"><span><span class="tchip" style="background:${tcol(t)}"></span>P${t}</span><b>${now[t]}</b>${d?`<small class="${d>0?'up':'dn'}">${d>0?'+':''}${d}</small>`:''}</div>`}).join('');
 $('topGrid').innerHTML=[...vis].sort((a,b)=>a.tier-b.tier||b.central-a.central).slice(0,8).map(it=>`<div class="card p${it.tier}" data-id="${it.id}">${cardHTML(it,true)}</div>`).join('');
 if(light)return;
 renderWeights();renderDirectives();$('modelSel').value=state.model;$('modelDesc').textContent=MODELS[state.model].desc;
}
function renderWeights(){
 const rows=Object.entries(weightData).map(([k,weight])=>({k,base:weight.base,eff:weight.effective,cond:weight.conditional})).sort((a,b)=>b.eff-a.eff);
 $('wNote').textContent=state.directives.length?'Highlighted rows changed.':'';
 $('weights').innerHTML=rows.map(r=>{const c=r.eff>r.base+.02?'up':r.eff<r.base-.02?'down':'';return`<div class="wrow ${c}"><span>${CLS[r.k].lab}${r.cond?' <span style="color:var(--accent)">*</span>':''}</span><div class="bar"><i style="width:${r.eff*100}%"></i></div><span class="v">${r.eff.toFixed(2)}</span></div>`}).join('')+(rows.some(r=>r.cond)?'<div class="hint">* boosted further when its condition is met (e.g. approaching, no AIS)</div>':'');
}
function renderDirectives(){
 const d=state.directives;$('clearDir').style.display=d.length?'':'none';
 $('directives').innerHTML=d.length?d.map((x,i)=>`<div class="dir"><span class="n">${i+1}</span><div class="body"><q>${esc(x.text)}</q><div class="rules">${x.rules.map(r=>`<span class="rule ${r.type==='suppress'?'sup':''}">${ruleLabel(r)}</span>`).join('')}${x.rules.some(r=>r.only)?'<span class="rule focus">Focus: others ×0.3</span>':''}</div></div><button class="x" data-id="${x.id}" title="Remove directive">×</button></div>`).join(''):'<div class="nodir">No directives — feed is ranked by the base model only.</div>';
 $('directives').querySelectorAll('.x').forEach(b=>b.onclick=async()=>{try{await request(`/api/directives/${encodeURIComponent(b.dataset.id)}`,'DELETE');toast('Directive removed')}catch(error){toast(error.message)}});
}
async function applyDirective(){
 const text=$('nlIn').value.trim();if(!text)return;
 $('thinking').classList.add('on');$('nlGo').disabled=true;
 try{await request('/api/directives','POST',{text});$('nlIn').value='';toast('Directive applied')}catch(error){toast(error.message)}finally{$('thinking').classList.remove('on');$('nlGo').disabled=false}
}
async function setManual(id,t){if(!findItem(id))return;try{await patchItem(id,{manual:t,reviewed:true});toast(`Operator set P${t} — logged as model feedback`)}catch(error){toast(error.message)}}
function toggleNight(){document.body.classList.toggle('night');$('nightBtn').textContent=document.body.classList.contains('night')?'Day mode (N)':'Night mode (N)'}
const TNAME=['','Critical','High','Routine','Low'];
const RVKEYS={'1':'1','2':'2','3':'3','4':'4',' ':'accept','enter':'accept','x':'dismiss','delete':'dismiss','c':'cdr','arrowright':'skip','s':'skip','z':'undo','backspace':'undo'};
let rv={cur:null,shown:null,order:'pri',done:0,agree:0,undo:[],busy:false,skipped:{}};
function rvQueue(){const l=items.filter(i=>(state.src==='ALL'||i.src.id===state.src)&&!i.reviewed&&!i.dismissed);l.sort((a,b)=>(rv.skipped[a.id]||0)-(rv.skipped[b.id]||0)||(rv.order==='pri'?a.tier-b.tier||b.central-a.central:b.ts-a.ts));return l}
function rvProgress(){const left=rvQueue().length;$('rvDone').textContent=rv.done;$('rvLeft').textContent=left;$('rvBarI').style.width=(rv.done/Math.max(1,rv.done+left)*100)+'%';$('rvSrc').textContent='· '+(state.src==='ALL'?'all sources':SRC[state.src].name)+(rv.done?` · agreed with model on ${Math.round(rv.agree/rv.done*100)}%`:'');if(!rv.cur&&left&&!rv.busy)renderReview()}
function renderReview(){
 let it=rv.cur;if(!it||it.reviewed||it.dismissed||!items.includes(it))it=rv.cur=rvQueue()[0]||null;
 rvProgress();
 [...$('rvKeys').children].forEach(b=>{if(b.dataset.a&&b.dataset.a!=='undo')b.disabled=!it});
 if(!it){rv.shown=null;$('rvMain').innerHTML=`<div class="rvdone"><div class="ck">✓</div><b>All caught up</b><div>New images will appear here as sensors report.</div></div><div class="rvflash" id="rvFlash"></div>`;return}
 const key=it.id+':'+it.centralTier+':'+it.central;if(rv.shown===key)return;rv.shown=key;
 const sug=it.centralTier,rows=it.dets.map((d,i)=>[d,it.det[i]]).sort((a,b)=>b[1].s-a[1].s),why=buildWhy(it,rows).slice(0,4);
 const age=Math.max(0,Math.round((Date.now()-it.ts)/60000));
 [...$('rvKeys').querySelectorAll('[data-a]')].forEach(b=>b.classList.toggle('sugg',b.dataset.a===String(sug)));
 $('accLbl').textContent=`Agree · P${sug}`;
 $('rvMain').innerHTML=`<div class="rvimg"><div class="rvframe p${sug}"><img src="${imageFor(it)}">${boxesHTML(it,true)}</div></div>
 <div class="rvside">
  <div class="rvcard"><div class="lbl">Model suggests</div><div class="rvsug"><span class="tier t${sug}">P${sug}</span><b>${TNAME[sug]}</b></div><div class="rvsub">Command score ${it.central.toFixed(2)} · edge ${it.edge.toFixed(2)}${state.directives.length?` · ${state.directives.length} directive${state.directives.length>1?'s':''} active`:''}</div></div>
  <div class="rvcard"><div class="lbl" style="margin-bottom:4px">Why</div><ul class="why">${why.map(w=>`<li class="${w.c}">${w.t}</li>`).join('')}</ul></div>
  <div class="rvcard rvmeta"><span>Source</span><b>${it.src.name}</b><span>Captured</span><b class="mono">${zulu(it.ts)} <span style="color:var(--muted);font-family:inherit;font-weight:500">(${age} min ago)</span></b><span>Objects</span><b>${rows.map(([d])=>CLS[d.cls].lab).join(', ')||'—'}</b></div>
 </div><div class="rvflash" id="rvFlash"></div>`;
}
function rvFlash(msg,col){const f=$('rvFlash');if(!f)return;f.textContent=msg;f.style.color=col;f.classList.add('on');setTimeout(()=>f.classList.remove('on'),300)}
function rvPress(a){const b=$('rvKeys').querySelector(`[data-a="${a}"]`);if(!b)return;b.classList.add('pressed');setTimeout(()=>b.classList.remove('pressed'),140)}
function rvAct(a){
 if(rv.busy)return;rvPress(a);
 if(a==='undo'){const p=rv.undo[rv.undo.length-1];if(!p){toast('Nothing to undo');return}rv.busy=true;patchItem(p.id,{manual:p.manual,reviewed:p.reviewed,dismissed:p.dismissed,sent:p.sent}).then(it=>{rv.undo.pop();rv.done--;if(p.agreed)rv.agree--;rv.cur=it;rv.shown=null;rv.busy=false;renderReview();rvFlash('↶ Undone','#c3d1dd')}).catch(error=>{rv.busy=false;toast(error.message)});return}
 const it=rv.cur;if(!it)return;
 if(a==='skip'){rv.skipped[it.id]=Date.now();rv.cur=null;rv.shown=null;renderReview();return}
 const sug=it.centralTier,p={id:it.id,manual:it.manual,reviewed:it.reviewed,dismissed:it.dismissed,sent:it.sent,agreed:false};
 let msg,col;
 let changes={reviewed:true};
 if('1234'.includes(a)){changes.manual=+a;msg=`P${a} ${TNAME[+a]}`;col=tcol(+a);p.agreed=+a===sug}
 else if(a==='accept'){changes.manual=sug;msg=`✓ P${sug}`;col=tcol(sug);p.agreed=true}
 else if(a==='dismiss'){changes.manual=4;changes.dismissed=true;msg='✕ Not relevant';col='#9aa7b3'}
 else if(a==='cdr'){changes.manual=1;changes.sent=true;msg='➤ Sent to CDR';col='var(--good)'}
 else return;
 rv.busy=true;
 patchItem(it.id,changes).then(updated=>{rv.done++;if(p.agreed)rv.agree++;rv.undo.push(p);if(rv.undo.length>50)rv.undo.shift();rvFlash(msg,col);setTimeout(()=>{rv.busy=false;rv.cur=null;renderReview()},280)}).catch(error=>{rv.busy=false;toast(error.message)});
}
let tt;function toast(m){const t=$('toast');t.textContent=m;t.classList.add('on');clearTimeout(tt);tt=setTimeout(()=>t.classList.remove('on'),3200)}
init();
