package config

import "testing"

func TestPurgePaths(t *testing.T) {
	c := Default()
	if ok, err := c.AddPurgePath(`D:\dev`); !ok || err != nil {
		t.Fatalf("add: %v %v", ok, err)
	}
	if ok, _ := c.AddPurgePath(`d:/DEV/`); ok {
		t.Error("the same folder was added twice")
	}
	if _, err := c.AddPurgePath(`relative\dev`); err == nil {
		t.Error("relative path accepted")
	}
	if ok, _ := c.AddPurgePath(`C:\Users\me\source\repos`); !ok || len(c.Purge.Paths) != 2 || c.Purge.Paths[0] != `C:\Users\me\source\repos` {
		t.Errorf("paths = %v", c.Purge.Paths)
	}
	if !c.RemovePurgePath(`D:\Dev`) || c.RemovePurgePath(`D:\dev`) || len(c.Purge.Paths) != 1 {
		t.Errorf("remove: %v", c.Purge.Paths)
	}
}
