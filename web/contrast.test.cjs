const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const {test}=require('node:test');
const {rgb,filter,contrast}=require('./contrast.cjs');
const read=name=>fs.readFileSync(path.join(__dirname,'static',name),'utf8');
const css=read('themes.css');
const DEV_RED_SELECTORS=new Set([
 '.commander.theme-red,.theme-red #settings',
 '.commander.night,.theme-red #settings',
 '.theme-red #settings .btn.primary',
 '.theme-red #settings .btn:hover',
 '.theme-red #settings .btn.primary:hover',
 '.theme-red #settings input',
 '.theme-red #settings input:checked::before',
 '.theme-red #settings :focus-visible',
 '.commander.theme-red :is(header,.live,.row.on,#learned,kbd,.bar,.toast,.av)',
 '.commander.theme-red #learned',
 '.commander.theme-red .read-only-badge',
 '.commander.theme-red .bar i',
 '.commander.theme-red :is(.banner,.tg,.box em,.pri .big)',
 '.commander.theme-red :is(.box.lo,.why li,.chain,.dets td,.tabs button.on,.wf:hover)',
 '.commander.theme-red [style*="--c:"]',
 '.commander.theme-red :is(.live i,.row.fresh)',
 '.commander.theme-red .toast',
 '.commander.theme-red textarea::placeholder',
 '.commander.theme-red ::-webkit-scrollbar-thumb'
]);
const NON_RED_SCOPED_SELECTORS=new Set([
 'body:not(.theme-red) .rvcard a',
 '.commander:not(.theme-red) textarea::placeholder',
 'body:not(.theme-red) :is(.seg button .c,.tabs span,.btn.primary kbd,.cl span)',
 'body:not(.theme-red) .ov'
]);
function ruleValue(source,selector){
 const rule=[...source.replace(/\/\*[\s\S]*?\*\//g,'').matchAll(/([^{}]+)\{([^{}]*)\}/g)].find(m=>m[1].trim()===selector);
 assert.ok(rule,`missing audited rule: ${selector}`);
 return rule[2].trim();
}
function selectors(source){
 return [...source.replace(/\/\*[\s\S]*?\*\//g,'').matchAll(/([^{}]+)\{([^{}]*)\}/g)].map(m=>m[1].trim());
}
function declarations(source,selector){
 const rule=[...source.replace(/\/\*[\s\S]*?\*\//g,'').matchAll(/([^{}]+)\{([^{}]*)\}/g)].find(m=>m[1].trim()===selector);
 assert.ok(rule,`missing audited rule: ${selector}`);
 return Object.fromEntries(rule[2].split(';').filter(x=>x.includes(':')).map(x=>{const i=x.indexOf(':');return [x.slice(0,i),x.slice(i+1)];}));
}
function palette(theme,commander=false){
 let p=declarations(read(commander?'cdr.css':'style.css'),':root');
 if(theme==='day')p={...p,...declarations(read('style.css'),'body.theme-day'),...(commander?declarations(css,'body.commander.theme-day'):{}),...declarations(css,'body.theme-day,body.commander.theme-day')};
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
 for(const theme of ['cic','day']){
  const samples=[];
  const add=(surface,label,fg,bg)=>samples.push({surface,label,ratio:contrast(rgb(fg),rgb(bg))});
  for(const surface of ['feed','detail','Review','Commander','Settings']){
   const direct=surface==='Commander'||surface==='Settings',p=palette(theme,direct&&surface==='Commander');
   for(const fg of ['--text','--muted','--good'])for(const bg of ['--bg','--panel','--panel2',...(p['--panel3']?['--panel3']:[])])add(surface,`${fg} on ${bg}`,p[fg],p[bg]);
   if(surface!=='Commander')add(surface,'links / export / What I sent',p['--human']||p['--accent'],p['--panel2']);
   if(surface==='Commander')for(let n=1;n<=4;n++)add(surface,`queue P${n} badge`,theme==='day'||n===4?'#fff':'#0a0f14',p['--p'+n]);
   if(surface==='feed'||surface==='detail'||surface==='Review'){
    for(let n=1;n<=4;n++){
     const fg=theme==='day'?'#fff':n===4?'#e6edf3':'#0a0f14';
     add(surface,`P${n} badge`,fg,p['--p'+n]);
    }
    // Captions sit on imagery, so a flat background does not represent their actual contrast.
   }
  }
  const p=palette(theme);
  if(theme==='day')add('feed','paused status',declarations(css,'.theme-day button.live.paused').color,'#fbf3d9');
  add('feed','primary button hover','#fff',declarations(css,`.theme-${theme} .btn.primary:hover`).background);
  add('Review','.rvcard a',p['--accent'],p['--panel']);
  add('feed','banner','#fff','#007a33');
  // Suppressed cards retain dev's intentional opacity dimming; disabled controls
  // and the redundant pulsing status arrow are not normal-text samples.
  const failed=samples.filter(x=>x.ratio<4.5);
  console.log(`${theme}: ${samples.length} samples, ${failed.length} failing samples, lowest ${Math.min(...samples.map(x=>x.ratio)).toFixed(2)}:1`);
  assert.deepEqual(failed,[]);
 }
 assert.match(read('style.css'),/@keyframes sendpulse\{50%\{transform:translateY\(-2px\);opacity:\.75\}\}/);
 assert.match(read('style.css'),/\.card\.sup\{border-style:dashed;opacity:\.85\}/);
});
test('red-light is unchanged from dev',()=>{
 assert.equal(declarations(css,'body')['--night-filter'],'grayscale(1) sepia(1) hue-rotate(-50deg) saturate(5) brightness(.62) contrast(1.1)');
 assert.equal(ruleValue(css,'.commander.theme-red,.theme-red #settings'),'--bg:#090000;--panel:#160000;--panel2:#210000;--panel3:#300000;--line:#650000;--text:#e00000;--muted:#b00000;--accent:#d00000;--accent2:#680000;--gold:#cc0000;--good:#c00000;--p1:#e00000;--p2:#c00000;--p3:#a00000;--p4:#800000;color-scheme:dark;accent-color:var(--accent)');
 const redSelectors=new Set(selectors(css).filter(s=>s.includes('.theme-red')&&!NON_RED_SCOPED_SELECTORS.has(s)));
 assert.deepEqual(redSelectors,DEV_RED_SELECTORS);
});
