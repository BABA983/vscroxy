package inspect

import (
	"context"
	jsonv1 "encoding/json"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/itchyny/gojq"
	"github.com/ohler55/ojg/jp"
)

const (
	queryInputLimit      = 1 << 20
	queryExpressionLimit = 4096
	queryOutputLimit     = 2 << 20
	queryResultLimit     = 4096
	queryDepthLimit      = 64
	queryNodeLimit       = 16384
	queryWorkLimit       = 100000
	queryNumberLimit     = 1024
	queryTimeout         = time.Second
)

// Query evaluates one JSON document, without external inputs or side effects.
// Call it on a worker, not the UI thread. It is synchronous and does not leave
// worker goroutines behind. Errors discard all partial output.
//
// JSONPath is a deliberately restricted OjG/Goessner dialect, NOT RFC 9535:
// root $, names, indexes, slices, unions, wildcards, one recursive descent,
// and boolean/comparison filters on singular @ paths. Filter arithmetic,
// functions, nested queries and root references are rejected. Filters require
// signed 64-bit integers; larger integers remain lossless in selections but
// require jq for filtering. Mixed floats and integers outside ±2^53 are rejected
// in JSONPath filters rather than compared imprecisely.
//
// jq uses gojq, with a bounded, read-only subset: no definitions, updates,
// multiplication, binary subtraction, formats, interpolation, reduce/foreach,
// or unlisted functions (see prepareJQ). Integer lexemes use gojq's arbitrary-
// precision arithmetic; decimal/exponent arithmetic follows float64 semantics.
//
// JSONPath always returns a pretty JSON array of matches (including []); jq
// returns pretty values separated by newlines, or "" for an empty stream.
// Neither has a trailing newline. Input must be strict UTF-8 JSON with unique
// object names. Limits: 1 MiB input, 4096-byte expression, 2 MiB output, 4096
// results, depth 64, 16384 nodes per value, 1024-byte numbers, 100000 work units,
// and a one-second execution context (or the caller's earlier cancellation).
func Query(ctx context.Context, dialect, expression, input string) (output string, err error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if dialect != "JSONPath" && dialect != "jq" {
		return "", errors.New("choose JSONPath or jq")
	}
	if len(input) > queryInputLimit {
		return "", queryLimit("input")
	}
	if len(expression) > queryExpressionLimit {
		return "", queryLimit("expression")
	}
	if strings.TrimSpace(expression) == "" {
		return "", errors.New("enter a query expression")
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	b := &queryBudget{ctx: ctx}
	// OjG's Walk API cannot return an error. Only our private stop value is
	// recovered here; execution is unwound, not abandoned in a goroutine.
	defer func() {
		if p := recover(); p != nil {
			if stop, ok := p.(queryStop); ok {
				output, err = "", stop.err
			} else {
				panic(p)
			}
		}
	}()
	value, err := decodeQueryInput(b, input)
	if err != nil {
		return "", err
	}
	w := &queryWriter{budget: b}
	enc := jsontext.NewEncoder(w, jsontext.WithIndent("  "))
	if dialect == "JSONPath" {
		path, err := preparePath(expression, value)
		if err != nil {
			return "", err
		}
		for i, frag := range path {
			path[i] = queryPathGuard{Frag: frag, budget: b}
		}
		if err := enc.WriteToken(jsontext.BeginArray); err != nil {
			return "", err
		}
		count := 0
		path.Walk(value, func(_ jp.Expr, nodes []any) {
			count++
			if count > queryResultLimit {
				panic(queryStop{queryLimit("results")})
			}
			// Include the result-list envelope in the depth bound.
			if err := b.checkValue([]any{nodes[len(nodes)-1]}); err != nil {
				panic(queryStop{err})
			}
			if err := json.MarshalEncode(enc, nodes[len(nodes)-1], json.Deterministic(true)); err != nil {
				panic(queryStop{err})
			}
		})
		if err := enc.WriteToken(jsontext.EndArray); err != nil {
			return "", err
		}
	} else {
		q, err := gojq.Parse(expression)
		if err != nil {
			return "", errors.New("invalid jq syntax")
		}
		if err := prepareJQ(reflect.ValueOf(q), 0, new(int)); err != nil {
			return "", err
		}
		code, err := gojq.Compile(q, gojq.WithFunction("_inspect_guard", 0, 0, func(v any, _ []any) any {
			if err := b.checkValue(v); err != nil {
				b.err = err
				cancel() // A try/optional cannot swallow the work/shape limit.
				return err
			}
			return v
		}))
		if err != nil {
			return "", errors.New("invalid or unsupported jq expression")
		}
		iter := code.RunWithContext(ctx, value)
		for count := 0; ; count++ {
			if err := b.check(); err != nil {
				return "", err
			}
			v, ok := iter.Next()
			if b.err != nil {
				return "", b.err
			}
			if !ok {
				break
			}
			if _, ok := v.(error); ok {
				if ctx.Err() != nil {
					return "", ctx.Err()
				}
				// Do not return errors containing document contents.
				return "", errors.New("jq evaluation failed")
			}
			if count == queryResultLimit {
				return "", queryLimit("results")
			}
			if err := b.checkValue(v); err != nil {
				return "", err
			}
			if err := json.MarshalEncode(enc, v, json.Deterministic(true)); err != nil {
				if b.err != nil {
					return "", b.err
				}
				return "", errors.New("cannot encode query result")
			}
		}
	}
	if err := b.check(); err != nil {
		return "", err
	}
	return strings.TrimSuffix(w.String(), "\n"), nil
}

func queryLimit(kind string) error { return fmt.Errorf("query limit exceeded: %s", kind) }

type queryBudget struct {
	ctx  context.Context
	work int
	err  error
}

func (b *queryBudget) check() error {
	if b.err != nil {
		return b.err
	}
	if err := b.ctx.Err(); err != nil {
		return err
	}
	b.work++
	if b.work > queryWorkLimit {
		return queryLimit("work")
	}
	return nil
}

func decodeQueryInput(b *queryBudget, input string) (any, error) {
	d := jsontext.NewDecoder(strings.NewReader(input))
	nodes := 0
	for {
		if err := b.check(); err != nil {
			return nil, err
		}
		tok, err := d.ReadToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errors.New("invalid JSON input")
		}
		nodes++
		if nodes > queryNodeLimit {
			return nil, queryLimit("input nodes")
		}
		if d.StackDepth() > queryDepthLimit {
			return nil, queryLimit("input depth")
		}
		if tok.Kind() == '0' && len(tok.String()) > queryNumberLimit {
			return nil, queryLimit("number")
		}
	}
	var value any
	// v2 supplies strict UTF-8 and duplicate-name validation. The v1 Number
	// type is only an interoperability type for gojq, never a float64 decode.
	numbers := json.UnmarshalFromFunc(func(d *jsontext.Decoder, v *any) error {
		if d.PeekKind() != '0' {
			return errors.ErrUnsupported
		}
		raw, err := d.ReadValue()
		*v = jsonv1.Number(string(raw))
		return err
	})
	if err := json.Unmarshal([]byte(input), &value, json.WithUnmarshalers(numbers)); err != nil {
		return nil, errors.New("invalid JSON input; expected one value")
	}
	return value, nil
}

func (b *queryBudget) checkValue(v any) error {
	nodes, size := 0, 0
	var visit func(any, int) error
	visit = func(v any, depth int) error {
		if err := b.check(); err != nil {
			return err
		}
		nodes++
		size += 8
		if nodes > queryNodeLimit || size > queryOutputLimit {
			return queryLimit("value size")
		}
		if depth > queryDepthLimit {
			return queryLimit("result depth")
		}
		switch v := v.(type) {
		case []any:
			for _, child := range v {
				if err := visit(child, depth+1); err != nil {
					return err
				}
			}
		case map[string]any:
			for key, child := range v {
				size += len(key)
				if err := visit(child, depth+1); err != nil {
					return err
				}
			}
		case string:
			size += len(v)
		case *big.Int:
			if v.BitLen() > queryNumberLimit*4 || len(v.String()) > queryNumberLimit {
				return queryLimit("number")
			}
		case jsonv1.Number:
			if len(v) > queryNumberLimit {
				return queryLimit("number")
			}
		case float64:
			if math.IsInf(v, 0) || math.IsNaN(v) {
				return errors.New("query produced a non-finite number")
			}
		}
		if size > queryOutputLimit {
			return queryLimit("value size")
		}
		return nil
	}
	return visit(v, 0)
}

type queryWriter struct {
	strings.Builder
	budget *queryBudget
}

func (w *queryWriter) Write(p []byte) (int, error) {
	if err := w.budget.check(); err != nil {
		w.budget.err = err
		return 0, err
	}
	if len(p) > queryOutputLimit-w.Len() {
		w.budget.err = queryLimit("output")
		return 0, w.budget.err
	}
	return w.Builder.Write(p)
}

type queryStop struct{ err error }

type queryPathGuard struct {
	jp.Frag
	budget *queryBudget
}

func (g queryPathGuard) Walk(rest, path jp.Expr, nodes []any, cb func(jp.Expr, []any)) {
	if err := g.budget.check(); err != nil {
		panic(queryStop{err})
	}
	if filter, ok := g.Frag.(*jp.Filter); ok {
		// Filter.Walk has no callback for rejected candidates. Let the
		// library enumerate children, checking cancellation even on a
		// filter that never matches. The library still evaluates the filter.
		jp.Wildcard('*').Walk(nil, path, nodes, func(path jp.Expr, nodes []any) {
			if err := g.budget.check(); err != nil {
				panic(queryStop{err})
			}
			if filter.Match(nodes[len(nodes)-1]) {
				if len(rest) != 0 {
					rest[0].Walk(rest[1:], path, nodes, cb)
				} else {
					cb(path, nodes)
				}
			}
		})
		return
	}
	g.Frag.Walk(rest, path, nodes, cb)
}

func preparePath(expression string, value any) (path jp.Expr, err error) {
	// OjG exposes filter inspection through a method that panics for literal
	// filters (e.g. [?true]). Reject these along with other unsupported forms.
	defer func() {
		if recover() != nil {
			path, err = nil, errors.New("invalid or unsupported JSONPath filter")
		}
	}()
	path, err = jp.ParseString(expression)
	if err != nil || len(path) == 0 {
		return nil, errors.New("invalid JSONPath syntax")
	}
	if _, ok := path[0].(jp.Root); !ok {
		return nil, errors.New("JSONPath must start with $")
	}
	if len(path) > queryDepthLimit {
		return nil, queryLimit("path depth")
	}
	descents, filters := 0, false
	wide, floating := false, false
	for _, frag := range path {
		switch f := frag.(type) {
		case jp.Root, jp.Child, jp.Nth, jp.Wildcard, jp.Bracket:
		case jp.Slice:
			// Huge steps can overflow OjG's integer loop counters.
			if f[2] != jp.SliceNotSet && (f[2] > queryNodeLimit || f[2] < -queryNodeLimit) {
				return nil, queryLimit("slice step")
			}
		case jp.Union:
			if len(f) > 32 {
				return nil, queryLimit("union")
			}
		case jp.Descent:
			descents++
			if descents > 1 {
				return nil, errors.New("JSONPath permits only one recursive descent")
			}
		case *jp.Filter:
			filters = true
			// NewScript also normalizes a bare singular path into OjG's
			// existence operation; readFilter alone leaves it unnormalized.
			script, err := jp.NewScript(f.Script.String())
			if err != nil {
				return nil, errors.New("invalid JSONPath filter")
			}
			f.Script = *script
			if err := checkPathFilter(f.Inspect(), &wide, &floating); err != nil {
				return nil, err
			}
		default:
			return nil, errors.New("unsupported JSONPath selector")
		}
	}
	if filters {
		var convert func(any) (any, error)
		convert = func(v any) (any, error) {
			switch v := v.(type) {
			case jsonv1.Number:
				if !strings.ContainsAny(string(v), ".eE") {
					n, err := strconv.ParseInt(string(v), 10, 64)
					if err != nil {
						return nil, errors.New("JSONPath filters require signed 64-bit integers; use jq")
					}
					wide = wide || n > 1<<53 || n < -(1<<53)
					return n, nil
				}
				floating = true
				f, err := strconv.ParseFloat(string(v), 64)
				if err != nil {
					return nil, errors.New("JSONPath filter number outside float64 range")
				}
				return f, nil
			case []any:
				for i, child := range v {
					x, err := convert(child)
					if err != nil {
						return nil, err
					}
					v[i] = x
				}
			case map[string]any:
				for key, child := range v {
					x, err := convert(child)
					if err != nil {
						return nil, err
					}
					v[key] = x
				}
			}
			return v, nil
		}
		if _, err := convert(value); err != nil {
			return nil, err
		}
		if wide && floating {
			return nil, errors.New("JSONPath filters cannot mix floats and integers outside ±2^53; use jq with integer literals")
		}
	}
	return path, nil
}

func checkPathFilter(v any, wide, floating *bool) error {
	switch v := v.(type) {
	case *jp.Form:
		switch v.Op {
		case "==", "!=", "<", ">", "<=", ">=", "&&", "||", "!", "exists", "get", "(":
		default:
			return errors.New("JSONPath filter arithmetic and functions are disabled")
		}
		if err := checkPathFilter(v.Left, wide, floating); err != nil {
			return err
		}
		return checkPathFilter(v.Right, wide, floating)
	case jp.Expr:
		// Walk's filter implementation supplies no root, and non-singular
		// subqueries expand cross-products before a callback can stop them.
		if len(v) == 0 {
			return errors.New("unsupported JSONPath filter path")
		}
		if _, ok := v[0].(jp.At); !ok {
			return errors.New("JSONPath filters allow only singular @ paths")
		}
		for _, f := range v[1:] {
			switch f.(type) {
			case jp.Child, jp.Nth, jp.Bracket:
			default:
				return errors.New("JSONPath filters allow only singular @ paths")
			}
		}
	case float64:
		*floating = true
		if math.IsInf(v, 0) || math.IsNaN(v) {
			return errors.New("invalid JSONPath numeric literal")
		}
	case int64:
		*wide = *wide || v > 1<<53 || v < -(1<<53)
	case nil, bool, string:
	default:
		return errors.New("unsupported JSONPath filter value")
	}
	return nil
}

// Walk the library's exported AST, not source text, so strings cannot hide an
// import or manufacture a forbidden function. Every query's stream is guarded,
// including streams consumed inside array constructors and function arguments.
func prepareJQ(v reflect.Value, depth int, nodes *int) error {
	if !v.IsValid() {
		return nil
	}
	*nodes++
	if depth > queryDepthLimit || *nodes > 4096 {
		return queryLimit("jq syntax tree")
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		switch n := v.Interface().(type) {
		case *gojq.Query:
			if n.Meta != nil || len(n.Imports) != 0 || len(n.FuncDefs) != 0 {
				return errors.New("jq modules, imports and definitions are disabled")
			}
			switch n.Op {
			case 0, gojq.OpPipe, gojq.OpComma, gojq.OpAdd, gojq.OpDiv, gojq.OpMod,
				gojq.OpEq, gojq.OpNe, gojq.OpGt, gojq.OpLt, gojq.OpGe, gojq.OpLe,
				gojq.OpAnd, gojq.OpOr, gojq.OpAlt:
			default:
				// Array subtraction is quadratic inside a non-interruptible
				// builtin; multiplication and sparse updates allocate first.
				return errors.New("jq multiplication, binary subtraction and updates are disabled")
			}
		case *gojq.Term:
			switch n.Type {
			case gojq.TermTypeFormat, gojq.TermTypeReduce, gojq.TermTypeForeach, gojq.TermTypeLabel, gojq.TermTypeBreak:
				return errors.New("unsupported jq term")
			}
			if len(n.Number) > queryNumberLimit {
				return queryLimit("number")
			}
		case *gojq.String:
			if n.Queries != nil {
				return errors.New("jq string interpolation is disabled")
			}
		case *gojq.Func:
			switch n.Name {
			case "select", "map", "map_values", "length", "keys", "keys_unsorted", "has", "type", "empty",
				"values", "arrays", "objects", "iterables", "booleans", "numbers", "strings", "nulls", "scalars", "not",
				"sort", "sort_by", "reverse", "unique", "unique_by", "group_by", "min", "max", "min_by", "max_by",
				"range", "recurse", "repeat", "limit", "first", "last", "nth", "isempty", "any", "all":
			default:
				if !strings.HasPrefix(n.Name, "$") || n.Name == "$ENV" {
					return errors.New("jq function is disabled: " + n.Name)
				}
			}
		}
		if err := prepareJQ(v.Elem(), depth+1, nodes); err != nil {
			return err
		}
		if q, ok := v.Interface().(*gojq.Query); ok {
			original := *q
			*q = gojq.Query{Left: &original, Op: gojq.OpPipe, Right: &gojq.Query{
				Term: &gojq.Term{Type: gojq.TermTypeFunc, Func: &gojq.Func{Name: "_inspect_guard"}},
			}}
		}
		return nil
	}
	switch v.Kind() {
	case reflect.Struct:
		for i := range v.NumField() {
			if err := prepareJQ(v.Field(i), depth+1, nodes); err != nil {
				return err
			}
		}
	case reflect.Slice:
		for i := range v.Len() {
			if err := prepareJQ(v.Index(i), depth+1, nodes); err != nil {
				return err
			}
		}
	}
	return nil
}
