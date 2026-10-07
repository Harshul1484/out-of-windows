package sandbox

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	msi                          = windows.NewLazySystemDLL("msi.dll")
	procMsiOpenDatabaseW         = msi.NewProc("MsiOpenDatabaseW")
	procMsiDatabaseOpenViewW     = msi.NewProc("MsiDatabaseOpenViewW")
	procMsiViewExecute           = msi.NewProc("MsiViewExecute")
	procMsiViewClose             = msi.NewProc("MsiViewClose")
	procMsiDatabaseCommit        = msi.NewProc("MsiDatabaseCommit")
	procMsiCloseHandle           = msi.NewProc("MsiCloseHandle")
	procMsiGetSummaryInformation = msi.NewProc("MsiGetSummaryInformationW")
	procMsiSummaryInfoSetProp    = msi.NewProc("MsiSummaryInfoSetPropertyW")
	procMsiSummaryInfoPersist    = msi.NewProc("MsiSummaryInfoPersist")
)

// CreateMSI writes a Windows Installer database at path containing only a
// Property table with props (ProductName, ProductCode, ProductVersion,
// Manufacturer, ...) and summary information. It installs nothing and runs
// nothing: it is a fixture for installer detection.
func CreateMSI(path string, props map[string]string) error {
	if _, err := os.Lstat(path); err == nil {
		return nil // already seeded; the content is deterministic
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	var db uintptr
	const msidbopenCreate = 3
	if rc, _, _ := procMsiOpenDatabaseW.Call(uintptr(unsafe.Pointer(p)), msidbopenCreate, uintptr(unsafe.Pointer(&db))); rc != 0 {
		return fmt.Errorf("MsiOpenDatabase: error %d", rc)
	}
	defer procMsiCloseHandle.Call(db)
	exec := func(sql string) error {
		q, err := windows.UTF16PtrFromString(sql)
		if err != nil {
			return err
		}
		var view uintptr
		if rc, _, _ := procMsiDatabaseOpenViewW.Call(db, uintptr(unsafe.Pointer(q)), uintptr(unsafe.Pointer(&view))); rc != 0 {
			return fmt.Errorf("MsiDatabaseOpenView(%s): error %d", sql, rc)
		}
		defer procMsiCloseHandle.Call(view)
		if rc, _, _ := procMsiViewExecute.Call(view, 0); rc != 0 {
			return fmt.Errorf("MsiViewExecute(%s): error %d", sql, rc)
		}
		procMsiViewClose.Call(view)
		return nil
	}
	if err := exec("CREATE TABLE `Property` (`Property` CHAR(72) NOT NULL, `Value` LONGCHAR NOT NULL LOCALIZABLE PRIMARY KEY `Property`)"); err != nil {
		return err
	}
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := strings.ReplaceAll(props[k], "'", "")
		if err := exec("INSERT INTO `Property` (`Property`, `Value`) VALUES ('" + k + "', '" + v + "')"); err != nil {
			return err
		}
	}

	var si uintptr
	if rc, _, _ := procMsiGetSummaryInformation.Call(db, 0, 20, uintptr(unsafe.Pointer(&si))); rc != 0 {
		return fmt.Errorf("MsiGetSummaryInformation: error %d", rc)
	}
	defer procMsiCloseHandle.Call(si)
	const vtI4, vtLPSTR = 3, 30
	setStr := func(pid uintptr, v string) error {
		s, err := windows.UTF16PtrFromString(v)
		if err != nil {
			return err
		}
		if rc, _, _ := procMsiSummaryInfoSetProp.Call(si, pid, vtLPSTR, 0, 0, uintptr(unsafe.Pointer(s))); rc != 0 {
			return fmt.Errorf("MsiSummaryInfoSetProperty(%d): error %d", pid, rc)
		}
		return nil
	}
	setInt := func(pid uintptr, v int) error {
		if rc, _, _ := procMsiSummaryInfoSetProp.Call(si, pid, vtI4, uintptr(v), 0, 0); rc != 0 {
			return fmt.Errorf("MsiSummaryInfoSetProperty(%d): error %d", pid, rc)
		}
		return nil
	}
	for _, err := range []error{
		setStr(2, "Installation Database"),
		setStr(3, props["ProductName"]),
		setStr(4, props["Manufacturer"]),
		setStr(7, "x64;1033"),
		setStr(9, props["ProductCode"]),
		setInt(14, 200),
		setInt(15, 2),
	} {
		if err != nil {
			return err
		}
	}
	if rc, _, _ := procMsiSummaryInfoPersist.Call(si); rc != 0 {
		return fmt.Errorf("MsiSummaryInfoPersist: error %d", rc)
	}
	if rc, _, _ := procMsiDatabaseCommit.Call(db); rc != 0 {
		return fmt.Errorf("MsiDatabaseCommit: error %d", rc)
	}
	return nil
}
