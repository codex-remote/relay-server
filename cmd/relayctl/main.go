package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ai-coding-remote/relay-server/internal/protocol"
	"github.com/coder/websocket"
)

const defaultAppURL = "ws://127.0.0.1:8080/ws/app"

func main() {
	os.Exit(realMain(os.Args[1:]))
}

func realMain(arguments []string) int {
	if len(arguments) == 0 {
		printUsage()
		return 2
	}
	switch arguments[0] {
	case "run":
		return run(arguments[1:])
	case "watch":
		return watch(arguments[1:])
	case "help", "--help", "-h":
		printUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", arguments[0])
		printUsage()
		return 2
	}
}

func run(arguments []string) int {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	url := flags.String("url", envOrDefault("RELAY_APP_URL", defaultAppURL), "Relay App WebSocket URL")
	prompt := flags.String("prompt", "", "development instruction")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if *prompt == "" && flags.NArg() > 0 {
		*prompt = strings.Join(flags.Args(), " ")
	}
	if strings.TrimSpace(*prompt) == "" {
		fmt.Fprintln(os.Stderr, "prompt is required")
		return 2
	}
	connection, _, err := websocket.Dial(context.Background(), *url, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect to Relay:", err)
		return 1
	}
	defer connection.Close(websocket.StatusNormalClosure, "run finished")
	connection.SetReadLimit(256 * 1024)

	runID := protocol.NewID()
	start, err := protocol.NewMessage(protocol.TypeRunStart, runID, protocol.Sender{Kind: "user", ID: "relayctl"}, protocol.RunStartPayload{RunID: runID, Prompt: *prompt})
	if err != nil {
		fmt.Fprintln(os.Stderr, "send run.start failed:", err)
		return 1
	}
	if err := writeMessage(context.Background(), connection, start); err != nil {
		fmt.Fprintln(os.Stderr, "send run.start failed:", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "[relayctl] submitted run %s\n", runID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	incoming := readMessages(ctx, connection)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	for {
		select {
		case received := <-incoming:
			if received.err != nil {
				fmt.Fprintln(os.Stderr, "Relay connection ended:", received.err)
				return 1
			}
			terminal, exitCode := printRunEvent(received.message, runID)
			if terminal {
				return exitCode
			}
		case <-signals:
			cancelMessage, _ := protocol.NewMessage(protocol.TypeRunCancel, runID, protocol.Sender{Kind: "user", ID: "relayctl"}, protocol.RunCancelPayload{RunID: runID})
			writeContext, writeCancel := context.WithTimeout(context.Background(), 3*time.Second)
			err := writeMessage(writeContext, connection, cancelMessage)
			writeCancel()
			if err != nil {
				fmt.Fprintln(os.Stderr, "send run.cancel failed:", err)
				return 1
			}
			fmt.Fprintln(os.Stderr, "[relayctl] cancellation requested")
			signal.Stop(signals)
		}
	}
}

func watch(arguments []string) int {
	flags := flag.NewFlagSet("watch", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	url := flags.String("url", envOrDefault("RELAY_APP_URL", defaultAppURL), "Relay App WebSocket URL")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	connection, _, err := websocket.Dial(ctx, *url, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect to Relay:", err)
		return 1
	}
	defer connection.Close(websocket.StatusNormalClosure, "watch finished")
	connection.SetReadLimit(256 * 1024)
	for {
		_, data, err := connection.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return 0
			}
			fmt.Fprintln(os.Stderr, "Relay connection ended:", err)
			return 1
		}
		var pretty any
		if json.Unmarshal(data, &pretty) == nil {
			formatted, _ := json.MarshalIndent(pretty, "", "  ")
			fmt.Println(string(formatted))
		}
	}
}

type receivedMessage struct {
	message protocol.Message
	err     error
}

func readMessages(ctx context.Context, connection *websocket.Conn) <-chan receivedMessage {
	messages := make(chan receivedMessage, 1)
	go func() {
		defer close(messages)
		for {
			_, data, err := connection.Read(ctx)
			if err != nil {
				messages <- receivedMessage{err: err}
				return
			}
			message, err := protocol.Decode(data)
			if err != nil {
				continue
			}
			messages <- receivedMessage{message: message}
		}
	}()
	return messages
}

func writeMessage(ctx context.Context, connection *websocket.Conn, message protocol.Message) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return connection.Write(ctx, websocket.MessageText, data)
}

func printRunEvent(message protocol.Message, runID string) (bool, int) {
	if message.TraceID != runID && message.Type != protocol.TypeAgentStatus && message.Type != protocol.TypeAgentHello {
		return false, 0
	}
	switch message.Type {
	case protocol.TypeAgentStatus:
		payload, _ := protocol.PayloadAs[protocol.AgentStatusPayload](message)
		fmt.Fprintf(os.Stderr, "[relayctl] Agent status: %s\n", payload.Status)
	case protocol.TypeRunStarted:
		fmt.Fprintln(os.Stderr, "[relayctl] Codex started")
	case protocol.TypeRunOutput:
		payload, err := protocol.PayloadAs[protocol.RunOutputPayload](message)
		if err == nil {
			if payload.Stream == "stderr" {
				fmt.Fprint(os.Stderr, payload.Text)
			} else {
				fmt.Fprint(os.Stdout, payload.Text)
			}
		}
	case protocol.TypeRunCompleted:
		payload, err := protocol.PayloadAs[protocol.RunCompletedPayload](message)
		if err != nil {
			return true, 1
		}
		fmt.Fprintf(os.Stderr, "\n[relayctl] completed in %s; changed files: %d\n", time.Duration(payload.DurationMS)*time.Millisecond, len(payload.ChangedFiles))
		if payload.Summary != "" {
			fmt.Fprintf(os.Stdout, "\nFinal summary:\n%s\n", payload.Summary)
		}
		if len(payload.ChangedFiles) > 0 {
			fmt.Fprintf(os.Stdout, "\nChanged files:\n- %s\n", strings.Join(payload.ChangedFiles, "\n- "))
		}
		if payload.Diff != "" {
			fmt.Fprintf(os.Stdout, "\nGit diff:\n%s\n", payload.Diff)
		}
		return true, 0
	case protocol.TypeRunFailed, protocol.TypeRunRejected:
		fmt.Fprintf(os.Stderr, "\n[relayctl] %s: %s\n", message.Type, string(message.Payload))
		return true, 1
	case protocol.TypeRunCancelled:
		fmt.Fprintln(os.Stderr, "\n[relayctl] run cancelled")
		return true, 130
	}
	return false, 0
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `AI Coding Remote Relay control client

Usage:
  relayctl run --url ws://HOST:8080/ws/app --prompt TEXT
  relayctl watch --url ws://HOST:8080/ws/app

Commands:
  run    Submit one Run and stream its output
  watch  Print every event as formatted JSON`)
}
