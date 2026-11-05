const assert=require('node:assert/strict');
const path=require('node:path');
const {execFileSync}=require('node:child_process');
const {test}=require('node:test');
const fs=require('node:fs');
const root=path.join(__dirname,'..');
for(const target of ['demo','demo-compare']){
 test(`${target} does not generate live-only ocean frames`,()=>{
  const plan=execFileSync('make',['-n',target],{cwd:root,encoding:'utf8'});
  assert.doesNotMatch(plan,/ocean\.py/);
  assert.match(plan,/compare\.sh/);
 });
}
for(const target of ['demo-clips','demo-edge']){
 test(`${target} generates ocean frames`,()=>{
  const plan=execFileSync('make',['-n',target],{cwd:root,encoding:'utf8'});
  assert.match(plan,/ocean\.py/);
 });
}
test('compose-demo prepares ocean frames before checking mounted clips',()=>{
 assert.match(fs.readFileSync(path.join(root,'Makefile'),'utf8'),/^compose-demo: demo-ocean/m);
});
