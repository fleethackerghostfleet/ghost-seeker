const oprep=(()=>{
 let trigger,created,type='whale',selected,demoSend=false;
 const dialog=document.getElementById('oprepDialog'),form=document.getElementById('oprepForm'),preview=document.getElementById('oprepPreview'),status=document.getElementById('oprepStatus');
 const hint=document.getElementById('oprepHint'),whaleHint=hint.textContent;
 const fields=()=>Object.fromEntries(new FormData(form));
 function update(){
  for(const control of form.elements)if(control.name){const value=control.value.trim(),missing=!value||/^\[[^\]]*\bVERIFY\s*\]$/i.test(value);control.classList.toggle('oprep-missing',missing);control.setAttribute('aria-invalid',String(missing))}
  preview.textContent=OprepModel.message(fields(),created,type);
 }
 function open(item,classes,sources,button,metadata={},metadataUnavailable=false,reportType='whale'){
  type=reportType;selected=item;
  document.getElementById('oprepTitle').textContent='Draft '+OprepModel.templates.find(t=>t.id===type).label;
  hint.textContent=type==='whale'?whaleHint:'Draft only. Verify format, reporting category, addressees and timelines against current guidance before release. Amber fields are empty and need input.';
  document.getElementById('oprepSend').hidden=!demoSend;
  document.getElementById('oprepSendConfirmation').hidden=true;
  trigger=button;created=new Date();
  const values=OprepModel.defaults(item,classes,sources,created,metadata,type);
  for(const [key,value] of Object.entries(values))form.elements.namedItem(key).value=value;
  const c={...item.capture,...metadata.capture},m={...item.capture,...metadata.model};
  const provenance={incidentTime:!!c.time,position:Number.isFinite(c.lat)&&Number.isFinite(c.lon),sensorHeading:Number.isFinite(c.headingDeg),species:!!values.species&&values.species===m.subtype,evidence:!!(item.capture||metadata.capture)};
  form.querySelectorAll('.oprep-provenance').forEach(tag=>tag.remove());
  for(const [key,present] of Object.entries(provenance))if(present){const tag=document.createElement('small');tag.className='oprep-provenance';tag.textContent='from metadata — verify before release';form.elements.namedItem(key).closest('label').append(tag)}
  status.textContent=metadataUnavailable?'Metadata request unavailable; using embedded image capture fields where present.':'';update();dialog.showModal();
 }
 form.addEventListener('input',update);
 form.addEventListener('submit',event=>event.preventDefault());
 document.getElementById('oprepClose').onclick=()=>dialog.close();
 dialog.addEventListener('close',()=>{const button=trigger?.isConnected?trigger:document.getElementById('oprepOpen');button?.focus()});
 document.getElementById('oprepCopy').onclick=async()=>{
  try{await navigator.clipboard.writeText(preview.textContent);status.textContent='Draft copied to clipboard.'}
  catch{status.textContent='Clipboard unavailable. Select and copy the preview text manually.'}
 };
 document.getElementById('oprepDownload').onclick=()=>{
  const url=URL.createObjectURL(new Blob([preview.textContent],{type:'text/plain;charset=utf-8'})),link=document.createElement('a');
  link.href=url;link.download=(type==='whale'?'OPREP3-NAVY-BLUE':type)+'-'+OprepModel.dtg(fields().incidentTime)+'.txt';link.click();
  setTimeout(()=>URL.revokeObjectURL(url),1000);status.textContent='Draft downloaded.';
 };
 document.getElementById('oprepSend').onclick=()=>{
  if(!demoSend||!selected)return;
  document.getElementById('oprepSendText').textContent=`Queued to send with image ${selected.id} (demo). Nothing is transmitted.`;
  document.getElementById('oprepSendImage').src=imageFor(selected);
  document.getElementById('oprepSendConfirmation').hidden=false;
 };
 const menu=document.getElementById('rvDraftMenu'),button=document.getElementById('rvOprep');
 let activate;
 function closeMenu(focus=false){menu.hidden=true;button.setAttribute('aria-expanded','false');if(focus)button.focus()}
 const options=OprepModel.templates.map(template=>{
  const option=document.createElement('button');option.type='button';option.setAttribute('role','menuitem');option.tabIndex=-1;option.textContent=template.label;
  option.onclick=()=>{closeMenu();activate?.(template.id)};menu.append(option);return option;
 });
 function showMenu(callback,last=false){if(button.disabled)return;activate=callback;menu.hidden=false;button.setAttribute('aria-expanded','true');options[last?options.length-1:0].focus()}
 button.addEventListener('keydown',event=>{if(['ArrowDown','ArrowUp'].includes(event.key)){event.preventDefault();button.click();if(event.key==='ArrowUp')options.at(-1).focus()}});
 document.addEventListener('keydown',event=>{
  if(menu.hidden)return;
  event.stopImmediatePropagation();
  const index=options.indexOf(document.activeElement);
  if(event.key==='Escape'){event.preventDefault();closeMenu(true)}
  else if(event.key==='Tab')closeMenu();
  else if(['ArrowDown','ArrowUp','Home','End'].includes(event.key)){event.preventDefault();options[event.key==='Home'?0:event.key==='End'?options.length-1:(index+(event.key==='ArrowUp'?-1:1)+options.length)%options.length].focus()}
  else if(event.key==='Enter'||event.key===' '){event.preventDefault();options[index]?.click()}
 },true);
 document.addEventListener('pointerdown',event=>{if(!menu.hidden&&!menu.contains(event.target)&&event.target!==button)closeMenu()});
 return {open,showMenu,closeMenu,menuOpen:()=>!menu.hidden,configure:features=>{demoSend=features.demoDraftSend===true;document.getElementById('oprepSend').hidden=!demoSend}};
})();
