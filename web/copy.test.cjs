const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const {test}=require('node:test');
const read=name=>fs.readFileSync(path.join(__dirname,'static',name),'utf8');

test('operator navigation and empty briefing explain the next step',()=>{
 const html=read('index.html'),briefing=read('briefing.js');
 for(const label of ['P1 Critical','P2 High','P3 Routine','P4 Low','⚙ Settings','Size','Collection Priorities','Briefing builder','Open briefing'])assert.ok(html.includes(label),label);
 assert.match(html,/id="briefCreate" title="Open an image and use Add detection to select ships for the briefing" disabled>Create briefing \(0\)/);
 assert.match(briefing,/selected.size\?'Create a briefing from the selected ship detections':'Open an image and use Add detection to select ships for the briefing'/);
 assert.match(briefing,/if\(CLS\[d.cls\]\?\.g!=='vessel'\)return/);
 assert.match(briefing,/button.textContent=`\$\{selected.has\(key\)\?'Remove':'Add'\} detection/);
 assert.match(html,/id="pgBrief" aria-label="Briefing workspace"/);
 assert.match(html,/>Tactical Action Officer<br>/);
 assert.match(briefing,/No briefing snapshot yet/);
});

test('advanced collection settings are together and collapsed, with active directives outside',()=>{
 const html=read('index.html');
 const advanced=html.slice(html.indexOf('<details class="panel"><summary>Advanced settings</summary>'),html.indexOf('<dialog id="wbConfirm">'));
 assert.ok(advanced.length>0);
 for(const label of ['Base prioritization model','Effective object weights','Super admin · raw policy (protojson)','Policy audit'])assert.ok(advanced.includes(label),label);
 assert.doesNotMatch(advanced,/<details[^>]*\bopen\b|<h3>Active directives<\/h3>/);
 assert.match(advanced,/id="policyAdmin" hidden/);
 assert.match(advanced,/<h3>Effective object weights<\/h3><p class="hint" id="wNote"><\/p>/);
 assert.match(html,/>↶ Undo<.*>↷ Redo</);
});

test('different dismissal consequences are explained without changing action keys',()=>{
 const html=read('index.html'),app=read('app.js'),workbench=read('workbench.js'),commander=read('cdr.js');
 assert.match(html,/title="Hide this image from the feed and mark it reviewed at P4 Low\. The image remains stored\." data-a="dismiss"/);
 assert.match(app,/id="aDis" title="Hide this image from the feed and mark it reviewed\. The image remains stored\.">Not relevant/);
 assert.match(app,/title="The computer drew a detection box on the wrong thing\.[^"]*" data-choice="wrong"/);
 assert.match(workbench,/title="Propose a rule to lower the priority of similar images\.[^"]*">Not relevant/);
 assert.match(commander,/Record a final decision to take no further action\. The image remains stored in Decided\./);
 assert.match(app,/Image hidden from feed \(still stored\)/);
 assert.match(app,/Not sent: enter your name or operator ID in the confirmation box/);
});

test('operator review explains that number keys assign a new image priority',()=>{
 const html=read('index.html');
 assert.match(html,/<p class="rvinstructions"><b>Change this image's priority:<\/b> press 1–4 to set it to P1–P4 and save your review\. Space \/ Enter agrees with the model's suggested priority\.<\/p>/);
 for(const [tier,label] of [[1,'Critical'],[2,'High'],[3,'Routine'],[4,'Low']]){
  assert.ok(html.includes(`data-a="${tier}"><span class="model-badge">MODEL</span><span class="kb-act">Set P${tier} ${label} &amp; next</span><kbd>${tier}</kbd></button>`));
 }
});

test('obsolete operator terms are absent from visible copy',()=>{
 const names=fs.readdirSync(path.join(__dirname,'static')).filter(name=>/\.(html|js)$/.test(name)&&name!=='pptxgen.bundle.js');
 for(const name of names){
  const source=read(name).replace(/rvFlash/g,'reviewFeedback');
  assert.doesNotMatch(source,/Flash|Fwd to CO|Commander's Board|Red light|FEED HELD|Images held|DTG|Precedence|J2|CIC Dark|Agree with model|Workbench applied/,name);
 }
});
