// Package sessionclientcmd implements the controller-side session tool.
package sessionclientcmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/omry/reploy/internal/controlledsession"
)

var runControllerBroker = controlledsession.RunControllerBrokerV1
var runTerminalAttachment = controlledsession.RunTerminalAttachmentV1

// Main runs reploy-session-client and returns its process exit code.
func Main(args []string, input io.Reader, output io.Writer, stderr io.Writer) int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return Run(ctx, args, input, output, stderr)
}

// Run dispatches one controller-side session mode.
func Run(ctx context.Context, args []string, input io.Reader, output io.Writer, stderr io.Writer) int {
	if len(args) == 1 && isHelpArg(args[0]) {
		printHelp(output)
		return 0
	}
	if len(args) >= 1 && args[0] == "client" {
		if len(args) == 2 && isHelpArg(args[1]) {
			printClientHelp(output)
			return 0
		}
		if len(args) != 1 {
			fmt.Fprintln(stderr, "reploy-session-client client usage error: unexpected argument")
			printClientShortUsage(stderr)
			return 2
		}
		if err := runControllerBroker(ctx, controlledsession.ControllerBrokerOptionsV1{
			SessionSocket: os.Getenv("REPLOY_SESSION_SOCKET"),
			TemporaryHome: controlledsession.ControllerTemporaryHomeV1,
			Input:         input,
			Output:        output,
		}); err != nil {
			fmt.Fprintf(stderr, "reploy-session-client client error: %v\n", err)
			return 1
		}
		return 0
	}
	if len(args) >= 1 && args[0] == "attach" {
		if len(args) == 2 && isHelpArg(args[1]) {
			printAttachHelp(output)
			return 0
		}
		socket, err := parseAttachSocket(args[1:])
		if err != nil {
			fmt.Fprintf(stderr, "reploy-session-client attach usage error: %v\n", err)
			printAttachShortUsage(stderr)
			return 2
		}
		if err := runTerminalAttachment(ctx, controlledsession.TerminalAttachmentOptionsV1{
			SocketPath: socket,
			Input:      input,
			Output:     output,
		}); err != nil {
			fmt.Fprintf(stderr, "reploy-session-client attach error: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintln(stderr, "reploy-session-client usage error: expected client or attach")
	printShortUsage(stderr)
	return 2
}

func parseAttachSocket(args []string) (string, error) {
	// The socket is deliberately the only public option. The broker chooses
	// and reports it; the attachment does not discover or create a socket.
	if len(args) == 1 && strings.HasPrefix(args[0], "--socket=") {
		socket := strings.TrimPrefix(args[0], "--socket=")
		if socket == "" {
			return "", fmt.Errorf("--socket requires a value")
		}
		return socket, nil
	}
	if len(args) == 2 && args[0] == "--socket" && args[1] != "" {
		return args[1], nil
	}
	if len(args) == 1 && args[0] == "--socket" {
		return "", fmt.Errorf("--socket requires a value")
	}
	if len(args) == 0 {
		return "", fmt.Errorf("--socket is required")
	}
	return "", fmt.Errorf("expected exactly --socket PATH")
}

func isHelpArg(arg string) bool {
	return arg == "-h" || arg == "--help" || arg == "help"
}

func printShortUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: reploy-session-client {client | attach --socket PATH}")
}

func printHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy-session-client COMMAND

Commands:
  client                 Run the structured controller session broker
  attach --socket PATH   Attach terminal bytes to a running broker

Run 'reploy-session-client COMMAND --help' for command-specific help.
`, "\n"))
}

func printClientShortUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: reploy-session-client client")
}

func printClientHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy-session-client client

Run the controller-side controlled-session broker. The command consumes the
controller-private REPLOY_SESSION_SOCKET environment variable and exchanges
versioned JSON Lines with the controller orchestrator on stdin and stdout.
Human-readable diagnostics are written only to stderr.
`, "\n"))
}

func printAttachShortUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: reploy-session-client attach --socket PATH")
}

func printAttachHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy-session-client attach --socket PATH

Attach stdin, stdout, and terminal resize events to the private terminal socket
reported by 'reploy-session-client client'. Terminal bytes are forwarded
unchanged; human-readable diagnostics are written only to stderr.
`, "\n"))
}
