package sandbox

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"sort"
	"strings"
	"unicode/utf16"
)

// This file builds realistic file contents for installer fixtures: Windows
// executables with a version resource, ZIP/MSIX archives, ISO 9660 images and
// OLE compound files. Nothing here is executable code; the PE files contain a
// single "ret" instruction and are only ever read.

var le = binary.LittleEndian

// PEOptions describes a synthetic Windows executable.
type PEOptions struct {
	// Version holds StringFileInfo values (ProductName, CompanyName,
	// FileDescription, ProductVersion, Comments, ...). Empty: no resource.
	Version map[string]string
	// FileVersion is the fixed file and product version.
	FileVersion [4]uint16
	// Sections are extra (empty) section names, e.g. ".wixburn".
	Sections []string
	// Overlay is appended after the image, where installers keep their
	// payload.
	Overlay []byte
	// DLL marks the image as a library.
	DLL bool
}

// BuildPE returns a minimal, valid PE32+ (x64) image.
func BuildPE(o PEOptions) []byte {
	const fileAlign, sectAlign, headerSize = 0x200, 0x1000, 0x400
	type section struct {
		name string
		data []byte
		char uint32
	}
	secs := []section{{".text", []byte{0xC3}, 0x60000020}}
	if len(o.Version) > 0 {
		secs = append(secs, section{".rsrc", nil, 0x40000040})
	}
	for _, n := range o.Sections {
		secs = append(secs, section{n, make([]byte, 16), 0x40000040})
	}
	alignUp := func(n, a int) int { return (n + a - 1) / a * a }

	// Assign addresses, building the resource section once its RVA is known.
	rva, raw := sectAlign, headerSize
	type placed struct {
		section
		rva, raw, rawSize int
	}
	var ps []placed
	resRVA, resSize := 0, 0
	for _, s := range secs {
		if s.name == ".rsrc" {
			s.data = resourceSection(rva, versionInfo(o.Version, o.FileVersion))
			resRVA, resSize = rva, len(s.data)
		}
		rs := alignUp(max(len(s.data), 1), fileAlign)
		ps = append(ps, placed{s, rva, raw, rs})
		rva += alignUp(max(len(s.data), 1), sectAlign)
		raw += rs
	}
	img := make([]byte, raw)

	// DOS header and PE signature.
	copy(img, "MZ")
	le.PutUint32(img[0x3C:], 0x80)
	copy(img[0x80:], "PE\x00\x00")
	// COFF header.
	coff := img[0x84:]
	le.PutUint16(coff[0:], 0x8664) // AMD64
	le.PutUint16(coff[2:], uint16(len(ps)))
	le.PutUint16(coff[16:], 240) // size of the PE32+ optional header
	chars := uint16(0x0022)      // executable, large address aware
	if o.DLL {
		chars |= 0x2000
	}
	le.PutUint16(coff[18:], chars)
	// Optional header.
	opt := img[0x98:]
	le.PutUint16(opt[0:], 0x20B)
	opt[2] = 14
	le.PutUint32(opt[4:], fileAlign)  // size of code
	le.PutUint32(opt[16:], sectAlign) // entry point (.text)
	le.PutUint32(opt[20:], sectAlign) // base of code
	le.PutUint64(opt[24:], 0x140000000)
	le.PutUint32(opt[32:], sectAlign)
	le.PutUint32(opt[36:], fileAlign)
	le.PutUint16(opt[40:], 6) // OS version
	le.PutUint16(opt[48:], 6) // subsystem version
	le.PutUint32(opt[56:], uint32(rva))
	le.PutUint32(opt[60:], headerSize)
	le.PutUint16(opt[68:], 2)      // Windows GUI
	le.PutUint16(opt[70:], 0x8160) // ASLR, NX, high-entropy VA, TS aware
	le.PutUint64(opt[72:], 0x100000)
	le.PutUint64(opt[80:], 0x1000)
	le.PutUint64(opt[88:], 0x100000)
	le.PutUint64(opt[96:], 0x1000)
	le.PutUint32(opt[108:], 16)
	if resRVA != 0 {
		le.PutUint32(opt[112+2*8:], uint32(resRVA))
		le.PutUint32(opt[112+2*8+4:], uint32(resSize))
	}
	// Section table and data.
	tbl := img[0x98+240:]
	for i, p := range ps {
		h := tbl[i*40:]
		copy(h[0:8], p.name)
		le.PutUint32(h[8:], uint32(max(len(p.data), 1)))
		le.PutUint32(h[12:], uint32(p.rva))
		le.PutUint32(h[16:], uint32(p.rawSize))
		le.PutUint32(h[20:], uint32(p.raw))
		le.PutUint32(h[36:], p.char)
		copy(img[p.raw:], p.data)
	}
	return append(img, o.Overlay...)
}

// resourceSection lays out a resource tree with one RT_VERSION entry
// (type 16, id 1, language 0x0409) whose data is vi.
func resourceSection(sectionRVA int, vi []byte) []byte {
	b := make([]byte, 88)
	dir := func(off int, id uint32, target uint32) {
		le.PutUint16(b[off+14:], 1) // one id entry
		le.PutUint32(b[off+16:], id)
		le.PutUint32(b[off+20:], target)
	}
	dir(0, 16, 0x80000000|24)                   // type: RT_VERSION
	dir(24, 1, 0x80000000|48)                   // name: 1
	dir(48, 0x0409, 72)                         // language → data entry
	le.PutUint32(b[72:], uint32(sectionRVA+88)) // data RVA
	le.PutUint32(b[76:], uint32(len(vi)))
	return append(b, vi...)
}

// versionInfo encodes a VS_VERSIONINFO block.
func versionInfo(fields map[string]string, v [4]uint16) []byte {
	fixed := make([]byte, 52)
	ms, ls := uint32(v[0])<<16|uint32(v[1]), uint32(v[2])<<16|uint32(v[3])
	le.PutUint32(fixed[0:], 0xFEEF04BD)
	le.PutUint32(fixed[4:], 0x00010000)
	le.PutUint32(fixed[8:], ms)
	le.PutUint32(fixed[12:], ls)
	le.PutUint32(fixed[16:], ms)
	le.PutUint32(fixed[20:], ls)
	le.PutUint32(fixed[24:], 0x3F)
	le.PutUint32(fixed[32:], 0x40004) // VOS_NT_WINDOWS32
	le.PutUint32(fixed[36:], 1)       // VFT_APP

	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var strs [][]byte
	for _, k := range keys {
		val := utf16z(fields[k])
		strs = append(strs, vnode(k, 1, val, uint16(len(val)/2)))
	}
	table := vnode("040904B0", 1, nil, 0, strs...)
	sfi := vnode("StringFileInfo", 1, nil, 0, table)
	vfi := vnode("VarFileInfo", 1, nil, 0, vnode("Translation", 0, []byte{0x09, 0x04, 0xB0, 0x04}, 4))
	return vnode("VS_VERSION_INFO", 0, fixed, 52, sfi, vfi)
}

func vnode(key string, typ uint16, value []byte, valueLen uint16, children ...[]byte) []byte {
	b := []byte{0, 0}
	b = le.AppendUint16(b, valueLen)
	b = le.AppendUint16(b, typ)
	b = append(b, utf16z(key)...)
	b = pad4(b)
	b = append(b, value...)
	for _, c := range children {
		b = pad4(b)
		b = append(b, c...)
	}
	le.PutUint16(b[0:], uint16(len(b)))
	return b
}

func pad4(b []byte) []byte {
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

func utf16z(s string) []byte {
	var b []byte
	for _, u := range utf16.Encode([]rune(s)) {
		b = le.AppendUint16(b, u)
	}
	return append(b, 0, 0)
}

// Installer engine payload headers as the engines write them.
var (
	// NSISOverlay starts like an NSIS installer's first header.
	NSISOverlay = append([]byte{0, 0, 0, 0, 0xEF, 0xBE, 0xAD, 0xDE}, []byte("NullsoftInst\x00\x10\x00\x00")...)
	// InnoOverlay starts like Inno Setup's setup data block.
	InnoOverlay = []byte("Inno Setup Setup Data (6.2.2)\x00\x00\x00zlb\x1a")
)

// ZipEntry is one file in a fixture archive.
type ZipEntry struct {
	Name string
	Data []byte
}

// BuildZip returns a ZIP archive with the given entries.
func BuildZip(entries ...ZipEntry) []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		f, err := w.Create(e.Name)
		if err != nil {
			panic(err)
		}
		if _, err := f.Write(e.Data); err != nil {
			panic(err)
		}
	}
	if err := w.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// AppxManifest returns a minimal MSIX/APPX package manifest.
func AppxManifest(name, publisher, version, displayName string) []byte {
	return []byte(`<?xml version="1.0" encoding="utf-8"?>
<Package xmlns="http://schemas.microsoft.com/appx/manifest/foundation/windows10">
  <Identity Name="` + name + `" Publisher="` + publisher + `" Version="` + version + `" ProcessorArchitecture="x64" />
  <Properties>
    <DisplayName>` + displayName + `</DisplayName>
    <PublisherDisplayName>` + strings.TrimPrefix(publisher, "CN=") + `</PublisherDisplayName>
    <Logo>Assets\StoreLogo.png</Logo>
  </Properties>
</Package>`)
}

// BuildISO returns an ISO 9660 image whose root directory holds files (names
// in ISO form, e.g. "SETUP.EXE").
func BuildISO(volumeID string, files map[string][]byte) []byte {
	const sector = 2048
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	rootLBA := 18
	next := rootLBA + 1
	type ext struct{ lba, size int }
	exts := map[string]ext{}
	for _, n := range names {
		exts[n] = ext{next, len(files[n])}
		next += max(1, (len(files[n])+sector-1)/sector)
	}
	img := make([]byte, next*sector)
	both32 := func(b []byte, v int) {
		le.PutUint32(b, uint32(v))
		binary.BigEndian.PutUint32(b[4:], uint32(v))
	}
	record := func(name []byte, lba, size int, dir bool) []byte {
		r := make([]byte, 33+len(name))
		if len(r)%2 == 1 {
			r = append(r, 0)
		}
		r[0] = byte(len(r))
		both32(r[2:], lba)
		both32(r[10:], size)
		if dir {
			r[25] = 2
		}
		le.PutUint16(r[28:], 1)
		binary.BigEndian.PutUint16(r[30:], 1)
		r[32] = byte(len(name))
		copy(r[33:], name)
		return r
	}
	pvd := img[16*sector:]
	pvd[0] = 1
	copy(pvd[1:], "CD001")
	pvd[6] = 1
	copy(pvd[8:40], strings.Repeat(" ", 32))
	copy(pvd[40:72], (volumeID + strings.Repeat(" ", 32))[:32])
	both32(pvd[80:], next)
	le.PutUint16(pvd[128:], sector)
	binary.BigEndian.PutUint16(pvd[130:], sector)
	copy(pvd[156:], record([]byte{0}, rootLBA, sector, true))
	term := img[17*sector:]
	term[0] = 255
	copy(term[1:], "CD001")
	term[6] = 1

	var dir []byte
	dir = append(dir, record([]byte{0}, rootLBA, sector, true)...)
	dir = append(dir, record([]byte{1}, rootLBA, sector, true)...)
	for _, n := range names {
		dir = append(dir, record([]byte(n+";1"), exts[n].lba, exts[n].size, false)...)
		copy(img[exts[n].lba*sector:], files[n])
	}
	copy(img[rootLBA*sector:], dir)
	return img
}

// CLSIDs of OLE compound files used by fixtures.
var (
	CLSIDWordDocument = [16]byte{0x06, 0x09, 0x02, 0x00, 0, 0, 0, 0, 0xC0, 0, 0, 0, 0, 0, 0, 0x46}
)

// BuildCFB returns a minimal OLE compound file whose root storage has the
// given class ID (the way Office documents and MSI databases are tagged).
func BuildCFB(clsid [16]byte) []byte {
	b := make([]byte, 512*4)
	copy(b, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1})
	le.PutUint16(b[0x18:], 0x3E)
	le.PutUint16(b[0x1A:], 3)
	le.PutUint16(b[0x1C:], 0xFFFE)
	le.PutUint16(b[0x1E:], 9) // 512-byte sectors
	le.PutUint16(b[0x20:], 6)
	le.PutUint32(b[0x2C:], 1)          // one FAT sector
	le.PutUint32(b[0x30:], 1)          // directory starts at sector 1
	le.PutUint32(b[0x38:], 0x1000)     // mini stream cutoff
	le.PutUint32(b[0x3C:], 0xFFFFFFFE) // no mini FAT
	le.PutUint32(b[0x44:], 0xFFFFFFFE) // no DIFAT sectors
	le.PutUint32(b[0x4C:], 0)          // FAT in sector 0
	for i := 1; i < 109; i++ {
		le.PutUint32(b[0x4C+4*i:], 0xFFFFFFFF)
	}
	fat := b[512:]
	le.PutUint32(fat[0:], 0xFFFFFFFD) // FAT sector
	le.PutUint32(fat[4:], 0xFFFFFFFE) // directory: end of chain
	for i := 2; i < 128; i++ {
		le.PutUint32(fat[4*i:], 0xFFFFFFFF)
	}
	root := b[1024:]
	name := utf16z("Root Entry")
	copy(root, name)
	le.PutUint16(root[64:], uint16(len(name)))
	root[66] = 5 // root storage
	le.PutUint32(root[68:], 0xFFFFFFFF)
	le.PutUint32(root[72:], 0xFFFFFFFF)
	le.PutUint32(root[76:], 0xFFFFFFFF)
	copy(root[80:96], clsid[:])
	le.PutUint32(root[116:], 0xFFFFFFFE)
	return b[:512*3]
}
