"""Negative fixtures for the active-track semantic validator."""
import importlib.util
import json
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("validate_todos", ROOT / "scripts/validate_todos.py")
VALIDATOR = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(VALIDATOR)


class TrackValidationTest(unittest.TestCase):
    def setUp(self):
        self.track = json.loads((ROOT / "todo/archive/review-followup-2026-11-21.json").read_text())

    def test_current_track(self):
        VALIDATOR.validate_track(self.track)

    def test_rejects_wrong_summary(self):
        self.track["tasks_status_summary"]["total"] += 1
        with self.assertRaises(VALIDATOR.TrackError):
            VALIDATOR.validate_track(self.track)

    def test_rejects_missing_dependency(self):
        self.track["tasks"][0]["depends_on"] = ["missing"]
        with self.assertRaises(VALIDATOR.TrackError):
            VALIDATOR.validate_track(self.track)

    def test_rejects_cycle(self):
        first, second = self.track["tasks"][:2]
        first["depends_on"] = [second["id"]]
        second["depends_on"] = [first["id"]]
        with self.assertRaises(VALIDATOR.TrackError):
            VALIDATOR.validate_track(self.track)

    def test_rejects_wrong_milestone(self):
        self.track["tasks"][0]["milestone"] = "missing"
        with self.assertRaises(VALIDATOR.TrackError):
            VALIDATOR.validate_track(self.track)

    def test_rejects_done_without_full_progress(self):
        # Independent of the live track state: force an inconsistent pair.
        self.track["tasks"][0]["status"] = "done"
        self.track["tasks"][0]["progress_percent"] = 50
        with self.assertRaises(VALIDATOR.TrackError):
            VALIDATOR.validate_track(self.track)
