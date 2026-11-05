"""Keep offline experiments out of product dependencies and container inputs."""

import ast
from pathlib import Path
import re
import unittest


ROOT = Path(__file__).resolve().parents[1]
PRODUCT_GO = ("cmd", "edge", "shore", "internal", "gen", "web")


class ProductBoundaryTests(unittest.TestCase):
    def test_product_go_does_not_import_research(self):
        for directory in PRODUCT_GO:
            for path in (ROOT / directory).rglob("*.go"):
                with self.subTest(path=path.relative_to(ROOT)):
                    self.assertNotRegex(path.read_text(),
                        r'"github\.com/fleethackerghostfleet/ghost-seeker/research(?:/|\")')

    def test_product_python_does_not_import_research(self):
        for directory in ("edgeml/src", "edgeml/tests", "algorithm/ghost_priority"):
            for path in (ROOT / directory).rglob("*.py"):
                for node in ast.walk(ast.parse(path.read_text())):
                    names = []
                    if isinstance(node, ast.Import):
                        names = [alias.name for alias in node.names]
                    elif isinstance(node, ast.ImportFrom):
                        names = [node.module or ""]
                    with self.subTest(path=path.relative_to(ROOT), line=getattr(node, "lineno", 0)):
                        self.assertFalse(any(name.split(".")[0] in ("research", "edgeml_research") for name in names))

    def test_docker_copies_only_product_inputs(self):
        for dockerfile, context in (("deploy/Dockerfile", ROOT), ("edgeml/Dockerfile", ROOT / "edgeml")):
            for line in (ROOT / dockerfile).read_text().splitlines():
                if not line.startswith("COPY ") or "--from=" in line:
                    continue
                for source in line.split()[1:-1]:
                    with self.subTest(dockerfile=dockerfile, source=source):
                        self.assertNotIn(source, (".", "./"), "Broad COPY can silently ship research")
                        self.assertNotIn("research", Path(source).parts)
                        self.assertTrue((context / source).exists(), f"Missing Docker input: {source}")

    def test_live_demo_has_no_research_mounts(self):
        compose = (ROOT / "deploy/compose/demo.compose.yml").read_text()
        self.assertNotIn("/research", compose)
        for relative in re.findall(r'"(\.\./\.\./[^":]+):', compose):
            with self.subTest(mount=relative):
                self.assertTrue((ROOT / "deploy/compose" / relative).exists())


if __name__ == "__main__":
    unittest.main()
