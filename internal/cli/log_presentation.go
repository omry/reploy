package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type runtimeTimestampWriter struct {
	output io.Writer
	color  bool
	buffer bytes.Buffer
}

func newRuntimeTimestampWriter(output io.Writer, color bool) *runtimeTimestampWriter {
	if output == nil {
		output = io.Discard
	}
	return &runtimeTimestampWriter{output: output, color: color}
}

func (writer *runtimeTimestampWriter) Write(content []byte) (int, error) {
	if !writer.color {
		return writer.output.Write(content)
	}
	written := len(content)
	if _, err := writer.buffer.Write(content); err != nil {
		return 0, err
	}
	for {
		line, err := writer.buffer.ReadString('\n')
		if err != nil {
			_, _ = writer.buffer.WriteString(line)
			break
		}
		if _, err := io.WriteString(writer.output, colorRuntimeTimestampLine(line)); err != nil {
			return 0, err
		}
	}
	return written, nil
}

func (writer *runtimeTimestampWriter) Flush() error {
	if writer == nil || writer.buffer.Len() == 0 {
		return nil
	}
	_, err := io.WriteString(writer.output, colorRuntimeTimestampLine(writer.buffer.String()))
	writer.buffer.Reset()
	return err
}

func colorRuntimeTimestampLine(line string) string {
	token, rest, found := strings.Cut(line, " ")
	if !found {
		return line
	}
	if _, err := time.Parse(time.RFC3339Nano, token); err != nil {
		return line
	}
	separator := strings.IndexByte(token, 'T')
	if separator <= 0 {
		return line
	}
	date := token[:separator]
	clockAndZone := token[separator+1:]
	zoneAt := strings.LastIndexAny(clockAndZone, "+-")
	if strings.HasSuffix(clockAndZone, "Z") {
		zoneAt = len(clockAndZone) - 1
	}
	if zoneAt <= 0 {
		return line
	}
	clock, zone := clockAndZone[:zoneAt], clockAndZone[zoneAt:]
	return fmt.Sprintf(
		"\x1b[2m%s\x1b[0mT\x1b[36m%s\x1b[0m\x1b[2m%s\x1b[0m %s",
		date, clock, zone, rest,
	)
}

func runtimeLogColorEnabled(output io.Writer, interactiveOverride *bool) bool {
	interactive := operationOutputIsInteractive(output)
	if interactiveOverride != nil {
		interactive = *interactiveOverride
	}
	if envBool("CI") || strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb") {
		interactive = false
	}
	return operationColorEnabled(interactive)
}
