package masking

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"testing"
)

const rules = `[
	{"id":"1","name":"phone","fields_pattern":"phone, mobile_*","masking_method":"hide_middle","mask_pattern":"******","priority":1,"is_active":true},
	{"id":"2","name":"card","fields_pattern":"CARD","masking_method":"keep_suffix","mask_pattern":"****","priority":2,"is_active":true},
	{"id":"3","name":"secret","fields_pattern":"secret","masking_method":"fixed_char","mask_pattern":"<hidden>","priority":3,"is_active":true},
	{"id":"4","name":"off","fields_pattern":"name","masking_method":"fixed_char","mask_pattern":"x","priority":4,"is_active":false}
]`

func TestMask(t *testing.T) {
	m, err := Parse(rules)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		column, value, want string
	}{
		{"phone", "0912345678", "0********8"},
		{"PHONE", "0912345678", "0********8"},
		{"mobile_home", "0912345678", "0********8"},
		{"phone", "ab", "******"},
		{"card", "1234567890", "********90"},
		{"secret", "abc", "<hidden>"},
		{"name", "Ken", "Ken"},
		{"phone_number", "0912345678", "0912345678"},
	}
	for _, tt := range tests {
		if got := m.Mask(tt.column, tt.value); got != tt.want {
			t.Errorf("Mask(%q, %q) = %q, want %q", tt.column, tt.value, got, tt.want)
		}
	}
}

func TestMaskPriority(t *testing.T) {
	m := New([]Rule{
		{FieldsPattern: "a", MaskingMethod: "fixed_char", MaskPattern: "late", Priority: 9, IsActive: true},
		{FieldsPattern: "a", MaskingMethod: "fixed_char", MaskPattern: "early", Priority: 1, IsActive: true},
	})
	if got := m.Mask("a", "v"); got != "late" {
		t.Errorf("got %q, want the rule with the highest priority number", got)
	}
}

func TestUnknownMethodHides(t *testing.T) {
	m := New([]Rule{{FieldsPattern: "a", MaskingMethod: "typo", MaskPattern: "#", IsActive: true}})
	if got := m.Mask("a", "secret"); got != "#" {
		t.Errorf("got %q, want the mask pattern", got)
	}
}

func TestParseError(t *testing.T) {
	if _, err := Parse(`{not json`); err == nil {
		t.Error("want an error for invalid JSON")
	}
}

func TestNilMasker(t *testing.T) {
	var m *Masker
	if !m.Empty() || m.Matches("a") || m.Mask("a", "v") != "v" {
		t.Error("a nil Masker must mask nothing")
	}
	if !New(nil).Empty() {
		t.Error("a Masker with no rule must be empty")
	}
}

// fakeDriver returns one table of fixed rows for any query.
type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return fakeConn{}, nil }

type fakeConn struct{}

func (fakeConn) Prepare(string) (driver.Stmt, error) { return fakeStmt{}, nil }
func (fakeConn) Close() error                        { return nil }
func (fakeConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }

type fakeStmt struct{}

func (fakeStmt) Close() error                               { return nil }
func (fakeStmt) NumInput() int                              { return -1 }
func (fakeStmt) Exec([]driver.Value) (driver.Result, error) { return nil, driver.ErrSkip }
func (fakeStmt) Query([]driver.Value) (driver.Rows, error)  { return &fakeRows{}, nil }

type fakeRows struct{ n int }

func (*fakeRows) Columns() []string { return []string{"id", "phone", "note"} }
func (*fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.n == 2 {
		return io.EOF
	}
	r.n++
	dest[0] = int64(r.n)
	dest[1] = []byte("0912345678")
	dest[2] = nil
	if r.n == 2 {
		dest[1] = nil
		dest[2] = "visible"
	}
	return nil
}

func TestRows(t *testing.T) {
	sql.Register("masking-fake", fakeDriver{})
	db, err := sql.Open("masking-fake", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.QueryContext(context.Background(), "select")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	m := New([]Rule{{FieldsPattern: "phone", MaskingMethod: "hide_middle", Priority: 1, IsActive: true}})
	r := m.Wrap(rows, "select")
	cts, err := r.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; r.Next(); n++ {
		dest := make([]any, len(cts))
		types := func(ct *sql.ColumnType) (any, error) {
			if ct.Name() == "id" {
				return new(sql.NullInt64), nil
			}
			return new(sql.NullString), nil
		}
		if err := r.ScanTypes(types, dest); err != nil {
			t.Fatal(err)
		}
		if _, ok := dest[0].(*sql.NullInt64); !ok {
			t.Errorf("id dest = %T, want the type that f gives", dest[0])
		}
		if err := r.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		phone := *dest[1].(*any)
		switch n {
		case 0:
			if phone != "0********8" {
				t.Errorf("row 0 phone = %v, want it hidden", phone)
			}
		case 1:
			if phone != nil {
				t.Errorf("row 1 phone = %v, want NULL to stay NULL", phone)
			}
		}
	}
}

func TestHideClearsOtherTypes(t *testing.T) {
	rule := Rule{FieldsPattern: "a", MaskingMethod: "fixed_char", MaskPattern: "#", IsActive: true}
	n := sql.NullInt64{Int64: 42, Valid: true}
	hide(&n, rule, false)
	if n.Valid || n.Int64 != 0 {
		t.Errorf("got %+v, want the value cleared", n)
	}
	s := sql.NullString{String: "abc", Valid: true}
	hide(&s, rule, false)
	if s.String != "#" {
		t.Errorf("got %q, want the mask", s.String)
	}
}
