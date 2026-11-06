const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');
const B=require('./static/briefing-model.js');
const source=fs.readFileSync(path.join(__dirname,'static/briefing.js'),'utf8');
const plain=value=>JSON.parse(JSON.stringify(value));

function fixture(){return {created:'2026-11-04T20:01:00.000Z',title:'Analyst briefing',layout:'combined',assessment:'Review required.',gaps:'Identity unknown.',records:Array.from({length:5},(_,i)=>({key:JSON.stringify(['frame-'+i,0]),id:'frame-'+i,index:0,label:'Small boat',source:'EO-1',time:'2026-11-04T20:00:00.000Z',priority:2,confidence:.86,box:{x:20,y:30,w:100,h:50},observations:['moving'],image:i===0?'data:image/jpeg;base64,fixture':null}))}}
function documentFixture(){
 const ids=new Map();
 const doc={title:'',createElement(tag){
  return {tagName:tag.toUpperCase(),children:[],style:{},attrs:{},listeners:{},clientWidth:640,_text:'',
   set textContent(value){this.replaceChildren();this._text=String(value)},get textContent(){return this._text+this.children.map(c=>c.textContent).join('')},
   append(...children){for(const child of children){child.parentElement=this;this.children.push(child)}},
   replaceChildren(...children){for(const child of this.children)child.parentElement=null;this.children=[];this._text='';this.append(...children)},
   setAttribute(k,v){this.attrs[k]=String(v)},addEventListener(type,fn){this.listeners[type]??=[];this.listeners[type].push(fn)}
  };
 },getElementById:id=>ids.get(id)||null,querySelectorAll(selector){assert.equal(selector,'#briefPreview .brief-slide');return descendants(ids.get('briefPreview'),n=>n.className==='brief-slide')}};
 doc.head=doc.createElement('head');doc.body=doc.createElement('body');
 return {doc,ids};
}
function descendants(root,predicate){return root.children.flatMap(c=>[...(predicate(c)?[c]:[]),...descendants(c,predicate)])}
function setup(frozen=null){
 const {doc,ids}=documentFixture(),listeners={},exports=[],opens=[];let rejectPopup=false,write=()=>Promise.resolve();
 for(const id of ['briefCreate','briefRebuild','briefPPTX','briefPDF','briefClear','briefCount','briefSelection','briefPreview','briefStatus','briefTitle','briefLayout','briefAssessment','briefGaps'])ids.set(id,doc.createElement('div'));
 const draft=frozen&&plain(frozen);
 for(const [id,key] of [['briefTitle','title'],['briefLayout','layout'],['briefAssessment','assessment'],['briefGaps','gaps']])ids.get(id).value=draft?.[key]||'';
 class Exporter{
  constructor(){this.slides=[];this.ShapeType={rect:'rect'};exports.push(this)}
  addSlide(){const operations=[];this.slides.push(operations);return {addShape:(kind,opts)=>operations.push({type:'rect',kind,opts:plain(opts)}),addImage:opts=>operations.push({type:'image',opts:plain(opts)}),addText:(text,opts)=>operations.push({type:'text',text,opts:plain(opts)})}}
  writeFile(options){this.file=plain(options);return write()}
 }
 const context=vm.createContext({document:doc,$:id=>{assert(ids.has(id),'Unexpected briefing element '+id);return ids.get(id)},BriefingModel:B,frozenDraft:draft,PptxGenJS:Exporter,state:{sel:null},
  addEventListener(type,fn){listeners[type]??=[];listeners[type].push(fn)},
  open(url,target){const popup=rejectPopup?null:{document:documentFixture().doc,printCalls:0,print(){this.printCalls++}};opens.push({url,target,popup});return popup},
  confirm:()=>assert.fail('Selection/capture paths are outside this test'),renderDetail:()=>assert.fail('Capture drawer must not be rendered')
 });context.window=context;
 // Seed already-frozen evidence without entering the selection/capture workflow.
 assert(source.includes('let draft=null,busy=false;'));
 vm.runInContext(source.replace('let draft=null,busy=false;','let draft=frozenDraft,busy=false;'),context);
 const briefing=vm.runInContext('briefing',context);briefing.init();
 return {doc,ids,draft,exports,opens,briefing,writeWith:fn=>write=fn,blockPopup:value=>rejectPopup=value,
  slides:()=>doc.querySelectorAll('#briefPreview .brief-slide'),
  click:id=>ids.get(id).onclick(),input(id,value){const el=ids.get(id);el.value=value;for(const fn of el.listeners.input)fn({target:el})},resize(){for(const fn of listeners.resize)fn()}
 };
}

test('init renders the no-snapshot state and wires guarded export and input entry points',async()=>{
 const h=setup();assert.equal(h.slides().length,0);assert.match(h.ids.get('briefPreview').textContent,/No briefing snapshot yet/);
 assert.equal(h.ids.get('briefPPTX').disabled,true);assert.equal(h.ids.get('briefPDF').disabled,true);
 for(const id of ['briefTitle','briefLayout','briefAssessment','briefGaps'])assert.equal(h.ids.get(id).listeners.input.length,1);
 h.input('briefTitle','Title before any snapshot');h.resize();await h.click('briefPPTX');h.click('briefPDF');
 assert.equal(h.exports.length,0);assert.equal(h.opens.length,0);assert.equal(h.slides().length,0);
});

test('frozen draft init, user edits and window resize render real model scenes without changing evidence',()=>{
 const frozen=fixture(),h=setup(frozen),before=JSON.stringify(h.draft.records);
 assert.equal(h.slides().length,2);assert.equal(h.ids.get('briefPPTX').disabled,false);assert.equal(h.ids.get('briefPDF').disabled,false);
 assert(h.slides().every(s=>s.style.transform==='scale(0.5)'));
 assert.match(h.ids.get('briefPreview').textContent,/Slide 2 \/ 2/);assert.match(h.ids.get('briefPreview').textContent,/EO-1/);
 assert.equal(descendants(h.ids.get('briefPreview'),n=>n.tagName==='IMG').length,1);
 h.input('briefTitle','Revised <briefing>');h.input('briefAssessment','Updated assessment');h.input('briefGaps','Updated gaps');h.input('briefLayout','individual');
 assert.equal(h.slides().length,5);assert.match(h.ids.get('briefPreview').textContent,/Slide 5 \/ 5/);
 for(const text of ['Revised <briefing>','Updated assessment','Updated gaps'])assert(h.ids.get('briefPreview').textContent.includes(text));
 const slides=h.slides();slides[0].parentElement.clientWidth=320;slides[1].parentElement.clientWidth=960;h.resize();
 assert.equal(slides[0].style.transform,'scale(0.25)');assert.equal(slides[1].style.transform,'scale(0.75)');
 assert.equal(JSON.stringify(h.draft.records),before);assert.equal(JSON.stringify(frozen),JSON.stringify(fixture()));
});

test('PowerPoint entry point awaits the writer, blocks reentry/print and restores export controls',async()=>{
 const h=setup(fixture());let finish;h.writeWith(()=>new Promise(resolve=>finish=resolve));
 h.input('briefTitle','Latest analyst title');const before=JSON.stringify(h.draft.records),pending=h.click('briefPPTX');
 assert.equal(h.exports.length,1);assert.equal(h.ids.get('briefPPTX').disabled,true);assert.equal(h.ids.get('briefPDF').disabled,true);
 await h.click('briefPPTX');h.click('briefPDF');assert.equal(h.exports.length,1);assert.equal(h.opens.length,0);
 const pptx=h.exports[0];assert.equal(pptx.layout,'LAYOUT_WIDE');assert.equal(pptx.author,'Ghost Seeker');assert.equal(pptx.subject,B.marking);assert.equal(pptx.title,'Latest analyst title');assert.equal(pptx.lang,'en-US');
 assert.equal(pptx.slides.length,2);assert(pptx.slides.flat().some(o=>o.type==='image'));assert(pptx.slides.flat().some(o=>o.type==='text'&&o.text==='Latest analyst title'));
 assert.deepEqual(pptx.file,{fileName:'ghost-seeker-j2-2026-11-04T20-01-00-000Z.pptx',compression:true});
 finish();await pending;assert.match(h.ids.get('briefStatus').textContent,/PowerPoint exported/);
 assert.equal(h.ids.get('briefPPTX').disabled,false);assert.equal(h.ids.get('briefPDF').disabled,false);assert.equal(h.slides().length,2);assert.equal(JSON.stringify(h.draft.records),before);
});

test('PowerPoint writer rejection reports failure, releases busy state and permits retry',async()=>{
 const h=setup(fixture());h.writeWith(()=>Promise.reject(Error('disk unavailable')));
 await h.click('briefPPTX');assert.equal(h.ids.get('briefStatus').textContent,'PowerPoint export failed: disk unavailable');
 assert.equal(h.ids.get('briefPPTX').disabled,false);assert.equal(h.ids.get('briefPDF').disabled,false);assert.equal(h.slides().length,2);
 h.writeWith(()=>Promise.resolve());await h.click('briefPPTX');assert.equal(h.exports.length,2);assert.match(h.ids.get('briefStatus').textContent,/PowerPoint exported/);
});

test('print entry point builds an independent printable document and invokes only its print action',()=>{
 const h=setup(fixture()),before=JSON.stringify(h.draft);h.click('briefPDF');
 assert.equal(h.opens.length,1);const {url,target,popup}=h.opens[0];assert.equal(url,'');assert.equal(target,'_blank');
 assert.equal(popup.document.title,'Ghost Seeker Open briefing');assert.equal(popup.printCalls,0);
 assert.match(popup.document.head.textContent,/@page\{size:13\.333333in 7\.5in;margin:0\}/);assert.match(popup.document.head.textContent,/@media print/);
 const slides=descendants(popup.document.body,n=>n.className==='brief-slide');assert.equal(slides.length,2);assert(slides.every(s=>s.style.transform===undefined));
 assert.match(popup.document.body.textContent,/Analyst briefing/);assert.match(popup.document.body.textContent,/EO-1/);
 const actions=descendants(popup.document.body,n=>n.tagName==='BUTTON');assert.equal(actions.length,1);actions[0].onclick();assert.equal(popup.printCalls,1);
 assert.match(h.ids.get('briefStatus').textContent,/Print preview opened/);assert.equal(JSON.stringify(h.draft),before);
});

test('blocked print popups report recovery guidance without disabling a subsequent export',()=>{
 const h=setup(fixture());h.blockPopup(true);h.click('briefPDF');assert.match(h.ids.get('briefStatus').textContent,/Allow popups/);
 assert.equal(h.ids.get('briefPDF').disabled,false);assert.equal(h.ids.get('briefPPTX').disabled,false);
 h.blockPopup(false);h.click('briefPDF');assert.equal(h.opens.length,2);assert(h.opens[1].popup);assert.match(h.ids.get('briefStatus').textContent,/Print preview opened/);
});
