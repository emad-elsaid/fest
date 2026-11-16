package alpm

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVerCmp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		v1, v2 string
		want   int
	}{
		{"1.5.0", "1.5.0", 0},
		{"1.5.1", "1.5.0", 1},
		{"1.5.1", "1.5", 1},
		{"1.5.0-1", "1.5.0-1", 0},
		{"1.5.0-1", "1.5.0-2", -1},
		{"1.5.0-1", "1.5.1-1", -1},
		{"1.5.0-2", "1.5.1-1", -1},
		{"1.5-1", "1.5.1-1", -1},
		{"1.5-2", "1.5.1-1", -1},
		{"1.5-2", "1.5.1-2", -1},
		{"1.5", "1.5-1", 0},
		{"1.5-1", "1.5", 0},
		{"1.1-1", "1.1", 0},
		{"1.0-1", "1.1", -1},
		{"1.1-1", "1.0", 1},
		{"1.5b-1", "1.5-1", -1},
		{"1.5b", "1.5", -1},
		{"1.5b-1", "1.5", -1},
		{"1.5b", "1.5.1", -1},
		{"1.0a", "1.0alpha", -1},
		{"1.0alpha", "1.0b", -1},
		{"1.0b", "1.0beta", -1},
		{"1.0beta", "1.0rc", -1},
		{"1.0rc", "1.0", -1},
		{"1.5.a", "1.5", 1},
		{"1.5.b", "1.5.a", 1},
		{"1.5.1", "1.5.b", 1},
		{"1.5.b-1", "1.5.b", 0},
		{"1.5-1", "1.5.b", -1},
		{"2.0", "2_0", 0},
		{"2.0_a", "2_0.a", 0},
		{"2.0a", "2.0.a", -1},
		{"2___a", "2_a", 1},
		{"0:1.0", "0:1.0", 0},
		{"0:1.0", "0:1.1", -1},
		{"1:1.0", "0:1.0", 1},
		{"1:1.0", "0:1.1", 1},
		{"1:1.0", "2:1.1", -1},
		{"1:1.0", "0:1.0-1", 1},
		{"1:1.0-1", "0:1.1-1", 1},
		{"0:1.0", "1.0", 0},
		{"0:1.0", "1.1", -1},
		{"0:1.1", "1.0", 1},
		{"1:1.0", "1.0", 1},
		{"1:1.0", "1.1", 1},
		{"1:1.1", "1.1", 1},
	}

	for _, tt := range tests {
		t.Run(tt.v1+"_"+tt.v2, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, sign(VerCmp(tt.v1, tt.v2)), "%s vs %s", tt.v1, tt.v2)
			require.Equal(t, -tt.want, sign(VerCmp(tt.v2, tt.v1)), "%s vs %s", tt.v2, tt.v1)
		})
	}
}

func TestVerCmpParityWithPacman(t *testing.T) {
	binary, err := exec.LookPath("vercmp")
	if err != nil {
		t.Skip("vercmp not installed; skipping parity test")
	}

	versions := []string{
		"0", "0.1", "1", "1.0", "1.0.0", "1.0-1", "1.0-01", "1.0-2",
		"1.0a", "1.0alpha", "1.0b", "1.0beta", "1.0rc", "1.0.a", "1.0.a-1",
		"1.5", "1.5-1", "1.5.1-1", "1.5b-1", "1.5.b", "2", "2.0",
		"2_0", "2.0_a", "2___a", "0:1.0", "0:1.0-1", "1:1.0", "1:1.0-1",
		"1:1.1-1", "2:1.1", "10.0", "9.10", "20240101", "r1.abc123-2",
	}

	for _, a := range versions {
		for _, b := range versions {
			out, err := exec.Command(binary, a, b).Output()
			require.NoError(t, err)

			want, err := strconv.Atoi(strings.TrimSpace(string(out)))
			require.NoError(t, err)

			require.Equal(t, want, sign(VerCmp(a, b)), "%s vs %s", a, b)
		}
	}
}

func sign(v int) int {
	switch {
	case v < 0:
		return -1
	case v > 0:
		return 1
	default:
		return 0
	}
}
