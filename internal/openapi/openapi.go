// Package openapi loads and checks OpenAPI 3.0/3.1 documents. It resolves
// local $ref files only, confined to the root document's folder, and never
// reads a URL.
package openapi

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

const (
	// MaxDocument bounds the root document.
	MaxDocument = 16 << 20
	// MaxFiles bounds every file one document resolves, the root included.
	MaxFiles = 64
	// MaxTotal bounds the bytes of every file one document resolves.
	MaxTotal = 32 << 20
)

// Spec is a parsed document and the local files it was read from.
type Spec struct {
	Root  string
	Doc   *openapi3.T
	Files []string
}

// Load reads path and its local $ref files. A $ref with a scheme, an absolute
// path outside the root's folder, a `..` escape or a symbolic link out of the
// folder is refused rather than followed. The root's folder is the boundary.
func Load(path string) (*Spec, error) {
	return load(path, nil)
}

// LoadSource parses source as the document at path, so edited text still
// resolves its local $ref files relative to the saved file's folder.
func LoadSource(path string, source []byte) (*Spec, error) {
	return load(path, source)
}

func load(path string, source []byte) (*Spec, error) {
	root, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(root))
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("the OpenAPI document must be a regular file")
	}
	if info.Size() > MaxDocument && source == nil {
		return nil, fmt.Errorf("OpenAPI documents are limited to %d MiB", MaxDocument>>20)
	}
	if len(source) > MaxDocument {
		return nil, fmt.Errorf("OpenAPI documents are limited to %d MiB", MaxDocument>>20)
	}
	root = filepath.Join(dir, filepath.Base(root))
	spec := &Spec{Root: root}
	var total int64
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true
	loader.ReadFromURIFunc = func(_ *openapi3.Loader, location *url.URL) ([]byte, error) {
		file, err := confinedPath(dir, location)
		if err != nil {
			return nil, err
		}
		if len(spec.Files) >= MaxFiles {
			return nil, fmt.Errorf("the document resolves more than %d files", MaxFiles)
		}
		info, err := os.Stat(file)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s is not a regular file", filepath.Base(file))
		}
		if info.Size() > MaxDocument {
			return nil, fmt.Errorf("%s exceeds %d MiB", filepath.Base(file), MaxDocument>>20)
		}
		total += info.Size()
		if total > MaxTotal {
			return nil, fmt.Errorf("the document resolves more than %d MiB", MaxTotal>>20)
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		spec.Files = append(spec.Files, file)
		return data, nil
	}
	var doc *openapi3.T
	if source == nil {
		doc, err = loader.LoadFromFile(root)
	} else {
		doc, err = loader.LoadFromDataWithPath(source, &url.URL{Path: filepath.ToSlash(root)})
	}
	if err != nil {
		return nil, err
	}
	if version := doc.OpenAPIMajorMinor(); version != "3.0" && version != "3.1" {
		return nil, fmt.Errorf("supported versions are OpenAPI 3.0.x and 3.1.x, not %q", doc.OpenAPI)
	}
	if err := doc.Validate(context.Background(), openapi3.EnableMultiError()); err != nil {
		return nil, err
	}
	spec.Doc = doc
	return spec, nil
}

// confinedPath turns a loader location into a file under dir. A URL with a
// scheme other than file is a remote ref, which is never fetched.
func confinedPath(dir string, location *url.URL) (string, error) {
	if location == nil {
		return "", errors.New("empty $ref")
	}
	if location.Scheme != "" && location.Scheme != "file" {
		return "", fmt.Errorf("remote $ref %q is not allowed; use a local file", location.Scheme+"://"+location.Host+location.Path)
	}
	if location.Host != "" && location.Host != "localhost" {
		return "", fmt.Errorf("remote $ref host %q is not allowed", location.Host)
	}
	name := location.Path
	if name == "" {
		name = location.Opaque
	}
	if name == "" {
		return "", errors.New("empty $ref path")
	}
	file := filepath.Clean(filepath.FromSlash(name))
	if !filepath.IsAbs(file) {
		file = filepath.Join(dir, file)
	}
	// Resolve symbolic links before the boundary check so a link cannot
	// point outside the root's folder.
	real, err := filepath.EvalSymlinks(file)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(dir, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%s is outside the document's folder", filepath.Base(real))
	}
	return real, nil
}
