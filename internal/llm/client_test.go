package llm

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUsagePresenceAndInvalidCounters(t *testing.T) {
	for _, v := range []struct {
		usage   string
		known   bool
		in, out int
	}{{"", false, 0, 0}, {`,"usage":null`, false, 0, 0}, {`,"usage":{}`, false, 0, 0}, {`,"usage":{"prompt_tokens":10}`, false, 10, 0}, {`,"usage":{"prompt_tokens":-1,"completion_tokens":5}`, false, 0, 5}, {`,"usage":{"prompt_tokens":0,"completion_tokens":0}`, true, 0, 0}, {`,"usage":{"prompt_tokens":10,"completion_tokens":5}`, true, 10, 5}} {
		c := New("https://test.invalid", "", 0)
		c.HTTP.Transport = testTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]` + v.usage + `}`)), Request: r}, nil
		})
		resp, e := c.Chat(context.Background(), Request{Model: "m"})
		if e != nil || resp.UsageKnown != v.known || resp.TokensIn != v.in || resp.TokensOut != v.out {
			t.Fatalf("%s: %+v %v", v.usage, resp, e)
		}
	}
}
