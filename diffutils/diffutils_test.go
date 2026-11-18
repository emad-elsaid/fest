package diffutils

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUnified(t *testing.T) {
	tests := []struct {
		name     string
		from     string
		to       string
		expected string
	}{
		{
			name:     "identical files produce no output",
			from:     "same\n",
			to:       "same\n",
			expected: "",
		},
		{
			name: "single line replacement",
			from: "a\n",
			to:   "b\n",
			expected: `@@ -1 +1 @@
-a
+b
`,
		},
		{
			name: "replacement with context",
			from: "one\ntwo\nthree\n",
			to:   "one\nTWO\nthree\n",
			expected: `@@ -1,3 +1,3 @@
 one
-two
+TWO
 three
`,
		},
		{
			name: "insertion",
			from: "one\ntwo\nthree\n",
			to:   "one\ninserted\ntwo\nthree\n",
			expected: `@@ -1,3 +1,4 @@
 one
+inserted
 two
 three
`,
		},
		{
			name: "deletion",
			from: "one\ntwo\nthree\n",
			to:   "one\nthree\n",
			expected: `@@ -1,3 +1,2 @@
 one
-two
 three
`,
		},
		{
			name: "missing trailing newline",
			from: "a\nb",
			to:   "a\nb\n",
			expected: `@@ -1,2 +1,2 @@
 a
-b
\ No newline at end of file
+b
`,
		},
		{
			name: "removing a single empty line",
			from: "\n",
			to:   "",
			expected: `@@ -1 +0,0 @@
-
`,
		},
		{
			name: "adding a single empty line",
			from: "",
			to:   "\n",
			expected: `@@ -0,0 +1 @@
+
`,
		},
		{
			name: "insertion into empty file",
			from: "",
			to:   "x\ny\n",
			expected: `@@ -0,0 +1,2 @@
+x
+y
`,
		},
		{
			name: "separate hunks",
			from: numbered(20, map[int]string{2: "two", 18: "eighteen"}),
			to:   numbered(20, nil),
			expected: `@@ -1,5 +1,5 @@
 1
-two
+2
 3
 4
 5
@@ -15,6 +15,6 @@
 15
 16
 17
-eighteen
+18
 19
 20
`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var buffer bytes.Buffer

			err := Unified(
				&buffer,
				File{Name: "old"},
				File{Name: "new"},
				[]byte(test.from),
				[]byte(test.to),
			)
			require.NoError(t, err)

			expected := test.expected
			if expected != "" {
				expected = "--- old\n+++ new\n" + expected
			}

			require.Equal(t, expected, buffer.String())
		})
	}
}

func TestUnifiedTrimsCommonPrefixAndSuffix(t *testing.T) {
	var buffer bytes.Buffer

	err := Unified(
		&buffer,
		File{Name: "old"},
		File{Name: "new"},
		[]byte(numbered(5000, map[int]string{2500: "old"})),
		[]byte(numbered(5000, map[int]string{2500: "new"})),
	)
	require.NoError(t, err)

	require.Equal(t, `--- old
+++ new
@@ -2497,7 +2497,7 @@
 2497
 2498
 2499
-old
+new
 2501
 2502
 2503
`, buffer.String())
}

func TestUnifiedSparseLargeEdits(t *testing.T) {
	var buffer bytes.Buffer

	err := Unified(
		&buffer,
		File{Name: "old"},
		File{Name: "new"},
		[]byte(numbered(5000, map[int]string{2: "old-two", 4999: "old-end"})),
		[]byte(numbered(5000, map[int]string{4999: "new-end"})),
	)
	require.NoError(t, err)

	// Edits far apart must stay minimal instead of falling back to replacing
	// the whole span between them.
	require.Contains(t, buffer.String(), "@@ -1,5 +1,5 @@")
	require.Contains(t, buffer.String(), "@@ -4996,5 +4996,5 @@")
	require.Less(t, buffer.Len(), 1000)
}

func TestUnifiedBinary(t *testing.T) {
	var buffer bytes.Buffer

	err := Unified(
		&buffer,
		File{Name: "old"},
		File{Name: "new"},
		[]byte{0x00, 0x01},
		[]byte{0x00, 0x02},
	)
	require.NoError(t, err)
	require.Equal(t, "Binary files old and new differ\n", buffer.String())
}

func TestUnifiedTimestamps(t *testing.T) {
	var buffer bytes.Buffer
	stamp := time.Date(2026, time.January, 2, 3, 4, 5, 6, time.UTC)

	err := Unified(
		&buffer,
		File{Name: "old", Time: stamp},
		File{Name: "new", Time: stamp},
		[]byte("a\n"),
		[]byte("b\n"),
	)
	require.NoError(t, err)

	require.Equal(t,
		"--- old\t2026-01-02 03:04:05.000000006 +0000\n"+
			"+++ new\t2026-01-02 03:04:05.000000006 +0000\n"+
			"@@ -1 +1 @@\n-a\n+b\n",
		buffer.String())
}

func TestFiles(t *testing.T) {
	dir := t.TempDir()
	from := filepath.Join(dir, "from")
	to := filepath.Join(dir, "to")

	require.NoError(t, os.WriteFile(from, []byte("a\n"), 0o644))
	require.NoError(t, os.WriteFile(to, []byte("b\n"), 0o644))

	var buffer bytes.Buffer
	require.NoError(t, Files(&buffer, from, to))

	require.Contains(t, buffer.String(), "--- "+from+"\t")
	require.Contains(t, buffer.String(), "+++ "+to+"\t")
	require.Contains(t, buffer.String(), "@@ -1 +1 @@\n-a\n+b\n")
}

func TestFilesRejectsNonRegular(t *testing.T) {
	dir := t.TempDir()
	regular := filepath.Join(dir, "regular")
	require.NoError(t, os.WriteFile(regular, []byte("x\n"), 0o644))

	fifo := filepath.Join(dir, "fifo")
	require.NoError(t, syscall.Mkfifo(fifo, 0o600))

	var buffer bytes.Buffer

	require.Error(t, Files(&buffer, fifo, regular), "a FIFO must be rejected without blocking")
	require.Error(t, Files(&buffer, regular, dir), "a directory must be rejected")

	if _, err := os.Stat("/dev/zero"); err == nil {
		require.Error(t, Files(&buffer, "/dev/zero", regular), "a device must be rejected")
	}
}

// numbered builds n lines of "1".."n", replacing lines named in changes.
func numbered(n int, changes map[int]string) string {
	var buffer bytes.Buffer

	for line := 1; line <= n; line++ {
		if replacement, ok := changes[line]; ok {
			buffer.WriteString(replacement)
		} else {
			buffer.WriteString(strconv.Itoa(line))
		}
		buffer.WriteByte('\n')
	}

	return buffer.String()
}
