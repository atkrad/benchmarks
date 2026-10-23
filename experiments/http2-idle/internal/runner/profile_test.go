package runner

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummarizeProfile(t *testing.T) {
	tests := []struct {
		name    string
		profile string
		want    ProfileSummary
		wantErr string
	}{
		{
			name: "two read groups and one serve group",
			profile: `goroutine profile: total 9
2 @ 0x1
#	0x1	golang.org/x/net/http2.(*serverConn).readFrames+0x1

3 @ 0x2
#	0x2	net/http/internal/http2.(*serverConn).readFrames+0x2

4 @ 0x3
#	0x3	golang.org/x/net/http2.(*serverConn).serve+0x3
#	0x4	golang.org/x/net/http2.(*serverConn).serveLoop+0x4
`,
			want: ProfileSummary{Goroutines: 9, HTTP2Serve: 4, ReadFrames: 5},
		},
		{
			name: "similar names are not HTTP/2 serve or read",
			profile: `goroutine profile: total 3
3 @ 0x1
#	0x1	net/http.(*conn).serve+0x10
#	0x2	net/http/internal/http2.readFrameHeader+0x10
`,
			want: ProfileSummary{Goroutines: 3},
		},
		{
			name:    "group with no symbolized frames",
			profile: "goroutine profile: total 1\n1 @ 0x4920a1\n",
			want:    ProfileSummary{Goroutines: 1},
		},
		{
			name:    "CRLF line endings",
			profile: "goroutine profile: total 1\r\n1 @ 0x1\r\n#\t0x1\truntime.other+0x1\r\n",
			want:    ProfileSummary{Goroutines: 1},
		},
		{
			name:    "empty profile",
			profile: " \n\t",
			wantErr: "empty profile",
		},
		{
			name:    "debug=2 trace",
			profile: "goroutine 1 [running]:\nmain.main()\n",
			wantErr: "unsupported format",
		},
		{
			name:    "zero count",
			profile: "goroutine profile: total 1\n0 @ 0x1\n",
			wantErr: "malformed group header",
		},
		{
			name:    "header without addresses",
			profile: "goroutine profile: total 1\n1 @garbage\n",
			wantErr: "malformed group header",
		},
		{
			name:    "group count overflows int",
			profile: "goroutine profile: total 1\n9223372036854775808 @ 0x1\n",
			wantErr: "overflows int",
		},
		{
			name:    "total does not match groups",
			profile: "goroutine profile: total 10\n2 @ 0x1\n#\t0x1\truntime.other+0x1\n",
			wantErr: "groups sum to 2, profile total is 10",
		},
		{
			name:    "frame before a group",
			profile: "goroutine profile: total 1\n#\t0x1\truntime.other+0x1\n",
			wantErr: "stack frame before a group header",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SummarizeProfile(tt.profile)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestSummarizeProfileFixtures uses the debug=1 dumps from the 2026-10-21
// smoke run at 200 connections. They are the captured text, not a rewrite.
func TestSummarizeProfileFixtures(t *testing.T) {
	tests := []struct {
		file string
		want ProfileSummary
	}{
		{"go127-n200.txt", ProfileSummary{Goroutines: 407, HTTP2Serve: 200, ReadFrames: 200}},
		{"go128-n200.txt", ProfileSummary{Goroutines: 207, HTTP2Serve: 0, ReadFrames: 200}},
		{"go128off-n200.txt", ProfileSummary{Goroutines: 407, HTTP2Serve: 200, ReadFrames: 200}},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			body, err := os.ReadFile("testdata/profiles/" + tt.file)
			require.NoError(t, err)
			got, err := SummarizeProfile(string(body))
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
