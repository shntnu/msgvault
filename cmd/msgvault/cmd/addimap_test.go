package cmd

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestPasswordPromptStrategy(t *testing.T) {
	tests := []struct {
		name       string
		stdinNat   bool // stdin is a native terminal
		stdinCyg   bool // stdin is a Cygwin/MSYS PTY
		stderrTTY  bool
		stdoutTTY  bool
		wantMethod passwordMethod
		wantOutput *os.File // nil for pipe/error methods
	}{
		{
			name:       "normal interactive terminal",
			stdinNat:   true,
			stderrTTY:  true,
			stdoutTTY:  true,
			wantMethod: passwordInteractive,
			wantOutput: os.Stderr,
		},
		{
			name:       "stdout redirected",
			stdinNat:   true,
			stderrTTY:  true,
			stdoutTTY:  false,
			wantMethod: passwordInteractive,
			wantOutput: os.Stderr,
		},
		{
			name:       "stderr redirected",
			stdinNat:   true,
			stderrTTY:  false,
			stdoutTTY:  true,
			wantMethod: passwordInteractive,
			wantOutput: os.Stdout,
		},
		{
			name:       "both outputs redirected, native stdin",
			stdinNat:   true,
			stderrTTY:  false,
			stdoutTTY:  false,
			wantMethod: passwordNoPrompt,
		},
		{
			name:       "cygwin normal terminal",
			stdinCyg:   true,
			stderrTTY:  true,
			stdoutTTY:  true,
			wantMethod: passwordInteractive,
			wantOutput: os.Stderr,
		},
		{
			name:       "cygwin stdout redirected",
			stdinCyg:   true,
			stderrTTY:  true,
			stdoutTTY:  false,
			wantMethod: passwordInteractive,
			wantOutput: os.Stderr,
		},
		{
			name:       "cygwin stderr redirected",
			stdinCyg:   true,
			stderrTTY:  false,
			stdoutTTY:  true,
			wantMethod: passwordInteractive,
			wantOutput: os.Stdout,
		},
		{
			name:       "cygwin both outputs redirected",
			stdinCyg:   true,
			stderrTTY:  false,
			stdoutTTY:  false,
			wantMethod: passwordNoPrompt,
		},
		{
			name:       "piped stdin",
			stdinNat:   false,
			stdinCyg:   false,
			stderrTTY:  true,
			stdoutTTY:  true,
			wantMethod: passwordPipe,
		},
		{
			name:       "piped stdin, all redirected",
			stdinNat:   false,
			stdinCyg:   false,
			stderrTTY:  false,
			stdoutTTY:  false,
			wantMethod: passwordPipe,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			method, output := choosePasswordStrategy(
				tt.stdinNat, tt.stdinCyg, tt.stderrTTY, tt.stdoutTTY,
			)
			if method != tt.wantMethod {
				t.Errorf("method = %v, want %v", method, tt.wantMethod)
			}
			if output != tt.wantOutput {
				t.Errorf("output = %v, want %v", output, tt.wantOutput)
			}
		})
	}
}

func TestReadPasswordFromPipe(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{
			name:  "reads password from pipe",
			input: "secret123\n",
			want:  "secret123",
		},
		{
			name:  "trims trailing newline",
			input: "mypassword\n",
			want:  "mypassword",
		},
		{
			name:  "trims trailing CRLF",
			input: "mypassword\r\n",
			want:  "mypassword",
		},
		{
			name:  "handles no trailing newline",
			input: "mypassword",
			want:  "mypassword",
		},
		{
			name:    "rejects empty input",
			input:   "\n",
			wantErr: "password is required",
		},
		{
			name:    "rejects whitespace-only input",
			input:   "  \n",
			wantErr: "password is required",
		},
		{
			name:    "rejects EOF with no data",
			input:   "",
			wantErr: "password is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := strings.NewReader(tt.input)
			got, err := readPasswordFromPipe(r)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReadPasswordFromPipeLargeInput(t *testing.T) {
	// Only first line should be used as the password.
	input := "firstline\nsecondline\n"
	r := strings.NewReader(input)
	got, err := readPasswordFromPipe(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "firstline" {
		t.Errorf("got %q, want %q", got, "firstline")
	}
}

// Verify the function signature accepts io.Reader.
var _ func(io.Reader) (string, error) = readPasswordFromPipe
