const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');
const read=name=>fs.readFileSync(path.join(__dirname,'static',name),'utf8');
function commanderSettings(saved){
 const classes=new Set(['commander']);
 const context=vm.createContext({Event,localStorage:{getItem:()=>saved,setItem:(key,value)=>saved=value},window:{dispatchEvent(){}},
  document:{documentElement:{style:{setProperty(){}}},getElementById:()=>null,
   body:{style:{setProperty(){},removeProperty(){}},classList:{[Symbol.iterator]:()=>classes.values(),add:(...names)=>names.forEach(n=>classes.add(n)),remove:n=>classes.delete(n),toggle:(n,on)=>on?classes.add(n):classes.delete(n)}}}});
 vm.runInContext(read('settings.js'),context);
 return {classes,settings:context.window.gsSettings,saved:()=>saved};
}
test('Commander honors each persisted theme without requiring a Settings dialog',()=>{
 for(const theme of ['cic','day','red']){
  const result=commanderSettings(JSON.stringify({theme,prev:'day'}));
  assert.ok(result.classes.has('commander'));
  assert.ok(result.classes.has('theme-'+theme));
  assert.equal(result.classes.has('night'),theme==='red');
 }
 for(const saved of [null,'broken','{"theme":"invalid"}']){
  assert.ok(commanderSettings(saved).classes.has('theme-cic'));
 }
});
test('theme changes persist across Commander reloads and remove stale theme classes',()=>{
 const result=commanderSettings('{"theme":"day","prev":"day"}');
 result.settings.toggleRed();
 assert.ok(result.classes.has('night'));
 assert.equal(result.classes.has('theme-day'),false);
 assert.ok(commanderSettings(result.saved()).classes.has('theme-red'));
 result.settings.toggleRed();
 assert.equal(result.classes.has('night'),false);
 assert.ok(commanderSettings(result.saved()).classes.has('theme-day'));
});
test('both consoles share red UI styling and the same imagery filter',()=>{
 for(const name of ['index.html','cdr.html'])assert.match(read(name),/<link rel="stylesheet" href="\/themes.css">/);
 assert.match(read('cdr.html'),/<body class="commander">/);
 const css=read('themes.css');
 assert.match(css,/body\.night,body\.night dialog\{filter:none\}/);
 assert.match(css,/--night-filter:url\('\/red-light.svg#red'\)/);
 assert.match(css,/body\.night :is\(img,canvas,svg\)\{filter:var\(--night-filter\)!important\}/);
 assert.match(css,/body\.commander\.theme-day\{[^}]*--panel:#fff/);
 assert.doesNotMatch(read('style.css'),/body\.night\{filter:/);
});
test('red Commander and Settings use a red-only palette and override bright controls',()=>{
 const css=read('themes.css');
 const palette=css.match(/body\.theme-red,\.theme-red #settings\{([^}]+)\}/)[1];
 for(const color of palette.matchAll(/#[0-9a-f]{6}/g))assert.match(color[0],/^#[0-9a-f]{2}0000$/);
 assert.match(css,/\.theme-red #settings \.btn\.primary\{color:var\(--text\)\}/);
 assert.match(css,/\.theme-red #settings input\{appearance:none/);
 assert.match(css,/\.commander\.theme-red \[style\*="--c:"\]\{--c:var\(--accent\)!important\}/);
 assert.match(css,/\.commander\.theme-red :is\(\.live i,\.row\.fresh\)\{animation:none/);
 assert.match(css,/\.commander\.theme-red \.toast\{transition-property:opacity,transform\}/);
 assert.match(css,/\.commander\.theme-red \.read-only-badge\{color:var\(--text\);border-color:var\(--line\)\}/);
});
