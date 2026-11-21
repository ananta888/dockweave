package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// RF-033: structural guard for the enforcement points of ADR 0002.
//   - Only the OpenAI chat handler and the herdr prompt admit pipeline work.
//   - Every path that prompts an agent (Sessions.Prompt, Native.Prompt) is
//     reached only through those two handlers.
//   - Start/stop/restart and the terminal are documented exceptions and do
//     not call the admission.
// A new prompt path or a new admission call site makes this test fail, so
// the decision is revisited instead of drifting silently.
func TestPipelineEnforcementPoints(t *testing.T) {
	calls := callGraph(t)
	if got := callersOf(calls, "admitPipelineWork"); strings.Join(got, ",") != "herdrPrompt,openAIChatCompletions" {
		t.Fatalf("admitPipelineWork called from %v", got)
	}
	// Who prompts an agent, transitively, must be one of the two handlers.
	prompting := map[string]bool{}
	for fn, callees := range calls {
		for c := range callees {
			if c == "Sessions.Prompt" || c == "Native.Prompt" {
				prompting[fn] = true
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for fn, callees := range calls {
			for c := range callees {
				if prompting[c] && !prompting[fn] {
					prompting[fn] = true
					changed = true
				}
			}
		}
	}
	handlers := []string{}
	for fn := range prompting {
		if len(callersOf(calls, fn)) == 0 { // entry points (HTTP handlers)
			handlers = append(handlers, fn)
		}
	}
	sort.Strings(handlers)
	if strings.Join(handlers, ",") != "herdrPrompt,openAIChatCompletions" {
		t.Fatalf("agents are prompted from unguarded entry points: %v", handlers)
	}
	for _, exempt := range []string{"stopAgent", "agentAction"} {
		if calls[exempt]["admitPipelineWork"] {
			t.Fatalf("%s must not be gated by the pipeline (documented exception)", exempt)
		}
	}
}

// callGraph maps each function/method name in package api (non-test files)
// to the names it calls; method calls on fields are recorded as Field.Method.
func callGraph(t *testing.T) map[string]map[string]bool {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	out := map[string]map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			callees := map[string]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch f := call.Fun.(type) {
				case *ast.Ident:
					callees[f.Name] = true
				case *ast.SelectorExpr:
					callees[f.Sel.Name] = true
					if inner, ok := f.X.(*ast.SelectorExpr); ok {
						callees[inner.Sel.Name+"."+f.Sel.Name] = true
					}
				}
				return true
			})
			out[fn.Name.Name] = callees
		}
	}
	return out
}

func callersOf(calls map[string]map[string]bool, name string) []string {
	var out []string
	for fn, callees := range calls {
		if callees[name] {
			out = append(out, fn)
		}
	}
	sort.Strings(out)
	return out
}
