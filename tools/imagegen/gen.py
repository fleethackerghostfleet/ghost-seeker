import json,base64,subprocess,sys,os,io,time,concurrent.futures as cf,urllib.request
from PIL import Image
P='qwiklabs-gcp-02-34a870014566'
TOK=subprocess.check_output([os.path.expanduser('~/google-cloud-sdk/bin/gcloud'),'auth','print-access-token']).decode().strip()
OUT=os.path.expanduser('~/genimg/raw');os.makedirs(OUT,exist_ok=True)
SUFFIX=(" The camera itself, its housing and mount are not visible. Wide landscape framing. Each object is clearly visible and separate, not overlapping. "
 "Absolutely no text, letters, numbers, timestamps, dates, watermarks, logos, borders, crosshairs or HUD overlays anywhere in the image. Unclassified synthetic training imagery.")
STYLE={
 'EO-P7':"Photorealistic daylight color image from a fixed mast-mounted surveillance camera on a naval pier, looking out across a working harbor, slightly elevated angle, horizon about 40% down from the top, realistic security-camera quality with mild compression.",
 'IR-HE':"Photorealistic thermal infrared surveillance camera image (white-hot grayscale, no color) of a harbor entrance channel at night; warm objects glow bright white, cold water is dark gray, horizon about 40% down from the top, slight sensor noise.",
 'BY-12':"Photorealistic color image from a low camera on an offshore ocean buoy, open ocean with no land, horizon about 40% down from the top, choppy gray-blue sea, slight sea-spray haze.",
 'UAV-3':"Photorealistic straight-down (nadir) aerial color image from a drone gimbal camera at about 300 m altitude over open ocean, deep blue water with small waves, no horizon visible.",
 'SKY-1':"Photorealistic color image from a ground camera beside a runway approach path, pointed up into the sky, pale hazy blue sky with some clouds, only a thin dark treeline at the very bottom edge.",
 'SAR-7':"Synthetic aperture radar (SAR) satellite image like Sentinel-1, grayscale with heavy speckle noise, top-down, dark ocean surface; vessels appear as small bright radar returns, some with faint wakes, no land.",
}
COMP={
 'EO-P7':["a gray navy destroyer moored at mid-distance and a small harbor launch in the foreground","a large container cargo ship transiting the harbor and two seagulls in the sky","a small fast motorboat with a white wake heading toward the camera and a red channel buoy","an oil tanker at anchor and a fishing trawler nearby","a gray navy helicopter flying low over the harbor and a cargo ship at a distant pier","two small boats and a fishing vessel near the pier, a few seagulls","a small quadcopter drone hovering over the water at mid-distance and a green channel buoy","calm empty harbor water with distant cranes on the far shore, no vessels"],
 'IR-HE':["a small boat with a hot engine moving through the channel, leaving a warm wake","a large cargo ship entering the channel","a warship silhouette at mid-distance and a small boat nearby","a person swimming in the water (warm head and shoulders) near a navigation buoy","a fishing vessel and two seabirds flying above","an oil tanker moving slowly through the channel","a helicopter hovering low over the channel","a small quadcopter drone hovering low over the water"],
 'BY-12':["a container cargo ship at mid-distance","an oil tanker on the horizon and a fishing vessel closer to the camera","a gray navy frigate underway at mid-distance","a surfaced submarine with its conning tower visible at mid-distance","floating debris (a wooden pallet and plastic barrels) in the foreground water","a military fighter jet flying low over the ocean","a small open boat and a few seabirds","empty open ocean under an overcast sky, nothing else visible"],
 'UAV-3':["a small fast boat with a long white wake","a container cargo ship seen from above","a fishing vessel with nets out and a small boat nearby","a person floating in the water wearing an orange life vest, with floating debris nearby","a gray navy warship seen from above","an oil tanker seen from above","a surfaced submarine seen from above","two small boats side by side"],
 'SKY-1':["a civil airliner on final approach with landing gear down","a gray military fighter jet","a military helicopter","a small quadcopter drone","a flock of birds","a gray military transport aircraft and a single bird","a regional passenger jet and a distant helicopter","empty sky with scattered clouds, nothing flying"],
 'SAR-7':["three cargo ships spread across the scene","an oil tanker with a long wake","a warship with a strong wake","a cluster of four small fishing vessels","one cargo ship and two small boats","two tankers at anchor","a single small boat with a faint wake","empty ocean surface with only speckle noise, no vessels"],
}
def post(url,body,tries=6):
  for i in range(tries):
    try:
      r=urllib.request.Request(url,data=json.dumps(body).encode(),headers={'Authorization':'Bearer '+TOK,'Content-Type':'application/json'})
      return json.load(urllib.request.urlopen(r,timeout=180))
    except Exception as e:
      err=getattr(e,'read',lambda:b'')().decode()[:300]; print('retry',i,e,err,file=sys.stderr); time.sleep(5*(i+1))
  raise RuntimeError('failed')
URL=f'https://aiplatform.googleapis.com/v1/projects/{P}/locations/global/publishers/google/models/gemini-2.5-flash-image:generateContent'
def gen(src,i):
  fn=f'{OUT}/{src.lower()}-{i+1:02d}.png'
  if os.path.exists(fn): return fn
  prompt=f"{STYLE[src]} The scene shows {COMP[src][i]}.{SUFFIX}"
  d=post(URL,{'contents':[{'role':'user','parts':[{'text':prompt}]}],'generationConfig':{'responseModalities':['IMAGE'],'imageConfig':{'aspectRatio':'16:9'}}})
  parts=[p for p in d['candidates'][0].get('content',{}).get('parts',[]) if 'inlineData' in p]
  if not parts:
    print('noimg',fn,d['candidates'][0].get('finishReason'),file=sys.stderr); return None
  open(fn,'wb').write(base64.b64decode(parts[0]['inlineData']['data'])); print('ok',fn,flush=True); return fn
jobs=[(s,i) for s in COMP for i in range(8)]
if len(sys.argv)>1: jobs=jobs[:int(sys.argv[1])]
with cf.ThreadPoolExecutor(6) as ex: list(ex.map(lambda a:gen(*a),jobs))
import sys as _s
json.dump({'style':STYLE,'comp':COMP,'suffix':SUFFIX},open(os.path.expanduser('~/genimg/prompts.json'),'w'),indent=1)
