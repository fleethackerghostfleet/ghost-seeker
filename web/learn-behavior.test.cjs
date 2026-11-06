const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');

const filename=path.join(__dirname,'static/learn.js');
const script=new vm.Script(fs.readFileSync(filename,'utf8'),{filename});
function learner(){const context=vm.createContext({window:{}});script.runInContext(context);return context.window.gsLearn}
function record(l,id,{cls='small boat',src=id,tier=4,sug=3}={}){
 return l.record({id,src,tier,sug,dets:[{cls}],det:[{s:.9}]});
}
function seed(l,cls='small boat',tier=4){for(let i=0;i<3;i++)record(l,cls+i,{cls,tier,src:i%2?'IR':'EO'})}
const plain=value=>JSON.parse(JSON.stringify(value));

test('empty learner ignores absent detections and unknown attribution or undo IDs',()=>{
 const l=learner();
 for(const item of [null,undefined,{}, {id:'empty',dets:[]}])assert.equal(l.record(item),null);
 assert.equal(l.attribute('missing','small boat'),false);
 assert.equal(l.attribute('missing',null),false);
 assert.equal(l.forget('missing'),false);
 assert.equal(l.suggestion(),null);assert.equal(l.wrongCount(),0);
});

test('unattributing a known or wrong review removes it from suggestions without losing undo',()=>{
 const l=learner();seed(l);
 assert.equal(l.suggestion().n,3);
 assert.equal(l.attribute('small boat0',null),true);
 assert.equal(l.suggestion(),null);
 assert.equal(l.attribute('small boat0','wrong'),true);assert.equal(l.wrongCount(),1);
 assert.equal(l.attribute('small boat0',null),true);assert.equal(l.wrongCount(),0);
 assert.equal(l.suggestion(),null);
 assert.equal(l.attribute('small boat0','small boat'),true);
 assert.equal(l.suggestion().n,3);
 assert.equal(l.forget('small boat0'),true);assert.equal(l.forget('small boat0'),false);
 assert.equal(l.suggestion(),null);
});

test('forgetting the latest review removes its contribution and leaves other classes intact',()=>{
 const l=learner();seed(l);seed(l,'submarine',2);
 assert.equal(l.forget('small boat2'),true);
 const result=l.suggestion();
 assert.equal(result.cls,'submarine');assert.equal(result.dir,'raise');assert.equal(result.highRisk,true);
 assert.equal(l.attribute('small boat0','wrong'),true);
 assert.equal(l.wrongCount(),1);assert.equal(l.forget('small boat0'),true);assert.equal(l.wrongCount(),0);
 assert.deepEqual(plain(l.suggestion()),plain(result));
});

test('cooldowns are class-specific and duplicate updates or repeated attribution do not advance them',()=>{
 const l=learner();seed(l);
 l.resolve(l.suggestion(),'later');
 for(let i=0;i<8;i++){
  record(l,'small boat0',{src:'EO'});
  assert.equal(l.attribute('small boat1','small boat'),true);
  record(l,'other'+i,{cls:'bird',tier:3});
 }
 assert.equal(l.suggestion(),null);
 for(let i=0;i<2;i++)record(l,'new'+i,{src:i%2?'IR':'EO'});
 assert.equal(l.suggestion(),null);
 record(l,'third',{src:'IR'});
 assert.equal(l.suggestion().cls,'small boat');assert.equal(l.suggestion().n,6);
});

test('raising cooldown does not suppress a later lowering trend for the same class',()=>{
 const l=learner();seed(l,'submarine',2);
 const raised=l.suggestion();assert.equal(raised.dir,'raise');l.resolve(raised,'no');
 for(let i=0;i<7;i++)record(l,'lower'+i,{cls:'submarine',src:i%2?'IR':'EO'});
 const result=l.suggestion();
 assert.equal(result.cls,'submarine');assert.equal(result.dir,'lower');assert.equal(result.n,7);assert.equal(result.of,8);
 assert.equal(result.from,3);assert.equal(result.to,4);
});

test('invalid resolutions leave evidence unchanged and acceptance clears only the chosen class',()=>{
 const l=learner();seed(l);seed(l,'warship',2);
 const before=plain(l.suggestion());
 for(const suggestion of [null,{}, {cls:'small boat',dir:'invalid'}])l.resolve(suggestion,'yes');
 l.resolve(before,'unknown');assert.deepEqual(plain(l.suggestion()),before);
 l.resolve(before,'yes');
 assert.equal(l.suggestion().cls,'warship');assert.equal(l.suggestion().dir,'raise');
 assert.equal(l.attribute('small boat0','small boat'),false);
 assert.equal(l.forget('small boat0'),false);
});

test('agreement with tied scores is not ambiguous, while reranks without scores require attribution',()=>{
 const l=learner();
 const item={id:'ties',src:'EO',sug:3,tier:3,dets:[{cls:'small boat'},{cls:'warship'}]};
 assert.equal(l.record(item).ambiguous,null);
 item.tier=4;
 assert.deepEqual(Array.from(l.record(item).ambiguous),['small boat','warship']);
 assert.equal(l.suggestion(),null);
 assert.equal(l.attribute('ties','warship'),true);
 record(l,'second',{cls:'warship',src:'IR'});record(l,'third',{cls:'warship',src:'EO'});
 assert.equal(l.suggestion().cls,'warship');assert.equal(l.suggestion().n,3);
});

test('browser learners and returned suggestions do not share mutable history',()=>{
 const first=learner(),second=learner();seed(first);
 const suggestion=first.suggestion();suggestion.cls='mutated';suggestion.n=999;
 assert.equal(first.suggestion().cls,'small boat');assert.equal(first.suggestion().n,3);
 assert.equal(second.suggestion(),null);assert.equal(second.forget('small boat0'),false);
});
