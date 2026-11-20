// Command releasecheck inspects Gent module ZIPs and Git source archives.
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

const modulePath = "github.com/rickchristie/gent"

func check(root, output, version string) error {
	if err := module.Check(modulePath, version); err != nil {
		return err
	}
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
	for _, path := range []string{"docs/go.mod", "docs/build.mjs", "examples/go.mod",
		"examples/history/main.go", "examples/chat/go.mod", "examples/chat/web/app.js"} {
		if !seen[path] {
			return fmt.Errorf("source was not supplied to the module packer: %s", path)
		}
	}
	var packed bytes.Buffer
	identity := module.Version{Path: modulePath, Version: version}
	if err := modzip.Create(&packed, identity, files); err != nil {
		return err
	}
	count, err := inspect(packed.Bytes(), modulePath+"@"+version+"/")
	if err != nil {
		return err
	}
	if err := os.WriteFile(output, packed.Bytes(), 0600); err != nil {
		return err
	}
	// Check the real Git archive too. Worktree attributes permit a packaging check before the
	// change is committed. Release execution also requires an unchanged, clean committed tree.
	command = exec.Command("git", "archive", "--format=zip", "--worktree-attributes",
		"--prefix=gent-"+version+"/", "HEAD")
	command.Dir = root
	source, err := command.Output()
	if err != nil {
		return err
	}
	sourceCount, err := inspect(source, "gent-"+version+"/")
	if err != nil {
		return err
	}
	sourcePath := strings.TrimSuffix(output, ".zip") + ".source.zip"
	if err := os.WriteFile(sourcePath, source, 0600); err != nil {
		return err
	}
	fmt.Printf("PASS %d supplied files; %d module entries; %d source entries; docs/examples omitted\n",
		len(files), count, sourceCount)
	return nil
}

func inspect(data []byte, prefix string) (int, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return 0, err
	}
	required := map[string]bool{"go.mod": false, "model.go": false, "LICENSE": false}
	for _, file := range archive.File {
		name, ok := strings.CutPrefix(file.Name, prefix)
		if !ok {
			return 0, fmt.Errorf("archive entry has the wrong prefix: %s", file.Name)
		}
		if strings.HasPrefix(name, "docs/") || strings.HasPrefix(name, "examples/") ||
			strings.Contains("/"+name, "/node_modules/") ||
			strings.HasPrefix(name, ".secrets/") || filepath.Base(name) == ".env" {
			return 0, fmt.Errorf("excluded source or secret in release: %s", name)
		}
		if _, exists := required[name]; exists {
			required[name] = true
		}
	}
	for _, name := range []string{"go.mod", "model.go", "LICENSE"} {
		if !required[name] {
			return 0, fmt.Errorf("required file missing from release: %s", name)
		}
	}
	return len(archive.File), nil
}

func main() {
	root := flag.String("root", "..", "Gent repository root")
	output := flag.String("out", "/tmp/gent-module.zip", "Inspected module ZIP")
	version := flag.String("version", "v0.0.0", "Module version in the ZIP")
	downloaded := flag.String("module-zip", "", "Inspect a downloaded module ZIP instead")
	source := flag.String("source-zip", "", "Inspect a downloaded GitHub source ZIP instead")
	flag.Parse()
	if *downloaded != "" || *source != "" {
		path := *downloaded
		prefix := modulePath + "@" + *version + "/"
		if *source != "" {
			path = *source
		}
		data, err := os.ReadFile(path)
		if err != nil {
			log.Fatal(err)
		}
		if *source != "" {
			archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				log.Fatal(err)
			}
			if len(archive.File) == 0 {
				log.Fatal("source archive has no files")
			}
			folder, _, _ := strings.Cut(archive.File[0].Name, "/")
			prefix = folder + "/"
		}
		count, err := inspect(data, prefix)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("PASS downloaded archive: %d entries; docs/examples excluded\n", count)
		return
	}
	if err := check(*root, *output, *version); err != nil {
		log.Fatal(err)
	}
}
