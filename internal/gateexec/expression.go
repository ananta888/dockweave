package gateexec

import (
	"fmt"
	"strconv"
	"strings"
)

// ExprEnv is the variable environment of a gate expression.
type ExprEnv struct {
	// Exit is the command's exit code (variables `command` and `exit`).
	Exit int
	// Budget is the day's cost in USD (variable `budget`). It may only be
	// read when BudgetAvailable is true.
	Budget          float64
	BudgetAvailable bool
	// BudgetUnavailable explains why the budget cannot be used; it becomes
	// part of the evaluation error.
	BudgetUnavailable string
}

// EvaluateExpression evaluates a small deterministic gate predicate over the
// command exit code and the daily budget cost, treating the budget as
// available. See Evaluate for the grammar.
func EvaluateExpression(expr string, exit int, budget float64) (bool, error) {
	return Evaluate(expr, ExprEnv{Exit: exit, Budget: budget, BudgetAvailable: true})
}

// Evaluate evaluates a gate predicate. Supported variables:
//
//	command | exit  int     the command's exit code
//	budget          float   the day's cost in USD; referencing it while no
//	                        budget snapshot is available is an error
//
// The literals true and false stand for 1 and 0.
//
// Supported operators: == != < <= > >= && || ! unary minus and parentheses.
// A bare number or variable is treated as a boolean (non-zero = true). Both
// sides of && and || are always evaluated, so an unavailable budget fails the
// expression even where short-circuiting would have skipped it (fail-closed).
func Evaluate(expr string, env ExprEnv) (bool, error) {
	p := &exprParser{src: expr, env: env}
	return p.run()
}

// Validation is the result of a static expression check.
type Validation struct {
	// UsesBudget is true when the expression reads the budget variable, i.e.
	// it needs a configured budget to evaluate at run time.
	UsesBudget bool
}

// Validate checks syntax and variable names without needing real values; the
// budget counts as available. Editors use it to reject an expression before
// it is saved.
func Validate(expr string) (Validation, error) {
	if strings.TrimSpace(expr) == "" {
		return Validation{}, fmt.Errorf("expression is empty")
	}
	p := &exprParser{src: expr, validate: true}
	if _, err := p.run(); err != nil {
		return Validation{}, err
	}
	return Validation{UsesBudget: p.usesBudget}, nil
}

const MaxExpressionBytes = 16 << 10
const MaxExpressionTokens = 2048
const MaxExpressionDepth = 128

func (p *exprParser) run() (bool, error) {
	if len(p.src) > MaxExpressionBytes {
		return false, fmt.Errorf("expression exceeds %d bytes", MaxExpressionBytes)
	}
	p.lex()
	if len(p.toks) > MaxExpressionTokens {
		return false, fmt.Errorf("expression exceeds %d tokens", MaxExpressionTokens)
	}
	if len(p.toks) == 0 {
		return false, fmt.Errorf("expression is empty")
	}
	v, err := p.parseOr()
	if err != nil {
		return false, err
	}
	if p.pos != len(p.toks) {
		return false, fmt.Errorf("unexpected token %q", p.toks[p.pos].text)
	}
	return v, nil
}

type exprTok struct {
	kind string // "num", "ident", "op", "lparen", "rparen"
	text string
}

type exprParser struct {
	src   string
	toks  []exprTok
	pos   int
	depth int
	env   ExprEnv
	// validate makes variable reads succeed without values (see Validate).
	validate   bool
	usesBudget bool
}

func (p *exprParser) lex() {
	s := p.src
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(':
			p.toks = append(p.toks, exprTok{kind: "lparen", text: "("})
			i++
		case c == ')':
			p.toks = append(p.toks, exprTok{kind: "rparen", text: ")"})
			i++
		case c == '&' && i+1 < len(s) && s[i+1] == '&':
			p.toks = append(p.toks, exprTok{kind: "op", text: "&&"})
			i += 2
		case c == '|' && i+1 < len(s) && s[i+1] == '|':
			p.toks = append(p.toks, exprTok{kind: "op", text: "||"})
			i += 2
		case c == '=' && i+1 < len(s) && s[i+1] == '=':
			p.toks = append(p.toks, exprTok{kind: "op", text: "=="})
			i += 2
		case c == '!' && i+1 < len(s) && s[i+1] == '=':
			p.toks = append(p.toks, exprTok{kind: "op", text: "!="})
			i += 2
		case c == '<' && i+1 < len(s) && s[i+1] == '=':
			p.toks = append(p.toks, exprTok{kind: "op", text: "<="})
			i += 2
		case c == '>' && i+1 < len(s) && s[i+1] == '=':
			p.toks = append(p.toks, exprTok{kind: "op", text: ">="})
			i += 2
		case c == '<':
			p.toks = append(p.toks, exprTok{kind: "op", text: "<"})
			i++
		case c == '>':
			p.toks = append(p.toks, exprTok{kind: "op", text: ">"})
			i++
		case c == '!':
			p.toks = append(p.toks, exprTok{kind: "op", text: "!"})
			i++
		case c >= '0' && c <= '9':
			j := i
			for j < len(s) && ((s[j] >= '0' && s[j] <= '9') || s[j] == '.') {
				j++
			}
			p.toks = append(p.toks, exprTok{kind: "num", text: s[i:j]})
			i = j
		case isIdentChar(c):
			j := i
			for j < len(s) && isIdentChar(s[j]) {
				j++
			}
			p.toks = append(p.toks, exprTok{kind: "ident", text: s[i:j]})
			i = j
		default:
			p.toks = append(p.toks, exprTok{kind: "op", text: string(c)})
			i++
		}
	}
}

func isIdentChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func (p *exprParser) parseOr() (bool, error) {
	l, err := p.parseAnd()
	if err != nil {
		return false, err
	}
	for p.peekOp("||") {
		p.pos++
		r, err := p.parseAnd()
		if err != nil {
			return false, err
		}
		l = l || r
	}
	return l, nil
}

func (p *exprParser) parseAnd() (bool, error) {
	l, err := p.parseNot()
	if err != nil {
		return false, err
	}
	for p.peekOp("&&") {
		p.pos++
		r, err := p.parseNot()
		if err != nil {
			return false, err
		}
		l = l && r
	}
	return l, nil
}

func (p *exprParser) parseNot() (bool, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > MaxExpressionDepth {
		return false, fmt.Errorf("expression exceeds nesting limit")
	}
	if p.peekOp("!") {
		p.pos++
		v, err := p.parseNot()
		if err != nil {
			return false, err
		}
		return !v, nil
	}
	return p.parsePrimary()
}

func (p *exprParser) parsePrimary() (bool, error) {
	if p.pos >= len(p.toks) {
		return false, fmt.Errorf("unexpected end of expression")
	}
	if p.toks[p.pos].kind == "lparen" {
		p.pos++
		v, err := p.parseOr()
		if err != nil {
			return false, err
		}
		if p.pos >= len(p.toks) || p.toks[p.pos].kind != "rparen" {
			return false, fmt.Errorf("missing closing parenthesis")
		}
		p.pos++
		return v, nil
	}
	return p.parseComparison()
}

func (p *exprParser) parseComparison() (bool, error) {
	if p.pos >= len(p.toks) {
		return false, fmt.Errorf("unexpected end of expression")
	}
	left, err := p.parseOperand()
	if err != nil {
		return false, err
	}
	if p.pos < len(p.toks) && isCmpOp(p.toks[p.pos].text) {
		op := p.toks[p.pos].text
		p.pos++
		right, err := p.parseOperand()
		if err != nil {
			return false, err
		}
		return compare(op, left, right)
	}
	return left.num != 0, nil
}

type operand struct {
	num float64
}

func (p *exprParser) parseOperand() (operand, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > MaxExpressionDepth {
		return operand{}, fmt.Errorf("expression exceeds nesting limit")
	}
	if p.pos >= len(p.toks) {
		return operand{}, fmt.Errorf("unexpected end of expression")
	}
	t := p.toks[p.pos]
	if t.kind == "op" && t.text == "-" {
		p.pos++
		v, err := p.parseOperand()
		if err != nil {
			return operand{}, err
		}
		return operand{num: -v.num}, nil
	}
	switch t.kind {
	case "num":
		f, err := strconv.ParseFloat(t.text, 64)
		if err != nil {
			return operand{}, fmt.Errorf("invalid number %q", t.text)
		}
		p.pos++
		return operand{num: f}, nil
	case "ident":
		p.pos++
		switch t.text {
		case "true":
			return operand{num: 1}, nil
		case "false":
			return operand{num: 0}, nil
		case "command", "exit":
			return operand{num: float64(p.env.Exit)}, nil
		case "budget":
			p.usesBudget = true
			if p.validate {
				return operand{}, nil
			}
			if !p.env.BudgetAvailable {
				why := p.env.BudgetUnavailable
				if why == "" {
					why = "no budget snapshot"
				}
				return operand{}, fmt.Errorf("variable budget is unavailable (%s)", why)
			}
			return operand{num: p.env.Budget}, nil
		default:
			return operand{}, fmt.Errorf("unknown variable %q", t.text)
		}
	default:
		return operand{}, fmt.Errorf("unexpected token %q", t.text)
	}
}

func isCmpOp(s string) bool {
	switch s {
	case "==", "!=", "<", "<=", ">", ">=":
		return true
	}
	return false
}

func compare(op string, a, b operand) (bool, error) {
	switch op {
	case "==":
		return a.num == b.num, nil
	case "!=":
		return a.num != b.num, nil
	case "<":
		return a.num < b.num, nil
	case "<=":
		return a.num <= b.num, nil
	case ">":
		return a.num > b.num, nil
	case ">=":
		return a.num >= b.num, nil
	}
	return false, fmt.Errorf("unknown operator %q", op)
}

func (p *exprParser) peekOp(s string) bool {
	return p.pos < len(p.toks) && p.toks[p.pos].kind == "op" && p.toks[p.pos].text == s
}
