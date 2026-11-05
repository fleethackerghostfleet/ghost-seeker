const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const {test}=require('node:test');

test('Feed, Review and Commander declare an embedded favicon instead of requesting missing favicon.ico',()=>{
 for(const name of ['index','cdr']){
  const html=fs.readFileSync(path.join(__dirname,'static',name+'.html'),'utf8');
  assert.match(html,/<link rel="icon" href="\/favicon.svg" type="image\/svg\+xml">/);
 }
 const icon=fs.readFileSync(path.join(__dirname,'static/favicon.svg'),'utf8');
 assert.match(icon,/<svg xmlns="http:\/\/www.w3.org\/2000\/svg"/);
});
