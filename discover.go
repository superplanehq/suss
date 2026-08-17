package suss

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

var projectManifests = map[string]struct{}{
	"package.json": {},
	"go.mod":       {},
	"mix.exs":      {},
}

var skippedDirectories = map[string]struct{}{
	"node_modules": {},
	"vendor":       {},
	"_build":       {},
	"deps":         {},
	"dist":         {},
	"target":       {},
}

func findProjectRoots(root string) ([]string, error) {
	found := make(map[string]struct{})

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			if shouldSkipDirectory(entry) {
				return filepath.SkipDir
			}
			return nil
		}
		if _, ok := projectManifests[entry.Name()]; !ok {
			return nil
		}

		relative, err := relativeProjectPath(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		found[relative] = struct{}{}
		return nil
	})
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(found))
	for path := range found {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return paths, nil
}

func shouldSkipDirectory(entry fs.DirEntry) bool {
	name := entry.Name()
	if strings.HasPrefix(name, ".") {
		return true
	}
	if _, skipped := skippedDirectories[name]; skipped {
		return true
	}
	return entry.Type()&os.ModeSymlink != 0
}

func relativeProjectPath(root, dir string) (string, error) {
	relative, err := filepath.Rel(root, dir)
	if err != nil {
		return "", err
	}
	relative = filepath.ToSlash(relative)
	if relative == "." || relative == "" {
		return ".", nil
	}
	if strings.HasPrefix(relative, "../") || relative == ".." {
		return "", fs.ErrInvalid
	}
	return relative, nil
}
