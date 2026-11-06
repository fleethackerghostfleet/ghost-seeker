const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const {test}=require('node:test');
const {rgb,filter,contrast}=require('./contrast.cjs');
const read=name=>fs.readFileSync(path.join(__dirname,'static',name),'utf8');
const css=read('themes.css');
function declarations(source,selector){
 const rule=[...source.replace(/\/\*[\s\S]*?\*\//g,'').matchAll(/([^{}]+)\{([^{}]*)\}/g)].find(m=>m[1].trim()===selector);
 assert.ok(rule,`missing audited rule: ${selector}`);
 return Object.fromEntries(rule[2].split(';').filter(x=>x.includes(':')).map(x=>{const i=x.indexOf(':');return [x.slice(0,i),x.slice(i+1)];}));
}
function palette(theme,commander=false){
 let p=declarations(read(commander?'cdr.css':'style.css'),':root');
 if(theme==='day')p={...p,...declarations(read('style.css'),'body.theme-day'),...(commander?declarations(css,'body.commander.theme-day'):{}),...declarations(css,'body.theme-day,body.commander.theme-day')};
 if(theme==='red')p={...p,...declarations(css,commander?'.commander.theme-red,.theme-red #settings':'body.theme-red:not(.commander)')};
 return p;
}
test('filter matrices handle identity, black, white, grayscale and invalid operations',()=>{
 assert.deepEqual(filter(rgb('#123456'),''),rgb('#123456'));
 assert.deepEqual(filter(rgb('#fff'),'brightness(0)'),[0,0,0]);
 assert.deepEqual(filter(rgb('#000'),'contrast(0)'),[.5,.5,.5]);
 const c=filter(rgb('#f00'),'grayscale(1)');assert.ok(c.every(x=>Math.abs(x-.2126)<1e-6));
 assert.throws(()=>filter([1,1,1],'unknown(1)'),/Unsupported/);
 assert.equal(contrast(rgb('#000'),rgb('#fff')),21);
});
test('feed, detail, Review, Commander and Settings palette/selector pairs meet AA',()=>{
 const chain=declarations(css,'body')['--night-filter'];
 for(const theme of ['cic','day','red']){
  const samples=[];
  const add=(surface,label,fg,bg,filtered=false)=>samples.push({surface,label,ratio:contrast(filter(rgb(fg),filtered?chain:''),filter(rgb(bg),filtered?chain:''))});
  for(const surface of ['feed','detail','Review','Commander','Settings']){
   const direct=surface==='Commander'||surface==='Settings',p=palette(theme,direct&&surface==='Commander');
   if(theme==='red'&&surface==='Settings')Object.assign(p,declarations(css,'.commander.theme-red,.theme-red #settings'));
   const f=theme==='red'&&!direct;
   for(const fg of ['--text','--muted','--good'])for(const bg of ['--bg','--panel','--panel2',...(p['--panel3']?['--panel3']:[])])add(surface,`${fg} on ${bg}`,p[fg],p[bg],f);
   if(surface!=='Commander')add(surface,'links / export / What I sent',p['--human']||p['--accent'],p['--panel2'],f);
   if(surface==='Commander')for(let n=1;n<=4;n++)add(surface,`queue P${n} badge`,theme==='red'?(n<=2?'#000':p['--text']):theme==='day'||n===4?'#fff':'#0a0f14',theme==='red'?(n<=2?p['--p'+n]:'#000'):p['--p'+n]);
   if(surface==='feed'||surface==='detail'||surface==='Review'){
    for(let n=1;n<=4;n++){
     const fg=theme==='red'?(n<=2?'#000':p['--text']):theme==='day'?'#fff':n===4?'#e6edf3':'#0a0f14';
     const bg=theme==='red'?(n===1?p['--text']:n===2?'#444':'#000'):p['--p'+n];
     add(surface,`P${n} badge`,fg,bg,f);
    }
    add(surface,'image overlay',theme==='red'?p['--text']:'#cfdbe5','#000',f);
   }
  }
  const p=palette(theme);
  if(theme==='day')add('feed','paused status',declarations(css,'.theme-day button.live.paused').color,'#fbf3d9');
  if(theme!=='red')add('feed','primary button hover','#fff',declarations(css,`.theme-${theme} .btn.primary:hover`).background);
  add('Review','.rvcard a',p['--accent'],p['--panel'],theme==='red');
  if(theme==='red')add('detail','Send to Commander',p['--text'],p['--panel'],true);
  add('feed','banner',theme==='red'?p['--text']:'#fff',theme==='red'?'#000':'#007a33',theme==='red');
  // Suppressed cards retain dev's intentional opacity dimming; disabled controls
  // and the redundant pulsing status arrow are not normal-text samples.
  const failed=samples.filter(x=>x.ratio<4.5);
  console.log(`${theme}: ${samples.length} samples, ${failed.length} failing samples, lowest ${Math.min(...samples.map(x=>x.ratio)).toFixed(2)}:1`);
  assert.deepEqual(failed,[]);
 }
 assert.match(css,/\.rvcard a\{color:var\(--accent\)\}/);
 assert.equal(declarations(css,'.ov').background,'#000');
 assert.equal(declarations(css,'.theme-red .tier.t2').background,'#444!important');
 assert.equal(declarations(css,'.theme-red .tier:is(.t3,.t4)').background,'#000!important');
 assert.equal(declarations(css,'.theme-red .tier:is(.t3,.t4)').color,'var(--text)!important');
 assert.match(read('style.css'),/@keyframes sendpulse\{50%\{transform:translateY\(-2px\);opacity:\.75\}\}/);
 assert.match(read('style.css'),/\.card\.sup\{border-style:dashed;opacity:\.85\}/);
});
