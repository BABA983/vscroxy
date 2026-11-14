// Package library reads the portable request library directory tree without UI state.
package library

import (
	"cmp"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
)

const (
	requestFileSuffix = ".request.json"
	folderFileName    = "folder.json"
)

type folderFile struct {
	request.Folder
	Order []string `json:"order,omitempty"`
}

// Read reads the tree at root, in saved request order within each directory,
// followed by unlisted requests in file-name order, then subdirectories in
// unescaped name order. Dot-prefixed entries and files other than folder.json
// and regular *.request.json files are ignored. A missing or empty root is an
// empty library.
//
// decode receives each request's bytes and its collection path, derived from
// the directory rather than the file contents. It owns decoding the request
// and setting its collection; Read does not assign or validate request IDs.
// Folders include only non-root directories with nonempty settings. known
// holds the original bytes of every read library file, keyed by slash-separated
// path relative to root. On error, all returned collections are nil.
func Read[T any](root string, decode func(data []byte, collection string) (T, error)) (requests []T, folders []request.Folder, known map[string][]byte, err error) {
	known = map[string][]byte{}
	if root == "" {
		return nil, nil, known, nil
	}
	var walk func(dir, collection string) error
	walk = func(dir, collection string) error {
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
		if errors.Is(err, fs.ErrNotExist) && dir == "" {
			return nil
		}
		if err != nil {
			return err
		}
		var settings folderFile
		if data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path.Join(dir, folderFileName)))); err == nil {
			if err := json.Unmarshal(data, &settings); err != nil {
				return fmt.Errorf("%s: %w", path.Join(dir, folderFileName), err)
			}
			known[path.Join(dir, folderFileName)] = data
			settings.Path = collection
			if collection != "" && !folderEmpty(settings.Folder) {
				folders = append(folders, settings.Folder)
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		rank := map[string]int{}
		for i, stem := range settings.Order {
			rank[stem] = i + 1
		}
		var here []string
		var subdirs []fs.DirEntry
		for _, e := range entries {
			switch name := e.Name(); {
			case strings.HasPrefix(name, "."):
			case e.IsDir():
				subdirs = append(subdirs, e)
			case strings.HasSuffix(name, requestFileSuffix) && e.Type().IsRegular():
				here = append(here, strings.TrimSuffix(name, requestFileSuffix))
			}
		}
		// Listed requests keep their order; others follow by name.
		slices.SortFunc(here, func(x, y string) int {
			unlisted := func(stem string) int { return min(rank[stem], 1) ^ 1 }
			return cmp.Or(cmp.Compare(unlisted(x), unlisted(y)), cmp.Compare(rank[x], rank[y]), cmp.Compare(x, y))
		})
		for _, stem := range here {
			file := path.Join(dir, stem+requestFileSuffix)
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
			if err != nil {
				return err
			}
			r, err := decode(data, collection)
			if err != nil {
				return fmt.Errorf("%s: %w", file, err)
			}
			known[file] = data
			requests = append(requests, r)
		}
		slices.SortFunc(subdirs, func(x, y fs.DirEntry) int { return cmp.Compare(UnescapeName(x.Name()), UnescapeName(y.Name())) })
		for _, e := range subdirs {
			if err := walk(path.Join(dir, e.Name()), strings.TrimPrefix(collection+"/"+UnescapeName(e.Name()), "/")); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk("", ""); err != nil {
		return nil, nil, nil, err
	}
	return requests, folders, known, nil
}

func folderEmpty(f request.Folder) bool {
	return f.Description == "" && f.Auth == (request.Auth{}) && f.Pre == "" && f.Post == "" && len(f.Variables) == 0
}

// UnescapeName decodes a library file-name segment's %XX escapes. A percent
// sign that starts no valid escape stays as it is.
func UnescapeName(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if c, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				b.WriteByte(byte(c))
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// FindRequest returns the one request ref names: the request with that ID, or
// else the only request with that name. A name several requests share is
// refused rather than guessed at.
func FindRequest(requests []request.Request, ref string) (request.Request, error) {
	for _, r := range requests {
		if r.ID == ref {
			return r, nil
		}
	}
	var named []request.Request
	for _, r := range requests {
		if r.Name == ref {
			named = append(named, r)
		}
	}
	switch len(named) {
	case 0:
		return request.Request{}, fmt.Errorf("no request with the ID or name %q", ref)
	case 1:
		return named[0], nil
	default:
		return request.Request{}, fmt.Errorf("%d requests are named %q; call it by request ID", len(named), ref)
	}
}

// Resolver finds the requests a script calls with pm.execution.runRequest, with
// the folders each one lives under, from requests and folders already read.
func Resolver(requests []request.Request, folders []request.Folder) request.Resolver {
	return func(ref string) (request.Callee, error) {
		r, err := FindRequest(requests, ref)
		if err != nil {
			return request.Callee{}, err
		}
		return request.Callee{Request: r, Folders: request.Chain(folders, r.Collection)}, nil
	}
}
