# tmplutil

A small `html/template` helper package to combine with
[Parcello](https://github.com/phogolabs/parcello).

## Usage

```go
package web

//go:generate go run github.com/phogolabs/parcello/cmd/parcello -r -i *.go

// Templater is the global template tree.
var Templater = tmplutil.Templater{
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

To update templates, run `go generate ./...`.
