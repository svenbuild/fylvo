package drivetest

import (
	"errors"
	"strings"
)

var errQuery = errors.New("drivetest: query not understood")

// parseQuery reads the forms of Drive's query language that are used here: terms joined
// by "and", each one of
//
//	'<id>' in parents
//	trashed = true|false
//	name = '<name>'        name != '<name>'
//	mimeType = '<type>'    mimeType != '<type>'
//
// Strings escape a backslash and a single quote with a backslash, as at Drive. An empty
// query matches every entry. Anything else is an error, as Drive answers 400 to it.
func parseQuery(q string) (func(*entry) bool, error) {
	tokens, err := tokenize(q)
	if err != nil {
		return nil, err
	}
	var terms []func(*entry) bool
	for len(tokens) > 0 {
		if len(terms) > 0 {
			if tokens[0] != (token{word: "and"}) {
				return nil, errQuery
			}
			tokens = tokens[1:]
		}
		if len(tokens) < 3 {
			return nil, errQuery
		}
		term, err := parseTerm(tokens[0], tokens[1], tokens[2])
		if err != nil {
			return nil, err
		}
		terms = append(terms, term)
		tokens = tokens[3:]
	}
	return func(e *entry) bool {
		for _, term := range terms {
			if !term(e) {
				return false
			}
		}
		return true
	}, nil
}

func parseTerm(left, op, right token) (func(*entry) bool, error) {
	if left.quoted && op == (token{word: "in"}) && right == (token{word: "parents"}) {
		return func(e *entry) bool { return e.parent == left.word }, nil
	}
	if left.quoted || (op.word != "=" && op.word != "!=") {
		return nil, errQuery
	}
	equal := op.word == "="

	switch left.word {
	case "trashed":
		if right.quoted || (right.word != "true" && right.word != "false") {
			return nil, errQuery
		}
		want := right.word == "true"
		return func(e *entry) bool { return (e.trashed == want) == equal }, nil
	case "name":
		if !right.quoted {
			return nil, errQuery
		}
		// Without regard to case, so that callers must check names themselves.
		return func(e *entry) bool { return strings.EqualFold(e.name, right.word) == equal }, nil
	case "mimeType":
		if !right.quoted {
			return nil, errQuery
		}
		return func(e *entry) bool { return (e.mimeType == right.word) == equal }, nil
	}
	return nil, errQuery
}

// token is a word, an operator or a quoted string with its escapes resolved.
type token struct {
	word   string
	quoted bool
}

func tokenize(q string) ([]token, error) {
	var tokens []token
	for i := 0; i < len(q); {
		switch c := q[i]; {
		case c == ' ':
			i++
		case c == '\'':
			var b strings.Builder
			i++
			for {
				if i >= len(q) {
					return nil, errQuery
				}
				if q[i] == '\'' {
					i++
					break
				}
				if q[i] == '\\' {
					if i+1 >= len(q) || (q[i+1] != '\\' && q[i+1] != '\'') {
						return nil, errQuery
					}
					i++
				}
				b.WriteByte(q[i])
				i++
			}
			tokens = append(tokens, token{word: b.String(), quoted: true})
		case c == '=':
			tokens = append(tokens, token{word: "="})
			i++
		case c == '!' && i+1 < len(q) && q[i+1] == '=':
			tokens = append(tokens, token{word: "!="})
			i += 2
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			j := i
			for j < len(q) && (q[j] >= 'a' && q[j] <= 'z' || q[j] >= 'A' && q[j] <= 'Z') {
				j++
			}
			tokens = append(tokens, token{word: q[i:j]})
			i = j
		default:
			return nil, errQuery
		}
	}
	return tokens, nil
}
