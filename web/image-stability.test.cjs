const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const {test}=require('node:test');
const source=fs.readFileSync(path.join(__dirname,'static/render.js'),'utf8');

class Node {
 constructor(name,attrs={},children=[]){this.nodeType=name==='#text'?3:1;this.nodeName=name;this.attrs={...attrs};this.childNodes=[];this.writes=[];for(const child of children)this.insertBefore(child,null)}
 get id(){return this.attrs.id||''}
 get attributes(){return Object.entries(this.attrs).map(([name,value])=>({name,value}))}
 get firstChild(){return this.childNodes[0]||null}
 get nextSibling(){const list=this.parentNode?.childNodes||[];return list[list.indexOf(this)+1]||null}
 getAttribute(name){return this.attrs[name]??null}
 hasAttribute(name){return Object.hasOwn(this.attrs,name)}
 setAttribute(name,value){this.writes.push(name);this.attrs[name]=value}
 removeAttribute(name){delete this.attrs[name]}
 insertBefore(node,before){node.remove();const index=before?this.childNodes.indexOf(before):this.childNodes.length;this.childNodes.splice(index,0,node);node.parentNode=this}
 remove(){if(this.parentNode){const list=this.parentNode.childNodes;list.splice(list.indexOf(this),1);this.parentNode=null}}
 cloneNode(){const copy=new Node(this.nodeName,this.attrs,this.childNodes.map(n=>n.cloneNode()));copy.nodeValue=this.nodeValue;return copy}
}
const text=value=>Object.assign(new Node('#text'),{nodeValue:value});
const tile=(id,score='P2',src='/'+id+'.jpg')=>new Node('ARTICLE',{'data-id':id},[new Node('DIV',{},[new Node('IMG',{src,alt:id}),text(score)])]);
function setup(){
 const templates=new Map();
 const context=vm.createContext({document:{createElement(tag){assert.equal(tag,'template');return {set innerHTML(html){this.content=templates.get(html)}}}}});
 vm.runInContext(source,context);
 return {render(root,html,children){templates.set(html,new Node('FRAGMENT',{},children));context.renderImageHTML(root,html)}};
}
test('unchanged image nodes stay connected through repeated accepts, reranks and metadata refreshes without src writes',()=>{
 const h=setup(),root=new Node('DIV',{},[tile('a'),tile('b')]);
 const a=root.childNodes[0],b=root.childNodes[1],image=a.firstChild.firstChild;
 for(let accept=0;accept<8;accept++){
  h.render(root,'accept-'+accept,[tile('b','P1'),tile('a','P'+(accept%4+1))]);
  assert.equal(root.firstChild,b);assert.equal(root.childNodes[1],a);
  assert.equal(a.firstChild.firstChild,image);assert.equal(image.parentNode,a.firstChild);
  assert.deepEqual(image.writes,[],'unchanged src/alt must never be reassigned');
 }
 assert.equal(a.firstChild.childNodes[1].nodeValue,'P4');
});
test('real image URL changes update src once while additions and evictions keep retained nodes',()=>{
 const h=setup(),root=new Node('DIV',{},[tile('a'),tile('b')]),a=root.firstChild,image=a.firstChild.firstChild;
 h.render(root,'next',[tile('a','P1','/full.jpg'),tile('c')]);
 assert.equal(root.firstChild,a);assert.equal(a.firstChild.firstChild,image);
 assert.equal(image.getAttribute('src'),'/full.jpg');assert.deepEqual(image.writes,['src']);
 assert.deepEqual(root.childNodes.map(n=>n.getAttribute('data-id')),['a','c']);
 h.render(root,'again',[tile('a','P2','/full.jpg'),tile('c')]);
 assert.deepEqual(image.writes,['src']);
 h.render(root,'empty',[]);assert.equal(root.childNodes.length,0);
});
test('drawer and zoom markup refreshes preserve images but update overlays and labels',()=>{
 const h=setup(),surface=score=>[new Node('DIV',{class:'header'},[text(score)]),new Node('DIV',{id:'dImg'},[new Node('IMG',{src:'/a.jpg'}),new Node('SPAN',{class:score})])];
 const root=new Node('DIV',{},surface('P2')),frame=root.childNodes[1],image=frame.firstChild;
 for(let i=0;i<20;i++)h.render(root,'tick-'+i,surface('P1'));
 assert.equal(root.childNodes[1],frame);assert.equal(frame.firstChild,image);
 assert.deepEqual(image.writes,[]);assert.equal(frame.childNodes[1].getAttribute('class'),'P1');
});
