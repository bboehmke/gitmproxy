package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
)

// bodyWithFile wraps the http.ResponseWriter.Body and the file, so that when closed both are closed.
type bodyWithFile struct {
	body io.ReadCloser
	file *os.File
}

// Read reads data from the body and returns it. It implements the io.ReadCloser interface.
func (b *bodyWithFile) Read(p []byte) (int, error) {
	return b.body.Read(p)
}

// Close closes both the body and the file. It implements the io.Closer interface.
func (b *bodyWithFile) Close() error {
	err1 := b.body.Close()
	err2 := b.file.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

// countingWriter is a writer that counts how many bytes have been written through it.
type countingWriter struct {
	w     io.Writer
	count int64
}

// Write writes data to the underlying writer and counts the number of bytes written.
func (cw *countingWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	cw.count += int64(n)
	return n, err
}

// writeResponseToTmpFile writes the response to a temporary file on disk and returns the bytes written.
func writeResponseToTmpFile(tmpPath string, resp *http.Response) (int64, error) {
	f, err := os.Create(tmpPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	cw := &countingWriter{w: f}
	if err := resp.Write(cw); err != nil {
		os.Remove(tmpPath)
		return 0, err
	}

	// Ensure data is flushed to disk before renaming
	if err := f.Sync(); err != nil {
		os.Remove(tmpPath)
		return 0, err
	}
	return cw.count, nil
}

// countingReadCloser wraps an io.ReadCloser and counts bytes read.
type countingReadCloser struct {
	rc    io.ReadCloser
	isHit bool // true if cache hit, false if miss
}

// Read reads data from the underlying ReadCloser and counts the number of bytes read.
func (c *countingReadCloser) Read(p []byte) (int, error) {
	n, err := c.rc.Read(p)
	if n > 0 {
		mCacheRequestsBytes.Add(float64(n))
		if c.isHit {
			mCacheRequestsHitBytes.Add(float64(n))
		} else {
			mCacheRequestsMissBytes.Add(float64(n))
		}
	}
	return n, err
}

// Close closes the underlying ReadCloser.
func (c *countingReadCloser) Close() error {
	return c.rc.Close()
}

// ResponseWriter is a custom http.ResponseWriter that captures the response headers and body.
type ResponseWriter struct {
	header http.Header
	buffer bytes.Buffer
	status int
}

func NewResponseWriter() *ResponseWriter {
	return &ResponseWriter{
		header: make(http.Header),
		status: http.StatusOK, // Default status code
	}
}

func (r *ResponseWriter) Header() http.Header {
	return r.header
}

func (r *ResponseWriter) Write(bytes []byte) (int, error) {
	return r.buffer.Write(bytes)
}

func (r *ResponseWriter) WriteHeader(statusCode int) {
	r.status = statusCode
}

// Response returns an http.Response constructed from the captured data in the ResponseWriter.
func (r *ResponseWriter) Response(req *http.Request) *http.Response {
	resp := &http.Response{
		StatusCode: r.status,
		Status:     fmt.Sprintf("%d %s", r.status, http.StatusText(r.status)),
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     r.header,
		Body:       io.NopCloser(&r.buffer),
		Request:    req,
	}
	return resp
}
