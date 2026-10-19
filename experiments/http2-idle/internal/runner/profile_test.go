package runner

import "testing"

func TestSummarizeProfileFixture(t *testing.T) {
	text := `goroutine 1 [running]:
main.main()
	/tmp/main.go:1

goroutine 8 [IO wait]:
net/http.(*conn).serve(0xc0)
	/usr/local/go/src/net/http/server.go:1
golang.org/x/net/http2.(*serverConn).serve(0xc1)
	/tmp/server.go:10

goroutine 9 [IO wait]:
golang.org/x/net/http2.(*serverConn).readFrames(0xc1)
	/tmp/server.go:20
`
	sum := SummarizeProfile(text)
	if sum.Goroutines != 3 {
		t.Fatalf("goroutines = %d", sum.Goroutines)
	}
	if sum.HTTP2Serve != 1 || sum.ReadFrames != 1 {
		t.Fatalf("summary = %+v", sum)
	}
}

func TestSummarizeProfileIgnoresUnknownSymbols(t *testing.T) {
	sum := SummarizeProfile("goroutine 1 [running]:\nmain.main()\n")
	if sum.Goroutines != 1 || sum.HTTP2Serve != 0 {
		t.Fatalf("summary = %+v", sum)
	}
}
