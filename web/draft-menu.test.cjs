const {test}=require('node:test');
const assert=require('node:assert/strict');
const setup=require('./oprep-harness.cjs');
const M=require('./static/oprep-model.js');
test('unverified addressee placeholders stay amber and invalid while remaining in the preview',()=>{
 const h=setup();
 h.run(`oprep.open({id:'image-1'},{},[],document.getElementById('rvOprep'))`);
 for(const name of ['to','info']){
  const control=h.form.elements.namedItem(name),placeholder=control.value;
  assert.equal(control['aria-invalid'],'true');assert.equal(control.classList['oprep-missing'],true);
  assert(h.nodes.oprepPreview.textContent.includes(placeholder));
  control.value='VERIFIED OPERATOR ADDRESSEE';h.form.input();
  assert.equal(control['aria-invalid'],'false');assert.equal(control.classList['oprep-missing'],false);
  for(const value of [placeholder,'  [recipient verify ]  ','']){
   control.value=value;h.form.input();
   assert.equal(control['aria-invalid'],'true');assert.equal(control.classList['oprep-missing'],true);
  }
 }
});
test('Draft menu lists whale OPREP first; every option opens a metadata-prefilled shared dialog',()=>{
 const h=setup(),options=h.nodes.rvDraftMenu.children;
 assert.equal(options.length,5);assert.equal(options[0].textContent,'OPREP-3 NAVY BLUE (whale strike)');
 for(let i=0;i<options.length;i++){
  h.run(`oprep.showMenu(type=>oprep.open({id:'image-1',img:'whale.jpg',src:'UAV-3',ts:'2026-11-06T12:00:00Z',dets:[{cls:'marine mammal',conf:.95}]},{},[],document.getElementById('rvOprep'),{capture:{lat:32,lon:-117}},false,type))`);
  options[i].click();
  assert.equal(h.nodes.rvDraftMenu.hidden,true);assert.equal(h.nodes.oprepDialog.open,true);
  assert.equal(h.nodes.oprepTitle.textContent,'Draft '+M.templates[i].label);
  assert.match(h.nodes.oprepPreview.textContent,/IMAGE-1/);assert.match(h.nodes.oprepPreview.textContent,/32-00.00N 117-00.00W/);
  assert.match(h.nodes.oprepPreview.textContent,/DRAFT/);
 }
});
test('menu keyboard navigation, activation, Escape focus restoration, outside close and disabled selection',()=>{
 const h=setup(),options=h.nodes.rvDraftMenu.children;
 h.run('chosen="";oprep.showMenu(type=>chosen=type)');assert.equal(h.context.document.activeElement,options[0]);
 assert.equal(h.key('1').stopped,true);assert.equal(h.run('chosen'),'');
 h.key('End');assert.equal(h.context.document.activeElement,options.at(-1));h.key('ArrowDown');assert.equal(h.context.document.activeElement,options[0]);
 h.key('ArrowUp');assert.equal(h.context.document.activeElement,options.at(-1));h.key('Home');h.key(' ');assert.equal(h.run('chosen'),'whale');
 h.run('oprep.showMenu(()=>{})');h.key('Escape');assert.equal(h.context.document.activeElement,h.nodes.rvOprep);assert.equal(h.nodes.rvOprep['aria-expanded'],'false');
 h.run('oprep.showMenu(type=>chosen=type)');h.key('ArrowDown');h.key('Enter');assert.equal(h.run('chosen'),'spotrep');
 h.run('oprep.showMenu(()=>{})');h.events.pointerdown({target:{}});assert.equal(h.nodes.rvDraftMenu.hidden,true);
 h.nodes.rvOprep.disabled=true;h.run('oprep.showMenu(()=>{})');assert.equal(h.nodes.rvDraftMenu.hidden,true);
});
test('demo send is hidden without explicit feature; queues local image confirmation without fetch',()=>{
 const h=setup();
 const open=`oprep.open({id:'whale-7',img:'whale.jpg'},{},[],document.getElementById('rvOprep'))`;
 h.run('oprep.configure({})');h.run(open);assert.equal(h.nodes.oprepSend.hidden,true);
 h.run('oprep.configure({demoDraftSend:true})');h.run(open);assert.equal(h.nodes.oprepSend.hidden,false);
 h.nodes.oprepSend.click();assert.equal(h.nodes.oprepSendConfirmation.hidden,false);assert.match(h.nodes.oprepSendText.textContent,/whale-7 \(demo\)/);assert.equal(h.nodes.oprepSendImage.src,'/whale.jpg');
 h.run(open);assert.equal(h.nodes.oprepSendConfirmation.hidden,true);
});
test('registry rejects unknown types and all templates support empty drafts',()=>{
 for(const type of M.templates)assert.match(M.message({},new Date(),type.id),/UNCLASSIFIED/);
 assert.throws(()=>M.message({},new Date(),'invalid'),/Unknown report type/);
 const contact=M.defaults({dets:[{cls:'cargo ship',conf:.9}]},{},[],new Date(),{},'spotrep');
 assert.match(contact.evidence,/cargo ship 90%/);assert.match(contact.to,/ADDRESSEE/);
 assert.match(M.message(contact,new Date(),'incident'),/MSGID\/OPREP-3\/.+\nFLAGWORD\/NAVY BLUE/);
});
