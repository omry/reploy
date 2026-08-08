package dockerdeploy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type dockerHijackedPTYV1 struct {
	connection net.Conn
	reader     *bufio.Reader
	writeMu    sync.Mutex
	closeOnce  sync.Once
	closeErr   error
}

func resizeDockerContainerPTYV1(
	ctx context.Context,
	docker CommandSpec,
	container string,
	columns uint32,
	rows uint32,
	timeout time.Duration,
) error {
	endpoint, source, err := effectiveDockerEndpointV1(ctx, docker, timeout)
	if err != nil {
		return err
	}
	if !localDockerEndpointV1(endpoint) {
		return fmt.Errorf(
			"remote Docker endpoint %q selected by %s is not supported; switch to a local Docker Engine or Docker Desktop context",
			endpoint, source,
		)
	}
	connection, err := dialLocalDockerEndpointV1(ctx, endpoint)
	if err != nil {
		return fmt.Errorf("dial Docker endpoint %q: %w", endpoint, err)
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()

	requestURL := "http://docker/containers/" + url.PathEscape(container) + "/resize?h=" +
		strconv.FormatUint(uint64(rows), 10) + "&w=" + strconv.FormatUint(uint64(columns), 10)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, nil)
	if err != nil {
		return fmt.Errorf("construct Docker PTY resize request: %w", err)
	}
	request.Close = true
	if err := request.Write(connection); err != nil {
		return fmt.Errorf("write Docker PTY resize request: %w", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), request)
	if err != nil {
		return fmt.Errorf("read Docker PTY resize response: %w", err)
	}
	defer response.Body.Close()
	diagnostic, _ := io.ReadAll(io.LimitReader(response.Body, commandOutputErrorLimit+1))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := strings.TrimSpace(string(diagnostic))
		if message != "" {
			return fmt.Errorf("Docker PTY resize returned %s: %s", response.Status, message)
		}
		return fmt.Errorf("Docker PTY resize returned %s", response.Status)
	}
	return nil
}

func attachDockerContainerPTYV1(
	ctx context.Context,
	docker CommandSpec,
	container string,
	timeout time.Duration,
) (dockerPTYAttachmentV1, error) {
	endpoint, source, err := effectiveDockerEndpointV1(ctx, docker, timeout)
	if err != nil {
		return nil, err
	}
	if !localDockerEndpointV1(endpoint) {
		return nil, fmt.Errorf(
			"remote Docker endpoint %q selected by %s is not supported; switch to a local Docker Engine or Docker Desktop context",
			endpoint, source,
		)
	}
	connection, err := dialLocalDockerEndpointV1(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("dial Docker endpoint %q: %w", endpoint, err)
	}
	claimed := false
	defer func() {
		if !claimed {
			_ = connection.Close()
		}
	}()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	watchDone := make(chan struct{})
	watchExited := make(chan struct{})
	go func() {
		defer close(watchExited)
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-watchDone:
		}
	}()
	var finishWatchOnce sync.Once
	finishWatch := func() {
		finishWatchOnce.Do(func() {
			close(watchDone)
			<-watchExited
			_ = connection.SetDeadline(time.Time{})
		})
	}
	defer finishWatch()

	requestURL := "http://docker/containers/" + url.PathEscape(container) + "/attach?stream=1&stdin=1&stdout=1&stderr=1"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("construct Docker PTY attach request: %w", err)
	}
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "tcp")
	if err := request.Write(connection); err != nil {
		return nil, fmt.Errorf("write Docker PTY attach request: %w", err)
	}

	reader := bufio.NewReader(connection)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		return nil, fmt.Errorf("read Docker PTY attach response: %w", err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		defer response.Body.Close()
		diagnostic, _ := io.ReadAll(io.LimitReader(response.Body, commandOutputErrorLimit+1))
		message := strings.TrimSpace(string(diagnostic))
		if len(message) > commandOutputErrorLimit {
			message = "[last 4000 bytes]\n" + message[len(message)-commandOutputErrorLimit:]
		}
		if message != "" {
			return nil, fmt.Errorf("Docker PTY attach returned %s: %s", response.Status, message)
		}
		return nil, fmt.Errorf("Docker PTY attach returned %s", response.Status)
	}
	finishWatch()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("establish Docker PTY attach: %w", err)
	}
	claimed = true
	return &dockerHijackedPTYV1{connection: connection, reader: reader}, nil
}

func (attachment *dockerHijackedPTYV1) Read(buffer []byte) (int, error) {
	return attachment.reader.Read(buffer)
}

func (attachment *dockerHijackedPTYV1) WriteContext(ctx context.Context, data []byte) error {
	attachment.writeMu.Lock()
	defer attachment.writeMu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := attachment.connection.SetWriteDeadline(deadline); err != nil {
			return err
		}
	}
	watchDone := make(chan struct{})
	watchExited := make(chan struct{})
	go func() {
		defer close(watchExited)
		select {
		case <-ctx.Done():
			_ = attachment.connection.SetWriteDeadline(time.Now())
		case <-watchDone:
		}
	}()
	defer func() {
		close(watchDone)
		<-watchExited
		_ = attachment.connection.SetWriteDeadline(time.Time{})
	}()
	for len(data) > 0 {
		count, err := attachment.connection.Write(data)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return err
		}
		if count == 0 {
			return io.ErrShortWrite
		}
		data = data[count:]
	}
	return nil
}

func (attachment *dockerHijackedPTYV1) Close() error {
	attachment.closeOnce.Do(func() {
		attachment.closeErr = attachment.connection.Close()
	})
	return attachment.closeErr
}
