/* Synthetic sensor imagery, shared by the watch-floor app and the commander view. */
const W=480,H=300;
const rand=(a,b)=>a+Math.random()*(b-a);
const pick=a=>a[Math.floor(Math.random()*a.length)];
const chance=p=>Math.random()<p;
const $=id=>document.getElementById(id);
const zulu=d=>d.toISOString().slice(11,19)+'Z';
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
function factPlaceholder(){
 const cv=document.createElement('canvas');cv.width=W;cv.height=H;const ctx=cv.getContext('2d');
 ctx.fillStyle='#0b1218';ctx.fillRect(0,0,W,H);
 ctx.strokeStyle='#2a3a48';ctx.lineWidth=4;ctx.setLineDash([18,12]);ctx.strokeRect(12,12,W-24,H-24);
 ctx.fillStyle='#8d99a5';ctx.textAlign='center';
 ctx.font=`bold ${Math.round(H/9)}px ui-monospace,monospace`;ctx.fillText('SUMMARY ONLY',W/2,H/2-H/20);
 ctx.font=`${Math.round(H/18)}px ui-monospace,monospace`;ctx.fillText('no image has crossed the link yet',W/2,H/2+H/12);
 return cv.toDataURL('image/png');
}
function imageFor(item){
 if(!imageCache.has(item.id))imageCache.set(item.id,item.img?`/${item.img}`:item.live?factPlaceholder():renderImage(item));
 return imageCache.get(item.id);
}
