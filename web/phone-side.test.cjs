const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const {test}=require('node:test');
const css=fs.readFileSync(path.join(__dirname,'static/style.css'),'utf8');
test('side toolbar layout and sizing only apply above the compact feed breakpoint',()=>{
 const match=css.match(/@media\(min-width:701px\)\{\n([\s\S]*?)\n\}/);
 assert.ok(match,'side toolbar must have a wide-screen media guard');
 const wide=match[1],outside=css.replace(match[0],'');
 assert.match(wide,/body\.bar-left #pgFeed,body\.bar-right #pgFeed\{flex-direction:row\}/);
 assert.match(wide,/body\.bar-right \.fhead\{order:2;/);
 assert.match(wide,/\.bar-left \.fhead,\.bar-right \.fhead\{width:280px;/);
 assert.match(wide,/\.bar-left \.toolbar,\.bar-right \.toolbar\{flex-direction:column;/);
 assert.doesNotMatch(outside,/body\.bar-(?:left|right) #pgFeed/);
 assert.doesNotMatch(outside,/\.bar-left \.fhead,\.bar-right \.fhead/);
 assert.doesNotMatch(outside,/\.bar-left \.toolbar,\.bar-right \.toolbar/);
});
test('compact feed keeps its existing collapsible filters and desktop custom widths remain guarded',()=>{
 assert.match(css,/@media\(max-width:700px\),\(max-width:1023px\) and \(max-height:500px\)\{/);
 assert.match(css,/#pgFeed \.fhead:not\(\.filters-open\) \.toolbar>/);
 assert.match(css,/@media\(min-width:901px\)\{\s*:is\(\.bar-left,\.bar-right\) \.fhead\{width:var\(--width-controls,280px\)\}/);
});
test('in-flow band labels are not shifted out of uploading or uploaded cards',()=>{
 const textCSS=fs.readFileSync(path.join(__dirname,'static/text-size.css'),'utf8');
 assert.match(textCSS,/\.card:is\(\.sending,\.uploaded\) \.thumb-meta>:is\(\.band-badge,\.newtag,\.night-badge\)\{inset:auto\}/);
});
