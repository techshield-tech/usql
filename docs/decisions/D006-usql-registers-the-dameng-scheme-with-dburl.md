# D6. usql registers the Dameng scheme with dburl

Status: Proposed.

The JumpServer connector needs Dameng DM8. Hard rule 2 says a scheme must
exist in `dburl` before usql can use it, and dburl v0.26.0 removed the Dameng
scheme. This decision departs from that rule for this one scheme.

## What it means

- `drivers/dameng` calls `dburl.Register` from its `init`. The generator is
  `dburl.GenFromURL`, so usql still parses nothing itself.
- `gen.go` reads the scheme from `dameng.Scheme()`, because `dburl.BaseSchemes`
  does not list it. The README row and the build tag file come from there.
- The driver is `gitee.com/chunanyong/dm`. Its `security` package imports
  `plugin` on Linux and macOS, which adds about 45 MB to a build with cgo. So
  the driver is in the `all` group, and a build that wants it names the
  `dameng` tag.
- The driver registers no metadata reader (D3). The rule for a trailing
  semicolon and the folding of names belong in dbmeta (D5). Until dbmeta has
  a Dameng model, `\d` reports that it is not supported, and a statement keeps
  its trailing semicolon.

## Why not wait for dburl

The connector release cannot wait. If dburl takes the scheme back, delete the
call to `dburl.Register` and the entry in `gen.go`, and change nothing else.
