//go:build amd64 || arm64

package tasks

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// System reads the scheduled tasks of the running Windows through the Task
// Scheduler COM API (ITaskService), as the current user: without
// administrator rights it sees the tasks the user may read. The only write is
// IRegisteredTask.Enabled.
//
// The calls pass VARIANT arguments by reference, which is how the x64 and
// ARM64 calling conventions pass 24-byte structures by value; other
// architectures use the stub in system_unsupported_windows.go.
type System struct{}

var (
	ole32                = windows.NewLazySystemDLL("ole32.dll")
	oleaut32             = windows.NewLazySystemDLL("oleaut32.dll")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	procSysAllocString   = oleaut32.NewProc("SysAllocString")
	procSysFreeString    = oleaut32.NewProc("SysFreeString")
	procSysStringLen     = oleaut32.NewProc("SysStringLen")

	// CLSID_TaskScheduler and IID_ITaskService (taskschd.h).
	clsidTaskScheduler = windows.GUID{Data1: 0x0f87369f, Data2: 0xa4e5, Data3: 0x4cfc,
		Data4: [8]byte{0xbd, 0x3e, 0x73, 0xe6, 0x15, 0x45, 0x72, 0xdd}}
	iidITaskService = windows.GUID{Data1: 0x2faba4c7, Data2: 0x4da9, Data3: 0x4013,
		Data4: [8]byte{0x96, 0x97, 0x20, 0xcc, 0x3f, 0xd4, 0x0f, 0x85}}
)

// Vtable slots, as recorded in the Task Scheduler type library in
// taskschd.dll (FUNCDESC.oVft / pointer size). Slots 0-6 are IUnknown and
// IDispatch. Calling a wrong slot could run a task (IRegisteredTask.Run is
// slot 12), so each one is named and none is computed.
const (
	slotRelease = 2 // IUnknown::Release

	slotServiceGetFolder = 7  // ITaskService::GetFolder(BSTR, ITaskFolder**)
	slotServiceConnect   = 10 // ITaskService::Connect(VARIANT x4)

	slotFolderGetFolders = 10 // ITaskFolder::GetFolders(LONG, ITaskFolderCollection**)
	slotFolderGetTask    = 13 // ITaskFolder::GetTask(BSTR, IRegisteredTask**)
	slotFolderGetTasks   = 14 // ITaskFolder::GetTasks(LONG, IRegisteredTaskCollection**)

	slotCollectionCount = 7 // ITaskFolderCollection / IRegisteredTaskCollection::get_Count(LONG*)
	slotCollectionItem  = 8 // ...::get_Item(VARIANT, T**)

	slotTaskPath       = 8  // IRegisteredTask::get_Path(BSTR*)
	slotTaskGetEnabled = 10 // IRegisteredTask::get_Enabled(VARIANT_BOOL*)
	slotTaskPutEnabled = 11 // IRegisteredTask::put_Enabled(VARIANT_BOOL)
	slotTaskXML        = 20 // IRegisteredTask::get_Xml(BSTR*)
)

const (
	clsctxInprocServer = 0x1
	clsctxLocalServer  = 0x4
	coinitMultithread  = 0x0
	taskEnumHidden     = 0x1
	vtI4               = 3
	variantTrue        = 0xFFFF // VARIANT_BOOL -1
	variantFalse       = 0

	hrSFalse         = 0x00000001
	hrRPCChangedMode = 0x80010106
	hrAccessDenied   = 0x80070005 // HRESULT_FROM_WIN32(ERROR_ACCESS_DENIED)
	hrFileNotFound   = 0x80070002 // HRESULT_FROM_WIN32(ERROR_FILE_NOT_FOUND): no such task
	hrPathNotFound   = 0x80070003 // HRESULT_FROM_WIN32(ERROR_PATH_NOT_FOUND): no such folder
)

// maxFolderDepth and maxTasks bound a walk of the library.
const (
	maxFolderDepth = 16
	maxTasks       = 20000
)

// comObject is a COM interface pointer; its first field points to the
// vtable. Objects live in memory the Go runtime does not manage.
type comObject struct {
	vtbl *[32]uintptr
}

// call invokes the method in slot. Pointer arguments must be converted with
// uintptr(unsafe.Pointer(&x)) in the call expression itself: uintptrescapes
// then keeps x on the heap and alive until the call returns, as for
// LazyProc.Call.
//
//go:uintptrescapes
func (o *comObject) call(slot int, args ...uintptr) error {
	all := append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)
	r, _, _ := syscall.SyscallN(o.vtbl[slot], all...)
	if int32(r) < 0 {
		return hresult(uint32(r))
	}
	return nil
}

func (o *comObject) release() {
	if o != nil {
		syscall.SyscallN(o.vtbl[slotRelease], uintptr(unsafe.Pointer(o)))
	}
}

type hresult uint32

func (h hresult) Error() string {
	switch uint32(h) {
	case hrAccessDenied:
		return "access denied"
	case hrFileNotFound, hrPathNotFound:
		return "not found"
	}
	return fmt.Sprintf("Task Scheduler error 0x%08X", uint32(h))
}

func mapErr(err error) error {
	var h hresult
	if !errors.As(err, &h) {
		return err
	}
	switch uint32(h) {
	case hrAccessDenied:
		return ErrAccessDenied
	case hrFileNotFound, hrPathNotFound:
		return ErrNotFound
	}
	return err
}

// variant mirrors VARIANT on 64-bit Windows (24 bytes).
type variant struct {
	vt       uint16
	reserved [3]uint16
	val      int64
	_        int64
}

// bstr allocates a BSTR; free it with freeBSTR.
func bstr(s string) (uintptr, error) {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return 0, err
	}
	b, _, _ := procSysAllocString.Call(uintptr(unsafe.Pointer(p)))
	if b == 0 {
		return 0, errors.New("out of memory")
	}
	return b, nil
}

func freeBSTR(b uintptr) {
	if b != 0 {
		procSysFreeString.Call(b)
	}
}

// readBSTR converts a BSTR returned by a method into a string and frees it.
func readBSTR(p *uint16) string {
	if p == nil {
		return ""
	}
	defer procSysFreeString.Call(uintptr(unsafe.Pointer(p)))
	n, _, _ := procSysStringLen.Call(uintptr(unsafe.Pointer(p)))
	if n == 0 {
		return ""
	}
	return string(utf16.Decode(unsafe.Slice(p, int(n))))
}

// session runs fn with a connected ITaskService on a thread of its own: the
// thread is locked and never unlocked, so it ends with the goroutine and takes
// its COM state with it.
func session(fn func(svc *comObject) error) error {
	errc := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		errc <- connected(fn)
	}()
	return <-errc
}

func connected(fn func(svc *comObject) error) error {
	switch err := windows.CoInitializeEx(0, coinitMultithread); {
	case err == nil, errors.Is(err, syscall.Errno(hrSFalse)):
		defer windows.CoUninitialize()
	case errors.Is(err, syscall.Errno(hrRPCChangedMode)):
		// Already initialized on this thread: usable as it is.
	default:
		return fmt.Errorf("could not start COM: %w", err)
	}
	var svc *comObject
	r, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidTaskScheduler)), 0,
		clsctxInprocServer|clsctxLocalServer, uintptr(unsafe.Pointer(&iidITaskService)), uintptr(unsafe.Pointer(&svc)))
	if int32(r) < 0 || svc == nil {
		return fmt.Errorf("could not open the Task Scheduler: %w", hresult(uint32(r)))
	}
	defer svc.release()
	// Connect to the local service as the current user: four empty VARIANTs.
	var server, user, domain, password variant
	if err := svc.call(slotServiceConnect, uintptr(unsafe.Pointer(&server)), uintptr(unsafe.Pointer(&user)),
		uintptr(unsafe.Pointer(&domain)), uintptr(unsafe.Pointer(&password))); err != nil {
		return fmt.Errorf("could not connect to the Task Scheduler: %w", err)
	}
	return fn(svc)
}

func getFolder(svc *comObject, path string) (*comObject, error) {
	b, err := bstr(path)
	if err != nil {
		return nil, err
	}
	defer freeBSTR(b)
	var f *comObject
	if err := svc.call(slotServiceGetFolder, b, uintptr(unsafe.Pointer(&f))); err != nil {
		return nil, err
	}
	if f == nil {
		return nil, errors.New("no folder returned")
	}
	return f, nil
}

func count(coll *comObject) (int, error) {
	var n int32
	if err := coll.call(slotCollectionCount, uintptr(unsafe.Pointer(&n))); err != nil {
		return 0, err
	}
	return int(n), nil
}

// item returns element i (1-based) of a collection.
func item(coll *comObject, i int) (*comObject, error) {
	v := variant{vt: vtI4, val: int64(int32(i))}
	var o *comObject
	if err := coll.call(slotCollectionItem, uintptr(unsafe.Pointer(&v)), uintptr(unsafe.Pointer(&o))); err != nil {
		return nil, err
	}
	if o == nil {
		return nil, errors.New("no item returned")
	}
	return o, nil
}

func taskString(t *comObject, slot int) (string, error) {
	var p *uint16
	if err := t.call(slot, uintptr(unsafe.Pointer(&p))); err != nil {
		return "", err
	}
	return readBSTR(p), nil
}

func taskEnabled(t *comObject) (bool, error) {
	var b int16
	if err := t.call(slotTaskGetEnabled, uintptr(unsafe.Pointer(&b))); err != nil {
		return false, err
	}
	return b != 0, nil
}

// readTask reads one registered task: its path, its definition and its
// Enabled flag.
func readTask(t *comObject) (Task, error) {
	path, err := taskString(t, slotTaskPath)
	if err != nil {
		return Task{}, err
	}
	text, err := taskString(t, slotTaskXML)
	if err != nil {
		return Task{Path: path}, err
	}
	task, err := ParseXML(text)
	if err != nil {
		return Task{Path: path}, err
	}
	task.Path = path
	if on, err := taskEnabled(t); err == nil {
		task.Enabled = on
	}
	return task, nil
}

type walker struct {
	ctx        context.Context
	tasks      []Task
	unreadable int // folders or tasks that could not be read
}

func (w *walker) folder(f *comObject, depth int) {
	if w.ctx.Err() != nil || len(w.tasks) >= maxTasks {
		return
	}
	var coll *comObject
	if err := f.call(slotFolderGetTasks, taskEnumHidden, uintptr(unsafe.Pointer(&coll))); err != nil || coll == nil {
		w.unreadable++
	} else {
		n, err := count(coll)
		if err != nil {
			w.unreadable++
		}
		for i := 1; i <= n && w.ctx.Err() == nil && len(w.tasks) < maxTasks; i++ {
			t, err := item(coll, i)
			if err != nil {
				w.unreadable++
				continue
			}
			task, err := readTask(t)
			t.release()
			if err != nil {
				w.unreadable++
				continue
			}
			w.tasks = append(w.tasks, task)
		}
		coll.release()
	}
	if depth >= maxFolderDepth {
		return
	}
	var subs *comObject
	if err := f.call(slotFolderGetFolders, 0, uintptr(unsafe.Pointer(&subs))); err != nil || subs == nil {
		w.unreadable++
		return
	}
	defer subs.release()
	n, err := count(subs)
	if err != nil {
		w.unreadable++
		return
	}
	for i := 1; i <= n && w.ctx.Err() == nil; i++ {
		sub, err := item(subs, i)
		if err != nil {
			w.unreadable++
			continue
		}
		w.folder(sub, depth+1)
		sub.release()
	}
}

// List reads every task in the library that the current user can read.
func (System) List(ctx context.Context) ([]Task, []string, error) {
	var w walker
	w.ctx = ctx
	err := session(func(svc *comObject) error {
		root, err := getFolder(svc, `\`)
		if err != nil {
			return fmt.Errorf("could not open the task library: %w", err)
		}
		defer root.release()
		w.folder(root, 0)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	var warnings []string
	if w.unreadable > 0 {
		warnings = append(warnings, fmt.Sprintf("%d scheduled task folders or tasks could not be read (some are visible only to administrators)", w.unreadable))
	}
	if w.tasks == nil {
		w.tasks = []Task{}
	}
	return w.tasks, warnings, nil
}

// SetEnabled sets IRegisteredTask.Enabled of the task at path, after
// checking that the task still exists, and reads the flag back.
func (System) SetEnabled(path string, enabled bool) error {
	if !strings.HasPrefix(path, `\`) {
		return fmt.Errorf("invalid task path %q", path)
	}
	return session(func(svc *comObject) error {
		root, err := getFolder(svc, `\`)
		if err != nil {
			return mapErr(err)
		}
		defer root.release()
		b, err := bstr(path)
		if err != nil {
			return err
		}
		defer freeBSTR(b)
		var t *comObject
		if err := root.call(slotFolderGetTask, b, uintptr(unsafe.Pointer(&t))); err != nil || t == nil {
			if err == nil {
				err = ErrNotFound
			}
			return mapErr(err)
		}
		defer t.release()
		flag := uintptr(variantFalse)
		if enabled {
			flag = variantTrue
		}
		if err := t.call(slotTaskPutEnabled, flag); err != nil {
			return mapErr(err)
		}
		// Read back through a fresh object, not the one just written to.
		var again *comObject
		if err := root.call(slotFolderGetTask, b, uintptr(unsafe.Pointer(&again))); err != nil || again == nil {
			return fmt.Errorf("written, but the task could not be read back: %v", mapErr(err))
		}
		defer again.release()
		on, err := taskEnabled(again)
		if err != nil {
			return fmt.Errorf("written, but the task could not be read back: %v", mapErr(err))
		}
		if on != enabled {
			return errors.New("written, but the Task Scheduler still reports the old state")
		}
		return nil
	})
}

// Account returns the user this process runs as.
func (System) Account() Account { return CurrentAccount() }

// CurrentAccount returns the user this process runs as.
func CurrentAccount() Account {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return Account{}
	}
	a := Account{SID: u.User.Sid.String()}
	if name, domain, _, err := u.User.Sid.LookupAccount(""); err == nil {
		a.Name, a.Domain = name, domain
	}
	return a
}
