"""Check packaged resources without host mounts, networking or model downloads."""
import subprocess
import sys


def main():
    image = sys.argv[1] if len(sys.argv) > 1 else "ghost-seeker/edgeml-sidecar:0.1.0"
    subprocess.run([
        "docker", "run", "--rm", "--network=none", "--read-only", "--entrypoint", "python", image,
        "-c",
        "import numpy as np; "
        "from edgeml.nightlights import BurstController, frame_report, load_config, load_rules; "
        "assert load_config() and load_rules(); "
        "burst = BurstController.from_config(); "
        "frame_report(np.zeros((32,32,3), dtype=np.uint8), [], burst=burst, t=0); "
        "print('Packaged sidecar night-light configuration and frame analysis: PASS')",
    ], check=True)


if __name__ == "__main__":
    main()
