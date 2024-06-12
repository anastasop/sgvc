package main

import (
	"bufio"
	"crypto/sha1"
	"errors"
	"flag"
	"fmt"
	"hash/crc32"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	// maxVersionLength is used with fmt %*d to print version numbers
	maxVersionLength = 4
)

// commit represents a new version of a file
type commit struct {
	path    string    // the absolute file path
	when    time.Time // version time
	version int       // version id
	basedOn int       // parent version id (optional)
	pathSig string    // path signature to identify in file store
	dataCrc uint32    // contents crc for verification
	changes string    // human readable summary of contents

	descs []*commit // used for the tree output, not serialized
}

// serialize the commit to a string. Inverse of deserializeCommit
func (cmt *commit) serialize() string {
	return fmt.Sprintf("%s\t%s\t%0*d\t%0*d\t%s\t%d\t%s",
		cmt.path, cmt.when.Format(time.RFC3339), maxVersionLength, cmt.version,
		maxVersionLength, cmt.basedOn, cmt.pathSig, cmt.dataCrc, cmt.changes)
}

// deserializeCommit from a string. Inverse of serialize
func deserializeCommit(s string) (*commit, error) {
	parts := strings.Split(s, "\t")
	if len(parts) != 7 {
		return nil, errors.New("malformed line")
	}

	cwhen, err := time.Parse(time.RFC3339, parts[1])
	if err != nil {
		return nil, errors.New("malformed timestamp")
	}
	cversion, err := strconv.Atoi(parts[2])
	if err != nil {
		return nil, errors.New("malformed version")
	}
	cbasedOn, err := strconv.Atoi(parts[3])
	if err != nil {
		return nil, errors.New("malformed parent")
	}
	dataCrc, err := strconv.ParseUint(parts[5], 10, 32)
	if err != nil {
		return nil, errors.New("malformed data crc")
	}

	return &commit{
		path:    parts[0],
		when:    cwhen,
		version: cversion,
		basedOn: cbasedOn,
		pathSig: parts[4],
		dataCrc: uint32(dataCrc),
		changes: parts[6],
	}, nil
}

// index represents a file store and an index file for versions
type index struct {
	workDir     string    // directory with files
	commitsFile string    // the index with the serialized commits, a file in workDir
	commits     []*commit // the commits of the index deserialized from commitsFile
}

// getIndex prepares the work directory and initializes the index
func getIndex() (*index, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	workDir := filepath.Join(cacheDir, "sgvc")
	if err := os.MkdirAll(workDir, 0700); err != nil {
		return nil, err
	}
	commitsFile := filepath.Join(workDir, "index")
	if _, err := os.Stat(commitsFile); os.IsNotExist(err) {
		f, err := os.Create(commitsFile)
		if err != nil {
			return nil, err
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
	}
	idx := &index{workDir: workDir, commitsFile: commitsFile}
	if err := idx.loadCommits(); err != nil {
		return nil, err
	}
	return idx, nil
}

// loadCommits deserializes the index commits.
func (idx *index) loadCommits() error {
	fin, err := os.Open(idx.commitsFile)
	if err != nil {
		return err
	}
	defer fin.Close()

	var commits []*commit
	nlines := 0
	scanner := bufio.NewScanner(fin)
	for scanner.Scan() {
		nlines++
		line := scanner.Text()
		cmt, err := deserializeCommit(line)
		if err != nil {
			log.Fatalf("can't load commit:%d: %v", nlines, err)
		}
		commits = append(commits, cmt)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	// sort ascending by path and descending by version
	slices.SortFunc(commits, func(a, b *commit) int {
		if c := strings.Compare(a.path, b.path); c != 0 {
			return c
		}
		return b.version - a.version
	})
	idx.commits = commits
	return nil
}

// currVersion returns the latest version of a file
func (idx *index) currVersion(path string) int {
	v := 0
	for _, cmt := range idx.commits {
		if cmt.path == path && cmt.version > v {
			v = cmt.version
		}
	}
	return v
}

// filter returns the commits for this file.
// Return all commits if path is the empty string.
func (idx *index) filter(path string) []*commit {
	if path == "" {
		return idx.commits
	}

	var commits []*commit
	for _, cmt := range idx.commits {
		if cmt.path == path {
			commits = append(commits, cmt)
		}
	}
	return commits
}

// filePath returns the file path with the contents of the commit
func (idx *index) filePath(cmt *commit) string {
	fname := fmt.Sprintf("%s-%0*d", cmt.pathSig, maxVersionLength, cmt.version)
	return filepath.Join(idx.workDir, fname)
}

// extract returns the contents of the version for the file
func (idx *index) extract(path string, version int) ([]byte, error) {
	var cmt *commit
	for _, c := range idx.commits {
		if c.path == path && c.version == version {
			cmt = c
			break
		}
	}
	if cmt == nil {
		return nil, fmt.Errorf("cannot find version %d for %s", version, path)
	}

	data, err := os.ReadFile(idx.filePath(cmt))
	if err != nil {
		return nil, err
	}
	if dataCrc := crc32.ChecksumIEEE(data); dataCrc != cmt.dataCrc {
		return nil, fmt.Errorf("corrupted file, wrong crc: expected %d got %d", cmt.dataCrc, dataCrc)
	}

	return data, nil
}

// commit writes a new commit to the index
func (idx *index) commit(path string, basedOn int, changes string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	currVersion := idx.currVersion(path)
	if basedOn != 0 && basedOn > currVersion {
		return fmt.Errorf("invalid base version %d", basedOn)
	}
	thisVersion := currVersion + 1

	pathSig := fmt.Sprintf("%x", sha1.New().Sum([]byte(path)))
	dataCrc := crc32.ChecksumIEEE(data)

	cmt := commit{
		path:    path,
		when:    time.Now(),
		version: thisVersion,
		basedOn: basedOn,
		pathSig: pathSig,
		dataCrc: dataCrc,
		changes: strconv.Quote(changes),
	}

	// first write the file contents
	fpath := idx.filePath(&cmt)
	if err := os.WriteFile(fpath, data, 0600); err != nil {
		return fmt.Errorf("failed to commit contents: %w", err)
	}
	// then write the index entry
	fout, err := os.OpenFile(idx.commitsFile, os.O_WRONLY|os.O_APPEND|os.O_SYNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to open index: %w", err)
	}
	defer fout.Close()

	_, err = fmt.Fprintln(fout, cmt.serialize())
	if err != nil {
		return fmt.Errorf("failed to commit index: %w", err)
	}
	return nil
}

// treeOfCommits organizes the index commits as a tree using the base field.
// It returns a dummy node where every child represents the tree of
// changes for a file.
func (idx *index) treeOfCommits(path string) *commit {
	commits := idx.filter(path)

	type sig struct {
		path    string
		version int
	}
	m := make(map[sig]*commit)
	for _, cmt := range commits {
		m[sig{cmt.path, cmt.version}] = cmt
	}

	var dummy commit
	for _, cmt := range commits {
		if cmt.basedOn > 0 {
			c := m[sig{cmt.path, cmt.basedOn}]
			c.descs = append(c.descs, cmt)
		} else {
			dummy.descs = append(dummy.descs, cmt)
		}
	}
	return &dummy
}

var tabs = strings.Repeat("\t", 128)

// treePrint descends and prints the tree rooted at cmt.
func treePrint(cmt *commit, indend int) {
	fmt.Printf("%s%s\t%s\t%0*d\t%0*d\t%s\n", tabs[0:indend],
		cmt.path, cmt.when.Format(time.RFC3339),
		maxVersionLength, cmt.version,
		maxVersionLength, cmt.basedOn, cmt.changes)
	for _, dcmt := range cmt.descs {
		treePrint(dcmt, indend+1)
	}
}

// diff writes the arguments to temp files and execs diff(1)
func diff(from, to []byte, labelFrom, labelTo string) error {
	fromFile, err := os.CreateTemp("", "sgvc")
	if err != nil {
		return err
	}
	if err := fromFile.Close(); err != nil {
		return err
	}
	if err := os.WriteFile(fromFile.Name(), from, 0600); err != nil {
		return err
	}
	defer os.Remove(fromFile.Name())

	toFile, err := os.CreateTemp("", "ipdp")
	if err != nil {
		return err
	}
	if err := toFile.Close(); err != nil {
		return err
	}
	if err := os.WriteFile(toFile.Name(), to, 0600); err != nil {
		return err
	}
	defer os.Remove(toFile.Name())

	// run diff but ignore exit status
	cmd := exec.Command("diff", "-u", "--label", labelFrom, "--label", labelTo,
		fromFile.Name(), toFile.Name())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run()
	return nil
}

var (
	printCommits  = flag.Bool("commits", false, "print commits")
	printTree     = flag.Bool("tree", false, "print commits tree")
	printList     = flag.Bool("list", false, "print tracked files")
	catVersion    = flag.Int("cat", 0, "print version")
	commitMessage = flag.String("add", "", "small description of commit")
	baseVersion   = flag.Int("base", 0, "base version of commit")
	diffVersions  = flag.Bool("diff", false, "diff versions")
	diffFrom      = flag.Int("from", 0, "diff from version")
	diffTo        = flag.Int("to", 0, "diff to version")
)

func usage() {
	fmt.Fprintln(os.Stderr, `usage: sgvc [-commits|-tree|-list|-cat|-add|-diff] <file>

sgvc provides version control for single files. You can commit, read, log, diff
and maintain all the versions of a file. The are no repos. Every file is uniquely identified
by the absolute path and everything is stored in a single work directory currently $HOME/.cache/sgvc.

Examples:
$ sgvc -add 'deploy production' deploy.sh
$ sgvc -commits
deploy.sh 20240501T00:00:00Z 0001 0000 "deploy production"
$ sgvc -add 'deploy production with redis' -base 1 deploy.sh

Flags:`)
	flag.PrintDefaults()
	os.Exit(2)
}

func main() {
	log.SetPrefix("")
	log.SetFlags(0)
	flag.Usage = usage
	flag.Parse()

	idx, err := getIndex()
	if err != nil {
		log.Fatal(err)
	}

	var cpath string
	requiresFile := *commitMessage != "" || *catVersion > 0 || *diffVersions
	optionalFile := *printList || *printCommits || *printTree
	if !requiresFile && !optionalFile {
		usage()
	}
	if requiresFile && flag.NArg() != 1 || optionalFile && flag.NArg() > 1 {
		usage()
	}
	if flag.NArg() == 1 {
		if cpath, err = filepath.Abs(flag.Arg(0)); err != nil {
			log.Fatalf("resolution failed: %v", err)
		}
		if _, err := os.Stat(cpath); err != nil {
			log.Fatalf("read failed: %v", err)
		}
	}

	if *printList {
		m := make(map[string]string)
		for _, cmt := range idx.commits {
			m[cmt.path] = cmt.pathSig
		}
		s := make([]string, 0, len(m))
		for path := range m {
			s = append(s, path)
		}
		slices.Sort(s)
		for _, path := range s {
			fmt.Println(path, "\t", m[path])
		}
		os.Exit(0)
	}

	if *printCommits {
		for _, cmt := range idx.filter(cpath) {
			fmt.Printf("%s\t%s\t%0*d\t%0*d\t%s\n",
				cmt.path, cmt.when.Format(time.RFC3339),
				maxVersionLength, cmt.version,
				maxVersionLength, cmt.basedOn, cmt.changes)
		}
		os.Exit(0)
	}

	if *printTree {
		dummy := idx.treeOfCommits(cpath)
		for _, cmt := range dummy.descs {
			treePrint(cmt, 0)
		}
		os.Exit(0)
	}

	if *commitMessage != "" {
		if err := idx.commit(cpath, *baseVersion, *commitMessage); err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	}

	if *catVersion > 0 {
		data, err := idx.extract(cpath, *catVersion)
		if err != nil {
			log.Fatal(err)
		}
		os.Stdout.Write(data)
		os.Exit(0)
	}

	if *diffVersions {
		load := func(path string, version int) (label string, data []byte, err error) {
			if version > 0 {
				data, err = idx.extract(cpath, version)
				label = fmt.Sprintf("%s @%0*d", cpath, maxVersionLength, version)
			} else {
				data, err = os.ReadFile(cpath)
				label = cpath
			}
			return
		}

		labelFrom, from, err := load(cpath, *diffFrom)
		if err != nil {
			log.Fatalf("failed to resolve diff from: %v", err)
		}
		labelTo, to, err := load(cpath, *diffTo)
		if err != nil {
			log.Fatalf("failed to resolve diff to: %v", err)
		}
		if err := diff(from, to, labelFrom, labelTo); err != nil {
			log.Fatalf("failed to diff: %v", err)
		}
		os.Exit(0)
	}
}
