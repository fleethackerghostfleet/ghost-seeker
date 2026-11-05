const assert = require('node:assert/strict');
const {test}=require('node:test');
const policy = require('./static/policy.js');

test('EMCON markup reflects effective mode and higher-echelon lock',()=>{
 const html=policy.emconHTML({emcon:{mode:'SILENT',locked:true,lockedBy:'CTF-72'}});
 assert.match(html,/aria-label="Go quiet \(EMCON\)"/);
 assert.match(html,/data-mode="SILENT" class="on" aria-pressed="true"/);
 assert.match(html,/EMCON locked by CTF-72/);
 assert.match(html,/data-mode="NORMAL"[^>]*disabled/);
});

test('directive badges and conflicts escape policy text',()=>{
 assert.match(policy.directiveBadge({source:'CTF-72',locked:true,lockedBy:'CTF-72'}),/Locked by CTF-72/);
 assert.match(policy.directiveBadge({source:'CTF-72'}),/From CTF-72 · delegated/);
 assert.match(policy.directiveBadge({blocked:'Locked by <CTF>'}),/Locked by &lt;CTF&gt;/);
 assert.match(policy.directiveBadge({expiresAtUnixMs:Date.UTC(2026,0,1,5,17)}),/Until 05:17Z/);
 assert.equal(policy.directiveBadge({}),'');
 assert.match(policy.conflictsHTML([{ruleId:'rule-1',reason:'Locked by <CTF>',blockedBy:'lock'}]),/&lt;CTF&gt;/);
 assert.equal(policy.conflictToast([{reason:'Locked by CTF-72'}]),'1 rule dropped — Locked by CTF-72');
});

test('audit rows and hotkey target helper are safe and scoped',()=>{
 assert.match(policy.auditRowsHTML([{atUnixMs:0,who:'<tao>',source:'ui',version:4,text:'x'.repeat(81),result:'rejected'}]),/&lt;tao&gt;/);
 assert.match(policy.auditRowsHTML([{atUnixMs:0,text:'x'.repeat(81)}]),/title="x{81}"/);
 assert.equal(policy.isPolicyHotkeyTarget({closest:selector=>selector.includes('#policyAdmin')}),true);
 assert.equal(policy.isPolicyHotkeyTarget({closest:()=>null}),false);
});
