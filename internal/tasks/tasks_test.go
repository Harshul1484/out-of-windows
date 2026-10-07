package tasks_test

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Harshul1484/out-of-windows/internal/tasks"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }

// A definition as IRegisteredTask.Xml returns it (abridged), declaring UTF-16
// although the text was already decoded.
const updaterXML = `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo><Author>Contoso</Author><URI>\Contoso\Updater</URI></RegistrationInfo>
  <Triggers>
    <LogonTrigger><Enabled>true</Enabled><UserId>DESKTOP-1\alice</UserId></LogonTrigger>
    <CalendarTrigger><StartBoundary>2026-01-01T09:00:00</StartBoundary><Enabled>false</Enabled>
      <ScheduleByDay><DaysInterval>1</DaysInterval></ScheduleByDay></CalendarTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>S-1-5-21-1-2-3-1001</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings><Enabled>false</Enabled><Hidden>true</Hidden></Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%LOCALAPPDATA%\Contoso Updater\updater.exe</Command>
      <Arguments>/silent --log "C:\Users\alice\AppData\Local\Contoso Updater\logs\u.log"</Arguments>
      <WorkingDirectory>%LOCALAPPDATA%\Contoso Updater</WorkingDirectory>
    </Exec>
    <ComHandler><ClassId>{12345678-1234-1234-1234-123456789012}</ClassId></ComHandler>
  </Actions>
</Task>`

const systemXML = `<Task xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <Triggers><BootTrigger/><LogonTrigger/></Triggers>
  <Principals><Principal id="LocalSystem"><UserId>S-1-5-18</UserId><RunLevel>HighestAvailable</RunLevel></Principal></Principals>
  <Actions><Exec><Command>"C:\Program Files\Wingtip\svc.exe"</Command></Exec></Actions>
</Task>`

func TestParseXML(t *testing.T) {
	got, err := tasks.ParseXML(updaterXML)
	if err != nil {
		t.Fatal(err)
	}
	want := tasks.Task{
		Enabled:   false,
		Triggers:  []tasks.Trigger{tasks.TriggerLogon}, // the disabled calendar trigger is left out
		LogonUser: `DESKTOP-1\alice`,
		UserID:    "S-1-5-21-1-2-3-1001",
		Actions: []tasks.Action{{
			Command:          `%LOCALAPPDATA%\Contoso Updater\updater.exe`,
			Arguments:        `/silent --log "C:\Users\alice\AppData\Local\Contoso Updater\logs\u.log"`,
			WorkingDirectory: `%LOCALAPPDATA%\Contoso Updater`,
		}},
		OtherActions: 1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseXML =\n%+v\nwant\n%+v", got, want)
	}

	sys, err := tasks.ParseXML(systemXML)
	if err != nil {
		t.Fatal(err)
	}
	if !sys.Enabled || !sys.Has(tasks.TriggerBoot) || !sys.Has(tasks.TriggerLogon) || sys.LogonUser != "" ||
		!sys.HighestPrivileges || sys.UserID != "S-1-5-18" || sys.Actions[0].Command != `"C:\Program Files\Wingtip\svc.exe"` {
		t.Errorf("system task = %+v", sys)
	}
}

func TestParseXMLRejectsMalformed(t *testing.T) {
	for _, s := range []string{"", "<Task>", "not xml", "<Task><Triggers><LogonTrigger></Triggers></Task>",
		strings.Repeat("x", 2<<20)} {
		if _, err := tasks.ParseXML(s); err == nil {
			t.Errorf("ParseXML(%.20q) accepted", s)
		}
	}
}

func TestLogonUserOfSeveralTriggers(t *testing.T) {
	two := `<Task><Triggers><LogonTrigger><UserId>alice</UserId></LogonTrigger><LogonTrigger><UserId>bob</UserId></LogonTrigger></Triggers></Task>`
	if task, err := tasks.ParseXML(two); err != nil || task.LogonUser != "" {
		t.Errorf("two users: %+v, %v (must count as any user)", task, err)
	}
	any := `<Task><Triggers><LogonTrigger><UserId>alice</UserId></LogonTrigger><LogonTrigger/></Triggers></Task>`
	if task, err := tasks.ParseXML(any); err != nil || task.LogonUser != "" {
		t.Errorf("one trigger for any user: %+v, %v", task, err)
	}
}

func TestOwnedBy(t *testing.T) {
	alice := tasks.Account{SID: "S-1-5-21-1-2-3-1001", Name: "alice", Domain: "DESKTOP-1"}
	for _, id := range []string{"S-1-5-21-1-2-3-1001", "alice", `DESKTOP-1\alice`, `.\Alice`, " s-1-5-21-1-2-3-1001 "} {
		if !alice.Matches(id) {
			t.Errorf("%q does not match alice", id)
		}
	}
	for _, id := range []string{"", "bob", `OTHER\alice`, "S-1-5-18", "S-1-5-21-1-2-3-1002"} {
		if alice.Matches(id) {
			t.Errorf("%q matches alice", id)
		}
	}
	own := tasks.Task{UserID: "S-1-5-21-1-2-3-1001", LogonUser: `DESKTOP-1\alice`, Triggers: []tasks.Trigger{tasks.TriggerLogon}}
	if !own.OwnedBy(alice) {
		t.Error("alice's own logon task not owned by alice")
	}
	for name, task := range map[string]tasks.Task{
		"any user's sign-in":  {UserID: "alice", Triggers: []tasks.Trigger{tasks.TriggerLogon}},
		"runs as SYSTEM":      {UserID: "S-1-5-18", LogonUser: "alice", Triggers: []tasks.Trigger{tasks.TriggerLogon}},
		"runs as a group":     {GroupID: "S-1-5-32-545", Triggers: []tasks.Trigger{tasks.TriggerLogon}},
		"highest privileges":  {UserID: "alice", LogonUser: "alice", HighestPrivileges: true, Triggers: []tasks.Trigger{tasks.TriggerLogon}},
		"another user's task": {UserID: "bob", LogonUser: "bob", Triggers: []tasks.Trigger{tasks.TriggerLogon}},
		"no principal":        {Triggers: []tasks.Trigger{tasks.TriggerBoot}},
	} {
		if task.OwnedBy(alice) {
			t.Errorf("%s: owned by alice", name)
		}
	}
}

func TestCommandLineAndPaths(t *testing.T) {
	expand := func(s string) string {
		return strings.NewReplacer("%LOCALAPPDATA%", `C:\Users\alice\AppData\Local`, "%windir%", `C:\Windows`).Replace(s)
	}
	for _, c := range []struct {
		a     tasks.Action
		line  string
		paths []string
	}{
		{tasks.Action{Command: `C:\Program Files\App\app.exe`, Arguments: "--tray"},
			`"C:\Program Files\App\app.exe" --tray`, []string{`C:\Program Files\App\app.exe`}},
		{tasks.Action{Command: `"C:\App\app.exe"`}, `"C:\App\app.exe"`, []string{`C:\App\app.exe`}},
		// A host program: the script, DLL or config in its arguments counts.
		{tasks.Action{Command: `%windir%\System32\rundll32.exe`, Arguments: `"C:\Old App\x.dll",Start`},
			`%windir%\System32\rundll32.exe "C:\Old App\x.dll",Start`,
			[]string{`C:\Windows\System32\rundll32.exe`, `C:\Old App\x.dll`}},
		{tasks.Action{Command: "wscript.exe", Arguments: `/B /config:C:\Tool\cfg.xml //nologo`, WorkingDirectory: `%LOCALAPPDATA%\Tool`},
			`wscript.exe /B /config:C:\Tool\cfg.xml //nologo`, []string{`C:\Users\alice\AppData\Local\Tool`, `C:\Tool\cfg.xml`}},
		// Network, relative and embedded drive-like text are not local paths.
		{tasks.Action{Command: `\\server\share\x.exe`, Arguments: `relative\y.txt abc:\z`, WorkingDirectory: `..\w`},
			`\\server\share\x.exe relative\y.txt abc:\z`, nil},
	} {
		if got := c.a.CommandLine(); got != c.line {
			t.Errorf("CommandLine(%+v) = %q, want %q", c.a, got, c.line)
		}
		if got := c.a.Paths(expand); !reflect.DeepEqual(got, c.paths) {
			t.Errorf("Paths(%+v) = %q, want %q", c.a, got, c.paths)
		}
	}
}

func TestNameAndWindowsFolder(t *testing.T) {
	for path, want := range map[string]string{`\Updater`: "Updater", `\Contoso\Updater`: "Updater", `\`: ""} {
		if got := (tasks.Task{Path: path}).Name(); got != want {
			t.Errorf("Name(%q) = %q", path, got)
		}
	}
	if !(tasks.Task{Path: `\Microsoft\Windows\Defrag\ScheduledDefrag`}).Windows() || (tasks.Task{Path: `\MicrosoftEdgeUpdateTaskMachineCore`}).Windows() {
		t.Error("Windows folder detection is wrong")
	}
}

func FuzzParseXML(f *testing.F) {
	f.Add(updaterXML)
	f.Add(systemXML)
	f.Add(`<Task><Actions><Exec><Command>x</Command></Exec></Actions></Task>`)
	f.Fuzz(func(t *testing.T, s string) {
		task, err := tasks.ParseXML(s)
		if err != nil {
			return
		}
		if task.Triggers == nil || task.Actions == nil {
			t.Fatalf("nil slices in %+v", task)
		}
	})
}
