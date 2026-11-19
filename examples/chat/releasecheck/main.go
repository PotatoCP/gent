// Command releasecheck creates and inspects the actual Gent module ZIP from current source.
// It does not stage files, create commits, or publish a version.
package main

import (
	"archive/zip"
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/mod/module"
	modzip "golang.org/x/mod/zip"
)

type sourceFile struct {
	root string
	path string
}

func (file sourceFile) Path() string { return file.path }
func (file sourceFile) Lstat() (os.FileInfo, error) {
	return os.Lstat(filepath.Join(file.root, file.path))
}
func (file sourceFile) Open() (io.ReadCloser, error) {
	return os.Open(filepath.Join(file.root, file.path))
}

func check(root, output string) error {
	command := exec.Command("git", "ls-files", "-c", "-o", "--exclude-standard", "-z")
	command.Dir = root
	listed, err := command.Output()
	if err != nil {
		return err
	}
	var files []modzip.File
	seen := map[string]bool{}
	for _, name := range bytes.Split(listed, []byte{0}) {
		path := string(name)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		file := sourceFile{root, path}
		if _, err := file.Lstat(); os.IsNotExist(err) {
			continue // A tracked file can be deleted in the current worktree.
		} else if err != nil {
			return err
		}
		files = append(files, file)
	}
	if !seen["examples/chat/go.mod"] || !seen["examples/chat/web/app.js"] {
		return fmt.Errorf("example sources were not supplied to the module packer")
	}
	var packed bytes.Buffer
	version := module.Version{Path: "github.com/rickchristie/gent", Version: "v0.0.0"}
	if err := modzip.Create(&packed, version, files); err != nil {
		return err
	}
	archive, err := zip.NewReader(bytes.NewReader(packed.Bytes()), int64(packed.Len()))
	if err != nil {
		return err
	}
	prefix := version.Path + "@" + version.Version + "/"
	rootModule := false
	for _, file := range archive.File {
		name := strings.TrimPrefix(file.Name, prefix)
		if strings.HasPrefix(name, "examples/chat/") || strings.Contains(name, "/node_modules/") ||
			filepath.Base(name) == ".env" {
			return fmt.Errorf("excluded application or secret in module payload: %s", name)
		}
		rootModule = rootModule || name == "go.mod"
	}
	if !rootModule {
		return fmt.Errorf("root go.mod missing from module payload")
	}
	if err := os.WriteFile(output, packed.Bytes(), 0600); err != nil {
		return err
	}
	fmt.Printf("PASS %d source files supplied; %d ZIP entries; chat module and assets excluded\n",
		len(files), len(archive.File))
	return nil
}

func main() {
	root := flag.String("root", "../..", "Gent repository root")
	output := flag.String("out", "/tmp/gent-module.zip", "Inspected module ZIP")
	flag.Parse()
	if err := check(*root, *output); err != nil {
		log.Fatal(err)
	}
}
