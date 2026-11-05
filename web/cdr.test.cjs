const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');

const source=fs.readFileSync(path.join(__dirname,'static/cdr.js'),'utf8');

test('commander queue and note templates escape item ids',()=>{
 const elements=new Map();
 const context=vm.createContext({
  imageFor:()=>'/frame.jpg',
  zulu:()=> '12:00:00Z',
  setInterval:()=>0,
  document:{querySelector:()=>null,addEventListener(){}},
 });
 context.$=id=>{
  if(!elements.has(id))elements.set(id,{
   innerHTML:'',textContent:'',value:'',dataset:{},hidden:false,disabled:false,
   classList:{toggle(){},add(){},remove(){}},
   querySelector:()=>null,querySelectorAll:()=>[],scrollIntoView(){},addEventListener(){},
  });
  return elements.get(id);
 };
 vm.runInContext(source.replace(/init\(\)\.catch\(e=>toast\('Could not load configuration: '\+e\.message\)\);\s*$/,''),context);

 const id='sighting:x"><style>.row{display:none}</style>';
 context.testItem={
  id,src:{id:'EO-P7',name:'EO-P7',loc:'',em:'',link:''},sent:true,tier:1,central:.9,edge:.8,
  sentAt:new Date(),ts:new Date(),dets:[],det:[],frame:1,dbId:1,
 };
 vm.runInContext('items=new Map([[testItem.id,testItem]]);view.sel=testItem.id;renderQueue();renderStage()',context);

 const expected='data-id="sighting:x&quot;&gt;&lt;style&gt;';
 assert.ok(elements.get('qlist').innerHTML.includes(expected));
 assert.ok(elements.get('brief').innerHTML.includes(expected));
 assert.doesNotMatch(elements.get('qlist').innerHTML,/<style/);
 assert.doesNotMatch(elements.get('brief').innerHTML,/<style/);
});
