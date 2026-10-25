package storage

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Storage struct {
	root *os.Root
	path string
}

func Open(path string) (*Storage, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf("open storage root: %w", err)
	}
	return &Storage{root: root, path: path}, nil
}

func (s *Storage) Close() error { return s.root.Close() }

func ValidName(name string) bool {
	if name == "" || name == "." || name == ".." || strings.TrimSpace(name) != name || utf8.RuneCountInString(name) > 80 || !utf8.ValidString(name) {
		return false
	}
	for _, r := range name {
		if r == '/' || r == '\\' || r == 0 || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validArea(area string) bool {
	if len(area) == 0 || len(area) > 100 {
		return false
	}
	for _, r := range area {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func (s *Storage) area(area string, create bool) (*os.Root, error) {
	if !validArea(area) {
		return nil, errors.New("invalid user storage area")
	}
	if create {
		if err := s.root.Mkdir(area, 0700); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
	}
	info, err := s.root.Lstat(area)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, errors.New("user storage area is not a real directory")
	}
	return s.root.OpenRoot(area)
}

func (s *Storage) Create(area, name string) (string, error) {
	if !ValidName(name) {
		return "", errors.New("invalid folder name")
	}
	root, err := s.area(area, true)
	if err != nil {
		return "", fmt.Errorf("open user storage area: %w", err)
	}
	defer root.Close()
	if err := root.Mkdir(name, 0700); err != nil {
		return "", fmt.Errorf("create folder: %w", err)
	}
	return filepath.Join(s.path, area, name), nil
}

func (s *Storage) Path(area, name string) (string, error) {
	if !validArea(area) || !ValidName(name) {
		return "", errors.New("invalid storage path")
	}
	return filepath.Join(s.path, area, name), nil
}

func (s *Storage) Exists(area, name string) (bool, error) {
	if !ValidName(name) {
		return false, errors.New("invalid folder name")
	}
	root, err := s.area(area, false)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer root.Close()
	info, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("registered folder path is not a real directory")
	}
	return true, nil
}

func (s *Storage) List(area string) ([]string, error) {
	root, err := s.area(area, false)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	names, err := dir.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	return names, nil
}

func (s *Storage) Remove(area, name string) error {
	if !ValidName(name) {
		return errors.New("invalid folder name")
	}
	root, err := s.area(area, false)
	if err != nil {
		return err
	}
	defer root.Close()
	info, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("folder path is not a real directory")
	}
	return root.RemoveAll(name)
}
