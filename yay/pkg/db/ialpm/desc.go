// SPDX-License-Identifier: GPL-2.0-or-later
//
// Database format parsing in this file follows pacman 7.1.0's
// lib/libalpm/be_local.c and lib/libalpm/be_sync.c.
// Copyright (c) 2006-2025 Pacman Development Team <pacman-dev@lists.archlinux.org>

package ialpm

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	pacmanconf "github.com/Morganamilo/go-pacmanconf"
	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"

	"github.com/emad-elsaid/fest/yay/pkg/alpm"
	"github.com/emad-elsaid/fest/yay/pkg/text"
)

// loadLocalDB reads every package under <dbpath>/local.
func loadLocalDB(dbPath string, shouldIgnore func(name string, groups []string) bool, logger *text.Logger) (*alpm.DB, error) {
	db := alpm.NewDB("local")

	entries, err := os.ReadDir(filepath.Join(dbPath, "local"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return db, nil
		}

		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pkgDir := filepath.Join(dbPath, "local", entry.Name())

		fields, err := readDesc(filepath.Join(pkgDir, "desc"))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}

			if logger != nil {
				logger.Warnln(fmt.Sprintf("skipping unreadable local database entry %s: %v", pkgDir, err))
			}

			continue
		}

		name := first(fields, "NAME")
		if name == "" {
			continue
		}

		groups := alpm.StringList(fields["GROUPS"])
		pkg := alpm.NewPackage(alpm.PackageData{
			Name:            name,
			Version:         first(fields, "VERSION"),
			Base:            first(fields, "BASE"),
			Description:     first(fields, "DESC"),
			URL:             first(fields, "URL"),
			Architecture:    first(fields, "ARCH"),
			Packager:        first(fields, "PACKAGER"),
			Validation:      parseValidation(fields["VALIDATION"]),
			BuildDate:       unixTime(first(fields, "BUILDDATE")),
			InstallDate:     unixTime(first(fields, "INSTALLDATE")),
			ISize:           parseInt(first(fields, "SIZE")),
			Reason:          parseReason(first(fields, "REASON")),
			Origin:          alpm.FromLocalDB,
			Depends:         parseDeps(fields["DEPENDS"]),
			OptionalDepends: parseDeps(fields["OPTDEPENDS"]),
			CheckDepends:    parseDeps(fields["CHECKDEPENDS"]),
			MakeDepends:     parseDeps(fields["MAKEDEPENDS"]),
			Conflicts:       parseDeps(fields["CONFLICTS"]),
			Provides:        parseDeps(fields["PROVIDES"]),
			Replaces:        parseDeps(fields["REPLACES"]),
			Groups:          groups,
			Licenses:        alpm.StringList(fields["LICENSE"]),
			DB:              db,
			ShouldIgnore:    func() bool { return shouldIgnore(name, groups) },
			FilesLoader:     func() ([]alpm.File, error) { return readFiles(filepath.Join(pkgDir, "files")) },
			BackupLoader:    func() alpm.BackupList { return readBackups(filepath.Join(pkgDir, "files")) },
		})
		db.Add(pkg)
	}

	return db, nil
}

// loadSyncDBs reads every repository in pacman.conf that has a database on
// disk. Missing or corrupt databases are kept as empty or partial caches so a
// bad mirror does not make every query fail.
func loadSyncDBs(conf *pacmanconf.Config, dbPath string, shouldIgnore func(name string, groups []string) bool, logger *text.Logger) (*alpm.DBList, []*alpm.DB) {
	dbs := make([]*alpm.DB, 0, len(conf.Repos))

	for _, repo := range conf.Repos {
		db := alpm.NewDB(repo.Name)
		db.SetServers(repo.Servers)
		db.SetUsage(parseUsage(repo.Usage))

		path := filepath.Join(dbPath, "sync", repo.Name+".db")
		if err := readSyncDB(path, db, shouldIgnore); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				if logger != nil {
					logger.Debugln(fmt.Sprintf("sync database not found: %s", path))
				}
			} else if logger != nil {
				logger.Warnln(fmt.Sprintf("using partial sync database %s: %v", path, err))
			}
		}

		dbs = append(dbs, db)
	}

	list := alpm.NewDBList()
	for _, db := range dbs {
		list.Append(db)
	}

	return list, dbs
}

// readSyncDB parses a pacman sync database, which is a compressed tar archive
// containing <pkgname>-<version>/{desc,depends,files} entries per package.
// Older repo databases split metadata across those files, so they are merged
// before a package is built.
func readSyncDB(filename string, db *alpm.DB, shouldIgnore func(name string, groups []string) bool) error {
	reader, err := openDatabase(filename)
	if err != nil {
		return err
	}
	defer reader.Close()

	tr := tar.NewReader(reader)

	entries := make(map[string]map[string][]string)
	var order []string
	var parseErr error

	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			parseErr = err

			break
		}

		if header.Typeflag != tar.TypeReg {
			continue
		}

		dir, base := path.Split(strings.TrimSuffix(header.Name, "/"))
		dir = strings.TrimSuffix(dir, "/")
		if dir == "" || (base != "desc" && base != "depends" && base != "files") {
			continue
		}

		fields, err := parseDesc(tr)
		if err != nil {
			parseErr = err

			break
		}

		merged, ok := entries[dir]
		if !ok {
			merged = make(map[string][]string)
			entries[dir] = merged
			order = append(order, dir)
		}

		for key, values := range fields {
			merged[key] = append(merged[key], values...)
		}
	}

	for _, dir := range order {
		fields := entries[dir]

		name := first(fields, "NAME")
		if name == "" {
			continue
		}

		groups := alpm.StringList(fields["GROUPS"])
		db.Add(alpm.NewPackage(alpm.PackageData{
			Name:            name,
			Version:         first(fields, "VERSION"),
			Base:            first(fields, "BASE"),
			Description:     first(fields, "DESC"),
			URL:             first(fields, "URL"),
			Architecture:    first(fields, "ARCH"),
			Packager:        first(fields, "PACKAGER"),
			FileName:        first(fields, "FILENAME"),
			SHA256Sum:       first(fields, "SHA256SUM"),
			Base64Signature: first(fields, "PGPSIG"),
			BuildDate:       unixTime(first(fields, "BUILDDATE")),
			InstallDate:     time.Unix(0, 0),
			Size:            parseInt(first(fields, "CSIZE")),
			ISize:           parseInt(first(fields, "ISIZE")),
			Origin:          alpm.FromSyncDB,
			Depends:         parseDeps(fields["DEPENDS"]),
			OptionalDepends: parseDeps(fields["OPTDEPENDS"]),
			CheckDepends:    parseDeps(fields["CHECKDEPENDS"]),
			MakeDepends:     parseDeps(fields["MAKEDEPENDS"]),
			Conflicts:       parseDeps(fields["CONFLICTS"]),
			Provides:        parseDeps(fields["PROVIDES"]),
			Replaces:        parseDeps(fields["REPLACES"]),
			Groups:          groups,
			Licenses:        alpm.StringList(fields["LICENSE"]),
			DB:              db,
			ShouldIgnore:    func() bool { return shouldIgnore(name, groups) },
			FilesLoader:     syncFilesLoader(fields["FILES"]),
		}))
	}

	return parseErr
}

// syncFilesLoader returns a lazy loader for a sync database file list.
func syncFilesLoader(names []string) func() ([]alpm.File, error) {
	if len(names) == 0 {
		return nil
	}

	return func() ([]alpm.File, error) {
		files := make([]alpm.File, len(names))
		for i, name := range names {
			files[i] = alpm.File{Name: name}
		}

		return files, nil
	}
}

// readDesc parses a pacman desc file.
func readDesc(path string) (map[string][]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return parseDesc(file)
}

// readFiles parses a local database files file into a package file list.
func readFiles(path string) ([]alpm.File, error) {
	fields, err := readDesc(path)
	if err != nil {
		return nil, err
	}

	names := fields["FILES"]
	files := make([]alpm.File, len(names))
	for i, name := range names {
		files[i] = alpm.File{Name: name}
	}

	return files, nil
}

// readBackups parses a local database files file into a backup list.
func readBackups(path string) alpm.BackupList {
	fields, err := readDesc(path)
	if err != nil {
		return nil
	}

	return parseBackup(fields["BACKUP"])
}

// parseDesc parses pacman's %KEY% value block format.
func parseDesc(reader io.Reader) (map[string][]string, error) {
	fields := make(map[string][]string)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	key := ""

	for scanner.Scan() {
		line := scanner.Text()

		if len(line) > 2 && line[0] == '%' && line[len(line)-1] == '%' {
			key = line[1 : len(line)-1]
			if _, ok := fields[key]; !ok {
				fields[key] = nil
			}

			continue
		}

		if line == "" {
			key = ""

			continue
		}

		if key != "" {
			fields[key] = append(fields[key], line)
		}
	}

	return fields, scanner.Err()
}

// openDatabase opens a sync database, transparently handling the compression
// formats pacman supports.
func openDatabase(path string) (io.ReadCloser, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	buffered := bufio.NewReaderSize(file, 64*1024)

	magic, err := buffered.Peek(6)
	if err != nil && !errors.Is(err, io.EOF) {
		file.Close()

		return nil, err
	}

	switch {
	case bytes.HasPrefix(magic, []byte{0x1f, 0x8b}):
		gzipReader, err := gzip.NewReader(buffered)
		if err != nil {
			file.Close()

			return nil, err
		}

		return &databaseReader{Reader: gzipReader, closeFns: []func() error{gzipReader.Close, file.Close}}, nil
	case bytes.HasPrefix(magic, []byte{0x28, 0xb5, 0x2f, 0xfd}):
		zstdReader, err := zstd.NewReader(buffered)
		if err != nil {
			file.Close()

			return nil, err
		}

		closeZstd := func() error { zstdReader.Close(); return nil }

		return &databaseReader{Reader: zstdReader, closeFns: []func() error{closeZstd, file.Close}}, nil
	case bytes.HasPrefix(magic, []byte{0xfd, '7', 'z', 'X', 'Z', 0x00}):
		xzReader, err := xz.NewReader(buffered)
		if err != nil {
			file.Close()

			return nil, err
		}

		return &databaseReader{Reader: xzReader, closeFns: []func() error{file.Close}}, nil
	case bytes.HasPrefix(magic, []byte("BZh")):
		return &databaseReader{Reader: bzip2.NewReader(buffered), closeFns: []func() error{file.Close}}, nil
	default:
		return &databaseReader{Reader: buffered, closeFns: []func() error{file.Close}}, nil
	}
}

type databaseReader struct {
	io.Reader
	closeFns []func() error
}

func (r *databaseReader) Close() error {
	var firstErr error

	for _, closeFn := range r.closeFns {
		if err := closeFn(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

func first(fields map[string][]string, key string) string {
	values := fields[key]
	if len(values) == 0 {
		return ""
	}

	return values[0]
}

func parseInt(value string) int64 {
	parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)

	return parsed
}

func unixTime(value string) time.Time {
	if value == "" {
		return time.Unix(0, 0)
	}

	return time.Unix(parseInt(value), 0)
}

func parseReason(value string) alpm.PkgReason {
	if value == "1" {
		return alpm.PkgReasonDepend
	}

	return alpm.PkgReasonExplicit
}

func parseValidation(values []string) alpm.Validation {
	var validation alpm.Validation

	for _, value := range values {
		switch value {
		case "none":
			validation |= alpm.ValidationNone
		case "md5":
			validation |= alpm.ValidationMD5Sum
		case "sha256":
			validation |= alpm.ValidationSHA256Sum
		case "pgp":
			validation |= alpm.ValidationSignature
		}
	}

	return validation
}

func parseDeps(values []string) alpm.DependList {
	deps := make(alpm.DependList, 0, len(values))
	for _, value := range values {
		deps = append(deps, alpm.ParseDepend(value))
	}

	return deps
}

func parseBackup(values []string) alpm.BackupList {
	backups := make(alpm.BackupList, 0, len(values))
	for _, value := range values {
		name, hash, _ := strings.Cut(value, "\t")
		backups = append(backups, alpm.BackupFile{Name: name, Hash: hash})
	}

	return backups
}

func parseUsage(values []string) alpm.Usage {
	if len(values) == 0 {
		return alpm.UsageAll
	}

	var usage alpm.Usage

	for _, value := range values {
		switch value {
		case "Sync":
			usage |= alpm.UsageSync
		case "Search":
			usage |= alpm.UsageSearch
		case "Install":
			usage |= alpm.UsageInstall
		case "Upgrade":
			usage |= alpm.UsageUpgrade
		case "All":
			usage |= alpm.UsageAll
		}
	}

	if usage == 0 {
		return alpm.UsageAll
	}

	return usage
}
