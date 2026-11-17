package nixpack

import (
	"archive/tar"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// writeCache creates a Nix binary cache from the given store paths and places
// it at out, either as a directory or as a tarball.
func writeCache(paths []string, out string) error {
	tmp, err := os.MkdirTemp("", "nixpack-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	input := []byte(strings.Join(paths, "\n") + "\n")
	args := []string{"copy", "--stdin", "--to", cacheURL(tmp)}
	if _, err := runStream("nix", args, input); err != nil {
		return fmt.Errorf("copy paths into binary cache: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "README.md"), bundleReadme(), 0o644); err != nil {
		return err
	}

	return placeOutput(tmp, out)
}

func cacheURL(dir string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(dir)}
	q := u.Query()
	q.Set("compression", "zstd")
	u.RawQuery = q.Encode()
	return u.String()
}

func isTar(out string) bool {
	return strings.HasSuffix(out, ".tar")
}

func placeOutput(src, out string) error {
	if isTar(out) {
		return archiveDir(src, out)
	}
	return copyDir(src, out)
}

func archiveDir(src, out string) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(f)

	err = filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return nil
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if info.IsDir() {
			header.Name += "/"
		}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		in, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, in)
		closeErr := in.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
	if err != nil {
		_ = tw.Close()
		_ = f.Close()
		return err
	}
	if err := tw.Close(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func copyDir(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		entries, err := os.ReadDir(dst)
		if err != nil {
			return err
		}
		if len(entries) > 0 {
			return fmt.Errorf("output directory %s already exists and is not empty", dst)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}

	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	return os.Rename(tmp, dst)
}

func bundleReadme() []byte {
	return []byte(`# nixpack bundle

This directory is a Nix binary cache created by nixpack. It contains the
store paths needed by the packed installables, including Nix derivations
(.drv files) and their source inputs.

## Install on another machine

If this came from a nixpack.tar archive, extract it first:

    tar -xf nixpack.tar -C ./nixpack

Then copy the cache into the target store:

    nix copy --all --no-check-sigs --from file:///absolute/path/to/this-directory

The --no-check-sigs flag is needed because locally built cache entries are
unsigned.
`)
}
