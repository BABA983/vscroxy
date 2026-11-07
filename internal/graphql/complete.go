package graphql

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/lexer"
	"github.com/vektah/gqlparser/v2/validator"
)

// CompletionKind is what a completion is, so the editor can choose an icon
// without importing MyGo.
type CompletionKind uint8

const (
	CompletionField CompletionKind = iota
	CompletionArgument
	CompletionType
	CompletionEnum
	CompletionVariable
	CompletionDirective
	CompletionFragment
)

// Completion is one item an editor can offer. Insert replaces the word before
// the caret; an empty Insert means Label.
type Completion struct {
	Label         string
	Detail        string
	Documentation string
	Insert        string
	Kind          CompletionKind
}

// CompletionResult is the completions for the context at a caret, and where
// the word being completed starts in the source, in bytes.
type CompletionResult struct {
	Start int
	Items []Completion
}

// Diagnostic is a schema problem with a byte range of an executable document.
type Diagnostic struct {
	Start, End int
	Message    string
}

// Hover is what a schema says about the text at a caret: the byte range and a
// short, plain-text card.
type Hover struct {
	Start, End int
	Text       string
}

type queryToken struct {
	kind  lexer.Type
	value string
	start int // runes
	end   int // runes
}

// lexQuery reads as much of source as is lexable. A half-typed string or an
// invalid character ends the token stream instead of failing completion: the
// tokens before it are still a useful context.
func lexQuery(source string) []queryToken {
	l := lexer.New(&ast.Source{Name: "query", Input: source})
	var out []queryToken
	for len(out) < maxTokens {
		t, err := l.ReadToken()
		if err != nil || t.Kind == lexer.EOF {
			break
		}
		if t.Kind == lexer.Comment {
			continue
		}
		out = append(out, queryToken{kind: t.Kind, value: t.Value, start: t.Pos.Start, end: t.Pos.End})
	}
	return out
}

func runeIndex(source string, byteOffset int) int {
	byteOffset = min(max(byteOffset, 0), len(source))
	return utf8.RuneCountInString(source[:byteOffset])
}

func byteIndex(source string, runes int) int {
	if runes <= 0 {
		return 0
	}
	n := 0
	for i := range source {
		if n == runes {
			return i
		}
		n++
	}
	return len(source)
}

// Complete returns the fields, arguments, types, enum values or variables the
// schema allows at byteOffset. An incomplete document is expected: it reads
// tokens up to the caret, not a full AST.
func (s *Schema) Complete(source string, byteOffset int) CompletionResult {
	if s == nil || s.ast == nil {
		return CompletionResult{Start: byteOffset}
	}
	tokens := lexQuery(source)
	caret := runeIndex(source, byteOffset)
	for _, t := range tokens {
		if (t.kind == lexer.String || t.kind == lexer.BlockString) && t.start <= caret && caret <= t.end {
			return CompletionResult{Start: byteOffset}
		}
	}
	word := len(tokens)
	for i, t := range tokens {
		if t.kind == lexer.Name && t.start <= caret && caret <= t.end {
			word = i
			break
		}
		if t.end > caret {
			word = i
			break
		}
	}
	start := caret
	prefix := ""
	if word < len(tokens) && tokens[word].kind == lexer.Name && tokens[word].start <= caret && caret <= tokens[word].end {
		start = tokens[word].start
		runes := []rune(source)
		prefix = string(runes[start:caret])
	}
	p := &completer{schema: s.ast, declared: map[string]*ast.Type{}, fragments: collectFragments(tokens)}
	p.pendingComposite = p.rootName()
	for i := 0; i < word; i++ {
		var next *queryToken
		if i+1 < len(tokens) {
			next = &tokens[i+1]
		}
		p.step(tokens[i], next)
	}
	items := p.items(prefix)
	return CompletionResult{Start: byteIndex(source, start), Items: items}
}

type braceFrame struct {
	object   bool
	typeName string
	seen     map[string]bool
}

type parenKind uint8

const (
	parenArguments parenKind = iota
	parenDirective
	parenVariables
)

type parenFrame struct {
	kind      parenKind
	field     *ast.FieldDefinition
	directive *ast.DirectiveDefinition
	seen      map[string]bool
}

type completer struct {
	schema           *ast.Schema
	braces           []braceFrame
	parens           []parenFrame
	lists            []*ast.Type
	pendingComposite string
	value            *ast.Type
	lastField        *ast.FieldDefinition
	lastArg          *ast.ArgumentDefinition
	lastInput        *ast.FieldDefinition
	lastDirective    *ast.DirectiveDefinition
	pendingAlias     bool
	afterDollar      bool
	afterAt          bool
	afterVarName     bool
	varName          string
	afterOperation   bool
	expectType       bool
	expectFragment   bool
	expectOn         bool
	spread           bool
	typeParent       string
	typeCtx          bool
	fragmentName     string
	fragments        map[string]string
	declared         map[string]*ast.Type
	op               ast.Operation
	prev             queryToken
	hasPrev          bool
}

func (p *completer) atTop() bool { return len(p.braces) == 0 && len(p.parens) == 0 }

func (p *completer) inObject() bool {
	return len(p.braces) > 0 && p.braces[len(p.braces)-1].object
}

func (p *completer) inVariables() bool {
	return len(p.parens) > 0 && p.parens[len(p.parens)-1].kind == parenVariables
}

func (p *completer) inArguments() bool {
	return len(p.parens) > 0 && p.parens[len(p.parens)-1].kind != parenVariables
}

func (p *completer) rootName() string {
	switch p.op {
	case ast.Mutation:
		if p.schema.Mutation != nil {
			return p.schema.Mutation.Name
		}
	case ast.Subscription:
		if p.schema.Subscription != nil {
			return p.schema.Subscription.Name
		}
	}
	if p.schema.Query != nil {
		return p.schema.Query.Name
	}
	return ""
}

func (p *completer) currentType() string {
	for i := len(p.braces) - 1; i >= 0; i-- {
		if !p.braces[i].object {
			return p.braces[i].typeName
		}
	}
	return p.pendingComposite
}

func (p *completer) composite(name string) bool {
	def := p.schema.Types[name]
	return def != nil && def.IsCompositeType()
}

func (p *completer) step(t queryToken, next *queryToken) {
	defer func() { p.prev, p.hasPrev = t, true }()
	switch t.kind {
	case lexer.Name:
		p.name(t.value, next)
	case lexer.Dollar:
		p.afterDollar = true
	case lexer.At:
		p.afterAt = true
		if p.spread {
			p.spread = false
			p.pendingComposite = p.currentType()
		}
	case lexer.Spread:
		p.spread = true
		if next != nil && next.kind == lexer.BraceL {
			p.pendingComposite = p.currentType()
		}
	case lexer.BraceL:
		p.openBrace()
	case lexer.BraceR:
		p.closeBrace()
	case lexer.ParenL:
		p.openParen()
	case lexer.ParenR:
		p.closeParen()
	case lexer.BracketL:
		if p.value != nil {
			p.lists = append(p.lists, p.value.Elem)
			p.value = p.value.Elem
		}
	case lexer.BracketR:
		if len(p.lists) > 0 {
			p.lists = p.lists[:len(p.lists)-1]
		}
		p.value = nil
	case lexer.Colon:
		p.colon()
	case lexer.Equals:
		if p.inVariables() {
			p.value = nil // A default value; its type is the variable's.
		}
	case lexer.Int, lexer.Float, lexer.String, lexer.BlockString:
		p.value = nil
	}
}

func (p *completer) name(name string, next *queryToken) {
	switch {
	case p.afterAt:
		p.afterAt = false
		p.lastDirective = p.schema.Directives[name]
		p.lastField = nil
		return
	case p.afterDollar:
		p.afterDollar = false
		if p.inVariables() {
			p.varName, p.afterVarName = name, true
			return
		}
		p.value = nil // A variable reference is a complete value.
		return
	case p.expectType:
		p.expectType = false
		p.pendingComposite = name
		p.typeCtx = false
		if p.fragmentName != "" {
			p.fragments[p.fragmentName] = name
			p.fragmentName = ""
		}
		return
	case p.expectFragment:
		p.expectFragment = false
		p.fragmentName, p.expectOn = name, true
		return
	case p.expectOn:
		p.expectOn = false
		if name == "on" {
			p.expectType = true
			p.typeParent = "" // A fragment definition can name any composite type.
		}
		return
	case p.typeCtx:
		p.typeCtx = false
		if p.afterVarName {
			p.declared[p.varName] = &ast.Type{NamedType: name}
			p.afterVarName = false
		}
		return
	case p.spread:
		p.spread = false
		if name == "on" {
			p.expectType = true
			p.typeParent = p.currentType()
		}
		return
	case p.atTop() && (name == "query" || name == "mutation" || name == "subscription"):
		p.op = ast.Operation(name)
		p.pendingComposite = p.rootName()
		p.afterOperation = true
		return
	case p.atTop() && name == "fragment":
		p.expectFragment = true
		return
	case p.afterOperation:
		p.afterOperation = false
		return // The operation's name; its root type is already pending.
	case p.inVariables():
		return
	}
	if p.inObject() {
		if p.value != nil {
			p.value = nil // An enum value of an input field.
			return
		}
		def := p.schema.Types[p.braces[len(p.braces)-1].typeName]
		p.lastInput = nil
		if def != nil {
			p.lastInput = def.Fields.ForName(name)
		}
		return
	}
	if p.inArguments() {
		if p.value != nil {
			p.value = nil // An enum, true, false or null value.
			return
		}
		frame := &p.parens[len(p.parens)-1]
		p.lastArg = nil
		if frame.field != nil {
			p.lastArg = frame.field.Arguments.ForName(name)
		} else if frame.directive != nil {
			p.lastArg = frame.directive.Arguments.ForName(name)
		}
		return
	}
	if p.pendingAlias {
		p.pendingAlias = false
	} else if next != nil && next.kind == lexer.Colon {
		p.pendingAlias = true
		return
	}
	p.lastField = fieldOf(p.schema, p.currentType(), name)
	p.pendingComposite = ""
	if p.lastField != nil && p.composite(namedType(p.lastField.Type)) {
		p.pendingComposite = namedType(p.lastField.Type)
	}
}

func (p *completer) openBrace() {
	p.afterOperation = false
	if p.value != nil {
		def := p.schema.Types[namedType(p.value)]
		if def != nil && def.Kind == ast.InputObject {
			p.braces = append(p.braces, braceFrame{object: true, typeName: def.Name, seen: map[string]bool{}})
			p.value, p.lastInput = nil, nil
			return
		}
	}
	typ := p.pendingComposite
	if typ == "" {
		typ = p.currentType()
	}
	if typ == "" {
		typ = p.rootName()
	}
	p.braces = append(p.braces, braceFrame{typeName: typ})
	p.pendingComposite, p.lastField, p.pendingAlias, p.value = "", nil, false, nil
}

func (p *completer) closeBrace() {
	if len(p.braces) > 0 {
		p.braces = p.braces[:len(p.braces)-1]
	}
	p.pendingComposite, p.lastField, p.pendingAlias, p.value, p.lastInput = "", nil, false, nil, nil
}

func (p *completer) openParen() {
	frame := parenFrame{seen: map[string]bool{}}
	switch {
	case p.lastField != nil && !p.inObject():
		frame.kind, frame.field = parenArguments, p.lastField
	case p.lastDirective != nil:
		frame.kind, frame.directive = parenDirective, p.lastDirective
	case p.atTop() || p.afterOperation || p.inVariables():
		frame.kind = parenVariables
	default:
		frame.kind = parenArguments
	}
	p.parens = append(p.parens, frame)
	p.lastField, p.lastDirective, p.afterOperation = nil, nil, false
}

func (p *completer) closeParen() {
	if len(p.parens) > 0 {
		p.parens = p.parens[:len(p.parens)-1]
	}
	p.lastArg, p.typeCtx, p.afterVarName, p.value = nil, false, false, nil
}

func (p *completer) colon() {
	switch {
	case p.inObject():
		if p.lastInput != nil {
			p.braces[len(p.braces)-1].seen[p.lastInput.Name] = true
			p.value = p.lastInput.Type
		}
	case p.inArguments():
		if p.lastArg != nil {
			p.parens[len(p.parens)-1].seen[p.lastArg.Name] = true
			p.value = p.lastArg.Type
		}
	case p.inVariables():
		if p.afterVarName {
			p.typeCtx = true
		}
	}
}

func (p *completer) items(prefix string) []Completion {
	var out []Completion
	switch {
	case p.expectType || p.typeCtx:
		out = p.typeItems(p.typeCtx)
	case p.expectOn:
		out = []Completion{{Label: "on", Detail: "type condition", Insert: "on", Kind: CompletionType}}
	case p.afterDollar:
		if !p.inVariables() {
			out = p.variableItems()
		}
	case p.afterAt:
		out = p.directiveItems()
	case p.inVariables():
	case p.inObject():
		if p.value != nil {
			out = p.valueItems(p.value)
		} else {
			out = p.inputItems()
		}
	case p.inArguments():
		if p.value != nil {
			out = p.valueItems(p.value)
		} else {
			out = p.argumentItems()
		}
	case p.spread:
		out = p.fragmentItems()
	case p.pendingAlias:
		out = p.fieldItems()
	case p.afterOperation:
	case p.expectFragment:
	case p.hasPrev && p.prev.kind == lexer.Name && (p.prev.value == "query" || p.prev.value == "mutation" || p.prev.value == "subscription" || p.prev.value == "fragment"):
	default:
		if len(p.braces) > 0 {
			out = p.fieldItems()
		}
	}
	if prefix != "" {
		out = slices.DeleteFunc(out, func(item Completion) bool {
			label := item.Label
			if item.Insert != "" {
				label = item.Insert
			}
			return !strings.HasPrefix(strings.ToLower(label), strings.ToLower(prefix))
		})
	}
	return out
}

func (p *completer) fieldItems() []Completion {
	typeName := p.currentType()
	def := p.schema.Types[typeName]
	if def == nil || !def.IsCompositeType() {
		return nil
	}
	var out []Completion
	if def.Kind == ast.Union {
		return []Completion{{Label: "__typename", Detail: "String!", Kind: CompletionField, Insert: "__typename"}}
	}
	for _, field := range def.Fields {
		if strings.HasPrefix(field.Name, "__") && field.Name != "__typename" {
			continue
		}
		out = append(out, Completion{
			Label: field.Name, Detail: field.Type.String(), Documentation: oneLine(field.Description),
			Insert: field.Name, Kind: CompletionField,
		})
	}
	if def.Fields.ForName("__typename") == nil {
		out = append(out, Completion{Label: "__typename", Detail: "String!", Kind: CompletionField, Insert: "__typename"})
	}
	return out
}

func (p *completer) argumentItems() []Completion {
	if len(p.parens) == 0 {
		return nil
	}
	frame := &p.parens[len(p.parens)-1]
	var defs ast.ArgumentDefinitionList
	switch {
	case frame.field != nil:
		defs = frame.field.Arguments
	case frame.directive != nil:
		defs = frame.directive.Arguments
	}
	var out []Completion
	for _, arg := range defs {
		if frame.seen[arg.Name] {
			continue
		}
		out = append(out, Completion{
			Label: arg.Name, Detail: arg.Type.String(), Documentation: oneLine(arg.Description),
			Insert: arg.Name, Kind: CompletionArgument,
		})
	}
	return out
}

func (p *completer) inputItems() []Completion {
	if len(p.braces) == 0 {
		return nil
	}
	frame := &p.braces[len(p.braces)-1]
	def := p.schema.Types[frame.typeName]
	if def == nil || def.Kind != ast.InputObject {
		return nil
	}
	var out []Completion
	for _, field := range def.Fields {
		if frame.seen[field.Name] {
			continue
		}
		out = append(out, Completion{
			Label: field.Name, Detail: field.Type.String(), Documentation: oneLine(field.Description),
			Insert: field.Name, Kind: CompletionField,
		})
	}
	return out
}

func (p *completer) valueItems(t *ast.Type) []Completion {
	t = deref(t)
	if t == nil {
		return nil
	}
	if t.Elem != nil {
		t = deref(t.Elem)
		if t == nil {
			return nil
		}
	}
	def := p.schema.Types[t.Name()]
	if def == nil || def.Kind != ast.Enum {
		return nil
	}
	var out []Completion
	for _, value := range def.EnumValues {
		out = append(out, Completion{
			Label: value.Name, Detail: def.Name, Documentation: oneLine(value.Description),
			Insert: value.Name, Kind: CompletionEnum,
		})
	}
	return out
}

func (p *completer) typeItems(input bool) []Completion {
	var defs []*ast.Definition
	for name, def := range p.schema.Types {
		if strings.HasPrefix(name, "__") {
			continue
		}
		if input {
			if !def.IsInputType() {
				continue
			}
		} else {
			if def.BuiltIn || !def.IsCompositeType() || !p.canFragment(def.Name) {
				continue
			}
		}
		defs = append(defs, def)
	}
	slices.SortFunc(defs, func(a, b *ast.Definition) int { return cmp.Compare(a.Name, b.Name) })
	out := make([]Completion, 0, len(defs))
	for _, def := range defs {
		out = append(out, Completion{
			Label: def.Name, Detail: typeLabel(def), Documentation: oneLine(def.Description),
			Insert: def.Name, Kind: CompletionType,
		})
	}
	return out
}

// canFragment keeps inline-fragment type conditions to composite types that
// can overlap the type being selected on. A fragment definition has no parent
// type, so every composite type is offered there.
func (p *completer) canFragment(candidate string) bool {
	return p.canFragmentFrom(p.typeParent, candidate)
}

func (p *completer) canFragmentFrom(parent, candidate string) bool {
	if parent == "" || parent == candidate {
		return true
	}
	concrete := func(name string) map[string]bool {
		out := map[string]bool{}
		def := p.schema.Types[name]
		if def == nil {
			return out
		}
		if def.Kind == ast.Object {
			out[name] = true
			return out
		}
		for _, possible := range p.schema.PossibleTypes[name] {
			out[possible.Name] = true
		}
		return out
	}
	parentTypes, candidateTypes := concrete(parent), concrete(candidate)
	for name := range parentTypes {
		if candidateTypes[name] {
			return true
		}
	}
	return false
}

func (p *completer) variableItems() []Completion {
	names := make([]string, 0, len(p.declared))
	for name := range p.declared {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make([]Completion, 0, len(names))
	for _, name := range names {
		detail := ""
		if t := p.declared[name]; t != nil {
			detail = t.String()
		}
		out = append(out, Completion{Label: name, Detail: detail, Insert: name, Kind: CompletionVariable})
	}
	return out
}

func (p *completer) directiveItems() []Completion {
	names := make([]string, 0, len(p.schema.Directives))
	for name := range p.schema.Directives {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make([]Completion, 0, len(names))
	for _, name := range names {
		dir := p.schema.Directives[name]
		out = append(out, Completion{
			Label: name, Detail: "directive", Documentation: oneLine(dir.Description),
			Insert: name, Kind: CompletionDirective,
		})
	}
	return out
}

func (p *completer) fragmentItems() []Completion {
	out := []Completion{{Label: "on", Detail: "type condition", Insert: "on", Kind: CompletionType}}
	parent := p.currentType()
	names := make([]string, 0, len(p.fragments))
	for name := range p.fragments {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		typ := p.fragments[name]
		if typ != "" && !p.canFragmentFrom(parent, typ) {
			continue
		}
		detail := "fragment"
		if typ != "" {
			detail = "on " + typ
		}
		out = append(out, Completion{Label: name, Detail: detail, Insert: name, Kind: CompletionFragment})
	}
	return out
}

func deref(t *ast.Type) *ast.Type {
	if t == nil {
		return nil
	}
	for t.NonNull {
		copy := *t
		copy.NonNull = false
		t = &copy
	}
	return t
}

func oneLine(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > 300 {
		text = string(runes[:300]) + "…"
	}
	return text
}

// collectFragments reads fragment names and type conditions from the whole
// document, including definitions below the caret.
func collectFragments(tokens []queryToken) map[string]string {
	out := map[string]string{}
	depth := 0
	for i := 0; i+1 < len(tokens); i++ {
		switch tokens[i].kind {
		case lexer.BraceL:
			depth++
			continue
		case lexer.BraceR:
			depth--
			continue
		}
		if depth != 0 || tokens[i].kind != lexer.Name || tokens[i].value != "fragment" || tokens[i+1].kind != lexer.Name {
			continue
		}
		name := tokens[i+1].value
		for j := i + 2; j+1 < len(tokens); j++ {
			if tokens[j].kind == lexer.BraceL {
				break
			}
			if tokens[j].kind == lexer.Name && tokens[j].value == "on" && tokens[j+1].kind == lexer.Name {
				out[name] = tokens[j+1].value
				break
			}
		}
	}
	return out
}

// Hover describes the field, argument, type, enum value or variable at
// byteOffset. It uses the same parser and validator as diagnostics, so a
// document with syntax errors has no hover rather than a guessed one.
func (s *Schema) Hover(source string, byteOffset int) (Hover, bool) {
	if s == nil || s.ast == nil {
		return Hover{}, false
	}
	remaining := maxTokens
	doc, err := parse(source, &remaining)
	if err != nil {
		return Hover{}, false
	}
	validator.Validate(s.ast, doc)
	tokens := lexQuery(source)
	caret := runeIndex(source, byteOffset)
	var tok *queryToken
	for i := range tokens {
		if tokens[i].kind == lexer.Name && tokens[i].start <= caret && caret <= tokens[i].end {
			tok = &tokens[i]
			break
		}
	}
	if tok == nil || tok.kind != lexer.Name {
		return Hover{}, false
	}
	best := hoverCandidate{}
	walkDocument(doc, func(c hoverCandidate) {
		if c.start <= tok.start && tok.end <= c.end && (best.start < c.start || best.start == c.start && best.end > c.end || best.text == "") {
			best = c
		}
	})
	if best.text == "" {
		// Type conditions and variable types are names, not fields; the
		// schema still explains them at the exact token.
		if def := s.ast.Types[tok.value]; def != nil {
			best.text = typeHover(def)
		}
	}
	if best.text == "" {
		return Hover{}, false
	}
	return Hover{Start: byteIndex(source, tok.start), End: byteIndex(source, tok.end), Text: best.text}, true
}

type hoverCandidate struct {
	start, end int // runes
	text       string
}

func walkDocument(doc *ast.QueryDocument, visit func(hoverCandidate)) {
	for _, op := range doc.Operations {
		for _, def := range op.VariableDefinitions {
			visit(hoverCandidate{start: def.Position.Start, end: def.Position.End + utf8.RuneCountInString(def.Variable), text: variableHover(def)})
			if def.DefaultValue != nil {
				walkValue(def.DefaultValue, visit)
			}
		}
		for _, dir := range op.Directives {
			walkDirective(dir, visit)
		}
		walkSelections(op.SelectionSet, visit)
	}
	for _, frag := range doc.Fragments {
		if frag.Definition != nil {
			visit(hoverCandidate{start: frag.Position.Start, end: frag.Position.End, text: typeHover(frag.Definition)})
		}
		for _, dir := range frag.Directives {
			walkDirective(dir, visit)
		}
		walkSelections(frag.SelectionSet, visit)
	}
}

func walkSelections(set ast.SelectionSet, visit func(hoverCandidate)) {
	for _, selection := range set {
		switch node := selection.(type) {
		case *ast.Field:
			if node.Definition != nil {
				visit(hoverCandidate{start: node.Position.Start, end: node.Position.End, text: fieldHover(node.Name, node.Definition)})
			}
			for _, arg := range node.Arguments {
				if node.Definition != nil {
					if def := node.Definition.Arguments.ForName(arg.Name); def != nil {
						visit(hoverCandidate{start: arg.Position.Start, end: arg.Position.End, text: argumentHover(def)})
					}
				}
				walkValue(arg.Value, visit)
			}
			for _, dir := range node.Directives {
				walkDirective(dir, visit)
			}
			walkSelections(node.SelectionSet, visit)
		case *ast.FragmentSpread:
			if node.Definition != nil {
				visit(hoverCandidate{start: node.Position.Start, end: node.Position.End, text: "fragment " + node.Definition.Name + " on " + node.Definition.TypeCondition})
			}
			for _, dir := range node.Directives {
				walkDirective(dir, visit)
			}
		case *ast.InlineFragment:
			if node.ObjectDefinition != nil {
				visit(hoverCandidate{start: node.Position.Start, end: node.Position.End, text: typeHover(node.ObjectDefinition)})
			}
			for _, dir := range node.Directives {
				walkDirective(dir, visit)
			}
			walkSelections(node.SelectionSet, visit)
		}
	}
}

func walkDirective(dir *ast.Directive, visit func(hoverCandidate)) {
	if dir.Definition != nil {
		visit(hoverCandidate{start: dir.Position.Start, end: dir.Position.End, text: directiveHover(dir.Definition)})
		for _, arg := range dir.Arguments {
			if def := dir.Definition.Arguments.ForName(arg.Name); def != nil {
				visit(hoverCandidate{start: arg.Position.Start, end: arg.Position.End, text: argumentHover(def)})
			}
			walkValue(arg.Value, visit)
		}
		return
	}
	for _, arg := range dir.Arguments {
		walkValue(arg.Value, visit)
	}
}

func walkValue(value *ast.Value, visit func(hoverCandidate)) {
	if value == nil {
		return
	}
	switch value.Kind {
	case ast.EnumValue:
		if value.Definition != nil && value.ExpectedType != nil {
			detail := value.Definition.Name
			for _, enum := range value.Definition.EnumValues {
				if enum.Name == value.Raw {
					if desc := oneLine(enum.Description); desc != "" {
						detail += "\n\n" + desc
					}
					break
				}
			}
			visit(hoverCandidate{start: value.Position.Start, end: value.Position.End, text: value.Raw + " · " + detail})
		}
	case ast.Variable:
		if value.VariableDefinition != nil {
			visit(hoverCandidate{start: value.Position.Start, end: value.Position.End + utf8.RuneCountInString(value.Raw), text: variableHover(value.VariableDefinition)})
		}
	case ast.ObjectValue:
		if value.Definition != nil {
			for _, child := range value.Children {
				if def := value.Definition.Fields.ForName(child.Name); def != nil {
					visit(hoverCandidate{start: child.Position.Start, end: child.Position.End, text: inputHover(def)})
				}
				walkValue(child.Value, visit)
			}
		}
	case ast.ListValue:
		for _, child := range value.Children {
			walkValue(child.Value, visit)
		}
	}
}

func fieldHover(name string, def *ast.FieldDefinition) string {
	var b strings.Builder
	b.WriteString(name + ": " + def.Type.String())
	if desc := oneLine(def.Description); desc != "" {
		b.WriteString("\n\n" + desc)
	}
	return b.String()
}

func argumentHover(def *ast.ArgumentDefinition) string {
	text := def.Name + ": " + def.Type.String()
	if def.DefaultValue != nil {
		text += " = " + def.DefaultValue.String()
	}
	if desc := oneLine(def.Description); desc != "" {
		text += "\n\n" + desc
	}
	return text
}

func inputHover(def *ast.FieldDefinition) string {
	text := def.Name + ": " + def.Type.String()
	if def.DefaultValue != nil {
		text += " = " + def.DefaultValue.String()
	}
	if desc := oneLine(def.Description); desc != "" {
		text += "\n\n" + desc
	}
	return text
}

func variableHover(def *ast.VariableDefinition) string {
	text := "$" + def.Variable + ": " + def.Type.String()
	if def.DefaultValue != nil {
		text += " = " + def.DefaultValue.String()
	}
	if desc := oneLine(def.Description); desc != "" {
		text += "\n\n" + desc
	}
	return text
}

func typeHover(def *ast.Definition) string {
	text := typeLabel(def) + " " + def.Name
	if desc := oneLine(def.Description); desc != "" {
		text += "\n\n" + desc
	}
	if def.Kind == ast.Union || def.Kind == ast.Interface {
		var names []string
		for _, possible := range def.Types {
			names = append(names, possible)
		}
		if len(names) > 0 {
			text += "\n\nPossible types: " + strings.Join(names, ", ")
		}
	}
	return text
}

func directiveHover(def *ast.DirectiveDefinition) string {
	text := "@" + def.Name
	if len(def.Arguments) > 0 {
		var args []string
		for _, arg := range def.Arguments {
			args = append(args, arg.Name+": "+arg.Type.String())
		}
		text += "(" + strings.Join(args, ", ") + ")"
	}
	if len(def.Locations) > 0 {
		var locations []string
		for _, loc := range def.Locations {
			locations = append(locations, string(loc))
		}
		text += " on " + strings.Join(locations, " | ")
	}
	if desc := oneLine(def.Description); desc != "" {
		text += "\n\n" + desc
	}
	return text
}

// Diagnostics validates an executable document against the schema. It reports
// unknown fields and arguments, fragments on the wrong type, bad values and
// the other executable rules, each with a byte range for the editor.
func (s *Schema) Diagnostics(source string) []Diagnostic {
	if s == nil || s.ast == nil || strings.TrimSpace(source) == "" {
		return nil
	}
	remaining := maxTokens
	doc, err := parse(source, &remaining)
	if err != nil {
		return nil // Half-typed text is not a schema error.
	}
	errs := validator.Validate(s.ast, doc)
	if len(errs) == 0 {
		return nil
	}
	lines := lineStarts(source)
	tokens := lexQuery(source)
	var out []Diagnostic
	seen := map[string]bool{}
	for _, err := range errs {
		if len(err.Locations) == 0 {
			continue
		}
		loc := err.Locations[0]
		start := lineColumnOffset(source, lines, loc.Line, loc.Column)
		if start < 0 {
			continue
		}
		end := start + 1
		runeStart := lineStartRune(source, lines, loc.Line) + loc.Column - 1
		for _, tok := range tokens {
			if tok.start == runeStart {
				start, end = byteIndex(source, tok.start), byteIndex(source, tok.end)
				break
			}
		}
		key := fmt.Sprintf("%d:%d:%s", start, end, err.Message)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, Diagnostic{Start: start, End: end, Message: err.Message})
		if len(out) == 100 {
			break
		}
	}
	return out
}

func lineStarts(source string) []int {
	starts := []int{0}
	for i := 0; i < len(source); i++ {
		switch source[i] {
		case '\n':
			starts = append(starts, i+1)
		case '\r':
			if i+1 < len(source) && source[i+1] == '\n' {
				continue
			}
			starts = append(starts, i+1)
		}
	}
	return starts
}

func lineStartRune(source string, lines []int, line int) int {
	if line < 1 || line > len(lines) {
		return 0
	}
	return runeIndex(source, lines[line-1])
}

func lineColumnOffset(source string, lines []int, line, column int) int {
	if line < 1 || line > len(lines) || column < 1 {
		return -1
	}
	start := lines[line-1]
	end := len(source)
	if line < len(lines) {
		end = lines[line] - 1
	}
	runes := 1
	for i := start; i < end; i++ {
		if runes == column {
			return i
		}
		_, size := utf8.DecodeRuneInString(source[i:])
		i += size - 1
		runes++
	}
	if runes == column {
		return end
	}
	return -1
}
