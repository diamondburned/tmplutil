package tmplutil

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/yuin/goldmark"
)

// DebugMode, if true, will cause the following to happen:
//
//    - Errors and verbose template information will be logged.
//    - The template will be reloaded on every request.
//
// It will be toggled true if the environment variable "TMPL_DEBUG" is set to a
// non-empty value (e.g. 1).
var DebugMode = os.Getenv("TMPL_DEBUG") != ""

// Templater describes the template information to be constructed. Methods
// called on Templater is NOT thread-safe, so it should only be called primarily
// from the global scope or init.
//
// Templater must not be changed after it has been preloaded or executed. Doing
// so after is undefined behavior and will trigger race conditions.
type Templater struct {
	// FileSystem is the filesystem to look up templates from. It must not be
	// nil.
	FileSystem fs.FS

	Includes  map[string]string // name -> path
	Functions template.FuncMap

	// OnRenderFail is called when the renderer fails. This function can be used
	// to catch errors.
	OnRenderFail RenderFailFunc

	// Markdown, if not nil, will allow Templater to handle rendering .md files
	// to HTML after they're templated.
	Markdown goldmark.Markdown

	tmpl atomic.Value // htmlTemplate
}

// HTMLExtensions is the list of HTML file extensions that files must have to be
// considered a template.
var HTMLExtensions = []string{".html", ".htm", ".md"}

func isHTML(path string) bool {
	pathExt := filepath.Ext(path)

	for _, ext := range HTMLExtensions {
		if ext == pathExt {
			return true
		}
	}

	return false
}

// Preregister registers all templates with the filetype ".html", ".htm" and
// ".md" from the given FileSystem. The basename without the file extension will
// be used, and duplicated names will be ignored.
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

			if DebugMode {
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
type RenderFailFunc func(sub *Subtemplate, w io.Writer, err error)

// failWriter wraps around the writer to be used within onRenderFail to break
// the recursion chain.
type failWriter struct{ io.Writer }

func (tmpler *Templater) onRenderFail(w io.Writer, tmpl string, err error) {
	if err == nil {
		return
	}

	if DebugMode {
		log.Printf("[tmplutil] failed to render %q: %v\n", tmpl, err)
	}

	if tmpler.OnRenderFail != nil {
		// Check if we're already in an onRenderFail callchain by checking if the
		// writer is wrapped.
		if _, ok := w.(failWriter); ok {
			// Break the callchain if yes to avoid recursion loops.
			return
		}

		sub := &Subtemplate{tmpler, tmpl}
		tmpler.OnRenderFail(sub, failWriter{w}, err)
	}
}

// Register registers a subtemplate. If a template is already not
// pre-registered, then it is registered. Otherwise, the pre-registered template
// is used.
func (tmpler *Templater) Register(name, path string) *Subtemplate {
	if _, ok := tmpler.Includes[name]; !ok {
		if DebugMode {
			log.Println("Registering", path)
		}

		tmpler.Includes[name] = path
	}

	return &Subtemplate{tmpler, name}
}

// Override overrides the template source files. It does not re-render
// templates.
func (tmpler *Templater) Override(overrideFS fs.FS) {
	tmpler.FileSystem = OverrideFS(tmpler.FileSystem, overrideFS)
}

// Subtemplate returns a registered subtemplate. If the template isn't yet
// registered, a subtemplate instance will still be returned, but executing it
// will return an error.
func (tmpler *Templater) Subtemplate(name string) *Subtemplate {
	return &Subtemplate{tmpler, name}
}

func (tmpler *Templater) execute(w io.Writer, tmpl string, v interface{}) error {
	if err := tmpler.Load().ExecuteTemplate(w, tmpl, v); err != nil {
		tmpler.onRenderFail(w, tmpl, err)
		return err
	}
	return nil
}

// Execute executes any subtemplate.
func (tmpler *Templater) Execute(w io.Writer, tmpl string, v interface{}) error {
	if tmpler.Markdown != nil && filepath.Ext(tmpler.Includes[tmpl]) == ".md" {
		var out bytes.Buffer
		if err := tmpler.execute(&out, tmpl, v); err != nil {
			return err
		}

		if err := tmpler.Markdown.Convert(out.Bytes(), w); err != nil {
			tmpler.onRenderFail(w, tmpl, fmt.Errorf("failed to convert markdown: %w", err))
			return err
		}

		return nil
	}

	if err := tmpler.execute(w, tmpl, v); err != nil {
		return err
	}

	return nil
}

// Func registers a function; it should only be called before preloading. The
// function will panic if there's a duplicate function.
func (tmpler *Templater) Func(name string, fn interface{}) {
	if _, ok := tmpler.Functions[name]; ok {
		log.Panicln("error: duplicate function with name", name)
	}
	tmpler.Functions[name] = fn
}

// Preload preloads the templates once. If the templates are already
// preloaded, then it does nothing.
func (tmpler *Templater) Preload() {
	tmpler.Load()
}

// Load loads the templates. If the templates are already loaded, then it does
// nothing.
func (tmpler *Templater) Load() *template.Template {
load:
	tmpl, _ := tmpler.tmpl.Load().(*template.Template)
	if tmpl != nil {
		return tmpl
	}

	oldTmpl := tmpl

	tmpl = template.New("")
	tmpl = tmpl.Funcs(tmpler.Functions)
	for name, incl := range tmpler.Includes {
		tmpl = template.Must(tmpl.New(name).Parse(readFile(tmpler.FileSystem, incl)))
	}

	if DebugMode {
		// Don't store into tmpler.tmpl.
		return tmpl
	}

	if tmpler.tmpl.CompareAndSwap(oldTmpl, tmpl) {
		return tmpl
	}

	goto load
}

// Reset resets the template to its initial state.
func (tmpler *Templater) Reset() {
	tmpler.tmpl.Store((*template.Template)(nil))
}

// Subtemplate describes a subtemplate that belongs to some parent template.
type Subtemplate struct {
	tmpl *Templater
	name string
}

// Templater gets the parent templater of the subtemplate.
func (sub *Subtemplate) Templater() *Templater {
	return sub.tmpl
}

// Name gets the subtemplate's name.
func (sub *Subtemplate) Name() string {
	return sub.name
}

// Execute executes the subtemplate.
func (sub *Subtemplate) Execute(w io.Writer, v interface{}) error {
	return sub.tmpl.Execute(w, sub.name, v)
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
