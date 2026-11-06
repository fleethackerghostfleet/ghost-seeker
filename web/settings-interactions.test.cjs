const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');

function classes(initial=[]){const values=new Set(initial);return {add:(...xs)=>xs.forEach(x=>values.add(x)),remove:(...xs)=>xs.forEach(x=>values.delete(x)),contains:x=>values.has(x),toggle(x,on){if(on)values.add(x);else values.delete(x)},[Symbol.iterator]:()=>values.values()}}
function style(){return {setProperty(k,v){this[k]=v},removeProperty(k){delete this[k]}}}
function node(dataset={},parentElement=null){return {dataset,parentElement,classList:classes(),closest(selector){if(selector==='.seg[data-k] button'&&this.dataset.v&&this.parentElement?.dataset.k)return this;return this.parentElement?.closest?.(selector)||null}}}
function setup(saved=null,{readThrows=false,writeThrows=false}={}){
 const ids=new Map(),groups=new Map(),inputs=new Map(),stored=new Map(saved===null?[]:[['gs.settings',saved]]),events=[];
 let attempts=0,layoutCalls=0;
 const dialog={open:false,showModal(){this.open=true},close(){this.open=false},
  set innerHTML(html){
   this.markup=html;
   for(const [,key,content] of html.matchAll(/<div class="seg" data-k="([^"]+)">([\s\S]*?)<\/div>/g)){
    const group=node({k:key});group.buttons=[];
    for(const [,value,label] of content.matchAll(/<button[^>]*data-v="([^"]+)"[^>]*>([^<]+)<\/button>/g)){const button=node({v:value},group);button.textContent=label;group.buttons.push(button)}
    groups.set(key,group);
   }
   for(const [,part] of html.matchAll(/<input[^>]*data-p="([^"]+)"[^>]*>/g))inputs.set(part,node({p:part}));
   for(const [,id] of html.matchAll(/<button[^>]*id="([^"]+)"[^>]*>/g))ids.set(id,node());
  },
  querySelectorAll(selector){if(selector==='.seg[data-k] button')return [...groups.values()].flatMap(g=>g.buttons);if(selector==='input[data-p]')return [...inputs.values()];throw Error('Unexpected dialog selector: '+selector)}
 };
 const body={style:style(),classList:classes(['unrelated','bar-stale','theme-stale','hide-stale'])},root={style:style()},densityButtons=['m','l'].map(d=>node({d}));
 ids.set('settings',dialog);ids.set('setBtn',node());ids.set('grid',node());ids.set('nightBtn',node());ids.set('densSeg',{querySelectorAll:()=>densityButtons});
 const context=vm.createContext({document:{body,documentElement:root,getElementById:id=>ids.get(id)||null},Event:class{constructor(type){this.type=type}},
  localStorage:{getItem(key){if(readThrows)throw Error('Storage unavailable');return stored.get(key)||null},setItem(key,value){attempts++;if(writeThrows)throw Error('Quota exceeded');stored.set(key,value)}},
  dispatchEvent:e=>events.push(e.type),gsTextLayout:()=>layoutCalls++
 });context.window=context;
 vm.runInContext(fs.readFileSync(path.join(__dirname,'static/settings.js'),'utf8'),context);
 return {settings:context.gsSettings,dialog,body,root,ids,groups,inputs,stored,events,densityButtons,attempts:()=>attempts,layoutCalls:()=>layoutCalls,failWrites:value=>writeThrows=value,
  click(key,value,nested=false){const button=groups.get(key).buttons.find(b=>b.dataset.v===value);assert(button,`Missing ${key}=${value} control`);dialog.onclick({target:nested?node({},button):button})},
  check(part,checked){const input=inputs.get(part);assert(input,`Missing ${part} checkbox`);input.checked=checked;dialog.onchange({target:input})}
 };
}

test('dialog listeners open, close on Done/backdrop, and leave ordinary content clicks open',()=>{
 const h=setup();
 assert.deepEqual([...h.groups.keys()],['bar','textSize','theme']);assert.equal(h.inputs.size,9);
 h.ids.get('setBtn').onclick();assert.equal(h.dialog.open,true);
 h.dialog.onclick({target:node()});assert.equal(h.dialog.open,true);
 h.ids.get('setDone').onclick();assert.equal(h.dialog.open,false);
 h.ids.get('setBtn').onclick();h.dialog.onclick({target:h.dialog});assert.equal(h.dialog.open,false);
 assert.equal(h.attempts(),0);
});

test('user segmented-control clicks update classes, active buttons, text scale and persisted theme history',()=>{
 const h=setup();h.ids.get('setBtn').onclick();
 h.click('bar','right',true);h.click('textSize','large');h.click('theme','day');h.click('theme','red');
 assert.equal(h.settings.get().bar,'right');assert(h.body.classList.contains('bar-right'));
 assert(h.body.classList.contains('theme-red'));assert(h.body.classList.contains('night'));
 assert.equal(h.body.classList.contains('theme-day'),false);assert(h.body.classList.contains('unrelated'));
 for(const old of ['bar-stale','theme-stale','hide-stale'])assert.equal(h.body.classList.contains(old),false);
 assert.equal(h.root.style['--text-scale'],1.25);assert.equal(h.ids.get('nightBtn').textContent,'Exit night mode (N)');
 for(const [key,value] of [['bar','right'],['textSize','large'],['theme','red']]){
  const active=h.groups.get(key).buttons.filter(b=>b.classList.contains('on'));assert.deepEqual(active.map(b=>b.dataset.v),[value]);
 }
 assert.equal(h.dialog.open,true);assert.equal(h.layoutCalls(),5);assert.equal(h.events.filter(e=>e==='gs:display-settings').length,4);
 const saved=JSON.parse(h.stored.get('gs.settings'));assert.equal(saved.prev,'day');assert.equal(saved.textSize,'large');
 h.settings.toggleRed();assert(h.body.classList.contains('theme-day'));assert.equal(h.body.classList.contains('night'),false);
 assert.equal(setup(h.stored.get('gs.settings')).settings.get().theme,'day');
});

test('checkbox listeners stay synchronized and Reset restores dialog, widths, feed and density defaults',()=>{
 const h=setup();h.ids.get('setBtn').onclick();
 h.check('ov',false);h.check('ov',false);h.check('clock',false);
 assert.deepEqual(Array.from(h.settings.get().hide),['ov','clock']);assert(h.body.classList.contains('hide-ov'));
 assert.equal(h.inputs.get('ov').checked,false);assert.equal(h.inputs.get('live').checked,true);
 h.check('ov',true);assert.equal(h.body.classList.contains('hide-ov'),false);assert.equal(h.inputs.get('ov').checked,true);
 const attempts=h.attempts();h.dialog.onchange({target:node()});assert.equal(h.attempts(),attempts);
 h.settings.resize('controls',400);h.settings.density('l');h.settings.setFeed({sensor:'EO-2',review:'reviewed'});h.click('textSize','extra-large');h.click('theme','red');
 assert.equal(h.ids.get('grid').className,'grid l');assert(h.densityButtons[1].classList.contains('on'));
 h.ids.get('setReset').onclick();
 const s=h.settings.get();assert.equal(s.bar,'top');assert.equal(s.theme,'cic');assert.equal(s.textSize,'default');assert.equal(s.density,'m');
 assert.equal(Object.keys(s.widths).length,0);assert.equal(s.hide.length,0);assert.equal(s.feed.sensor,'ALL');assert.equal(s.feed.review,'all');
 assert.equal(h.body.style['--width-controls'],undefined);assert.equal(h.root.style['--text-scale'],1.12);
 assert.equal(h.body.classList.contains('night'),false);assert.equal(h.body.classList.contains('hide-clock'),false);
 assert([...h.inputs.values()].every(i=>i.checked));assert.equal(h.ids.get('grid').className,'grid m');assert(h.densityButtons[0].classList.contains('on'));
 assert.equal(h.events.filter(e=>e==='gs:feed-filters').length,1);assert.equal(h.dialog.open,true);
 assert.deepEqual(JSON.parse(h.stored.get('gs.settings')),JSON.parse(JSON.stringify(s)));
});

test('failed storage writes do not break listeners, live display changes, reset or later persistence recovery',()=>{
 const initial=JSON.stringify({bar:'left',theme:'day'}),h=setup(initial,{writeThrows:true});
 h.click('bar','right');h.click('theme','red');h.check('live',false);h.settings.resize('controls',350);
 assert.equal(h.stored.get('gs.settings'),initial);assert.equal(h.attempts(),4);
 assert.equal(h.settings.get().bar,'right');assert(h.body.classList.contains('theme-red'));assert(h.body.classList.contains('hide-live'));
 assert.equal(h.body.style['--width-controls'],'350px');assert.equal(h.events.filter(e=>e==='gs:display-settings').length,2);
 h.ids.get('setReset').onclick();assert.equal(h.settings.get().theme,'cic');assert.equal(h.body.style['--width-controls'],undefined);assert.equal(h.stored.get('gs.settings'),initial);
 h.failWrites(false);h.click('bar','bottom');
 const reloaded=setup(h.stored.get('gs.settings'));assert.equal(reloaded.settings.get().bar,'bottom');assert.equal(reloaded.settings.get().theme,'cic');
});

test('bad or unreadable stored settings fall back to functional default dialog controls',async t=>{
 for(const [name,saved,options] of [['empty',null,{}],['invalid JSON','{',{}],['read failure',null,{readThrows:true}]])await t.test(name,()=>{
  const h=setup(saved,options);assert.equal(h.settings.get().theme,'cic');assert(h.body.classList.contains('bar-top'));
  h.click('theme','day');assert(h.body.classList.contains('theme-day'));assert.equal(JSON.parse(h.stored.get('gs.settings')).theme,'day');
 });
});
