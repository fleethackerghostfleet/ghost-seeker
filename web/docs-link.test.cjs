const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const {test}=require('node:test');

const shared=fs.readFileSync(path.join(__dirname,'../internal/httpx/docs.go'),'utf8');
const link=shared.match(/const DocsLinkHTML = `([^`]+)`/)[1];
const css=shared.match(/const DocsLinkCSS = `([^`]+)`/)[1];

test('every operator HTML header has the same accessible, native documentation link',()=>{
 const pages=fs.readdirSync(path.join(__dirname,'static')).filter(name=>name.endsWith('.html')).map(name=>path.join(__dirname,'static',name));
 pages.push(path.join(__dirname,'../ui/index.html'));
 for(const file of pages){
  const html=fs.readFileSync(file,'utf8'),headers=[...html.matchAll(/<header\b[^>]*>([\s\S]*?)<\/header>/g)];
  assert.ok(headers.length,file);
  for(const [,header] of headers)assert.equal(header.split(link).length-1,1,file);
 }
 assert.match(link,/href="https:\/\/ghostfleet-docs\.vercel\.app"/);
 assert.match(link,/target="_blank" rel="noopener noreferrer"/);
 for(const attribute of ['aria-label','title'])assert.ok(link.includes(attribute+'="Open documentation (new tab)"'));
 assert.match(link,/<svg[^>]*stroke="currentColor"[^>]*aria-hidden="true"[^>]*focusable="false"/);
 assert.doesNotMatch(link,/\b(?:onclick|onkeydown|accesskey|tabindex)=/);
});

test('operator and ship consoles share the documentation icon styling',()=>{
 assert.ok(fs.readFileSync(path.join(__dirname,'static/auth.css'),'utf8').includes(css));
 assert.ok(fs.readFileSync(path.join(__dirname,'../ui/index.html'),'utf8').includes(css));
 for(const name of ['index','cdr'])assert.match(fs.readFileSync(path.join(__dirname,'static',name+'.html'),'utf8'),/href="\/auth.css"/);
});
