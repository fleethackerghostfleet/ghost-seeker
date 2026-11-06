const clamp=x=>Math.max(0,Math.min(1,x));
const rgb=s=>s.startsWith('#')?(s.length===4?[...s.slice(1)].map(x=>parseInt(x+x,16)):s.slice(1).match(/../g).map(x=>parseInt(x,16))).map(x=>x/255):s.match(/[\d.]+/g).slice(0,3).map(Number).map(x=>x/255);
function filter(color,chain){
 let c=[...color];
 for(const [,name,arg] of chain.matchAll(/([\w-]+)\(([^)]+)\)/g)){
  const n=parseFloat(arg),t=arg.includes('%')?n/100:n;
  let m;
  if(name==='grayscale')m=[.2126+.7874*(1-t),.7152*t,.0722*t,.2126*t,.7152+.2848*(1-t),.0722*t,.2126*t,.7152*t,.0722+.9278*(1-t)];
  if(name==='sepia')m=[1-.607*t,.769*t,.189*t,.349*t,1-.314*t,.168*t,.272*t,.534*t,1-.869*t];
  if(name==='saturate')m=[.213+.787*t,.715-.715*t,.072-.072*t,.213-.213*t,.715+.285*t,.072-.072*t,.213-.213*t,.715-.715*t,.072+.928*t];
  if(name==='hue-rotate'){
   const a=n*Math.PI/180,C=Math.cos(a),S=Math.sin(a);
   m=[.213+.787*C-.213*S,.715-.715*C-.715*S,.072-.072*C+.928*S,.213-.213*C+.143*S,.715+.285*C+.140*S,.072-.072*C-.283*S,.213-.213*C-.787*S,.715-.715*C+.715*S,.072+.928*C+.072*S];
  }
  if(m)c=[0,1,2].map(i=>clamp(m[i*3]*c[0]+m[i*3+1]*c[1]+m[i*3+2]*c[2]));
  else if(name==='brightness')c=c.map(x=>clamp(x*t));
  else if(name==='contrast')c=c.map(x=>clamp((x-.5)*t+.5));
  else throw Error('Unsupported filter '+name);
 }
 return c;
}
const luminance=c=>c.map(x=>x<=.04045?x/12.92:((x+.055)/1.055)**2.4).reduce((a,x,i)=>a+x*[.2126,.7152,.0722][i],0);
const contrast=(a,b)=>(Math.max(luminance(a),luminance(b))+.05)/(Math.min(luminance(a),luminance(b))+.05);
module.exports={rgb,filter,contrast};
