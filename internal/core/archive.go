package core

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// cleanEntry turns an archive entry name into a safe relative path, or ""
// when the entry would land outside the target folder.
func cleanEntry(name string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	name = strings.TrimLeft(name, "/")
	if name == "" || strings.Contains(name, ":") {
		return ""
	}
	var parts []string
	for _, p := range strings.Split(name, "/") {
		switch p {
		case "", ".":
		case "..":
			return ""
		default:
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "/")
}

// commonTop returns the single top-level folder shared by all names, or "".
func commonTop(names []string) string {
	top := ""
	for _, n := range names {
		if n == "" {
			continue
		}
		first, rest, found := strings.Cut(n, "/")
		if !found && rest == "" {
			// A file (or bare folder entry) at the top level.
			if top == "" {
				top = first
			} else if top != first {
				return ""
			}
			continue
		}
		if top == "" {
			top = first
		} else if top != first {
			return ""
		}
	}
	return top
}

func stripTop(name, top string) string {
	if top == "" {
		return name
	}
	if name == top {
		return ""
	}
	return strings.TrimPrefix(name, top+"/")
}

func writeEntry(dest, rel string, mode os.FileMode, r io.Reader) error {
	target := filepath.Join(dest, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	perm := os.FileMode(0o644)
	if mode&0o111 != 0 {
		perm = 0o755
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Unzip extracts a zip into dest. With strip set, a single top-level folder
// ("mysql-8.0.46-winx64/") is removed so the contents land directly in dest.
func Unzip(ctx context.Context, src, dest string, strip bool, progress func(done, total int)) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("打开压缩包失败：%w", err)
	}
	defer zr.Close()
	names := make([]string, len(zr.File))
	for i, f := range zr.File {
		names[i] = cleanEntry(f.Name)
	}
	top := ""
	if strip {
		top = commonTop(names)
		// Only strip when the top entry really is a folder.
		isDir := false
		for _, n := range names {
			if strings.HasPrefix(n, top+"/") {
				isDir = true
				break
			}
		}
		if !isDir {
			top = ""
		}
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	for i, f := range zr.File {
		if i%64 == 0 {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if progress != nil {
				progress(i, len(zr.File))
			}
		}
		rel := stripTop(names[i], top)
		if rel == "" {
			continue
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(filepath.Join(dest, filepath.FromSlash(rel)), 0o755); err != nil {
				return err
			}
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("解压 %s 失败：%w", f.Name, err)
		}
		err = writeEntry(dest, rel, f.Mode(), rc)
		rc.Close()
		if err != nil {
			return fmt.Errorf("解压 %s 失败：%w", f.Name, err)
		}
	}
	if progress != nil {
		progress(len(zr.File), len(zr.File))
	}
	return nil
}

// Untgz extracts a .tar.gz / .tgz into dest, with the same strip rule as Unzip.
func Untgz(ctx context.Context, src, dest string, strip bool) error {
	open := func() (*os.File, *tar.Reader, error) {
		f, err := os.Open(src)
		if err != nil {
			return nil, nil, err
		}
		gz, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			return nil, nil, fmt.Errorf("打开压缩包失败：%w", err)
		}
		return f, tar.NewReader(gz), nil
	}
	top := ""
	if strip {
		f, tr, err := open()
		if err != nil {
			return err
		}
		var names []string
		hasChild := false
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				f.Close()
				return fmt.Errorf("读取压缩包失败：%w", err)
			}
			n := cleanEntry(h.Name)
			names = append(names, n)
			hasChild = hasChild || strings.Contains(n, "/")
		}
		f.Close()
		if hasChild {
			top = commonTop(names)
		}
	}
	f, tr, err := open()
	if err != nil {
		return err
	}
	defer f.Close()
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	for n := 0; ; n++ {
		if n%64 == 0 && ctx.Err() != nil {
			return ctx.Err()
		}
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("读取压缩包失败：%w", err)
		}
		rel := stripTop(cleanEntry(h.Name), top)
		if rel == "" {
			continue
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(filepath.Join(dest, filepath.FromSlash(rel)), 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeEntry(dest, rel, os.FileMode(h.Mode), tr); err != nil {
				return fmt.Errorf("解压 %s 失败：%w", h.Name, err)
			}
		}
	}
}

// copyTree copies a folder; used when a rename across drives is not possible.
func copyTree(src, dest string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		return copyFile(path, target)
	})
}

// moveDir moves a folder, falling back to copy+delete across drives.
func moveDir(src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dest); err == nil {
		return nil
	}
	if err := copyTree(src, dest); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

func dirSize(path string) int64 {
	var total int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}
