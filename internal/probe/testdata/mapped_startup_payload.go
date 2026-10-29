// A trusted disposable observer, never part of the shipped launcher.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

type observation struct {
	PID          int                 `json:"pid"`
	Threads      []map[string]string `json:"threads"`
	Securebits   uintptr             `json:"securebits"`
	PrivateLeaks []string            `json:"private_leaks"`
	Stdin        string              `json:"stdin"`
	NativeDrop   map[string]string   `json:"native_uid_drop,omitempty"`
}

func main() {
	runtime.LockOSThread()
	result := observation{PID: os.Getpid(), Threads: []map[string]string{}, PrivateLeaks: []string{}}
	entries, err := os.ReadDir("/proc/self/task")
	if err != nil {
		panic(err)
	}
	for _, entry := range entries {
		content, err := os.ReadFile("/proc/self/task/" + entry.Name() + "/status")
		if os.IsNotExist(err) {
			continue // A vanished thread cannot survive the transition.
		}
		if err != nil {
			panic(err)
		}
		fields := map[string]string{"tid": entry.Name()}
		for _, line := range strings.Split(string(content), "\n") {
			key, value, ok := strings.Cut(line, ":")
			if ok && (key == "Uid" || key == "Gid" || key == "Groups" || strings.HasPrefix(key, "Cap") || key == "NoNewPrivs" || key == "Seccomp") {
				fields[key] = strings.Join(strings.Fields(value), " ")
			}
		}
		result.Threads = append(result.Threads, fields)
	}
	result.Securebits, _, _ = syscall.RawSyscall6(syscall.SYS_PRCTL, 27, 0, 0, 0, 0, 0)
	for _, fd := range os.Args[1:] {
		value, err := strconv.Atoi(fd)
		if err != nil {
			panic(err)
		}
		// The fixture gives each inherited private handle a unique inode.
		// Runtime-created descriptors can reuse its number after exec.
		link, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", value))
		if err == nil && strings.Contains(link, "mapped-private-canary") {
			result.PrivateLeaks = append(result.PrivateLeaks, link)
		}
	}
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		panic(err)
	}
	result.Stdin = string(input)
	// Native masters must retain Linux's normal root-to-user capability
	// fixup. This checks behavior after the accepted initial-state snapshot.
	mask, err := strconv.ParseUint(result.Threads[0]["CapPrm"], 16, 64)
	if err != nil {
		panic(err)
	}
	if result.Threads[0]["Uid"] == "0 0 0 0" && mask&(1<<7) != 0 {
		if err := syscall.Setresuid(200, 200, 200); err != nil {
			panic(err)
		}
		content, err := os.ReadFile("/proc/thread-self/status")
		if err != nil {
			panic(err)
		}
		result.NativeDrop = map[string]string{}
		for _, line := range strings.Split(string(content), "\n") {
			key, value, ok := strings.Cut(line, ":")
			if ok && (key == "Uid" || key == "CapPrm" || key == "CapEff") {
				result.NativeDrop[key] = strings.Join(strings.Fields(value), " ")
			}
		}
	}
	if len(result.Threads) == 0 {
		panic("no surviving threads observed")
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		panic(err)
	}
}
