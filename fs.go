package tmplutil

import "io/fs"

type overrideFS struct {
	base     fs.FS
	override fs.FS
}

// OverrideFS creates a new filesystem that overrides base. This is useful for
// letting the user override certain template files.
func OverrideFS(base, override fs.FS) fs.FS {
	return overrideFS{base, override}
}

func (ov overrideFS) Open(name string) (fs.File, error) {
	f, err := ov.override.Open(name)
	if err != nil {
		f, err = ov.base.Open(name)
	}
	return f, err
}
