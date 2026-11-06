const oprep=(()=>{
 let trigger,created;
 const dialog=document.getElementById('oprepDialog'),form=document.getElementById('oprepForm'),preview=document.getElementById('oprepPreview'),status=document.getElementById('oprepStatus');
 const fields=()=>Object.fromEntries(new FormData(form));
 function update(){preview.textContent=OprepModel.message(fields(),created)}
 function open(item,classes,sources,button){
  trigger=button;created=new Date();
  const values=OprepModel.defaults(item,classes,sources,created);
  for(const [key,value] of Object.entries(values))form.elements.namedItem(key).value=value;
  status.textContent='';update();dialog.showModal();
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
  link.href=url;link.download='OPREP3-NAVY-BLUE-'+OprepModel.dtg(fields().incidentTime)+'.txt';link.click();
  setTimeout(()=>URL.revokeObjectURL(url),1000);status.textContent='Draft downloaded.';
 };
 return {open};
})();
