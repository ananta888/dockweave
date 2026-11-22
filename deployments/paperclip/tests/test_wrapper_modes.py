"""RF-030: which protection steps run in each wrapper mode (see the mode
matrix in docs/paperclip-delegation.md). No real Paperclip or Docker calls."""
import os
import subprocess
import unittest

from test_wrapper import DIR, Fakes, adm, write_exe

FAKE_DOCKER = '''#!/bin/sh
echo "$*" >> "$FAKE_DIR/docker.log"
cat >/dev/null
echo "legacy output"
'''


class WrapperModeTest(unittest.TestCase):
    def setUp(self):
        self.f = Fakes()
        self.addCleanup(self.f.close)
        write_exe(os.path.join(self.f.bin, "docker"), FAKE_DOCKER)
        self.ws = os.path.join(self.f.dir, "ws")
        os.makedirs(self.ws)
        self.f.set("curl", {"response": {"choices": [{"message": {"content": "ok\nSTATUS: IN_PROGRESS"}}]}})
        self.f.set("dw", {"admissions": {"beta": adm("beta", "ag_b", True)}})

    def run_wrapper(self, *args):
        env = self.f.env(DOCKWEAVE_DEFAULT_TASK="Bitte X", DOCKWEAVE_PAPERCLIP_HELPER_DIR=DIR,
                         DOCKWEAVE_PAPERCLIP_ENV=os.path.join(self.f.dir, "none.env"),
                         DOCKWEAVE_HOST_WORKSPACE=self.ws)
        return subprocess.run(["sh", os.path.join(DIR, "dockweave-run.sh")] + list(args),
                              capture_output=True, text=True, env=env, timeout=120)

    def dw_calls(self):
        return self.f.log("dw")

    def endpoint_calls(self):
        return [l for l in self.f.log("curl") if "/v1/chat/completions" in l]

    def test_worker_runs_admission_and_pre_run(self):
        p = self.run_wrapper("beta")
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertTrue(any(l.startswith("pipeline status beta") for l in self.dw_calls()), self.dw_calls())
        self.assertTrue(any("--phase pre_run" in l for l in self.dw_calls()), self.dw_calls())
        self.assertEqual(len(self.endpoint_calls()), 1)
        self.assertNotIn("NOT pipeline-protected", p.stderr)

    def test_orchestrate_runs_admission_but_no_pre_run(self):
        p = self.run_wrapper("beta", "orchestrate")
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertTrue(any(l.startswith("pipeline status beta") for l in self.dw_calls()), self.dw_calls())
        self.assertFalse(any("gate run" in l for l in self.dw_calls()), self.dw_calls())

    def test_orchestrate_blocked_spends_no_turn(self):
        self.f.set("dw", {"admissions": {"beta": adm("beta", "ag_b", False, [{
            "edge_id": "e", "gate_id": "tests", "satisfied": False, "code": "failed", "action": "deny"}])}})
        p = self.run_wrapper("beta", "orchestrate")
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertEqual(self.endpoint_calls(), [])

    def test_legacy_target_is_unprotected_and_says_so(self):
        p = self.run_wrapper("dockweave-ag123", "opencode", "/memory/.local/bin/opencode")
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertIn("NOT pipeline-protected", p.stderr)
        self.assertIn("'ag123'", p.stderr, "migration hint names the agent id")
        self.assertEqual(self.dw_calls(), [], "legacy path must not pretend to check the pipeline")
        self.assertEqual(self.endpoint_calls(), [])
        self.assertIn("exec -i dockweave-ag123", "\n".join(self.f.log("docker")))


if __name__ == "__main__":
    unittest.main()


class DaemonDispatchTest(unittest.TestCase):
    """ADR 0003 D-4: DOCKWEAVE_DISPATCH=daemon hands the worker loop to dockweaved."""

    setUp = WrapperModeTest.setUp
    dw_calls = WrapperModeTest.dw_calls
    endpoint_calls = WrapperModeTest.endpoint_calls

    def run_daemon(self, *args, **extra):
        env = self.f.env(DOCKWEAVE_PAPERCLIP_HELPER_DIR=DIR, DOCKWEAVE_PAPERCLIP_ENV=os.path.join(self.f.dir, "none.env"),
                         DOCKWEAVE_DISPATCH="daemon", **extra)
        return subprocess.run(["sh", os.path.join(DIR, "dockweave-run.sh")] + list(args),
                              capture_output=True, text=True, env=env, timeout=120)

    def test_worker_calls_ticket_run_only(self):
        p = self.run_daemon("beta")
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertEqual(self.dw_calls(), ["ticket run beta"])
        self.assertEqual(self.endpoint_calls(), [])
        self.assertEqual(self.f.log("pc"), [])

    def test_blocked_is_handled_not_an_error(self):
        self.f.set("dw", {"admissions": {}, "ticket_run": {"kind": "blocked"}, "ticket_run_exit": 3})
        p = self.run_daemon("beta")
        self.assertEqual(p.returncode, 0, p.stderr)

    def test_failure_is_reported_to_paperclip(self):
        self.f.set("dw", {"admissions": {}, "ticket_run": {"kind": "agent_failed"}, "ticket_run_exit": 1})
        self.assertEqual(self.run_daemon("beta").returncode, 1)

    def test_orchestrate_and_legacy_keep_the_script(self):
        self.run_daemon("lead", "orchestrate", DOCKWEAVE_DEFAULT_TASK="Plan")
        self.assertNotIn("ticket run lead", self.dw_calls())

