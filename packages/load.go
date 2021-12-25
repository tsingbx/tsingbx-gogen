/*
 Copyright 2021 The GoPlus Authors (goplus.org)
 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at
     http://www.apache.org/licenses/LICENSE-2.0
 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package packages

import (
	"fmt"
	"go/token"
	"go/types"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/tools/go/gcexportdata"
)

type Config struct {
	// ModRoot specifies module root directory (required).
	ModRoot string

	// ModPath specifies module path (required).
	ModPath string

	// SupportedExts specifies all supported file extensions (optional).
	SupportedExts map[string]struct{}

	// Loaded specifies all loaded packages (optional).
	Loaded map[string]*types.Package

	// Fset provides source position information for syntax trees and types (optional).
	// If Fset is nil, Load will use a new fileset, but preserve Fset's value.
	Fset *token.FileSet
}

var (
	defaultSupportedExts = map[string]struct{}{
		".go": {},
	}
)

func (p *Config) getTempDir() string {
	return filepath.Join(p.ModRoot, ".gop/_dummy")
}

func (p *Config) listPkgs(pkgPaths []string, pat, modRoot string) ([]string, error) {
	const multi = "/..."
	recursive := strings.HasSuffix(pat, multi)
	if recursive {
		pat = pat[:len(pat)-len(multi)]
		if pat == "" {
			pat = "/"
		}
	}
	if strings.HasPrefix(pat, ".") || strings.HasPrefix(pat, "/") {
		patAbs, err1 := filepath.Abs(pat)
		patRel, err2 := filepath.Rel(modRoot, patAbs)
		if err1 != nil || err2 != nil || strings.HasPrefix(patRel, "..") {
			return nil, fmt.Errorf("directory `%s` outside available modules", pat)
		}
		exts := p.SupportedExts
		if exts == nil {
			exts = defaultSupportedExts
		}
		pkgPathBase := path.Join(p.ModPath, filepath.ToSlash(patRel))
		return doListPkgs(pkgPaths, pkgPathBase, pat, exts, recursive)
	} else {
		pkgPaths = append(pkgPaths, pat)
	}
	return pkgPaths, nil
}

func doListPkgs(pkgPaths []string, pkgPathBase, pat string, exts map[string]struct{}, recursive bool) ([]string, error) {
	fis, err := os.ReadDir(pat)
	if err != nil {
		return pkgPaths, err
	}
	noSouceFile := true
	for _, fi := range fis {
		name := fi.Name()
		if strings.HasPrefix(name, "_") {
			continue
		}
		if fi.IsDir() {
			if recursive {
				pkgPaths, _ = doListPkgs(pkgPaths, pkgPathBase+"/"+name, pat+"/"+name, exts, true)
			}
		} else if noSouceFile {
			ext := path.Ext(name)
			if _, ok := exts[ext]; ok {
				noSouceFile = false
			}
		}
	}
	if !noSouceFile {
		pkgPaths = append(pkgPaths, pkgPathBase)
	}
	return pkgPaths, nil
}

func List(conf *Config, pattern ...string) (pkgPaths []string, err error) {
	if conf == nil {
		conf = new(Config)
	}
	modRoot, _ := filepath.Abs(conf.ModRoot)
	for _, pat := range pattern {
		if pkgPaths, err = conf.listPkgs(pkgPaths, pat, modRoot); err != nil {
			return
		}
	}
	return
}

func Load(conf *Config, pattern ...string) (pkgs []*types.Package, err error) {
	p, pkgPaths, err := NewImporter(conf, pattern...)
	if err != nil {
		return
	}
	pkgs = make([]*types.Package, len(pkgPaths))
	for i, pkgPath := range pkgPaths {
		if pkgs[i], err = p.Import(pkgPath); err != nil {
			return
		}
	}
	return
}

// ----------------------------------------------------------------------------

type Importer struct {
	pkgs   map[string]pkgExport
	loaded map[string]*types.Package
	fset   *token.FileSet
}

func NewImporter(conf *Config, pattern ...string) (p *Importer, pkgPaths []string, err error) {
	if conf == nil {
		conf = new(Config)
	}
	pkgPaths, err = List(conf, pattern...)
	if err != nil {
		return
	}
	pkgs, err := loadDeps(conf.getTempDir(), pkgPaths...)
	if err != nil {
		return
	}
	fset := conf.Fset
	if fset == nil {
		fset = token.NewFileSet()
	}
	loaded := conf.Loaded
	if loaded == nil {
		loaded = make(map[string]*types.Package)
	}
	loaded["unsafe"] = types.Unsafe
	p = &Importer{pkgs: pkgs, loaded: loaded, fset: fset}
	return
}

func (p *Importer) Import(pkgPath string) (*types.Package, error) {
	if ret, ok := p.loaded[pkgPath]; ok && ret.Complete() {
		return ret, nil
	}
	if expfile, ok := p.pkgs[pkgPath]; ok {
		return p.loadPkgExport(expfile, pkgPath)
	}
	return nil, syscall.ENOENT
}

func (p *Importer) loadPkgExport(expfile string, pkgPath string) (*types.Package, error) {
	f, err := os.Open(expfile)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r, err := gcexportdata.NewReader(f)
	if err != nil {
		return nil, err
	}
	return gcexportdata.Read(r, p.fset, p.loaded, pkgPath)
}

// ----------------------------------------------------------------------------
