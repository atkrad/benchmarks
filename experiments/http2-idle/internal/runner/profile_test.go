package runner

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummarizeProfileGrouped(t *testing.T) {
	tests := []struct {
		name    string
		profile string
		want    ProfileSummary
		wantErr string
	}{
		{
			name: "Go 1.27",
			profile: `goroutine profile: total 407
200 @ 0x111 0x222
#	0x111	net/http/internal/http2.(*serverConn).readFrames+0x8b

200 @ 0x333 0x444
#	0x333	net/http/internal/http2.(*serverConn).serve+0x916

7 @ 0x555
#	0x555	runtime.otherFunction+0x10
`,
			want: ProfileSummary{Goroutines: 407, HTTP2Serve: 200, ReadFrames: 200},
		},
		{
			name: "Go 1.28 parking enabled",
			profile: `goroutine profile: total 207
200 @ 0x111
#	0x111	net/http/internal/http2.(*serverConn).readFrames+0x8b

7 @ 0x222
#	0x222	runtime.otherFunction+0x10
`,
			want: ProfileSummary{Goroutines: 207, HTTP2Serve: 0, ReadFrames: 200},
		},
		{
			name: "Go 1.28 parking disabled",
			profile: `goroutine profile: total 407
200 @ 0x111
#	0x111	net/http/internal/http2.(*serverConn).readFrames+0x8b

200 @ 0x222
#	0x222	net/http/internal/http2.(*serverConn).serveLoop+0x14d
#	0x333	net/http/internal/http2.(*serverConn).serve+0x664

7 @ 0x444
#	0x444	runtime.otherFunction+0x10
`,
			want: ProfileSummary{Goroutines: 407, HTTP2Serve: 200, ReadFrames: 200},
		},
		{
			name: "x/net symbols and two read groups",
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
			name: "unrelated serve function",
			profile: `goroutine profile: total 3
3 @ 0x1
#	0x1	net/http.(*conn).serve+0x10
#	0x2	net/http/internal/http2.readFrameHeader+0x10
`,
			want: ProfileSummary{Goroutines: 3},
		},
		{
			name:    "group immediately after header",
			profile: "goroutine profile: total 2\n2 @ 0x1\n#\t0x1\truntime.other+0x1\n",
			want:    ProfileSummary{Goroutines: 2},
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
			name:    "malformed group header",
			profile: "goroutine profile: total 1\n0 @ 0x1\n",
			wantErr: "malformed line",
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

func TestSummarizeProfileSmokeFixtures(t *testing.T) {
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
