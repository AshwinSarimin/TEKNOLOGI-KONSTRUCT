import importlib.util
import pathlib
import re
import subprocess
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "pin_promise_images.py"


class PinPromiseImagesTest(unittest.TestCase):
    def load_module(self):
        spec = importlib.util.spec_from_file_location("pin_promise_images", SCRIPT)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        return module

    def test_rewrite_replaces_only_known_promise_images(self):
        module = self.load_module()
        source = (
            "image: teknologieur1acr.azurecr.io/kratix-team-onboarding:latest\n"
            "image: teknologieur1acr.azurecr.io/kratix-ownership@sha256:" + "a" * 64 + "\n"
            "image: unrelated.example/app:latest\n"
        )
        digests = {name: "sha256:" + "b" * 64 for name in module.IMAGES}
        digests["ownership"] = "sha256:" + "c" * 64
        result = module.rewrite_images(source, digests)
        self.assertIn("kratix-team-onboarding@sha256:" + "b" * 64, result)
        self.assertIn("kratix-ownership@sha256:" + "c" * 64, result)
        self.assertIn("unrelated.example/app:latest", result)

    def test_all_deployed_promise_images_match_the_git_lock(self):
        module = self.load_module()
        lock = module.read_lock(ROOT / "release" / "promise-images.json")
        for relative_path in module.MANIFESTS:
            source = (ROOT / relative_path).read_text()
            matches = list(module.IMAGE_PATTERN.finditer(source))
            self.assertEqual(source.count("teknologieur1acr.azurecr.io/kratix-"), len(matches), relative_path)
            for match in matches:
                name = match.group("name")
                self.assertIn(name, lock["images"], relative_path)
                self.assertEqual("@" + lock["images"][name], match.group("reference"), relative_path)
        self.assertFalse(re.search(r"kratix-[\w-]+:latest", "\n".join((ROOT / path).read_text() for path in module.MANIFESTS)))

    def test_kustomize_preserves_pinned_promise_images(self):
        module = self.load_module()
        lock = module.read_lock(ROOT / "release" / "promise-images.json")
        for relative_path in module.MANIFESTS:
            if not relative_path.startswith("promises/"):
                continue
            directory = str((ROOT / relative_path).parent)
            rendered = subprocess.run(
                ["kubectl", "kustomize", directory],
                check=True, capture_output=True, text=True,
            ).stdout
            for match in module.IMAGE_PATTERN.finditer((ROOT / relative_path).read_text()):
                expected = match.group(0)
                self.assertIn(expected, rendered, relative_path)
            self.assertNotRegex(rendered, r"kratix-[\w-]+:latest")

    def test_promise_ci_does_not_publish_mutable_latest_tags(self):
        workflow = (ROOT / ".github" / "workflows" / "build-kratix-promises.yml").read_text()
        self.assertNotIn(":latest", workflow)

    def test_existing_lock_cannot_be_relabelled_as_another_commit(self):
        module = self.load_module()
        with self.assertRaisesRegex(ValueError, "different source commit"):
            module.digests_from_lock(ROOT / "release" / "promise-images.json", "a" * 40)


if __name__ == "__main__":
    unittest.main()
