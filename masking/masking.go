// Package masking hides the values of columns in a result set.
//
// The JumpServer connector passes the rules in the data-masking-rules
// parameter of the connection URL, as a JSON list. The rules are the same as
// the rules that the connector applies to its own result sets, and they have
// the same meaning.
package masking

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Param is the name of the connection URL parameter that holds the rules.
const Param = "data-masking-rules"

// Rule is one masking rule.
type Rule struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	FieldsPattern string `json:"fields_pattern"`
	MaskingMethod string `json:"masking_method"`
	MaskPattern   string `json:"mask_pattern"`
	Priority      int    `json:"priority"`
	IsActive      bool   `json:"is_active"`
}

// rule is a rule with its column patterns compiled.
type rule struct {
	Rule
	patterns []*regexp.Regexp
}

// matches reports whether the rule applies to the column.
func (r rule) matches(column string) bool {
	for _, p := range r.patterns {
		if p.MatchString(column) {
			return true
		}
	}
	return false
}

// Masker applies a list of rules. The zero value, and a nil Masker, mask
// nothing.
type Masker struct {
	rules []rule
}

// Parse returns a Masker for a JSON list of rules. A rule that is not active
// is dropped.
func Parse(s string) (*Masker, error) {
	var rules []Rule
	if err := json.Unmarshal([]byte(s), &rules); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", Param, err)
	}
	return New(rules), nil
}

// New returns a Masker for the rules. The rules apply in order of priority,
// and when more than one rule matches a column, the rule with the highest
// priority number wins.
func New(rules []Rule) *Masker {
	active := make([]Rule, 0, len(rules))
	for _, r := range rules {
		if r.IsActive {
			active = append(active, r)
		}
	}
	sort.SliceStable(active, func(i, j int) bool {
		return active[i].Priority < active[j].Priority
	})
	m := &Masker{rules: make([]rule, 0, len(active))}
	for _, r := range active {
		c := rule{Rule: r}
		for _, field := range strings.Split(r.FieldsPattern, ",") {
			field = strings.TrimSpace(field)
			if field == "" {
				continue
			}
			// a star matches any run of characters, and the name of a column
			// is not case sensitive
			expr := strings.ReplaceAll(regexp.QuoteMeta(field), `\*`, ".*")
			c.patterns = append(c.patterns, regexp.MustCompile("(?i)^"+expr+"$"))
		}
		if len(c.patterns) != 0 {
			m.rules = append(m.rules, c)
		}
	}
	return m
}

// Empty reports whether m has no rule to apply.
func (m *Masker) Empty() bool {
	return m == nil || len(m.rules) == 0
}

// Join returns a Masker with the rules of m and of o. A new connection adds
// its rules to the rules of the session, so that it cannot remove one.
func (m *Masker) Join(o *Masker) *Masker {
	switch {
	case m.Empty():
		return o
	case o.Empty():
		return m
	}
	rules := make([]Rule, 0, len(m.rules)+len(o.rules))
	for _, r := range m.rules {
		rules = append(rules, r.Rule)
	}
	for _, r := range o.rules {
		rules = append(rules, r.Rule)
	}
	return New(rules)
}

// Matches reports whether a rule hides the column.
func (m *Masker) Matches(column string) bool {
	if m == nil {
		return false
	}
	for _, r := range m.rules {
		if r.matches(column) {
			return true
		}
	}
	return false
}

// Mask returns the value of the column after the rules apply to it.
func (m *Masker) Mask(column, value string) string {
	if m == nil {
		return value
	}
	if i := m.find(column); i >= 0 {
		return apply(m.rules[i].Rule, value)
	}
	return value
}

// apply hides the value by the method of the rule. A method that is not known
// gives the mask pattern, so that a typo in a rule hides the value.
func apply(r Rule, value string) string {
	chars := []rune(value)
	switch r.MaskingMethod {
	case "hide_middle":
		if len(chars) < 3 {
			return r.MaskPattern
		}
		return string(chars[:1]) + strings.Repeat("*", len(chars)-2) + string(chars[len(chars)-1:])
	case "keep_prefix":
		if len(chars) <= 2 {
			return "####"
		}
		return string(chars[:2]) + strings.Repeat("*", len(chars)-2)
	case "keep_suffix":
		if len(chars) <= 2 {
			return "####"
		}
		return strings.Repeat("*", len(chars)-2) + string(chars[len(chars)-2:])
	default:
		return r.MaskPattern
	}
}

// cover hides the value by the rule. With full, the value becomes the mask
// pattern of the rule, or ###### when the rule has none, whatever the method.
func cover(r Rule, value string, full bool) string {
	switch {
	case !full:
		return apply(r, value)
	case r.MaskPattern != "":
		return r.MaskPattern
	}
	return "######"
}

// Rows is a result set that hides the columns that the rules match, by the
// name of the column or by the expression in the query that gives it.
type Rows struct {
	*sql.Rows
	m    *Masker
	plan plan
	// set counts the result sets before the current one.
	set int
	// hits holds how each column of the current result set is hidden. It is
	// nil until the columns are read.
	hits []hit
}

// Wrap returns rows with the columns that the rules match hidden. The query
// is the statement that gave the rows. A column is hidden when its name
// matches a rule, and also when its expression in the query names a masked
// column, as in "SELECT phone AS p" or "SELECT upper(phone)". When the query
// is not a plain SELECT, such as a UNION or a WITH, and it names a masked
// column anywhere, every column is hidden. A column that is not a plain
// reference to a masked column gets the whole mask pattern.
func (m *Masker) Wrap(rows *sql.Rows, query string) *Rows {
	return &Rows{Rows: rows, m: m, plan: m.plan(query)}
}

// init reads the columns of the current result set.
func (r *Rows) init() error {
	if r.hits != nil {
		return nil
	}
	cols, err := r.Rows.Columns()
	if err != nil {
		return err
	}
	r.hits = r.plan.columns(r.m, cols, r.set)
	return nil
}

// ScanTypes fills dest with the scan destination of each column of the
// current result set. A column that a rule hides is scanned into an any,
// whatever its type, so that the mask can replace the value. Other columns
// use f, or an any when f is nil.
func (r *Rows) ScanTypes(f func(*sql.ColumnType) (any, error), dest []any) error {
	if err := r.init(); err != nil {
		return err
	}
	var cts []*sql.ColumnType
	if f != nil {
		var err error
		if cts, err = r.Rows.ColumnTypes(); err != nil {
			return err
		}
		if len(cts) != len(dest) {
			return fmt.Errorf("masking: %d column types for %d columns", len(cts), len(dest))
		}
	}
	for i := range dest {
		if f == nil || i < len(r.hits) && r.hits[i].rule >= 0 {
			dest[i] = new(any)
			continue
		}
		var err error
		if dest[i], err = f(cts[i]); err != nil {
			return err
		}
	}
	return nil
}

// Scan scans the row, then replaces the value of each hidden column.
func (r *Rows) Scan(dest ...any) error {
	if err := r.Rows.Scan(dest...); err != nil {
		return err
	}
	if err := r.init(); err != nil {
		return err
	}
	for i, d := range dest {
		if i < len(r.hits) && r.hits[i].rule >= 0 {
			hide(d, r.m.rules[r.hits[i].rule].Rule, r.hits[i].full)
		}
	}
	return nil
}

// NextResultSet moves to the next result set, which has its own columns.
func (r *Rows) NextResultSet() bool {
	r.hits = nil
	r.set++
	return r.Rows.NextResultSet()
}

// hide replaces the value in the scan destination with its mask by the rule.
// A NULL stays a NULL. A destination of a type that cannot hold text is
// cleared, so that the value is never shown.
func hide(dest any, rule Rule, full bool) {
	switch d := dest.(type) {
	case *any:
		if *d != nil {
			*d = cover(rule, text(*d), full)
		}
	case *sql.NullString:
		if d.Valid {
			d.String = cover(rule, d.String, full)
		}
	default:
		if v := reflect.ValueOf(dest); v.Kind() == reflect.Pointer && !v.IsNil() {
			v.Elem().SetZero()
		}
	}
}

// text returns the text of a scanned value.
func text(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	case fmt.Stringer:
		return x.String()
	}
	return fmt.Sprint(v)
}
