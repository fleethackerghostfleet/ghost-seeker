const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');
const source=fs.readFileSync(path.join(__dirname,'static/auth.js'),'utf8');

function setup({fetch=async()=>{throw Error('unexpected fetch')},controls=true,header=true}={}){
 const classes=new Set(),listeners={},calls=[];
 const element=()=>{
  const node={hidden:true,children:[],textContent:'',append(...children){this.children.push(...children)},replaceChildren(){this.children=[]}};
  Object.defineProperty(node,'innerHTML',{set(){throw Error('identity must use text nodes')}});
  return node;
 };
 const user=element(),logout=element(),badge=element();
 const context=vm.createContext({
  fetch(...args){calls.push(args);return fetch(...args)},
  document:{
   body:{classList:{toggle(name,on){on?classes.add(name):classes.delete(name)}}},
   getElementById:id=>controls?({authLogout:logout,readOnlyBadge:badge}[id]||null):null,
   querySelector:()=>header?user:null,createElement:element,
   addEventListener(type,fn,capture){listeners[type]={fn,capture}},
  },
 });
 vm.runInContext(source,context);
 return {auth:vm.runInContext('gsAuth',context),user,logout,badge,classes,listeners,calls};
}

test('auth init waits for the response then safely displays hostile PIV identity text',async()=>{
 let finish;
 const s=setup({fetch:()=>new Promise(resolve=>{finish=resolve})});
 const pending=s.auth.init();
 assert.deepEqual(s.calls,[['/api/auth']]);
 assert.equal(s.user.children.length,0);
 const name=`<img src=x onerror="alert('x')"> & Commander`,id=`unit'"<svg>@example`;
 finish({ok:true,json:async()=>({mode:'piv',role:'viewer',authenticated:true,name,id})});
 await pending;
 assert.equal(s.auth.viewer(),true);
 assert.equal(s.badge.hidden,false);assert.equal(s.logout.hidden,false);
 const [avatar,text]=s.user.children;
 assert.equal(avatar.textContent,'<S');
 assert.equal(text.children[0].textContent,name);
 assert.equal(text.children[1].textContent,'Viewer (read-only)');
 assert.equal(s.user.title,`${name} · ${id} · viewer`);
 assert.ok(s.classes.has('auth-piv')&&s.classes.has('role-viewer'));
});

for(const failure of ['network','invalid-json','denied','missing-message']){
 test(`auth init propagates ${failure} without replacing the current identity`,async()=>{
  const messages={network:'offline', 'invalid-json':'malformed JSON',denied:'<denied> & "unauthorized"','missing-message':'Could not authenticate'};
  const s=setup({fetch:async()=>{
   if(failure==='network')throw Error(messages[failure]);
   return {ok:failure==='invalid-json',json:async()=>{
    if(failure==='invalid-json')throw Error(messages[failure]);
    return {mode:'piv',role:'operator',name:'untrusted response',id:'other',...(failure==='denied'?{message:messages[failure]}:{})};
   }};
  }});
  s.auth.update({mode:'piv',role:'viewer',authenticated:true,name:'Existing viewer',id:'old'});
  await assert.rejects(s.auth.init(),error=>error.message===messages[failure]);
  assert.equal(s.auth.viewer(),true);
  assert.equal(s.user.children[1].children[0].textContent,'Existing viewer');
  assert.equal(s.auth.mayWrite('POST','/api/model'),false);
 });
}

test('identity transitions clear stale viewer/reviewer classes and update logout affordances',()=>{
 const s=setup();
 s.auth.update({mode:'piv',role:'viewer',authenticated:true,name:'Viewer',id:'viewer'});
 s.auth.update({mode:'token',role:'reviewer',authenticated:true,dev:true});
 assert.equal(s.auth.viewer(),false);assert.equal(s.auth.reviewer(),true);
 assert.deepEqual([...s.classes],['role-reviewer']);
 assert.equal(s.badge.hidden,true);assert.equal(s.logout.hidden,false);
 s.auth.update({mode:'token',role:'operator',authenticated:true,dev:true});
 assert.equal(s.auth.reviewer(),false);assert.equal(s.classes.size,0);
 assert.equal(s.logout.hidden,true);
 assert.doesNotThrow(()=>s.auth.requireWrite('patch','/api/model'));
 s.auth.update({mode:'token',role:'operator',authenticated:true,dev:false});
 assert.equal(s.logout.hidden,false);
 s.auth.update({mode:'token',role:'operator',authenticated:false,dev:false});
 assert.equal(s.logout.hidden,true);
});

test('optional identity DOM is safe and an empty display name falls back to literal id text',()=>{
 const absent=setup({controls:false,header:false});
 assert.doesNotThrow(()=>absent.auth.update({mode:'piv',role:'viewer',authenticated:true,name:'Viewer',id:'id'}));
 assert.equal(absent.auth.viewer(),true);
 const s=setup(),id=`<svg onload="bad"> & 'id'`;
 s.auth.update({mode:'piv',role:'operator',authenticated:true,name:'',id});
 assert.equal(s.user.children[1].children[0].textContent,id);
 assert.equal(s.user.children[1].children[1].textContent,`Operator · ${id}`);
 assert.equal(s.badge.hidden,true);
});

test('capture-phase click guard blocks only viewer write controls and follows role changes',()=>{
 const s=setup(),dispatch=matches=>{
  const seen={prevented:0,stopped:0,selectors:[]};
  s.listeners.click.fn({target:{closest(selector){seen.selectors.push(selector);return matches?{}:null}},
   preventDefault(){seen.prevented++},stopImmediatePropagation(){seen.stopped++}});
  return seen;
 };
 assert.equal(s.listeners.click.capture,true);
 s.auth.update({mode:'piv',role:'viewer',authenticated:true,name:'Viewer',id:'id'});
 const blocked=dispatch(true);
 assert.equal(blocked.prevented,1);assert.equal(blocked.stopped,1);
 assert.deepEqual(blocked.selectors,[s.auth.writeControls]);
 assert.equal(dispatch(false).prevented,0);
 s.auth.update({mode:'piv',role:'operator',authenticated:true,name:'Operator',id:'id'});
 const allowed=dispatch(true);
 assert.equal(allowed.prevented,0);assert.equal(allowed.stopped,0);assert.equal(allowed.selectors.length,0);
});

test('review-only paths are exact and methods are case insensitive',()=>{
 const s=setup();
 s.auth.update({mode:'token',role:'reviewer',authenticated:true});
 for(const route of ['/api/items/id/review','/api/items/a%2Fb/review','/api/reviews/id/reason','/api/reviews/id/undo']){
  assert.equal(s.auth.mayWrite('post',route),true);
  assert.doesNotThrow(()=>s.auth.requireWrite('post',route));
 }
 for(const route of ['/api/items//review','/api/items/a/b/review','/api/items/id/review/extra','/api/items/id/review?x=1','/api/reviews/id/undoing']){
  assert.equal(s.auth.mayWrite('POST',route),false);
  assert.throws(()=>s.auth.requireWrite('POST',route),/review-only role/);
 }
 for(const method of ['put','patch','delete'])assert.equal(s.auth.mayWrite(method,'/api/items/id/review'),false);
 for(const method of ['get','head'])assert.equal(s.auth.mayWrite(method,'/anywhere'),true);
});
