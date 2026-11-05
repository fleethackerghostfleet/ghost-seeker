const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');
const source=fs.readFileSync(path.join(__dirname,'static/app.js'),'utf8');
test('feed-status button styles do not apply to the live-console body',()=>{
 const css=fs.readFileSync(path.join(__dirname,'static/style.css'),'utf8');
 assert.match(css,/button\.live\{font:inherit;display:flex/);
 assert.match(css,/body\.live \.fchips\{display:flex\}/);
 assert.doesNotMatch(css,/(?<![\w-])\.live(?=[{,:\s])/);
});
function setup(){
 const elements=new Map();
 const context=vm.createContext({
  $:id=>{if(!elements.has(id))elements.set(id,{});return elements.get(id)},
  esc:s=>String(s).replaceAll('&','&amp;').replaceAll('<','&lt;').replaceAll('"','&quot;'),
  fmtBytes:n=>`${n} B`,fmtRate:n=>`${n} bps`,presetName:n=>`${n} bps`,ago:()=> 'now',
  LIVE:{shore:{ok:true,itemsTotal:2},edge:{ok:true,itemsTotal:3,itemsDeduped:1,queuedItems:1},link:{controllable:false},counts:{objects:2,objectsWithImage:1},policy:{},cameras:[]},
  visible:()=>({l:[]}),TNAME:['','Critical','High','Routine','Low'],
  toast:message=>{context.message=message},request:async()=>{}
 });
 vm.runInContext(source.slice(source.indexOf('const FORMS='),source.indexOf('function mergeLiveConfig')),context);
 vm.runInContext(source.slice(source.indexOf('function renderLiveStrip('),source.indexOf('/* ---- detail drawer ---- */')),context);
 return context;
}
test('live connection and camera copy explain data flow and empty states',()=>{
 const c=setup();
 vm.runInContext('renderLiveStrip();renderCameras()',c);
 const html=vm.runInContext("$('liveStrip').innerHTML",c);
 for(const label of ['Ship-to-shore connection','On ship','On shore','detected objects','Data transferred','Ship score','P4 Low','save bandwidth'])assert.ok(html.includes(label),label);
 assert.equal(vm.runInContext("$('cameras').hidden",c),true);
 vm.runInContext("LIVE.cameras=[{name:'Mast <camera>',lastMs:0,contacts:0}];renderCameras()",c);
 assert.match(vm.runInContext("$('cameras').innerHTML",c),/Mast &lt;camera>/);
 assert.match(vm.runInContext("$('cameras').innerHTML",c),/no view yet/);
});
test('image versions explain summary-only, waiting, transfer and received states',()=>{
 const c=setup();
 c.item={live:{itemId:'test',forms:{fact:{state:'received',bytes:200},thumb:{state:'partial',received:10,bytes:20},crop:{state:'pending',bytes:40},full:{state:'offered',bytes:80}},reports:1}};
 const html=vm.runInContext('liveLadderHTML(item)',c);
 for(const label of ['Image versions','Summary','Preview','Object close-up','Full image','summary only','waiting on the ship','on the ship, not sent','Request full image'])assert.ok(html.includes(label),label);
 assert.equal(vm.runInContext('liveLadderHTML({})',c),'');
 assert.equal(vm.runInContext('formChipsHTML({})',c),'');
 assert.match(vm.runInContext('formChipsHTML(item)',c),/Preview 50%/);
});
test('live errors stay visible and escaped, and failed image requests retain their error',async()=>{
 const c=setup();
 vm.runInContext("LIVE.shore={ok:false,error:'<offline>'};renderLiveStrip()",c);
 assert.match(vm.runInContext("$('liveStrip').innerHTML",c),/&lt;offline>/);
 vm.runInContext("LIVE.cameras=[{name:'Mast',lastMs:0,contacts:0,view:{itemId:'test',lastMs:0,pull:{enabled:true,label:'Request full image',reason:'Stored on ship'}}}];renderCameras()",c);
 c.request=async()=>{throw new Error('Connection unavailable')};
 c.button={dataset:{pull:'test'},disabled:false};
 await vm.runInContext("$('cameras').onclick({target:{closest:()=>button}})",c);
 assert.equal(c.message,'Connection unavailable');
 assert.equal(vm.runInContext("pulling.has('test')",c),false);
});
