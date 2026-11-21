package gateexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// rootedFS never resolves a workspace entry through a symlink. The root and
// every child are opened beneath an anchored directory handle; unsupported
// openat2 kernels fail closed instead of falling back to a racy path check.
type rootedFS struct {
	dir  *os.File
	path string
	info fs.FileInfo
}

func openRoot(path string) (*rootedFS, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("workspace root: %w", err)
	}
	base, err := os.Open("/")
	if err != nil {
		return nil, fmt.Errorf("open filesystem root: %w", err)
	}
	defer base.Close()
	rel := strings.TrimPrefix(abs, "/")
	if rel == "" {
		rel = "."
	}
	fd, err := unix.Openat2(int(base.Fd()), rel, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_SYMLINKS,
	})
	if err != nil {
		return nil, fmt.Errorf("open workspace root: %w", err)
	}
	f := os.NewFile(uintptr(fd), abs)
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("stat workspace root: %w", err)
	}
	return &rootedFS{dir: f, path: abs, info: info}, nil
}

func (r *rootedFS) close() { r.dir.Close() }

func (r *rootedFS) unchanged() error {
	info, err := os.Lstat(r.path)
	if err != nil {
		return fmt.Errorf("workspace root changed: %w", err)
	}
	if !info.IsDir() || !os.SameFile(info, r.info) {
		return errors.New("workspace root replaced during access")
	}
	return nil
}

func (r *rootedFS) open(rel string, flags int) (*os.File, error) {
	if rel != "." && !filepath.IsLocal(rel) {
		return nil, fmt.Errorf("workspace path must be relative: %q", rel)
	}
	flags |= unix.O_CLOEXEC | unix.O_NOFOLLOW
	if flags&unix.O_PATH == 0 {
		flags |= unix.O_NONBLOCK
	}
	fd, err := unix.Openat2(int(r.dir.Fd()), rel, &unix.OpenHow{
		Flags:   uint64(flags),
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_SYMLINKS,
	})
	if err != nil {
		return nil, fmt.Errorf("open workspace entry %q: %w", rel, err)
	}
	return os.NewFile(uintptr(fd), rel), nil
}

// readFile permits only regular files and accounts for bytes actually read.
func (r *rootedFS) readFile(ctx context.Context, rel string, limit int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := r.open(rel, unix.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat %q: %w", rel, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular workspace file: %q", rel)
	}
	var out strings.Builder
	if err := copyBounded(ctx, &out, f, &limit); err != nil {
		return nil, err
	}
	if err := unchangedFile(f, info); err != nil {
		return nil, err
	}
	if err := r.unchanged(); err != nil {
		return nil, err
	}
	return []byte(out.String()), nil
}

func unchangedFile(f *os.File, before fs.FileInfo) error {
	after, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat completed read: %w", err)
	}
	a, aok := before.Sys().(*syscall.Stat_t)
	b, bok := after.Sys().(*syscall.Stat_t)
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || (aok && bok && a.Ctim != b.Ctim) {
		return fmt.Errorf("workspace file changed during read: %q", f.Name())
	}
	return nil
}

func copyBounded(ctx context.Context, dst io.Writer, src io.Reader, remaining *int64) error {
	buf := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		want := int64(len(buf))
		if want > *remaining+1 {
			want = *remaining + 1
		}
		n, err := src.Read(buf[:want])
		if int64(n) > *remaining {
			return ErrFingerprintLimit
		}
		*remaining -= int64(n)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return fmt.Errorf("write workspace content: %w", werr)
			}
		}
		if errors.Is(err, io.EOF) {
			return ctx.Err()
		}
		if err != nil {
			return fmt.Errorf("read workspace content: %w", err)
		}
	}
}

type treeEntry struct {
	rel  string
	info fs.FileInfo
	file *os.File
	link string
}

func (r *rootedFS) walk(ctx context.Context, ignore map[string]bool, visit func(treeEntry) error) error {
	var walk func(string) error
	walk = func(rel string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		dir, err := r.open(rel, unix.O_RDONLY|unix.O_DIRECTORY)
		if err != nil {
			return err
		}
		defer dir.Close()
		entries, err := dir.ReadDir(-1)
		if err != nil {
			return fmt.Errorf("read directory %q: %w", rel, err)
		}
		for _, entry := range entries {
			if ignore[entry.Name()] {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			name := filepath.Join(rel, entry.Name())
			metadata, err := r.open(name, unix.O_PATH)
			if err != nil {
				return err
			}
			info, err := metadata.Stat()
			metadata.Close()
			if err != nil {
				return fmt.Errorf("stat entry %q: %w", name, err)
			}
			e := treeEntry{rel: name, info: info}
			switch {
			case info.Mode()&fs.ModeSymlink != 0:
				buf := make([]byte, 4096)
				n, err := unix.Readlinkat(int(dir.Fd()), entry.Name(), buf)
				if err != nil {
					return fmt.Errorf("read link %q: %w", name, err)
				}
				if n == len(buf) {
					return fmt.Errorf("symlink target too long: %q", name)
				}
				e.link = string(buf[:n])
				if err := visit(e); err != nil {
					return err
				}
			case info.IsDir():
				if err := visit(e); err != nil {
					if errors.Is(err, fs.SkipDir) {
						continue
					}
					return err
				}
				if err := walk(name); err != nil {
					return err
				}
			case info.Mode().IsRegular():
				f, err := r.open(name, unix.O_RDONLY)
				if err != nil {
					return err
				}
				e.file = f
				opened, serr := f.Stat()
				if serr == nil && (!opened.Mode().IsRegular() || !os.SameFile(info, opened)) {
					serr = fmt.Errorf("workspace entry replaced: %q", name)
				}
				if serr == nil {
					serr = visit(e)
				}
				if serr == nil {
					serr = unchangedFile(f, info)
				}
				f.Close()
				if serr != nil {
					return serr
				}
			default:
				return fmt.Errorf("special workspace file is unsupported: %q", name)
			}
		}
		return nil
	}
	if err := walk("."); err != nil {
		return err
	}
	return r.unchanged()
}
