const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const {test}=require('node:test');

test('Feed, Review and Commander declare an embedded favicon instead of requesting missing favicon.ico',()=>{
 for(const name of ['index','cdr']){
  const html=fs.readFileSync(path.join(__dirname,'static',name+'.html'),'utf8');
  assert.match(html,/<link rel="icon" type="image\/png" sizes="32x32" href="\/brand\/favicon-32.png">/);
 }
 const icon=fs.readFileSync(path.join(__dirname,'static/brand/favicon-32.png'));
 assert.deepEqual([...icon.subarray(0,8)],[0x89,0x50,0x4e,0x47,0x0d,0x0a,0x1a,0x0a]);
});
