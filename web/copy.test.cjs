const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const {test}=require('node:test');
const read=name=>fs.readFileSync(path.join(__dirname,'static',name),'utf8');

test('operator navigation and empty briefing explain the next step',()=>{
 const html=read('index.html'),briefing=read('briefing.js');
 for(const label of ['P1 Critical','P2 High','P3 Routine','P4 Low','⚙ Settings','Size','Collection Priorities','Briefing builder','Open briefing'])assert.ok(html.includes(label),label);
 assert.match(html,/id="briefCreate" title="Select ships in image details before creating a briefing" disabled>Create briefing \(0\)/);
 assert.match(briefing,/selected.size\?'Build a briefing from the selected ship images':'Select ships in image details first'/);
 assert.match(briefing,/No briefing snapshot yet/);
});

test('advanced collection settings are together and collapsed, with active directives outside',()=>{
 const html=read('index.html');
 const advanced=html.slice(html.indexOf('<details class="panel"><summary>Advanced settings</summary>'),html.indexOf('<dialog id="wbConfirm">'));
 assert.ok(advanced.length>0);
 for(const label of ['Base prioritization model','Effective object weights','Super admin · raw policy (protojson)','Policy audit'])assert.ok(advanced.includes(label),label);
 assert.doesNotMatch(advanced,/<details[^>]*\bopen\b|<h3>Active directives<\/h3>/);
 assert.match(advanced,/id="policyAdmin" hidden/);
 assert.match(html,/>↶ Undo<.*>↷ Redo</);
});

test('different dismissal consequences are explained without changing action keys',()=>{
 const html=read('index.html'),app=read('app.js'),shore=read('shore.js'),workbench=read('workbench.js'),commander=read('cdr.js');
 assert.match(html,/title="Hide this image from the feed and mark it reviewed at P4 Low\. The image remains stored\." data-a="dismiss"/);
 assert.match(app,/id="aDis" title="Hide this image from the feed and mark it reviewed\. The image remains stored\.">Not relevant/);
 assert.match(app,/title="The computer drew a detection box on the wrong thing\.[^"]*" data-choice="wrong"/);
 assert.match(shore,/title="Mark this object as not relevant in this browser’s review queue\.[^"]*" data-act="dismiss"/);
 assert.match(workbench,/title="Propose a rule to lower the priority of similar images\.[^"]*">Not relevant/);
 assert.match(commander,/Record a final decision to take no further action\. The image remains stored in Decided\./);
 assert.match(app,/Image hidden from feed \(still stored\)/);
 for(const source of [app,shore])assert.match(source,/Not sent: enter your name or operator ID in the confirmation box/);
});

test('obsolete operator terms are absent from visible copy',()=>{
 const names=fs.readdirSync(path.join(__dirname,'static')).filter(name=>/\.(html|js)$/.test(name)&&name!=='pptxgen.bundle.js');
 for(const name of names){
  const source=read(name).replace(/rvFlash/g,'reviewFeedback');
  assert.doesNotMatch(source,/Flash|Fwd to CO|Commander's Board|Red light|FEED HELD|Images held|DTG|Precedence|J2|CIC Dark|Agree with model|Workbench applied/,name);
 }
});
