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
	result := value
	for _, r := range m.rules {
		if r.matches(column) {
			result = apply(r.Rule, value)
		}
	}
	return result
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

// ColumnTypes returns a func that builds the scan destination of a column. A
// column that a rule hides is scanned into an any, whatever its type, so that
// the mask can replace the value. Other columns use f, or an any when f is
// nil.
func (m *Masker) ColumnTypes(f func(*sql.ColumnType) (any, error)) func(*sql.ColumnType) (any, error) {
	return func(ct *sql.ColumnType) (any, error) {
		if f == nil || m.Matches(ct.Name()) {
			return new(any), nil
		}
		return f(ct)
	}
}

// Rows is a result set that hides the columns that the rules match.
type Rows struct {
	*sql.Rows
	m      *Masker
	cols   []string
	hidden []bool
}

// Wrap returns rows with the columns that the rules match hidden.
func (m *Masker) Wrap(rows *sql.Rows) *Rows {
	return &Rows{Rows: rows, m: m}
}

// Scan scans the row, then replaces the value of each hidden column.
func (r *Rows) Scan(dest ...any) error {
	if err := r.Rows.Scan(dest...); err != nil {
		return err
	}
	if r.cols == nil {
		cols, err := r.Rows.Columns()
		if err != nil {
			return err
		}
		r.cols, r.hidden = cols, make([]bool, len(cols))
		for i, c := range cols {
			r.hidden[i] = r.m.Matches(c)
		}
	}
	for i, d := range dest {
		if i < len(r.hidden) && r.hidden[i] {
			hide(d, r.m, r.cols[i])
		}
	}
	return nil
}

// NextResultSet moves to the next result set, which has its own columns.
func (r *Rows) NextResultSet() bool {
	r.cols, r.hidden = nil, nil
	return r.Rows.NextResultSet()
}

// hide replaces the value in the scan destination with its mask. A NULL stays
// a NULL. A destination of a type that cannot hold text is cleared, so that
// the value is never shown.
func hide(dest any, m *Masker, column string) {
	switch d := dest.(type) {
	case *any:
		if *d != nil {
			*d = m.Mask(column, text(*d))
		}
	case *sql.NullString:
		if d.Valid {
			d.String = m.Mask(column, d.String)
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
