package terminal

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"golang.org/x/term"
)

// Session represents a local terminal connected to a remote terminal.
type Session struct {
	client *Client
	termID int
	done   chan struct{}
	once   sync.Once
}

// RunSession creates (or attaches to) a remote terminal and bridges it
// with the local terminal stdin/stdout until the remote process exits or
// the user presses Ctrl+C.
func RunSession(client *Client, cwd string) error {
	// Put local terminal in raw mode
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("set raw mode: %w", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	cols, rows, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		cols, rows = 80, 24 // fallback
	}

	// Create remote terminal
	id, err := client.CreateProcess(cwd, cols, rows)
	if err != nil {
		return fmt.Errorf("create terminal: %w", err)
	}

	s := &Session{
		client: client,
		termID: id,
		done:   make(chan struct{}),
	}

	// Subscribe to output before starting
	unsubOutput, err := client.SubscribeOutput(func(termID int, data string) {
		if termID == id {
			os.Stdout.WriteString(data)
		}
	})
	if err != nil {
		return fmt.Errorf("subscribe output: %w", err)
	}
	defer unsubOutput()

	// Subscribe to exit
	unsubExit, err := client.SubscribeExit(func(termID int, code int) {
		if termID == id {
			s.once.Do(func() { close(s.done) })
		}
	})
	if err != nil {
		return fmt.Errorf("subscribe exit: %w", err)
	}
	defer unsubExit()

	// Start the terminal process
	if err := client.Start(id); err != nil {
		return fmt.Errorf("start terminal: %w", err)
	}

	// Handle SIGWINCH (terminal resize)
	sigwinch := make(chan os.Signal, 1)
	signal.Notify(sigwinch, syscall.SIGWINCH)
	defer signal.Stop(sigwinch)
	go func() {
		for {
			select {
			case <-sigwinch:
				if c, r, err := term.GetSize(int(os.Stdin.Fd())); err == nil {
					client.Resize(id, c, r)
				}
			case <-s.done:
				return
			}
		}
	}()

	// Forward stdin → remote
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				if sendErr := client.Input(id, string(buf[:n])); sendErr != nil {
					s.once.Do(func() { close(s.done) })
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					s.once.Do(func() { close(s.done) })
				}
				return
			}
		}
	}()

	// Handle Ctrl+C
	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigint)

	select {
	case <-s.done:
		fmt.Println("\r\nRemote terminal exited.")
	case <-sigint:
		fmt.Println("\r\nDisconnecting...")
		client.Shutdown(id, true)
	}

	return nil
}
