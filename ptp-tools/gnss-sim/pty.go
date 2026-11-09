package main

import (
	"fmt"
	"log"
	"os"
	"syscall"
	"unsafe"
)

// ptyWriter wraps a PTY master fd for non-blocking writes.
// Writes that fail with EAGAIN (buffer full) or EIO (no slave reader)
// are silently dropped so the simulator loop never stalls.
type ptyWriter struct {
	master   *os.File
	linkPath string
}

func (pw *ptyWriter) Write(p []byte) (int, error) {
	// Use syscall.Write directly to get true O_NONBLOCK semantics.
	// os.File.Write goes through Go's internal poller which retries
	// EAGAIN in a tight loop, spinning the CPU and blocking the
	// goroutine when the PTY buffer is full (no slave reader).
	n, err := syscall.Write(int(pw.master.Fd()), p)
	if err != nil {
		if err == syscall.EAGAIN || err == syscall.EIO {
			return len(p), nil
		}
		return n, err
	}
	return n, nil
}

func (pw *ptyWriter) Close() error {
	os.Remove(pw.linkPath)
	return pw.master.Close()
}

// openPTYLink creates a PTY pair, symlinks the slave to linkPath, and
// returns a ptyWriter that writes to the master with O_NONBLOCK.
func openPTYLink(linkPath string) (*ptyWriter, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NONBLOCK|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/ptmx: %w", err)
	}

	fd := master.Fd()

	var ptyNum uint32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd,
		syscall.TIOCGPTN, uintptr(unsafe.Pointer(&ptyNum))); errno != 0 {
		master.Close()
		return nil, fmt.Errorf("TIOCGPTN: %w", errno)
	}

	var unlock int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd,
		syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		master.Close()
		return nil, fmt.Errorf("TIOCSPTLCK: %w", errno)
	}

	slavePath := fmt.Sprintf("/dev/pts/%d", ptyNum)

	os.Remove(linkPath)
	if err := os.Symlink(slavePath, linkPath); err != nil {
		master.Close()
		return nil, fmt.Errorf("symlink %s -> %s: %w", linkPath, slavePath, err)
	}

	log.Printf("created PTY: %s -> %s", linkPath, slavePath)
	return &ptyWriter{master: master, linkPath: linkPath}, nil
}
