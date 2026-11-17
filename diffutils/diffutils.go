// Package diffutils implements the subset of GNU diff that fest uses: unified
// diffs of two files. It replaces the external diff binary.
package diffutils

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// contextLines is the number of unchanged lines shown around each change,
// matching diff's default of 3.
const contextLines = 3

// noNewline is appended to the final line of a file that has no trailing
// newline so the diff algorithm treats it as different from a newline-terminated
// line. It is stripped before the line is written.
const noNewline = "\x00"

// File describes one side of a diff.
type File struct {
	Name string
	Time time.Time
}

type opKind int

const (
	opEqual opKind = iota
	opDelete
	opInsert
)

type op struct {
	kind opKind
	text string
}

// Files writes a unified diff of fromPath and toPath to w. Unlike diff, it
// returns an error if either file cannot be read.
func Files(w io.Writer, fromPath, toPath string) error {
	fromData, err := os.ReadFile(fromPath)
	if err != nil {
		return err
	}

	toData, err := os.ReadFile(toPath)
	if err != nil {
		return err
	}

	from := File{Name: fromPath}
	if info, err := os.Stat(fromPath); err == nil {
		from.Time = info.ModTime()
	}

	to := File{Name: toPath}
	if info, err := os.Stat(toPath); err == nil {
		to.Time = info.ModTime()
	}

	return Unified(w, from, to, fromData, toData)
}

// Unified writes a unified diff of fromData and toData to w, labelling the
// sides with the names (and timestamps, when set) in from and to. Identical
// inputs produce no output.
func Unified(w io.Writer, from, to File, fromData, toData []byte) error {
	if bytes.Equal(fromData, toData) {
		return nil
	}

	if bytes.IndexByte(fromData, 0) >= 0 || bytes.IndexByte(toData, 0) >= 0 {
		_, err := fmt.Fprintf(w, "Binary files %s and %s differ\n", from.Name, to.Name)
		return err
	}

	operations := editScript(splitLines(fromData), splitLines(toData))
	changes := changeIndexes(operations)
	if len(changes) == 0 {
		return nil
	}

	if _, err := fmt.Fprintf(w, "--- %s\n+++ %s\n", header(from), header(to)); err != nil {
		return err
	}

	return writeHunks(w, operations, changes)
}

// header formats a diff file header line body.
func header(file File) string {
	if file.Time.IsZero() {
		return file.Name
	}

	return file.Name + "\t" + file.Time.Format("2006-01-02 15:04:05.000000000 -0700")
}

// splitLines splits data into lines, preserving the distinction between a file
// that ends with a newline and one that does not.
func splitLines(data []byte) []string {
	var lines []string

	for line := range strings.Lines(string(data)) {
		lines = append(lines, strings.TrimSuffix(line, "\n"))
	}

	if len(lines) > 0 && data[len(data)-1] != '\n' {
		lines[len(lines)-1] += noNewline
	}

	return lines
}

// changeIndexes returns the indexes of operations that add or remove a line.
func changeIndexes(operations []op) []int {
	var indexes []int

	for index, operation := range operations {
		if operation.kind != opEqual {
			indexes = append(indexes, index)
		}
	}

	return indexes
}

// writeHunks groups changes into hunks with surrounding context and writes
// them. It annotates operations with their line numbers as it goes.
func writeHunks(w io.Writer, operations []op, changes []int) error {
	oldLine, newLine := 1, 1
	oldLines := make([]int, len(operations))
	newLines := make([]int, len(operations))

	for index, operation := range operations {
		oldLines[index] = oldLine
		newLines[index] = newLine

		switch operation.kind {
		case opEqual:
			oldLine++
			newLine++
		case opDelete:
			oldLine++
		case opInsert:
			newLine++
		}
	}

	for first := 0; first < len(changes); {
		hunkFirst := changes[first]
		last := hunkFirst
		first++

		for first < len(changes) && changes[first]-last <= 2*contextLines+1 {
			last = changes[first]
			first++
		}

		start := max(0, hunkFirst-contextLines)
		end := min(len(operations), last+contextLines+1)

		if err := writeHunk(w, operations[start:end], oldLines, newLines, start); err != nil {
			return err
		}
	}

	return nil
}

// writeHunk writes a single hunk.
func writeHunk(w io.Writer, operations []op, oldLines, newLines []int, start int) error {
	oldCount, newCount := 0, 0
	oldStart := oldLines[start]
	newStart := newLines[start]

	for _, operation := range operations {
		switch operation.kind {
		case opEqual:
			oldCount++
			newCount++
		case opDelete:
			oldCount++
		case opInsert:
			newCount++
		}
	}

	if oldCount == 0 {
		oldStart--
	}
	if newCount == 0 {
		newStart--
	}

	if _, err := fmt.Fprintf(w,
		"@@ -%s +%s @@\n",
		lineRange(oldStart, oldCount),
		lineRange(newStart, newCount),
	); err != nil {
		return err
	}

	for _, operation := range operations {
		prefix := byte(' ')
		switch operation.kind {
		case opDelete:
			prefix = '-'
		case opInsert:
			prefix = '+'
		}

		text, missingNewline := strings.CutSuffix(operation.text, noNewline)

		if _, err := fmt.Fprintf(w, "%c%s\n", prefix, text); err != nil {
			return err
		}

		if missingNewline {
			if _, err := io.WriteString(w, "\\ No newline at end of file\n"); err != nil {
				return err
			}
		}
	}

	return nil
}

// lineRange formats a hunk range, omitting the count when it is one to match
// GNU diff's output.
func lineRange(start, count int) string {
	if count == 1 {
		return fmt.Sprintf("%d", start)
	}

	return fmt.Sprintf("%d,%d", start, count)
}

// maxEditDistance bounds Myers' trace size. Larger differences fall back to a
// full replacement, which is still a valid diff and keeps memory bounded.
const maxEditDistance = 2000

// editScript computes a shortest edit script between a and b, keeping the
// unchanged prefix and suffix out of the Myers computation so ordinary edits in
// large files stay cheap.
func editScript(a, b []string) []op {
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}

	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}

	operations := make([]op, 0, len(a)+len(b))

	for _, line := range a[:prefix] {
		operations = append(operations, op{kind: opEqual, text: line})
	}

	operations = append(operations, myers(a[prefix:len(a)-suffix], b[prefix:len(b)-suffix])...)

	for _, line := range a[len(a)-suffix:] {
		operations = append(operations, op{kind: opEqual, text: line})
	}

	return operations
}

// myers computes a shortest edit script using Myers' algorithm.
func myers(a, b []string) []op {
	n, m := len(a), len(b)
	maxD := n + m
	if maxD > maxEditDistance {
		return replaceAll(a, b)
	}

	offset := maxD + 1
	v := make([]int, 2*maxD+3)
	trace := make([][]int, 0, maxD+1)

	for d := 0; d <= maxD; d++ {
		// Snapshot only k in [-d-1, d+1]; the outermost entries stay zero.
		snapshot := make([]int, 2*d+3)
		for k := -d; k <= d; k++ {
			snapshot[k+d+1] = v[k+offset]
		}
		trace = append(trace, snapshot)

		for k := -d; k <= d; k += 2 {
			var x int

			if k == -d || (k != d && v[k-1+offset] < v[k+1+offset]) {
				x = v[k+1+offset]
			} else {
				x = v[k-1+offset] + 1
			}

			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}

			v[k+offset] = x

			if x >= n && y >= m {
				return backtrack(trace, a, b)
			}
		}
	}

	return replaceAll(a, b)
}

// replaceAll returns a script that removes every line of a and adds every line
// of b.
func replaceAll(a, b []string) []op {
	operations := make([]op, 0, len(a)+len(b))

	for _, line := range a {
		operations = append(operations, op{kind: opDelete, text: line})
	}
	for _, line := range b {
		operations = append(operations, op{kind: opInsert, text: line})
	}

	return operations
}

// backtrack reconstructs the edit script from Myers' trace.
func backtrack(trace [][]int, a, b []string) []op {
	x, y := len(a), len(b)

	var reversed []op

	for d := len(trace) - 1; d >= 0; d-- {
		v := trace[d]
		at := func(k int) int {
			index := k + d + 1
			if index < 0 || index >= len(v) {
				return 0
			}
			return v[index]
		}

		k := x - y

		var prevK int

		if k == -d || (k != d && at(k-1) < at(k+1)) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}

		prevX := at(prevK)
		prevY := prevX - prevK

		for x > prevX && y > prevY {
			reversed = append(reversed, op{kind: opEqual, text: a[x-1]})
			x--
			y--
		}

		if d == 0 {
			continue
		}

		if x == prevX {
			reversed = append(reversed, op{kind: opInsert, text: b[y-1]})
			y--
		} else {
			reversed = append(reversed, op{kind: opDelete, text: a[x-1]})
			x--
		}
	}

	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}

	return reversed
}
