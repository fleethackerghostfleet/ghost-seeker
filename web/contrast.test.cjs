const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const {test}=require('node:test');
const read=file=>fs.readFileSync(path.join(__dirname,'static',file),'utf8');
const css=read('contrast.css'),svg=read('red-light.svg');
const matrix=svg.match(/values="([^"]+)"/)[1].trim().split(/\s+/).map(Number);
const rgb=hex=>hex.length===4?[...hex.slice(1)].map(c=>parseInt(c+c,16)):hex.slice(1).match(/../g).map(c=>parseInt(c,16));
const red=c=>[0,1,2].map(row=>matrix[row*5]*c[0]+matrix[row*5+1]*c[1]+matrix[row*5+2]*c[2]+matrix[row*5+4]*255);
const luminance=c=>c.map(v=>v/255).map(v=>v<=.04045?v/12.92:((v+.055)/1.055)**2.4).reduce((n,v,i)=>n+v*[.2126,.7152,.0722][i],0);
const contrast=(a,b)=>(Math.max(luminance(a),luminance(b))+.05)/(Math.min(luminance(a),luminance(b))+.05);
function palette(selector){return Object.fromEntries([...css.match(new RegExp(selector+'\\{([^}]+)'))[1].matchAll(/--([\w]+):(#[\da-f]+);?/g)].map(([,key,value])=>[key,rgb(value)]))}
test('both consoles load contrast rules and the red filter also covers top-layer dialogs',()=>{
 for(const file of ['index.html','cdr.html'])assert.match(read(file),/href="\/contrast.css"/);
 assert.match(read('cdr.html'),/<body class="commander">/);
 assert.match(css,/body\.night,body\.night dialog\{filter:url\('\/red-light.svg#red'\)\}/);
 assert.doesNotMatch(read('style.css'),/body\.night\{filter:/);
 assert.match(svg,/color-interpolation-filters="sRGB"/);
 assert.equal(matrix.length,20);
 assert.deepEqual(matrix.slice(15),[0,0,0,1,0]);
});
test('red UI tokens pass AA after the actual sRGB filter matrix, with no green or blue light',()=>{
 const p=palette('body\\.theme-red');
 for(const name of ['text','muted','accent','gold','good','p1','p2','p3','p4','human']){
  const fg=red(p[name]);
  assert.ok(fg[0]>0);assert.equal(fg[1],0);assert.equal(fg[2],0);
  assert.ok(luminance(fg)<=.2126+.00001,'red cannot emit white-level luminance');
  for(const bg of ['bg','panel','panel2','panel3'])assert.ok(contrast(fg,red(p[bg]))>=4.5,`${name} on ${bg}`);
 }
 assert.ok(contrast(red(p.text),[0,0,0])>=4.5,'solid red image labels need black ink');
 assert.deepEqual(red([0,0,0]),[0,0,0]);
 assert.deepEqual(red([255,0,0]).map(Math.round),[54,0,0]);
 assert.deepEqual(red([0,255,0]).map(Math.round),[182,0,0]);
 assert.deepEqual(red([0,0,255]).map(Math.round),[18,0,0]);
});
test('Day status, human review, priority and Dark export colours pass normal-text AA',()=>{
 const p=palette('body\\.theme-day');
 for(const bg of ['#ffffff','#e3f5ec','#e9eef2'])assert.ok(contrast(p.good,rgb(bg))>=4.5);
 assert.ok(contrast(p.human,rgb('#e2e8ee'))>=4.5);
 assert.ok(contrast(p.p1,rgb('#ffffff'))>=4.5);
 for(const key of ['p1','p2','p3','p4'])assert.ok(contrast([255,255,255],p[key])>=4.5,`white on ${key}`);
 assert.ok(contrast(rgb('#4fc3d9'),rgb('#0b1014'))>=4.5);
 assert.match(css,/\.rvcard a\{color:var\(--accent\)\}/);
});
test('secondary counts and solid image badges retain contrast instead of opacity dimming',()=>{
 assert.match(css,/\.seg button \.c,\.tabs span,\.btn\.primary kbd,\.cl span\{opacity:1!important\}/);
 assert.match(css,/\.theme-red :is\(\.box em,\.sendmark\)\{color:#000!important\}/);
 assert.match(css,/\.theme-red \.box em\{background:var\(--text\)!important\}/);
 assert.match(css,/\.card\.sup\{opacity:1\}/);
 assert.match(css,/\.theme-day \.rvnext kbd\{background:#eef2f5\}/);
 assert.ok(contrast(rgb('#0f1c26'),rgb('#eef2f5'))>=4.5);
 assert.ok(contrast(rgb('#e6edf3'),rgb('#5d6b75'))>=4.5);
 assert.match(read('style.css'),/@keyframes sendpulse\{50%\{transform:translateY\(-2px\)\}\}/);
});
test('Commander note placeholders and completed decisions use theme text colours',()=>{
 assert.match(css,/\.commander textarea::placeholder\{color:var\(--muted\);opacity:1\}/);
 assert.match(css,/\.commander\.theme-day \.decided b\{color:var\(--text\)\}/);
 assert.match(css,/\.commander\.theme-day \.pri \.big\{color:#fff\}/);
 assert.ok(contrast(rgb('#8392a8'),rgb('#111b2b'))>=4.5);
 assert.ok(contrast(rgb('#4c5f6d'),rgb('#f2f5f8'))>=4.5);
});
