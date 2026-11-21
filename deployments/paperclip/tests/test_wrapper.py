"""Tests for the Paperclip wrapper helpers (pipeline.py, blocked-check.py,
dockweave-run.sh) with faked dockweave/paperclipai/curl binaries.

Run: python3 -m unittest discover -s deployments/paperclip/tests
"""
import json
import os
import stat
import subprocess
import sys
import tempfile
import textwrap
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
DIR = os.path.dirname(HERE)


def write_exe(path, body):
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(body)
    os.chmod(path, os.stat(path).st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)


FAKE_DW = textwrap.dedent('''\
    #!/usr/bin/env python3
    """Fake dockweave CLI driven by $FAKE_DIR/dw.json."""
    import json, os, sys
    d = os.environ["FAKE_DIR"]
    with open(os.path.join(d, "dw.log"), "a") as fh:
        fh.write(" ".join(sys.argv[1:]) + "\\n")
    cfg = json.load(open(os.path.join(d, "dw.json")))
    args = [a for a in sys.argv[1:] if a != "--json"]
    if cfg.get("down"):
        print("dial unix /run/dockweave/dockweaved.sock: connect: no such file", file=sys.stderr)
        sys.exit(1)
    if args[:2] == ["pipeline", "status"]:
        adm = cfg["admissions"].get(args[2])
        if adm is None:
            print("not_found: agent", file=sys.stderr); sys.exit(1)
        if adm == "garbage":
            print("<html>"); sys.exit(0)
        print(json.dumps(adm))
        sys.exit(0 if adm.get("allowed") else 3)
    if args[:2] == ["pipeline", "list"]:
        print(json.dumps(cfg.get("state", {"revision": 1, "edges": []}))); sys.exit(0)
    if args[:2] == ["gate", "run"]:
        print(json.dumps(cfg.get("gate_run", {"status": "passed", "id": "gate-1", "results": []}))); sys.exit(0)
    if args[:2] == ["gate", "review"]:
        sys.exit(0)
    print("unexpected: %s" % args, file=sys.stderr); sys.exit(1)
''')

FAKE_PC = textwrap.dedent('''\
    #!/usr/bin/env python3
    """Fake paperclipai CLI driven by $FAKE_DIR/pc.json; logs every call."""
    import json, os, sys
    d = os.environ["FAKE_DIR"]
    with open(os.path.join(d, "pc.log"), "a") as fh:
        fh.write(json.dumps(sys.argv[1:]) + "\\n")
    cfg = json.load(open(os.path.join(d, "pc.json")))
    if cfg.get("fail"):
        print("boom", file=sys.stderr); sys.exit(1)
    a = sys.argv[1:]
    if a[:2] == ["issue", "get"]:
        issue = cfg.get("issues", {}).get(a[2])
        if issue is None:
            sys.exit(1)
        print(issue if isinstance(issue, str) else json.dumps(issue)); sys.exit(0)
    if a[:2] == ["issue", "list"]:
        print(json.dumps(cfg.get("list", []))); sys.exit(0)
    if a[:2] == ["issue", "create"]:
        if cfg.get("create_exit"):
            sys.exit(cfg["create_exit"])
        print(json.dumps({"id": "new-1", "identifier": "DOC-99"})); sys.exit(0)
    if a[:2] == ["agent", "wake"] and cfg.get("wake_exit"):
        sys.exit(cfg["wake_exit"])
    if a[:2] == ["issue", "update"] and cfg.get("update_exit"):
        sys.exit(cfg["update_exit"])
    print("{}"); sys.exit(0)
''')

FAKE_CURL = textwrap.dedent('''\
    #!/usr/bin/env python3
    """Fake curl: logs the URL and returns $FAKE_DIR/curl.json for the endpoint."""
    import json, os, sys
    d = os.environ["FAKE_DIR"]
    with open(os.path.join(d, "curl.log"), "a") as fh:
        fh.write(" ".join(sys.argv[1:]) + "\\n")
    cfg = json.load(open(os.path.join(d, "curl.json")))
    print(json.dumps(cfg.get("response", {})))
''')


class Fakes:
    def __init__(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = self.tmp.name
        self.bin = os.path.join(self.dir, "bin")
        os.makedirs(self.bin)
        write_exe(os.path.join(self.bin, "dockweave"), FAKE_DW)
        write_exe(os.path.join(self.bin, "paperclipai"), FAKE_PC)
        write_exe(os.path.join(self.bin, "curl"), FAKE_CURL)
        self.set("dw", {"admissions": {}})
        self.set("pc", {})
        self.set("curl", {})

    def set(self, name, cfg):
        with open(os.path.join(self.dir, name + ".json"), "w") as fh:
            json.dump(cfg, fh)

    def log(self, name):
        p = os.path.join(self.dir, name + ".log")
        if not os.path.exists(p):
            return []
        with open(p) as fh:
            return [l.rstrip("\n") for l in fh]

    def env(self, **extra):
        env = {
            "PATH": self.bin + os.pathsep + "/usr/bin:/bin",
            "HOME": self.dir,
            "FAKE_DIR": self.dir,
            "DOCKWEAVE_CLI": os.path.join(self.bin, "dockweave"),
            "PAPERCLIPAI_BIN": os.path.join(self.bin, "paperclipai"),
            "DOCKWEAVE_PIPELINE_STATE_DIR": os.path.join(self.dir, "state"),
            "PAPERCLIP_COMPANY_ID": "co-1",
        }
        env.update(extra)
        return env

    def py(self, script, *args, stdin="", **extra):
        return subprocess.run([sys.executable, "-I", os.path.join(DIR, script)] + list(args),
                              input=stdin, capture_output=True, text=True, env=self.env(**extra), timeout=60)

    def close(self):
        self.tmp.cleanup()


def adm(agent, agent_id, allowed, edges=(), action=None):
    return {"agent": agent, "agent_id": agent_id, "allowed": allowed,
            "action": action or ("allow" if allowed else "deny"), "edges": list(edges)}


BLOCKED_EDGE = {"edge_id": "e1", "gate_id": "tests", "upstream_agent_id": "ag_a", "satisfied": False,
                "code": "failed", "run_id": "gate-9", "action": "rework", "reason": "fix tests", "message": "tests red"}


class PipelineHelperTest(unittest.TestCase):
    def setUp(self):
        self.f = Fakes()

    def tearDown(self):
        self.f.close()

    def test_precheck_allowed(self):
        self.f.set("dw", {"admissions": {"beta": adm("beta", "ag_b", True)}})
        p = self.f.py("pipeline.py", "precheck", "beta", "--issue", "DOC-1")
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertIn("ALLOWED", p.stdout)
        self.assertEqual(self.f.log("pc"), [])

    def test_precheck_blocked_comments_once(self):
        self.f.set("dw", {"admissions": {"beta": adm("beta", "ag_b", False, [BLOCKED_EDGE], "rework")}})
        for _ in range(2):
            p = self.f.py("pipeline.py", "precheck", "beta", "--issue", "DOC-1")
            self.assertEqual(p.returncode, 3, p.stderr)
            self.assertIn("Gate tests: failed", p.stdout)
        calls = [json.loads(l) for l in self.f.log("pc")]
        self.assertEqual(len(calls), 2)
        self.assertIn("--comment", calls[0])
        self.assertNotIn("--comment", calls[1], "second heartbeat must not repeat the comment")
        for c in calls:
            self.assertEqual(c[:3], ["issue", "update", "DOC-1"])
            self.assertIn("todo", c)
            self.assertNotIn("--blocked-by", c)

    def test_precheck_daemon_down_is_blocked(self):
        self.f.set("dw", {"down": True, "admissions": {}})
        p = self.f.py("pipeline.py", "precheck", "beta")
        self.assertEqual(p.returncode, 3)
        self.assertIn("unavailable", p.stdout)

    def test_precheck_invalid_answer_is_blocked(self):
        self.f.set("dw", {"admissions": {"beta": "garbage"}})
        self.assertEqual(self.f.py("pipeline.py", "precheck", "beta").returncode, 3)

    def test_precheck_missing_cli_is_blocked(self):
        p = self.f.py("pipeline.py", "precheck", "beta", DOCKWEAVE_CLI="/nonexistent/dockweave")
        self.assertEqual(p.returncode, 3)

    def test_handle_409(self):
        body = {"error": {"code": "pipeline_blocked", "type": "pipeline_blocked", "gate_id": "tests",
                          "action": "deny", "reasons": [dict(BLOCKED_EDGE, code="stale")]},
                "admission": adm("beta", "ag_b", False, [dict(BLOCKED_EDGE, code="stale")])}
        p = self.f.py("pipeline.py", "handle-409", "beta", "--issue", "DOC-1", stdin=json.dumps(body))
        self.assertEqual(p.returncode, 3, p.stderr)
        self.assertIn("stale", p.stdout)
        ok = {"choices": [{"message": {"content": "hi"}}]}
        self.assertEqual(self.f.py("pipeline.py", "handle-409", "beta", stdin=json.dumps(ok)).returncode, 1)
        self.assertEqual(self.f.py("pipeline.py", "handle-409", "beta", stdin="not json").returncode, 1)

    def _route_setup(self, beta_adm):
        self.f.set("dw", {
            "admissions": {"alpha": adm("alpha", "ag_a", True), "ag_b": beta_adm},
            "state": {"revision": 4, "edges": [{"id": "e1", "gate_id": "tests", "upstream_agent_id": "ag_a",
                                                "downstream_agent_id": "ag_b", "workspace": "/ws"}]},
        })
        amap = os.path.join(self.f.dir, "map.json")
        with open(amap, "w") as fh:
            json.dump({"ag_a": "pc-alpha", "beta": "pc-beta", "gamma": "pc-gamma"}, fh)
        self.f.set("pc", {"issues": {"DOC-1": {"companyId": "co-1", "parentId": "P-1"}}, "list": []})
        return amap

    def test_route_allow_wakes_downstream(self):
        amap = self._route_setup(adm("beta", "ag_b", True, [dict(BLOCKED_EDGE, satisfied=True, code="ok", action="allow")]))
        p = self.f.py("pipeline.py", "route", "--upstream", "alpha", "--issue", "DOC-1", "--run-json", "-",
                      stdin='{"id":"gate-9"}', DOCKWEAVE_PAPERCLIP_AGENT_MAP=amap)
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertIn("WAKE\tbeta\tallow", p.stdout)
        wakes = [json.loads(l) for l in self.f.log("pc") if '"wake"' in l]
        self.assertEqual(wakes[0][:3], ["agent", "wake", "pc-beta"])

    def test_route_wake_action_targets_named_agent(self):
        amap = self._route_setup(adm("beta", "ag_b", True, [], action="wake:gamma"))
        p = self.f.py("pipeline.py", "route", "--upstream", "alpha", "--issue", "DOC-1",
                      DOCKWEAVE_PAPERCLIP_AGENT_MAP=amap)
        self.assertIn("WAKE\tgamma\twake:gamma", p.stdout)

    def test_route_rework_is_idempotent(self):
        amap = self._route_setup(adm("beta", "ag_b", False, [BLOCKED_EDGE], "rework"))
        for _ in range(2):
            p = self.f.py("pipeline.py", "route", "--upstream", "alpha", "--issue", "DOC-1",
                          DOCKWEAVE_PAPERCLIP_AGENT_MAP=amap)
            self.assertIn("REWORK\ttests", p.stdout, p.stderr)
        creates = [json.loads(l) for l in self.f.log("pc") if '"create"' in l]
        self.assertEqual(len(creates), 1, "exactly one rework issue per gate run")
        c = creates[0]
        self.assertIn("pc-alpha", c)
        self.assertIn("P-1", c)
        self.assertTrue(any("[gate tests / run gate-9]" in x for x in c))
        # An existing issue with the tag is found even without the local marker.
        os.system("rm -rf %s" % os.path.join(self.f.dir, "state"))
        self.f.set("pc", {"issues": {"DOC-1": {"companyId": "co-1"}},
                          "list": [{"id": "x", "identifier": "DOC-50", "title": "Rework: Gate tests fehlgeschlagen [gate tests / run gate-9]"}]})
        p = self.f.py("pipeline.py", "route", "--upstream", "alpha", "--issue", "DOC-1", DOCKWEAVE_PAPERCLIP_AGENT_MAP=amap)
        self.assertIn("REWORK\ttests\tDOC-50", p.stdout)
        self.assertEqual(len([l for l in self.f.log("pc") if '"create"' in l]), 1)

    def test_route_deny_reports_without_wake(self):
        amap = self._route_setup(adm("beta", "ag_b", False, [dict(BLOCKED_EDGE, action="deny", code="failed")]))
        p = self.f.py("pipeline.py", "route", "--upstream", "alpha", "--issue", "DOC-1", DOCKWEAVE_PAPERCLIP_AGENT_MAP=amap)
        self.assertIn("DENY\tbeta\ttests\tfailed", p.stdout)
        self.assertFalse([l for l in self.f.log("pc") if '"wake"' in l or '"create"' in l])

    def test_route_unmapped_agent_is_not_guessed(self):
        self._route_setup(adm("beta", "ag_b", True))
        p = self.f.py("pipeline.py", "route", "--upstream", "alpha", "--issue", "DOC-1")
        self.assertIn("UNMAPPED\tbeta", p.stdout)
        self.assertFalse([l for l in self.f.log("pc") if '"wake"' in l])

    def test_route_no_edges(self):
        self.f.set("dw", {"admissions": {"alpha": adm("alpha", "ag_a", True)}, "state": {"revision": 1, "edges": []}})
        p = self.f.py("pipeline.py", "route", "--upstream", "alpha")
        self.assertIn("NONE", p.stdout)

    def test_route_unknown_upstream_without_edges_is_none(self):
        # Legacy dockweave-<cid> targets are unknown to the daemon; with no
        # pipeline edges there is nothing to route and no error to report.
        self.f.set("dw", {"admissions": {}, "state": {"revision": 1, "edges": []}})
        p = self.f.py("pipeline.py", "route", "--upstream", "dockweave-abc123")
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertTrue(p.stdout.startswith("NONE"), p.stdout)

    def test_route_unknown_upstream_with_edges_is_error(self):
        amap = self._route_setup(adm("beta", "ag_b", True))
        p = self.f.py("pipeline.py", "route", "--upstream", "ghost", "--issue", "DOC-1",
                      DOCKWEAVE_PAPERCLIP_AGENT_MAP=amap)
        self.assertEqual(p.returncode, 1)
        self.assertIn("ERR", p.stdout)
        self.assertFalse([l for l in self.f.log("pc") if '"wake"' in l or '"create"' in l])

    def test_route_accepts_edges_without_upstream(self):
        amap = self._route_setup(adm("beta", "ag_b", True, [dict(BLOCKED_EDGE, satisfied=True, code="ok", action="allow")]))
        cfg = json.load(open(os.path.join(self.f.dir, "dw.json")))
        cfg["state"]["edges"].append({"id": "e0", "gate_id": "lint", "downstream_agent_id": "ag_c", "workspace": "/ws"})
        self.f.set("dw", cfg)
        p = self.f.py("pipeline.py", "route", "--upstream", "alpha", "--issue", "DOC-1",
                      DOCKWEAVE_PAPERCLIP_AGENT_MAP=amap)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertIn("WAKE\tbeta\tallow", p.stdout)

    def test_route_rejects_malformed_edges(self):
        amap = self._route_setup(adm("beta", "ag_b", True))
        for bad in (None, "x", {"id": "e", "downstream_agent_id": ""}, {"id": "e", "downstream_agent_id": "d", "upstream_agent_id": 7}):
            cfg = json.load(open(os.path.join(self.f.dir, "dw.json")))
            cfg["state"]["edges"] = [bad]
            self.f.set("dw", cfg)
            p = self.f.py("pipeline.py", "route", "--upstream", "alpha", "--issue", "DOC-1",
                          DOCKWEAVE_PAPERCLIP_AGENT_MAP=amap)
            self.assertEqual(p.returncode, 1, (bad, p.stdout))
            self.assertIn("invalid pipeline edge", p.stdout)


class BlockedCheckTest(unittest.TestCase):
    def setUp(self):
        self.f = Fakes()

    def tearDown(self):
        self.f.close()

    def test_usage_error(self):
        self.assertEqual(self.f.py("blocked-check.py").returncode, 2)

    def test_no_blockers(self):
        self.f.set("pc", {"issues": {"I1": {"status": "todo"}}})
        self.assertEqual(self.f.py("blocked-check.py", "I1").returncode, 0)

    def test_open_blocker(self):
        self.f.set("pc", {"issues": {"I1": {"blockedByIssueIds": ["I2"]}, "I2": {"status": "in_progress"}}})
        p = self.f.py("blocked-check.py", "I1")
        self.assertEqual(p.returncode, 1)
        self.assertIn("I2=in_progress", p.stdout)

    def test_done_blocker(self):
        self.f.set("pc", {"issues": {"I1": {"blockedBy": [{"id": "I2"}]}, "I2": {"status": "done"}}})
        self.assertEqual(self.f.py("blocked-check.py", "I1").returncode, 0)

    def test_cli_failure_fails_closed(self):
        self.f.set("pc", {"fail": True})
        p = self.f.py("blocked-check.py", "I1")
        self.assertEqual(p.returncode, 1)
        self.assertIn("treating I1 as blocked", p.stderr)

    def test_invalid_json_fails_closed(self):
        self.f.set("pc", {"issues": {"I1": "not json"}})
        self.assertEqual(self.f.py("blocked-check.py", "I1").returncode, 1)

    def test_unreadable_blocker_fails_closed(self):
        self.f.set("pc", {"issues": {"I1": {"blockedByIssueIds": ["GONE"]}}})
        self.assertEqual(self.f.py("blocked-check.py", "I1").returncode, 1)

    def test_missing_cli_fails_closed(self):
        p = self.f.py("blocked-check.py", "I1", PAPERCLIPAI_BIN="", PATH="/usr/bin:/bin")
        self.assertEqual(p.returncode, 1)

    def test_fail_open_opt_out(self):
        self.f.set("pc", {"fail": True})
        self.assertEqual(self.f.py("blocked-check.py", "I1", DOCKWEAVE_BLOCKED_CHECK_FAIL_OPEN="1").returncode, 0)


class WrapperTest(unittest.TestCase):
    """dockweave-run.sh with DOCKWEAVE_DEFAULT_TASK (no Paperclip inbox)."""

    def setUp(self):
        self.f = Fakes()
        self.f.set("curl", {"response": {"choices": [{"message": {"content": "erledigt\nSTATUS: IN_PROGRESS"}}]}})

    def tearDown(self):
        self.f.close()

    def run_wrapper(self, **extra):
        env = self.f.env(DOCKWEAVE_DEFAULT_TASK="Bitte X", DOCKWEAVE_PAPERCLIP_HELPER_DIR=DIR,
                         DOCKWEAVE_PAPERCLIP_ENV=os.path.join(self.f.dir, "none.env"), **extra)
        return subprocess.run(["sh", os.path.join(DIR, "dockweave-run.sh"), "beta"], capture_output=True,
                              text=True, env=env, timeout=120)

    def endpoint_calls(self):
        return [l for l in self.f.log("curl") if "/v1/chat/completions" in l]

    def test_allowed_agent_gets_the_task(self):
        self.f.set("dw", {"admissions": {"beta": adm("beta", "ag_b", True)}})
        p = self.run_wrapper()
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertEqual(len(self.endpoint_calls()), 1)
        self.assertIn("erledigt", p.stdout)

    def test_blocked_agent_spends_no_turn(self):
        self.f.set("dw", {"admissions": {"beta": adm("beta", "ag_b", False, [BLOCKED_EDGE])}})
        p = self.run_wrapper()
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertEqual(self.endpoint_calls(), [])
        self.assertIn("pipeline blocked", p.stderr)

    def test_daemon_down_spends_no_turn(self):
        self.f.set("dw", {"down": True, "admissions": {}})
        p = self.run_wrapper()
        self.assertEqual(p.returncode, 0)
        self.assertEqual(self.endpoint_calls(), [])

    def test_endpoint_409_is_handled(self):
        self.f.set("dw", {"admissions": {"beta": adm("beta", "ag_b", True)}})
        self.f.set("curl", {"response": {"error": {"code": "pipeline_blocked", "type": "pipeline_blocked",
                                                   "reasons": [dict(BLOCKED_EDGE, code="stale")]}}})
        p = self.run_wrapper()
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertIn("pipeline_blocked", p.stderr)
        self.assertNotIn("invalid endpoint response", p.stderr)

    def test_failing_pre_run_gate_prevents_turn(self):
        ws = os.path.join(self.f.dir, "ws")
        os.makedirs(ws)
        self.f.set("dw", {"admissions": {"beta": adm("beta", "ag_b", True)},
                          "gate_run": {"status": "failed", "id": "g", "results": [{"id": "pre", "status": "failed"}]}})
        p = self.run_wrapper(DOCKWEAVE_HOST_WORKSPACE=ws)
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertEqual(self.endpoint_calls(), [])
        self.assertTrue(any("--phase pre_run" in l for l in self.f.log("dw")))

    def test_no_user_specific_defaults(self):
        with open(os.path.join(DIR, "dockweave-run.sh")) as fh:
            body = fh.read()
        for f in os.listdir(DIR):
            if f.endswith(".py"):
                with open(os.path.join(DIR, f)) as fh:
                    body += fh.read()
        self.assertNotIn("/home/krusty", body)
        self.assertNotIn("crystal-run", body)


if __name__ == "__main__":
    unittest.main()
