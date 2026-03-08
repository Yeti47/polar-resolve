package logging

import "fmt"

// Logger is the interface used by internal packages for diagnostic output.
type Logger interface {
	// Infof logs an informational message (verbose/debug output).
	Infof(format string, args ...any)
	// Warnf logs a warning (always shown in console mode).
	Warnf(format string, args ...any)
}

// Nop returns a logger that discards all output.
func Nop() Logger { return nopLogger{} }

type nopLogger struct{}

func (nopLogger) Infof(string, ...any) {}
func (nopLogger) Warnf(string, ...any) {}

// Console returns a logger that prints to stdout.
// If verbose is false, Infof messages are suppressed.
func Console(verbose bool) Logger {
	return &consoleLogger{verbose: verbose}
}

type consoleLogger struct{ verbose bool }

func (l *consoleLogger) Infof(format string, args ...any) {
	if l.verbose {
		fmt.Printf("[polar-resolve] "+format+"\n", args...)
	}
}

func (l *consoleLogger) Warnf(format string, args ...any) {
	fmt.Printf("[polar-resolve] WARNING: "+format+"\n", args...)
}
