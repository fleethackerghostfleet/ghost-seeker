"""Image-only local-change vetoes for global color+pHash; experimental, not calibrated."""
from __future__ import annotations
from dataclasses import dataclass
import hashlib
import json
import math
import cv2
import numpy as np

VERSION = 'ghost-local-v1'
METHOD_NAMES = ('tiled_phash', 'local_color', 'registered_residual')

def _resize(bgr, cap):
    h, w = bgr.shape[:2]
    scale = min(1.0, cap / max(h, w))
    return cv2.resize(bgr, (max(1, round(w*scale)), max(1, round(h*scale))), interpolation=cv2.INTER_AREA)

def _phash(bgr):
    small = cv2.resize(bgr, (32, 32), interpolation=cv2.INTER_LINEAR_EXACT)
    gray = cv2.cvtColor(small, cv2.COLOR_BGR2GRAY).astype(np.float32)
    low = cv2.dct(gray)[:8, :8].copy()
    low[0, 0] = 0
    return int.from_bytes(np.packbits((low > low.mean()).ravel(), bitorder='little').tobytes(), 'little')

def _tile_phash(bgr, margin):
    tiny = cv2.resize(bgr,(32,32),interpolation=cv2.INTER_LINEAR_EXACT)
    gray = cv2.cvtColor(tiny,cv2.COLOR_BGR2GRAY).astype(np.float32)
    low = cv2.dct(gray)[:8,:8].copy()
    low[0,0] = 0.
    centered = low-low.mean()
    bits = int.from_bytes(np.packbits((centered>0).ravel(),bitorder='little').tobytes(),'little')
    reliable = np.abs(centered)>margin
    reliable[0,0] = False
    mask = int.from_bytes(np.packbits(reliable.ravel(),bitorder='little').tobytes(),'little')
    return bits,mask

def _global(bgr):
    small = _resize(bgr, 320)
    color = cv2.resize(small.astype(np.float32)/255., (8, 8), interpolation=cv2.INTER_AREA).ravel()
    return tuple(round(float(v), 5) for v in color), _phash(small)

def _decay(x):
    return 0. if x > 30 else math.exp(-(float(x)**2))

def _global_similarity(a, b):
    distance = sum(abs(x-y)/len(a.color) for x, y in zip(a.color, b.color))
    return min(_decay(distance/.08), _decay((a.phash ^ b.phash).bit_count()/8.))

def _frozen(a):
    a = np.ascontiguousarray(a)
    a.flags.writeable = False
    return a

def _tail_mean(values, fraction):
    values = np.asarray(values).ravel()
    if not len(values):
        return 0.
    k = max(1, math.ceil(len(values)*fraction))
    return float(np.partition(values, len(values)-k)[-k:].mean())

@dataclass(frozen=True)
class Descriptor:
    signature: str
    aspect: float
    color: tuple
    phash: int
    local: np.ndarray
    tile_hashes: tuple = ()
    tile_masks: tuple = ()
    spectrum: np.ndarray | None = None

class LocalSimilarity:
    """prepare(original uint8 BGR)->Descriptor; compare(desc,desc)->[0,1]."""
    def __init__(self, method, **overrides):
        if method not in METHOD_NAMES:
            raise ValueError(f'unknown method {method!r}')
        cfg = dict(analysis_size=640, aspect_tolerance=.03, global_gate=True)
        if method == 'tiled_phash':
            cfg.update(grid=4, tile_color_size=4, phash_scale=8., color_scale=.08, tail_fraction=.0625, hash_margin=2.)
        else:
            cfg.update(local_size=128, residual_scale=.04, tail_fraction=.002, compensate_offset=True)
            if method == 'registered_residual':
                cfg.update(max_shift_fraction=.05, min_response=.08, min_overlap=.88)
        unknown = set(overrides)-set(cfg)
        if unknown:
            raise ValueError(f'unknown config keys: {sorted(unknown)}')
        cfg.update(overrides)
        for key, value in cfg.items():
            if key in {'global_gate', 'compensate_offset'}:
                if type(value) is not bool:
                    raise ValueError(f'{key} must be bool')
            elif key in {'analysis_size','grid','tile_color_size','local_size'}:
                if type(value) is not int or value < 1:
                    raise ValueError(f'{key} must be positive integer')
            elif isinstance(value, bool) or not isinstance(value, (float,int)) or not math.isfinite(value) or value <= 0:
                raise ValueError(f'{key} must be finite and positive')
        for key in ('aspect_tolerance','tail_fraction','max_shift_fraction','min_response','min_overlap'):
            if key in cfg and cfg[key] > 1:
                raise ValueError(f'{key} must be at most 1')
        if cfg['analysis_size'] < 32 or cfg.get('local_size',128) < 16 or cfg.get('grid',4) > 16:
            raise ValueError('analysis_size >= 32, local_size >= 16, grid <= 16 required')
        self.name, self._config = method, cfg
        self.signature = hashlib.sha256(json.dumps([VERSION,method,cfg],sort_keys=True).encode()).hexdigest()[:20]

    @property
    def config(self):
        return dict(self._config)

    def prepare(self, bgr):
        if not isinstance(bgr,np.ndarray) or bgr.dtype != np.uint8:
            raise ValueError('prepare requires original uint8 BGR pixels')
        if bgr.ndim != 3 or bgr.shape[2] != 3 or min(bgr.shape[:2]) < 1:
            raise ValueError('prepare requires nonempty H x W x 3 image')
        cfg = self._config
        color, phash = _global(bgr)
        preview = _resize(bgr,cfg['analysis_size'])
        aspect = bgr.shape[1]/bgr.shape[0]
        if self.name == 'tiled_phash':
            g, side = cfg['grid'],cfg['tile_color_size']
            if min(preview.shape[:2]) < g:
                preview = cv2.resize(preview,(max(g,preview.shape[1]),max(g,preview.shape[0])))
            ys,xs = np.linspace(0,preview.shape[0],g+1,dtype=int),np.linspace(0,preview.shape[1],g+1,dtype=int)
            colors,hashes,masks = [],[],[]
            for y in range(g):
                for x in range(g):
                    patch = preview[ys[y]:ys[y+1],xs[x]:xs[x+1]]
                    hashed,mask = _tile_phash(patch,cfg['hash_margin'])
                    hashes.append(hashed)
                    masks.append(mask)
                    colors.append(cv2.resize(patch.astype(np.float32)/255.,(side,side),interpolation=cv2.INTER_AREA).ravel())
            return Descriptor(self.signature,aspect,color,phash,_frozen(np.stack(colors)),tuple(hashes),tuple(masks))
        size = cfg['local_size']
        local = cv2.resize(preview.astype(np.float32)/255.,(size,size),interpolation=cv2.INTER_AREA)
        spectrum = None
        if self.name == 'registered_residual':
            gray = cv2.cvtColor(local,cv2.COLOR_BGR2GRAY)
            gray = gray-gray.mean()
            window = cv2.createHanningWindow((size,size),cv2.CV_32F)
            spectrum = _frozen(np.fft.rfft2(gray*window).astype(np.complex64))
        return Descriptor(self.signature,aspect,color,phash,_frozen(local),spectrum=spectrum)

    def _residual(self,a,b,mask=None):
        values = a-b
        if mask is not None:
            values = values[mask]
        if not values.size:
            return 1.
        if self._config['compensate_offset']:
            values = values-np.median(values.reshape(-1,3),axis=0)
        return _tail_mean(np.mean(np.abs(values),axis=-1),self._config['tail_fraction'])

    def _registration(self,a,b):
        cfg,size = self._config,self._config['local_size']
        cross = a.spectrum*np.conj(b.spectrum)
        mag = np.abs(cross)
        cross = np.divide(cross,mag,out=np.zeros_like(cross),where=mag>1e-8)
        corr = np.fft.irfft2(cross,s=(size,size)).real
        y,x = np.unravel_index(np.argmax(corr),corr.shape)
        response = float(sum(corr[(y+dy)%size,(x+dx)%size] for dy in (-1,0,1) for dx in (-1,0,1)))
        dy,dx = int(y if y<=size//2 else y-size),int(x if x<=size//2 else x-size)
        if response < cfg['min_response'] or max(abs(dx),abs(dy)) > size*cfg['max_shift_fraction']:
            return None,dict(registered=False,shift_xy=[dx,dy],response=response,registration_reason='response_or_shift_gate')
        matrix = np.float32([[1,0,dx],[0,1,dy]])
        aligned = cv2.warpAffine(b.local,matrix,(size,size),flags=cv2.INTER_LINEAR,borderMode=cv2.BORDER_CONSTANT,borderValue=0)
        mask = cv2.warpAffine(np.ones((size,size),np.uint8),matrix,(size,size),flags=cv2.INTER_NEAREST,borderMode=cv2.BORDER_CONSTANT)>0
        overlap = float(mask.mean())
        if overlap < cfg['min_overlap']:
            return None,dict(registered=False,shift_xy=[dx,dy],response=response,overlap=overlap,registration_reason='overlap_gate')
        return self._residual(a.local,aligned,mask),dict(registered=True,shift_xy=[dx,dy],response=response,overlap=overlap,registration_reason='accepted')

    def compare_details(self,a,b):
        if not isinstance(a,Descriptor) or not isinstance(b,Descriptor):
            raise ValueError('compare requires descriptors returned by prepare')
        if a.signature != self.signature or b.signature != self.signature:
            raise ValueError('descriptor method/config mismatch')
        if abs(math.log(a.aspect/b.aspect)) > self._config['aspect_tolerance']:
            return dict(score=0.,local_similarity=0.,global_similarity=None,reason='incompatible_aspect_ratio')
        global_similarity = _global_similarity(a,b)
        details = {}
        if self.name == 'tiled_phash':
            color_dist = np.mean(np.abs(a.local-b.local),axis=1)/self._config['color_scale']
            hash_dist = np.array([((x^y)&(ma|mb)).bit_count() for x,y,ma,mb in zip(a.tile_hashes,b.tile_hashes,a.tile_masks,b.tile_masks)])/self._config['phash_scale']
            distance = _tail_mean(np.maximum(color_dist,hash_dist),self._config['tail_fraction'])
            details['worst_tail_scaled_distance'] = distance
        else:
            raw = self._residual(a.local,b.local)
            residual = raw
            if self.name == 'registered_residual':
                registered,reg_details = self._registration(a,b)
                reverse,reverse_details = self._registration(b,a)
                details.update(reg_details)
                details['reverse_registration'] = reverse_details
                details['bidirectional_registration_accepted'] = registered is not None and reverse is not None
                if registered is not None and reverse is not None:
                    # Both directions must pass; the worse common-area residual
                    # makes the score symmetric and avoids one-way acceptance.
                    residual = min(raw,max(registered,reverse))
            distance = residual/self._config['residual_scale']
            details.update(raw_tail_residual=raw,used_tail_residual=residual)
        local_similarity = _decay(distance)
        score = min(global_similarity,local_similarity) if self._config['global_gate'] else local_similarity
        return dict(score=float(score),local_similarity=float(local_similarity),global_similarity=float(global_similarity),**details)

    def compare(self,a,b):
        return self.compare_details(a,b)['score']

    def upper_bound(self,a,b):
        """Exact cheap bound for optional pair pruning, not approximate retrieval."""
        if not isinstance(a,Descriptor) or not isinstance(b,Descriptor) or a.signature!=self.signature or b.signature!=self.signature:
            raise ValueError('descriptor method/config mismatch')
        if abs(math.log(a.aspect/b.aspect)) > self._config['aspect_tolerance']:
            return 0.
        return _global_similarity(a,b) if self._config['global_gate'] else 1.

def make_method(name,**config):
    return LocalSimilarity(name,**config)

def get_methods():
    return {name:make_method(name) for name in METHOD_NAMES}

def descriptor_nbytes(desc):
    """Array/scalar payload estimate; excludes Python/container overhead."""
    return int(desc.local.nbytes+(0 if desc.spectrum is None else desc.spectrum.nbytes)+len(desc.color)*8+len(desc.tile_hashes)*16)
