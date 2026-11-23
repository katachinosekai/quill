//go:build !windows

package main

import (
	"io"
	"os"
	"strconv"
	"syscall"
)

// The device-lock state crosses a live reload in a pipe, never a file: the
// old build writes it into the pipe and closes the write end, and the read
// end (made inheritable only under syscall.ForkLock, right before the exec,
// so no helper started meanwhile gets it) survives the exec under the same
// fd number, named by unlocksEnv. The new build reads it once at start and
// closes it. Only this process (and what can already read its memory: the
// same user's debuggers, /proc/<pid>/fd) can read it.

const (
	unlocksEnv = "QUILL_UNLOCKS_FD"
	unlocksMax = 32 << 10 // below the pipe buffer: the write never blocks
)

// passUnlocks prepares b for the exec: it returns the env entry, and done,
// to call only when the exec failed (it closes the fd and releases the
// lock). ok=false: nothing to pass (too big, no pipe).
func passUnlocks(b []byte) (env string, done func(), ok bool) {
	if len(b) == 0 || len(b) > unlocksMax {
		return "", nil, false
	}
	r, w, err := os.Pipe()
	if err != nil {
		return "", nil, false
	}
	_, err = w.Write(b)
	w.Close()
	if err != nil {
		r.Close()
		return "", nil, false
	}
	syscall.ForkLock.RLock()
	fd, err := syscall.Dup(int(r.Fd())) // a dup has no close-on-exec
	r.Close()
	if err != nil {
		syscall.ForkLock.RUnlock()
		return "", nil, false
	}
	return unlocksEnv + "=" + strconv.Itoa(fd), func() {
		syscall.Close(fd)
		syscall.ForkLock.RUnlock()
	}, true
}

// takeUnlocks reads what the old build passed (nil if nothing) and closes
// the fd; the env entry is removed so no child sees it.
func takeUnlocks() []byte {
	v := os.Getenv(unlocksEnv)
	if v == "" {
		return nil
	}
	os.Unsetenv(unlocksEnv)
	fd, err := strconv.Atoi(v)
	if err != nil || fd < 3 {
		return nil
	}
	var st syscall.Stat_t
	if syscall.Fstat(fd, &st) != nil || st.Mode&syscall.S_IFMT != syscall.S_IFIFO {
		return nil // not our pipe: leave whatever it is alone
	}
	syscall.CloseOnExec(fd)
	f := os.NewFile(uintptr(fd), "unlocks")
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, unlocksMax+1))
	if err != nil || len(b) > unlocksMax {
		return nil
	}
	return b
}
