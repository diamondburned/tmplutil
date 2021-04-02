# tmplutil

A small `html/template` helper package to combine with `go:embed`.

## Usage

```go
package web

//go:embed *
var webFS embed.FS

// Templater is the global template tree.
var Templater = tmplutil.Templater{
	FileSystem: webFS,
	Includes: map[string]string{
		"css":    "components/css.html",
		"header": "components/header.html",
		"footer": "components/footer.html",
	},
	Functions: template.FuncMap{},
}
```

```go
package pages

var index = web.Templater.Register("index", "pages/index.html")

func init() {
	web.Templater.Func("doThing", func() string {
		return "done Thing."
	})
}

func render(w http.ResponseWriter, r *http.Request) {
	index.Execute(w, data{})
}
```

The templates are updated on build/run.
