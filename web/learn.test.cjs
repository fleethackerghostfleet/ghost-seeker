const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');

const source=fs.readFileSync(path.join(__dirname,'static/learn.js'),'utf8');
let serial=0;
function learner(){
 const context=vm.createContext({});
 vm.runInContext(source,context);
 return vm.runInContext('createLearner()',context);
}
function add(l,id,{cls='small boat',dir=1,src=id}={}){
 return l.record({id,src,dets:[{cls}],det:[{s:.9}],sug:3,tier:dir>0?4:dir<0?2:3});
}
function addMany(l,count,{cls='small boat',dir=1,sources=['EO-P7','IR-HE']}={}){
 const ids=[];
 for(let i=0;i<count;i++){const id=`${cls}-${dir}-${serial++}`;ids.push(id);add(l,id,{cls,dir,src:sources[i%sources.length]})}
 return ids;
}

test('lowering must recur across two sources and meets the eight-record window thresholds',()=>{
 const across=learner();addMany(across,5);
 const result=across.suggestion();
 assert.equal(result.cls,'small boat');assert.equal(result.dir,'lower');assert.equal(result.n,5);assert.equal(result.of,5);

 const oneSource=learner();addMany(oneSource,5,{sources:['EO-P7']});
 assert.equal(oneSource.suggestion(),null);

 const four=learner();addMany(four,4);
 assert.equal(four.suggestion(),null);

 const belowRate=learner();addMany(belowRate,5);addMany(belowRate,3,{dir:0});
 assert.equal(belowRate.suggestion(),null);

 const aboveRate=learner();addMany(aboveRate,6);addMany(aboveRate,2,{dir:0});
 const sixOfEight=aboveRate.suggestion();
 assert.equal(sixOfEight.n,6);assert.equal(sixOfEight.of,8);
});

test('raising is suggested and high-risk classes are identified',()=>{
 const l=learner();addMany(l,5,{cls:'warship',dir:-1});
 const result=l.suggestion();
 assert.equal(result.dir,'raise');assert.equal(result.highRisk,true);
});

test('ambiguous contacts are ordered, unattributed, and can be attributed or marked wrong',()=>{
 const l=learner();
 const result=l.record({id:'amb',src:'EO-P7',dets:[{cls:'warship'},{cls:'small boat'},{cls:'cargo ship'},{cls:'submarine'}],det:[{s:1},{s:.92},{s:.9},{s:.84}],sug:3,tier:4});
 assert.equal(Array.from(result.ambiguous).join(','),'warship,small boat,cargo ship');
 addMany(l,4,{cls:'warship'});
 l.attribute('amb','warship');
 const attributed=l.suggestion();
 assert.equal(attributed.cls,'warship');assert.equal(attributed.n,5);assert.equal(attributed.of,5);

 const wrong=learner();
 wrong.record({id:'wrong',src:'EO-P7',dets:[{cls:'small boat'},{cls:'bird'}],det:[{s:1},{s:.9}],sug:3,tier:4});
 wrong.attribute('wrong','wrong');
 assert.equal(wrong.wrongCount(),1);
 addMany(wrong,5);
 assert.equal(wrong.suggestion().n,5);
 wrong.forget('wrong');
 assert.equal(wrong.wrongCount(),0);
});

test('agreement never opens a why prompt even for close scores',()=>{
 const l=learner();
 const result=l.record({id:'agree',src:'EO-P7',dets:[{cls:'small boat'},{cls:'warship'}],det:[{s:1},{s:.95}],sug:3,tier:3});
 assert.equal(result.ambiguous,null);
});

test('empty detections are ignored, duplicate ids replace, and the history stays capped',()=>{
 const l=learner();
 assert.equal(l.record({id:'empty',dets:[],det:[],sug:3,tier:4}),null);
 const ids=addMany(l,5);
 add(l,ids[0],{dir:0,src:'EO-P7'});
 assert.equal(l.suggestion(),null);

 const capped=learner();addMany(capped,5);addMany(capped,196,{cls:'warship',dir:0});
 assert.equal(capped.suggestion(),null);
});

test('no and later cooldowns count new attributed records; yes clears the class window',()=>{
 const rejected=learner();addMany(rejected,5);
 const rejectedSuggestion=rejected.suggestion();rejected.resolve(rejectedSuggestion,'no');
 assert.equal(rejected.suggestion(),null);
 addMany(rejected,7);assert.equal(rejected.suggestion(),null);
 add(rejected,'no-eighth',{src:'SAR-7'});
 assert.equal(rejected.suggestion().n,8);

 const later=learner();addMany(later,5);
 later.resolve(later.suggestion(),'later');
 addMany(later,2);assert.equal(later.suggestion(),null);
 add(later,'later-third',{src:'SAR-7'});
 assert.equal(later.suggestion().n,8);

 const accepted=learner();addMany(accepted,5);
 accepted.resolve(accepted.suggestion(),'yes');
 assert.equal(accepted.suggestion(),null);
 addMany(accepted,4);assert.equal(accepted.suggestion(),null);
 add(accepted,'yes-fifth',{src:'SAR-7'});
 assert.equal(accepted.suggestion().n,5);
});
