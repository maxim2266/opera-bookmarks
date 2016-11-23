#opera-bookmarks

Program `opera-bookmarks` reads Opera browser bookmarks and converts them to an HTML file, similar to what other
browsers but not Opera can do when saving bookmarks. Type `opera-bookmarks --help` for command line options.

### Compilation
```bash
go get -u github.com/juju/gnuflag
go build -o opera-bookmarks bm.go
```

##### License: BSD
##### Platform: Linux
Tested with Go v1.7.3 and Opera 41.0
