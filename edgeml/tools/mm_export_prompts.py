"""Generate the bundled MobileCLIP-S1 text bank (requires CPU torch/open_clip)."""
import argparse
import json
from pathlib import Path

from edgeml.detectors.marine_mammal import MODEL, PROMPTS
from edgeml.embed.clip_embedder import ClipEmbedder


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--out', type=Path, default=Path(__file__).resolve().parents[1] / 'src/edgeml/detectors/marine_mammal_text.json')
    args = parser.parse_args()
    import torch
    torch.set_num_threads(4)
    embedder = ClipEmbedder(MODEL, backend='torch')
    prompts = [p for group in PROMPTS.values() for p in group]
    vectors = embedder.embed_text(prompts)
    args.out.write_text(json.dumps({'model': MODEL, 'dim': embedder.dim, 'embeddings': dict(zip(prompts, vectors.tolist()))}))
    print(f'Exported {len(prompts)} prompts, dimension {embedder.dim}, to {args.out}')


if __name__ == '__main__':
    main()
