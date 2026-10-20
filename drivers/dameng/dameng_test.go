package dameng

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/xo/dburl"
)

func TestScheme(t *testing.T) {
	for _, s := range []string{"dameng", "dm", "dm8"} {
		u, err := dburl.Parse(s + "://SYSDBA:p%40ss@db.example:5237/?schema=APP")
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if u.SchemeName != Name || u.Driver != driverName {
			t.Errorf("%s: scheme %q, driver %q, want %q and %q", s, u.SchemeName, u.Driver, Name, driverName)
		}
		// the dm driver parses the DSN when it makes a connector, before it
		// connects, so a DSN that it refuses fails here
		db, err := sql.Open(u.Driver, u.DSN)
		if err != nil {
			t.Fatalf("%s: %q: %v", s, u.DSN, err)
		}
		if _, ok := db.Driver().(driver.DriverContext); !ok {
			t.Fatal("the dm driver has no connector")
		}
		db.Close()
	}
}

func TestDefaultPort(t *testing.T) {
	u, err := dburl.Parse("dameng://SYSDBA:x@db.example/")
	if err != nil {
		t.Fatal(err)
	}
	if u.Port() != "" || !strings.Contains(u.DSN, "db.example:5236") {
		t.Errorf("DSN %q, want port 5236 by default", u.DSN)
	}
}

func TestIsPasswordErr(t *testing.T) {
	if !isPasswordErr(errors.New("Invalid username or password")) {
		t.Error("an English refusal must count")
	}
	if !isPasswordErr(errors.New("用户名或密码错误")) {
		t.Error("a Chinese refusal must count")
	}
	if isPasswordErr(errors.New("connection refused")) {
		t.Error("a network error must not count")
	}
}
