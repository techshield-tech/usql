// Package dameng defines and registers usql's Dameng DM8 driver.
//
// dburl v0.53.0 has no Dameng scheme, so this package registers the scheme
// with dburl as well as the driver with usql. See D6.
//
// The driver is gitee.com/chunanyong/dm, which is pure Go and writes no global
// state in its init. It calls rand.Seed when it picks a standby server, and
// usql does not use the global random source.
//
// The security package of the driver imports the Go plugin package on Linux
// and macOS, to load a third party cipher library. In a build with cgo, that
// import forces the linker to export every symbol. The same import added
// about 45 MB to a build with an earlier Dameng driver. So the driver is in
// the all group and not in most, and the connector release adds the dameng
// tag by name. A build without cgo links a stub and stays small.
//
// See: https://gitee.com/chunanyong/dm
// Group: all
// Build: !(linux && arm)
package dameng

import (
	"context"
	"database/sql"
	"strings"

	_ "gitee.com/chunanyong/dm" // DRIVER: dm
	"github.com/techshield-tech/usql/drivers"
	"github.com/xo/dburl"
)

// Name is the name of the scheme, and of the driver in usql.
const Name = "dameng"

// driverName is the name that the dm package registers with database/sql.
const driverName = "dm"

func init() {
	dburl.Register(Scheme())
	drivers.Register(Name, drivers.Driver{
		Version:       version,
		IsPasswordErr: isPasswordErr,
	})
}

// Scheme returns the dburl scheme of Dameng. gen.go reads it, because dburl
// does not list it in BaseSchemes.
func Scheme() dburl.Scheme {
	gen := dburl.GenFromURL("dm://localhost:5236/")
	return dburl.Scheme{
		Name: Name,
		Generator: func(u *dburl.URL) (string, string, error) {
			dsn, _, err := gen(u)
			return dsn, driverName, err
		},
		Transport:  dburl.TransportTCP,
		Aliases:    []string{"dm", "dm8"},
		Dialect:    Name,
		Desc:       "Dameng DM8",
		Home:       "https://www.dameng.com",
		DriverURL:  "https://gitee.com/chunanyong/dm",
		GoPackage:  "gitee.com/chunanyong/dm",
		Deployment: dburl.DeploymentServer,
	}
}

// version returns the banner of the server. It never fails, because a
// connection that works must not be refused for want of a banner.
func version(ctx context.Context, db drivers.DB) (string, error) {
	var banner sql.NullString
	err := db.QueryRowContext(ctx, `SELECT BANNER FROM SYS.V$VERSION WHERE ROWNUM = 1`).Scan(&banner)
	if err != nil || banner.String == "" {
		return "Dameng DM8", nil
	}
	return banner.String, nil
}

// isPasswordErr reports whether the server refused the user name or the
// password. The server writes the message in English or in Chinese.
func isPasswordErr(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, s := range []string{"password", "密码", "口令", "用户名"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
