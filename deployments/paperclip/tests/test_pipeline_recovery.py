"""Behavioral regressions for routing recovery and untrusted daemon replies."""
import json
import os
import subprocess
import sys
import unittest

from test_wrapper import BLOCKED_EDGE, DIR, Fakes, adm


class PipelineRecoveryTest(unittest.TestCase):
    def setUp(self):
        self.f = Fakes()
        self.addCleanup(self.f.close)
        self.mapping = os.path.join(self.f.dir, "mapping.json")
        with open(self.mapping, "w", encoding="utf-8") as stream:
            json.dump({"ag_a": "pc-a", "ag_b": "pc-b"}, stream)

    def route_setup(self, edge=BLOCKED_EDGE, allowed=False):
        self.f.set("dw", {"admissions": {
            "alpha": adm("alpha", "ag_a", True),
            "ag_b": adm("beta", "ag_b", allowed, [edge], "allow" if allowed else "rework"),
        }, "state": {"revision": 1, "edges": [{
            "upstream_agent_id": "ag_a", "downstream_agent_id": "ag_b",
        }]}})

    def route(self, **env):
        return self.f.py("pipeline.py", "route", "--upstream", "alpha",
                         DOCKWEAVE_PAPERCLIP_AGENT_MAP=self.mapping, **env)

    def calls(self, verb):
        return [json.loads(line) for line in self.f.log("pc")
                if json.loads(line)[:2] == verb]

    def test_rework_routes_true_branch_action(self):
        self.route_setup(dict(BLOCKED_EDGE, code="denied", reason="review this pass"))
        result = self.route()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(len(self.calls(["issue", "create"])), 1)

    def test_unmapped_upstream_never_uses_current_agent(self):
        self.route_setup()
        os.unlink(self.mapping)
        result = self.route(PAPERCLIP_AGENT_ID="wrong-agent")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.calls(["issue", "create"]), [])

    def test_mapping_without_upstream_entry_never_uses_current_agent(self):
        self.route_setup()
        with open(self.mapping, "w", encoding="utf-8") as stream:
            json.dump({"ag_b": "pc-b"}, stream)  # downstream known, upstream not
        result = self.route(PAPERCLIP_AGENT_ID="pc-b")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.calls(["issue", "create"]), [])

    def test_unreadable_mapping_is_reported(self):
        self.route_setup(dict(BLOCKED_EDGE, satisfied=True, code="ok", action="allow"), True)
        with open(self.mapping, "w", encoding="utf-8") as stream:
            stream.write("{not json")
        result = self.route()
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue("WARN" in result.stderr or "UNMAPPED" in result.stdout, result.stdout + result.stderr)
        self.assertEqual(self.calls(["agent", "wake"]), [])

    def test_partial_routing_over_several_targets_is_not_success(self):
        ok_edge = dict(BLOCKED_EDGE, satisfied=True, code="ok", action="allow")
        self.f.set("dw", {"admissions": {
            "alpha": adm("alpha", "ag_a", True),
            "ag_b": adm("beta", "ag_b", True, [ok_edge]),
            "ag_c": adm("gamma", "ag_c", True, [dict(ok_edge, edge_id="e2")]),
        }, "state": {"revision": 1, "edges": [
            {"upstream_agent_id": "ag_a", "downstream_agent_id": "ag_b"},
            {"upstream_agent_id": "ag_a", "downstream_agent_id": "ag_c"},
        ]}})
        result = self.route()  # mapping knows ag_b but not ag_c
        self.assertNotEqual(result.returncode, 0, "partial routing reported as success")
        self.assertIn("WAKE\tbeta", result.stdout)
        self.assertIn("UNMAPPED\tgamma", result.stdout)
        self.assertEqual([c[2] for c in self.calls(["agent", "wake"])], ["pc-b"])
        # Retry after fixing the mapping wakes only the missing target.
        with open(self.mapping, "w", encoding="utf-8") as stream:
            json.dump({"ag_a": "pc-a", "ag_b": "pc-b", "ag_c": "pc-c"}, stream)
        self.assertEqual(self.route().returncode, 0)
        self.assertEqual(sorted(c[2] for c in self.calls(["agent", "wake"])), ["pc-b", "pc-c"])

    def test_failed_issue_lookup_cannot_create_rework(self):
        self.route_setup()
        self.f.set("pc", {"fail": True})
        result = self.route()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.calls(["issue", "create"]), [])

    def test_wake_is_once_per_gate_run(self):
        self.route_setup(dict(BLOCKED_EDGE, satisfied=True, code="ok", action="allow"), True)
        for _ in range(2):
            result = self.route()
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(len(self.calls(["agent", "wake"])), 1)

    def test_failed_comment_is_retried(self):
        self.f.set("dw", {"admissions": {"beta": adm("beta", "ag_b", False, [BLOCKED_EDGE])}})
        self.f.set("pc", {"fail": True})
        self.f.py("pipeline.py", "precheck", "beta", "--issue", "DOC-1")
        self.f.set("pc", {})
        result = self.f.py("pipeline.py", "precheck", "beta", "--issue", "DOC-1")
        self.assertEqual(result.returncode, 3)
        self.assertIn("--comment", self.calls(["issue", "update"])[-1])

    def test_failed_creation_can_retry_after_lookup(self):
        self.route_setup()
        self.f.set("pc", {"create_exit": 1})
        self.assertNotEqual(self.route().returncode, 0)
        self.f.set("pc", {})
        result = self.route()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(len(self.calls(["issue", "create"])), 2)

    def test_wake_retry_keeps_confirmed_issue(self):
        self.route_setup()
        self.f.set("pc", {"wake_exit": 1})
        self.assertNotEqual(self.route().returncode, 0)
        self.f.set("pc", {})
        result = self.route()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(len(self.calls(["issue", "create"])), 1)
        self.assertEqual(len(self.calls(["agent", "wake"])), 2)

    def test_unknown_create_outcome_requires_reconciliation(self):
        self.route_setup()
        self.f.set("pc", {"create_exit": 124})
        self.assertNotEqual(self.route().returncode, 0)
        self.f.set("pc", {})
        self.assertNotEqual(self.route().returncode, 0)
        self.assertEqual(len(self.calls(["issue", "create"])), 1)
        self.f.set("pc", {"list": [{"id": "existing", "title": "Rework [gate tests / run gate-9]"}]})
        self.assertEqual(self.route().returncode, 0)
        self.assertEqual(len(self.calls(["issue", "create"])), 1)

    def test_blocked_update_timeout_keeps_requeueing(self):
        # A timed-out comment must not stop the status re-queue forever; the
        # comment itself is retried only after the reconcile window.
        self.f.set("dw", {"admissions": {"beta": adm("beta", "ag_b", False, [BLOCKED_EDGE])}})
        self.f.set("pc", {"update_exit": 124})
        self.f.py("pipeline.py", "precheck", "beta", "--issue", "DOC-1")
        self.f.set("pc", {})
        result = self.f.py("pipeline.py", "precheck", "beta", "--issue", "DOC-1")
        self.assertEqual(result.returncode, 3)
        updates = self.calls(["issue", "update"])
        self.assertEqual(len(updates), 2, "status update must still run while the comment is unknown")
        self.assertNotIn("--comment", updates[-1])
        self.assertIn("comment outcome unknown", result.stderr)
        result = self.f.py("pipeline.py", "precheck", "beta", "--issue", "DOC-1",
                           DOCKWEAVE_PIPELINE_RECONCILE_SEC="0")
        self.assertIn("--comment", self.calls(["issue", "update"])[-1])
        result = self.f.py("pipeline.py", "precheck", "beta", "--issue", "DOC-1")
        self.assertNotIn("--comment", self.calls(["issue", "update"])[-1], "confirmed comment is not repeated")

    def test_pending_creation_recovers_after_reconcile_window(self):
        self.route_setup()
        self.f.set("pc", {"create_exit": 124})
        self.assertNotEqual(self.route().returncode, 0)
        self.f.set("pc", {})
        # Inside the window: still reconciling, no second create.
        self.assertNotEqual(self.route().returncode, 0)
        self.assertEqual(len(self.calls(["issue", "create"])), 1)
        # Window elapsed and the authoritative lookup shows no tagged issue.
        result = self.route(DOCKWEAVE_PIPELINE_RECONCILE_SEC="0")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(len(self.calls(["issue", "create"])), 2)
        self.assertEqual(self.route().returncode, 0)
        self.assertEqual(len(self.calls(["issue", "create"])), 2)

    def test_pending_wake_recovers_after_reconcile_window(self):
        self.route_setup(dict(BLOCKED_EDGE, satisfied=True, code="ok", action="allow"), True)
        self.f.set("pc", {"wake_exit": 124})
        self.assertNotEqual(self.route().returncode, 0)
        self.f.set("pc", {})
        self.assertNotEqual(self.route().returncode, 0)
        self.assertEqual(len(self.calls(["agent", "wake"])), 1)
        self.assertEqual(self.route(DOCKWEAVE_PIPELINE_RECONCILE_SEC="0").returncode, 0)
        self.assertEqual(self.route().returncode, 0)
        self.assertEqual(len(self.calls(["agent", "wake"])), 2)

    def test_concurrent_routes_never_duplicate_confirmed_create(self):
        self.route_setup()
        args = [sys.executable, "-I", os.path.join(DIR, "pipeline.py"), "route", "--upstream", "alpha"]
        env = self.f.env(DOCKWEAVE_PAPERCLIP_AGENT_MAP=self.mapping)
        children = [subprocess.Popen(args, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE) for _ in range(2)]
        for child in children:
            child.communicate(timeout=60)
        self.assertEqual(self.route().returncode, 0)
        self.assertEqual(len(self.calls(["issue", "create"])), 1)

    def test_concurrent_prechecks_comment_once(self):
        self.f.set("dw", {"admissions": {"beta": adm("beta", "ag_b", False, [BLOCKED_EDGE])}})
        args = [sys.executable, "-I", os.path.join(DIR, "pipeline.py"), "precheck", "beta", "--issue", "DOC-1"]
        children = [subprocess.Popen(args, env=self.f.env(), stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
                    for _ in range(4)]
        for child in children:
            _, err = child.communicate(timeout=60)
            self.assertNotEqual(child.returncode, 0, "a blocked agent is never admitted")
            self.assertNotIn("Traceback", err)
        self.f.py("pipeline.py", "precheck", "beta", "--issue", "DOC-1")
        comments = [c for c in self.calls(["issue", "update"]) if "--comment" in c]
        self.assertEqual(len(comments), 1, comments)

    def test_invalid_admission_never_permits(self):
        for invalid in [dict(adm("beta", "ag_b", True), edges=None),
                        dict(adm("beta", "ag_b", True), edges={"e": 1}),
                        dict(adm("beta", "ag_b", True), edges=[None]),
                        dict(adm("beta", "ag_b", True), allowed="true"),
                        dict(adm("beta", "ag_b", True), edges="bad"),
                        dict(adm("beta", "ag_b", True), edges=[7]),
                        adm("wrong", "wrong", True),
                        adm("beta", "ag_b", True, [BLOCKED_EDGE]),
                        adm("beta", "ag_b", True, action="rework")]:
            with self.subTest(invalid=invalid):
                self.f.set("dw", {"admissions": {"beta": invalid}})
                result = self.f.py("pipeline.py", "precheck", "beta")
                self.assertEqual(result.returncode, 3, result.stdout + result.stderr)

    def test_malformed_409_is_blocked_without_traceback(self):
        result = self.f.py("pipeline.py", "handle-409", "beta", stdin=json.dumps({
            "error": {"code": "pipeline_blocked"}, "admission": [1]}))
        self.assertEqual(result.returncode, 3, result.stderr)
        self.assertNotIn("Traceback", result.stderr)
