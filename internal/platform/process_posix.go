//go:build linux || darwin

package platform

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

type posixProcess struct {
	command  *exec.Cmd
	pid      int
	wait     sync.Once
	exit     Exit
	err      error
	stopMu   sync.Mutex
	graceful bool
	hardStop sync.Once
	hardDone chan struct{}
}

func Start(spec Spec) (OwnedProcess, *os.File, *os.File, error) {
	executable, err := resolveExecutable(spec)
	if err != nil {
		return nil, nil, nil, err
	}
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		return nil, nil, nil, err
	}
	command := exec.Command(executable, spec.Arguments...)
	command.Dir = spec.Directory
	command.Env = append([]string(nil), spec.Environment...)
	command.Stdout = stdoutWriter
	command.Stderr = stderrWriter
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := revalidateDirectory(spec); err != nil {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		_ = stderrReader.Close()
		_ = stderrWriter.Close()
		return nil, nil, nil, err
	}
	if err := command.Start(); err != nil {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		_ = stderrReader.Close()
		_ = stderrWriter.Close()
		return nil, nil, nil, err
	}
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()
	return &posixProcess{command: command, pid: command.Process.Pid, hardDone: make(chan struct{})}, stdoutReader, stderrReader, nil
}

func (p *posixProcess) Wait() (Exit, error) {
	p.wait.Do(func() {
		waitErr := p.command.Wait()
		if p.gracefulStopRequested() {
			p.waitForGroup()
		} else {
			_ = signalGroup(p.pid, syscall.SIGKILL)
		}
		if p.command.ProcessState == nil {
			p.err = waitErr
			return
		}
		status, ok := p.command.ProcessState.Sys().(syscall.WaitStatus)
		if !ok {
			p.err = waitErr
			return
		}
		if status.Signaled() {
			termination := strings.ToLower(status.Signal().String())
			p.exit.Termination = &termination
		} else {
			code := status.ExitStatus()
			p.exit.ExitCode = &code
		}
		if waitErr != nil {
			var exitError *exec.ExitError
			if !errors.As(waitErr, &exitError) {
				p.err = waitErr
			}
		}
	})
	return p.exit, p.err
}

func (p *posixProcess) RequestGracefulStop() {
	p.stopMu.Lock()
	p.graceful = true
	p.stopMu.Unlock()
}

func (p *posixProcess) GracefulStop() error {
	return signalGroup(p.pid, syscall.SIGTERM)
}

func (p *posixProcess) HardStop() error {
	err := signalGroup(p.pid, syscall.SIGKILL)
	p.hardStop.Do(func() { close(p.hardDone) })
	return err
}

func (p *posixProcess) Close() error { return nil }

func (p *posixProcess) gracefulStopRequested() bool {
	p.stopMu.Lock()
	defer p.stopMu.Unlock()
	return p.graceful
}

func (p *posixProcess) waitForGroup() {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for processGroupAlive(p.pid) {
		select {
		case <-p.hardDone:
			return
		case <-ticker.C:
		}
	}
}

func processGroupAlive(pid int) bool {
	err := syscall.Kill(-pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func signalGroup(pid int, signal syscall.Signal) error {
	err := syscall.Kill(-pid, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
