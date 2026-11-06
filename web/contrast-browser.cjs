// Run separately: PLAYWRIGHT_MODULE=/path/to/playwright node web/contrast-browser.cjs [origin] [shots directory]
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const {rgb,filter,contrast}=require('./contrast.cjs');
const fs=require('node:fs');
const assert=require('node:assert/strict');
const origin=process.argv[2]||'http://127.0.0.1:8120',shots=process.argv[3];
(async()=>{
 const browser=await chromium.launch({headless:true});
 const results=[];
 const tiers=[];
 try{
 for(const theme of ['cic','day','red']){
  const page=await browser.newPage({viewport:{width:1920,height:1200}});
  await page.addInitScript(t=>localStorage.setItem('gs.settings',JSON.stringify({theme:t})),theme);
  for(const surface of ['feed','detail','review','settings','commander']){
   await page.goto(origin+(surface==='commander'?'/cdr':'/'));
   await page.waitForTimeout(1500);
   if(surface!=='commander')await page.locator('#pauseBtn').click();
   if(surface==='detail')await page.locator('.card:not(.sup)').first().click();
   if(surface==='feed'){
    await page.locator('#sortSeg [data-s="new"]').click();
    await page.waitForTimeout(300);
    // Reorder real cards only, so all four priority treatments share the shot.
    await page.evaluate(()=>{const grid=document.querySelector('.grid');if(grid)for(const n of [4,3,2,1]){const card=grid.querySelector('.card.p'+n+':not(.sup)');if(!card)throw Error('Missing P'+n+' screenshot sample');grid.prepend(card);}});
   }
   if(surface==='review'){await page.locator('#navRv').click();await page.waitForTimeout(300);}
   if(surface==='settings')await page.locator('#setBtn').click();
   if(theme==='red'&&['feed','commander'].includes(surface)){
    const marks=await page.evaluate(commander=>{
     const host=document.createElement('div');document.body.append(host);
     const rows=[];
     for(const type of commander?['tg','big']:['tier'])for(let n=1;n<=4;n++){
      host.innerHTML=type==='big'?`<div class="pri" style="--c:var(--p${n})"><span class="big">P${n}</span></div>`:`<span class="${type==='tier'?'tier t'+n:'tg'}" style="background:var(--p${n})">P${n}</span>`;
      const el=host.querySelector('span'),s=getComputedStyle(el);
      rows.push({type,n,fg:s.color,bg:s.backgroundColor,border:s.borderTopColor,width:parseFloat(s.borderTopWidth),weight:Number(s.fontWeight)});
     }
     host.remove();return rows;
    },surface==='commander');
    const chain=surface==='feed'?await page.evaluate(()=>getComputedStyle(document.body).filter):'';
    for(const mark of marks){
     const fg=filter(rgb(mark.fg),chain),bg=filter(rgb(mark.bg),chain),border=filter(rgb(mark.border),chain);
     assert.ok(contrast(fg,bg)>=4.5,JSON.stringify(mark));
     assert.ok(contrast(border,[0,0,0])>=3,JSON.stringify(mark));
     assert.equal(mark.weight,[0,900,800,600,400][mark.n]);
     assert.equal(mark.width,[0,3,2,2,1][mark.n]);
     if(mark.n<=2){assert.deepEqual(fg,[0,0,0]);assert.ok(bg[0]>.93);}
     else{assert.deepEqual(bg,[0,0,0]);assert.ok(fg[0]>.99);}
     tiers.push({...mark,surface,fg:fg.map(x=>Math.round(x*255)),bg:bg.map(x=>Math.round(x*255)),border:border.map(x=>Math.round(x*255)),ratio:contrast(fg,bg)});
    }
    if(surface==='feed'){
     const stripes=await page.evaluate(()=>[1,2,3,4].map(n=>{const s=getComputedStyle(document.querySelector('.card.p'+n),'::after');return {n,bg:s.backgroundColor,height:parseFloat(s.height)};}));
     for(const stripe of stripes){
      const color=filter(rgb(stripe.bg),chain),ratio=contrast(color,[0,0,0]);
      assert.equal(stripe.height,[0,9,6,3,1][stripe.n]);assert.ok(ratio>=3,JSON.stringify(stripe));
      tiers.push({...stripe,type:'stripe',surface,bg:color.map(x=>Math.round(x*255)),ratio});
     }
    }
   }
   const samples=await page.evaluate(()=>{
    const out=[];
    for(const el of document.querySelectorAll('body *')){
     // Suppressed cards and disabled controls are intentionally de-emphasized.
     if(el.closest('.card.sup,[disabled],.policy-blocked'))continue;
     // The pulsing arrow is a redundant animated status icon, not a text label.
     if(el.matches('.sendmark.sending'))continue;
     if(![...el.childNodes].some(n=>n.nodeType===3&&n.textContent.trim()))continue;
     const s=getComputedStyle(el),r=el.getBoundingClientRect();
     if(!r.width||!r.height||s.visibility==='hidden'||el.closest('[hidden]'))continue;
     if(document.querySelector('dialog[open]')&&!el.closest('dialog[open]'))continue;
     let bg=[0,0,0],chain=[],opacity=1;
     const ancestors=[];
     for(let a=el;a;a=a.parentElement){ancestors.unshift(a);if(a.matches('dialog[open]'))break;}
     for(const a of ancestors){const z=getComputedStyle(a),v=z.backgroundColor.match(/[\d.]+/g).map(Number),alpha=v[3]??1;bg=bg.map((x,i)=>x*(1-alpha)+v[i]*alpha);opacity*=Number(z.opacity);if(z.filter!=='none')chain.unshift(z.filter);}
     const fg=s.color.match(/[\d.]+/g).slice(0,3).map(Number).map((v,i)=>v*opacity+bg[i]*(1-opacity));
     out.push({selector:el.tagName.toLowerCase()+(el.id?'#'+el.id:'')+'.'+[...el.classList].join('.'),text:el.textContent.trim().slice(0,45),fg:`rgb(${fg})`,bg:`rgb(${bg})`,chain:chain.join(' '),threshold:parseFloat(s.fontSize)>=24||(parseFloat(s.fontSize)>=18.66&&Number(s.fontWeight)>=700)?3:4.5});
    }
    return out;
   });
   for(const s of samples){s.theme=theme;s.surface=surface;s.ratio=contrast(filter(rgb(s.fg),s.chain),filter(rgb(s.bg),s.chain));results.push(s);}
   if(shots&&['feed','settings','commander'].includes(surface)){fs.mkdirSync(shots,{recursive:true});await page.screenshot({path:`${shots}/${theme}-${surface}.png`,fullPage:true});}
  }
  await page.close();
 }
 }finally{await browser.close()}
 console.log(`Rendered red tier treatments: ${tiers.length} samples, 0 failing samples`);
 if(process.env.AUDIT_JSON)fs.writeFileSync(process.env.AUDIT_JSON.replace(/\.json$/,'.tiers.json'),JSON.stringify(tiers,null,2));
 for(const theme of ['cic','day','red']){const rows=results.filter(x=>x.theme===theme),bad=rows.filter(x=>x.ratio+.001<x.threshold);console.log(`${theme}: ${rows.length} samples, ${bad.length} failing samples, lowest ${Math.min(...rows.map(x=>x.ratio)).toFixed(2)}:1`);console.log(JSON.stringify(bad,null,2));}
 if(process.env.AUDIT_JSON)fs.writeFileSync(process.env.AUDIT_JSON,JSON.stringify(results,null,2));
 process.exitCode=results.some(x=>x.ratio+.001<x.threshold)?1:0;
})().catch(e=>{console.error(e);process.exitCode=1});
