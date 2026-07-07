module github.com/ematvey/kvt

go 1.25.0

require gopkg.in/yaml.v3 v3.0.1

require (
	github.com/asg017/sqlite-vec-go-bindings v0.1.6
	github.com/mattn/go-sqlite3 v1.14.47
)

replace github.com/asg017/sqlite-vec-go-bindings => ./third_party/sqlite-vec-go-bindings
