package masking

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func queryMasker() *Masker {
	return New([]Rule{
		{FieldsPattern: "phone", MaskingMethod: "hide_middle", Priority: 1, IsActive: true},
		{FieldsPattern: "email", MaskingMethod: "keep_prefix", Priority: 2, IsActive: true},
		{FieldsPattern: "name", MaskingMethod: "fixed_char", MaskPattern: "x", Priority: 3, IsActive: false},
	})
}

// hits gives each hit as p or e for the method of the phone or the email
// rule, P or E for the whole mask pattern of that rule, or - for none.
func hits(hs []hit) string {
	var s []string
	for _, h := range hs {
		c := "-"
		switch h.rule {
		case 0:
			c = "p"
		case 1:
			c = "e"
		}
		if h.full {
			c = strings.ToUpper(c)
		}
		s = append(s, c)
	}
	return strings.Join(s, " ")
}

func TestColumns(t *testing.T) {
	tests := []struct {
		query string
		cols  []string
		want  string
	}{
		{`select id, name from t`, []string{"id", "name"}, "- -"},
		{`select phone from t`, []string{"phone"}, "p"},
		{`select phone as pp from t`, []string{"pp"}, "p"},
		{`select phone pp, id from t`, []string{"pp", "id"}, "p -"},
		{`select concat(phone, '') from t`, []string{"concat(phone, '')"}, "P"},
		{`select phone||'' from t`, []string{"?column?"}, "P"},
		{`select upper(email) e from t`, []string{"e"}, "E"},
		{`select substr(phone, 1, 3) from t`, []string{"s"}, "P"},
		{`select coalesce(phone, email) c from t`, []string{"c"}, "E"},
		{`select t.phone from t`, []string{"phone"}, "p"},
		{`select T.PHONE as x from t`, []string{"x"}, "p"},
		{`select "phone" as x from t`, []string{"x"}, "p"},
		{"select `phone` as x from t", []string{"x"}, "p"},
		{`select [phone] as x from t`, []string{"x"}, "P"},
		{`select x = phone, id from t`, []string{"x", "id"}, "p -"},
		{`select distinct phone as p from t`, []string{"p"}, "p"},
		{`select top 5 phone as p from t`, []string{"p"}, "p"},
		{`select * from t`, []string{"id", "name", "phone"}, "- - p"},
		{`select t.* from t`, []string{"id", "phone"}, "- p"},
		{`select *, phone as p from t`, []string{"id", "phone", "p"}, "- p p"},
		{`select id, * from t`, []string{"id", "id", "phone"}, "- - p"},
		{`select id, name from t where phone = '1'`, []string{"id", "name"}, "- -"},
		{`select a.id from a join b on a.phone = b.phone`, []string{"id"}, "-"},
		{`select count(*) from t`, []string{"count"}, "-"},
		{`select id, (select phone from u where u.id = t.id) as p from t`, []string{"id", "p"}, "- P"},
		{`select pp from (select phone as pp from t) x`, []string{"pp"}, "P"},
		{`select * from (select phone as pp, id from t) x`, []string{"pp", "id"}, "P P"},
		{`select x from t, lateral (select t.phone as x) s`, []string{"x"}, "P"},
		{`select u from t, unnest(array[t.phone]) u`, []string{"u"}, "P"},
		{`select name from t union select phone from t`, []string{"name"}, "P"},
		{`select id from t union all select id from u`, []string{"id"}, "-"},
		{`with c as (select phone as pp from t) select pp from c`, []string{"pp"}, "P"},
		{`with c as (select id from t) select id from c`, []string{"id"}, "-"},
		{`with c (pp) as (select * from t) select pp from c`, []string{"pp"}, "E"},
		{`with c (pp) as (select t from t) select pp from c`, []string{"pp"}, "E"},
		{`with recursive r (n) as (select 1 union all select n + 1 from r where n < 3) select n from r`, []string{"n"}, "-"},
		{`select a from (select * from t) x (a)`, []string{"a"}, "E"},
		{`select a from (select id from t) x (a)`, []string{"a"}, "-"},
		{`select t from t`, []string{"t"}, "E"},
		{`select row_to_json(t) from t`, []string{"row_to_json"}, "E"},
		{`select id, to_json(x.*) from t x`, []string{"id", "to_json"}, "- E"},
		{`select (t.*)::text from t`, []string{"text"}, "E"},
		{`select * from t as x(a, b)`, []string{"a", "b"}, "E E"},
		{`select j from t, to_jsonb(t) j`, []string{"j"}, "E"},
		{`select phone as p, p as q from t`, []string{"p", "q"}, "p P"},
		{`select concat(p, '') from t where (phone as p) != ''`, []string{"c"}, "P"},
		{`select 'a\', phone from t`, []string{"a", "b"}, "P P"},
		{`select name /*! , phone */ from t`, []string{"name", "phone"}, "P P"},
		{`select id, name from t; select phone p from t`, []string{"id", "name"}, "P P"},
		{`show create table t`, []string{"Table", "Create Table"}, "- -"},
		{`show columns from t like 'phone'`, []string{"Field", "Type"}, "P P"},
		{`call p()`, []string{"a", "phone"}, "- p"},
		{`select name from v`, []string{"name"}, "-"},
		{`select extract(year from d), d from t`, []string{"y", "d"}, "- -"},
	}
	m := queryMasker()
	for _, tt := range tests {
		p := m.plan(tt.query)
		if got := hits(p.columns(m, tt.cols, 0)); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.query, got, tt.want)
		}
	}
}

func TestColumnsNextResultSet(t *testing.T) {
	m := queryMasker()
	p := m.plan(`select id from t; select phone p from t`)
	if got := hits(p.columns(m, []string{"p"}, 1)); got != "P" {
		t.Errorf("got %q, want every column hidden in a later result set", got)
	}
	p = m.plan(`select id from t`)
	if got := hits(p.columns(m, []string{"id", "phone"}, 1)); got != "- p" {
		t.Errorf("got %q, want the names to match in a later result set", got)
	}
}

func TestCover(t *testing.T) {
	r := Rule{MaskingMethod: "hide_middle", MaskPattern: "***"}
	if got := cover(r, "0912345678", false); got != "0********8" {
		t.Errorf("got %q, want the method", got)
	}
	if got := cover(r, "0912345678", true); got != "***" {
		t.Errorf("got %q, want the mask pattern", got)
	}
	if got := cover(Rule{MaskingMethod: "hide_middle"}, "0912345678", true); got != "######" {
		t.Errorf("got %q, want a mask for a rule with no pattern", got)
	}
}

func TestCheck(t *testing.T) {
	refused := []string{
		`create table x as select phone as p from t`,
		`create view v as select phone p from t`,
		`create view v (p) as select * from t`,
		`create table x (p) as table t`,
		`create function f() returns text as $$ select phone from t $$ language sql`,
		`insert into t2 select phone from t`,
		`insert into t2 select * from t`,
		`insert into t2 select row_to_json(t) from t`,
		`insert into t2 table t`,
		`insert into t (id) values (1) on duplicate key update note = phone`,
		`select phone into t2 from t`,
		`select phone into @v from t`,
		`select @v = phone from t`,
		`select @v := phone from t`,
		`with u as (update t set note = phone returning id) select * from u`,
		`update t set note = phone`,
		`update t set note = upper(t.email), id = 1 where id = 2`,
		`update t set note = s.pp from (select phone as pp from t) s where s.id = t.id`,
		`update t set note = row_to_json(t)::text`,
		`merge into t2 using t on t2.id = t.id when matched then update set t2.note = t.phone`,
		`alter table t rename column phone to pp`,
		`exec sp_rename 't.phone', 'pp', 'COLUMN'`,
		`prepare s as select phone as p from t`,
		`set @v = (select phone from t limit 1)`,
		`declare c cursor for select phone from t`,
		`explain analyze create table x as select phone from t`,
		`copy t to '/tmp/t.csv'`,
		`copy (select phone from t) to stdout`,
	}
	allowed := []string{
		`select phone as p from t`,
		`select name from t for update`,
		`select replace(phone, '1', '') from t`,
		`update t set phone = '1' where id = 1`,
		`update t set phone = upper(phone)`,
		`update t set note = 'phone'`,
		`update t set note = 'x' where phone = '1'`,
		`insert into t (id, phone) values (1, '090')`,
		`insert into t2 select id, note from t`,
		`create table t (id int, phone text)`,
		`create index i on t (phone)`,
		`create table x as select id from t`,
		`delete from t where phone = '1'`,
		`alter table t add column x int`,
		`drop table t`,
		`copy t from stdin`,
		``,
	}
	m := queryMasker()
	for _, q := range refused {
		if err := m.Check(q); !errors.Is(err, ErrRefused) {
			t.Errorf("%s: got %v, want it refused", q, err)
		}
	}
	for _, q := range allowed {
		if err := m.Check(q); err != nil {
			t.Errorf("%s: got %v, want it allowed", q, err)
		}
	}
	var none *Masker
	if err := none.Check(`create table x as select phone from t`); err != nil {
		t.Errorf("got %v, want no check without rules", err)
	}
}

func TestRedact(t *testing.T) {
	m := queryMasker()
	tests := []struct {
		query, err, want string
	}{
		{`select cast(phone as int) from t`, `invalid input syntax for type integer: "0912345678"`, `invalid input syntax for type integer: "***"`},
		{`select cast(phone as int) as "p" from t`, `converting the varchar value '0912345678' to "p"`, `converting the varchar value '***' to "p"`},
		{`select * from t where id = 'x'`, `bad value '0912345678'`, `bad value '***'`},
		{`select id from t where id = 'x'`, `bad value 'x'`, `bad value 'x'`},
		{`select id from t`, `bad value '0912345678'`, `bad value '0912345678'`},
	}
	for _, tt := range tests {
		if got := m.Redact(tt.query, errors.New(tt.err)).Error(); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.query, got, tt.want)
		}
	}
	if m.Redact(`select phone from t`, nil) != nil {
		t.Error("a nil error must stay nil")
	}
	err := errors.New(`bad value '0912345678'`)
	if got := (*Masker)(nil).Redact(`select phone from t`, err); got != err {
		t.Error("an error must stay the same without rules")
	}
}

func TestJoin(t *testing.T) {
	m := queryMasker()
	if got := m.Join(New(nil)); got != m {
		t.Error("a Masker with no rule must not change the rules")
	}
	if got := (*Masker)(nil).Join(New(nil)); !got.Empty() {
		t.Error("two empty Maskers must give an empty one")
	}
	j := m.Join(New([]Rule{{FieldsPattern: "card", MaskingMethod: "fixed_char", MaskPattern: "#", Priority: 9, IsActive: true}}))
	if j.Mask("phone", "0912345678") != "0********8" || j.Mask("card", "1234") != "#" {
		t.Error("Join must keep the rules of both")
	}
}

func TestLex(t *testing.T) {
	toks, lead, unsure := lex(`-- c1
select "a""b", 'it''s', $q$x$q$, n /* c2 */ from t`)
	if unsure || strings.TrimSpace(lead) != "c1" {
		t.Fatalf("unsure = %v, lead = %q", unsure, lead)
	}
	var texts []string
	for _, tok := range toks {
		texts = append(texts, tok.text)
	}
	want := []string{"select", `a"b`, ",", "it's", ",", "x", ",", "n", "from", "t"}
	if !slices.Equal(texts, want) {
		t.Errorf("got %q, want %q", texts, want)
	}
	if strings.TrimSpace(toks[7].note) != "c2" {
		t.Errorf("note = %q, want the comment", toks[7].note)
	}
	for _, s := range []string{`select 'a`, `select "a`, `select /* a`, `select 'a\'b'`, `select $$a`} {
		if _, _, unsure := lex(s); !unsure {
			t.Errorf("%s: want unsure", s)
		}
	}
}
