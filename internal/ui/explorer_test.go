package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/Harshul1484/out-of-windows/internal/analyzer"
)

// memTree builds an in-memory tree (paths do not exist on disk, so listings
// contain folders only).
func memTree() *analyzer.Node {
	root := &analyzer.Node{Name: `X:\`, Path: `X:\`}
	add := func(parent *analyzer.Node, name string, size int64) *analyzer.Node {
		n := &analyzer.Node{Name: name, Path: strings.TrimSuffix(parent.Path, `\`) + `\` + name, Parent: parent, Size: size, Files: 1, Own: size, OwnFiles: 1}
		parent.Children = append(parent.Children, n)
		return n
	}
	users := add(root, "Users", 0)
	add(users, "alice", 700)
	add(users, "bob", 300)
	add(root, "Games", 5000)
	add(root, "Tools", 100)
	link := add(root, "Link", 0)
	link.Reparse = true
	var sumUp func(n *analyzer.Node) (int64, int64)
	sumUp = func(n *analyzer.Node) (int64, int64) {
		s, f := n.Own, n.OwnFiles
		for _, c := range n.Children {
			cs, cf := sumUp(c)
			s, f = s+cs, f+cf
		}
		n.Size, n.Files = s, f
		return s, f
	}
	sumUp(root)
	return root
}

func explorerWith(recycle func([]string) []error) *explorer {
	root := memTree()
	return NewExplorerModel(ExplorerOptions{Title: "Analyze", Root: root, Recycle: recycle,
		Largest: []analyzer.File{{Path: `X:\Games\game.pak`, Size: 4000}}}).(*explorer)
}

func TestExplorerNavigation(t *testing.T) {
	m := explorerWith(nil)
	if e, _ := m.current(); e.Name != "Games" {
		t.Fatalf("first entry = %s (want largest first)", e.Name)
	}
	press(m, "down", "enter") // Users
	if ExplorerPath(m) != `X:\Users` {
		t.Fatalf("path = %s", ExplorerPath(m))
	}
	press(m, "backspace")
	if ExplorerPath(m) != `X:\` {
		t.Fatalf("after back: %s", ExplorerPath(m))
	}
	if e, _ := m.current(); e.Name != "Users" {
		t.Errorf("cursor not restored to Users: %s", e.Name)
	}
	// Links are never opened.
	press(m, "end", "enter")
	if ExplorerPath(m) != `X:\` || !strings.Contains(m.message, "not followed") {
		t.Errorf("opened a link: %s %q", ExplorerPath(m), m.message)
	}
}

func TestExplorerSortFilterSearch(t *testing.T) {
	m := explorerWith(nil)
	press(m, "s") // by name: folders first, alphabetical
	if e, _ := m.current(); e.Name != "Games" {
		t.Errorf("sort by name first = %s", e.Name)
	}
	press(m, "f", "t", "o", "enter")
	if len(m.view) != 1 || m.entries[m.view[0]].Name != "Tools" {
		t.Errorf("filter result = %d entries", len(m.view))
	}
	press(m, "esc")
	press(m, "/", "u", "s", "enter")
	if e, _ := m.current(); e.Name != "Users" {
		t.Errorf("search landed on %s", e.Name)
	}
	press(m, "L")
	if !m.large || len(m.view) != 1 {
		t.Errorf("largest view: %v %d", m.large, len(m.view))
	}
}

func TestExplorerRecycleUpdatesSizes(t *testing.T) {
	var got []string
	m := explorerWith(func(paths []string) []error {
		got = paths
		errs := make([]error, len(paths))
		for i, p := range paths {
			if strings.HasSuffix(p, "Tools") {
				errs[i] = errors.New("protected")
			}
		}
		return errs
	})
	press(m, "space", "end", "up", "space") // mark Games and Tools
	press(m, "d")
	if !m.confirm || !strings.Contains(m.View(), "Move 2 items") {
		t.Fatalf("no confirmation shown:\n%s", m.View())
	}
	press(m, "y")
	if len(got) != 2 {
		t.Fatalf("recycled %v", got)
	}
	if m.opts.Root.Size != 1100 { // 6100 - Games(5000)
		t.Errorf("root size after recycling = %d", m.opts.Root.Size)
	}
	if !strings.Contains(m.message, "1 item") || !strings.Contains(m.message, "Tools: protected") {
		t.Errorf("message = %q", m.message)
	}
	if len(m.opts.Largest) != 0 {
		t.Errorf("largest file list not updated: %+v", m.opts.Largest)
	}
}

func TestExplorerCancelDelete(t *testing.T) {
	called := false
	m := explorerWith(func(p []string) []error { called = true; return make([]error, len(p)) })
	press(m, "d", "n")
	if called || m.confirm || m.opts.Root.Size != 6100 {
		t.Errorf("cancel: called=%v confirm=%v size=%d", called, m.confirm, m.opts.Root.Size)
	}
}
