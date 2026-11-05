const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const {test}=require('node:test');
const source=fs.readFileSync(`${__dirname}/static/auth.js`,'utf8');
const css=fs.readFileSync(`${__dirname}/static/auth.css`,'utf8');
function setup(){
 const classes=new Set(),events={};
 const element=()=>({hidden:true,children:[],className:'',textContent:'',append(...children){this.children.push(...children)},replaceChildren(){this.children=[]}});
 const user=element(),logout=element(),badge=element();
 const context=vm.createContext({document:{body:{classList:{toggle(c,on){on?classes.add(c):classes.delete(c)}}},getElementById:id=>id==='authLogout'?logout:badge,querySelector:()=>user,createElement:element,addEventListener:(name,fn)=>events[name]=fn}});
 vm.runInContext(source,context);return{context,classes,events,user,logout,badge,run:s=>vm.runInContext(s,context)};
}
test('viewer blocks every non-read method and replaces identity using text nodes',()=>{
 const s=setup();s.run(`gsAuth.update({mode:'piv',role:'viewer',authenticated:true,name:'Jane Q. Doe',id:'synthetic@mil'})`);
 assert.equal(s.run('gsAuth.viewer()'),true);assert.ok(s.classes.has('role-viewer'));assert.equal(s.badge.hidden,false);assert.equal(s.logout.hidden,false);
 assert.match(s.run('gsAuth.writeControls'),/#aUploadNext,#aUploadHold/);
 assert.equal(s.user.children[0].textContent,'JQ');assert.equal(s.user.children[1].children[0].textContent,'Jane Q. Doe');
 for(const method of ['POST','PUT','PATCH','DELETE']){assert.equal(s.run(`gsAuth.mayWrite('${method}')`),false);assert.throws(()=>s.run(`gsAuth.requireWrite('${method}')`),/read-only role/)}
 for(const method of ['GET','HEAD'])assert.equal(s.run(`gsAuth.mayWrite('${method}')`),true);
 let prevented=false,stopped=false;s.events.click({target:{closest:()=>true},preventDefault(){prevented=true},stopImmediatePropagation(){stopped=true}});assert.ok(prevented&&stopped);
});
test('operator permits writes; token mode preserves hardcoded identity',()=>{
 const s=setup();s.run(`gsAuth.update({mode:'token',role:'operator',authenticated:true,dev:false})`);assert.equal(s.user.children.length,0);assert.equal(s.logout.hidden,false);assert.equal(s.run(`gsAuth.mayWrite('POST')`),true);
 s.run(`gsAuth.update({mode:'piv',role:'operator',authenticated:true,name:'<b>Fake</b>',id:'synthetic'})`);assert.equal(s.user.children[1].children[0].textContent,'<b>Fake</b>');assert.equal(s.run(`gsAuth.mayWrite('POST')`),true);
});
test('reviewer keeps review actions but cannot write outside review endpoints',()=>{
 const s=setup();s.run(`gsAuth.update({mode:'token',role:'reviewer',authenticated:true,dev:false})`);
 assert.equal(s.run('gsAuth.reviewer()'),true);assert.equal(s.run('gsAuth.viewer()'),false);
 assert.ok(s.classes.has('role-reviewer'));assert.ok(!s.classes.has('role-viewer'));assert.equal(s.logout.hidden,false);
 s.run(`gsAuth.update({mode:'token',role:'reviewer',authenticated:true,dev:true})`);assert.equal(s.logout.hidden,false);
 for(const method of ['GET','HEAD'])assert.equal(s.run(`gsAuth.mayWrite('${method}','/api/anything')`),true);
 for(const path of ['/api/items/item%2Fid/review','/api/reviews/record/reason','/api/reviews/record/undo']){
  assert.equal(s.run(`gsAuth.mayWrite('POST',${JSON.stringify(path)})`),true);
  assert.doesNotThrow(()=>s.run(`gsAuth.requireWrite('POST',${JSON.stringify(path)})`));
 }
 for(const [method,path] of [['PUT','/api/model'],['POST','/api/directives'],['POST','/api/items/i/upload-override'],['PATCH','/api/items/i']]){
  assert.equal(s.run(`gsAuth.mayWrite('${method}',${JSON.stringify(path)})`),false);
  assert.throws(()=>s.run(`gsAuth.requireWrite('${method}',${JSON.stringify(path)})`),/review-only role/);
 }
 const reviewerRule=css.split('\n').find(line=>line.startsWith('body.role-reviewer'));
 assert.match(reviewerRule,/#navRp/);assert.match(reviewerRule,/#dirBar/);assert.doesNotMatch(reviewerRule,/#rvKeys/);
});
test('both pages fetch auth before config, gate hotkeys, and use server rejection messages',()=>{
 for(const name of ['app','cdr']){const js=fs.readFileSync(`${__dirname}/static/${name}.js`,'utf8');assert.match(js,/await gsAuth.init\(\)/);assert.match(js,/gsAuth.viewer\(\)\)return/);assert.match(js,/gsAuth.requireWrite\(method,path\)/);assert.match(js,name==='app'?/data\?\.message/:/j.message/);const html=fs.readFileSync(`${__dirname}/static/${name==='app'?'index':'cdr'}.html`,'utf8');assert.match(html,/method="post" action="\/logout" hidden/);assert.match(html,/src="\/auth.js"/)}
});
