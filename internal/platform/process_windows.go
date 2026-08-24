//go:build windows

package platform

import (
	"errors"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const maxWindowsCommandLineUTF16 = 32767

type windowsProcess struct {
	process  windows.Handle
	job      windows.Handle
	pid      uint32
	wait     sync.Once
	exit     Exit
	err      error
	stopMu   sync.Mutex
	stopped  bool
	graceful bool
	hardStop sync.Once
	hardDone chan struct{}
	close    sync.Once
	closeErr error
}

type jobBasicAccountingInformation struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

func Start(spec Spec) (OwnedProcess, *os.File, *os.File, error) {
	executable, err := resolveExecutable(spec)
	if err != nil {
		return nil, nil, nil, err
	}
	commandLine := windows.ComposeCommandLine(append([]string{executable}, spec.Arguments...))
	if len(utf16.Encode([]rune(commandLine)))+1 > maxWindowsCommandLineUTF16 {
		return nil, nil, nil, errors.New("command line exceeds the Windows UTF-16 limit")
	}
	executable16, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return nil, nil, nil, err
	}
	commandLine16, err := windows.UTF16PtrFromString(commandLine)
	if err != nil {
		return nil, nil, nil, err
	}
	directory16, err := windows.UTF16PtrFromString(spec.Directory)
	if err != nil {
		return nil, nil, nil, err
	}
	environment, err := environmentBlock(spec.Environment)
	if err != nil {
		return nil, nil, nil, err
	}

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		closeFiles(stdoutReader, stdoutWriter)
		return nil, nil, nil, err
	}
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		closeFiles(stdoutReader, stdoutWriter, stderrReader, stderrWriter)
		return nil, nil, nil, err
	}
	failed := true
	defer func() {
		if failed {
			closeFiles(stdoutReader, stdoutWriter, stderrReader, stderrWriter, stdin)
		}
	}()

	handles := []windows.Handle{
		windows.Handle(stdin.Fd()), windows.Handle(stdoutWriter.Fd()), windows.Handle(stderrWriter.Fd()),
	}
	for _, handle := range handles {
		if err := windows.SetHandleInformation(handle, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			return nil, nil, nil, err
		}
	}
	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, nil, nil, err
	}
	defer attributes.Delete()
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0])); err != nil {
		return nil, nil, nil, err
	}

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	jobOpen := true
	defer func() {
		if jobOpen {
			_ = windows.CloseHandle(job)
		}
	}()
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return nil, nil, nil, err
	}

	startup := windows.StartupInfoEx{
		StartupInfo: windows.StartupInfo{
			Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{})), Flags: windows.STARTF_USESTDHANDLES,
			StdInput: handles[0], StdOutput: handles[1], StdErr: handles[2],
		},
		ProcThreadAttributeList: attributes.List(),
	}
	var processInfo windows.ProcessInformation
	flags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)
	if err := revalidateDirectory(spec); err != nil {
		return nil, nil, nil, err
	}
	if err := windows.CreateProcess(executable16, commandLine16, nil, nil, true, flags, &environment[0], directory16, &startup.StartupInfo, &processInfo); err != nil {
		return nil, nil, nil, err
	}
	processOpen := true
	threadOpen := true
	defer func() {
		if threadOpen {
			_ = windows.CloseHandle(processInfo.Thread)
		}
		if processOpen {
			_ = windows.CloseHandle(processInfo.Process)
		}
	}()
	closeFiles(stdoutWriter, stderrWriter, stdin)
	stdoutWriter, stderrWriter, stdin = nil, nil, nil

	if err := windows.AssignProcessToJobObject(job, processInfo.Process); err != nil {
		_ = windows.TerminateProcess(processInfo.Process, 1)
		_, _ = windows.WaitForSingleObject(processInfo.Process, windows.INFINITE)
		return nil, nil, nil, err
	}
	if _, err := windows.ResumeThread(processInfo.Thread); err != nil {
		_ = windows.TerminateJobObject(job, 1)
		_, _ = windows.WaitForSingleObject(processInfo.Process, windows.INFINITE)
		return nil, nil, nil, err
	}
	_ = windows.CloseHandle(processInfo.Thread)
	threadOpen = false
	processOpen = false
	jobOpen = false
	failed = false
	runtime.KeepAlive(handles)
	return &windowsProcess{
		process: processInfo.Process, job: job, pid: processInfo.ProcessId, hardDone: make(chan struct{}),
	}, stdoutReader, stderrReader, nil
}

func (p *windowsProcess) Wait() (Exit, error) {
	p.wait.Do(func() {
		if _, err := windows.WaitForSingleObject(p.process, windows.INFINITE); err != nil {
			p.err = err
			return
		}
		var code uint32
		if err := windows.GetExitCodeProcess(p.process, &code); err != nil {
			p.err = err
			return
		}
		value := int(code)
		p.exit.ExitCode = &value
		p.stopMu.Lock()
		if p.stopped {
			termination := "terminated"
			p.exit.Termination = &termination
		}
		p.stopMu.Unlock()
		if p.gracefulStopRequested() {
			p.err = errors.Join(p.err, p.waitForJob())
		} else {
			_ = windows.TerminateJobObject(p.job, 1)
		}
	})
	return p.exit, p.err
}

func (p *windowsProcess) RequestGracefulStop() {
	p.stopMu.Lock()
	p.stopped = true
	p.graceful = true
	p.stopMu.Unlock()
}

func (p *windowsProcess) GracefulStop() error {
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, p.pid)
}

func (p *windowsProcess) HardStop() error {
	p.markStopped()
	err := windows.TerminateJobObject(p.job, 1)
	p.hardStop.Do(func() { close(p.hardDone) })
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return nil
	}
	return err
}

func (p *windowsProcess) gracefulStopRequested() bool {
	p.stopMu.Lock()
	defer p.stopMu.Unlock()
	return p.graceful
}

func (p *windowsProcess) waitForJob() error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		active, err := p.jobHasProcesses()
		if err != nil || !active {
			return err
		}
		select {
		case <-p.hardDone:
			return nil
		case <-ticker.C:
		}
	}
}

func (p *windowsProcess) jobHasProcesses() (bool, error) {
	var information jobBasicAccountingInformation
	err := windows.QueryInformationJobObject(
		p.job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&information)),
		uint32(unsafe.Sizeof(information)), nil,
	)
	return information.ActiveProcesses != 0, err
}

func (p *windowsProcess) markStopped() {
	p.stopMu.Lock()
	p.stopped = true
	p.stopMu.Unlock()
}

func (p *windowsProcess) Close() error {
	p.close.Do(func() {
		p.closeErr = errors.Join(windows.CloseHandle(p.process), windows.CloseHandle(p.job))
	})
	return p.closeErr
}

func environmentBlock(environment []string) ([]uint16, error) {
	values := append([]string(nil), environment...)
	sort.SliceStable(values, func(i, j int) bool { return strings.ToLower(values[i]) < strings.ToLower(values[j]) })
	block := make([]uint16, 0)
	for _, value := range values {
		if strings.IndexByte(value, 0) >= 0 {
			return nil, errors.New("environment entry contains NUL")
		}
		block = append(block, utf16.Encode([]rune(value))...)
		block = append(block, 0)
	}
	if len(block) == 0 {
		block = append(block, 0)
	}
	return append(block, 0), nil
}

func closeFiles(files ...*os.File) {
	for _, file := range files {
		if file != nil {
			_ = file.Close()
		}
	}
}
