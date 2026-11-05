"""Torch-free CLIP BPE using exported OpenCLIP assets.

Algorithm follows OpenCLIP SimpleTokenizer (MIT, Copyright 2022 ML Foundations)
and OpenAI CLIP (MIT, Copyright 2021 OpenAI). No runtime asset downloads.
"""
from __future__ import annotations
import html
import json
from functools import lru_cache
from pathlib import Path
import numpy as np


def byte_encoder():
    values = list(range(ord("!"), ord("~") + 1)) + list(range(ord("¡"), ord("¬") + 1)) + list(range(ord("®"), ord("ÿ") + 1))
    chars = values[:]
    offset = 0
    for value in range(256):
        if value not in values:
            values.append(value)
            chars.append(256 + offset)
            offset += 1
    return dict(zip(values, map(chr, chars)))


class Tokenizer:
    def __init__(self, path: str | Path):
        import ftfy
        import regex
        self.ftfy, self.regex = ftfy, regex
        data = json.loads(Path(path).read_text())
        if data.get("clean") != "lower" or data.get("schemaVersion") != 1:
            raise ValueError("unsupported CLIP tokenizer normalization")
        self.encoder = data["encoder"]
        self.ranks = {tuple(pair): i for i, pair in enumerate(data["merges"])}
        self.context = int(data["contextLength"])
        self.sot, self.eot = int(data["sotToken"]), int(data["eotToken"])
        self.bytes = byte_encoder()
        self.pattern = regex.compile(r"<start_of_text>|<end_of_text>|'s|'t|'re|'ve|'m|'ll|'d|[\p{L}]+|[\p{N}]|[^\s\p{L}\p{N}]+", regex.IGNORECASE)

    @lru_cache(maxsize=8192)
    def bpe(self, token: str):
        word = tuple(token[:-1]) + (token[-1] + "</w>",)
        while len(word) > 1:
            pairs = set(zip(word, word[1:]))
            pair = min(pairs, key=lambda p: self.ranks.get(p, float("inf")))
            if pair not in self.ranks:
                break
            first, second = pair
            out, i = [], 0
            while i < len(word):
                if i + 1 < len(word) and word[i] == first and word[i + 1] == second:
                    out.append(first + second)
                    i += 2
                else:
                    out.append(word[i])
                    i += 1
            word = tuple(out)
        return word

    def __call__(self, texts: list[str]):
        result = np.zeros((len(texts), self.context), dtype=np.int64)
        for row, raw in enumerate(texts):
            text = html.unescape(html.unescape(self.ftfy.fix_text(raw))).strip()
            text = self.regex.sub(r"\s+", " ", text).strip().lower()
            ids = [self.sot]
            for token in self.regex.findall(self.pattern, text):
                if token == "<start_of_text>":
                    ids.append(self.sot)
                elif token == "<end_of_text>":
                    ids.append(self.eot)
                else:
                    escaped = "".join(self.bytes[b] for b in token.encode("utf-8"))
                    ids.extend(self.encoder[p] for p in self.bpe(escaped))
            ids.append(self.eot)
            if len(ids) > self.context:
                raise ValueError(f"text exceeds {self.context - 2} CLIP tokens")
            result[row, :len(ids)] = ids
        return result
