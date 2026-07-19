package runner

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"time"
)

const maxCapturedOutput = 2 << 20

type Spec struct {
	Name        string
	Args        []string
	GracePeriod time.Duration
	OnLine      func(stream, line string)
}

type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Started  time.Time
	Finished time.Time
}

type Runner struct{}

func (Runner) Run(ctx context.Context, spec Spec) (Result, error) {
	if spec.Name == "" {
		return Result{}, fmt.Errorf("command name is required")
	}
	if spec.GracePeriod <= 0 {
		spec.GracePeriod = 3 * time.Second
	}
	command := exec.Command(spec.Name, spec.Args...)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return Result{}, fmt.Errorf("open stdout: %w", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return Result{}, fmt.Errorf("open stderr: %w", err)
	}
	result := Result{Started: time.Now().UTC(), ExitCode: -1}
	if err := command.Start(); err != nil {
		return result, fmt.Errorf("start %s: %w", spec.Name, err)
	}
	processGroupID := command.Process.Pid

	var stdoutBuffer, stderrBuffer cappedBuffer
	stdoutBuffer.limit, stderrBuffer.limit = maxCapturedOutput, maxCapturedOutput
	readDone := make(chan struct{}, 2)
	go scan(stdout, "stdout", &stdoutBuffer, spec.OnLine, readDone)
	go scan(stderr, "stderr", &stderrBuffer, spec.OnLine, readDone)
	waitDone := make(chan error, 1)
	go func() { waitDone <- command.Wait() }()

	var runErr error
	select {
	case runErr = <-waitDone:
	case <-ctx.Done():
		_ = syscall.Kill(-processGroupID, syscall.SIGTERM)
		timer := time.NewTimer(spec.GracePeriod)
		select {
		case runErr = <-waitDone:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
			_ = syscall.Kill(-processGroupID, syscall.SIGKILL)
			runErr = <-waitDone
		}
		runErr = errors.Join(ctx.Err(), runErr)
	}
	<-readDone
	<-readDone
	result.Finished = time.Now().UTC()
	result.Stdout = stdoutBuffer.String()
	result.Stderr = stderrBuffer.String()
	if command.ProcessState != nil {
		result.ExitCode = command.ProcessState.ExitCode()
	}
	if runErr != nil {
		return result, fmt.Errorf("%s exited with code %d: %w", spec.Name, result.ExitCode, runErr)
	}
	return result, nil
}

func scan(reader io.Reader, stream string, capture io.Writer, onLine func(string, string), done chan<- struct{}) {
	defer func() { done <- struct{}{} }()
	buffered := bufio.NewReaderSize(reader, 64*1024)
	var line bytes.Buffer
	for {
		value, err := buffered.ReadByte()
		if err != nil {
			if line.Len() > 0 && onLine != nil {
				onLine(stream, line.String())
			}
			return
		}
		_, _ = capture.Write([]byte{value})
		switch value {
		case '\n', '\r':
			if line.Len() > 0 && onLine != nil {
				onLine(stream, line.String())
			}
			line.Reset()
		default:
			_ = line.WriteByte(value)
		}
	}
}

type cappedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *cappedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := b.limit - b.Len()
	if remaining <= 0 {
		return original, nil
	}
	if len(value) > remaining {
		value = value[:remaining]
	}
	_, _ = b.Buffer.Write(value)
	return original, nil
}
