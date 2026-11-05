import json,os
from PIL import Image,ImageDraw
G=os.path.expanduser('~/genimg');L=json.load(open(f'{G}/'+os.environ.get('LIB','library.json')))
for src in sorted({e['src'] for e in L}):
  es=[e for e in L if e['src']==src];sh=Image.new('RGB',(960,600*((len(es)+1)//2)//2),'black')
  for k,e in enumerate(es):
    im=Image.open(f"{G}/final/{os.path.basename(e['file'])}").resize((480,300));d=ImageDraw.Draw(im)
    for t in e['dets']:
      b=t['b'];d.rectangle([b['x'],b['y'],b['x']+b['w'],b['y']+b['h']],outline='yellow',width=2);d.text((b['x']+2,max(0,b['y']-11)),t['cls'],fill='yellow')
    d.text((4,4),os.path.basename(e['file']),fill='red');sh.paste(im,((k%2)*480,(k//2)*300))
  sh.save(f'{G}/'+os.environ.get('PFX','sheet')+f'-{src}.jpg',quality=80)
