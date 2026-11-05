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
  fmtBytes:n=>`${n} B`,fmtRate:n=>`${n} bps`,ago:()=> 'now',
  LIVE:{shore:{ok:true,itemsTotal:2},edge:{ok:true,itemsTotal:3,itemsDeduped:1,queuedItems:1},link:{controllable:false},counts:{objects:2,objectsWithImage:1},policy:{},cameras:[]},
  visible:()=>({l:[]}),TNAME:['','Critical','High','Routine','Low'],
  toast:message=>{context.message=message},request:async()=>{}
 });
 vm.runInContext(source.slice(source.indexOf('const FORMS='),source.indexOf('function mergeLiveConfig')),context);
 vm.runInContext(source.slice(source.indexOf('function renderLiveStrip('),source.indexOf('/* ---- detail drawer ---- */')),context);
 return context;
}
test('simulation pipeline wraps groups instead of shrinking labels into each other',()=>{
 const css=fs.readFileSync(path.join(__dirname,'static/style.css'),'utf8');
 assert.match(css,/#simStrip\{[^}]*flex-wrap:wrap;[^}]*gap:4px 8px;[^}]*white-space:normal/);
 assert.match(css,/#simStrip>\.st\{[^}]*flex:0 0 auto;[^}]*flex-wrap:wrap;[^}]*max-width:100%/);
 assert.match(css,/#simStrip>\.ar\{display:none\}/);
 assert.match(css,/#simStrip>\.sv\{[^}]*flex:0 1 auto/);
});
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

test('automatic bandwidth shows one applied rate, no estimator readout and no manual controls',()=>{
 const c=setup();
 c.LIVE.link={controllable:true,bps:12500000,bytes:900,distribution:{minBps:0,maxBps:16000000,meanBps:4000000,stdDevBps:2431913.17,intervalMs:5000}};
 c.LIVE.edge.estimateBps=2000000;c.LIVE.edge.consecutiveFailures=2;
 vm.runInContext('renderLiveStrip()',c);
 let html=vm.runInContext("$('liveStrip').innerHTML",c);
 for(const text of ['Ship-to-shore connection</span> <b>12500000 bps</b>','<span class="hint">Changes every 5 s</span>','mean 4000000 bps','clipped to 0 bps–16000000 bps'])assert.ok(html.includes(text),text);
 assert.ok(!html.includes('Simulated · '),'no Simulated prefix beside the rate');
 assert.doesNotMatch(html,/estimat|2000000 bps|linkPresets|data-bps|<button/);
 c.LIVE.link.bps=0;
 vm.runInContext('renderLiveStrip()',c);
 html=vm.runInContext("$('liveStrip').innerHTML",c);
 assert.match(html,/class="st link down".*<b>0 bps<\/b>.*Link down — sending paused/);
 c.LIVE.link.bps=16000000;
 vm.runInContext('renderLiveStrip()',c);
 assert.match(vm.runInContext("$('liveStrip').innerHTML",c),/Ship-to-shore connection<\/span> <b>16000000 bps<\/b>/);
});

test('externally shaped links show the one measured rate without claiming automatic simulation',()=>{
 const c=setup();
 c.LIVE.edge.estimateBps=64000;
 vm.runInContext('renderLiveStrip()',c);
 const html=vm.runInContext("$('liveStrip').innerHTML",c);
 assert.match(html,/Ship-to-shore connection<\/span> <b>64000 bps<\/b>/);
 assert.match(html,/Measured by the ship/);
 assert.match(html,/controlled outside this console/);
 assert.doesNotMatch(html,/Simulated|linkPresets|data-bps/);
});

test('externally shaped links without a measurement yet say measuring, not link down',()=>{
 const c=setup();
 c.LIVE.edge.estimateBps=0;
 vm.runInContext('renderLiveStrip()',c);
 const html=vm.runInContext("$('liveStrip').innerHTML",c);
 assert.match(html,/class="st link".*<b>—<\/b>.*Measuring link speed…/);
 assert.doesNotMatch(html,/Link down|st link down/);
});

test('edge offline and shore reports the link down: stale shore estimate is not shown as a working link',()=>{
 const c=setup();
 c.LIVE.edge={ok:false};c.LIVE.shore.estimateBps=64000;c.LIVE.shore.linkUp=false;
 vm.runInContext('renderLiveStrip()',c);
 const html=vm.runInContext("$('liveStrip').innerHTML",c);
 assert.match(html,/class="st link down".*Link down — sending paused/);
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
