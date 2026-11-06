const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');
const read=name=>fs.readFileSync(path.join(__dirname,'static',name),'utf8');

test('each operator page owns a named main landmark and one visually hidden h1',()=>{
 const html=read('index.html');
 const pages=[...html.matchAll(/<section class="page(?: on)?" id="(pg\w+)"[^>]*>/g)];
 assert.deepEqual(pages.map(p=>p[1]),['pgFeed','pgRp','pgRv','pgDetails','pgBrief']);
 for(let i=0;i<pages.length;i++){
  assert.match(pages[i][0],/role="main"/);assert.match(pages[i][0],/aria-label="[^"]+"/);
  const content=html.slice(pages[i].index,pages[i+1]?.index||html.indexOf('<div class="cardmenu"'));
  assert.equal([...content.matchAll(/<h1\b/g)].length,1);
  assert.match(content,/<h1 class="sr-only">[^<]+<\/h1>/);
  assert.doesNotMatch(content,/<main\b|role="main"[^>]*>[\s\S]*role="main"/);
 }
 assert.match(html,/<div class="wb-results">/);
 assert.match(html,/<div id="briefPreview" role="region" aria-label="Briefing slide previews">/);
 assert.match(read('style.css'),/\.page\{[^}]*display:none/);
 assert.match(read('style.css'),/\.page\.on\{display:flex\}/);
});

test('Commander retains its main and uses the same nonvisual heading treatment as the feed',()=>{
 const html=read('cdr.html');assert.equal([...html.matchAll(/<main>/g)].length,1);
 assert.equal([...html.matchAll(/<h1\b/g)].length,1);assert.match(html,/<h1 class="sr-only">Commander View<\/h1>/);
 const hidden=name=>read(name).match(/\.sr-only\{([^}]+)\}/)[1];
 assert.equal(hidden('cdr.css'),hidden('style.css'));
 assert.match(hidden('cdr.css'),/position:absolute/);assert.match(hidden('cdr.css'),/clip-path:inset\(50%\)/);
 assert.doesNotMatch(hidden('cdr.css'),/display:none|visibility:hidden/);
 assert.match(read('cdr.js'),/<h2>Why it reached/);assert.doesNotMatch(read('cdr.js'),/<\/?h4\b/);
 assert.match(read('cdr.css'),/h2\{margin:20px 0 8px;font-size:0\.6875rem/);
});

test('classification banners and panel resizing controls stay in named landmarks without selector-breaking wrappers',()=>{
 for(const name of ['index.html','cdr.html']){
  const html=read(name),banners=[...html.matchAll(/<div class="banner"[^>]*>/g)];assert.equal(banners.length,2);
  assert.match(banners[0][0],/role="region" aria-label="Classification marking"/);
  assert.match(banners[1][0],/role="contentinfo" aria-label="Classification marking"/);
  assert.match(html,/<div id="panelResizers" class="panel-resizers" role="region" aria-label="Panel resizing controls">/);
 }
 assert.match(read('text-size.css'),/\.panel-resizers\{display:contents\}/);
 const handles=[],region={append:node=>handles.push(node)};
 const context=vm.createContext({innerWidth:1440,requestAnimationFrame(){},window:{gsSettings:{get:()=>({bar:'top'})},addEventListener(){}},
  document:{body:{append(){assert.fail('handles must be inside the named resizing landmark')}},getElementById:id=>id==='panelResizers'?region:null,
   querySelector:()=>null,createElement:()=>({attrs:{},setAttribute(k,v){this.attrs[k]=v},addEventListener(){}}),addEventListener(){}},
  ResizeObserver:class{observe(){}disconnect(){}},MutationObserver:class{observe(){}}
 });
 vm.runInContext(read('resize.js'),context);
 assert.equal(handles.length,7);assert(handles.every(h=>h.attrs.role==='separator'&&h.hidden));
});

test('drawer subsections follow its h2 with h3 headings while retaining the old heading style',()=>{
 const app=read('app.js');assert.match(app,/<div class="dhead"><div><h2>/);
 for(const title of ['Detections','Why this priority','Processing chain','Shore upload queue'])assert(app.includes('<h3>'+title+'</h3>'));
 for(const name of ['app.js','lights.js','briefing.js'])assert.doesNotMatch(read(name),/<\/?h4>|createElement\('h4'\)/);
 assert.match(read('briefing.js'),/createElement\('h3'\)/);
 assert.match(read('style.css'),/\.dsec h3\{margin:0 0 6px;font-size:0\.65625rem/);
 assert.doesNotMatch(read('style.css'),/\.dsec h4/);
});
