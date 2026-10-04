// Package analyzer measures disk usage: a parallel, cancellable scan that
// builds a tree of folders with their total sizes, and keeps the largest
// files. Scanning is read-only; links and junctions are never followed.
//
// Sizes are logical file sizes. Hard-linked files (common in Windows\WinSxS)
// are counted once per link, so folders full of hard links appear larger than
// the space they use.
package analyzer

import (
	"container/heap"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/filesystem"
)

// Node is one directory in the scanned tree.
type Node struct {
	Name     string
	Path     string
	Parent   *Node
	Children []*Node // subdirectories
	Size     int64   // total bytes of all files below
	Files    int64   // total number of files below
	Own      int64   // bytes of files directly in this folder
	OwnFiles int64
	Newest   time.Time
	Reparse  bool  // a link or junction: not followed
	Err      error // could not be read
	mu       sync.Mutex
}

// Dirs counts subdirectories below n (recursively).
func (n *Node) Dirs() int64 {
	var c int64
	for _, ch := range n.Children {
		c += 1 + ch.Dirs()
	}
	return c
}

// Find returns the node at path below n, or nil.
func (n *Node) Find(path string) *Node {
	rel, err := filepath.Rel(n.Path, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil
	}
	cur := n
	if rel == "." {
		return cur
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		var next *Node
		for _, c := range cur.Children {
			if strings.EqualFold(c.Name, part) {
				next = c
				break
			}
		}
		if next == nil {
			return nil
		}
		cur = next
	}
	return cur
}

// SortedChildren returns children ordered by size, largest first.
func (n *Node) SortedChildren() []*Node {
	out := append([]*Node(nil), n.Children...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Size > out[j].Size })
	return out
}

// Remove detaches a removed path's size from the tree: a child directory is
// unlinked, a file's bytes are subtracted. Sizes of all ancestors are updated.
func (n *Node) Remove(path string, isDir bool, size int64, files int64) {
	parent := n.Find(filepath.Dir(path))
	if parent == nil {
		return
	}
	if isDir {
		for i, c := range parent.Children {
			if strings.EqualFold(c.Name, filepath.Base(path)) {
				size, files = c.Size, c.Files
				parent.Children = append(parent.Children[:i], parent.Children[i+1:]...)
				break
			}
		}
	} else {
		parent.Own -= size
		parent.OwnFiles -= files
	}
	for p := parent; p != nil; p = p.Parent {
		p.Size -= size
		p.Files -= files
	}
}

// File is a file found by the scan.
type File struct {
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

// Progress is updated during a scan; UIs poll it.
type Progress struct {
	Files atomic.Int64
	Dirs  atomic.Int64
	Bytes atomic.Int64
}

// Options control a scan.
type Options struct {
	// TopFiles is how many of the largest files to keep (0 = 200).
	TopFiles int
	// MinFileSize only keeps largest files at least this big.
	MinFileSize int64
	Workers     int
}

// Result is a completed (or cancelled) scan.
type Result struct {
	Root      *Node
	Largest   []File // largest first
	Errors    int64  // folders that could not be read
	Links     int64  // links and junctions not followed
	Cancelled bool
	Duration  time.Duration
}

// Scan measures root. It returns a partial result marked Cancelled when ctx
// is cancelled.
func Scan(ctx context.Context, root string, opts Options, prog *Progress) (*Result, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	e, err := filesystem.Lstat(abs)
	if err != nil {
		return nil, err
	}
	if e.Reparse {
		return nil, filesystem.ErrReparsePoint
	}
	if !e.IsDir() {
		return nil, &os.PathError{Op: "analyze", Path: abs, Err: os.ErrInvalid}
	}
	if opts.TopFiles == 0 {
		opts.TopFiles = 200
	}
	if opts.Workers == 0 {
		// Directory reads are I/O-bound: on slow (cold, network-attached or
		// spinning) disks more requests in flight help, and on fast disks 16
		// concurrent reads measured about twice as fast as 8 (bench_test.go).
		opts.Workers = max(16, runtime.NumCPU()*2)
	}
	if prog == nil {
		prog = &Progress{}
	}
	s := &scanner{ctx: ctx, opts: opts, prog: prog, sem: make(chan struct{}, opts.Workers)}
	start := time.Now()
	rootNode := &Node{Name: filepath.Base(abs), Path: abs}
	if vol := filepath.VolumeName(abs); vol != "" && strings.TrimSuffix(abs, `\`) == vol {
		rootNode.Name = vol + `\`
	}
	s.scanDir(rootNode)
	s.wg.Wait()
	sum(rootNode)

	res := &Result{Root: rootNode, Errors: s.errors.Load(), Links: s.links.Load(),
		Cancelled: ctx.Err() != nil, Duration: time.Since(start)}
	res.Largest = make([]File, len(s.top))
	for i := len(s.top) - 1; i >= 0; i-- {
		res.Largest[i] = heap.Pop(&s.top).(File)
	}
	return res, nil
}

type scanner struct {
	ctx    context.Context
	opts   Options
	prog   *Progress
	sem    chan struct{}
	wg     sync.WaitGroup
	errors atomic.Int64
	links  atomic.Int64
	topMu  sync.Mutex
	top    fileHeap
}

// scanDir reads one directory and schedules its subdirectories. The
// semaphore only limits concurrent directory reads, so waiting goroutines
// never hold a slot.
func (s *scanner) scanDir(n *Node) {
	if s.ctx.Err() != nil {
		return
	}
	s.sem <- struct{}{}
	entries, err := os.ReadDir(n.Path)
	<-s.sem
	if err != nil && len(entries) == 0 {
		n.Err = err
		s.errors.Add(1)
		return
	}
	s.prog.Dirs.Add(1)
	var subdirs []*Node
	for _, de := range entries {
		info, err := de.Info()
		if err != nil {
			continue
		}
		p := filepath.Join(n.Path, de.Name())
		e := filesystemEntry(p, info)
		if e.Reparse {
			s.links.Add(1)
			child := &Node{Name: de.Name(), Path: p, Parent: n, Reparse: true}
			subdirs = append(subdirs, child)
			continue
		}
		if e.IsDir() {
			subdirs = append(subdirs, &Node{Name: de.Name(), Path: p, Parent: n})
			continue
		}
		size := e.Size()
		n.Own += size
		n.OwnFiles++
		if t := e.Fingerprint.ModTime(); t.After(n.Newest) {
			n.Newest = t
		}
		s.prog.Files.Add(1)
		s.prog.Bytes.Add(size)
		if size >= s.opts.MinFileSize {
			s.offer(File{Path: p, Size: size, Modified: e.Fingerprint.ModTime()})
		}
	}
	n.Children = subdirs
	for _, c := range subdirs {
		if c.Reparse {
			continue
		}
		s.wg.Add(1)
		go func(c *Node) {
			defer s.wg.Done()
			s.scanDir(c)
		}(c)
	}
}

func (s *scanner) offer(f File) {
	s.topMu.Lock()
	defer s.topMu.Unlock()
	if len(s.top) < s.opts.TopFiles {
		heap.Push(&s.top, f)
		return
	}
	if f.Size > s.top[0].Size {
		s.top[0] = f
		heap.Fix(&s.top, 0)
	}
}

// sum computes totals bottom-up once all directories are read.
func sum(n *Node) {
	n.Size, n.Files = n.Own, n.OwnFiles
	for _, c := range n.Children {
		sum(c)
		n.Size += c.Size
		n.Files += c.Files
		if c.Newest.After(n.Newest) {
			n.Newest = c.Newest
		}
	}
}

// fileHeap is a min-heap by size, keeping the largest files.
type fileHeap []File

func (h fileHeap) Len() int           { return len(h) }
func (h fileHeap) Less(i, j int) bool { return h[i].Size < h[j].Size }
func (h fileHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *fileHeap) Push(x any)        { *h = append(*h, x.(File)) }
func (h *fileHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

// Entry is a file or folder directly inside a folder, for listing.
type Entry struct {
	Name     string
	Path     string
	IsDir    bool
	Size     int64
	Files    int64
	Modified time.Time
	Reparse  bool
	Node     *Node // for directories
}

// List returns the folders (from the tree) and files (read now) directly in
// n. Reading files on demand keeps memory small on huge drives.
func List(n *Node) []Entry {
	var out []Entry
	for _, c := range n.Children {
		out = append(out, Entry{Name: c.Name, Path: c.Path, IsDir: true, Size: c.Size, Files: c.Files,
			Modified: c.Newest, Reparse: c.Reparse, Node: c})
	}
	entries, _ := os.ReadDir(n.Path)
	for _, de := range entries {
		info, err := de.Info()
		if err != nil {
			continue
		}
		e := filesystemEntry(filepath.Join(n.Path, de.Name()), info)
		if e.IsDir() || e.Reparse {
			continue
		}
		out = append(out, Entry{Name: de.Name(), Path: e.Path, Size: e.Size(), Files: 1, Modified: e.Fingerprint.ModTime()})
	}
	return out
}
