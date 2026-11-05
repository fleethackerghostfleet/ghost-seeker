/* Shared browser identity and read-only affordances. Authorization stays server-side. */
const gsAuth=(()=>{
 let identity={mode:'token',role:''};
 const writeControls='#aUploadNext,#aUploadHold,#rvKeys button,#rvAsk button,.tierbtns button,#dReset,#aCdr,#aFull,#aSim,#aDis,#aPull,[data-pull],#clearDir,#directives .x,#modelSel,#emconControl button,#linkPresets button,#wbAdd,#nlIn,#examples,#wbChips,#wbSuggestions,#wbReset,#wbUndo,#wbRedo,.wb-pin,.wb-actions,#wbAcceptFix,#wbHistory [data-revert],#wbApply,#wbConfirmApply,#polRawApply,#polRawValidate,.decs button,#reopen,#note';
 const viewer=()=>identity.mode==='piv'&&identity.role==='viewer';
 const mayWrite=method=>!viewer()||['GET','HEAD'].includes(method.toUpperCase());
 function requireWrite(method){if(!mayWrite(method))throw new Error('read-only role')}
 function update(data){
  identity=data;document.body.classList.toggle('role-viewer',viewer());document.body.classList.toggle('auth-piv',data.mode==='piv');
  const logout=document.getElementById('authLogout');if(logout)logout.hidden=!(data.authenticated&&(data.mode==='piv'||!data.dev));
  const badge=document.getElementById('readOnlyBadge');if(badge)badge.hidden=!viewer();
  if(data.mode!=='piv')return;
  const user=document.querySelector('header .user,header .who');if(!user)return;
  user.replaceChildren();const avatar=document.createElement('div');avatar.className='avatar av';avatar.textContent=(data.name||data.id).split(/\s+/).filter(Boolean).slice(0,2).map(s=>s[0]).join('').toUpperCase();
  const text=document.createElement('div');text.className='auth-identity';const name=document.createElement('strong');name.textContent=data.name||data.id;const role=document.createElement('small');role.textContent=viewer()?'Viewer (read-only)':`Operator · ${data.id}`;text.append(name,role);user.append(avatar,text);user.title=`${data.name} · ${data.id} · ${data.role}`;
 }
 async function init(){const response=await fetch('/api/auth');const data=await response.json();if(!response.ok)throw new Error(data.message||'Could not authenticate');update(data)}
 document.addEventListener('click',event=>{if(viewer()&&event.target.closest(writeControls)){event.preventDefault();event.stopImmediatePropagation()}},true);
 return{init,update,viewer,mayWrite,requireWrite,writeControls};
})();
