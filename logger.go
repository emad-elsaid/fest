package fest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/fatih/color"
)

// levelSuccess is a custom log level for success messages.
const levelSuccess = slog.Level(2)

// levelEmoji maps log levels to their display emoji/icons.
var levelEmoji = map[slog.Level]string{
	slog.LevelDebug: "·",
	slog.LevelInfo:  blue("::"),
	levelSuccess:    green("✓"),
	slog.LevelWarn:  "!",
	slog.LevelError: red("✗"),
}

// logSuccess logs a message at the success level.
func logSuccess(msg string, args ...any) {
	slog.Log(context.Background(), levelSuccess, msg, args...)
}

// fatal logs an error message and exits the program with status code 1.
func fatal(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}

// checkFatal logs a fatal error and exits if err is non-nil.
func checkFatal(err error, msg string) {
	if err != nil {
		fatal(msg, "error", err)
	}
}

// checkWarn logs a warning if err is non-nil.
func checkWarn(err error, msg string) {
	if err != nil {
		slog.Warn(msg, "error", err)
	}
}

// logIf logs a message at the given level if the condition is true.
func logIf(cond bool, level slog.Level, msg string, args ...any) {
	if cond {
		slog.Log(context.Background(), level, msg, args...)
	}
}

// logInfoIf logs an info message if the condition is true.
func logInfoIf(cond bool, msg string, args ...any) {
	logIf(cond, slog.LevelInfo, msg, args...)
}

// Color helpers for terminal output.
var (
	red   = color.New(color.FgRed).SprintFunc()
	green = color.New(color.FgGreen).SprintFunc()
	blue  = color.New(color.FgBlue).SprintFunc()
)

// prettyHandler implements a custom slog.Handler for formatted console output.
type prettyHandler struct {
	w      io.Writer
	attrs  []slog.Attr
	groups []string
}

// newPrettyHandler creates a new pretty handler that writes to the given writer.
func newPrettyHandler(w io.Writer) slog.Handler {
	if w == nil {
		panic("writer cannot be nil")
	}
	return &prettyHandler{w: w}
}

// Enabled returns true for all log levels.
func (h *prettyHandler) Enabled(_ context.Context, level slog.Level) bool { return true }

// WithGroup creates a new handler with the given group name.
func (h *prettyHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	groups := make([]string, len(h.groups)+1)
	copy(groups, h.groups)
	groups[len(h.groups)] = name
	return &prettyHandler{w: h.w, attrs: h.attrs, groups: groups}
}

// Handle formats and writes log records to the output writer.
func (h *prettyHandler) Handle(_ context.Context, r slog.Record) error {
	var buf strings.Builder
	var detailedBuf strings.Builder

	emoji, ok := levelEmoji[r.Level]
	if !ok {
		emoji = "❓"
	}
	fmt.Fprintf(&buf, "%s %s", emoji, r.Message)

	prefix := ""
	if len(h.groups) > 0 {
		prefix = strings.Join(h.groups, ".") + "."
	}

	processAttr := func(a slog.Attr, keyPrefix string) {
		key := keyPrefix + a.Key

		value := a.Value.Any()
		if a.Key == "count" {
			fmt.Fprintf(&buf, " (%v)", value)
		} else if items, ok := value.([]string); ok && len(items) > 0 {
			for _, item := range items {
				escaped := strings.ReplaceAll(item, "\n", "\\n")
				fmt.Fprintf(&detailedBuf, "   • %s\n", escaped)
			}
		} else {
			fmt.Fprintf(&buf, " [%s=%v]", key, value)
		}
	}

	// h.attrs are already qualified with the groups active when they were added.
	for _, attr := range h.attrs {
		processAttr(attr, "")
	}
	// Record attributes are qualified by all of the handler's current groups.
	r.Attrs(func(a slog.Attr) bool {
		processAttr(a, prefix)
		return true
	})

	buf.WriteString("\n")
	buf.WriteString(detailedBuf.String())

	_, err := h.w.Write([]byte(buf.String()))
	return err
}

// WithAttrs returns a new handler with the given attributes prepended.
// Attributes are qualified with the groups active at the time they are added,
// per the slog.Handler contract, and the returned handler never shares storage
// with the receiver.
func (h *prettyHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	prefix := ""
	if len(h.groups) > 0 {
		prefix = strings.Join(h.groups, ".") + "."
	}
	qualified := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	qualified = append(qualified, h.attrs...)
	for _, a := range attrs {
		a.Key = prefix + a.Key
		qualified = append(qualified, a)
	}
	return &prettyHandler{w: h.w, attrs: qualified, groups: h.groups}
}
