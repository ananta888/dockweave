package gateexec

import (
	"fmt"
	"strconv"
)

// EvaluateExpression evaluates a small deterministic gate predicate over the
// command exit code and the daily budget cost. Supported variables:
//
//	command | exit  int     the command's exit code
//	budget          float   the day's cost in USD (0 when no budget is configured)
//
// Supported operators: == != < <= > >= && || ! and parentheses. A bare number
// or variable is treated as a boolean (non-zero = true).
func EvaluateExpression(expr string, exit int, budget float64) (bool, error) {
	p := &exprParser{src: expr, exit: exit, budget: budget}
	p.lex()
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
	src    string
	toks   []exprTok
	pos    int
	exit   int
	budget float64
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
	if p.pos >= len(p.toks) {
		return operand{}, fmt.Errorf("unexpected end of expression")
	}
	t := p.toks[p.pos]
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
		case "command", "exit":
			return operand{num: float64(p.exit)}, nil
		case "budget":
			return operand{num: p.budget}, nil
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
