package masking

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrRefused is the error for a statement that can copy or rename the value
// of a masked column into a place that the rules do not hide.
var ErrRefused = errors.New("masking: statement refused")

// kind is the kind of a token.
type kind int

const (
	// word is a name, a keyword or a number.
	word kind = iota
	// quoted is a quoted name.
	quoted
	// literal is a string.
	literal
	// symbol is any other character.
	symbol
)

// token is a token of a statement. The text of a quoted name or a string has
// no quotes. The note holds the comments that follow the token.
type token struct {
	kind kind
	text string
	note string
}

// is reports whether the token is one of the words, in any case.
func (t token) is(words ...string) bool {
	if t.kind != word {
		return false
	}
	for _, w := range words {
		if strings.EqualFold(t.text, w) {
			return true
		}
	}
	return false
}

// sym reports whether the token is the symbol s.
func (t token) sym(s string) bool {
	return t.kind == symbol && t.text == s
}

// ident reports whether the token can be a name.
func (t token) ident() bool {
	return t.kind == quoted || t.kind == word && !t.is(reserved...)
}

// reserved are the words that end a table reference, so that they are not
// read as an alias.
var reserved = []string{
	"WHERE", "ON", "USING", "JOIN", "INNER", "LEFT", "RIGHT", "FULL", "CROSS",
	"NATURAL", "OUTER", "GROUP", "ORDER", "HAVING", "LIMIT", "OFFSET", "FETCH",
	"UNION", "INTERSECT", "EXCEPT", "MINUS", "WINDOW", "FOR", "INTO", "WITH",
	"TABLESAMPLE", "SAMPLE", "PARTITION", "PIVOT", "UNPIVOT", "MATCH_RECOGNIZE",
	"CONNECT", "START", "QUALIFY", "FINAL", "PREWHERE", "ARRAY", "SETTINGS",
	"FORMAT", "APPLY", "LATERAL", "USE", "FORCE", "IGNORE", "STRAIGHT_JOIN",
	"RETURNING", "OUTPUT", "SET", "VALUES", "SELECT", "AS", "ASOF", "ANY",
	"ALL", "GLOBAL", "SEMI", "ANTI", "PASTE", "OPTION", "WHEN", "THEN", "ELSE",
	"END", "AND", "OR", "NOT", "IS", "IN", "LIKE", "BETWEEN", "INDEX", "KEY",
	"LOCK", "ONLY",
}

// isWordRune reports whether r can be part of a word.
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '$' || r == '#' || r == '@'
}

// lex splits a statement into tokens. The comments before the first token
// are returned on their own. The split is unsure when a string, a quoted name
// or a comment has no end, when a string holds a backslash, which only some
// databases read as an escape, or when a comment holds code that MySQL runs.
func lex(s string) ([]token, string, bool) {
	var toks []token
	var lead string
	unsure := false
	note := func(text string) {
		if len(toks) == 0 {
			lead += " " + text
			return
		}
		toks[len(toks)-1].note += " " + text
	}
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case unicode.IsSpace(r):
			i += size
		case strings.HasPrefix(s[i:], "--"):
			j := strings.IndexByte(s[i:], '\n')
			if j < 0 {
				j = len(s) - i
			}
			note(s[i+2 : i+j])
			i += j
		case strings.HasPrefix(s[i:], "/*"):
			j := strings.Index(s[i+2:], "*/")
			if j < 0 {
				unsure = true
				note(s[i+2:])
				i = len(s)
				continue
			}
			body := s[i+2 : i+2+j]
			if strings.HasPrefix(body, "!") || strings.Contains(body, "/*") {
				unsure = true
			}
			note(body)
			i += j + 4
		case r == '\'' || r == '"' || r == '`':
			text, n, ok := quote(s[i:], byte(r))
			k := quoted
			if r == '\'' {
				k = literal
				if strings.Contains(text, `\`) {
					unsure = true
				}
			}
			if !ok {
				unsure = true
			}
			toks = append(toks, token{kind: k, text: text})
			i += n
		case r == '$' && dollarTag(s[i:]) != "" && (i == 0 || !isWordRune(lastRune(s[:i]))):
			tag := dollarTag(s[i:])
			j := strings.Index(s[i+len(tag):], tag)
			if j < 0 {
				unsure = true
				toks = append(toks, token{kind: literal, text: s[i+len(tag):]})
				i = len(s)
				continue
			}
			toks = append(toks, token{kind: literal, text: s[i+len(tag) : i+len(tag)+j]})
			i += len(tag) + j + len(tag)
		case isWordRune(r):
			j := i
			for j < len(s) {
				c, n := utf8.DecodeRuneInString(s[j:])
				if !isWordRune(c) {
					break
				}
				j += n
			}
			toks = append(toks, token{kind: word, text: s[i:j]})
			i = j
		default:
			toks = append(toks, token{kind: symbol, text: s[i : i+size]})
			i += size
		}
	}
	return toks, lead, unsure
}

// quote reads the quoted text at the start of s, where a doubled quote
// stands for one quote. It returns the text, the length that it read, and
// whether the text has an end.
func quote(s string, q byte) (string, int, bool) {
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		if s[i] != q {
			b.WriteByte(s[i])
			continue
		}
		if i+1 < len(s) && s[i+1] == q {
			b.WriteByte(q)
			i++
			continue
		}
		return b.String(), i + 1, true
	}
	return b.String(), len(s), false
}

// dollarTag returns the PostgreSQL dollar quote tag at the start of s, such
// as $$ or $body$, or "" when there is none.
func dollarTag(s string) string {
	for i := 1; i < len(s); i++ {
		switch c := s[i]; {
		case c == '$':
			return s[:i+1]
		case c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || i > 1 && '0' <= c && c <= '9':
		default:
			return ""
		}
	}
	return ""
}

// lastRune returns the last rune of s.
func lastRune(s string) rune {
	r, _ := utf8.DecodeLastRuneInString(s)
	return r
}

// analysis is what a statement tells about the masked columns.
type analysis struct {
	m      *Masker
	toks   []token
	lead   string
	unsure bool
	// depth is the depth of each token in brackets. An opening bracket has
	// the depth outside it.
	depth []int
	// close is the index of the closing bracket of each opening bracket.
	close []int
	// open is the index of the innermost opening bracket around each token,
	// or -1.
	open []int
	// found caches the rule that matches a word.
	found map[string]int
	// taint holds the aliases of expressions that read a masked column, with
	// the rule of that column.
	taint map[string]int
	// defs holds the tokens that name a table or an alias where it is
	// defined, and rels holds those names.
	defs map[int]bool
	rels map[string]bool
	// groups holds the brackets that make a table: a subquery or the
	// arguments of a table function.
	groups []int
	// lists holds, for each list of column aliases, as in "FROM t AS x(a, b)"
	// or "WITH c(a, b) AS (...)", the opening bracket of the subquery that
	// the list renames, or -1 for a table or a function.
	lists []int
	// renamed reports whether a list of column aliases can hide the names of
	// masked columns: it renames a table or a function, or a subquery that
	// reads a star or a whole row.
	renamed bool
	// whole marks the tokens that read a whole row of a table, as in
	// "SELECT t FROM t" or "to_json(t.*)".
	whole []bool
	// mention is the rule that matches a word anywhere in the statement, and
	// cause is that word.
	mention int
	cause   string
}

// analyze reads the statement.
func (m *Masker) analyze(query string) *analysis {
	toks, lead, unsure := lex(query)
	n := len(toks)
	a := &analysis{
		m:      m,
		toks:   toks,
		lead:   lead,
		unsure: unsure,
		depth:  make([]int, n),
		close:  make([]int, n),
		open:   make([]int, n),
		found:  make(map[string]int),
		taint:  make(map[string]int),
		defs:   make(map[int]bool),
		rels:   make(map[string]bool),
		whole:  make([]bool, n),
	}
	var stack []int
	for i, t := range toks {
		a.depth[i] = len(stack)
		a.open[i] = -1
		if len(stack) != 0 {
			a.open[i] = stack[len(stack)-1]
		}
		switch {
		case t.sym("(") || t.sym("["):
			a.close[i] = n
			stack = append(stack, i)
		case t.sym(")") || t.sym("]"):
			if len(stack) == 0 {
				a.unsure = true
				continue
			}
			a.close[stack[len(stack)-1]] = i
			stack = stack[:len(stack)-1]
			a.depth[i] = len(stack)
		}
	}
	if len(stack) != 0 {
		a.unsure = true
	}
	a.tables()
	a.rows()
	a.renamed = a.renames()
	a.aliases()
	a.mention, a.cause = a.mentions(0, n, true)
	if r, w := a.m.words(a.lead, a.found, a.taint); r > a.mention {
		a.mention, a.cause = r, w
	}
	return a
}

// find returns the rule with the highest priority that matches the name, or
// -1.
func (m *Masker) find(name string) int {
	for i := len(m.rules) - 1; i >= 0; i-- {
		if m.rules[i].matches(name) {
			return i
		}
	}
	return -1
}

// words returns the rule with the highest priority that matches a word of the
// text, or an alias in taint, and the word.
func (m *Masker) words(text string, found map[string]int, taint map[string]int) (int, string) {
	best, cause := -1, ""
	for _, w := range strings.FieldsFunc(text, func(r rune) bool { return !isWordRune(r) }) {
		key := strings.ToLower(w)
		r, ok := found[key]
		if !ok {
			r = m.find(w)
			found[key] = r
		}
		if t, ok := taint[key]; ok && t > r {
			r = t
		}
		if r > best {
			best, cause = r, w
		}
	}
	return best, cause
}

// mentions returns the rule with the highest priority that a word of the
// tokens in [i, j) matches, and the word. The text of strings counts when
// strs is set. The comments always count.
func (a *analysis) mentions(i, j int, strs bool) (int, string) {
	best, cause := -1, ""
	for k := i; k < j && k < len(a.toks); k++ {
		t := a.toks[k]
		if t.kind == quoted {
			if r := a.m.find(t.text); r > best {
				best, cause = r, t.text
			}
		}
		if t.kind != literal || strs || a.unsure {
			if r, w := a.m.words(t.text, a.found, a.taint); r > best {
				best, cause = r, w
			}
		}
		if r, w := a.m.words(t.note, a.found, a.taint); r > best {
			best, cause = r, w
		}
	}
	return best, cause
}

// fromClause reports whether the FROM at i starts a list of tables, and is
// not part of an expression such as "EXTRACT(YEAR FROM d)" or "IS DISTINCT
// FROM".
func (a *analysis) fromClause(i int) bool {
	if i > 0 && a.toks[i-1].is("DISTINCT") {
		return false
	}
	o := a.open[i]
	if o < 0 {
		return true
	}
	return o+1 < len(a.toks) && a.toks[o+1].is("SELECT", "WITH")
}

// tables finds the tables and the aliases that the statement defines.
func (a *analysis) tables() {
	for i, t := range a.toks {
		switch {
		case t.is("FROM") && a.fromClause(i):
			a.tableList(i + 1)
		case t.is("JOIN", "APPLY"):
			a.table(i + 1)
		case t.is("UPDATE") && i == 0:
			j := i + 1
			for j < len(a.toks) && a.toks[j].is("LOW_PRIORITY", "IGNORE", "ONLY") {
				j++
			}
			a.tableList(j)
		case t.is("WITH") && (i == 0 || a.toks[i-1].sym("(")):
			a.ctes(i + 1)
		}
	}
}

// tableList reads a list of table references at i.
func (a *analysis) tableList(i int) {
	for {
		j := a.table(i)
		if j == i || j >= len(a.toks) || !a.toks[j].sym(",") {
			return
		}
		i = j + 1
	}
}

// table reads a table reference at i, and returns the index after it.
func (a *analysis) table(i int) int {
	toks := a.toks
	for i < len(toks) && toks[i].is("LATERAL", "ONLY") {
		i++
	}
	if i >= len(toks) {
		return i
	}
	body := -1
	switch {
	case toks[i].sym("(") || toks[i].sym("["):
		a.groups = append(a.groups, i)
		body = i
		i = a.close[i] + 1
	case toks[i].ident():
		for i+2 < len(toks) && toks[i+1].sym(".") && toks[i+2].ident() {
			a.defs[i] = true
			i += 2
		}
		a.def(i)
		i++
		if i < len(toks) && toks[i].sym("(") {
			a.groups = append(a.groups, i)
			i = a.close[i] + 1
		}
	default:
		return i
	}
	alias := false
	if i < len(toks) && toks[i].is("AS") {
		i++
	}
	if i < len(toks) && toks[i].ident() {
		a.def(i)
		alias = true
		i++
	}
	if alias && i < len(toks) && toks[i].sym("(") {
		a.lists = append(a.lists, body)
		i = a.close[i] + 1
	}
	return i
}

// renames reports whether a list of column aliases can hide the names of
// masked columns.
func (a *analysis) renames() bool {
	for _, body := range a.lists {
		if body < 0 || a.wholeIn(body, a.close[body]+1) {
			return true
		}
		for k := body; k <= a.close[body] && k < len(a.toks); k++ {
			if a.starItem(k) {
				return true
			}
		}
	}
	return false
}

// ctes reads the names of the common table expressions of a WITH at i. A
// list of column names after the name renames the columns.
func (a *analysis) ctes(i int) {
	toks := a.toks
	if i < len(toks) && toks[i].is("RECURSIVE") {
		i++
	}
	for i < len(toks) && toks[i].ident() {
		a.def(i)
		i++
		list := i < len(toks) && toks[i].sym("(")
		if list {
			i = a.close[i] + 1
		}
		if i >= len(toks) || !toks[i].is("AS") {
			if list {
				a.lists = append(a.lists, -1)
			}
			return
		}
		i++
		for i < len(toks) && toks[i].is("NOT", "MATERIALIZED") {
			i++
		}
		if i >= len(toks) || !toks[i].sym("(") {
			if list {
				a.lists = append(a.lists, -1)
			}
			return
		}
		if list {
			a.lists = append(a.lists, i)
		}
		i = a.close[i] + 1
		if i >= len(toks) || !toks[i].sym(",") {
			return
		}
		i++
	}
}

// def records the name at i as a table or an alias.
func (a *analysis) def(i int) {
	a.defs[i] = true
	a.rels[strings.ToLower(a.toks[i].text)] = true
}

// starItem reports whether the * at i is a select item, such as * or t.*,
// and not part of an expression.
func (a *analysis) starItem(i int) bool {
	toks := a.toks
	if !toks[i].sym("*") {
		return false
	}
	j := i
	for j >= 2 && toks[j-1].sym(".") && toks[j-2].ident() {
		j -= 2
	}
	if j > 0 && !toks[j-1].sym(",") && !toks[j-1].is("SELECT", "DISTINCT", "ALL") {
		return false
	}
	return i+1 == len(toks) || toks[i+1].sym(",") || toks[i+1].sym(")") || toks[i+1].sym(";") ||
		toks[i+1].is("FROM", "EXCEPT", "REPLACE", "EXCLUDE")
}

// rows marks the tokens that read a whole row of a table.
func (a *analysis) rows() {
	toks := a.toks
	for i, t := range toks {
		switch {
		case t.sym("*"):
			a.whole[i] = i > 0 && toks[i-1].sym(".") && !a.starItem(i)
		case t.kind == word || t.kind == quoted:
			a.whole[i] = !a.defs[i] && a.rels[strings.ToLower(t.text)] &&
				(i == 0 || !toks[i-1].sym(".") && !toks[i-1].is("AS")) &&
				(i+1 == len(toks) || !toks[i+1].sym(".") && !toks[i+1].sym("("))
		}
	}
}

// aliases taints the alias of each expression that reads a masked column,
// so that a later use of the alias is masked too, as ClickHouse allows.
func (a *analysis) aliases() {
	for changed := true; changed; {
		changed = false
		for i, t := range a.toks {
			if !t.is("AS") || i+1 >= len(a.toks) || !a.toks[i+1].ident() || a.cast(i) {
				continue
			}
			r, _ := a.mentions(a.segment(i), i, false)
			if a.wholeIn(a.segment(i), i) {
				r = max(r, len(a.m.rules)-1)
			}
			if a.tainted(a.toks[i+1].text, r) {
				changed = true
			}
		}
	}
}

// tainted records the alias as reading the rule r. It reports whether this
// is new.
func (a *analysis) tainted(alias string, r int) bool {
	key := strings.ToLower(alias)
	if t, ok := a.taint[key]; r < 0 || ok && t >= r {
		return false
	}
	a.taint[key] = r
	return true
}

// cast reports whether the AS at i is in a CAST or a CONVERT, where a type
// follows it.
func (a *analysis) cast(i int) bool {
	o := a.open[i]
	return o > 0 && a.toks[o-1].is("CAST", "TRY_CAST", "SAFE_CAST", "CONVERT", "TRY_CONVERT")
}

// segment returns the start of the expression that ends before i.
func (a *analysis) segment(i int) int {
	d := a.depth[i]
	for j := i - 1; j >= 0; j-- {
		t := a.toks[j]
		if a.depth[j] < d || a.depth[j] == d && (t.sym(",") || t.sym(";") ||
			t.is("SELECT", "WHERE", "ON", "AND", "OR", "HAVING", "BY", "PREWHERE", "WITH", "WHEN", "THEN", "ELSE", "SET", "DISTINCT", "ALL")) {
			return j + 1
		}
	}
	return 0
}

// wholeIn reports whether a token in [i, j) reads a whole row.
func (a *analysis) wholeIn(i, j int) bool {
	for k := i; k < j && k < len(a.whole); k++ {
		if a.whole[k] {
			return true
		}
	}
	return false
}

// item is one expression of a select list.
type item struct {
	star bool
	hit  hit
}

// hit is the rule that hides a column, or -1, and whether the column gets
// the whole mask pattern. A column that is an expression gets the whole mask
// pattern, because the method of a rule shows a part of the value, and the
// parts of many expressions, such as substr(phone, k, 3) for each k, give
// back the value.
type hit struct {
	rule int
	full bool
}

// none is a hit for a column that no rule hides.
var none = hit{rule: -1}

// join returns the hit that hides a column that both h and o hide. The rule
// with the highest priority wins, and the whole mask pattern wins.
func (h hit) join(o hit) hit {
	switch {
	case o.rule < 0:
		return h
	case h.rule < 0:
		return o
	}
	return hit{rule: max(h.rule, o.rule), full: h.full || o.full}
}

// plan says which rule hides each column of the result of a statement.
type plan struct {
	// items holds the select list of a plain SELECT, in order, or nil when
	// the statement is not one.
	items []item
	// all hides every column.
	all hit
	// mention hides every column when the select list is not known. Its rule
	// is the rule that a word of the statement matches, or -1.
	mention hit
}

// plan reads the statement.
func (m *Masker) plan(query string) plan {
	a := m.analyze(query)
	top := hit{rule: len(m.rules) - 1, full: true}
	p := plan{all: none, mention: hit{rule: a.mention, full: true}}
	if a.renamed {
		p.all = top
	}
	if !a.simple() {
		if a.wholeIn(0, len(a.toks)) {
			p.all = top
		}
		return p
	}
	mention, whole := a.groupsRead(0)
	if whole {
		p.all = top
	}
	if mention {
		return p
	}
	p.items = a.selectList()
	return p
}

// groupsRead reports whether a subquery or the arguments of a table function
// among the tables names a masked column, and whether one reads a whole row.
// With depth 0, only the tables of the outer statement count, and with -1,
// all of them count.
func (a *analysis) groupsRead(depth int) (bool, bool) {
	mention, whole := false, false
	for _, g := range a.groups {
		if depth >= 0 && a.depth[g] != depth {
			continue
		}
		if r, _ := a.mentions(g, a.close[g]+1, true); r >= 0 {
			mention = true
		}
		if a.wholeIn(g, a.close[g]+1) {
			whole = true
		}
	}
	return mention, whole
}

// simple reports whether the statement is one plain SELECT, so that each
// expression of its select list gives one column.
func (a *analysis) simple() bool {
	if a.unsure || a.renamed || len(a.toks) == 0 || !a.toks[0].is("SELECT") {
		return false
	}
	for i, t := range a.toks {
		if a.depth[i] != 0 {
			continue
		}
		if t.is("UNION", "INTERSECT", "EXCEPT", "MINUS") || t.sym(";") && i+1 < len(a.toks) {
			return false
		}
	}
	return true
}

// selectList reads the select list of a plain SELECT.
func (a *analysis) selectList() []item {
	toks := a.toks
	i := a.modifiers(1)
	var items []item
	start := i
	for ; i <= len(toks); i++ {
		end := i == len(toks)
		if !end && a.depth[i] == 0 {
			t := toks[i]
			end = t.sym(";") || t.is("FROM", "INTO", "WHERE", "GROUP", "HAVING", "ORDER", "LIMIT", "UNION", "INTERSECT", "EXCEPT", "MINUS", "FOR")
		}
		if !end && !(a.depth[i] == 0 && toks[i].sym(",")) {
			continue
		}
		if i == start {
			return nil
		}
		items = append(items, a.item(start, i))
		start = i + 1
		if end {
			break
		}
	}
	return items
}

// modifiers skips the words such as DISTINCT or TOP 10 at the start of a
// select list at i.
func (a *analysis) modifiers(i int) int {
	toks := a.toks
	for i < len(toks) {
		switch t := toks[i]; {
		case t.is("DISTINCT"):
			i++
			if i+1 < len(toks) && toks[i].is("ON") && toks[i+1].sym("(") {
				i = a.close[i+1] + 1
			}
		case t.is("ALL", "DISTINCTROW", "UNIQUE", "HIGH_PRIORITY", "STRAIGHT_JOIN", "SQL_SMALL_RESULT",
			"SQL_BIG_RESULT", "SQL_BUFFER_RESULT", "SQL_NO_CACHE", "SQL_CACHE", "SQL_CALC_FOUND_ROWS"):
			i++
		case t.is("TOP"):
			i++
			switch {
			case i < len(toks) && toks[i].sym("("):
				i = a.close[i] + 1
			case i < len(toks):
				i++
			}
			if i < len(toks) && toks[i].is("PERCENT") {
				i++
			}
			if i+1 < len(toks) && toks[i].is("WITH") && toks[i+1].is("TIES") {
				i += 2
			}
		default:
			return i
		}
	}
	return i
}

// item reads the expression in [i, j) of a select list. An expression that
// names a masked column anywhere, even in a function or a subquery, is
// masked by the rule of that column. An expression that reads a whole row
// is masked by the rule with the highest priority, because the columns of
// the row are not known. Only a plain reference to a masked column, with or
// without an alias, is masked by the method of the rule. Any other
// expression gets the whole mask pattern.
func (a *analysis) item(i, j int) item {
	toks := a.toks
	if a.isStar(i, j) {
		return item{star: true, hit: none}
	}
	r, _ := a.mentions(i, j, true)
	if a.wholeIn(i, j) {
		r = len(a.m.rules) - 1
	}
	if r >= 0 && j-i >= 2 && toks[j-1].ident() {
		if p := toks[j-2]; p.kind != symbol || p.sym(")") || p.sym("]") {
			a.tainted(toks[j-1].text, r)
		}
	}
	d, ok := a.reference(i, j)
	return item{hit: hit{rule: r, full: !ok || d < r}}
}

// reference reads the expression in [i, j) as a plain reference to a column,
// such as phone, t.phone, "phone" AS p or p = phone. It returns the rule that
// matches the column, and whether the expression is such a reference.
func (a *analysis) reference(i, j int) (int, bool) {
	toks := a.toks
	k := i
	if j-i >= 3 && toks[i].ident() && toks[i+1].sym("=") {
		k = i + 2
	}
	if k >= j || !toks[k].ident() {
		return -1, false
	}
	for k+2 < j && toks[k+1].sym(".") && toks[k+2].ident() {
		k += 2
	}
	name := toks[k].text
	k++
	if k < j && toks[k].is("AS") {
		k++
	}
	if k < j && toks[k].ident() {
		k++
	}
	if k != j {
		return -1, false
	}
	return a.m.find(name), true
}

// isStar reports whether the tokens in [i, j) are * or a qualified star, such
// as t.* or s.t.*.
func (a *analysis) isStar(i, j int) bool {
	if j-i == 1 {
		return a.toks[i].sym("*")
	}
	if (j-i)%2 == 0 || !a.toks[j-1].sym("*") {
		return false
	}
	for k := i; k < j-1; k += 2 {
		if !a.toks[k].ident() || !a.toks[k+1].sym(".") {
			return false
		}
	}
	return true
}

// mapping returns the item of each of n columns, or -1 for a column that a
// star gives. It returns nil when the items do not fit the columns.
func (p plan) mapping(n int) []int {
	first, last, stars := -1, -1, 0
	for i, it := range p.items {
		if it.star {
			if first < 0 {
				first = i
			}
			last = i
			stars++
		}
	}
	fixed := len(p.items) - stars
	switch {
	case stars == 0 && fixed != n, fixed > n:
		return nil
	}
	for i := first + 1; i < last; i++ {
		if !p.items[i].star {
			return nil
		}
	}
	idx := make([]int, n)
	for i := range idx {
		idx[i] = -1
	}
	if stars == 0 {
		for i := range idx {
			idx[i] = i
		}
		return idx
	}
	for i := 0; i < first; i++ {
		idx[i] = i
	}
	after := len(p.items) - last - 1
	for i := 0; i < after; i++ {
		idx[n-after+i] = last + 1 + i
	}
	return idx
}

// columns returns how each column of a result set is hidden. A column is
// hidden by the method of a rule when its name matches the rule. In the first
// result set of a plain SELECT, it is also hidden when its expression names a
// masked column. In any other result set, every column gets the whole mask
// pattern when the statement names a masked column anywhere.
func (p plan) columns(m *Masker, cols []string, set int) []hit {
	var idx []int
	if set == 0 && p.items != nil {
		idx = p.mapping(len(cols))
	}
	hits := make([]hit, len(cols))
	for i, c := range cols {
		h := hit{rule: m.find(c)}.join(p.all)
		switch {
		case idx == nil:
			h = h.join(p.mention)
		case idx[i] >= 0:
			h = h.join(p.items[idx[i]].hit)
		}
		hits[i] = h
	}
	return hits
}

// Check returns an error for a statement that can copy or rename the value
// of a masked column into a place that the rules do not hide, such as a new
// table, a view, a variable, a prepared statement or another column. A copy
// passes when it does not name a masked column, does not read a whole row,
// and does not read a star.
func (m *Masker) Check(query string) error {
	if m.Empty() {
		return nil
	}
	a := m.analyze(query)
	if len(a.toks) == 0 {
		return nil
	}
	risky, stars := a.reads()
	refuse := false
	switch a.kind() {
	case "SELECT", "WITH", "TABLE", "VALUES":
		refuse = (risky || stars) && (a.has(false, "INTO", "INSERT", "MERGE") || a.assigns()) || a.updates()
	case "INSERT", "UPSERT", "REPLACE":
		refuse = (risky || stars) && a.has(false, "SELECT") || a.source("INTO", "INSERT") || a.updates()
	case "UPDATE":
		mention, whole := a.groupsRead(-1)
		refuse = mention || whole || a.renamed || a.updates()
	case "MERGE":
		refuse = risky || stars
	case "CREATE":
		switch {
		case a.has(true, "SELECT") || a.source("CREATE", "TEMP", "TEMPORARY", "UNLOGGED", "GLOBAL", "LOCAL", "EXTERNAL", "OR", "REPLACE", "IF", "EXISTS", "NOT"):
			refuse = risky || stars || a.columnList()
		case a.has(false, "VIEW", "FUNCTION", "PROCEDURE", "PROC", "TRIGGER", "RULE", "PACKAGE", "MACRO", "EVENT"):
			refuse = risky
		}
	case "COPY":
		refuse = risky || stars || a.has(false, "TO")
	case "ALTER", "RENAME", "EXEC", "EXECUTE", "CALL", "DO", "DECLARE", "BEGIN", "SET", "PREPARE",
		"PRINT", "RAISE", "LOAD", "UNLOAD", "HANDLER":
		refuse = risky
	}
	if !refuse {
		return nil
	}
	if a.cause != "" {
		return fmt.Errorf("%w: it can copy or rename the value of the masked column %q", ErrRefused, a.cause)
	}
	return fmt.Errorf("%w: it can copy or rename the value of a masked column", ErrRefused)
}

// reads reports whether the statement can read a masked column: it names
// one, reads a whole row, or renames the columns of a table. It also reports
// whether the statement reads a star, which gives every column of a table.
func (a *analysis) reads() (bool, bool) {
	risky := a.mention >= 0 || a.renamed || a.wholeIn(0, len(a.toks))
	for i := range a.toks {
		if a.starItem(i) {
			return risky, true
		}
	}
	return risky, false
}

// quotedText matches the quoted parts of an error message.
var quotedText = regexp.MustCompile("'[^']*'|\"[^\"]*\"|`[^`]*`")

// Redact hides the quoted parts of an error that are not in the query, when
// the query can read a masked column. A database error can quote a value, as
// in `invalid input syntax for type integer: "0912345678"`. A quoted part that
// is in the query, such as a name or a string that the query holds, stays.
func (m *Masker) Redact(query string, err error) error {
	if err == nil || m.Empty() {
		return err
	}
	if risky, stars := m.analyze(query).reads(); !risky && !stars {
		return err
	}
	msg := err.Error()
	out := quotedText.ReplaceAllStringFunc(msg, func(s string) string {
		if strings.Contains(query, s[1:len(s)-1]) {
			return s
		}
		return s[:1] + "***" + s[len(s)-1:]
	})
	if out == msg {
		return err
	}
	return errors.New(out)
}

// kind returns the first word of the statement in upper case. For EXPLAIN,
// it is the first word of the statement that EXPLAIN runs.
func (a *analysis) kind() string {
	i := 0
	for i < len(a.toks) && a.toks[i].sym("(") {
		i++
	}
	if i >= len(a.toks) {
		return ""
	}
	if a.toks[i].is("EXPLAIN") {
		for _, t := range a.toks[i+1:] {
			if t.is("SELECT", "WITH", "TABLE", "VALUES", "INSERT", "UPSERT", "REPLACE", "UPDATE", "MERGE", "CREATE", "DELETE", "EXECUTE") {
				return strings.ToUpper(t.text)
			}
		}
	}
	return strings.ToUpper(a.toks[i].text)
}

// has reports whether one of the words is in the statement. With strs, a
// word in a string or a comment counts too, as in the body of a function.
func (a *analysis) has(strs bool, words ...string) bool {
	for _, t := range a.toks {
		if t.is(words...) {
			return true
		}
		if !strs {
			continue
		}
		for _, w := range strings.FieldsFunc(t.text+" "+t.note, func(r rune) bool { return !isWordRune(r) }) {
			if (token{kind: word, text: w}).is(words...) {
				return true
			}
		}
	}
	return false
}

// source reports whether the statement reads a table with TABLE t, as in
// "INSERT INTO x TABLE t" or "CREATE TABLE x AS TABLE t". A TABLE after one
// of the words names the table that the statement writes, and does not
// count.
func (a *analysis) source(words ...string) bool {
	for i, t := range a.toks {
		if t.is("TABLE") && i > 0 && !a.toks[i-1].is(words...) {
			return true
		}
	}
	return false
}

// columnList reports whether a list of column names comes before AS, as in
// "CREATE VIEW v (a, b) AS", which renames the columns that the query gives.
func (a *analysis) columnList() bool {
	for i, t := range a.toks {
		if t.sym(")") && a.depth[i] == 0 && i+1 < len(a.toks) && a.toks[i+1].is("AS") {
			return true
		}
	}
	return false
}

// assigns reports whether the statement sets a variable, as in "SELECT @v =
// x" or "SELECT @v := x".
func (a *analysis) assigns() bool {
	toks := a.toks
	for i, t := range toks {
		if t.kind != word || !strings.HasPrefix(t.text, "@") || strings.HasPrefix(t.text, "@@") || i+1 >= len(toks) {
			continue
		}
		if toks[i+1].sym("=") || toks[i+1].sym(":") && i+2 < len(toks) && toks[i+2].sym("=") {
			return true
		}
	}
	return false
}

// updates reports whether an assignment after SET, or after ON DUPLICATE KEY
// UPDATE, puts the value of a masked column or of a whole row into a column
// that the same rule does not hide, as in "UPDATE t SET note = phone".
func (a *analysis) updates() bool {
	for i, t := range a.toks {
		if t.is("SET") || t.is("UPDATE") && i > 0 && a.toks[i-1].is("KEY") {
			if a.assignments(i + 1) {
				return true
			}
		}
	}
	return false
}

// assignments reads the list of assignments at i.
func (a *analysis) assignments(i int) bool {
	toks := a.toks
	if i >= len(toks) {
		return false
	}
	d := a.depth[i]
	target, eq := i, -1
	for j := i; ; j++ {
		end := j >= len(toks) || a.depth[j] < d
		next := end
		if !end && a.depth[j] == d {
			t := toks[j]
			end = t.sym(";") || t.is("WHERE", "FROM", "RETURNING", "OUTPUT", "ORDER", "LIMIT", "ON")
			next = end || t.sym(",")
			if t.sym("=") && eq < 0 {
				eq = j
			}
		}
		if !next {
			continue
		}
		if eq > target {
			value, _ := a.mentions(eq+1, j, false)
			if a.wholeIn(eq+1, j) {
				value = len(a.m.rules) - 1
			}
			if value >= 0 && value > a.least(target, eq) {
				return true
			}
		}
		if end {
			return false
		}
		target, eq = j+1, -1
	}
}

// least returns the lowest rule that hides a name in the target [i, j) of an
// assignment, or -1 when a name in it is not hidden.
func (a *analysis) least(i, j int) int {
	least := len(a.m.rules)
	for k := i; k < j; k++ {
		t := a.toks[k]
		if t.kind != word && t.kind != quoted || k+1 < j && a.toks[k+1].sym(".") {
			continue
		}
		least = min(least, a.m.find(t.text))
	}
	if least == len(a.m.rules) {
		return -1
	}
	return least
}
