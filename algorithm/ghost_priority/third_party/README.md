# NanoDet source

`nanodet.py` is vendored from OpenCV Zoo, commit
`48b10fa505aeb51fbd127fc60a5c660040e823b7`:
https://github.com/opencv/opencv_zoo/blob/48b10fa505aeb51fbd127fc60a5c660040e823b7/models/object_detection_nanodet/nanodet.py

The Apache 2.0 license supplied with that directory is reproduced in
`LICENSE-NANODET`. Local modifications pair output tensors by feature-map
shape to support OpenCV 5's changed output ordering, validate the expected
model output dimensions, and subtract the row maximum for stable softmax.
The wrapper uses class-agnostic NMS. `providers.py` adds aspect-preserving letterboxing,
coordinate conversion and explicit CPU execution around it.

The supported model is OpenCV Zoo's FP32
`object_detection_nanodet_2022nov.onnx` (NanoDet-Plus-m-1.5x, 416 x 416).
Weights are not bundled. Inference runs locally and does not download models.
Generic COCO categories do not identify vessel subtypes or affiliation.
