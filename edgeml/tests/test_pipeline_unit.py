from pathlib import Path
from types import SimpleNamespace

import numpy as np
import pytest

from edgeml import pipeline
from edgeml.types import Detection, Track


class Detector:
    def __init__(self):
        self.frames = []
        self.images = []
        self.detections = [Detection([10, 20, 30, 40], 0.8, "vessel")]

    def set_frame(self, frame):
        self.frames.append(frame)

    def detect(self, image):
        self.images.append(image)
        return self.detections


class Tracker:
    def __init__(self, uses_image=False):
        self.uses_image = uses_image
        self.calls = []
        self.tracks = []

    def update(self, detections, image, frame, t_s):
        self.calls.append((detections, image, frame, t_s))
        self.tracks = [Track(7, list(d.box), d.score, d.label, frame, t_s) for d in detections]
        return self.tracks


class Aggregator:
    def __init__(self):
        self.embedder = None
        self.images = []
        self.calls = []
        self.flushed = []

    def set_frame(self, image):
        self.images.append(image)

    def update(self, tracks, image, frame, t_s):
        self.calls.append((tracks, image, frame, t_s))
        return [{"event": "update", "frame_idx": frame}]

    def flush(self, t_s):
        self.flushed.append(t_s)
        return [{"event": "close", "t_s": t_s}]

    def trajectories(self):
        return [("sighting", 1, [10, 20, 30, 40], 0.8)]


def test_frame_inputs_preserve_explicit_indices_times_and_load_paths_with_no_io(monkeypatch):
    image = np.zeros((8, 12, 3), np.uint8)
    loaded = np.ones_like(image)
    paths = []

    def read(path):
        paths.append(path)
        return loaded

    monkeypatch.setattr(pipeline.cv2, "imread", read)
    frames = [image, (9, 1.25, Path("tuple.png")), Path("auto.png"), "string.png", (12, 2.5, image)]
    result = list(pipeline._iter_frames(frames, fps=2))
    assert [(fr, t_s) for fr, t_s, _ in result] == [(1, 0), (9, 1.25), (3, 1), (4, 1.5), (12, 2.5)]
    assert paths == ["tuple.png", "auto.png", "string.png"]
    assert result[0][2] is result[-1][2] is image
    assert all(result[i][2] is loaded for i in [1, 2, 3])
    assert [t_s for _, t_s, _ in pipeline._iter_frames([image, image], fps=None)] == [0, 1]


@pytest.mark.parametrize("every,expected", [(2, [10, 30, 50]), (3, [10, 40]), (0, [10, 20, 30, 40, 50]), (-2, [10, 20, 30, 40, 50])])
def test_cadence_counts_input_frames_preserves_provenance_and_flushes_after_last_frame(monkeypatch, every, expected):
    detector, tracker, aggregator = Detector(), Tracker(), Aggregator()
    images = [np.full((8, 12, 3), i, np.uint8) for i in range(5)]
    frames = [(10 * (i + 1), float(i), image) for i, image in enumerate(images)]
    ticks = iter(range(4 * len(expected)))
    monkeypatch.setattr(pipeline, "time", SimpleNamespace(perf_counter=lambda: next(ticks)))
    embedder = object()
    result = pipeline.run_clip(iter(frames), detector, tracker, aggregator, embedder=embedder, detect_every=every)
    assert result["n_frames"] == 5 and result["n_detect_frames"] == len(expected)
    assert detector.frames == expected
    assert len(detector.images) == len(tracker.calls) == len(aggregator.calls) == len(expected)
    for image, frame in zip(detector.images, expected):
        assert image is images[frame // 10 - 1]
    assert [fr for fr, _, _ in result["dets"]] == [fr for fr, _, _ in result["tracks"]] == expected
    for call, frame in zip(tracker.calls, expected):
        assert call[1] is images[frame // 10 - 1]
        assert call[2:] == (frame, float(frame // 10 - 1))
    assert aggregator.embedder is embedder
    assert len(aggregator.images) == len(expected)
    assert aggregator.flushed == [4.001]
    assert [ev["frame_idx"] for ev in result["events"][:-1]] == expected
    assert result["events"][-1] == {"event": "close", "t_s": 4.001}
    assert result["sighting_tracks"] == aggregator.trajectories()
    assert result["timing"] == {"detect_s": len(expected), "track_s": len(expected),
                                "aggregate_s": len(expected), "ms_per_detect_frame": 3000.0}


def test_adaptive_rate_switches_between_idle_and_attention_without_coasting_tracker_updates():
    detector, tracker, aggregator = Detector(), Tracker(), Aggregator()
    attention = []

    def needs_attention(t_s, window):
        attention.append((t_s, window))
        return t_s in (2, 3)

    aggregator.needs_attention = needs_attention
    image = np.zeros((8, 12, 3), np.uint8)
    result = pipeline.run_clip([image] * 7, detector, tracker, aggregator,
                               detect_every=pipeline.AdaptiveRate(idle_every=3, active_every=1, active_for_s=2), fps=1)
    assert attention == [(float(i), 2) for i in range(7)]
    assert detector.frames == [1, 3, 4, 7]
    assert len(tracker.calls) == result["n_detect_frames"] == 4
    assert pipeline.AdaptiveRate(idle_every=4).every(object(), 0) == 4


def test_empty_clip_and_minimal_collaborators_have_zero_timings_and_optional_hooks_are_not_required():
    detector = SimpleNamespace(detect=lambda image: [])
    tracker = SimpleNamespace(update=lambda *args: [])
    flushed = []
    aggregator = SimpleNamespace(update=lambda *args: [], flush=lambda t_s: flushed.append(t_s) or [])
    result = pipeline.run_clip([], detector, tracker, aggregator, embedder=object())
    assert result["n_frames"] == result["n_detect_frames"] == 0
    assert result["events"] == result["dets"] == result["tracks"] == result["sighting_tracks"] == []
    assert result["timing"] == {"detect_s": 0, "track_s": 0, "aggregate_s": 0, "ms_per_detect_frame": 0}
    assert flushed == [0.001]
    result = pipeline.run_clip([np.zeros((8, 12, 3), np.uint8)], detector, tracker, aggregator, record_dets=False)
    assert result["dets"] == [] and result["tracks"] == [(1, 0.0, [])]


@pytest.mark.parametrize("uses_image", [False, True])
def test_stabilization_warps_detector_boxes_and_images_but_restores_tracks_to_sensor_coordinates(monkeypatch, uses_image):
    detector, tracker, aggregator = Detector(), Tracker(uses_image), Aggregator()
    image = np.zeros((8, 12, 3), np.uint8)
    warped_image = np.ones_like(image)
    matrix = np.array([[1, 0, 5], [0, 1, 7]], np.float32)
    inverse = np.array([[1, 0, -5], [0, 1, -7]], np.float32)
    warped_calls = []

    def warp_image(img, transform, size):
        assert img is image
        np.testing.assert_array_equal(transform, matrix)
        warped_calls.append(size)
        return warped_image

    def estimate(img):
        assert img is image
        return matrix

    def invert(transform):
        assert transform is matrix
        return inverse

    stabilizer = SimpleNamespace(estimate=estimate, inverse=invert,
                                 warp_box=lambda box, m: [box[0] + m[0, 2], box[1] + m[1, 2], *box[2:]])
    monkeypatch.setattr(pipeline.cv2, "warpAffine", warp_image)
    result = pipeline.run_clip([image], detector, tracker, aggregator, stabilizer=stabilizer)
    assert tracker.calls[0][0][0].box == [15, 27, 30, 40]
    assert tracker.calls[0][1] is (warped_image if uses_image else image)
    assert warped_calls == ([(12, 8)] if uses_image else [])
    assert tracker.tracks[0].box == [15, 27, 30, 40]
    assert detector.detections[0].box == [10, 20, 30, 40]
    assert result["tracks"][0][2][0].box == aggregator.calls[0][0][0].box == [10, 20, 30, 40]
    assert aggregator.images[0] is image and aggregator.calls[0][1] is image


@pytest.mark.parametrize("stage", ["detector", "tracker", "aggregator"])
def test_stage_errors_propagate_without_returning_a_successful_partial_clip(stage):
    detector, tracker, aggregator = Detector(), Tracker(), Aggregator()

    def fail(*args):
        raise RuntimeError(f"{stage} failed")

    target, method = {"detector": (detector, "detect"), "tracker": (tracker, "update"),
                      "aggregator": (aggregator, "update")}[stage]
    setattr(target, method, fail)
    with pytest.raises(RuntimeError, match=f"{stage} failed"):
        pipeline.run_clip([np.zeros((8, 12, 3), np.uint8)], detector, tracker, aggregator)
    assert aggregator.flushed == []


def test_mot_output_formats_generator_rows_empty_outputs_and_file_errors(tmp_path):
    output = tmp_path / "tracks.txt"
    pipeline.write_mot(output, iter([(1, 7, [1.234, -2.356, 3, 4], 0.87654)]))
    assert output.read_text() == "1,7,1.23,-2.36,3.00,4.00,0.8765,-1,-1,-1\n"
    pipeline.write_mot(output, [])
    assert output.read_text() == ""
    with pytest.raises(FileNotFoundError):
        pipeline.write_mot(tmp_path / "missing" / "tracks.txt", [])
