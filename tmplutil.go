package tmplutil

import (
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
)

// Log, if true, will log additional verbose information.
var Log = false

// Templater describes the template information to be constructed.
type Templater struct {
	// FileSystem is the filesystem to look up templates from. It must not be
	// nil.
	FileSystem fs.FS

	Includes  map[string]string // name -> path
	Functions template.FuncMap

	renderFail RenderFailFunc
	tmpl       template.Template
	once       sync.Once
}

// HTMLExtensions is the list of HTML file extensions that files must have to be
// considered a template.
var HTMLExtensions = []string{".html", ".htm"}

func isHTML(path string) bool {
	pathExt := filepath.Ext(path)

	for _, ext := range HTMLExtensions {
		if ext == pathExt {
			return true
		}
	}

	return false
}

// Preregister registers all templates with the filetype ".html" and ".htm" from
// the given FileSystem. The basename without the file extension will be used,
// and duplicated names will be ignored.
//
// Use the Subtemplate method to get the subtemplate, or call Register with an
// empty path.
//
// The list of valid filetypes to be considered templates can be changed in
// tmplutil.HTMLExtensions.
func Preregister(tmpler *Templater) *Templater {
	err := fs.WalkDir(tmpler.FileSystem, ".",
		func(fullPath string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			name := d.Name()
			if !isHTML(name) {
				return nil
			}

			name = filepath.Base(name)
			name = strings.TrimSuffix(name, filepath.Ext(name))

			if _, ok := tmpler.Includes[name]; ok {
				return nil
			}

			if Log {
				log.Println("Pre-registering", name, "at", fullPath)
			}

			tmpler.Includes[name] = fullPath
			return nil
		},
	)

	if err != nil {
		log.Fatalln("failed to glob:", err)
	}

	return tmpler
}

// RenderFailFunc is the function that's called when a template render fails.
// Refer to OnRenderFail.
type RenderFailFunc func(w io.Writer, tmpl string, err error)

// OnRenderFail sets the render fail function that would be called when a
// template render fails. The given writer is the request's writer.
func (tmpler *Templater) OnRenderFail(f RenderFailFunc) {
	tmpler.renderFail = f
}

func (tmpler *Templater) onRenderFail(w io.Writer, tmpl string, err error) {
	if err == nil || tmpler.renderFail == nil {
		return
	}

	if Log {
		log.Printf("[tmplutil] failed to render %q: %v\n", tmpl, err)
	}

	tmpler.renderFail(w, tmpl, err)
}

// Register registers a subtemplate. If a template is already not
// pre-registered, then it is registered. Otherwise, the pre-registered template
// is used.
func (tmpler *Templater) Register(name, path string) *Subtemplate {
	if _, ok := tmpler.Includes[name]; !ok {
		if Log {
			log.Println("Registering", path)
		}

		tmpler.Includes[name] = path
	}

	return &Subtemplate{tmpler, name}
}

// Subtemplate returns a registered subtemplate. Nil is returned otherwise.
func (tmpler *Templater) Subtemplate(name string) *Subtemplate {
	_, ok := tmpler.Includes[name]
	if ok {
		return &Subtemplate{tmpler, name}
	}

	return nil
}

// Execute executes any subtemplate.
func (tmpler *Templater) Execute(w io.Writer, tmpl string, v interface{}) error {
	tmpler.Preload()

	if err := tmpler.tmpl.ExecuteTemplate(w, tmpl, v); err != nil {
		tmpler.onRenderFail(w, tmpl, err)
		return err
	}

	return nil
}

// Func registers a function.
func (tmpler *Templater) Func(name string, fn interface{}) {
	if _, ok := tmpler.Functions[name]; ok {
		log.Panicln("error: duplicate function with name", name)
	}
	tmpler.Functions[name] = fn
}

// Preload preloads the templates once. If the templates are already
// preloaded, then it does nothing.
func (tmpler *Templater) Preload() {
	tmpler.once.Do(func() {
		tmpl := template.New("")
		tmpl = tmpl.Funcs(tmpler.Functions)
		for name, incl := range tmpler.Includes {
			tmpl = template.Must(tmpl.New(name).Parse(readFile(tmpler.FileSystem, incl)))
		}

		tmpler.tmpl = *tmpl
		tmpler.Includes = nil
	})
}

// Subtemplate describes a subtemplate that belongs to some parent template.
type Subtemplate struct {
	tmpl *Templater
	name string
}

// Execute executes the subtemplate.
func (sub *Subtemplate) Execute(w io.Writer, v interface{}) error {
	err := sub.tmpl.Execute(w, sub.name, v)
	sub.tmpl.onRenderFail(w, sub.name, err)
	return err
}

// MustSubFS forces creation of a sub-filesystem using fs.Sub. It panics on
// errors.
func MustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		log.Panicln(err)
	}
	return sub
}

func readFile(fsys fs.FS, filePath string) string {
	b, err := fs.ReadFile(fsys, filePath)
	if err != nil {
		log.Fatalln("failed to read file:", err)
	}

	return string(b)
}

// AlwaysFlush is the middleware to always flush after a write.
func AlwaysFlush(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(flushWriter{w, flusher}, r)
	})
}

type flushWriter struct {
	http.ResponseWriter
	flusher http.Flusher
}

func (f flushWriter) Write(b []byte) (int, error) {
	n, err := f.ResponseWriter.Write(b)
	if err != nil {
		return n, err
	}

	f.flusher.Flush()
	return n, nil
}
