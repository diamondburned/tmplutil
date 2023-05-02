package tmplutil

import (
	"io/fs"
	"path/filepath"
)

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

// FilterFileTypes creates a new filesystem that only contains files with the
// given file types.
func FilterFileTypes(fs fs.FS, fileTypes ...string) fs.FS {
	return filterFS{fs, fileTypes}
}

type filterFS struct {
	fs        fs.FS
	fileTypes []string
}

func isFileType(name string, fileTypes []string) bool {
	ext := filepath.Ext(name)
	for _, fileType := range fileTypes {
		if ext == fileType {
			return true
		}
	}
	return false
}

func (f filterFS) Open(name string) (fs.File, error) {
	file, err := f.fs.Open(name)
	if err != nil {
		return nil, err
	}

	// Just in case we do some chicanery with the given name, we'll check the
	// file's stat.
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}

	if !isFileType(stat.Name(), f.fileTypes) {
		return nil, fs.ErrNotExist
	}

	return file, nil
}
