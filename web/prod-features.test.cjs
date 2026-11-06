// Prod contract: demo-only console controls are unreachable unless the server
// reports them in /api/config "features". Run by scripts/prod-gate.sh.
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');
const read=name=>fs.readFileSync(path.join(__dirname,'static',name),'utf8');
const app=read('app.js');
const start=app.indexOf('function gsFeatures(');
const end=app.indexOf('\n}\n',app.indexOf('function setupDemoReset('))+3;
const DEMO_IDS=['resetDemoBtn','resetDemoDialog','resetDemoCancel','resetDemoConfirm','datasetStrip','simStrip'];

function setup(){
 const elements=new Map(),requests=[],simOnly=[{hidden:false},{hidden:false}],body=new Set();
 for(const id of DEMO_IDS)elements.set(id,{id,hidden:id!=='simStrip',removed:false,remove(){this.removed=true;elements.delete(id)},showModal(){},close(){},focus(){}});
 const context=vm.createContext({
  $:id=>elements.get(id)||null,
  document:{body:{classList:{toggle:(name,on)=>on?body.add(name):body.delete(name)}},querySelectorAll:sel=>sel==='.sim-only'?simOnly:[]},
  request:async(url,method)=>{requests.push([method,url])},toast(){},
 });
 vm.runInContext(app.slice(start,end),context);
 const run=config=>vm.runInContext('(c=>{const f=gsFeatures(c);applyFeatures(f);setupDemoReset(f.demoReset);return f})',context)(config);
 return {elements,requests,simOnly,body,run};
}

for(const [name,config] of [
 ['prod build (all features false)',{live:true,features:{profile:'prod',simulation:false,demoReset:false,dataset:false,demoLinkControl:false}}],
 ['live demo build',{live:true,features:{profile:'demo',simulation:false,demoReset:false,dataset:false}}],
 ['server without a features block',{live:false,demoReset:true,datasetMode:true}],
]){
 test(`TestProdContract demo controls unreachable: ${name}`,async()=>{
  const t=setup();
  const f=t.run(config);
  assert.equal(f.demoReset,false);assert.equal(f.simulation,false);assert.equal(f.dataset,false);
  for(const id of ['resetDemoBtn','resetDemoDialog','datasetStrip'])assert.equal(t.elements.has(id),false,`${id} must be removed`);
  assert.equal(t.elements.get('simStrip').hidden,true);
  assert.ok(t.simOnly.every(el=>el.hidden),'sim-only labels hidden');
  assert.ok(t.body.has('no-sim'));
  assert.equal(t.elements.get('resetDemoConfirm').onclick,undefined,'reset confirm handler must not be wired');
  assert.deepEqual(t.requests,[]);
 });
}

test('demo simulation build keeps the reset demo control',async()=>{
 const t=setup();
 t.run({features:{profile:'demo',simulation:true,demoReset:true,dataset:false}});
 assert.equal(t.elements.get('resetDemoBtn').hidden,false);
 assert.equal(t.elements.get('simStrip').hidden,false);
 assert.ok(!t.body.has('no-sim'));
 await t.elements.get('resetDemoConfirm').onclick();
 assert.deepEqual(t.requests,[['POST','/api/demo/reset']]);
});

test('TestProdContract demo controls are only reachable through the feature gate',()=>{
 const html=read('index.html');
 for(const id of DEMO_IDS)assert.match(html,new RegExp(`id="${id}"`),`${id} still in index.html; update this test if it moves`);
 const outside=app.slice(0,start)+app.slice(end);
 assert.doesNotMatch(outside,/api\/demo\//,'demo API calls must live in setupDemoReset');
 assert.doesNotMatch(outside,/resetDemo(Btn|Dialog|Confirm)/,'reset controls must only be wired by setupDemoReset');
 assert.doesNotMatch(app,/config\.(demoReset|datasetMode)/,'read demo capabilities via gsFeatures(config)');
 assert.doesNotMatch(app,/api\/shore\/link/,'no UI for the presenter link override');
 const init=app.slice(app.indexOf('async function init('));
 assert.ok(init.indexOf('applyFeatures(')<init.indexOf('setupDemoReset('));
 assert.match(init,/setupDemoReset\(features\.demoReset\)/);
 assert.match(init,/DATASET_MODE=features\.dataset/);
 const cdr=read('cdr.js');
 assert.doesNotMatch(cdr,/c\.datasetMode/);
 assert.match(cdr,/datasetMode=c\.features\?\.dataset===true/);
});
