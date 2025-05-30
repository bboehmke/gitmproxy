package main

import (
	"io"
	"net/http"
	"os"
)

// bodyWithFile wraps the http.Response.Body and the file, so that when closed both are closed.
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
