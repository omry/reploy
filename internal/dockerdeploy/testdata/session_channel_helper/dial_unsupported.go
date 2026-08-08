//go:build !linux

package main

import (
	"fmt"
	"net"
)

func netDialUnix(string) (*net.UnixConn, error) {
	return nil, fmt.Errorf("session channel helper requires Linux")
}
