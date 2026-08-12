//go:build linux

package controlledsession

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"

	"github.com/charmbracelet/x/term"
)

var controllerTerminalDirectoryPatternV1 = regexp.MustCompile(`^reploy-controlled-session-[0-9a-f]{32}$`)
var controllerTerminalAttachmentHomeV1 = ControllerTemporaryHomeV1

func RunTerminalAttachmentV1(ctx context.Context, options TerminalAttachmentOptionsV1) (resultErr error) {
	return runTerminalAttachmentAtHomeV1(ctx, options, controllerTerminalAttachmentHomeV1)
}

func runTerminalAttachmentAtHomeV1(ctx context.Context, options TerminalAttachmentOptionsV1, temporaryHome string) (resultErr error) {
	if ctx == nil || ctx.Done() == nil {
		return fmt.Errorf("run controlled-session terminal attachment: cancelable context is required")
	}
	if options.Input == nil || options.Output == nil {
		return fmt.Errorf("run controlled-session terminal attachment: input and output are required")
	}
	connection, err := dialTerminalAttachmentV1(ctx, options.SocketPath, temporaryHome)
	if err != nil {
		return err
	}
	tty, err := prepareTerminalAttachmentTTYV1(options.Input)
	if err != nil {
		return errors.Join(err, connection.Close())
	}
	return runTerminalAttachmentV1(ctx, connection, options.Input, options.Output, tty)
}

func dialTerminalAttachmentV1(ctx context.Context, socket string, temporaryHome string) (*terminalAttachmentConnectionV1, error) {
	if !filepath.IsAbs(temporaryHome) || filepath.Clean(temporaryHome) != temporaryHome || !filepath.IsAbs(socket) || filepath.Clean(socket) != socket || filepath.Dir(filepath.Dir(socket)) != temporaryHome || filepath.Base(socket) != ControllerTerminalSocketNameV1 || !controllerTerminalDirectoryPatternV1.MatchString(filepath.Base(filepath.Dir(socket))) {
		return nil, fmt.Errorf("connect controlled-session terminal attachment: socket path does not use the broker path grammar")
	}
	if err := verifyTerminalAttachmentPathV1("temporary home", temporaryHome, 0o700, os.ModeDir); err != nil {
		return nil, err
	}
	if err := verifyTerminalAttachmentPathV1("directory", filepath.Dir(socket), 0o700, os.ModeDir); err != nil {
		return nil, err
	}
	if err := verifyTerminalAttachmentPathV1("socket", socket, 0o600, os.ModeSocket); err != nil {
		return nil, err
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, fmt.Errorf("connect controlled-session terminal attachment: %w", err)
	}
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		return nil, errors.Join(fmt.Errorf("controlled-session terminal broker did not use a Unix connection"), connection.Close())
	}
	uid, gid, err := unixPeerIdentityV1(unixConnection)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("inspect controlled-session terminal broker identity: %w", err), connection.Close())
	}
	if uid != uint32(os.Geteuid()) || gid != uint32(os.Getegid()) {
		return nil, errors.Join(fmt.Errorf("controlled-session terminal broker identity is %d:%d, expected %d:%d", uid, gid, os.Geteuid(), os.Getegid()), connection.Close())
	}
	return &terminalAttachmentConnectionV1{connection: connection}, nil
}

func verifyTerminalAttachmentPathV1(subject string, path string, permission os.FileMode, kind os.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect controlled-session terminal %s: %w", subject, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeType != kind {
		return fmt.Errorf("controlled-session terminal %s is not the required real filesystem type", subject)
	}
	if info.Mode().Perm() != permission {
		return fmt.Errorf("controlled-session terminal %s mode is %04o, expected %04o", subject, info.Mode().Perm(), permission)
	}
	identity, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("controlled-session terminal %s ownership is unavailable", subject)
	}
	if identity.Uid != uint32(os.Geteuid()) || identity.Gid != uint32(os.Getegid()) {
		return fmt.Errorf("controlled-session terminal %s is owned by %d:%d, expected %d:%d", subject, identity.Uid, identity.Gid, os.Geteuid(), os.Getegid())
	}
	return nil
}

func prepareTerminalAttachmentTTYV1(input io.Reader) (terminalAttachmentTTYV1, error) {
	file, ok := input.(*os.File)
	if !ok || !term.IsTerminal(file.Fd()) {
		return terminalAttachmentTTYV1{restore: func() error { return nil }}, nil
	}
	columns, rows, err := term.GetSize(file.Fd())
	if err != nil {
		return terminalAttachmentTTYV1{}, fmt.Errorf("read controlled-session initial terminal dimensions: %w", err)
	}
	initial, err := terminalResizeRequestV1(columns, rows)
	if err != nil {
		return terminalAttachmentTTYV1{}, err
	}
	state, err := term.MakeRaw(file.Fd())
	if err != nil {
		return terminalAttachmentTTYV1{}, fmt.Errorf("switch controlled-session terminal to raw mode: %w", err)
	}
	resizes := make(chan terminalAttachmentResizeV1, 1)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGWINCH)
	stopped := make(chan struct{})
	go func() {
		defer close(resizes)
		for {
			select {
			case <-stopped:
				return
			case <-signals:
				columns, rows, sizeErr := term.GetSize(file.Fd())
				var request RequestV1
				if sizeErr == nil {
					request, sizeErr = terminalResizeRequestV1(columns, rows)
				}
				resized := terminalAttachmentResizeV1{request: request, err: sizeErr}
				select {
				case resizes <- resized:
				default:
					select {
					case <-resizes:
					default:
					}
					resizes <- resized
				}
			}
		}
	}()
	var restoreOnce sync.Once
	var restoreErr error
	return terminalAttachmentTTYV1{
		initialResize: &initial,
		resizes:       resizes,
		restore: func() error {
			restoreOnce.Do(func() {
				signal.Stop(signals)
				close(stopped)
				restoreErr = term.Restore(file.Fd(), state)
			})
			return restoreErr
		},
	}, nil
}

func terminalResizeRequestV1(columns int, rows int) (RequestV1, error) {
	if columns < 1 || columns > 65535 || rows < 1 || rows > 65535 {
		return RequestV1{}, fmt.Errorf("controlled-session terminal dimensions must be between 1 and 65535")
	}
	return RequestV1{Kind: RequestResizeV1, Columns: uint32(columns), Rows: uint32(rows)}, nil
}
