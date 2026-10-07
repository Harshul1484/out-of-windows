package installer

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
)

// Type is an installer package format.
type Type string

const (
	TypeMSI    Type = "msi"        // Windows Installer package
	TypeMSP    Type = "msp"        // Windows Installer patch
	TypeMSIX   Type = "msix"       // MSIX or APPX package
	TypeBundle Type = "msixbundle" // MSIX or APPX bundle
	TypeEXE    Type = "exe"        // setup program
	TypeZIP    Type = "zip"        // archive with an installer at its root
	TypeISO    Type = "iso"        // disc image
)

// Details is what the file's content says about it.
type Details struct {
	Type        Type
	Format      string // human-readable format
	Engine      string // setup engine of an EXE (Inno Setup, NSIS, ...)
	Product     string
	Version     string
	Publisher   string
	ProductCode string   // MSI ProductCode
	Targets     []string // product codes an MSP patch applies to
	Identity    string   // MSIX/APPX package identity name
	Evidence    []string
	// Review marks a weak identification that is listed but never
	// preselected (a disc image without setup files).
	Review string
	// Description is the EXE's FileDescription (used for name matching).
	Description string
}

var le = binary.LittleEndian

// Content limits: how much of a file is read to identify it.
const (
	markerScanBytes = 8 << 20  // engine markers are near the start of the payload
	zipEntryBytes   = 16 << 20 // decompressed bytes read from one archive entry
	maxZipEntries   = 32       // root-level archive entries inspected
	manifestBytes   = 1 << 20
)

// Class IDs of OLE compound files (root storage).
var (
	clsidMSI = [16]byte{0x84, 0x10, 0x0C, 0x00, 0, 0, 0, 0, 0xC0, 0, 0, 0, 0, 0, 0, 0x46}
	clsidMSP = [16]byte{0x86, 0x10, 0x0C, 0x00, 0, 0, 0, 0, 0xC0, 0, 0, 0, 0, 0, 0, 0x46}
	cfbMagic = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}
)

// Inspect identifies an installer package by its content. It returns nil
// (and no error) for anything that is not one, whatever its name.
func Inspect(p string) (*Details, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	head := make([]byte, 4096)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	switch {
	case bytes.HasPrefix(head, cfbMagic):
		return inspectCFB(f, p)
	case bytes.HasPrefix(head, []byte("PK\x03\x04")):
		return inspectZip(p)
	case bytes.HasPrefix(head, []byte("MZ")):
		return inspectEXE(f, p, st.Size())
	}
	if st.Size() >= 0x8800 {
		return inspectISO(f)
	}
	return nil, nil
}

// cfbClass returns the class ID of a compound file's root storage, read
// from an io.ReaderAt (a file, or the decompressed start of an archive entry).
func cfbClass(r io.ReaderAt) ([16]byte, bool) {
	var clsid [16]byte
	hdr := make([]byte, 512)
	if _, err := r.ReadAt(hdr, 0); err != nil || !bytes.HasPrefix(hdr, cfbMagic) {
		return clsid, false
	}
	shift := le.Uint16(hdr[0x1E:])
	if shift != 9 && shift != 12 {
		return clsid, false
	}
	dirStart := int64(le.Uint32(hdr[0x30:]))
	if dirStart >= 0xFFFFFFFA {
		return clsid, false
	}
	entry := make([]byte, 128)
	if _, err := r.ReadAt(entry, (dirStart+1)<<shift); err != nil {
		return clsid, false
	}
	if entry[66] != 5 || !bytes.HasPrefix(entry, []byte("R\x00o\x00o\x00t\x00 \x00E\x00n\x00t\x00r\x00y\x00")) {
		return clsid, false
	}
	copy(clsid[:], entry[80:96])
	return clsid, true
}

func inspectCFB(f *os.File, p string) (*Details, error) {
	clsid, ok := cfbClass(f)
	if !ok {
		return nil, nil
	}
	switch clsid {
	case clsidMSI:
		d := &Details{Type: TypeMSI, Format: "Windows Installer package",
			Evidence: []string{"Windows Installer database (compound file with the MSI class ID)"}}
		if props, err := msiProperties(p); err == nil {
			d.Product, d.Version = props["ProductName"], props["ProductVersion"]
			d.Publisher, d.ProductCode = props["Manufacturer"], props["ProductCode"]
			if d.Product != "" {
				d.Evidence = append(d.Evidence, "product information read with Windows Installer")
			}
		}
		return d, nil
	case clsidMSP:
		d := &Details{Type: TypeMSP, Format: "Windows Installer patch",
			Evidence: []string{"Windows Installer patch (compound file with the MSP class ID)"}}
		if title, subject, targets, err := msiSummary(p); err == nil {
			d.Product = firstNonEmpty(title, subject)
			d.Targets = targets
		}
		return d, nil
	}
	return nil, nil // Office documents, transforms and other compound files
}

// appxManifest holds the fields of an MSIX/APPX (bundle) manifest we use.
type appxManifest struct {
	Identity struct {
		Name      string `xml:"Name,attr"`
		Publisher string `xml:"Publisher,attr"`
		Version   string `xml:"Version,attr"`
	} `xml:"Identity"`
	Properties struct {
		DisplayName          string `xml:"DisplayName"`
		PublisherDisplayName string `xml:"PublisherDisplayName"`
	} `xml:"Properties"`
}

func inspectZip(p string) (*Details, error) {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return nil, nil // not a readable archive
	}
	defer zr.Close()
	for _, f := range zr.File {
		switch strings.ToLower(f.Name) {
		case "appxmanifest.xml", "appxmetadata/appxbundlemanifest.xml":
			bundle := strings.Contains(strings.ToLower(f.Name), "bundle")
			m, err := readManifest(f)
			if err != nil || m.Identity.Name == "" {
				continue
			}
			d := &Details{Type: TypeMSIX, Format: "MSIX/APPX app package", Identity: m.Identity.Name,
				Version: m.Identity.Version, Evidence: []string{"package with " + path.Base(f.Name)}}
			if bundle {
				d.Type, d.Format = TypeBundle, "MSIX/APPX app bundle"
			}
			d.Product = m.Properties.DisplayName
			if d.Product == "" || strings.HasPrefix(d.Product, "ms-resource:") {
				d.Product = m.Identity.Name
			}
			d.Publisher = m.Properties.PublisherDisplayName
			if d.Publisher == "" || strings.HasPrefix(d.Publisher, "ms-resource:") {
				d.Publisher = publisherCN(m.Identity.Publisher)
			}
			return d, nil
		}
	}
	inspected := 0
	for _, f := range zr.File {
		if strings.ContainsAny(f.Name, `/\`) || f.FileInfo().IsDir() || f.UncompressedSize64 < 1024 {
			continue
		}
		if inspected++; inspected > maxZipEntries {
			break
		}
		inner, err := inspectZipEntry(f)
		if err != nil || inner == nil {
			continue
		}
		return &Details{Type: TypeZIP, Format: "archive with an installer", Engine: inner.Engine,
			Evidence: append([]string{"contains " + f.Name + " at its root"}, inner.Evidence...)}, nil
	}
	return nil, nil
}

func readManifest(f *zip.File) (*appxManifest, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, manifestBytes))
	if err != nil {
		return nil, err
	}
	var m appxManifest
	return &m, xml.Unmarshal(data, &m)
}

// inspectZipEntry identifies an installer inside an archive from the start
// of its decompressed content (engine markers or the MSI class ID).
func inspectZipEntry(f *zip.File) (*Details, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, zipEntryBytes))
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	r := bytes.NewReader(data)
	switch {
	case bytes.HasPrefix(data, cfbMagic):
		if c, ok := cfbClass(r); ok && c == clsidMSI {
			return &Details{Type: TypeMSI, Evidence: []string{f.Name + " is a Windows Installer database"}}, nil
		}
	case bytes.HasPrefix(data, []byte("MZ")):
		pe, err := parsePE(r, int64(len(data)))
		if err != nil || pe.dll {
			return nil, nil
		}
		if engine, ev := engineOf(pe, data); engine != "" {
			return &Details{Type: TypeEXE, Engine: engine, Evidence: []string{f.Name + ": " + ev}}, nil
		}
	}
	return nil, nil
}

// peInfo is what purge needs from PE headers.
type peInfo struct {
	dll      bool
	sections []string
	imageEnd int64 // end of the last section's raw data: where an overlay starts
	signed   bool
}

func parsePE(r io.ReaderAt, size int64) (*peInfo, error) {
	hdr := make([]byte, 64)
	if _, err := r.ReadAt(hdr, 0); err != nil || hdr[0] != 'M' || hdr[1] != 'Z' {
		return nil, errors.New("not a PE file")
	}
	off := int64(le.Uint32(hdr[0x3C:]))
	if off <= 0 || off > 64*1024 || off+24 > size {
		return nil, errors.New("not a PE file")
	}
	coff := make([]byte, 24)
	if _, err := r.ReadAt(coff, off); err != nil || !bytes.Equal(coff[:4], []byte("PE\x00\x00")) {
		return nil, errors.New("not a PE file")
	}
	nsec := int(le.Uint16(coff[6:]))
	optSize := int64(le.Uint16(coff[20:]))
	pe := &peInfo{dll: le.Uint16(coff[22:])&0x2000 != 0}
	if nsec == 0 || nsec > 96 {
		return nil, errors.New("not a PE file")
	}
	opt := make([]byte, optSize)
	if _, err := r.ReadAt(opt, off+24); err == nil && len(opt) >= 2 {
		dirs := 96 // PE32
		if le.Uint16(opt) == 0x20B {
			dirs = 112 // PE32+
		}
		if sec := dirs + 4*8; len(opt) >= sec+8 {
			pe.signed = le.Uint32(opt[sec:]) != 0 && le.Uint32(opt[sec+4:]) != 0
		}
	}
	tbl := make([]byte, 40*nsec)
	if _, err := r.ReadAt(tbl, off+24+optSize); err != nil {
		return nil, errors.New("truncated PE section table")
	}
	for i := 0; i < nsec; i++ {
		s := tbl[i*40:]
		pe.sections = append(pe.sections, strings.TrimRight(string(s[:8]), "\x00"))
		if end := int64(le.Uint32(s[20:])) + int64(le.Uint32(s[16:])); end > pe.imageEnd {
			pe.imageEnd = end
		}
	}
	return pe, nil
}

var (
	nsisMagic = append([]byte{0xEF, 0xBE, 0xAD, 0xDE}, []byte("NullsoftInst")...)
	innoMagic = []byte("Inno Setup Setup Data (")
)

// engineOf names the setup engine that built an executable, from its
// sections and payload markers in data (the start of the file, plus the
// start of its overlay when that lies further in).
func engineOf(pe *peInfo, data []byte) (engine, evidence string) {
	for _, s := range pe.sections {
		if s == ".wixburn" {
			return "WiX Burn", "WiX Burn bundle (.wixburn section)"
		}
	}
	switch {
	case bytes.Contains(data, nsisMagic):
		return "NSIS", "Nullsoft (NSIS) installer data"
	case bytes.Contains(data, innoMagic):
		return "Inno Setup", "Inno Setup installer data"
	case bytes.Contains(data, []byte("InstallShield")):
		return "InstallShield", "InstallShield setup launcher"
	case bytes.Contains(data, []byte("Advanced Installer")):
		return "Advanced Installer", "Advanced Installer bootstrapper"
	}
	return "", ""
}

// setupWords in version resource text identify a setup program.
var setupWords = regexp.MustCompile(`(?i)\b(setup|installer|install|installation|bootstrapper)\b`)

func inspectEXE(f *os.File, p string, size int64) (*Details, error) {
	pe, err := parsePE(f, size)
	if err != nil || pe.dll {
		return nil, nil
	}
	data := make([]byte, min(size, markerScanBytes))
	n, _ := f.ReadAt(data, 0)
	data = data[:n]
	if pe.imageEnd > int64(len(data)) && pe.imageEnd < size {
		tail := make([]byte, min(size-pe.imageEnd, 1<<20))
		m, _ := f.ReadAt(tail, pe.imageEnd)
		data = append(data, tail[:m]...)
	}
	d := &Details{Type: TypeEXE, Format: "setup program"}
	engine, ev := engineOf(pe, data)
	if engine != "" {
		d.Engine = engine
		d.Evidence = append(d.Evidence, ev)
	}
	vi, _ := versionInfo(p)
	d.Product, d.Version, d.Publisher = vi["ProductName"], firstNonEmpty(vi["ProductVersion"], vi["FileVersion"]), vi["CompanyName"]
	d.Description = vi["FileDescription"]
	for _, key := range []string{"FileDescription", "ProductName", "OriginalFilename", "InternalName", "Comments"} {
		if v := vi[key]; v != "" && setupWords.MatchString(strings.TrimSuffix(strings.ToLower(v), ".exe")) {
			d.Evidence = append(d.Evidence, "version resource "+key+": "+v)
			break
		}
	}
	if d.Engine == "" && strings.Contains(strings.ToLower(vi["Comments"]+vi["CompanyName"]+vi["ProductName"]), "installshield") {
		d.Engine = "InstallShield"
	}
	if len(d.Evidence) == 0 {
		return nil, nil // a program, not an installer
	}
	if pe.signed {
		d.Evidence = append(d.Evidence, "carries an Authenticode signature (not verified)")
	}
	return d, nil
}

// inspectISO accepts ISO 9660 images. setup.exe, autorun.inf or a Windows
// Installer package at the root is installer evidence; other images are
// listed for review only.
func inspectISO(f *os.File) (*Details, error) {
	pvd := make([]byte, 2048)
	if _, err := f.ReadAt(pvd, 16*2048); err != nil || pvd[0] != 1 || string(pvd[1:6]) != "CD001" {
		return nil, nil
	}
	d := &Details{Type: TypeISO, Format: "disc image", Product: strings.TrimSpace(string(pvd[40:72]))}
	root := pvd[156:190]
	lba, size := int64(le.Uint32(root[2:])), int64(le.Uint32(root[10:]))
	size = min(size, 64*1024)
	dir := make([]byte, size)
	if _, err := f.ReadAt(dir, lba*2048); err == nil {
		var found []string
		for i := 0; i < len(dir); {
			n := int(dir[i])
			if n == 0 { // records do not cross sectors; skip to the next one
				i = (i/2048 + 1) * 2048
				continue
			}
			if i+n > len(dir) || n < 34 {
				break
			}
			nameLen := int(dir[i+32])
			if 33+nameLen <= n {
				name := strings.ToUpper(strings.SplitN(string(dir[i+33:i+33+nameLen]), ";", 2)[0])
				if name == "SETUP.EXE" || name == "AUTORUN.INF" || strings.HasSuffix(name, ".MSI") {
					found = append(found, strings.ToLower(name))
				}
			}
			i += n
		}
		if len(found) > 0 {
			d.Evidence = []string{"disc image with " + strings.Join(found, " and ") + " at its root"}
			return d, nil
		}
	}
	d.Evidence = []string{"disc image (ISO 9660)"}
	d.Review = "disc image without setup.exe or autorun.inf at its root: review it"
	return d, nil
}

func publisherCN(dn string) string {
	for _, part := range strings.Split(dn, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(strings.ToUpper(part), "CN=") {
			return strings.Trim(part[3:], `"`)
		}
	}
	return dn
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
