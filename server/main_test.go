// Copyright 2026 Dolthub, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeS3 serves a single object at /bucket/key, supporting ranged GETs. It
// can be told to cut off the response body partway through for requests
// starting at a given offset, the way a connection reset during a download
// does.
type fakeS3 struct {
	data []byte

	mu sync.Mutex
	// truncate[start] is the number of remaining requests starting at
	// offset |start| whose response bodies will be cut off.
	truncate map[int]int
	requests int
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests++
	f.mu.Unlock()

	if r.URL.Path != "/bucket/key" {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchKey</Code><Message>The specified key does not exist.</Message></Error>`)
		return
	}

	start, end := 0, len(f.data)-1
	if rng := r.Header.Get("Range"); rng != "" {
		if _, err := fmt.Sscanf(rng, "bytes=%d-%d", &start, &end); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		end = min(end, len(f.data)-1)
	}
	body := f.data[start : end+1]

	f.mu.Lock()
	cut := f.truncate[start] > 0
	if cut {
		f.truncate[start]--
	}
	f.mu.Unlock()

	headers := fmt.Sprintf("Content-Length: %d\r\nContent-Range: bytes %d-%d/%d\r\nETag: \"etag\"\r\n", len(body), start, end, len(f.data))
	if !cut {
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(f.data)))
		w.Header().Set("ETag", `"etag"`)
		w.WriteHeader(http.StatusPartialContent)
		w.Write(body)
		return
	}

	// Promise the whole body, send half of it and hang up.
	conn, buf, err := w.(http.Hijacker).Hijack()
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	fmt.Fprintf(buf, "HTTP/1.1 206 Partial Content\r\n%s\r\n", headers)
	buf.Write(body[:len(body)/2])
	buf.Flush()
}

func newTestS3Downloader(t *testing.T, f *fakeS3) *S3Downloader {
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	client := s3.New(s3.Options{
		Region:       "us-west-2",
		BaseEndpoint: aws.String(srv.URL),
		UsePathStyle: true,
		Credentials:  aws.AnonymousCredentials{},
	})
	return newS3Downloader(client, "bucket", func(d *manager.Downloader) {
		d.PartSize = 1024
	})
}

func testData() []byte {
	data := make([]byte, 10*1024+17)
	for i := range data {
		data[i] = byte(rand.N(256))
	}
	return data
}

func TestS3DownloaderRetriesTruncatedParts(t *testing.T) {
	data := testData()
	f := &fakeS3{
		data: data,
		// The first part, which the downloader fetches alone to learn
		// the object's size, a part in the middle and the short last
		// part each get cut off a couple of times.
		truncate: map[int]int{0: 2, 3 * 1024: 2, 10 * 1024: 1},
	}
	d := newTestS3Downloader(t, f)

	dest := filepath.Join(t.TempDir(), "out")
	require.NoError(t, d.Download(context.Background(), dest, "key"))
	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, data, got)
	assert.Equal(t, 11+5, f.requests)
}

func TestS3DownloaderGivesUp(t *testing.T) {
	f := &fakeS3{data: testData(), truncate: map[int]int{2 * 1024: 100}}
	d := newTestS3Downloader(t, f)

	err := d.Download(context.Background(), filepath.Join(t.TempDir(), "out"), "key")
	assert.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func TestS3DownloaderMissingKey(t *testing.T) {
	f := &fakeS3{data: testData()}
	d := newTestS3Downloader(t, f)

	err := d.Download(context.Background(), filepath.Join(t.TempDir(), "out"), "missing")
	assert.ErrorContains(t, err, "NoSuchKey")
	assert.Equal(t, 1, f.requests)
}
