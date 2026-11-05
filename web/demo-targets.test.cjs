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
for(const target of ['demo-clips','demo-edge','compose-demo']){
 test(`${target} does not generate ocean frames by default`,()=>{
  const plan=execFileSync('make',['-n',target],{cwd:root,encoding:'utf8'});
  assert.doesNotMatch(plan,/ocean\.py/);
 });
}
test('demo-clips explicitly opts into ocean frames',()=>{
 const plan=execFileSync('make',['-n','demo-clips','INCLUDE_OCEAN_SCENES=1'],{cwd:root,encoding:'utf8'});
 assert.match(plan,/ocean\.py/);
});
test('base compose is secure and presenter conveniences require the local override',()=>{
 const base=fs.readFileSync(path.join(root,'deploy/compose/demo.compose.yml'),'utf8');
 const local=fs.readFileSync(path.join(root,'deploy/compose/demo.local.compose.yml'),'utf8');
 for(const flag of ['-dev-trust-peers','-demo-accept-any-token','-demo-link-control']){
  assert.ok(!base.includes(flag));assert.ok(local.includes(flag));
 }
 assert.ok(base.includes('${GS_DEMO_SCENARIO:-scenario.json}'));
});
