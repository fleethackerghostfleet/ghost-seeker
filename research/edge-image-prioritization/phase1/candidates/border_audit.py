"""Stress the known overlap-only registration border failure without retuning."""
import json
from pathlib import Path
import cv2
import numpy as np
from candidate_methods import get_methods
from test_candidate_methods import scene


def main():
    cv2.setNumThreads(0)
    base=scene();rows=[]
    for shift in (5,10,20):
        translated=cv2.warpAffine(base,np.float32([[1,0,shift],[0,1,0]]),(640,384),borderMode=cv2.BORDER_REFLECT)
        added=translated.copy()
        # Black object in the newly exposed left image border, which the
        # inferred translation can remove from both overlap comparisons.
        added[170:190,1:max(2,shift-1)]=0
        for name,method in get_methods().items():
            a=method.prepare(base)
            rows.append({'method':name,'translation_original_pixels':shift,
                         'translation_only':method.compare_details(a,method.prepare(translated)),
                         'translation_plus_new_border_patch':method.compare_details(a,method.prepare(added))})
    result={'scope':'Controlled registration-border stress probe; candidate code/config frozen; not an object accuracy metric.',
            'rows':rows,'limitation':'Overlap-only alignment may exclude newly exposed border content. Candidate registered_residual takes the better raw/aligned residual, so a successful alignment can override raw evidence of that change. Conservative bidirectional acceptance does not prevent this geometric blind spot.',
            'possible_future_mitigation':'Separately score newly exposed borders or reject registration when border residual is large; any change needs development-only tuning and fresh held-out validation.'}
    path=Path(__file__).with_name('border-audit.json');path.write_text(json.dumps(result,indent=2)+'\n')
    print(path)


if __name__=='__main__':main()
