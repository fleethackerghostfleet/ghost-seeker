const {test}=require('node:test');
const assert=require('node:assert/strict');
const lights=require('./static/lights.js');
const item={night:true,src:{mode:'eo'},dets:[{cls:'small boat',lights:[{colour:'red',y:20,flashing:true}],sig:{line:'NUC — keep clear',ref:'Rule 27(a)',aspect:'unknown',severity:'urgent'}}]};
test('night detail, badge and urgent why include pattern and caveat',()=>{
 assert.match(lights.section(item),/Navigation lights \(night\)/);assert.match(lights.section(item),/auto-detected — verify visually/);
 assert.match(lights.why(item)[0].t,/NUC/);assert.match(lights.badge(item),/Lights/);assert.match(lights.icon(item.dets[0].lights),/light-flash/);
});
test('day, unknown, IR and SAR do not show light interpretations',()=>{
 for(const changed of [{night:false},{night:null},{src:{mode:'ir'}},{src:{mode:'sar'}}]){
  const value={...item,...changed};assert.equal(lights.section(value),'');assert.equal(lights.badge(value),'');assert.deepEqual(lights.why(value),[]);
 }
});
test('empty patterns, assisted labels, escaping and reduced motion',()=>{
 assert.equal(lights.icon([]),'');assert.equal(lights.icon([{colour:'<script>',y:1}]),'');
 const value={...item,dets:[{...item.dets[0],sig:{...item.dets[0].sig,assisted:true,line:'<img onerror="bad">'}}]};
 assert.match(lights.section(value),/signal-assisted/);assert.doesNotMatch(lights.section(value),/<img/);
 assert.match(require('node:fs').readFileSync(require('node:path').join(__dirname,'static/lights.css'),'utf8'),/prefers-reduced-motion:reduce/);
});
test('partial interpretation is muted and never explains an alert boost',()=>{
 const value={...item,dets:[{...item.dets[0],sig:{...item.dets[0].sig,lowConfidence:true,boost:0}}]};
 assert.match(lights.section(value),/light-low-confidence/);assert.match(lights.section(value),/extra unexplained lights — verify, no priority boost/);
 assert.equal(lights.why(value)[0].c,'light-low-confidence');assert.match(lights.why(value)[0].t,/no priority boost/);
 assert.match(lights.badge(value),/Lights/);
});
test('confidence suppression explains uncertainty, while supported boosts are explicit',()=>{
 const sig={...item.dets[0].sig,lowConfidence:true,boost:0,confidenceNote:'uncertain light evidence — verify, no priority boost'};
 const value={...item,dets:[{...item.dets[0],sig}]};
 assert.match(lights.section(value),/uncertain light evidence/);
 assert.doesNotMatch(lights.why(value)[0].t,/extra unexplained lights/);
 const boosted={...item,dets:[{...item.dets[0],sig:{...item.dets[0].sig,boost:.9}}]};
 assert.match(lights.section(boosted),/priority boost \+0\.90/);
 assert.match(lights.why(boosted)[0].t,/priority boost \+0\.90/);
});
