"""Temporary higher-rate capture requests with a sliding duty-cycle cap."""
from __future__ import annotations

import logging
from dataclasses import dataclass, field

from .core import load_config

logger = logging.getLogger(__name__)


@dataclass
class BurstController:
    burst_fps: float = 8.0
    duration_s: float = 3.0
    base_fps: float = 1.0
    max_duty: float = 0.2
    window_s: float = 60.0
    intervals: list[tuple[float, float]] = field(default_factory=list)
    reasons: list[tuple[float, str]] = field(default_factory=list)
    granted: int = 0
    denied: int = 0
    degraded: int = 0
    _warned_rate: bool = False

    @classmethod
    def from_config(cls, cfg=None):
        burst = (cfg or load_config())["burst"]
        return cls(burst_fps=burst["burst_fps"], duration_s=burst["duration_s"], base_fps=burst["base_fps"],
                   max_duty=burst["max_duty"], window_s=burst["window_s"])

    def active(self, now: float) -> bool:
        return any(start <= now < end for start, end in self.intervals)

    def burst_seconds(self, now: float) -> float:
        lo = now - self.window_s
        return sum(max(0.0, min(end, now + self.duration_s) - max(start, lo)) for start, end in self.intervals)

    def duty(self, now: float) -> float:
        lo = now - self.window_s
        return sum(max(0.0, min(end, now) - max(start, lo)) for start, end in self.intervals) / self.window_s

    def request(self, now: float, reason: str = "") -> bool:
        """Ask for burst_fps for duration_s. Extends an active burst; denied past the duty cap."""
        self.intervals = [(start, end) for start, end in self.intervals if end > now - self.window_s]
        current = next(((start, end) for start, end in self.intervals if start <= now < end), None)
        end = now + self.duration_s
        if current is not None:
            if end <= current[1]:
                return True
            if self.duty(now) + (end - now) / self.window_s > self.max_duty:
                self.denied += 1
                return False
            self.intervals[self.intervals.index(current)] = (current[0], end)
            self.reasons.append((now, reason))
            return True
        if self.duty(now) + self.duration_s / self.window_s > self.max_duty:
            self.denied += 1
            logger.info("burst denied at %.1fs (%s): duty cap %.0f%%", now, reason, self.max_duty * 100)
            return False
        self.intervals.append((now, end))
        self.reasons.append((now, reason))
        self.granted += 1
        logger.info("burst %.0f fps for %.1fs at %.1fs: %s", self.burst_fps, self.duration_s, now, reason)
        return True

    def fps(self, now: float, source_max_fps: float | None = None) -> float:
        if not self.active(now):
            return self.base_fps
        if source_max_fps is not None and source_max_fps < self.burst_fps:
            if source_max_fps <= self.base_fps:
                if not self._warned_rate:
                    logger.warning("burst requested but frame source is limited to %.2f fps; staying at %.2f fps",
                                   source_max_fps, self.base_fps)
                    self._warned_rate = True
                self.degraded += 1
                return self.base_fps
            self.degraded += 1
            return source_max_fps
        return self.burst_fps

    def frame_interval(self, now: float, source_max_fps: float | None = None) -> float:
        return 1.0 / self.fps(now, source_max_fps)
