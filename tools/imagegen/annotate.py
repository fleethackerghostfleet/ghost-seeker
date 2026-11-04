import json,subprocess,os,sys,base64,io,time,re,concurrent.futures as cf,urllib.request
from PIL import Image
P='qwiklabs-gcp-02-34a870014566'
TOK=subprocess.check_output([os.path.expanduser('~/google-cloud-sdk/bin/gcloud'),'auth','print-access-token']).decode().strip()
G=os.path.expanduser('~/genimg')
SCENE={'EO-P7':'harbor','IR-HE':'harbor','BY-12':'open','UAV-3':'aerial','SKY-1':'sky','SAR-7':'sat'}
POOLS={
 "harbor":["cargo ship","tanker","small boat","fishing vessel","warship","person","bird","buoy","helicopter","drone"],
 "open":["cargo ship","tanker","warship","submarine","fishing vessel","small boat","debris","bird","military aircraft","helicopter"],
 "aerial":["small boat","cargo ship","fishing vessel","warship","debris","person","submarine","tanker"],
 "sky":["civil aircraft","military aircraft","helicopter","drone","bird"],
 "sat":["cargo ship","tanker","warship","fishing vessel","small boat"],
}
URL=f'https://aiplatform.googleapis.com/v1/projects/{P}/locations/global/publishers/google/models/'+os.environ.get('MODEL','gemini-2.5-pro')+':generateContent'
def post(body):
  for i in range(6):
    try:
      r=urllib.request.Request(URL,data=json.dumps(body).encode(),headers={'Authorization':'Bearer '+TOK,'Content-Type':'application/json'})
      return json.load(urllib.request.urlopen(r,timeout=300))
    except Exception as e: print('retry',i,e,file=sys.stderr);time.sleep(5*(i+1))
  raise RuntimeError
def prep(fn):
  im=Image.open(fn).convert('RGB');w,h=im.size;tw=int(h*1.6)
  if tw<=w: l=(w-tw)//2; im=im.crop((l,0,l+tw,h))
  else: th=int(w/1.6); t=(h-th)//2; im=im.crop((0,t,w,t+th))
  return im.resize((960,600),Image.LANCZOS)
def ann(fn):
  base=os.path.basename(fn)[:-4]; src=base.rsplit('-',1)[0].upper(); scene=SCENE[src]
  out=f'{G}/final/{base}.jpg'; im=prep(fn); im.save(out,quality=82,optimize=True)
  buf=io.BytesIO(); im.save(buf,'JPEG',quality=90)
  cls=POOLS[scene]
  prompt=("You are labeling a synthetic surveillance image for an object detector. Detect every clearly visible object of these classes: "+", ".join(cls)+
   ". 'small boat' = launches, RIBs, motorboats, skiffs; 'warship' = navy gray combatants; 'person' = person in the water. Ignore objects tinier than about 1% of the image width, cranes, buildings, land and the camera housing. "
   "Each box must tightly enclose the whole object along its full length, whatever its orientation. Return a JSON array of objects {\"label\": <one of the classes exactly>, \"box_2d\": [ymin, xmin, ymax, xmax]} with coordinates normalized to 0-1000. Return [] if none. At most 6 objects, largest/most important first.")
  for att in range(4):
   d=post({'contents':[{'role':'user','parts':[{'inlineData':{'mimeType':'image/jpeg','data':base64.b64encode(buf.getvalue()).decode()}},{'text':prompt}]}],
           'generationConfig':dict({'responseMimeType':'application/json','temperature':0,'maxOutputTokens':4096},**({'thinkingConfig':{'thinkingBudget':0}} if 'flash' in os.environ.get('MODEL','') else {}))})
   txt=''.join(p.get('text','') for p in d['candidates'][0]['content']['parts'])
   try: objs=json.loads(txt); break
   except Exception as e: print('badjson',base,e,file=sys.stderr); objs=[]
  dets=[]
  for o in objs:
    if not isinstance(o,dict) or o.get('label') not in cls or len(o.get('box_2d') or [])!=4: continue
    y0,x0,y1,x1=o['box_2d']
    b={'x':round(x0/1000*480,1),'y':round(y0/1000*300,1),'w':round((x1-x0)/1000*480,1),'h':round((y1-y0)/1000*300,1)}
    if b['w']<3 or b['h']<3: continue
    dets.append({'cls':o['label'],'b':b})
  print(base,[d['cls'] for d in dets],flush=True)
  return {'file':f'imagery/{base}.jpg','src':src,'dets':dets[:6]}
os.makedirs(f'{G}/final',exist_ok=True)
files=sorted(f'{G}/raw/{f}' for f in os.listdir(f'{G}/raw') if f.endswith('.png'))
if len(sys.argv)>1: files=[f for f in files if any(a in f for a in sys.argv[1:])]
prev={}
if os.path.exists(f'{G}/'+os.environ.get('LIB','library.json')): prev={e['file']:e for e in json.load(open(f'{G}/'+os.environ.get('LIB','library.json')))}
with cf.ThreadPoolExecutor(6) as ex: res=list(ex.map(ann,files))
for r in res: prev[r['file']]=r
json.dump(sorted(prev.values(),key=lambda e:e['file']),open(f'{G}/'+os.environ.get('LIB','library.json'),'w'),indent=1)
