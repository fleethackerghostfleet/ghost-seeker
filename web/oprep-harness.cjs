const vm=require('node:vm');
const fs=require('node:fs');
const M=require('./static/oprep-model.js');
module.exports=function(){
 const nodes={},events={};let context;
 function node(id=''){
  const n={id,hidden:false,children:[],value:'',isConnected:true,classList:{toggle(){}},setAttribute(k,v){this[k]=v},append(child){this.children.push(child)},contains(target){return this===target||this.children.includes(target)},focus(){context.document.activeElement=this},addEventListener(k,fn){this[k]=fn},click(){return this.onclick?.()},closest(){return {append(){}}},querySelectorAll(){return []},showModal(){this.open=true},close(){this.open=false}};
  return n;
 }
 const document={getElementById:id=>nodes[id]||(nodes[id]=node(id)),createElement:()=>node(),addEventListener:(k,fn)=>events[k]=fn};
 const form=document.getElementById('oprepForm');
 form.elements=Object.keys(M.defaults()).map(name=>Object.assign(node(),{name}));
 form.elements.namedItem=name=>form.elements.find(el=>el.name===name);
 document.getElementById('rvDraftMenu').hidden=true;
 context=vm.createContext({document,OprepModel:M,Date,FormData:class{*[Symbol.iterator](){for(const el of form.elements)yield [el.name,el.value]}},imageFor:item=>'/'+item.img,fetch(){throw Error('Unexpected network call')},navigator:{clipboard:{writeText:async()=>{}}}});
 vm.runInContext(fs.readFileSync(require.resolve('./static/oprep.js'),'utf8'),context);
 return {context,nodes,events,form,run:code=>vm.runInContext(code,context),key(key){const event={key,preventDefault(){this.prevented=true},stopImmediatePropagation(){this.stopped=true}};events.keydown(event);return event}};
};
