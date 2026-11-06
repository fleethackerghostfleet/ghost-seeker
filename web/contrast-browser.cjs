// Run separately: PLAYWRIGHT_MODULE=/path/to/playwright node web/contrast-browser.cjs [origin] [shots directory]
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const {rgb,filter,contrast}=require('./contrast.cjs');
const fs=require('node:fs');
const assert=require('node:assert/strict');
const origin=process.argv[2]||'http://127.0.0.1:8120',shots=process.argv[3];
const auditThemes=['cic','day'];
const themes=process.env.SCREENSHOT_ONLY_THEME?[process.env.SCREENSHOT_ONLY_THEME]:auditThemes;
(async()=>{
 const browser=await chromium.launch({headless:true});
 const results=[];
 try{
 for(const theme of themes){
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
    assert.equal(await page.locator('#hideUploaded').isChecked(),false,`${theme}: Hide uploaded starts unchecked`);
    console.log(`${theme}: Hide uploaded is unchecked`);
   }
   if(surface==='review'){await page.locator('#navRv').click();await page.waitForTimeout(300);}
   if(surface==='settings')await page.locator('#setBtn').click();
   if(shots&&surface==='feed'){fs.mkdirSync(shots,{recursive:true});await page.screenshot({path:`${shots}/${theme}-feed.png`,fullPage:true});}
   if(!auditThemes.includes(theme))continue;
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
  }
  await page.close();
 }
 }finally{await browser.close()}
 for(const theme of auditThemes){const rows=results.filter(x=>x.theme===theme);if(!rows.length)continue;const bad=rows.filter(x=>x.ratio+.001<x.threshold);console.log(`${theme}: ${rows.length} samples, ${bad.length} failing samples, lowest ${Math.min(...rows.map(x=>x.ratio)).toFixed(2)}:1`);console.log(JSON.stringify(bad,null,2));}
 if(process.env.AUDIT_JSON)fs.writeFileSync(process.env.AUDIT_JSON,JSON.stringify(results,null,2));
 process.exitCode=results.some(x=>x.ratio+.001<x.threshold)?1:0;
})().catch(e=>{console.error(e);process.exitCode=1});
