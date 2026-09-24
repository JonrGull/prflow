package github

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/JonrGull/prflow/internal/run"
)

// The Actions screen polls every repo every few seconds, which spent the
// REST rate limit (5,000 an hour) in about 15 minutes, after which every call,
// PR creation included, failed until it reset. cachedGet sends each poll as a
// conditional request: GitHub answers 304 Not Modified for a resource that
// has not changed, and a 304 does not count against the limit.

var (
	etagMu    sync.Mutex
	etagCache = map[string]etagEntry{} // by API path
)

type etagEntry struct {
	etag string
	body []byte
}

// cachedGet fetches an API path, returning the body from the last fetch when
// GitHub says it has not changed. Only for paths that repeat: every distinct
// path keeps its body.
func cachedGet(path string) ([]byte, error) {
	etagMu.Lock()
	cached := etagCache[path]
	etagMu.Unlock()

	args := []string{"api", "-i", path}
	if cached.etag != "" {
		args = append(args, "-H", "If-None-Match: "+cached.etag)
	}
	// gh exits 1 on a 304, so the status line decides, not the exit code.
	out, err := run.Output(run.Network, "", "gh", args...)
	status, header, body := splitHTTPResponse(out)
	switch {
	case status == http.StatusNotModified && cached.body != nil:
		return cached.body, nil
	case status != http.StatusOK:
		return nil, errors.New(ghFailure(err, status, body))
	}
	if etag := header.Get("Etag"); etag != "" {
		etagMu.Lock()
		etagCache[path] = etagEntry{etag: etag, body: body}
		etagMu.Unlock()
	}
	return body, nil
}

// splitHTTPResponse splits gh api -i output into its status code, headers and
// body. gh ends the status line with \n and the headers with \r\n.
func splitHTTPResponse(out []byte) (int, http.Header, []byte) {
	head, body, ok := bytes.Cut(out, []byte("\r\n\r\n"))
	if !ok {
		head, body, _ = bytes.Cut(out, []byte("\n\n"))
	}
	header := http.Header{}
	lines := strings.Split(string(head), "\n")
	fields := strings.Fields(lines[0])
	if len(fields) < 2 || !strings.HasPrefix(fields[0], "HTTP/") {
		return 0, header, nil
	}
	status, _ := strconv.Atoi(fields[1])
	for _, line := range lines[1:] {
		if name, value, ok := strings.Cut(strings.TrimRight(line, "\r"), ": "); ok {
			header.Add(name, value)
		}
	}
	return status, header, body
}

// ghFailure says why a request failed: gh's own message when it printed one.
func ghFailure(err error, status int, body []byte) string {
	var exit *exec.ExitError
	if errors.As(err, &exit) && len(bytes.TrimSpace(exit.Stderr)) > 0 {
		return strings.TrimSpace(string(exit.Stderr))
	}
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("HTTP %d: %s", status, strings.TrimSpace(string(body)))
}
